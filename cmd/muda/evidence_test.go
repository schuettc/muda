package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestEvidenceCommands(t *testing.T) {
	for _, command := range []string{"gates", "notices"} {
		for _, format := range []string{"json", "md"} {
			t.Run(command+format, func(t *testing.T) {
				var out, errs bytes.Buffer
				code := newApp(fixtureEnv(t)).Dispatch([]string{command, "--since", "2026-09-01", "--format", format, "--no-cache"}, &out, &errs)
				if code != 0 {
					t.Fatalf("%d %s", code, errs.String())
				}
				if format == "json" {
					var r map[string]any
					if err := json.Unmarshal(out.Bytes(), &r); err != nil || r["schema"] != float64(1) {
						t.Fatalf("%s %v", out.String(), err)
					}
					keys := []string{"groups", "signals", "unavailable"}
					if command == "gates" {
						keys = []string{"gates", "unavailable", "workflows", "security", "rulesets", "environments"}
					}
					for _, key := range keys {
						if _, ok := r[key].([]any); !ok {
							t.Errorf("product array %s missing or null", key)
						}
					}
				} else if !strings.HasPrefix(out.String(), "# muda "+command) {
					t.Fatal(out.String())
				}
				if strings.Contains(out.String()+errs.String(), "fixture-token") {
					t.Fatal("credential leak")
				}
			})
		}
	}
}
func TestLogsCommand(t *testing.T) {
	for _, args := range [][]string{{"logs", "36606519472", "--since", "2026-09-01"}, {"logs", "36606519472", "--job", "109536828086", "--step", "Operating System", "--since", "2026-09-01"}} {
		var out, errs bytes.Buffer
		code := newApp(fixtureEnv(t)).Dispatch(args, &out, &errs)
		if code != 0 {
			t.Fatalf("%d %s", code, errs.String())
		}
		var r map[string]any
		if err := json.Unmarshal(out.Bytes(), &r); err != nil || r["schema"] != float64(1) {
			t.Fatalf("%s %v", out.String(), err)
		}
	}
}
func TestEvidenceErrors(t *testing.T) {
	for _, tc := range []struct {
		args []string
		code int
	}{{[]string{"logs", "1", "--grep", "["}, 2}, {[]string{"logs", "bad"}, 2}, {[]string{"gates", "extra"}, 2}, {[]string{"notices", "--format", "xml"}, 2}, {[]string{"logs", "1", "--job", "-1"}, 2}, {[]string{"gates", "--repo", "o/missing"}, 1}, {[]string{"logs", "36606519472", "--job", "11", "--since", "2026-09-01"}, 1}} {
		var out, errs bytes.Buffer
		if got := newApp(fixtureEnv(t)).Dispatch(tc.args, &out, &errs); got != tc.code {
			t.Fatalf("%v exit %d want %d: %s", tc.args, got, tc.code, errs.String())
		}
	}
}

func TestLogMarkdownBounds(t *testing.T) {
	// --since after the run started (and before now) does not constrain logs.
	var out, errs bytes.Buffer
	args := []string{"logs", "36606519472", "--job", "109536828086", "--step", "Operating System", "--format", "md", "--since", "2026-09-29T17:50:00Z"}
	if code := newApp(fixtureEnv(t)).Dispatch(args, &out, &errs); code != 0 {
		t.Fatalf("%d %s", code, errs.String())
	}
	for _, text := range []string{"2026-09-29T17:39:56.918535Z", "2026-09-29T17:39:56.918705Z", "https://github.com/schuettc/tackle/actions/runs/36606519472/job/109536828086"} {
		if !strings.Contains(out.String(), text) {
			t.Errorf("missing bounds/evidence %s", text)
		}
	}
}

// Future --since and a missing origin are usage errors (exit 2) with no
// credential in the output.
func TestUsageErrorsExitTwo(t *testing.T) {
	env := fixtureEnv(t)
	for _, args := range [][]string{{"measure", "--since", "2026-09-30", "--no-cache"}, {"notices", "--since", "2027-01-01T00:00:00Z"}} {
		var out, errs bytes.Buffer
		if code := newApp(env).Dispatch(args, &out, &errs); code != 2 {
			t.Errorf("%v: exit %d %s", args, code, errs.String())
		}
	}
	env.GitRemote = func() (string, error) { return "", errors.New("exit status 128") }
	var out, errs bytes.Buffer
	if code := newApp(env).Dispatch([]string{"gates"}, &out, &errs); code != 2 || !strings.Contains(errs.String(), "--repo OWNER/NAME") || strings.Contains(out.String()+errs.String(), "fixture-token") {
		t.Fatalf("missing origin: exit %d %s", code, errs.String())
	}
}
