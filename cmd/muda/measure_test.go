package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/schuettc/muda/internal/cli"
	"github.com/schuettc/muda/internal/ghtest"
)

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

func fixtureEnv(t *testing.T) cli.Env {
	t.Helper()
	root := filepath.Join("..", "..", "internal")
	return cli.Env{
		Getenv:    func(k string) string { return map[string]string{"GH_TOKEN": "fixture-token"}[k] },
		GHToken:   func() (string, error) { return "", errors.New("gh not used") },
		GitRemote: func() (string, error) { return "git@github.com:schuettc/tackle.git", nil },
		Now:       func() time.Time { return time.Date(2026, 9, 29, 18, 0, 0, 0, time.UTC) },
		BaseURL:   "https://api.github.com",
		CacheDir:  t.TempDir(),
		HTTP: &http.Client{Transport: layered{
			ghtest.Replay(filepath.Join(root, "measure", "testdata", "tackle")),
			ghtest.Replay(filepath.Join(root, "ghtest", "testdata", "tackle")),
		}},
	}
}

// TestMeasureCommand drives `muda measure` through the real app wiring.
func TestMeasureCommand(t *testing.T) {
	app := newApp(fixtureEnv(t))
	var out, errs bytes.Buffer
	if code := app.Dispatch([]string{"measure", "--since", "2026-09-01", "--no-cache"}, &out, &errs); code != 0 {
		t.Fatalf("exit %d: %s", code, errs.String())
	}
	want, err := os.ReadFile(filepath.Join("..", "..", "internal", "measure", "testdata", "tackle.measure.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), want) {
		t.Fatal("`muda measure` JSON differs from the measure golden")
	}
	if strings.Contains(errs.String()+out.String(), "fixture-token") {
		t.Fatal("token leaked into output")
	}

	out.Reset()
	if code := app.Dispatch([]string{"measure", "--repo", "schuettc/tackle", "--since", "2026-09-01", "--format", "md"}, &out, &errs); code != 0 {
		t.Fatalf("md exit %d: %s", code, errs.String())
	}
	if !strings.HasPrefix(out.String(), "# muda measure: schuettc/tackle") {
		t.Fatalf("markdown: %.200s", out.String())
	}
}

func TestMeasureCommandErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		env  func(*cli.Env)
		code int
		want string
	}{
		{"bad format is usage", []string{"measure", "--format", "xml"}, nil, 2, "xml"},
		{"stray argument is usage", []string{"measure", "extra"}, nil, 2, "extra"},
		{"no token is a runtime error", []string{"measure", "--since", "2026-09-01"}, func(e *cli.Env) {
			e.Getenv = func(string) string { return "" }
		}, 1, "GH_TOKEN"},
		{"API failure names the endpoint", []string{"measure", "--repo", "o/missing", "--since", "2026-09-01"}, nil, 1, "/repos/o/missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := fixtureEnv(t)
			if tc.env != nil {
				tc.env(&env)
			}
			var out, errs bytes.Buffer
			code := newApp(env).Dispatch(tc.args, &out, &errs)
			if code != tc.code || !strings.Contains(errs.String(), tc.want) {
				t.Fatalf("exit %d, stderr %q; want %d mentioning %q", code, errs.String(), tc.code, tc.want)
			}
			if out.Len() != 0 {
				t.Fatalf("no report on failure, got %q", out.String())
			}
		})
	}
}

// TestPastWindowBaseline drives a past [since, until) window through the real
// app: measure and scan over 2026-09-01..2026-09-15 (well before the fixture
// clock), then record baseline with both reports, then record check.
func TestPastWindowBaseline(t *testing.T) {
	recordRepo(t, `{"since":"2026-09-01","until":"2026-09-15"}`)
	env := absFixtureEnv(t, nil)
	window := []string{"--since", "2026-09-01", "--until", "2026-09-15", "--no-cache"}
	var measureOut, scanOut, errs bytes.Buffer
	if code := newApp(env).Dispatch(append([]string{"measure"}, window...), &measureOut, &errs); code != 0 {
		t.Fatalf("measure exit %d: %s", code, errs.String())
	}
	var m struct {
		Since, Until time.Time
		Workflows    []struct {
			Path     string
			Runs     int
			Evidence []struct {
				RunID int64 `json:"run_id"`
			}
		}
	}
	if err := json.Unmarshal(measureOut.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if !m.Since.Equal(time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)) || !m.Until.Equal(time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)) || len(m.Workflows) == 0 {
		t.Fatalf("measure window %v..%v, %d workflows", m.Since, m.Until, len(m.Workflows))
	}
	// 36606519472 is the snapshot's last run (2026-09-29); it is outside.
	for _, w := range m.Workflows {
		for _, e := range w.Evidence {
			if e.RunID == 36606519472 {
				t.Fatalf("%s cites a run after --until", w.Path)
			}
		}
	}
	if code := newApp(env).Dispatch(append([]string{"scan"}, window...), &scanOut, &errs); code != 0 {
		t.Fatalf("scan exit %d: %s", code, errs.String())
	}
	if !strings.Contains(scanOut.String(), `"since": "2026-09-01T00:00:00Z"`) || !strings.Contains(scanOut.String(), `"until": "2026-09-15T00:00:00Z"`) {
		t.Fatalf("scan report lacks its window: %.300s", scanOut.String())
	}
	// notices states the same whole window, in JSON and in Markdown.
	var noticesOut bytes.Buffer
	if code := newApp(env).Dispatch(append([]string{"notices"}, window...), &noticesOut, &errs); code != 0 {
		t.Fatalf("notices exit %d: %s", code, errs.String())
	}
	if !strings.Contains(noticesOut.String(), `"since": "2026-09-01T00:00:00Z"`) || !strings.Contains(noticesOut.String(), `"until": "2026-09-15T00:00:00Z"`) {
		t.Fatalf("notices report lacks its window: %.300s", noticesOut.String())
	}
	for _, cmd := range []string{"notices", "scan"} {
		var md bytes.Buffer
		if code := newApp(env).Dispatch(append([]string{cmd, "--format", "md"}, window...), &md, &errs); code != 0 || !strings.Contains(md.String(), "[2026-09-01T00:00:00Z, 2026-09-15T00:00:00Z)") {
			t.Fatalf("%s md exit %d lacks the window: %.300s %s", cmd, code, md.String(), errs.String())
		}
	}
	var out bytes.Buffer
	args := []string{"record", "baseline", "--file", writeInput(t, measureOut.String()), "--scan", writeInput(t, scanOut.String())}
	if code := NewApp().Dispatch(args, &out, &errs); code != 0 {
		t.Fatalf("record baseline exit %d: %s %s", code, out.String(), errs.String())
	}
	out.Reset()
	if code := NewApp().Dispatch([]string{"record", "check"}, &out, &errs); code != 0 {
		t.Fatalf("record check exit %d: %s %s", code, out.String(), errs.String())
	}
}

// A bad or future --until is a usage error (exit 2) with no report, on the
// window commands and on those that ignore the window alike.
func TestUntilUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{"measure", "--since", "2026-09-01", "--until", "2026-09-30"},
		{"measure", "--since", "2026-09-15", "--until", "2026-09-15"},
		{"measure", "--since", "2026-09-15", "--until", "2026-09-01"},
		{"notices", "--since", "2026-09-01", "--until", "soon"},
		{"scan", "--since", "2026-09-01", "--until", "2026-09-29T18:00:01Z"},
		{"gates", "--until", "2027-01-01"},
		{"logs", "1", "--until", "2027-01-01"},
		{"compare", "--before", "2026-09-01..2026-09-10", "--after", "2026-09-10..2026-09-20", "--until", "2027-01-01"},
	} {
		var out, errs bytes.Buffer
		if code := newApp(fixtureEnv(t)).Dispatch(args, &out, &errs); code != 2 || !strings.Contains(errs.String(), "--until") || out.Len() != 0 {
			t.Errorf("%v: exit %d stderr %q out %d bytes", args, code, errs.String(), out.Len())
		}
	}
}
