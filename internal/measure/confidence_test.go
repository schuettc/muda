package measure

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/schuettc/muda/internal/gh"
)

// TestConfidenceBoundaries: the label is by sample size only, low below 10,
// medium from 10 to 29, high from 30.
func TestConfidenceBoundaries(t *testing.T) {
	for n, want := range map[int]string{0: "low", 1: "low", 9: "low", 10: "medium", 29: "medium", 30: "high", 500: "high"} {
		if got := Confidence(n); got != want {
			t.Errorf("Confidence(%d) = %q, want %q", n, got, want)
		}
	}
}

// confidenceReport has one workflow of n runs, each with one job "a" running
// one step "make".
func confidenceReport(t *testing.T, n int) *Report {
	t.Helper()
	var runs []RunData
	for i := 1; i <= n; i++ {
		start := t0.Add(time.Duration(i) * time.Minute)
		run := mkRun(int64(i), ".github/workflows/ci.yml", "push", "main", fmt.Sprintf("t%d", i), "success", 1, start, time.Minute)
		runs = append(runs, RunData{Run: run, Jobs: []gh.Job{mkJob(run, int64(i)*10, "a", "success", start, 0, time.Minute, mkSteps(start, "make")...)}})
	}
	return analyze(t, runs, nil)
}

// TestConfidenceLabelsStats: every workflow, job, step and sink carries the
// label for its own run count. The label informs; the numbers are all there.
func TestConfidenceLabelsStats(t *testing.T) {
	for n, want := range map[int]string{9: "low", 10: "medium", 29: "medium", 30: "high"} {
		r := confidenceReport(t, n)
		w := findWorkflow(t, r, ".github/workflows/ci.yml")
		j := findJob(t, w, "a")
		if w.Runs != n || w.Confidence != want || j.Confidence != want || len(j.Steps) != 1 || j.Steps[0].Confidence != want {
			t.Fatalf("n=%d: workflow %d %q, job %q, steps %+v", n, w.Runs, w.Confidence, j.Confidence, j.Steps)
		}
		if w.P50 == 0 || j.P50 == 0 || j.Steps[0].P50 == 0 {
			t.Fatalf("n=%d: a label must never hide a number", n)
		}
		if len(r.Sinks) != 1 || r.Sinks[0].Confidence != want {
			t.Fatalf("n=%d: sinks %+v", n, r.Sinks)
		}
		md := RenderMarkdown(r)
		for _, s := range []string{
			fmt.Sprintf("| %d | %s |", n, want),
			fmt.Sprintf("- Runs: %d (%s confidence: n=%d)", n, want, n),
			fmt.Sprintf("- `a`: %d runs (%s confidence)", n, want),
			fmt.Sprintf("  - step `make`: %d runs (%s confidence)", n, want),
			fmt.Sprintf("× %d runs (%s confidence)", n, want),
		} {
			if !strings.Contains(md, s) {
				t.Fatalf("n=%d: markdown lacks %q:\n%s", n, s, md)
			}
		}
	}
	if md := RenderMarkdown(confidenceReport(t, 1)); !strings.Contains(md, "Confidence is by sample size") {
		t.Fatalf("markdown must say what the label means:\n%s", md)
	}
}
