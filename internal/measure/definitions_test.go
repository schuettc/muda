package measure

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/schuettc/muda/internal/gh"
)

const (
	defOld = "on: push\njobs:\n  build:\n    runs-on: x\n    steps: [{run: make}]\n  test:\n    needs: build\n    runs-on: x\n    steps: [{run: make test}]\n"
	defNew = "on: push\njobs:\n  build:\n    runs-on: x\n    steps: [{run: make}]\n  e2e:\n    needs: build\n    runs-on: x\n    steps: [{run: make e2e}]\n"
)

// definitionFake serves five ci.yml runs a day apart, run i at head commit
// sha<i>, with files[i] its workflow file ("" for none at that commit).
func definitionFake(files ...string) *fakeGitHub {
	f := &fakeGitHub{files: map[string]string{}}
	for i, src := range files {
		id := int64(i + 1)
		start := t0.Add(time.Duration(i) * 24 * time.Hour)
		run := mkRun(id, ciPath, "push", "main", fmt.Sprintf("tree-%d", id), "success", 1, start, 3*time.Minute)
		f.addRun(run, mkJob(run, id*10, "build", "success", start, 0, time.Minute))
		if src != "" {
			f.files[fmt.Sprintf("sha%d:%s", id, ciPath)] = src
		}
	}
	return f
}

func runDefinitions(t *testing.T, f *fakeGitHub) (*Report, WorkflowStats) {
	t.Helper()
	r, err := Run(context.Background(), f.client(), testRepo, t0.Add(-time.Hour), t0.Add(10*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	w := findWorkflow(t, r, ciPath)
	if w.Definitions == nil {
		t.Fatal("no definition history")
	}
	return r, w
}

// TestDefinitionChangeMidWindow: the workflow file each run used is read at
// its head commit; the run that first used new content is reported with the
// runs before and after it and the jobs that appeared and disappeared.
func TestDefinitionChangeMidWindow(t *testing.T) {
	f := definitionFake(defOld, defOld, defNew, defNew, defNew)
	r, w := runDefinitions(t, f)
	h := w.Definitions
	if h.RunsRead != 5 || h.Versions != 2 || h.Unavailable != "" || len(h.Unread) != 0 || len(h.Changes) != 1 {
		t.Fatalf("history %+v", h)
	}
	c := h.Changes[0]
	if c.RunID != 3 || c.HeadSHA != "sha3" || !c.CreatedAt.Equal(t0.Add(48*time.Hour)) || c.Branch != "main" || c.Event != "push" {
		t.Fatalf("first run on the new definition: %+v", c)
	}
	if c.RunsBefore != 2 || c.RunsAfter != 3 || c.UnreadBetween != 0 {
		t.Fatalf("counts: %+v", c)
	}
	if !reflect.DeepEqual(c.JobsAdded, []string{"e2e"}) || !reflect.DeepEqual(c.JobsRemoved, []string{"test"}) || c.JobsUnavailable != "" {
		t.Fatalf("jobs: %+v", c)
	}
	if c.From == "" || c.To == "" || c.From == c.To {
		t.Fatalf("content hashes: %+v", c)
	}
	urls := map[string]bool{}
	for _, e := range c.Evidence {
		urls[e.URL] = true
	}
	for _, u := range []string{"https://github.com/o/r/actions/runs/3", "https://github.com/o/r/blob/sha3/" + ciPath, "https://github.com/o/r/blob/sha2/" + ciPath} {
		if !urls[u] {
			t.Fatalf("change must cite %s: %+v", u, c.Evidence)
		}
	}
	if hasUnavailable(r, "workflow file history") {
		t.Fatalf("every run's file was read: %+v", r.Unavailable)
	}
	if f.hits["/repos/o/r/contents/"+ciPath] != 5 {
		t.Fatalf("each run's (path, commit) is read once: %v", f.hits)
	}

	t.Run("one definition throughout is no change", func(t *testing.T) {
		_, w := runDefinitions(t, definitionFake(defOld, defOld, defOld))
		if h := w.Definitions; h.Versions != 1 || len(h.Changes) != 0 || h.Unavailable != "" || h.RunsRead != 3 {
			t.Fatalf("history %+v", h)
		}
	})
	t.Run("a change back is a second change", func(t *testing.T) {
		_, w := runDefinitions(t, definitionFake(defOld, defNew, defOld))
		h := w.Definitions
		if h.Versions != 2 || len(h.Changes) != 2 || h.Changes[1].RunID != 3 || h.Changes[1].RunsBefore != 2 || h.Changes[1].RunsAfter != 1 ||
			!reflect.DeepEqual(h.Changes[1].JobsAdded, []string{"test"}) {
			t.Fatalf("history %+v", h)
		}
	})
	t.Run("an unparseable version still compares by content", func(t *testing.T) {
		_, w := runDefinitions(t, definitionFake(defOld, "jobs: [\n"))
		h := w.Definitions
		if len(h.Changes) != 1 || h.Unavailable != "" || !strings.Contains(h.Changes[0].JobsUnavailable, "does not parse") ||
			len(h.Changes[0].JobsAdded) != 0 || len(h.Changes[0].JobsRemoved) != 0 {
			t.Fatalf("history %+v", h)
		}
	})
}

// TestDefinitionUnreadableIsUnavailable: a run whose workflow file cannot be
// read is cited, and the history never claims no change across it.
func TestDefinitionUnreadableIsUnavailable(t *testing.T) {
	t.Run("between two versions", func(t *testing.T) {
		r, w := runDefinitions(t, definitionFake(defOld, defOld, "", defNew, defNew))
		h := w.Definitions
		if h.RunsRead != 4 || len(h.Unread) != 1 || h.Unread[0].RunID != 3 || !strings.Contains(h.Unread[0].Note, "sha3") {
			t.Fatalf("unread run must be cited with its reason: %+v", h)
		}
		if !strings.Contains(h.Unavailable, "1 of 5") || len(h.Changes) != 1 {
			t.Fatalf("history %+v", h)
		}
		if c := h.Changes[0]; c.RunID != 4 || c.UnreadBetween != 1 || c.RunsBefore != 3 || c.RunsAfter != 2 {
			t.Fatalf("change after an unread run: %+v", c)
		}
		if !hasUnavailable(r, "workflow file history of "+ciPath) {
			t.Fatalf("report must list the gap: %+v", r.Unavailable)
		}
	})
	t.Run("no change among the runs read is not no change", func(t *testing.T) {
		r, w := runDefinitions(t, definitionFake(defOld, defOld, ""))
		h := w.Definitions
		if len(h.Changes) != 0 || h.Unavailable == "" || !hasUnavailable(r, "workflow file history of "+ciPath) {
			t.Fatalf("history %+v", h)
		}
		md := RenderMarkdown(r)
		if !strings.Contains(md, "no change among the runs read") || !strings.Contains(md, "a change among the unread runs cannot be ruled out") {
			t.Fatalf("markdown must not claim no change:\n%s", md)
		}
	})
	for event, file := range map[string]string{"pull_request_target": "base branch's", "workflow_run": "default branch's", "repository_dispatch": "default branch's"} {
		t.Run(event+" runs another branch's file, not the head's", func(t *testing.T) {
			f := definitionFake(defOld, defOld, defOld)
			f.runs[1].Event = event
			f.files["sha2:"+ciPath] = defNew // the head's file, which did not run
			r, w := runDefinitions(t, f)
			h := w.Definitions
			if len(h.Changes) != 0 || len(h.BranchOnly) != 0 || h.RunsRead != 2 || len(h.Unread) != 0 || h.Unavailable != "" ||
				len(h.OtherUnread) != 1 || h.OtherUnread[0].RunID != 2 || !strings.Contains(h.OtherUnread[0].Note, event+" runs the "+file+" workflow file") {
				t.Fatalf("history %+v", h)
			}
			if hasUnavailable(r, "workflow file history of "+ciPath) {
				t.Fatalf("every default-branch run was read: %+v", r.Unavailable)
			}
			if f.hits["/repos/o/r/contents/"+ciPath] != 2 {
				t.Fatalf("the head's file is not read for a %s run: %v", event, f.hits)
			}
		})
	}
	t.Run("no file to read", func(t *testing.T) {
		run := mkRun(1, "dynamic/pages/pages-build-deployment", "dynamic", "main", "t", "success", 1, t0, time.Minute)
		r := analyze(t, []RunData{{Run: run, Jobs: []gh.Job{mkJob(run, 11, "build", "success", t0, 0, time.Minute)}}}, nil)
		h := findWorkflow(t, r, run.Path).Definitions
		if h == nil || h.RunsRead != 0 || h.Versions != 0 || len(h.OtherUnread) != 1 || !strings.Contains(h.Unavailable, "GitHub-managed") || !hasUnavailable(r, "workflow file history of "+run.Path) {
			t.Fatalf("history %+v", h)
		}
	})
}

// TestDefinitionHistoryMarkdown: the change is rendered with its run, time,
// commit, counts and jobs, and every piece of its evidence.
func TestDefinitionHistoryMarkdown(t *testing.T) {
	r, _ := runDefinitions(t, definitionFake(defOld, defOld, defNew, defNew, defNew))
	renderChecks(t, r)
	md := RenderMarkdown(r)
	for _, s := range []string{
		"## Workflow file changes",
		"`" + ciPath + "`: 2 versions across all 5 default-branch runs; 1 change",
		"run 3 (created 2026-09-12T12:00:00Z, head sha3, push on main) first used a new version: 2 runs before it, 3 from it on; jobs added: e2e; jobs removed: test",
	} {
		if !strings.Contains(md, s) {
			t.Fatalf("markdown lacks %q:\n%s", s, md)
		}
	}
	if i, j := strings.Index(md, "## Workflow file changes"), strings.Index(md, "## Workflows"); i < 0 || j < i {
		t.Fatal("workflow file changes come before the workflow stats they qualify")
	}
	_, same := runDefinitions(t, definitionFake(defOld, defOld))
	one := &Report{Repo: "o/r", Workflows: []WorkflowStats{same}}
	if md := RenderMarkdown(one); !strings.Contains(md, "1 version across all 2 default-branch runs; no change") {
		t.Fatalf("an unchanged workflow says so:\n%s", md)
	}
}

// TestOldReportLoads: a report written before confidence labels and the
// definition history still loads, and renders them as absent, never as
// "no change" or a guessed label.
func TestOldReportLoads(t *testing.T) {
	const old = `{"schema": 1, "repo": "o/r", "since": "2026-09-01T00:00:00Z", "until": "2026-09-29T00:00:00Z",
  "workflows": [{"path": ".github/workflows/ci.yml", "name": "CI", "role": "ci", "role_reason": "r", "events": ["push"],
    "runs": 3, "reruns": 0, "failures": 0, "p50_ns": 60000000000, "p90_ns": 60000000000, "runs_end_estimated": 0,
    "queue_p50_ns": 0, "queue_p90_ns": 0, "success_rate": 1, "carried_over_jobs": 0,
    "jobs": [{"name": "a", "runs": 3, "p50_ns": 1, "p90_ns": 1, "queue_p50_ns": 0, "queue_p90_ns": 0,
      "steps": [{"name": "make", "occurrence": 1, "runs": 3, "p50_ns": 1, "p90_ns": 1, "evidence": []}], "evidence": [], "queue_evidence": []}],
    "critical_path": [], "critical_path_detail": {"basis_runs": 0, "unmatched_runs": 0, "rejected": [], "p50_ns": 0, "nodes": [], "unavailable": "x"},
    "evidence": []}],
  "sinks": [{"workflow": ".github/workflows/ci.yml", "job": "a", "step": "make", "occurrence": 1, "runs": 3, "p50_ns": 1, "p90_ns": 1, "weight_ns": 3, "evidence": []}],
  "signals": [], "unavailable": []}`
	var r Report
	if err := json.Unmarshal([]byte(old), &r); err != nil {
		t.Fatal(err)
	}
	w := r.Workflows[0]
	if r.Schema != Schema || w.Runs != 3 || w.Definitions != nil || w.Confidence != "" || r.Sinks[0].Runs != 3 {
		t.Fatalf("old report: %+v", r)
	}
	md := RenderMarkdown(&r)
	for _, s := range []string{"`.github/workflows/ci.yml`: not recorded in this report", "3 runs (unlabelled confidence)"} {
		if !strings.Contains(md, s) {
			t.Fatalf("markdown lacks %q:\n%s", s, md)
		}
	}
	if strings.Contains(md, "no change") {
		t.Fatalf("an old report must not claim no change:\n%s", md)
	}
}

// TestDefinitionReadsUseTheCache: a second measure of the same window reads
// no workflow file again; each is cached at the run's reported head commit.
func TestDefinitionReadsUseTheCache(t *testing.T) {
	f := definitionFake(defOld, defOld, defNew)
	c := gh.New(gh.Options{BaseURL: "https://api.github.com", CacheDir: t.TempDir(), HTTP: &http.Client{Transport: f}})
	for range 2 {
		if _, err := Run(context.Background(), c, testRepo, t0.Add(-time.Hour), t0.Add(10*24*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if n := f.hits["/repos/o/r/contents/"+ciPath]; n != 3 {
		t.Fatalf("3 run head commits, read %d times over two measures", n)
	}
}

// TestDefinitionHistoryFollowsTheDefaultBranch: changes are placed on the
// default branch's push, schedule and workflow_dispatch runs only. Other
// branches' versions are branch-only versions, never window changes, and a
// pull request's is approximate: it is read at the head, but GitHub ran the
// merge ref.
func TestDefinitionHistoryFollowsTheDefaultBranch(t *testing.T) {
	t.Run("interleaved branches are not changes", func(t *testing.T) {
		f := definitionFake(defOld, defNew, defOld, defNew)
		for _, i := range []int{1, 3} {
			f.runs[i].Event, f.runs[i].HeadBranch = "pull_request", "feature"
		}
		r, w := runDefinitions(t, f)
		h := w.Definitions
		if len(h.Changes) != 0 || h.Versions != 1 || h.RunsRead != 2 || h.BranchRuns != 2 || h.Branch != "main" || h.Unavailable != "" || len(h.BranchOnly) != 1 {
			t.Fatalf("history %+v", h)
		}
		v := h.BranchOnly[0]
		if v.Runs != 2 || v.FirstRunID != 2 || v.FirstHeadSHA != "sha2" || !reflect.DeepEqual(v.Branches, []string{"feature"}) ||
			!reflect.DeepEqual(v.Events, []string{"pull_request"}) || !v.Approximate || !strings.Contains(v.ApproximateReason, "merge ref") {
			t.Fatalf("branch-only version %+v", v)
		}
		renderChecks(t, r)
		md := RenderMarkdown(r)
		for _, s := range []string{"1 version across all 2 default-branch runs; no change",
			"branch-only version", "on feature (pull\\_request): 2 runs, first run 2", "approximate:", "not a window change"} {
			if !strings.Contains(md, s) {
				t.Fatalf("markdown lacks %q:\n%s", s, md)
			}
		}
	})
	t.Run("a dependabot-style pull request changes the window only when it merges", func(t *testing.T) {
		f := definitionFake(defOld, defOld, defNew, defOld, defNew)
		f.runs[2].Event, f.runs[2].HeadBranch = "pull_request", "dependabot/github_actions/actions/checkout-5"
		_, w := runDefinitions(t, f)
		h := w.Definitions
		if len(h.Changes) != 1 || len(h.BranchOnly) != 0 || h.BranchRuns != 4 {
			t.Fatalf("history %+v", h)
		}
		if c := h.Changes[0]; c.RunID != 5 || c.Branch != "main" || c.Event != "push" || c.RunsBefore != 4 || c.RunsAfter != 1 {
			t.Fatalf("the change is placed at the merge's default-branch run: %+v", c)
		}
	})
	t.Run("no default-branch run cannot place a change", func(t *testing.T) {
		f := definitionFake(defOld, defNew)
		for i := range f.runs {
			f.runs[i].HeadBranch = "v1." + fmt.Sprint(i) // tag pushes
		}
		r, w := runDefinitions(t, f)
		h := w.Definitions
		if len(h.Changes) != 0 || h.BranchRuns != 0 || len(h.BranchOnly) != 2 || h.BranchOnly[0].Approximate ||
			!strings.Contains(h.Unavailable, "no push, schedule or workflow_dispatch run on main") {
			t.Fatalf("history %+v", h)
		}
		// Every file was read: nothing failed, so the report's Unavailable
		// list stays for reads that did; the history and md say it.
		if hasUnavailable(r, "workflow file history of "+ciPath) {
			t.Fatalf("no read failed: %+v", r.Unavailable)
		}
		if md := RenderMarkdown(r); !strings.Contains(md, "unavailable: no push, schedule or workflow\\_dispatch run on main") {
			t.Fatalf("markdown must say the history is unavailable:\n%s", md)
		}
	})
}

// TestDefinitionReadErrorDegradesTheHistory: a read failure other than 404
// makes that run's history unavailable with the reason; measure goes on. The
// critical path's own definition read still fails the command.
func TestDefinitionReadErrorDegradesTheHistory(t *testing.T) {
	f := definitionFake(defOld, defOld, defOld)
	f.files["sha2:"+ciPath] = serverError
	r, w := runDefinitions(t, f)
	h := w.Definitions
	if len(h.Unread) != 1 || h.Unread[0].RunID != 2 || !strings.Contains(h.Unread[0].Note, "read failed") || !strings.Contains(h.Unavailable, "1 of 3") ||
		!hasUnavailable(r, "workflow file history of "+ciPath) {
		t.Fatalf("history %+v", h)
	}
	f.files["sha3:"+ciPath] = serverError // the critical-path source
	if _, err := Run(context.Background(), f.client(), testRepo, t0.Add(-time.Hour), t0.Add(10*24*time.Hour)); err == nil {
		t.Fatal("a failed read of the critical path's definition must fail the command")
	}
}
