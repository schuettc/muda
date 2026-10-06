package scan_test

import (
	"bytes"
	"github.com/schuettc/muda/internal/scan"
	"github.com/schuettc/muda/internal/workflow"
	"testing"
	"time"
)

func parseText(t *testing.T, path, text string) *workflow.File {
	t.Helper()
	f, err := workflow.Parse(path, []byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func TestSplitTriggers(t *testing.T) {
	for _, text := range []string{"push: {paths: ['src/**']}\n  pull_request: {}", "push: {}\n  pull_request: {paths-ignore: ['docs/**']}"} {
		f := parseText(t, "a.yml", "on:\n  "+text+"\njobs: {}\n")
		requireSignal(t, scan.Files([]*workflow.File{f}, map[string]time.Duration{"a.yml": 5 * time.Minute}), "no-path-filter")
		if len(scan.Skipped([]*workflow.File{f}, nil)) != 1 {
			t.Error("split trigger must be skipped without durations")
		}
	}
}
func TestCacheRegressions(t *testing.T) {
	for _, tc := range []struct {
		name, runner, setup, cmd string
		want                     bool
	}{
		{"bare-go", "ubuntu-24.04", "", "go mod download", true},
		{"npm-not-pip", "ubuntu-24.04", "- uses: actions/cache@v4\n        with: {path: '~/.npm'}\n      ", "pip install x", true},
		{"uv-hosted-auto", "ubuntu-24.04", "- uses: astral-sh/setup-uv@v5\n      ", "uv sync", false},
		{"uv-false", "ubuntu-24.04", "- uses: astral-sh/setup-uv@v5\n        with: {enable-cache: 'false'}\n      ", "uv sync", true},
		{"uv-true-self", "self-hosted", "- uses: astral-sh/setup-uv@v5\n        with: {enable-cache: 'true'}\n      ", "uv sync", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := parseText(t, "a.yml", "on: push\njobs:\n  build:\n    runs-on: "+tc.runner+"\n    steps:\n      "+tc.setup+"- run: "+tc.cmd+"\n")
			sigs := scan.Files([]*workflow.File{f}, nil)
			if tc.want {
				requireSignal(t, sigs, "uncached-install")
			} else {
				requireNoSignal(t, sigs, "uncached-install")
			}
		})
	}
}
func TestDuplicateRegressions(t *testing.T) {
	a := parseText(t, "a.yml", "on: {push: {branches-ignore: [main]}}\njobs:\n  lint:\n    steps: [{run: check}]\n")
	b := parseText(t, "b.yml", "on: {push: {branches: [main]}}\njobs:\n  lint:\n    steps: [{run: check}]\n")
	requireNoSignal(t, scan.Files([]*workflow.File{a, b}, nil), "duplicate-check-set")
	a = parseText(t, "a.yml", "on: push\njobs:\n  lint:\n    steps: [{run: check}]\n  test:\n    steps: [{run: check}]\n")
	if n := len(signalsWithID(scan.Files([]*workflow.File{a, b}, nil), "duplicate-check-set")); n != 2 {
		t.Fatalf("pairs=%d want 2", n)
	}
}
func TestEmptyArrays(t *testing.T) {
	var out bytes.Buffer
	if err := scan.WriteJSON(&out, &scan.Report{Schema: 1, Signals: scan.Files(nil, nil)}); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out.Bytes(), []byte(": null")) {
		t.Fatal(out.String())
	}
}
