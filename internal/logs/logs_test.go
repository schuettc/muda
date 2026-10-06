package logs

import (
	"context"
	"fmt"
	"github.com/schuettc/muda/internal/gh"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLogsSplitGroups(t *testing.T) {
	paths, _ := filepath.Glob("../ghtest/testdata/tackle/*_logs_*.json")
	if len(paths) != 1 {
		t.Fatal(paths)
	}
	raw, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	steps := Split(string(raw))
	if len(steps) < 10 {
		t.Fatal(len(steps))
	}
	found := false
	for _, s := range steps {
		if s.Name == "Operating System" {
			found = true
		}
		if s.Start.IsZero() || s.End.Before(s.Start) {
			t.Fatal(s)
		}
		for _, l := range s.Lines {
			if strings.Contains(l, "##[group]") {
				t.Fatal(l)
			}
		}
	}
	if !found {
		t.Fatal("missing group")
	}
}
func TestLogsGrep(t *testing.T) {
	steps := Split("2026-09-01T00:00:00Z ##[group]Test\n2026-09-01T00:00:01Z \x1b[31mFAIL\x1b[0m\n2026-09-01T00:00:02Z pass\n2026-09-01T00:00:03Z ##[endgroup]\n")
	got, err := Filter(steps, "Test", "FAIL")
	if err != nil || len(got) != 1 || len(got[0].Lines) != 1 || got[0].Lines[0] != "FAIL" {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := Filter(steps, "", "["); err == nil {
		t.Fatal("invalid regexp accepted")
	}
}

func TestFetchFinalAttemptAndMembership(t *testing.T) {
	logCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/o/r/actions/runs/1":
			_, _ = fmt.Fprint(w, `{"id":1,"run_attempt":2,"created_at":"2000-01-01T00:00:00Z"}`)
		case "/repos/o/r/actions/runs/1/attempts/2/jobs":
			_, _ = fmt.Fprint(w, `{"jobs":[{"id":22,"run_id":1,"run_attempt":2}]}`)
		case "/repos/o/r/actions/jobs/22":
			_, _ = fmt.Fprint(w, `{"status":"completed"}`)
		case "/repos/o/r/actions/jobs/22/logs":
			logCalls++
			_, _ = fmt.Fprint(w, "2026-09-01T00:00:00Z ##[group]Test\n2026-09-01T00:00:01Z success\n")
		default:
			t.Errorf("unexpected %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	c := gh.New(gh.Options{BaseURL: server.URL, HTTP: server.Client(), NoCache: true})
	steps, err := Fetch(context.Background(), c, "o/r", 1, 22, "Test", "success")
	if err != nil || len(steps) != 1 {
		t.Fatalf("%+v %v", steps, err)
	}
	if _, err := Fetch(context.Background(), c, "o/r", 1, 21, "", ""); err == nil {
		t.Fatal("wrong attempt job accepted")
	}
	if logCalls != 1 {
		t.Fatal("foreign job log fetched")
	}
	if _, err := Fetch(context.Background(), c, "o/r", 1, 22, "", "["); err == nil {
		t.Fatal("invalid grep accepted")
	}
}
