// Package measure reports where a repository's delivery time goes: per
// workflow, job and step durations, queue time, re-runs, success rate and the
// critical path, plus the cross-run signals only run data shows. Every number
// carries the run, job or step it was measured from.
package measure

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/schuettc/muda/internal/gh"
	"github.com/schuettc/muda/internal/signal"
	"github.com/schuettc/muda/internal/workflow"
)

// Schema is the JSON contract version.
const Schema = 1

// fetchWorkers bounds concurrent API calls.
const fetchWorkers = 8

// Report is `muda measure`'s output. Durations are integer nanoseconds in
// JSON (fields ending _ns).
type Report struct {
	Schema      int             `json:"schema"`
	Repo        string          `json:"repo"`
	Since       time.Time       `json:"since"`
	Until       time.Time       `json:"until"`
	Workflows   []WorkflowStats `json:"workflows"`
	Sinks       []Sink          `json:"sinks"`
	Signals     []signal.Signal `json:"signals"`
	Unavailable []Unavailable   `json:"unavailable"`
}

// WorkflowStats aggregates one workflow's completed runs in the window. Only
// each run's final attempt is timed; earlier attempts are counted as re-runs
// and cited.
type WorkflowStats struct {
	Path       string   `json:"path"`
	Name       string   `json:"name"`
	Role       string   `json:"role"`        // a candidate, for the user to confirm
	RoleReason string   `json:"role_reason"` // the rule that matched, or that none did
	Events     []string `json:"events"`      // trigger events observed in the window
	Runs       int      `json:"runs"`
	Confidence string   `json:"confidence"` // Confidence(Runs): informs, never hides a number
	Reruns     int      `json:"reruns"`     // runs whose final attempt is > 1
	Failures   int      `json:"failures"`   // final conclusion failure, timed_out or startup_failure
	// P50 and P90 time the final attempt: its start to the completion of its
	// last timed job. A run with no timed job ends at updated_at, which is an
	// estimate (GitHub may touch a run later); EstimatedEnds counts those.
	P50           time.Duration `json:"p50_ns"`
	P90           time.Duration `json:"p90_ns"`
	EstimatedEnds int           `json:"runs_end_estimated"`
	// QueueP50 and QueueP90 are over every timed job: created to started.
	// GitHub creates a job when its needs resolve, so this is runner wait.
	QueueP50    time.Duration `json:"queue_p50_ns"`
	QueueP90    time.Duration `json:"queue_p90_ns"`
	SuccessRate float64       `json:"success_rate"`
	// CarriedOverJobs counts jobs listed under a final attempt but not run in
	// it (a partial re-run carries earlier results forward). They are cited,
	// never timed.
	CarriedOverJobs int                `json:"carried_over_jobs"`
	Jobs            []JobStats         `json:"jobs"`
	CriticalPath    []string           `json:"critical_path"` // workflow job ids, first to last
	Critical        CriticalPathDetail `json:"critical_path_detail"`
	// Definitions is which workflow file version each run used. A report
	// written before it existed has none (nil): not recorded, never "no
	// change".
	Definitions *DefinitionHistory `json:"definition_history,omitempty"`
	// Evidence cites every counted run, every superseded attempt, every job
	// not timed, and the percentile samples.
	Evidence []signal.Evidence `json:"evidence"`
}

// JobStats aggregates one job name's timed executions; skipped jobs are not
// timed.
type JobStats struct {
	Name          string            `json:"name"`
	Runs          int               `json:"runs"`
	Confidence    string            `json:"confidence"`
	P50           time.Duration     `json:"p50_ns"`
	P90           time.Duration     `json:"p90_ns"`
	QueueP50      time.Duration     `json:"queue_p50_ns"`
	QueueP90      time.Duration     `json:"queue_p90_ns"`
	Steps         []StepStats       `json:"steps"`
	Evidence      []signal.Evidence `json:"evidence"`       // duration samples
	QueueEvidence []signal.Evidence `json:"queue_evidence"` // queue samples
}

// StepStats aggregates one step within a job. A job can run several steps
// with one name ("Run make"); Occurrence tells them apart (1 is the first),
// so Runs counts runs, not executions.
type StepStats struct {
	Name       string            `json:"name"`
	Occurrence int               `json:"occurrence"`
	Runs       int               `json:"runs"`
	Confidence string            `json:"confidence"`
	P50        time.Duration     `json:"p50_ns"`
	P90        time.Duration     `json:"p90_ns"`
	Evidence   []signal.Evidence `json:"evidence"`
}

// CriticalPathDetail says where the critical path came from. The jobs API has
// no `needs` edges, so they are read from one workflow file: at the head
// commit of the latest default-branch run (else the latest non-pull-request
// run, else the latest run), named by Ref, Branch, Event and SourceRunID. They
// are applied only to runs whose job list matches that definition and whose
// timings do not contradict its edges (BasisRuns). Runs whose timings do
// contradict are Rejected and cited.
type CriticalPathDetail struct {
	Ref           string            `json:"ref,omitempty"`
	Branch        string            `json:"branch,omitempty"`
	Event         string            `json:"event,omitempty"`
	SourceRunID   int64             `json:"source_run_id,omitempty"`
	Definition    string            `json:"definition_url,omitempty"`
	BasisRuns     int               `json:"basis_runs"`
	UnmatchedRuns int               `json:"unmatched_runs"`
	Rejected      []signal.Evidence `json:"rejected"`
	P50           time.Duration     `json:"p50_ns"` // sum of the path's node p50s
	Nodes         []PathNode        `json:"nodes"`
	Unavailable   string            `json:"unavailable,omitempty"`
}

// PathNode is one workflow job on the critical path. A matrix or reusable job
// is timed as the span of the API jobs it expanded to.
type PathNode struct {
	Job      string            `json:"job"`
	Kind     string            `json:"kind"` // job, matrix or reusable
	Runs     int               `json:"runs"`
	P50      time.Duration     `json:"p50_ns"`
	Evidence []signal.Evidence `json:"evidence"`
}

// Sink is a ranked time sink: a step weighted by p50 × runs.
type Sink struct {
	Workflow   string            `json:"workflow"`
	Job        string            `json:"job"`
	Step       string            `json:"step"`
	Occurrence int               `json:"occurrence"`
	Runs       int               `json:"runs"`
	Confidence string            `json:"confidence"`
	P50        time.Duration     `json:"p50_ns"`
	P90        time.Duration     `json:"p90_ns"`
	Weight     time.Duration     `json:"weight_ns"`
	Evidence   []signal.Evidence `json:"evidence"`
}

// Unavailable names something measure could not read or decide, and why.
type Unavailable struct {
	What     string            `json:"what"`
	Why      string            `json:"why"`
	Evidence []signal.Evidence `json:"evidence"`
}

// RunData is one run's fetched data: jobs of the final attempt only, and the
// attempt list when the run was re-run.
type RunData struct {
	Run      gh.Run
	Attempts []gh.Attempt
	Jobs     []gh.Job
}

// DefinitionHistory is which version of its workflow file each run in the
// window used: the file at the run's head commit, compared by content
// (SHA-256) in run order (created, then id).
//
// Changes are placed on one line of history: the default branch's push,
// schedule and workflow_dispatch runs (BranchRuns, of which RunsRead were
// read). Changes lists every such run whose file differs from the previous
// read one's. A default-branch run whose file could not be read is cited in
// Unread, and Unavailable says a change among them cannot be ruled out: an
// unread run is never assumed unchanged.
//
// Every other run (other branches, tags, pull requests) is not part of that
// line. A version it ran that no default-branch run did is a BranchOnly
// version, never a window change. Other runs whose file was not read are
// cited in OtherUnread: a fork's commit, a read failure, or an event that
// runs another branch's file (pull_request_target, workflow_run,
// repository_dispatch).
type DefinitionHistory struct {
	Branch      string             `json:"branch"`
	BranchRuns  int                `json:"branch_runs"`
	RunsRead    int                `json:"runs_read"`
	Versions    int                `json:"versions"` // distinct contents among the runs read
	Changes     []DefinitionChange `json:"changes"`
	Unread      []signal.Evidence  `json:"unread"`
	Unavailable string             `json:"unavailable,omitempty"`
	BranchOnly  []BranchVersion    `json:"branch_only_versions"`
	OtherUnread []signal.Evidence  `json:"other_unread"`
}

// BranchVersion is a workflow file version that only runs off the default
// branch used. It is Approximate when a pull_request run used it: that run
// is read at the pull request's head commit, but GitHub ran the merge ref's
// file, which also carries base-branch changes.
type BranchVersion struct {
	SHA256            string            `json:"sha256"`
	Runs              int               `json:"runs"`
	Branches          []string          `json:"branches"`
	Events            []string          `json:"events"`
	FirstRunID        int64             `json:"first_run_id"`
	FirstCreatedAt    time.Time         `json:"first_created_at"`
	FirstHeadSHA      string            `json:"first_head_sha"`
	Approximate       bool              `json:"approximate"`
	ApproximateReason string            `json:"approximate_reason,omitempty"`
	Evidence          []signal.Evidence `json:"evidence"`
}

// DefinitionChange is the first default-branch run on a new workflow file
// version. RunsBefore counts the workflow's runs (every branch) created
// before it and RunsAfter the runs from it on, so the two sum to the
// workflow's runs: RunsAfter is the sample a window starting at CreatedAt
// keeps. UnreadBetween counts unread default-branch runs between it and the
// previous version's last run: the change may have come with any of them. Jobs are workflow job ids (the keys
// under jobs:); JobsUnavailable says why they could not be compared.
type DefinitionChange struct {
	RunID           int64             `json:"run_id"`
	RunURL          string            `json:"run_url"`
	CreatedAt       time.Time         `json:"created_at"`
	HeadSHA         string            `json:"head_sha"`
	Branch          string            `json:"branch"`
	Event           string            `json:"event"`
	From            string            `json:"from_sha256"`
	To              string            `json:"to_sha256"`
	RunsBefore      int               `json:"runs_before"`
	RunsAfter       int               `json:"runs_after"`
	UnreadBetween   int               `json:"unread_between"`
	JobsAdded       []string          `json:"jobs_added"`
	JobsRemoved     []string          `json:"jobs_removed"`
	JobsUnavailable string            `json:"jobs_unavailable,omitempty"`
	Evidence        []signal.Evidence `json:"evidence"`
}

// Definition is a workflow file read at Ref (a run's head commit), or why it
// could not be. Branch, Event and RunID name the run it was read for. SHA256
// is the file's content hash, set whenever its bytes were read, even if they
// do not parse (then File is nil and Unavailable says why).
type Definition struct {
	Path, Ref, URL string
	Branch, Event  string
	RunID          int64
	SHA256         string
	File           *workflow.File
	Unavailable    string
}

// Input is everything Analyze needs; Run fetches it from GitHub.
type Input struct {
	Repo          string
	Since, Until  time.Time
	DefaultBranch string
	Runs          []RunData
	// Definitions is each workflow's critical-path definition, by path.
	Definitions map[string]Definition
	// RefDefinitions are workflow files at run commits, keyed by
	// RefKey(path, sha): every run's own, so the definition history sees
	// each version and tree-rechecked compares each run's own definition.
	RefDefinitions map[string]Definition
	// Names are the workflows' names from the workflow list, by path. A run's
	// name can be a per-run title (GitHub-managed workflows), so it is only
	// the fallback.
	Names       map[string]string
	Unavailable []Unavailable
}

// RefKey keys a workflow file at a commit.
func RefKey(path, sha string) string { return path + "@" + sha }

var pullRequestEvents = map[string]bool{"pull_request": true, "pull_request_target": true}

// Run fetches the completed runs created in [since, until), their
// final-attempt jobs, the attempts of re-run runs and the workflow definitions
// analysis needs, then analyzes them. The listing is gh.RunsWindow's: live,
// bounded and complete or an error. Any API failure is returned as an error
// naming the endpoint.
func Run(ctx context.Context, c *gh.Client, repo string, since, until time.Time) (*Report, error) {
	runs, err := c.RunsWindow(ctx, repo, since, until)
	if err != nil {
		return nil, err
	}
	return RunSelected(ctx, c, repo, runs, since, until)
}

// RunSelected reuses measure's final-attempt fetching and analysis for an
// already selected run set, keeping runs created in [since, until). Zero bounds
// select explicit IDs regardless of age.
func RunSelected(ctx context.Context, c *gh.Client, repo string, runs []gh.Run, since, until time.Time) (*Report, error) {
	branch, err := c.DefaultBranch(ctx, repo)
	if err != nil {
		return nil, err
	}
	in := Input{Repo: repo, Since: since, Until: until, DefaultBranch: branch, Definitions: map[string]Definition{},
		RefDefinitions: map[string]Definition{}, Names: map[string]string{}}
	flows, err := c.Workflows(ctx, repo)
	if err != nil {
		return nil, err
	}
	for _, f := range flows {
		in.Names[f.Path] = f.Name
	}
	var pending []signal.Evidence
	var kept []gh.Run
	for _, r := range runs {
		if (!since.IsZero() && r.CreatedAt.Before(since)) || (!until.IsZero() && !r.CreatedAt.Before(until)) {
			continue
		}
		if r.Status != "completed" {
			pending = append(pending, signal.Evidence{Kind: signal.KindRun, URL: r.HTMLURL, RunID: r.ID, Path: r.Path, Note: "status " + r.Status})
			continue
		}
		kept = append(kept, r)
	}
	if len(pending) > 0 {
		sortEvidence(pending)
		in.Unavailable = append(in.Unavailable, Unavailable{What: "runs not yet completed",
			Why: fmt.Sprintf("%d runs were still in progress; they are not timed or cached", len(pending)), Evidence: pending})
	}

	data := make([]RunData, len(kept))
	err = parallel(ctx, len(kept), func(i int) error {
		r := kept[i]
		if r.RunAttempt < 1 {
			return fmt.Errorf("run %d has no run_attempt", r.ID)
		}
		d := RunData{Run: r}
		if r.RunAttempt > 1 {
			attempts, err := c.RunAttempts(ctx, repo, r)
			if err != nil {
				return err
			}
			for n, a := range attempts {
				if a.RunAttempt != n+1 {
					return fmt.Errorf("run %d: attempt %d reported as %d", r.ID, n+1, a.RunAttempt)
				}
			}
			if len(attempts) != r.RunAttempt {
				return fmt.Errorf("run %d: %d attempts listed, run_attempt is %d", r.ID, len(attempts), r.RunAttempt)
			}
			d.Attempts = attempts
		}
		jobs, err := c.Jobs(ctx, repo, r.ID, r.RunAttempt)
		if err != nil {
			return err
		}
		d.Jobs = jobs
		data[i] = d
		return nil
	})
	if err != nil {
		return nil, err
	}
	in.Runs = data

	// Definitions to read: every run's own workflow file, for the definition
	// history and tree-rechecked; one of them per workflow supplies the
	// critical path. Each (path, commit) is read once, and cached: the commit
	// is the run's head_sha as the API reported it.
	want := map[string]gh.Run{}
	sources := map[string]string{}
	byPath := map[string][]gh.Run{}
	for _, r := range kept {
		byPath[r.Path] = append(byPath[r.Path], r)
	}
	for path, rs := range byPath {
		src := definitionSource(rs, branch)
		key := RefKey(path, src.HeadSHA)
		sources[path] = key
		want[key] = src
	}
	for _, r := range kept {
		if otherFileEvents[r.Event] != "" {
			continue // its head commit's file is not the one that ran
		}
		key := RefKey(r.Path, r.HeadSHA)
		if _, ok := want[key]; !ok {
			want[key] = r
		}
	}
	// A failed read of a critical-path or tree-rechecked definition fails the
	// command, as it always has. Any other read serves only the definition
	// history, which is informational: its failure makes that run unread,
	// with the reason.
	strict := map[string]bool{}
	for _, key := range sources {
		strict[key] = true
	}
	for _, r := range w6Runs(data) {
		strict[RefKey(r.Path, r.HeadSHA)] = true
	}
	keys := sortedKeys(want)
	defs := make([]Definition, len(keys))
	err = parallel(ctx, len(keys), func(i int) error {
		d, err := readDefinition(ctx, c, repo, want[keys[i]])
		if err != nil && !strict[keys[i]] && ctx.Err() == nil {
			d.URL, d.Unavailable, err = "", "workflow file read failed: "+err.Error(), nil
		}
		defs[i] = d
		return err
	})
	if err != nil {
		return nil, err
	}
	for i, k := range keys {
		in.RefDefinitions[k] = defs[i]
	}
	for path, key := range sources {
		in.Definitions[path] = in.RefDefinitions[key]
	}
	return Analyze(in), nil
}

// definitionSource picks the run whose commit supplies a workflow's needs
// edges: the latest default-branch run that is not a pull request, else the
// latest non-pull-request run, else the latest run. A pull request's
// unmerged edit, or a fork's commit, is the last resort.
func definitionSource(runs []gh.Run, defaultBranch string) gh.Run {
	latest := func(ok func(gh.Run) bool) (gh.Run, bool) {
		var best gh.Run
		found := false
		for _, r := range runs {
			if !ok(r) {
				continue
			}
			if !found || r.CreatedAt.After(best.CreatedAt) || (r.CreatedAt.Equal(best.CreatedAt) && r.ID > best.ID) {
				best, found = r, true
			}
		}
		return best, found
	}
	if r, ok := latest(func(r gh.Run) bool { return r.HeadBranch == defaultBranch && !pullRequestEvents[r.Event] }); ok {
		return r
	}
	if r, ok := latest(func(r gh.Run) bool { return !pullRequestEvents[r.Event] }); ok {
		return r
	}
	r, _ := latest(func(gh.Run) bool { return true })
	return r
}

// githubManaged is why a GitHub-managed workflow (path outside
// .github/workflows/) has no definition.
const githubManaged = "GitHub-managed workflow: there is no workflow file to read"

func readDefinition(ctx context.Context, c *gh.Client, repo string, r gh.Run) (Definition, error) {
	d := Definition{Path: r.Path, Ref: r.HeadSHA, URL: blobURL(r, r.Path, 0), Branch: r.HeadBranch, Event: r.Event, RunID: r.ID}
	if !strings.HasPrefix(r.Path, ".github/workflows/") {
		d.URL, d.Ref = "", ""
		d.Unavailable = githubManaged
		return d, nil
	}
	if r.HeadSHA == "" {
		d.URL = ""
		d.Unavailable = fmt.Sprintf("run %d has no head commit to read the workflow file at", r.ID)
		return d, nil
	}
	data, found, err := c.RunFile(ctx, repo, r)
	if err != nil {
		return d, err
	}
	if !found {
		d.Unavailable = fmt.Sprintf("workflow file %s not found at head commit %s (a fork's commit is not readable from this repository)", r.Path, r.HeadSHA)
		return d, nil
	}
	sum := sha256.Sum256(data)
	d.SHA256 = hex.EncodeToString(sum[:])
	f, err := workflow.Parse(r.Path, data)
	if err != nil {
		d.Unavailable = fmt.Sprintf("workflow file at %s does not parse: %v", r.HeadSHA, err)
		return d, nil
	}
	d.File = f
	return d, nil
}

// parallel runs fn(0..n-1) on a bounded pool and returns the first error.
func parallel(ctx context.Context, n int, fn func(i int) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		wg    sync.WaitGroup
		once  sync.Once
		first error
		next  = make(chan int)
	)
	for w := 0; w < fetchWorkers && w < n; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				if err := fn(i); err != nil {
					once.Do(func() { first = err; cancel() })
				}
			}
		}()
	}
feed:
	for i := 0; i < n; i++ {
		select {
		case next <- i:
		case <-ctx.Done():
			break feed
		}
	}
	close(next)
	wg.Wait()
	if first != nil {
		return first
	}
	return ctx.Err()
}

// WriteJSON writes the report as indented, deterministic JSON.
func WriteJSON(w io.Writer, r *Report) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

// Analyze turns fetched data into a report. It is pure: the same input gives
// byte-identical output.
func Analyze(in Input) *Report {
	rep := &Report{Schema: Schema, Repo: in.Repo, Since: in.Since.UTC(), Until: in.Until.UTC(),
		Workflows: []WorkflowStats{}, Sinks: []Sink{}, Signals: []signal.Signal{}, Unavailable: []Unavailable{}}
	rep.Unavailable = append(rep.Unavailable, in.Unavailable...)

	byPath := map[string][]RunData{}
	for _, rd := range in.Runs {
		byPath[rd.Run.Path] = append(byPath[rd.Run.Path], rd)
	}
	for _, path := range sortedKeys(byPath) {
		runs := byPath[path]
		sort.Slice(runs, func(i, j int) bool {
			a, b := runs[i].Run, runs[j].Run
			if !a.CreatedAt.Equal(b.CreatedAt) {
				return a.CreatedAt.Before(b.CreatedAt)
			}
			return a.ID < b.ID
		})
		def, hasDef := in.Definitions[path]
		w := workflowStats(path, in.Names[path], runs, def, hasDef)
		w.Definitions = definitionHistory(runs, in.RefDefinitions, in.DefaultBranch)
		// Unread runs are a gap and are listed. A workflow with no
		// default-branch run, every file read (a tag-driven release), has no
		// line to place a change on: its history says so, but nothing failed.
		if h := w.Definitions; h.Unavailable != "" {
			ev := append([]signal.Evidence{}, h.Unread...)
			if h.BranchRuns == 0 {
				ev = append(ev, h.OtherUnread...)
			}
			if len(ev) > 0 {
				rep.Unavailable = append(rep.Unavailable, Unavailable{What: "workflow file history of " + path, Why: h.Unavailable, Evidence: ev})
			}
		}
		if w.Critical.Unavailable != "" {
			ev := []signal.Evidence{}
			if def.URL != "" {
				ev = append(ev, signal.Evidence{Kind: signal.KindFile, URL: def.URL, Path: path})
			} else {
				last := runs[len(runs)-1].Run
				ev = append(ev, signal.Evidence{Kind: signal.KindRun, URL: last.HTMLURL, RunID: last.ID})
			}
			ev = append(ev, w.Critical.Rejected...)
			rep.Unavailable = append(rep.Unavailable, Unavailable{What: "critical path of " + path, Why: w.Critical.Unavailable, Evidence: ev})
		}
		rep.Workflows = append(rep.Workflows, w)
		rep.Signals = append(rep.Signals, rerunSignals(w, runs)...)
		rep.Signals = append(rep.Signals, handoffSignals(w, runs)...)
		rep.Signals = append(rep.Signals, queueSignals(w)...)
	}
	rep.Sinks = sinks(rep.Workflows)
	rep.Signals = append(rep.Signals, slowStepSignals(rep.Sinks)...)
	tree, unavailable := treeRechecked(in.Runs, in.RefDefinitions)
	rep.Signals = append(rep.Signals, tree...)
	rep.Unavailable = append(rep.Unavailable, unavailable...)
	// By id; within an id, emission order (workflow path, then rank).
	sort.SliceStable(rep.Signals, func(i, j int) bool { return rep.Signals[i].ID < rep.Signals[j].ID })
	return rep
}

var failed = map[string]bool{"failure": true, "timed_out": true, "startup_failure": true}

// executed reports whether a job actually ran (a skipped job has no work).
func executed(j gh.Job) bool {
	return j.Conclusion != "skipped" && !j.StartedAt.IsZero() && !j.CompletedAt.Before(j.StartedAt)
}

func stepExecuted(s gh.Step) bool {
	return s.Conclusion != "skipped" && !s.StartedAt.IsZero() && !s.CompletedAt.Before(s.StartedAt)
}

// attemptStart is when the final attempt started.
func attemptStart(rd RunData) time.Time {
	if n := len(rd.Attempts); n > 0 && !rd.Attempts[n-1].RunStartedAt.IsZero() {
		return rd.Attempts[n-1].RunStartedAt
	}
	return rd.Run.RunStartedAt
}

// timedJobs returns the executed jobs that ran in the final attempt, and
// cites the rest. A partial re-run lists the jobs it did not re-run under the
// new attempt, with new ids but the earlier attempt's timestamps; a job that
// reports another attempt number is not this attempt's either.
func timedJobs(rd RunData) ([]gh.Job, []signal.Evidence) {
	start := attemptStart(rd)
	var timed []gh.Job
	var untimed []signal.Evidence
	for _, j := range rd.Jobs {
		if !executed(j) {
			continue
		}
		switch {
		case j.RunAttempt != 0 && j.RunAttempt != rd.Run.RunAttempt:
			ev := jobEvidence(j)
			ev.Note = fmt.Sprintf("job %q listed under attempt %d reports attempt %d; not timed", j.Name, rd.Run.RunAttempt, j.RunAttempt)
			untimed = append(untimed, ev)
		case rd.Run.RunAttempt > 1 && j.StartedAt.Before(start):
			ev := jobEvidence(j)
			ev.Note = fmt.Sprintf("job %q carried over from an earlier attempt (started %s, before attempt %d began %s); not timed",
				j.Name, j.StartedAt.UTC().Format(time.RFC3339), rd.Run.RunAttempt, start.UTC().Format(time.RFC3339))
			untimed = append(untimed, ev)
		default:
			timed = append(timed, j)
		}
	}
	return timed, untimed
}

// runWindow is the final attempt's start and end. The end is the last timed
// job's completion; without one it is updated_at, and estimated is true.
func runWindow(rd RunData, timed []gh.Job) (start, end time.Time, estimated bool) {
	start = attemptStart(rd)
	for _, j := range timed {
		if j.CompletedAt.After(end) {
			end = j.CompletedAt
		}
	}
	if !end.IsZero() {
		return start, end, false
	}
	end = rd.Run.UpdatedAt
	if n := len(rd.Attempts); n > 0 && !rd.Attempts[n-1].UpdatedAt.IsZero() {
		end = rd.Attempts[n-1].UpdatedAt
	}
	return start, end, true
}

func runEvidence(r gh.Run, note string) signal.Evidence {
	return signal.Evidence{Kind: signal.KindRun, URL: r.HTMLURL, RunID: r.ID, Note: note}
}

func jobEvidence(j gh.Job) signal.Evidence {
	return signal.Evidence{Kind: signal.KindJob, URL: j.HTMLURL, RunID: j.RunID, JobID: j.ID}
}

func stepEvidence(j gh.Job, s gh.Step) signal.Evidence {
	return signal.Evidence{Kind: signal.KindStep, URL: fmt.Sprintf("%s#step:%d:1", j.HTMLURL, s.Number), RunID: j.RunID, JobID: j.ID, Step: s.Name}
}

// attemptURL is the web page of one attempt of a run.
func attemptURL(r gh.Run, n int) string { return fmt.Sprintf("%s/attempts/%d", r.HTMLURL, n) }

// blobURL links a file at the run's head commit, on the run's own web host.
func blobURL(r gh.Run, path string, line int) string {
	i := strings.Index(r.HTMLURL, "/actions/runs/")
	if i < 0 || r.HeadSHA == "" {
		return ""
	}
	u := r.HTMLURL[:i] + "/blob/" + r.HeadSHA + "/" + path
	if line > 0 {
		u += fmt.Sprintf("#L%d", line)
	}
	return u
}

type stepKey struct {
	name       string
	occurrence int
}

type jobAcc struct {
	dur, queue []Sample
	steps      map[stepKey][]Sample
	stepOrder  map[stepKey]int
}

func workflowStats(path, name string, runs []RunData, def Definition, hasDef bool) WorkflowStats {
	if name == "" {
		name = runs[len(runs)-1].Run.Name
	}
	w := WorkflowStats{Path: path, Name: name, Runs: len(runs), Confidence: Confidence(len(runs)), Events: []string{}, Jobs: []JobStats{},
		CriticalPath: []string{}, Evidence: []signal.Evidence{}}
	events := map[string]bool{}
	var durations, queues []Sample
	var priorAttempts, untimedJobs []signal.Evidence
	successes := 0
	jobs := map[string]*jobAcc{}
	for _, rd := range runs {
		r := rd.Run
		events[r.Event] = true
		timed, untimed := timedJobs(rd)
		untimedJobs = append(untimedJobs, untimed...)
		w.CarriedOverJobs += len(untimed)
		start, end, estimated := runWindow(rd, timed)
		d := nonNegative(end.Sub(start))
		basis := "start to last job completed"
		if estimated {
			w.EstimatedEnds++
			basis = "no timed job: end estimated from updated_at"
		}
		note := fmt.Sprintf("%s in %s (%s); %s on %s; attempt %d", orNone(r.Conclusion), d, basis, r.Event, r.HeadBranch, r.RunAttempt)
		w.Evidence = append(w.Evidence, runEvidence(r, note))
		durations = append(durations, Sample{D: d, Ev: runEvidence(r, "")})
		switch {
		case r.Conclusion == "success":
			successes++
		case failed[r.Conclusion]:
			w.Failures++
		}
		if r.RunAttempt > 1 {
			w.Reruns++
			priorAttempts = append(priorAttempts, supersededAttempts(rd)...)
		}
		for _, j := range timed {
			acc := jobs[j.Name]
			if acc == nil {
				acc = &jobAcc{steps: map[stepKey][]Sample{}, stepOrder: map[stepKey]int{}}
				jobs[j.Name] = acc
			}
			ev := jobEvidence(j)
			acc.dur = append(acc.dur, Sample{D: j.CompletedAt.Sub(j.StartedAt), Ev: ev})
			q := Sample{D: nonNegative(j.StartedAt.Sub(j.CreatedAt)), Ev: ev}
			acc.queue = append(acc.queue, q)
			queues = append(queues, q)
			steps := append([]gh.Step(nil), j.Steps...)
			sort.SliceStable(steps, func(a, b int) bool { return steps[a].Number < steps[b].Number })
			seen := map[string]int{}
			for _, s := range steps {
				seen[s.Name]++
				if !stepExecuted(s) {
					continue
				}
				k := stepKey{s.Name, seen[s.Name]}
				if n, ok := acc.stepOrder[k]; !ok || s.Number < n {
					acc.stepOrder[k] = s.Number
				}
				acc.steps[k] = append(acc.steps[k], Sample{D: s.CompletedAt.Sub(s.StartedAt), Ev: stepEvidence(j, s)})
			}
		}
	}
	w.Evidence = append(w.Evidence, priorAttempts...)
	w.Evidence = append(w.Evidence, untimedJobs...)
	w.SuccessRate = float64(successes) / float64(len(runs))
	var ev []signal.Evidence
	w.P50, w.P90, ev = percentiles(durations, "run duration")
	w.Evidence = append(w.Evidence, ev...)
	w.QueueP50, w.QueueP90, ev = percentiles(queues, "job queue")
	w.Evidence = append(w.Evidence, ev...)
	for e := range events {
		w.Events = append(w.Events, e)
	}
	sort.Strings(w.Events)

	for _, name := range sortedKeys(jobs) {
		acc := jobs[name]
		js := JobStats{Name: name, Runs: len(acc.dur), Confidence: Confidence(len(acc.dur)), Steps: []StepStats{}}
		js.P50, js.P90, js.Evidence = percentiles(acc.dur, "job duration")
		js.QueueP50, js.QueueP90, js.QueueEvidence = percentiles(acc.queue, "job queue")
		keys := make([]stepKey, 0, len(acc.steps))
		for k := range acc.steps {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			a, b := keys[i], keys[j]
			if acc.stepOrder[a] != acc.stepOrder[b] {
				return acc.stepOrder[a] < acc.stepOrder[b]
			}
			if a.name != b.name {
				return a.name < b.name
			}
			return a.occurrence < b.occurrence
		})
		for _, k := range keys {
			ss := StepStats{Name: k.name, Occurrence: k.occurrence, Runs: len(acc.steps[k]), Confidence: Confidence(len(acc.steps[k]))}
			ss.P50, ss.P90, ss.Evidence = percentiles(acc.steps[k], "step duration")
			js.Steps = append(js.Steps, ss)
		}
		w.Jobs = append(w.Jobs, js)
	}

	var f *workflow.File
	if hasDef {
		f = def.File
	}
	w.Role, w.RoleReason = InferRole(w.Name, path, f, w.Events)
	w.Critical = criticalPath(runs, def, hasDef)
	for _, n := range w.Critical.Nodes {
		w.CriticalPath = append(w.CriticalPath, n.Job)
	}
	return w
}

// supersededAttempts cites every attempt before the final one.
func supersededAttempts(rd RunData) []signal.Evidence {
	r := rd.Run
	out := []signal.Evidence{}
	for n := 1; n < r.RunAttempt; n++ {
		conclusion := "conclusion not fetched"
		if n <= len(rd.Attempts) {
			conclusion = orNone(rd.Attempts[n-1].Conclusion)
		}
		out = append(out, signal.Evidence{Kind: signal.KindRun, URL: attemptURL(r, n), RunID: r.ID,
			Note: fmt.Sprintf("attempt %d of %d: %s (superseded; not timed)", n, r.RunAttempt, conclusion)})
	}
	return out
}

func orNone(s string) string {
	if s == "" {
		return "no conclusion"
	}
	return s
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortEvidence(ev []signal.Evidence) {
	sort.SliceStable(ev, func(i, j int) bool {
		if ev[i].RunID != ev[j].RunID {
			return ev[i].RunID < ev[j].RunID
		}
		if ev[i].JobID != ev[j].JobID {
			return ev[i].JobID < ev[j].JobID
		}
		return ev[i].URL < ev[j].URL
	})
}
