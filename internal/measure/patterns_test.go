package measure

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/schuettc/muda/internal/gh"
	"github.com/schuettc/muda/internal/workflow"
)

func analyze(t *testing.T, runs []RunData, defs map[string]Definition) *Report {
	t.Helper()
	return analyzeWith(t, runs, defs, nil)
}

func analyzeWith(t *testing.T, runs []RunData, defs, refDefs map[string]Definition) *Report {
	t.Helper()
	if defs == nil {
		defs = map[string]Definition{}
	}
	if refDefs == nil {
		refDefs = map[string]Definition{}
	}
	return Analyze(Input{Repo: testRepo, Since: t0.Add(-24 * time.Hour), Until: t0.Add(24 * time.Hour), Runs: runs,
		Definitions: defs, RefDefinitions: refDefs, DefaultBranch: "main"})
}

func mkDef(t *testing.T, path, sha, src string) Definition {
	t.Helper()
	f, err := workflow.Parse(path, []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(src)) // readDefinition always sets the content hash with the file
	return Definition{Path: path, Ref: sha, URL: "https://github.com/o/r/blob/" + sha + "/" + path, SHA256: hex.EncodeToString(sum[:]), File: f}
}

var checkSteps = []string{"Set up job", "Run actions/checkout@v7.0.1", "lint", "test", "Post Run actions/checkout@v7.0.1", "Complete job"}

// checkJob is a workflow job whose executed steps are checkSteps.
func checkJob(id, runsOn, testCmd, extra string) string {
	return "  " + id + ":\n    runs-on: " + runsOn + "\n" + extra +
		"    steps:\n      - uses: actions/checkout@v7.0.1\n      - name: lint\n        run: make lint\n      - name: test\n        run: " + testCmd + "\n"
}

const (
	ciPath  = ".github/workflows/ci.yml"
	depPath = ".github/workflows/deploy.yml"
)

// w6Fixture is a CI run and a deploy run on the same tree and branch, each
// executing a job with checkSteps' names, with the given definitions.
func w6Fixture(t *testing.T, ciEvent, depEvent, ciJob, depJob string) ([]RunData, map[string]Definition) {
	t.Helper()
	ci := mkRun(1, ciPath, ciEvent, "main", "tree-a", "success", 1, t0, 10*time.Minute)
	dep := mkRun(2, depPath, depEvent, "main", "tree-a", "success", 1, t0, 20*time.Minute)
	runs := []RunData{
		{Run: ci, Jobs: []gh.Job{mkJob(ci, 11, "checks", "success", t0, 0, 6*time.Minute, mkSteps(t0, checkSteps...)...)}},
		{Run: dep, Jobs: []gh.Job{
			mkJob(dep, 21, "mirror", "success", t0, 0, 6*time.Minute, mkSteps(t0, checkSteps...)...),
			mkJob(dep, 22, "deploy", "success", t0.Add(7*time.Minute), 0, 5*time.Minute, mkSteps(t0, "Set up job", "Deploy stack", "Complete job")...),
		}},
	}
	refs := map[string]Definition{
		RefKey(ciPath, "sha1"):  mkDef(t, ciPath, "sha1", "on: push\njobs:\n"+ciJob),
		RefKey(depPath, "sha2"): mkDef(t, depPath, "sha2", "on: push\njobs:\n"+depJob+"  deploy:\n    needs: mirror\n    runs-on: x\n    environment: prod\n    steps: [{run: ./deploy}]\n"),
	}
	return runs, refs
}

func TestTreeRechecked(t *testing.T) {
	same := checkJob("checks", "ubuntu-26.04", "make test", "")
	mirror := checkJob("mirror", "ubuntu-26.04", "make test", "")
	runs, refs := w6Fixture(t, "push", "push", same, mirror)
	r := analyzeWith(t, runs, nil, refs)
	got := signalsWithID(r, "tree-rechecked")
	if len(got) != 1 {
		t.Fatalf("want one tree-rechecked, got %+v\nunavailable %+v", r.Signals, r.Unavailable)
	}
	s := got[0]
	if s.Severity != "warn" || s.Waste[0] != "W6" || !strings.Contains(s.Summary, "tree-a") || !strings.Contains(s.Summary, "6m0s") {
		t.Fatalf("signal: %+v", s)
	}
	ids := map[int64]bool{}
	files := 0
	for _, e := range s.Evidence {
		ids[e.JobID] = true
		if e.URL == "" {
			t.Fatalf("evidence without URL: %+v", e)
		}
		if e.Kind == "file" {
			files++
		}
	}
	if !ids[11] || !ids[21] || ids[22] || files != 2 {
		t.Fatalf("evidence must name both executed check jobs and both definitions, not the deploy job: %+v", s.Evidence)
	}

	for _, tc := range []struct {
		name, ciJob, depJob string
		ciEvent             string
		unavailable         string // "" means neither a signal nor an unavailable entry
	}{
		{"same step names, different command (CE1)", same, checkJob("mirror", "ubuntu-26.04", "make test-prod-preflight", ""), "push", ""},
		{"different platform", same, checkJob("mirror", "macos-26", "make test", ""), "push", ""},
		{"different env", same, checkJob("mirror", "ubuntu-26.04", "make test", "    env:\n      TARGET: prod\n"), "push", ""},
		{"prod environment job is not a mirror", same, checkJob("mirror", "ubuntu-26.04", "make test", "    environment: prod\n"), "push", "environment"},
		{"matrix context is unknown", same, checkJob("mirror", "ubuntu-26.04", "make test", "    strategy:\n      matrix:\n        v: [1]\n"), "push", "matrix"},
		{"pull_request checks the merge ref, not head tree (I2)", same, mirror, "pull_request", "pull_request"},
		{"checkout of another ref", same, strings.Replace(mirror, "      - uses: actions/checkout@v7.0.1\n", "      - uses: actions/checkout@v7.0.1\n        with:\n          ref: main\n", 1), "push", "checkout"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runs, refs := w6Fixture(t, tc.ciEvent, "push", tc.ciJob, tc.depJob)
			r := analyzeWith(t, runs, nil, refs)
			if got := signalsWithID(r, "tree-rechecked"); len(got) != 0 {
				t.Fatalf("no W6 may be claimed: %+v", got)
			}
			if tc.unavailable == "" {
				return
			}
			if !unavailableMentions(r, "tree-rechecked", tc.unavailable) {
				t.Fatalf("want an unavailable tree-rechecked entry mentioning %q, got %+v", tc.unavailable, r.Unavailable)
			}
		})
	}
	t.Run("missing definition at the run's commit is unavailable", func(t *testing.T) {
		runs, refs := w6Fixture(t, "push", "push", same, mirror)
		delete(refs, RefKey(depPath, "sha2"))
		r := analyzeWith(t, runs, nil, refs)
		if len(signalsWithID(r, "tree-rechecked")) != 0 || !unavailableMentions(r, "tree-rechecked", "sha2") {
			t.Fatalf("got signals %+v unavailable %+v", r.Signals, r.Unavailable)
		}
	})
	t.Run("different branch or tree is not a recheck", func(t *testing.T) {
		runs, refs := w6Fixture(t, "push", "push", same, mirror)
		runs[1].Run.HeadBranch = "dev"
		if got := signalsWithID(analyzeWith(t, runs, nil, refs), "tree-rechecked"); len(got) != 0 {
			t.Fatalf("got %+v", got)
		}
		runs, refs = w6Fixture(t, "push", "push", same, mirror)
		runs[1].Run.HeadCommit.TreeID = "tree-b"
		if got := signalsWithID(analyzeWith(t, runs, nil, refs), "tree-rechecked"); len(got) != 0 {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("same workflow and event is a re-trigger, not a mirror", func(t *testing.T) {
		runs, refs := w6Fixture(t, "push", "push", same, mirror)
		again := mkRun(3, ciPath, "push", "main", "tree-a", "success", 1, t0.Add(time.Hour), time.Minute)
		refs[RefKey(ciPath, "sha3")] = refs[RefKey(ciPath, "sha1")]
		r := analyzeWith(t, []RunData{runs[0], {Run: again, Jobs: []gh.Job{mkJob(again, 31, "checks", "success", again.RunStartedAt, 0, time.Minute, mkSteps(t0, checkSteps...)...)}}}, nil, refs)
		if got := signalsWithID(r, "tree-rechecked"); len(got) != 0 {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("one execution per origin; failed executions are not savings (M1)", func(t *testing.T) {
		runs, refs := w6Fixture(t, "push", "push", same, mirror)
		again := mkRun(3, ciPath, "push", "main", "tree-a", "success", 1, t0.Add(time.Hour), time.Minute)
		refs[RefKey(ciPath, "sha3")] = refs[RefKey(ciPath, "sha1")]
		failedRun := mkRun(4, depPath, "workflow_dispatch", "main", "tree-a", "failure", 1, t0.Add(2*time.Hour), time.Minute)
		refs[RefKey(depPath, "sha4")] = refs[RefKey(depPath, "sha2")]
		runs = append(runs,
			RunData{Run: again, Jobs: []gh.Job{mkJob(again, 31, "checks", "success", again.RunStartedAt, 0, 9*time.Minute, mkSteps(t0, checkSteps...)...)}},
			RunData{Run: failedRun, Jobs: []gh.Job{mkJob(failedRun, 41, "mirror", "failure", failedRun.RunStartedAt, 0, 2*time.Minute, mkSteps(t0, checkSteps...)...)}})
		got := signalsWithID(analyzeWith(t, runs, nil, refs), "tree-rechecked")
		if len(got) != 1 || !strings.Contains(got[0].Summary, "ran 2 times") || !strings.Contains(got[0].Summary, "took 6m0s") {
			t.Fatalf("want 2 origins and a 6m repeat: %+v", got)
		}
		for _, e := range got[0].Evidence {
			if e.JobID == 31 || e.JobID == 41 {
				t.Fatalf("same-origin repeat or failed execution cited as saving: %+v", e)
			}
		}
	})
}

func unavailableMentions(r *Report, what, why string) bool {
	for _, u := range r.Unavailable {
		text := u.Why + " " + u.What
		for _, e := range u.Evidence {
			text += " " + e.Note
		}
		if strings.Contains(u.What, what) && strings.Contains(text, why) {
			return true
		}
	}
	return false
}

func TestTreeRecheckedSkipsGuardedDeploy(t *testing.T) {
	ci := mkRun(1, ciPath, "push", "main", "tree-a", "success", 1, t0, 10*time.Minute)
	dep := mkRun(2, depPath, "push", "main", "tree-a", "success", 1, t0, 8*time.Minute)
	guarded := mkJob(dep, 21, "checks", "skipped", t0, 0, 0)
	job := checkJob("checks", "ubuntu-26.04", "make test", "")
	refs := map[string]Definition{
		RefKey(ciPath, "sha1"):  mkDef(t, ciPath, "sha1", "on: push\njobs:\n"+job),
		RefKey(depPath, "sha2"): mkDef(t, depPath, "sha2", "on: push\njobs:\n"+job),
	}
	r := analyzeWith(t, []RunData{
		{Run: ci, Jobs: []gh.Job{mkJob(ci, 11, "checks", "success", t0, 0, 6*time.Minute, mkSteps(t0, checkSteps...)...)}},
		{Run: dep, Jobs: []gh.Job{
			mkJob(dep, 20, "tested-tree", "success", t0, 0, 10*time.Second, mkSteps(t0, "Set up job", "Look up CI result", "Complete job")...),
			guarded,
			mkJob(dep, 22, "deploy", "success", t0.Add(time.Minute), 0, 5*time.Minute, mkSteps(t0, "Set up job", "Deploy stack", "Complete job")...),
		}},
	}, nil, refs)
	if got := signalsWithID(r, "tree-rechecked"); len(got) != 0 {
		t.Fatalf("a skipped mirrored check is not a duplicate: %+v", got)
	}
	for _, s := range r.Signals {
		for _, e := range s.Evidence {
			if e.JobID == 21 {
				t.Fatalf("the skipped job must not be cited as executed work: %+v", s)
			}
		}
	}
	w := findWorkflow(t, r, depPath)
	for _, j := range w.Jobs {
		if j.Name == "checks" {
			t.Fatalf("skipped job must not get duration stats: %+v", j)
		}
	}
}

func TestHandoffFailure(t *testing.T) {
	const path = ".github/workflows/deploy.yml"
	def, err := workflow.Parse(path, []byte("name: deploy\non:\n  workflow_run:\n    workflows: [ci]\n    types: [completed]\njobs:\n  deploy:\n    runs-on: x\n    steps: [{run: ./deploy}]\n"))
	if err != nil {
		t.Fatal(err)
	}
	build := func(n, failures int, event string) []RunData {
		var out []RunData
		for i := 0; i < n; i++ {
			c := "success"
			if i < failures {
				c = "failure"
			}
			r := mkRun(int64(100+i), path, event, "main", "t", c, 1, t0.Add(time.Duration(i)*time.Hour), 5*time.Minute)
			out = append(out, RunData{Run: r, Jobs: []gh.Job{mkJob(r, int64(1000+i), "deploy", c, r.RunStartedAt, 0, 4*time.Minute)}})
		}
		return out
	}
	defs := map[string]Definition{path: {Path: path, Ref: "sha", File: def}}
	for _, tc := range []struct {
		name            string
		n, fail         int
		event           string
		want            bool
		wantInSummary   string
		wantFailedCited int
	}{
		{"20% of 5 hand-offs", 5, 1, "workflow_run", true, "1 of 5", 1},
		{"40% of 5", 5, 2, "workflow_run", true, "2 of 5", 2},
		{"too few runs", 4, 2, "workflow_run", false, "", 0},
		{"below the rate", 10, 1, "workflow_run", false, "", 0},
		{"not a hand-off trigger", 5, 3, "push", false, "", 0},
		{"dispatch hand-off", 5, 1, "repository_dispatch", true, "1 of 5", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := analyze(t, build(tc.n, tc.fail, tc.event), defs)
			got := signalsWithID(r, "handoff-failure")
			if (len(got) == 1) != tc.want || len(got) > 1 {
				t.Fatalf("handoff signals %+v", got)
			}
			if !tc.want {
				return
			}
			s := got[0]
			if s.Severity != "high" || !strings.Contains(s.Summary, tc.wantInSummary) {
				t.Fatalf("signal %+v", s)
			}
			failed := 0
			for _, e := range s.Evidence {
				if strings.Contains(e.Note, "failure") {
					failed++
				}
			}
			if failed != tc.wantFailedCited {
				t.Fatalf("cited %d failures: %+v", failed, s.Evidence)
			}
		})
	}
	t.Run("not a bump or deploy workflow", func(t *testing.T) {
		const other = ".github/workflows/notify.yml"
		f, _ := workflow.Parse(other, []byte("on: workflow_run\njobs:\n  n:\n    runs-on: x\n"))
		var runs []RunData
		for _, rd := range build(5, 3, "workflow_run") {
			rd.Run.Path, rd.Run.Name = other, "notify"
			runs = append(runs, rd)
		}
		r := analyze(t, runs, map[string]Definition{other: {Path: other, Ref: "sha", File: f}})
		if got := signalsWithID(r, "handoff-failure"); len(got) != 0 {
			t.Fatalf("got %+v", got)
		}
	})
}

func TestQueueRerunAndSlowStepThresholds(t *testing.T) {
	const path = ".github/workflows/ci.yml"
	var runs []RunData
	for i := 0; i < 10; i++ {
		attempt := 1
		if i == 0 {
			attempt = 2
		}
		r := mkRun(int64(i+1), path, "push", "main", "t", "success", attempt, t0.Add(time.Duration(i)*time.Hour), 20*time.Minute)
		if attempt == 2 {
			r.RunStartedAt = r.CreatedAt.Add(30 * time.Minute)
			r.UpdatedAt = r.RunStartedAt.Add(20 * time.Minute)
		}
		queue := time.Minute
		if i >= 8 {
			queue = 6 * time.Minute
		}
		start := r.RunStartedAt.Add(queue)
		rd := RunData{Run: r, Jobs: []gh.Job{mkJob(r, int64(100+i), "build", "success", start, queue, 3*time.Minute, mkSteps(start, "Set up job", "compile", "Complete job")...)}}
		if attempt == 2 {
			rd.Attempts = []gh.Attempt{
				{RunAttempt: 1, Conclusion: "failure", RunStartedAt: r.CreatedAt, UpdatedAt: r.CreatedAt.Add(5 * time.Minute)},
				{RunAttempt: 2, Conclusion: "success", RunStartedAt: r.RunStartedAt, UpdatedAt: r.UpdatedAt},
			}
		}
		runs = append(runs, rd)
	}
	r := analyze(t, runs, nil)
	if got := signalsWithID(r, "rerun"); len(got) != 1 || !strings.Contains(got[0].Summary, "1 of 10") {
		t.Fatalf("10%% re-runs must signal: %+v", r.Signals)
	}
	if got := signalsWithID(r, "queue-time"); len(got) != 1 || !strings.Contains(got[0].Summary, "6m0s") {
		t.Fatalf("queue p90 of 6m must signal: %+v", got)
	}
	slow := signalsWithID(r, "slow-step")
	if len(slow) != 3 || len(r.Sinks) != 3 {
		t.Fatalf("slow steps %+v sinks %+v", slow, r.Sinks)
	}
	if r.Sinks[0].Weight != 10*r.Sinks[0].P50 {
		t.Fatalf("sink weight is p50 × runs: %+v", r.Sinks[0])
	}

	// Seven runs, none re-run and short queues: no rerun, no queue-time.
	r = analyze(t, runs[1:8], nil)
	if len(signalsWithID(r, "rerun")) != 0 || len(signalsWithID(r, "queue-time")) != 0 {
		t.Fatalf("unexpected signals %+v", r.Signals)
	}
}

func TestSlowStepTopN(t *testing.T) {
	var runs []RunData
	names := []string{"Set up job"}
	for i := 0; i < SlowStepTop+5; i++ {
		names = append(names, "step-"+string(rune('a'+i)))
	}
	r := mkRun(1, ".github/workflows/ci.yml", "push", "main", "t", "success", 1, t0, time.Hour)
	runs = append(runs, RunData{Run: r, Jobs: []gh.Job{mkJob(r, 10, "build", "success", t0, 0, time.Hour, mkSteps(t0, names...)...)}})
	rep := analyze(t, runs, nil)
	if len(rep.Sinks) != SlowStepTop || len(signalsWithID(rep, "slow-step")) != SlowStepTop {
		t.Fatalf("want top %d, got %d sinks", SlowStepTop, len(rep.Sinks))
	}
}

// sameWorkflowTwoEvents runs one workflow's job on two tree-proven events on
// the same tree and branch, each reading the given job YAML at its own commit.
// extra adds a third run of the same job on another event.
func sameWorkflowTwoEvents(t *testing.T, ev1, ev2, job string, extra ...string) ([]RunData, map[string]Definition) {
	t.Helper()
	events := append([]string{ev1, ev2}, extra...)
	var runs []RunData
	refs := map[string]Definition{}
	for i, ev := range events {
		id := int64(i + 1)
		r := mkRun(id, ciPath, ev, "main", "tree-a", "success", 1, t0.Add(time.Duration(i)*time.Hour), 10*time.Minute)
		dur := 6 * time.Minute
		if i > 0 {
			dur = 40 * time.Minute
		}
		runs = append(runs, RunData{Run: r, Jobs: []gh.Job{mkJob(r, id*10+1, "checks", "success", r.RunStartedAt, 0, dur, mkSteps(r.RunStartedAt, checkSteps...)...)}})
		sha := fmt.Sprintf("sha%d", id)
		refs[RefKey(ciPath, sha)] = mkDef(t, ciPath, sha, "on: [push, schedule, workflow_dispatch, pull_request]\njobs:\n"+job)
	}
	return runs, refs
}

// TestTreeRecheckedExpressionIdentity (N1/CE5): definition text that is
// identical but holds an expression may do different work per origin (a
// nightly full suite vs a quick push check), so it is not a proven identity.
func TestTreeRecheckedExpressionIdentity(t *testing.T) {
	literal := checkJob("checks", "ubuntu-26.04", "make test", "")
	runs, refs := sameWorkflowTwoEvents(t, "push", "schedule", literal)
	if got := signalsWithID(analyzeWith(t, runs, nil, refs), "tree-rechecked"); len(got) != 1 {
		t.Fatalf("a proven literal identity on push and schedule is W6: %+v", got)
	}
	for _, tc := range []struct{ name, ev2, job string }{
		{"CE5 event-dependent command", "schedule", checkJob("checks", "ubuntu-26.04", "make ${{ github.event_name == 'schedule' && 'full-suite' || 'quick' }}", "")},
		{"workflow_dispatch inputs", "workflow_dispatch", checkJob("checks", "ubuntu-26.04", "make ${{ inputs.suite }}", "")},
		{"expression in job env", "schedule", checkJob("checks", "ubuntu-26.04", "make test", "    env:\n      SUITE: ${{ github.event.schedule }}\n")},
		{"expression in runs-on", "schedule", checkJob("checks", "${{ vars.RUNNER }}", "make test", "")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runs, refs := sameWorkflowTwoEvents(t, "push", tc.ev2, tc.job)
			r := analyzeWith(t, runs, nil, refs)
			if got := signalsWithID(r, "tree-rechecked"); len(got) != 0 {
				t.Fatalf("an expression-bearing identity is not proven: %+v", got)
			}
			if !unavailableMentions(r, "tree-rechecked", "expression") {
				t.Fatalf("want a named unavailable entry: %+v", r.Unavailable)
			}
		})
	}
}

// TestTreeRecheckedKeepsUnprovenBesideSignal (N3): a proven pair still
// emits W6, and an unproven execution in the same nominated group is still
// reported, not dropped.
func TestTreeRecheckedKeepsUnprovenBesideSignal(t *testing.T) {
	runs, refs := sameWorkflowTwoEvents(t, "push", "schedule", checkJob("checks", "ubuntu-26.04", "make test", ""), "pull_request")
	r := analyzeWith(t, runs, nil, refs)
	if got := signalsWithID(r, "tree-rechecked"); len(got) != 1 {
		t.Fatalf("the proven push+schedule pair must still signal: %+v", got)
	}
	var found bool
	for _, u := range r.Unavailable {
		for _, e := range u.Evidence {
			if strings.Contains(u.What, "tree-rechecked") && e.JobID == 31 && strings.Contains(e.Note, "pull_request") {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("the unproven pull_request execution must be reported beside the signal: %+v", r.Unavailable)
	}
}
