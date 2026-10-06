package main

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/schuettc/muda/internal/scan"
	"github.com/schuettc/muda/internal/signal"
)

// TestScanHelpNamesEveryScanSignal keeps `muda scan --help` in step with the
// registry: every signal whose From is "scan" must be named as a whole word.
func TestScanHelpNamesEveryScanSignal(t *testing.T) {
	var help, errs bytes.Buffer
	if code := newApp(fixtureEnv(t)).Dispatch([]string{"scan", "--help"}, &help, &errs); code != 0 {
		t.Fatalf("help exit %d: %s", code, errs.String())
	}
	n := 0
	for _, id := range signal.IDs() {
		if signal.Registry[id].From != "scan" {
			continue
		}
		n++
		if !regexp.MustCompile(`(^|[^a-z-])` + regexp.QuoteMeta(id) + `([^a-z-]|$)`).MatchString(help.String()) {
			t.Errorf("scan --help does not name scan signal %q", id)
		}
	}
	if n == 0 {
		t.Fatal("registry has no scan signals; the check proves nothing")
	}
}

func TestScanCommand(t *testing.T) {
	env := fixtureEnv(t)
	var out, errs bytes.Buffer
	if code := newApp(env).Dispatch([]string{"scan", "--since", "2026-09-01", "--no-cache"}, &out, &errs); code != 0 {
		t.Fatalf("exit %d: %s", code, errs.String())
	}
	var report scan.Report
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Repo != "schuettc/tackle" || report.Ref != "main" || len(report.Files) == 0 || len(report.Signals) == 0 {
		t.Fatalf("report: %+v", report)
	}
	for _, s := range report.Signals {
		for _, e := range s.Evidence {
			if !strings.HasPrefix(e.URL, "https://github.com/schuettc/tackle/blob/main/") {
				t.Errorf("URL %q", e.URL)
			}
		}
	}
	out.Reset()
	if code := newApp(env).Dispatch([]string{"scan", "--since", "2026-09-01", "--format", "md"}, &out, &errs); code != 0 || !strings.HasPrefix(out.String(), "# muda scan: schuettc/tackle") {
		t.Fatalf("md exit %d: %s", code, errs.String())
	}
	for _, s := range report.Signals {
		for _, e := range s.Evidence {
			if !strings.Contains(out.String(), e.URL) {
				t.Errorf("Markdown missing %s", e.URL)
			}
		}
	}
}
func TestScanCommandErrors(t *testing.T) {
	for _, tc := range []struct {
		args []string
		code int
	}{
		{[]string{"scan", "--format", "xml"}, 2},
		{[]string{"scan", "extra"}, 2},
		{[]string{"scan", "--repo", "o/missing"}, 1},
	} {
		var out, errs bytes.Buffer
		if code := newApp(fixtureEnv(t)).Dispatch(tc.args, &out, &errs); code != tc.code || errs.Len() == 0 || out.Len() != 0 {
			t.Fatalf("%v code %d stderr %s", tc.args, code, errs.String())
		}
	}
}

func TestScanNoTokenRuntimeCode(t *testing.T) {
	env := fixtureEnv(t)
	env.Getenv = func(string) string { return "" }
	var out, errs bytes.Buffer
	if code := newApp(env).Dispatch([]string{"scan"}, &out, &errs); code != 1 || !strings.Contains(errs.String(), "GH_TOKEN") || out.Len() != 0 {
		t.Fatalf("exit %d: %s", code, errs.String())
	}
}
