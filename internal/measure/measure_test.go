package measure

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/schuettc/muda/internal/gh"
	"github.com/schuettc/muda/internal/ghtest"
	"github.com/schuettc/muda/internal/signal"
)

var update = flag.Bool("update", false, "rewrite golden files")

// TestMeasureRerunAttempts (Review Focus 3): a run with two attempts counts
// one re-run, is timed from attempt 2 only, and cites attempt 1 as evidence.
func TestMeasureRerunAttempts(t *testing.T) {
	const path = ".github/workflows/ci.yml"
	f := &fakeGitHub{files: map[string]string{}}
	run := mkRun(7, path, "push", "main", "t", "success", 2, t0, 0)
	attempt1Start := t0
	attempt2Start := t0.Add(time.Hour)
	run.RunStartedAt, run.UpdatedAt = attempt2Start, attempt2Start.Add(4*time.Minute)
	f.attempts = map[int64][]gh.Attempt{7: {
		{RunAttempt: 1, Status: "completed", Conclusion: "failure", CreatedAt: t0, RunStartedAt: attempt1Start, UpdatedAt: attempt1Start.Add(50 * time.Minute)},
		{RunAttempt: 2, Status: "completed", Conclusion: "success", CreatedAt: t0, RunStartedAt: attempt2Start, UpdatedAt: attempt2Start.Add(4 * time.Minute)},
	}}
	f.addRun(run, mkJob(run, 72, "test", "success", attempt2Start, 30*time.Second, 3*time.Minute, mkSteps(attempt2Start, "Set up job", "go test")...))
	// The run's end is its last job's completion (+3m), not updated_at (+4m).
	// Attempt 1's job is served too: measure must not read or time it.
	a1 := run
	a1.RunAttempt = 1
	f.jobs["7/1"] = []gh.Job{mkJob(a1, 71, "test", "failure", attempt1Start, 0, 49*time.Minute)}
	f.files["sha7:"+path] = "on: push\njobs:\n  test:\n    runs-on: x\n    steps: [{run: go test}]\n"

	r, err := Run(context.Background(), f.client(), testRepo, t0.Add(-time.Hour), t0.Add(48*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	w := findWorkflow(t, r, path)
	if w.Runs != 1 || w.Reruns != 1 || w.P50 != 3*time.Minute || w.P90 != w.P50 || w.SuccessRate != 1 || w.EstimatedEnds != 0 {
		t.Fatalf("workflow stats: runs=%d reruns=%d p50=%v p90=%v success=%v", w.Runs, w.Reruns, w.P50, w.P90, w.SuccessRate)
	}
	job := findJob(t, w, "test")
	if job.Runs != 1 || job.P50 != 3*time.Minute || job.QueueP50 != 30*time.Second {
		t.Fatalf("job stats timed the wrong attempt: %+v", job)
	}
	if f.hits["/repos/o/r/actions/runs/7/attempts/1/jobs"] != 0 {
		t.Fatal("measure fetched the superseded attempt's jobs")
	}
	sigs := signalsWithID(r, "rerun")
	if len(sigs) != 1 {
		t.Fatalf("rerun signals: %+v", r.Signals)
	}
	var cited bool
	for _, e := range sigs[0].Evidence {
		if e.URL == run.HTMLURL+"/attempts/1" && strings.Contains(e.Note, "failure") {
			cited = true
		}
	}
	if !cited {
		t.Fatalf("prior attempt not cited: %+v", sigs[0].Evidence)
	}
}

func TestMeasureRerunAttemptsMismatchIsAnError(t *testing.T) {
	f := &fakeGitHub{files: map[string]string{}}
	run := mkRun(7, ".github/workflows/ci.yml", "push", "main", "t", "success", 2, t0, time.Minute)
	f.attempts = map[int64][]gh.Attempt{7: {{RunAttempt: 1}, {RunAttempt: 3}}}
	f.addRun(run)
	_, err := Run(context.Background(), f.client(), testRepo, t0.Add(-time.Hour), t0.Add(48*time.Hour))
	if err == nil || !strings.Contains(err.Error(), "7") {
		t.Fatalf("an attempt list that does not end in the run's attempt must fail naming the run: %v", err)
	}
}

// TestMeasureCriticalPathNeeds: the jobs API has no needs edges, so the path
// comes from the workflow YAML read at the run's head commit.
func TestMeasureCriticalPathNeeds(t *testing.T) {
	const path = ".github/workflows/ci.yml"
	yml := `on: push
jobs:
  a:
    runs-on: x
    steps: [{run: make}]
  b:
    needs: a
    runs-on: ${{ matrix.os }}
    strategy:
      matrix:
        os: [l, m]
    steps: [{run: make test}]
  c:
    needs: [a]
    runs-on: x
    steps: [{run: make lint}]
  d:
    needs: [b, c]
    runs-on: x
    steps: [{run: make ship}]
`
	f := &fakeGitHub{files: map[string]string{}}
	for i := int64(1); i <= 3; i++ {
		start := t0.Add(time.Duration(i) * 24 * time.Hour)
		run := mkRun(i, path, "push", "main", "t", "success", 1, start, 9*time.Minute)
		f.addRun(run,
			mkJob(run, i*10+1, "a", "success", start, 0, time.Minute),
			mkJob(run, i*10+2, "b (l)", "success", start.Add(time.Minute), 0, 5*time.Minute),
			mkJob(run, i*10+3, "b (m)", "success", start.Add(time.Minute), 0, 4*time.Minute),
			mkJob(run, i*10+4, "c", "success", start.Add(time.Minute), 0, 2*time.Minute),
			mkJob(run, i*10+5, "d", "success", start.Add(6*time.Minute), 0, time.Minute),
		)
	}
	// A fourth, older run whose job list does not match the definition is
	// excluded from the path basis rather than fitted to it.
	old := mkRun(4, path, "push", "main", "t", "success", 1, t0, 30*time.Minute)
	f.addRun(old, mkJob(old, 41, "legacy", "success", t0, 0, 30*time.Minute))
	f.files["sha3:"+path] = yml // the latest run's head commit

	r, err := Run(context.Background(), f.client(), testRepo, t0.Add(-time.Hour), t0.Add(10*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	w := findWorkflow(t, r, path)
	if !reflect.DeepEqual(w.CriticalPath, []string{"a", "b", "d"}) {
		t.Fatalf("critical path %v, detail %+v", w.CriticalPath, w.Critical)
	}
	c := w.Critical
	if c.Ref != "sha3" || c.BasisRuns != 3 || c.P50 != 7*time.Minute || c.Unavailable != "" || len(c.Nodes) != 3 {
		t.Fatalf("detail %+v", c)
	}
	if c.Nodes[1].Kind != "matrix" || c.Nodes[1].P50 != 5*time.Minute {
		t.Fatalf("matrix node is the span of its legs: %+v", c.Nodes[1])
	}
	for _, n := range c.Nodes {
		if len(n.Evidence) == 0 || n.Evidence[0].URL == "" {
			t.Fatalf("node without evidence: %+v", n)
		}
	}
	if !strings.Contains(c.Definition, "/blob/sha3/.github/workflows/ci.yml") {
		t.Fatalf("definition URL %q", c.Definition)
	}

	t.Run("no definition at the head commit is named, not invented", func(t *testing.T) {
		delete(f.files, "sha3:"+path)
		r, err := Run(context.Background(), f.client(), testRepo, t0.Add(-time.Hour), t0.Add(10*24*time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		w := findWorkflow(t, r, path)
		if len(w.CriticalPath) != 0 || !strings.Contains(w.Critical.Unavailable, "sha3") {
			t.Fatalf("want unavailable naming the commit: %+v", w.Critical)
		}
		if !hasUnavailable(r, path) {
			t.Fatalf("report must list the unavailable definition: %+v", r.Unavailable)
		}
	})
	t.Run("expression job names cannot be matched", func(t *testing.T) {
		f.files["sha3:"+path] = "on: push\njobs:\n  a:\n    name: build ${{ inputs.x }}\n    runs-on: x\n"
		r, err := Run(context.Background(), f.client(), testRepo, t0.Add(-time.Hour), t0.Add(10*24*time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		w := findWorkflow(t, r, path)
		if len(w.CriticalPath) != 0 || !strings.Contains(w.Critical.Unavailable, "expression") {
			t.Fatalf("want an explicit expression-name reason: %+v", w.Critical)
		}
	})
}

func hasUnavailable(r *Report, what string) bool {
	for _, u := range r.Unavailable {
		if strings.Contains(u.What, what) {
			return true
		}
	}
	return false
}

func TestMeasureAPIErrorNamesEndpoint(t *testing.T) {
	f := &fakeGitHub{files: map[string]string{}}
	run := mkRun(9, ".github/workflows/ci.yml", "push", "main", "t", "success", 1, t0, time.Minute)
	f.runs = append(f.runs, run) // jobs endpoint will 404
	_, err := Run(context.Background(), f.client(), testRepo, t0.Add(-time.Hour), t0.Add(48*time.Hour))
	if err == nil || !strings.Contains(err.Error(), "/actions/runs/9/attempts/1/jobs") {
		t.Fatalf("want error naming the endpoint, got %v", err)
	}
}

func TestMeasureExcludesIncompleteRunsByName(t *testing.T) {
	f := &fakeGitHub{files: map[string]string{}}
	done := mkRun(1, ".github/workflows/ci.yml", "push", "main", "t", "success", 1, t0, time.Minute)
	f.addRun(done, mkJob(done, 11, "a", "success", t0, 0, time.Minute))
	live := mkRun(2, ".github/workflows/ci.yml", "push", "main", "t", "", 1, t0, time.Minute)
	live.Status = "in_progress"
	f.addRun(live)
	r, err := Run(context.Background(), f.client(), testRepo, t0.Add(-time.Hour), t0.Add(48*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if w := findWorkflow(t, r, ".github/workflows/ci.yml"); w.Runs != 1 {
		t.Fatalf("in-progress run was counted: %+v", w)
	}
	found := false
	for _, u := range r.Unavailable {
		for _, e := range u.Evidence {
			if e.RunID == 2 {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("excluded run must be named: %+v", r.Unavailable)
	}
}

// layered serves measure's own recorded fixtures (workflow files at run head
// commits) first, then the shared Task 2 snapshot.
type layered []http.RoundTripper

func (l layered) RoundTrip(req *http.Request) (*http.Response, error) {
	var last error
	for _, rt := range l {
		resp, err := rt.RoundTrip(req)
		if err == nil {
			return resp, nil
		}
		last = err
	}
	return nil, last
}

var (
	fixtureSince = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	fixtureNow   = time.Date(2026, 9, 29, 18, 0, 0, 0, time.UTC)
)

func tackleReport(t *testing.T) *Report {
	t.Helper()
	c := gh.New(gh.Options{BaseURL: "https://api.github.com", NoCache: true, HTTP: &http.Client{Transport: layered{
		ghtest.Replay(filepath.Join("testdata", "tackle")),
		ghtest.Replay(filepath.Join("..", "ghtest", "testdata", "tackle")),
	}}})
	r, err := Run(context.Background(), c, "schuettc/tackle", fixtureSince, fixtureNow)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestMeasureTackleFixture(t *testing.T) {
	r := tackleReport(t)
	var buf bytes.Buffer
	if err := WriteJSON(&buf, r); err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "tackle.measure.json")
	if *update {
		if err := os.WriteFile(golden, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Fatalf("measure JSON differs from %s; rerun with -update and review the diff", golden)
	}
	// The contract round-trips: compare and record read this JSON back.
	var back Report
	if err := json.Unmarshal(want, &back); err != nil || back.Schema != 1 || len(back.Workflows) != len(r.Workflows) {
		t.Fatalf("golden does not round-trip: %v", err)
	}
	// Determinism: a second run is byte-identical.
	var again bytes.Buffer
	if err := WriteJSON(&again, tackleReport(t)); err != nil || !bytes.Equal(again.Bytes(), buf.Bytes()) {
		t.Fatal("measure output is not deterministic")
	}
	if strings.Contains(buf.String(), "null") {
		t.Fatal("JSON must use empty arrays, not null")
	}
}

func allEvidence(r *Report) []signal.Evidence {
	var out []signal.Evidence
	for _, w := range r.Workflows {
		out = append(out, w.Evidence...)
		for _, j := range w.Jobs {
			out = append(out, j.Evidence...)
			out = append(out, j.QueueEvidence...)
			for _, s := range j.Steps {
				out = append(out, s.Evidence...)
			}
		}
		for _, n := range w.Critical.Nodes {
			out = append(out, n.Evidence...)
		}
		out = append(out, w.Critical.Rejected...)
		if h := w.Definitions; h != nil {
			out = append(out, h.Unread...)
			out = append(out, h.OtherUnread...)
			for _, c := range h.Changes {
				out = append(out, c.Evidence...)
			}
			for _, v := range h.BranchOnly {
				out = append(out, v.Evidence...)
			}
		}
	}
	for _, s := range r.Sinks {
		out = append(out, s.Evidence...)
	}
	for _, s := range r.Signals {
		out = append(out, s.Evidence...)
	}
	for _, u := range r.Unavailable {
		out = append(out, u.Evidence...)
	}
	return out
}

// TestEveryNumberHasEvidence: every sink, signal and aggregate cites at least
// one source with a URL.
func TestEveryNumberHasEvidence(t *testing.T) {
	check := func(t *testing.T, r *Report) {
		t.Helper()
		has := func(what string, ev []signal.Evidence) {
			t.Helper()
			if len(ev) == 0 {
				t.Errorf("%s has no evidence", what)
			}
			for _, e := range ev {
				if e.URL == "" || e.Kind == "" {
					t.Errorf("%s has evidence without URL or kind: %+v", what, e)
				}
			}
		}
		for _, s := range r.Sinks {
			has("sink "+s.Step, s.Evidence)
		}
		for _, s := range r.Signals {
			has("signal "+s.ID+" "+s.Summary, s.Evidence)
			if !signal.Known(s.ID) {
				t.Errorf("unregistered signal %s", s.ID)
			}
		}
		for _, w := range r.Workflows {
			has("workflow "+w.Path, w.Evidence)
			for _, j := range w.Jobs {
				has("job "+j.Name, j.Evidence)
				has("job queue "+j.Name, j.QueueEvidence)
				for _, s := range j.Steps {
					has("step "+s.Name, s.Evidence)
				}
			}
			for _, n := range w.Critical.Nodes {
				has("critical path node "+n.Job, n.Evidence)
			}
		}
	}
	t.Run("tackle", func(t *testing.T) {
		r := tackleReport(t)
		if len(r.Sinks) == 0 || len(r.Workflows) == 0 {
			t.Fatal("fixture should produce workflows and sinks")
		}
		check(t, r)
	})
	t.Run("synthetic signals", func(t *testing.T) {
		runs, refs := w6Fixture(t, "push", "push", checkJob("checks", "x", "make test", ""), checkJob("mirror", "x", "make test", ""))
		runs[0].Run.RunAttempt = 2
		runs[0].Attempts = []gh.Attempt{{RunAttempt: 1, Conclusion: "failure"}, {RunAttempt: 2, Conclusion: "success", RunStartedAt: t0.Add(-11 * time.Minute), UpdatedAt: t0.Add(10 * time.Minute)}}
		runs[0].Jobs[0].CreatedAt = t0.Add(-10 * time.Minute)
		for i := range runs[0].Jobs {
			runs[0].Jobs[i].RunAttempt = 2
		}
		r := analyzeWith(t, runs, nil, refs)
		for _, id := range []string{"rerun", "queue-time", "tree-rechecked", "slow-step"} {
			if len(signalsWithID(r, id)) == 0 {
				t.Errorf("expected %s in synthetic report", id)
			}
		}
		check(t, r)
	})
}

func TestRenderMarkdown(t *testing.T) {
	for _, r := range []*Report{tackleReport(t), contradictingReport(t)} {
		renderChecks(t, r)
	}
	empty := RenderMarkdown(&Report{Schema: 1, Repo: "o/r", Since: t0, Until: t0, Workflows: []WorkflowStats{}, Sinks: []Sink{}, Signals: []signal.Signal{}, Unavailable: []Unavailable{}})
	if !strings.Contains(empty, "No completed runs") {
		t.Fatalf("an empty window must say so:\n%s", empty)
	}
}

// contradictingReport has a critical path with some rejected runs, and a
// tree-rechecked signal.
func contradictingReport(t *testing.T) *Report {
	t.Helper()
	runs, refs := w6Fixture(t, "push", "push", checkJob("checks", "x", "make test", ""), checkJob("mirror", "x", "make test", ""))
	def := mkDef(t, depPath, "sha2", "on: push\njobs:\n"+checkJob("mirror", "x", "make test", "")+"  deploy:\n    needs: mirror\n    runs-on: x\n    steps: [{run: ./deploy}]\n")
	bad := mkRun(9, depPath, "push", "main", "tree-z", "success", 1, t0, time.Minute)
	runs = append(runs, RunData{Run: bad, Jobs: []gh.Job{mkJob(bad, 91, "mirror", "success", t0, 0, time.Minute), mkJob(bad, 92, "deploy", "success", t0, 0, time.Minute)}})
	r := analyzeWith(t, runs, map[string]Definition{depPath: def}, refs)
	w := findWorkflow(t, r, depPath)
	if len(w.Critical.Rejected) != 1 || w.Critical.BasisRuns != 1 || len(signalsWithID(r, "tree-rechecked")) != 1 {
		t.Fatalf("fixture: %+v", w.Critical)
	}
	return r
}

func renderChecks(t *testing.T, r *Report) {
	t.Helper()
	md := RenderMarkdown(r)
	for _, e := range allEvidence(r) {
		if !strings.Contains(md, e.URL) {
			t.Fatalf("markdown lacks evidence URL %s", e.URL)
		}
	}
	for _, w := range r.Workflows {
		if !strings.Contains(md, w.Path) {
			t.Fatalf("markdown lacks workflow %s", w.Path)
		}
	}
	lines := strings.Split(strings.TrimRight(md, "\n"), "\n")
	for i, l := range lines {
		if !strings.HasPrefix(l, "#") {
			continue
		}
		j := i + 1
		for j < len(lines) && strings.TrimSpace(lines[j]) == "" {
			j++
		}
		if j == len(lines) || (strings.HasPrefix(lines[j], "#") && strings.Count(lines[j], "#") <= strings.Count(l, "#")) {
			t.Fatalf("empty section %q", l)
		}
	}
}

// TestMeasurePartialRerunCarriedOverJobs: "re-run failed jobs" lists the
// jobs it did not re-run under the new attempt, with new ids but the earlier
// attempt's timestamps. They are not timed as the final attempt, and are cited.
func TestMeasurePartialRerunCarriedOverJobs(t *testing.T) {
	const path = ".github/workflows/ci.yml"
	start1, start2 := t0, t0.Add(2*time.Hour)
	run := mkRun(8, path, "push", "main", "t", "success", 2, start1, 0)
	run.RunStartedAt, run.UpdatedAt = start2, start2.Add(time.Hour) // updated_at far after the work
	rerun := mkJob(run, 81, "flaky", "success", start2.Add(5*time.Second), 3*time.Second, 2*time.Minute, mkSteps(start2, "Set up job", "test")...)
	carried := mkJob(run, 82, "lint", "success", start1.Add(10*time.Second), 0, 20*time.Minute, mkSteps(start1, "Set up job", "lint")...)
	carried.CreatedAt = start2.Add(3 * time.Second) // created with the new attempt, timed in the old one
	stale := mkJob(run, 83, "other", "success", start2.Add(10*time.Second), 0, time.Minute)
	stale.RunAttempt = 1 // listed under attempt 2 but claims attempt 1
	r := analyze(t, []RunData{{Run: run, Jobs: []gh.Job{rerun, carried, stale}, Attempts: []gh.Attempt{
		{RunAttempt: 1, Conclusion: "failure", RunStartedAt: start1, UpdatedAt: start1.Add(21 * time.Minute)},
		{RunAttempt: 2, Conclusion: "success", RunStartedAt: start2, UpdatedAt: start2.Add(time.Hour)},
	}}}, nil)
	w := findWorkflow(t, r, path)
	if len(w.Jobs) != 1 || w.Jobs[0].Name != "flaky" {
		t.Fatalf("only the re-run job may be timed: %+v", w.Jobs)
	}
	if w.P50 != 2*time.Minute+5*time.Second || w.CarriedOverJobs != 2 {
		t.Fatalf("run duration %v (want start→last timed job), carried over %d", w.P50, w.CarriedOverJobs)
	}
	cited := map[int64]string{}
	for _, e := range w.Evidence {
		if e.JobID != 0 {
			cited[e.JobID] = e.Note
		}
	}
	if !strings.Contains(cited[82], "carried over") || !strings.Contains(cited[83], "attempt 1") {
		t.Fatalf("excluded jobs must be cited with the reason: %+v", cited)
	}
	for _, s := range r.Sinks {
		if s.Job == "lint" {
			t.Fatalf("carried-over step timed: %+v", s)
		}
	}
}

func TestMeasureRunEndEstimatedWithoutJobs(t *testing.T) {
	run := mkRun(1, ".github/workflows/ci.yml", "push", "main", "t", "failure", 1, t0, 3*time.Minute)
	r := analyze(t, []RunData{{Run: run, Jobs: []gh.Job{mkJob(run, 11, "a", "skipped", t0, 0, 0)}}}, nil)
	w := findWorkflow(t, r, ".github/workflows/ci.yml")
	if w.EstimatedEnds != 1 || w.P50 != 3*time.Minute || !strings.Contains(w.Evidence[0].Note, "updated_at") {
		t.Fatalf("a run with no timed job must label its end as estimated: %+v", w)
	}
}

func TestStepsKeyedByOccurrence(t *testing.T) {
	run := mkRun(1, ".github/workflows/ci.yml", "push", "main", "t", "success", 1, t0, 5*time.Minute)
	r := analyze(t, []RunData{{Run: run, Jobs: []gh.Job{mkJob(run, 11, "a", "success", t0, 0, 3*time.Minute, mkSteps(t0, "Run make", "Run make")...)}}}, nil)
	steps := findJob(t, findWorkflow(t, r, ".github/workflows/ci.yml"), "a").Steps
	if len(steps) != 2 || steps[0].Runs != 1 || steps[1].Runs != 1 || steps[0].Occurrence != 1 || steps[1].Occurrence != 2 {
		t.Fatalf("two steps with one name in one run are two steps, each run once: %+v", steps)
	}
}

func TestQueueEvidenceIsTyped(t *testing.T) {
	run := mkRun(1, ".github/workflows/ci.yml", "push", "main", "t", "success", 1, t0, 20*time.Minute)
	r := analyze(t, []RunData{{Run: run, Jobs: []gh.Job{mkJob(run, 11, "a", "success", t0.Add(6*time.Minute), 6*time.Minute, time.Minute)}}}, nil)
	j := findJob(t, findWorkflow(t, r, ".github/workflows/ci.yml"), "a")
	if len(j.QueueEvidence) != 2 || j.QueueEvidence[0].JobID != 11 {
		t.Fatalf("queue evidence: %+v", j)
	}
	q := signalsWithID(r, "queue-time")
	if len(q) != 1 || len(q[0].Evidence) != 2 {
		t.Fatalf("queue-time cites the queue samples: %+v", q)
	}
}

// TestCriticalPathRejectsContradictingRuns (I3/CE3): the definition says b
// needs a, but the runs ran a and b in parallel. Those runs contradict the
// edge, so they are rejected and named, and no path is invented.
func TestCriticalPathRejectsContradictingRuns(t *testing.T) {
	const path = ".github/workflows/ci.yml"
	f := &fakeGitHub{files: map[string]string{}}
	for i := int64(1); i <= 3; i++ {
		start := t0.Add(time.Duration(i) * time.Hour)
		run := mkRun(i, path, "push", "main", "t", "success", 1, start, 5*time.Minute)
		f.addRun(run, mkJob(run, i*10+1, "a", "success", start, 0, 5*time.Minute), mkJob(run, i*10+2, "b", "success", start, 0, 5*time.Minute))
	}
	f.files["sha3:"+path] = "on: push\njobs:\n  a:\n    runs-on: x\n  b:\n    needs: a\n    runs-on: x\n"
	r, err := Run(context.Background(), f.client(), testRepo, t0.Add(-time.Hour), t0.Add(48*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	c := findWorkflow(t, r, path).Critical
	if c.BasisRuns != 0 || len(c.Nodes) != 0 || len(c.Rejected) != 3 || !strings.Contains(c.Unavailable, "contradict") {
		t.Fatalf("contradicting runs must be rejected with evidence: %+v", c)
	}
	if !strings.Contains(c.Rejected[0].Note, "b started") {
		t.Fatalf("rejection names the edge: %+v", c.Rejected[0])
	}
}

// TestCriticalPathDefinitionSource (M5): the definition comes from the latest
// default-branch run, not a later pull request's unmerged edit, and says so.
func TestCriticalPathDefinitionSource(t *testing.T) {
	const path = ".github/workflows/ci.yml"
	f := &fakeGitHub{files: map[string]string{}}
	main := mkRun(1, path, "push", "main", "t", "success", 1, t0, 3*time.Minute)
	f.addRun(main, mkJob(main, 11, "a", "success", t0, 0, time.Minute))
	pr := mkRun(2, path, "pull_request", "feature", "t2", "success", 1, t0.Add(time.Hour), 3*time.Minute)
	f.addRun(pr, mkJob(pr, 21, "a", "success", t0.Add(time.Hour), 0, time.Minute))
	f.files["sha1:"+path] = "on: push\njobs:\n  a:\n    runs-on: x\n"
	r, err := Run(context.Background(), f.client(), testRepo, t0.Add(-time.Hour), t0.Add(48*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	c := findWorkflow(t, r, path).Critical
	if c.Ref != "sha1" || c.Branch != "main" || c.SourceRunID != 1 || c.BasisRuns != 2 {
		t.Fatalf("definition source: %+v", c)
	}
	// Each run's own file is read once for the definition history (sha2 is
	// absent, so it is unread); the critical path still uses sha1's.
	if f.hits["/repos/o/r/contents/"+path] != 2 {
		t.Fatalf("each (path, commit) must be read once: %v", f.hits)
	}
}

// TestMeasureTreeRecheckedReadsEachRunsDefinition: W6 identity is checked
// against each run's own workflow file at its head commit, fetched once per
// path and commit.
func TestMeasureTreeRecheckedReadsEachRunsDefinition(t *testing.T) {
	f := &fakeGitHub{files: map[string]string{}}
	runs, _ := w6Fixture(t, "push", "push", checkJob("checks", "x", "make test", ""), checkJob("mirror", "x", "make test", ""))
	for _, rd := range runs {
		f.addRun(rd.Run, rd.Jobs...)
	}
	f.files["sha1:"+ciPath] = "on: push\njobs:\n" + checkJob("checks", "x", "make test", "")
	f.files["sha2:"+depPath] = "on: push\njobs:\n" + checkJob("mirror", "x", "make test", "") + "  deploy:\n    needs: mirror\n    runs-on: x\n    steps: [{run: ./deploy}]\n"
	r, err := Run(context.Background(), f.client(), testRepo, t0.Add(-time.Hour), t0.Add(48*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(signalsWithID(r, "tree-rechecked")) != 1 {
		t.Fatalf("signals %+v unavailable %+v", r.Signals, r.Unavailable)
	}
	if f.hits["/repos/o/r/contents/"+ciPath] != 1 || f.hits["/repos/o/r/contents/"+depPath] != 1 {
		t.Fatalf("each (path, commit) is read once: %v", f.hits)
	}
}

// TestQueueExcludesNeedsWaitInRecordedRuns pins, against the real recorded
// producer, the assumption queue-time rests on: GitHub creates a dependent
// job when its needs resolve, so created→started is runner wait, not the
// upstream job's runtime. (The public snapshot's GitHub Pages workflow has
// deploy needing build.)
func TestQueueExcludesNeedsWaitInRecordedRuns(t *testing.T) {
	c := gh.New(gh.Options{BaseURL: "https://api.github.com", NoCache: true, HTTP: &http.Client{Transport: ghtest.Replay(filepath.Join("..", "ghtest", "testdata", "tackle"))}})
	runs, err := c.Runs(context.Background(), "schuettc/tackle", fixtureSince)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, r := range runs {
		if !strings.HasPrefix(r.Path, "dynamic/pages/") {
			continue
		}
		jobs, err := c.Jobs(context.Background(), "schuettc/tackle", r.ID, r.RunAttempt)
		if err != nil {
			t.Fatal(err)
		}
		byName := map[string]gh.Job{}
		for _, j := range jobs {
			byName[j.Name] = j
		}
		build, okB := byName["build"]
		deploy, okD := byName["deploy"]
		if !okB || !okD || !executed(build) || !executed(deploy) {
			continue
		}
		checked++
		if deploy.CreatedAt.Before(build.CompletedAt) {
			t.Fatalf("run %d: dependent job created %s before its need completed %s; queue would include needs wait", r.ID, deploy.CreatedAt, build.CompletedAt)
		}
	}
	if checked < 10 {
		t.Fatalf("only %d recorded dependent-job pairs checked", checked)
	}
}

// untilBound is the creation time of the window's last recorded run (release
// run 36606519472). An --until at that instant excludes it; one second later
// includes it. Both listings are the recorded window's bodies: GitHub's
// created range is inclusive, so it returns the same 138 runs for either bound.
var untilBound = time.Date(2026, 9, 29, 17, 39, 43, 0, time.UTC)

const boundRun = int64(36606519472)

// listingQueries records the created filter of every runs listing request.
type listingQueries struct {
	base    http.RoundTripper
	created []string
}

func (l *listingQueries) RoundTrip(r *http.Request) (*http.Response, error) {
	if strings.HasSuffix(r.URL.Path, "/actions/runs") {
		l.created = append(l.created, r.URL.Query().Get("created"))
	}
	return l.base.RoundTrip(r)
}

func tackleWindow(t *testing.T, until time.Time) *Report {
	t.Helper()
	l := &listingQueries{base: layered{
		ghtest.Replay(filepath.Join("testdata", "tackle")),
		ghtest.Replay(filepath.Join("..", "ghtest", "testdata", "tackle")),
	}}
	c := gh.New(gh.Options{BaseURL: "https://api.github.com", NoCache: true, HTTP: &http.Client{Transport: l}})
	r, err := Run(context.Background(), c, "schuettc/tackle", fixtureSince, until)
	if err != nil {
		t.Fatal(err)
	}
	// The listing itself is bounded: runs after --until are never requested.
	want := fixtureSince.Format(time.RFC3339Nano) + ".." + until.Format(time.RFC3339Nano)
	if len(l.created) == 0 {
		t.Fatal("no runs listing requested")
	}
	for _, got := range l.created {
		if got != want {
			t.Fatalf("runs listing created filters %q, want every page bounded %q", l.created, want)
		}
	}
	return r
}

func citesRun(r *Report, id int64) bool {
	for _, w := range r.Workflows {
		for _, e := range w.Evidence {
			if e.RunID == id {
				return true
			}
		}
	}
	return false
}

// TestRunUntilIsExclusive: a run created at --until is outside the window, one
// created just before it is inside, and the report carries the bounds.
func TestRunUntilIsExclusive(t *testing.T) {
	at := tackleWindow(t, untilBound)
	after := tackleWindow(t, untilBound.Add(time.Second))
	if citesRun(at, boundRun) {
		t.Fatalf("run %d created at --until was measured", boundRun)
	}
	if !citesRun(after, boundRun) {
		t.Fatalf("run %d created before --until was not measured", boundRun)
	}
	if !at.Since.Equal(fixtureSince) || !at.Until.Equal(untilBound) || !after.Until.Equal(untilBound.Add(time.Second)) {
		t.Fatalf("bounds: %v..%v and %v", at.Since, at.Until, after.Until)
	}
	release := ".github/workflows/release.yml"
	if got, want := findWorkflow(t, at, release).Runs, findWorkflow(t, after, release).Runs-1; got != want {
		t.Fatalf("release runs %d at --until, want %d", got, want)
	}
}
