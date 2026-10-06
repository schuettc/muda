package measure

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/schuettc/muda/internal/gh"
	"github.com/schuettc/muda/internal/signal"
	"github.com/schuettc/muda/internal/workflow"
)

// treeProvenEvents check out the run's head commit, so head_commit.tree_id is
// the tree the job tested. A pull_request run checks out the merge ref
// (pull_request_target the base), and workflow_run or repository_dispatch
// check out the default branch: head_commit's tree is not proven there.
var treeProvenEvents = map[string]bool{"push": true, "merge_group": true, "workflow_dispatch": true, "schedule": true}

// bookkeeping reports steps GitHub adds around a job's own work.
func bookkeeping(name string) bool {
	return name == "Set up job" || name == "Complete job" || strings.HasPrefix(name, "Post ")
}

type checkExec struct {
	run gh.Run
	job gh.Job
}

func (e checkExec) origin() string { return e.run.Path + " (" + e.run.Event + ")" }

type w6Group struct {
	tree, branch string
	steps        int
	execs        []checkExec
}

// w6Candidates groups successful, fully timed jobs by tree, branch and the
// ordered names of the steps they executed. Names only nominate candidates;
// they never establish that two jobs are the same check. A group needs at
// least two origins (workflow and event).
func w6Candidates(runs []RunData) []w6Group {
	type key struct{ tree, branch, fingerprint string }
	groups := map[key][]checkExec{}
	for _, rd := range runs {
		r := rd.Run
		if r.HeadCommit.TreeID == "" {
			continue
		}
		timed, _ := timedJobs(rd)
		for _, j := range timed {
			if j.Conclusion != "success" || deployName.MatchString(j.Name) {
				continue
			}
			var steps []string
			prod := false
			for _, s := range j.Steps {
				if !stepExecuted(s) || bookkeeping(s.Name) {
					continue
				}
				if deployName.MatchString(s.Name) {
					prod = true
				}
				steps = append(steps, s.Name)
			}
			if prod || len(steps) < MinMirroredCheckSteps {
				continue
			}
			k := key{r.HeadCommit.TreeID, r.HeadBranch, strings.Join(steps, "\x00")}
			groups[k] = append(groups[k], checkExec{run: r, job: j})
		}
	}
	var out []w6Group
	for k, execs := range groups {
		origins := map[string]bool{}
		for _, e := range execs {
			origins[e.origin()] = true
		}
		if len(origins) < 2 {
			continue
		}
		sort.Slice(execs, func(i, j int) bool {
			a, b := execs[i].job, execs[j].job
			if !a.StartedAt.Equal(b.StartedAt) {
				return a.StartedAt.Before(b.StartedAt)
			}
			return a.ID < b.ID
		})
		out = append(out, w6Group{tree: k.tree, branch: k.branch, steps: strings.Count(k.fingerprint, "\x00") + 1, execs: execs})
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.tree != b.tree {
			return a.tree < b.tree
		}
		if a.branch != b.branch {
			return a.branch < b.branch
		}
		return a.execs[0].job.ID < b.execs[0].job.ID
	})
	return out
}

// w6Runs are the runs whose own definitions tree-rechecked must read.
func w6Runs(runs []RunData) []gh.Run {
	var out []gh.Run
	for _, g := range w6Candidates(runs) {
		for _, e := range g.execs {
			if treeProvenEvents[e.run.Event] {
				out = append(out, e.run)
			}
		}
	}
	return out
}

// jobIdentity is what a job executes, beyond its step names: the workflow's
// env and defaults, and the job's runner, container, services, env, defaults
// and every step's action, command, inputs, env, shell, directory and guard.
type jobIdentity struct {
	Env         map[string]string `json:"env"`
	Defaults    string            `json:"defaults"`
	RunsOn      string            `json:"runs_on"`
	Container   string            `json:"container"`
	Services    string            `json:"services"`
	JobEnv      map[string]string `json:"job_env"`
	JobDefaults string            `json:"job_defaults"`
	Steps       []stepIdentity    `json:"steps"`
}

type stepIdentity struct {
	Uses, Run, If, Shell, WorkingDirectory string
	With, Env                              map[string]string
}

func identity(f *workflow.File, j workflow.Job) string {
	id := jobIdentity{Env: f.Env, Defaults: f.Defaults, RunsOn: j.RunsOn, Container: j.Container, Services: j.Services,
		JobEnv: j.Env, JobDefaults: j.Defaults, Steps: []stepIdentity{}}
	for _, s := range j.Steps {
		id.Steps = append(id.Steps, stepIdentity{Uses: s.Uses, Run: s.Run, If: s.If, Shell: s.Shell, WorkingDirectory: s.WorkingDirectory, With: s.With, Env: s.Env})
	}
	b, _ := json.Marshal(id) // map keys marshal sorted: deterministic
	return string(b)
}

// resolve finds the job's definition in its own run's workflow file and says
// why it cannot stand as a check identity when it cannot.
func resolve(e checkExec, refs map[string]Definition) (def Definition, job workflow.Job, why string) {
	if !treeProvenEvents[e.run.Event] {
		return def, job, fmt.Sprintf("%s runs do not check out head_commit's tree (a merge, base or default-branch ref), so the tree tested is unproven", e.run.Event)
	}
	def, ok := refs[RefKey(e.run.Path, e.run.HeadSHA)]
	switch {
	case !ok:
		return def, job, fmt.Sprintf("workflow file %s at %s was not read", e.run.Path, e.run.HeadSHA)
	case def.File == nil:
		return def, job, def.Unavailable
	}
	var hits []string
	for _, id := range def.File.JobOrder {
		if strings.Contains(def.File.Jobs[id].Name, "${{") {
			continue
		}
		if matches(def.File.Jobs[id], e.job.Name) {
			hits = append(hits, id)
		}
	}
	if len(hits) != 1 {
		return def, job, fmt.Sprintf("job %q matches %d jobs in %s at %s", e.job.Name, len(hits), e.run.Path, e.run.HeadSHA)
	}
	job = def.File.Jobs[hits[0]]
	switch {
	case job.Matrix:
		return def, job, "matrix job: which matrix values this execution ran is unknown"
	case job.Uses != "":
		return def, job, "reusable-workflow job: the called definition is not compared"
	case job.Environment != "":
		return def, job, fmt.Sprintf("job uses environment %s: deployment-specific, not a mirrored check", job.Environment)
	}
	for _, s := range job.Steps {
		if strings.HasPrefix(s.Uses, "actions/checkout@") && (s.With["ref"] != "" || s.With["repository"] != "") {
			return def, job, "checkout step selects another ref or repository, so the tree tested is unproven"
		}
	}
	return def, job, ""
}

// treeRechecked reports the same check executed more than once on one tree
// and branch, across workflows or events. Step names only nominate
// candidates. Each execution's job is resolved in its own run's workflow file
// at its head commit, and only executions whose job definitions are identical
// count. One successful execution per origin is counted, so only other
// origins' executions are repeats. Anything that cannot be established is a
// named Unavailable, never a signal or a saving.
func treeRechecked(runs []RunData, refs map[string]Definition) ([]signal.Signal, []Unavailable) {
	var sigs []signal.Signal
	var unavailable []Unavailable
	for _, g := range w6Candidates(runs) {
		what := fmt.Sprintf("tree-rechecked on tree %s (%s)", short(g.tree), g.branch)
		type proven struct {
			exec checkExec
			def  Definition
			job  workflow.Job
		}
		byIdentity := map[string][]proven{}
		var unknown []signal.Evidence
		for _, e := range g.execs {
			def, job, why := resolve(e, refs)
			var id string
			if why == "" {
				id = identity(def.File, job)
				// Identical text is not an identical check when it holds an
				// expression: `${{ github.event_name … }}` or `${{ inputs.* }}`
				// can do different work per origin. No allowlist, no evaluation.
				if strings.Contains(id, "${{") {
					why = "definition uses ${{ }} expressions in compared fields (env, defaults, runs-on, container, services, or a step's uses/run/with/env/shell/working-directory/if) that may evaluate differently per origin, so identical text does not prove the same check"
				}
			}
			if why != "" {
				ev := jobEvidence(e.job)
				ev.Note = fmt.Sprintf("job %q in %s: %s", e.job.Name, e.origin(), why)
				unknown = append(unknown, ev)
				continue
			}
			byIdentity[id] = append(byIdentity[id], proven{e, def, job})
		}
		emitted := false
		for _, id := range sortedKeys(byIdentity) {
			execs := byIdentity[id]
			perRun := map[int64]int{}
			firstPerOrigin := map[string]bool{}
			var kept []proven
			ambiguous := false
			for _, p := range execs {
				perRun[p.exec.run.ID]++
				if perRun[p.exec.run.ID] > 1 {
					ambiguous = true
				}
				if !firstPerOrigin[p.exec.origin()] {
					firstPerOrigin[p.exec.origin()] = true
					kept = append(kept, p)
				}
			}
			if len(kept) < 2 {
				continue
			}
			if ambiguous {
				var ev []signal.Evidence
				for _, p := range execs {
					e := jobEvidence(p.exec.job)
					e.Note = fmt.Sprintf("job %q in %s", p.exec.job.Name, p.exec.origin())
					ev = append(ev, e)
				}
				unavailable = append(unavailable, Unavailable{What: what,
					Why: "one run executed the same check definition in more than one job; the mirror match is ambiguous, so no duplicate is claimed", Evidence: ev})
				emitted = true
				continue
			}
			var ev []signal.Evidence
			var repeat time.Duration
			files := map[string]bool{}
			var fileEv []signal.Evidence
			for i, p := range kept {
				d := p.exec.job.CompletedAt.Sub(p.exec.job.StartedAt)
				je := jobEvidence(p.exec.job)
				role := "first execution"
				if i > 0 {
					role = "repeat"
					repeat += d
				}
				je.Note = fmt.Sprintf("%s: job %q in %s, success, %s", role, p.exec.job.Name, p.exec.origin(), d)
				ev = append(ev, je)
				if u := p.def.URL; u != "" {
					u = fmt.Sprintf("%s#L%d", u, p.job.Line)
					if !files[u] {
						files[u] = true
						fileEv = append(fileEv, signal.Evidence{Kind: signal.KindFile, URL: u, Path: p.def.Path, Line: p.job.Line,
							Note: "job definition at " + p.def.Ref + ", identical to the others"})
					}
				}
			}
			origins := make([]string, 0, len(kept))
			for _, p := range kept {
				origins = append(origins, p.exec.origin())
			}
			sigs = append(sigs, signal.New("tree-rechecked", signal.SeverityWarn,
				fmt.Sprintf("tree %s on %s: the same job definition (%d executed steps; identical at each run's commit) ran %d times across %s; the %d repeats took %s",
					short(g.tree), g.branch, g.steps, len(kept), strings.Join(origins, ", "), len(kept)-1, repeat), append(ev, fileEv...)))
			emitted = true
		}
		// Reported even beside a signal: the signal covers proven executions
		// only, and these may be further repeats nobody can yet prove.
		if len(unknown) > 0 {
			why := fmt.Sprintf("jobs with the same executed step names ran across origins, but %d executions' check identity could not be established; they are not claimed as duplicates", len(unknown))
			if emitted {
				why += " and are not counted in the signal's repeat time"
			}
			unavailable = append(unavailable, Unavailable{What: what, Why: why, Evidence: unknown})
		}
	}
	return sigs, unavailable
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
