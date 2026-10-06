package scan

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/schuettc/muda/internal/gh"
	"github.com/schuettc/muda/internal/workflow"
)

func TestFinalAttemptAndLast20(t *testing.T) {
	var finalCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/o/r/actions/runs":
			if r.URL.Query().Get("created") != "2026-09-01T00:00:00Z..2026-09-29T18:00:00Z" {
				t.Error("[since, until) window not honored")
			}
			_, _ = fmt.Fprint(w, `{"total_count":21,"workflow_runs":[`)
			for n := 1; n <= 21; n++ {
				if n > 1 {
					_, _ = fmt.Fprint(w, ",")
				}
				_, _ = fmt.Fprintf(w, `{"id":%d,"path":"a.yml","status":"completed","run_attempt":2,"created_at":"2026-09-01T00:00:00Z","run_started_at":"2026-09-01T00:00:00Z","updated_at":"2026-09-%02dT00:10:00Z"}`, n, n+1)
			}
			_, _ = fmt.Fprint(w, `]}`)
		case strings.HasSuffix(r.URL.Path, "/attempts/1"):
			_, _ = fmt.Fprint(w, `{"run_attempt":1,"status":"completed","run_started_at":"2026-09-01T00:00:00Z"}`)
		case strings.HasSuffix(r.URL.Path, "/attempts/2"):
			finalCalls.Add(1)
			if strings.Contains(r.URL.Path, "/runs/1/") {
				t.Error("oldest run outside last20 fetched")
			}
			_, _ = fmt.Fprint(w, `{"run_attempt":2,"status":"completed","run_started_at":"2026-09-22T00:00:00Z"}`)
		case strings.HasSuffix(r.URL.Path, "/jobs"):
			// Carried-over completion must not inflate final-attempt wall time.
			_, _ = fmt.Fprint(w, `{"jobs":[{"run_attempt":1,"status":"completed","started_at":"2026-09-01T00:00:00Z","completed_at":"2026-09-23T00:00:00Z"},{"run_attempt":2,"status":"completed","started_at":"2026-09-22T00:00:00Z","completed_at":"2026-09-22T00:06:00Z"}]}`)
		default:
			t.Errorf("unexpected endpoint %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := gh.New(gh.Options{BaseURL: srv.URL, NoCache: true})
	ds, notes, err := fetchDurations(context.Background(), c, "o/r", time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 29, 18, 0, 0, 0, time.UTC), []*workflow.File{{Path: "a.yml"}})
	if finalCalls.Load() != 20 {
		t.Errorf("final calls %d want20", finalCalls.Load())
	}
	if err != nil || len(notes) != 0 || ds["a.yml"] != 6*time.Minute {
		t.Fatalf("durations=%v notes=%v err=%v", ds, notes, err)
	}
}

func TestUnknownCacheCoverage(t *testing.T) {
	for _, setup := range []string{"- uses: actions/cache@v4\n        with: {path: '${{ inputs.cache }}'}", "- uses: astral-sh/setup-uv@v5"} {
		f, err := workflow.Parse("a.yml", []byte("on: push\njobs:\n  build:\n    runs-on: self-hosted\n    steps:\n      "+setup+"\n      - run: uv sync\n"))
		if err != nil {
			t.Fatal(err)
		}
		notes := cacheUnavailable([]*workflow.File{f})
		if len(notes) != 1 || !strings.Contains(notes[0].What, "uv cache coverage") {
			t.Fatalf("unknown cache not named: %v", notes)
		}
	}
}

func TestDurationEstimateAndUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/jobs") {
			_, _ = fmt.Fprint(w, `{"jobs":[]}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	c := gh.New(gh.Options{BaseURL: srv.URL, NoCache: true})
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	r := gh.Run{ID: 1, RunAttempt: 1, RunStartedAt: start, UpdatedAt: start.Add(7 * time.Minute)}
	d, note, err := finalDuration(context.Background(), c, "o/r", r)
	if d != 7*time.Minute || err != nil || !strings.Contains(note, "UpdatedAt estimate") {
		t.Fatalf("%v %q %v", d, note, err)
	}
	r.RunAttempt = 2
	if _, _, err := finalDuration(context.Background(), c, "o/r", r); err == nil {
		t.Fatal("missing final attempt must not reuse original start")
	}
	r.RunAttempt = 1
	r.RunStartedAt = time.Time{}
	if _, _, err := finalDuration(context.Background(), c, "o/r", r); err == nil {
		t.Fatal("missing start must be unavailable")
	}
}

func TestMixedCachePathsRetainUncertainty(t *testing.T) {
	f, err := workflow.Parse("a.yml", []byte(`on: push
jobs:
  build:
    runs-on: ubuntu-24.04
    env:
      PIP_CACHE_DIR: /opt/custom-pip-cache
    steps:
      - uses: actions/cache@v4
        with:
          path: |
            ~/.npm
            /opt/custom-pip-cache
      - run: pip install example
      - run: npm ci
`))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range Files([]*workflow.File{f}, nil) {
		if s.ID == "uncached-install" {
			t.Errorf("mixed cache paths must not assert uncached install: %s", s.Summary)
		}
	}
	notes := cacheUnavailable([]*workflow.File{f})
	if len(notes) != 1 || notes[0].What != "a.yml job build pip cache coverage" {
		t.Errorf("want named pip uncertainty only, got %v", notes)
	}
	if ci := jobCacheInfo(f.Jobs["build"]); !ci.covered["npm"] {
		t.Error("literal npm coverage must remain proven")
	}
}
