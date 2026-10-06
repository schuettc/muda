package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/schuettc/muda/internal/cli"
	"github.com/schuettc/muda/internal/compare"
	"github.com/schuettc/muda/internal/gh"
	"github.com/schuettc/muda/internal/ghtest"
	"github.com/schuettc/muda/internal/record"
	"github.com/schuettc/muda/internal/scan"
	"github.com/schuettc/muda/internal/signal"
)

// compareReplay supplies bounded listing/direct run responses using only the
// already recorded public tackle listing. All jobs/files still use strict replay.
type compareReplay struct {
	base      http.RoundTripper
	runs      []gh.Run
	t         *testing.T
	listCalls int
}

func (f *compareReplay) RoundTrip(r *http.Request) (*http.Response, error) {
	prefix := "/repos/schuettc/tackle/actions/runs"
	if r.URL.Path == prefix {
		f.listCalls++
		parts := strings.Split(r.URL.Query().Get("created"), "..")
		if len(parts) != 2 {
			return nil, fmt.Errorf("unbounded history request: %s", r.URL)
		}
		since, e := time.Parse(time.RFC3339, parts[0])
		if e != nil {
			return nil, e
		}
		until, e := time.Parse(time.RFC3339, parts[1])
		if e != nil {
			return nil, e
		}
		kept := []gh.Run{}
		seen := map[int64]bool{}
		for _, run := range f.runs {
			if !seen[run.ID] && !run.CreatedAt.Before(since) && !run.CreatedAt.After(until) {
				seen[run.ID] = true
				kept = append(kept, run)
			}
		}
		return fixtureResponse(r, map[string]any{"total_count": len(kept), "workflow_runs": kept}), nil
	}
	if idText, ok := strings.CutPrefix(r.URL.Path, prefix+"/"); ok && !strings.Contains(idText, "/") {
		for _, run := range f.runs {
			if fmt.Sprint(run.ID) == idText {
				return fixtureResponse(r, run), nil
			}
		}
	}
	return f.base.RoundTrip(r)
}
func fixtureResponse(r *http.Request, v any) *http.Response {
	b, _ := json.Marshal(v)
	return &http.Response{StatusCode: 200, Status: "200 OK", Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(b)), Request: r}
}
func compareEnv(t *testing.T) (*compareReplay, *http.Client) {
	t.Helper()
	env := fixtureEnv(t)
	f := &compareReplay{base: env.HTTP.Transport, t: t}
	paths, err := filepath.Glob(filepath.Join("..", "..", "internal", "ghtest", "testdata", "tackle", "*runs_created*json"))
	if err != nil {
		t.Fatal(err)
	}
	more, err := filepath.Glob(filepath.Join("..", "..", "internal", "ghtest", "testdata", "tackle", "*repositories*actions_runs*page_2*json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range append(paths, more...) {
		if strings.HasSuffix(p, ".meta.json") {
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Runs []gh.Run `json:"workflow_runs"`
		}
		if err = json.Unmarshal(b, &doc); err != nil {
			t.Fatal(err)
		}
		f.runs = append(f.runs, doc.Runs...)
	}
	if len(f.runs) == 0 {
		t.Fatal("no public run fixtures")
	}
	return f, &http.Client{Transport: f}
}
func TestCompareAppWindowsAndOldRunIDs(t *testing.T) {
	f, httpClient := compareEnv(t)
	env := fixtureEnv(t)
	env.HTTP = httpClient
	app := newApp(env)
	var out, errs bytes.Buffer
	args := []string{"compare", "--before", "2026-09-01T00:00:00Z..2026-09-29T18:00:00Z", "--after", "2026-08-31T00:00:00Z..2026-09-29T18:00:00Z", "--no-cache"}
	if code := app.Dispatch(args, &out, &errs); code != 0 {
		t.Fatalf("exit %d %s", code, errs.String())
	}
	var r compare.Result
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if r.Schema != 1 || len(r.Workflows) == 0 || len(r.Steps) == 0 || f.listCalls != 2 {
		t.Fatalf("bad comparison: %s", out.String())
	}
	assertCompareGolden(t, "compare.windows.json", out.Bytes())
	first := append([]byte(nil), out.Bytes()...)
	out.Reset()
	if code := app.Dispatch(args, &out, &errs); code != 0 || !bytes.Equal(first, out.Bytes()) {
		t.Fatalf("nondeterministic: %d %s", code, errs.String())
	}
	// A September run selected explicitly must survive a current-date 30d
	// default even when the injected clock advances years beyond its creation.
	env.Now = func() time.Time { return time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC) }
	f.listCalls = 0
	out.Reset()
	errs.Reset()
	args = []string{"compare", "--before-runs", "36606519472", "--after-runs", "36606519472", "--format", "md", "--no-cache"}
	if code := newApp(env).Dispatch(args, &out, &errs); code != 0 {
		t.Fatalf("old run IDs exit %d %s", code, errs.String())
	}
	if f.listCalls != 0 || !strings.Contains(out.String(), "https://github.com/schuettc/tackle/actions/runs/36606519472") {
		t.Fatalf("direct selection/evidence: %s", out.String())
	}
	out.Reset()
	errs.Reset()
	args = []string{"compare", "--before-runs", "36606519472", "--after-runs", "36606519472", "--no-cache"}
	if code := newApp(env).Dispatch(args, &out, &errs); code != 0 {
		t.Fatalf("JSON direct selection: %d %s", code, errs.String())
	}
	assertCompareGolden(t, "compare.selected.json", out.Bytes())

}
func TestCompareAppExitCodes(t *testing.T) {
	for _, args := range [][]string{{"compare", "--before", "", "--before-runs", "1", "--after-runs", "2"}, {"compare"}, {"compare", "--before", "2026-09-02..2026-09-01", "--after-runs", "1"}, {"compare", "--before-runs", "1", "--before", "2026-09-01..2026-09-02", "--after-runs", "2"}, {"compare", "--before-runs", "0", "--after-runs", "2"}} {
		var out, errs bytes.Buffer
		if code := newApp(fixtureEnv(t)).Dispatch(args, &out, &errs); code != 2 {
			t.Fatalf("%v exit %d %s", args, code, errs.String())
		}
	}
	var out, errs bytes.Buffer
	if code := newApp(fixtureEnv(t)).Dispatch([]string{"compare", "--before-runs", "1", "--after-runs", "2"}, &out, &errs); code != 1 || out.Len() != 0 {
		t.Fatalf("runtime: %d %s", code, errs.String())
	}
	env := fixtureEnv(t)
	root := t.TempDir()
	s := &record.Store{Root: root}
	if err := s.Init(record.Settings{Roles: map[string]string{"ci.yml": "verify"}, Stacks: []string{"go"}, Window: record.Window{Since: "2026-09-01", Until: "2026-09-15"}, MudaVersion: "dev"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetGatesJSON([]byte(`{"schema":1,"repo":"schuettc/tackle","security":[],"workflows":[],"gates":[],"unavailable":[]}`)); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, ".muda", "gates.toml")
	saved, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errs.Reset()
	code := newApp(env).Dispatch([]string{"compare", "--before-runs", "36606519472", "--after-runs", "36606519472", "--gates-file", file}, &out, &errs)
	if code != 1 || !strings.Contains(out.String(), `"status": "UNVERIFIED"`) {
		t.Fatalf("gate failure must report first: %d %s %s", code, out.String(), errs.String())
	}
	after, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(saved, after) {
		t.Fatal("compare wrote baseline")
	}
}

func TestCompareMissingGateFileReportsMarkdown(t *testing.T) {
	var out, errs bytes.Buffer
	args := []string{"compare", "--before-runs", "36606519472", "--after-runs", "36606519472", "--gates-file", filepath.Join(t.TempDir(), "absent.toml"), "--format", "md"}
	if code := newApp(fixtureEnv(t)).Dispatch(args, &out, &errs); code != 1 || !strings.Contains(out.String(), "UNVERIFIED") || !strings.Contains(out.String(), "absent.toml") {
		t.Fatalf("%d %s %s", code, out.String(), errs.String())
	}
}

func TestCompareMissingRecordReportsJSON(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	env := fixtureEnv(t)
	// Replay paths must remain relative to the fixture directory, so this test
	// injects a standard gates path rather than relocating the replay consumer.
	var out, errs bytes.Buffer
	args := []string{"compare", "--before-runs", "36606519472", "--after-runs", "36606519472", "--gates-file", filepath.Join(root, ".muda", "gates.toml")}
	if code := newApp(env).Dispatch(args, &out, &errs); code != 1 || !strings.Contains(out.String(), `"status": "UNVERIFIED"`) {
		t.Fatalf("%d %s %s", code, out.String(), errs.String())
	}
}

func TestCompareGatesOutsideGitStillReports(t *testing.T) {
	env := fixtureEnv(t)
	root, err := filepath.Abs(filepath.Join("..", "..", "internal"))
	if err != nil {
		t.Fatal(err)
	}
	env.HTTP = &http.Client{Transport: layered{ghtest.Replay(filepath.Join(root, "measure", "testdata", "tackle")), ghtest.Replay(filepath.Join(root, "ghtest", "testdata", "tackle"))}}
	t.Chdir(t.TempDir())
	var out, errs bytes.Buffer
	args := []string{"compare", "--before-runs", "36606519472", "--after-runs", "36606519472", "--gates"}
	if code := newApp(env).Dispatch(args, &out, &errs); code != 1 || !strings.Contains(out.String(), `"status": "UNVERIFIED"`) {
		t.Fatalf("%d %s %s", code, out.String(), errs.String())
	}
}

func assertCompareGolden(t *testing.T, name string, data []byte) {
	t.Helper()
	golden := filepath.Join("testdata", name)
	if os.Getenv("UPDATE_COMPARE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(golden), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(want, data) {
		t.Fatalf("compare JSON differs from replay golden %s", name)
	}
}

func TestCompareGatesFileExemptionContract(t *testing.T) {
	env := fixtureEnv(t)
	var help, errs bytes.Buffer
	if code := newApp(env).Dispatch([]string{"compare", "--help"}, &help, &errs); code != 0 {
		t.Fatalf("help: %d %s", code, errs.String())
	}
	for _, text := range []string{"Arbitrary overrides disable exemptions", "that record's confined exemptions", "explicit acceptance, not proof"} {
		if !strings.Contains(help.String(), text) {
			t.Fatalf("contract absent from help: %s", help.String())
		}
	}
	root := t.TempDir()
	s := &record.Store{Root: root}
	if err := s.Init(record.Settings{Roles: map[string]string{"ci.yml": "verify"}, Stacks: []string{"go"}, Window: record.Window{Since: "2026-09-01", Until: "2026-09-15"}, MudaVersion: "dev"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetGatesJSON([]byte(`{"schema":1,"repo":"schuettc/tackle","security":[],"workflows":[],"gates":[],"unavailable":[]}`)); err != nil {
		t.Fatal(err)
	}
	today := time.Now().UTC()
	if err := s.Exempt(record.Exemption{ID: "E-policy", Waste: "overprocessing", Location: "gate:policy:security", Reason: "explicit acceptance for test", AgreedBy: "owner", Date: today.Format("2006-01-02"), ReviewBy: today.AddDate(0, 0, 7).Format("2006-01-02")}); err != nil {
		t.Fatal(err)
	}
	canonical := filepath.Join(root, ".muda", "gates.toml")
	original, err := os.ReadFile(canonical)
	if err != nil {
		t.Fatal(err)
	}
	override := filepath.Join(root, "baseline.toml")
	if err := os.WriteFile(override, original, 0600); err != nil {
		t.Fatal(err)
	}
	// A copied arbitrary override does not borrow exemptions from a nearby
	// standard record. Selecting the canonical file uses its own confined store.
	for _, tc := range []struct {
		path   string
		exempt bool
	}{{canonical, true}, {override, false}} {
		var out, errs bytes.Buffer
		code := newApp(env).Dispatch([]string{"compare", "--before-runs", "36606519472", "--after-runs", "36606519472", "--gates-file", tc.path}, &out, &errs)
		if code != 1 {
			t.Fatalf("other missing proof should still fail: %d %s", code, errs.String())
		}
		var result compare.Result
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, id := range result.Gates.Exempted {
			if id == "policy:security" {
				found = true
			}
		}
		if found != tc.exempt {
			t.Fatalf("%s: exemptions %v; want applied=%v", tc.path, result.Gates.Exempted, tc.exempt)
		}
		after, err := os.ReadFile(tc.path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(original, after) {
			t.Fatal("compare wrote inventory")
		}
	}
}

// absFixtureEnv is fixtureEnv with absolute replay paths, so a test can run
// from inside a temporary record.
func absFixtureEnv(t *testing.T, tap func(*http.Request)) cli.Env {
	t.Helper()
	if fixtureRootErr != nil {
		t.Fatal(fixtureRootErr)
	}
	env := fixtureEnv(t)
	env.HTTP = &http.Client{Transport: tapping{tap, layered{ghtest.Replay(filepath.Join(fixtureRoot, "measure", "testdata", "tackle")), ghtest.Replay(filepath.Join(fixtureRoot, "ghtest", "testdata", "tackle"))}}}
	return env
}

// fixtureRoot is resolved at package init, before any test changes directory.
var fixtureRoot, fixtureRootErr = filepath.Abs(filepath.Join("..", "..", "internal"))

type tapping struct {
	tap  func(*http.Request)
	base http.RoundTripper
}

func (t tapping) RoundTrip(r *http.Request) (*http.Response, error) {
	if t.tap != nil {
		t.tap(r)
	}
	return t.base.RoundTrip(r)
}

// scanRecord makes a record in a temporary repository root, optionally with
// a scan.json, and runs the test from inside it.
func scanRecord(t *testing.T, scanJSON []byte) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	s := &record.Store{Root: root}
	if err := s.Init(record.Settings{Roles: map[string]string{"verify.yml": "verify"}, Stacks: []string{"go"}, Window: record.Window{Since: "2026-09-01", Until: "2026-09-15"}, MudaVersion: "dev"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetGatesJSON([]byte(`{"schema":1,"repo":"schuettc/tackle","security":[],"workflows":[],"gates":[],"unavailable":[]}`)); err != nil {
		t.Fatal(err)
	}
	if scanJSON != nil {
		if err := os.WriteFile(filepath.Join(root, ".muda", "scan.json"), scanJSON, 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(root)
	return root
}

// freshTackleScan is the real producer's output for the replayed repository.
func freshTackleScan(t *testing.T, args ...string) scan.Report {
	t.Helper()
	var out, errs bytes.Buffer
	if code := newApp(absFixtureEnv(t, nil)).Dispatch(append([]string{"scan", "--since", "2026-09-01", "--no-cache"}, args...), &out, &errs); code != 0 {
		t.Fatalf("scan exit %d: %s", code, errs.String())
	}
	var r scan.Report
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if len(r.Signals) < 2 {
		t.Fatalf("fixture scan has too few signals: %d", len(r.Signals))
	}
	return r
}

func TestCompareScanFlag(t *testing.T) {
	fresh := freshTackleScan(t)
	stored := fresh
	// The recorded scan lacks the first current signal (added), carries a
	// signal the repository no longer has (removed), and has every other
	// signal on shifted lines (unchanged).
	gone := signal.New("floating-ref", "medium", "gone floating ref", []signal.Evidence{{Kind: signal.KindFile, Path: ".github/workflows/retired.yml", Line: 3}})
	stored.Signals = append([]signal.Signal{gone}, fresh.Signals[1:]...)
	for i := range stored.Signals[1:] {
		s := &stored.Signals[i+1]
		s.Evidence = append([]signal.Evidence(nil), s.Evidence...)
		for j := range s.Evidence {
			if s.Evidence[j].Line > 0 {
				s.Evidence[j].Line += 7
			}
		}
	}
	b, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	scanRecord(t, b)
	env := absFixtureEnv(t, nil)
	args := []string{"compare", "--before-runs", "36606519472", "--after-runs", "36606519472", "--scan", "--since", "2026-09-01", "--no-cache"}
	var out, errs bytes.Buffer
	if code := newApp(env).Dispatch(args, &out, &errs); code != 0 {
		t.Fatalf("signal changes must not fail compare: exit %d %s", code, errs.String())
	}
	var r compare.Result
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	d := r.Scan
	if d == nil || d.Status != compare.ScanCompared || r.Gates != nil {
		t.Fatalf("scan diff: %s", out.String())
	}
	if len(d.Removed) != 1 || d.Removed[0].Summary != "gone floating ref" {
		t.Fatalf("removed: %+v", d.Removed)
	}
	if len(d.Added) != 1 || d.Added[0].ID != fresh.Signals[0].ID {
		t.Fatalf("added: %+v", d.Added)
	}
	if len(d.Unchanged) != len(fresh.Signals)-1 {
		t.Fatalf("unchanged %d; want %d", len(d.Unchanged), len(fresh.Signals)-1)
	}
	out.Reset()
	if code := newApp(env).Dispatch(append(args, "--format", "md"), &out, &errs); code != 0 {
		t.Fatalf("md exit %d %s", code, errs.String())
	}
	for _, want := range []string{"## Scan: compared", "### Removed (1)", ".github/workflows/retired.yml:3", "### Added (1)", "`" + fresh.Signals[0].ID + "`"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("markdown lacks %q:\n%s", want, out.String())
		}
	}
}

// --until reaches compare's fresh scan: its run history is [since, until),
// so a past-window scan diffs cleanly against a scan recorded for that window.
func TestCompareScanUntil(t *testing.T) {
	window := []string{"--since", "2026-09-01", "--until", "2026-09-15"}
	stored := freshTackleScan(t, "--until", "2026-09-15")
	b, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	scanRecord(t, b)
	var mu sync.Mutex
	var created []string
	env := absFixtureEnv(t, func(r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/actions/runs") {
			mu.Lock()
			created = append(created, r.URL.Query().Get("created"))
			mu.Unlock()
		}
	})
	args := append([]string{"compare", "--before-runs", "36606519472", "--after-runs", "36606519472", "--scan", "--no-cache"}, window...)
	var out, errs bytes.Buffer
	if code := newApp(env).Dispatch(args, &out, &errs); code != 0 {
		t.Fatalf("exit %d %s", code, errs.String())
	}
	var r compare.Result
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if r.Scan == nil || r.Scan.Status != compare.ScanCompared || len(r.Scan.Added) != 0 || len(r.Scan.Removed) != 0 || len(r.Scan.Unchanged) != len(stored.Signals) {
		t.Fatalf("past-window scan diff: %s", out.String())
	}
	if len(created) == 0 {
		t.Fatal("fresh scan listed no runs")
	}
	for _, c := range created {
		if c != "2026-09-01T00:00:00Z..2026-09-15T00:00:00Z" {
			t.Fatalf("fresh scan listing %q is not [since, until)", c)
		}
	}
}

// A record without scan.json, or with an unfinished baseline set, gives an
// unavailable scan comparison, never "no changes", and exit 0.
func TestCompareScanUnavailable(t *testing.T) {
	root := scanRecord(t, nil)
	args := []string{"compare", "--before-runs", "36606519472", "--after-runs", "36606519472", "--scan", "--since", "2026-09-01", "--no-cache"}
	var out, errs bytes.Buffer
	if code := newApp(absFixtureEnv(t, nil)).Dispatch(args, &out, &errs); code != 0 {
		t.Fatalf("exit %d %s", code, errs.String())
	}
	var r compare.Result
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if r.Scan == nil || r.Scan.Status != compare.ScanUnavailable || !strings.Contains(r.Scan.Unavailable, ".muda/scan.json") {
		t.Fatalf("missing scan.json: %s", out.String())
	}
	if err := os.WriteFile(filepath.Join(root, ".muda", ".baseline-set-pending"), []byte(`{"schema":1,"files":{"scan.json":"x"}}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := newApp(absFixtureEnv(t, nil)).Dispatch(append(args, "--format", "md"), &out, &errs); code != 0 {
		t.Fatalf("exit %d %s", code, errs.String())
	}
	if !strings.Contains(out.String(), "## Scan: unavailable") || !strings.Contains(out.String(), "incomplete-baseline-set") {
		t.Fatalf("pending set not named:\n%s", out.String())
	}
	// Outside any record, --scan is unavailable too.
	t.Chdir(t.TempDir())
	out.Reset()
	if code := newApp(absFixtureEnv(t, nil)).Dispatch(args, &out, &errs); code != 0 || !strings.Contains(out.String(), `"status": "unavailable"`) {
		t.Fatalf("outside record: %d %s %s", code, out.String(), errs.String())
	}
}

func TestCompareRefPassesThrough(t *testing.T) {
	fresh := freshTackleScan(t, "--ref", "feature")
	b, err := json.Marshal(fresh)
	if err != nil {
		t.Fatal(err)
	}
	scanRecord(t, b)
	var mu sync.Mutex
	var contents []string
	env := absFixtureEnv(t, func(r *http.Request) {
		if strings.Contains(r.URL.Path, "/contents/") {
			mu.Lock()
			contents = append(contents, r.URL.Path+"?"+r.URL.RawQuery)
			mu.Unlock()
		}
	})
	var out, errs bytes.Buffer
	args := []string{"compare", "--before-runs", "36606519472", "--after-runs", "36606519472", "--gates", "--scan", "--ref", "feature", "--since", "2026-09-01", "--no-cache"}
	// The recorded inventory has no workflows, so --gates reports and fails.
	if code := newApp(env).Dispatch(args, &out, &errs); code != 1 {
		t.Fatalf("exit %d %s", code, errs.String())
	}
	var r compare.Result
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if r.Scan == nil || r.Scan.Status != compare.ScanCompared || len(r.Scan.Unchanged) != len(fresh.Signals) || len(r.Scan.Added)+len(r.Scan.Removed) != 0 {
		t.Fatalf("scan at ref: %s", out.String())
	}
	if r.Gates == nil || !strings.Contains(out.String(), "/blob/feature/.github/workflows/") {
		t.Fatalf("gates at ref: %s", out.String())
	}
	// Timing phases read each run's workflow at its head commit; every other
	// workflow read (gates and scan) is at the ref, and both listed it there.
	commit := regexp.MustCompile(`ref=[0-9a-f]{40}$`)
	listings := 0
	for _, c := range contents {
		if commit.MatchString(c) {
			continue
		}
		if !strings.HasSuffix(c, "?ref=feature") {
			t.Errorf("workflow request not at the ref: %s", c)
		}
		if strings.HasSuffix(c, "/contents/.github/workflows?ref=feature") {
			listings++
		}
	}
	if listings != 2 {
		t.Fatalf("gates and scan listed workflows at the ref %d times; want 2: %v", listings, contents)
	}
}

// --ref only changes --gates and --scan; alone it would silently do nothing.
func TestCompareRefUsage(t *testing.T) {
	for _, extra := range [][]string{{"--ref", "feature"}, {"--ref", "", "--scan"}, {"--ref", " ", "--gates"}} {
		var out, errs bytes.Buffer
		args := append([]string{"compare", "--before-runs", "1", "--after-runs", "2"}, extra...)
		if code := newApp(fixtureEnv(t)).Dispatch(args, &out, &errs); code != 2 || !strings.Contains(errs.String(), "--ref") || out.Len() != 0 {
			t.Errorf("%v: exit %d %q", extra, code, errs.String())
		}
	}
}

func TestCompareHelpNamesScanAndRef(t *testing.T) {
	var help, errs bytes.Buffer
	if code := newApp(fixtureEnv(t)).Dispatch([]string{"compare", "--help"}, &help, &errs); code != 0 {
		t.Fatalf("help: %d %s", code, errs.String())
	}
	for _, want := range []string{"--scan", "--ref", ".muda/scan.json", "unavailable", "raw file", "never change the exit code", "byte-identical", "VERIFIED", "workflow_diffs", "old → new",
		// Final round: the VERIFIED boundary, and equal hashes are not a change.
		"VERIFIED covers .github/workflows files only", "local composite actions (uses: ./…)", "scripts a step runs", "remote actions on moving refs", "same-hash"} {
		if !strings.Contains(help.String(), want) {
			t.Errorf("compare help lacks %q:\n%s", want, help.String())
		}
	}
}

// histRecord writes a historical gates.toml (inventory read at ref) into the
// record at root, as record baseline --ref would.
func histRecord(t *testing.T, root string, inventory []byte, ref string) {
	t.Helper()
	body := fmt.Sprintf("schema = 1\ninventory-json = %q\nhistorical-ref = %q\nsettings-read = \"2026-10-01\"\n", string(inventory), ref)
	if err := os.WriteFile(filepath.Join(root, ".muda", "gates.toml"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

// A historical baseline's before side is its recorded commit, not the
// branch: compare names it for --gates and --scan, labels the before side's
// repository settings as current, and refuses a stored report read elsewhere.
func TestCompareHistoricalBeforeSide(t *testing.T) {
	sha := "0123456789abcdef0123456789abcdef01234567"
	label := "settings read 2026-10-01, not historical"
	stored := freshTackleScan(t)
	stored.Ref = sha
	sb, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	var gout, errs bytes.Buffer
	if code := newApp(absFixtureEnv(t, nil)).Dispatch([]string{"gates", "--no-cache"}, &gout, &errs); code != 0 {
		t.Fatalf("gates %d %s", code, errs.String())
	}
	var inv map[string]any
	if err := json.Unmarshal(gout.Bytes(), &inv); err != nil {
		t.Fatal(err)
	}
	inv["ref"] = sha
	gb, err := json.Marshal(inv)
	if err != nil {
		t.Fatal(err)
	}
	root := scanRecord(t, sb)
	histRecord(t, root, gb, sha)
	args := []string{"compare", "--before-runs", "36606519472", "--after-runs", "36606519472", "--gates", "--scan", "--since", "2026-09-01", "--no-cache"}
	run := func(extra ...string) (compare.Result, string) {
		t.Helper()
		var out bytes.Buffer
		errs.Reset()
		if code := newApp(absFixtureEnv(t, nil)).Dispatch(append(args, extra...), &out, &errs); code != 1 && code != 0 {
			t.Fatalf("exit %d %s", code, errs.String())
		}
		var r compare.Result
		if len(extra) == 0 {
			if err := json.Unmarshal(out.Bytes(), &r); err != nil {
				t.Fatal(err)
			}
		}
		return r, out.String()
	}
	r, out := run()
	if r.Gates == nil || r.Gates.BeforeRef != sha || r.Gates.BeforeSettings != label {
		t.Fatalf("gates before side: %s", out)
	}
	for _, g := range r.Gates.Unverified {
		if g.ID == record.PolicyInventory {
			t.Fatalf("historical inventory unavailable: %+v", g)
		}
	}
	if r.Scan == nil || r.Scan.Status != compare.ScanCompared || r.Scan.BeforeRef != sha || len(r.Scan.Unchanged) != len(stored.Signals) {
		t.Fatalf("scan before side: %s", out)
	}
	_, md := run("--format", "md")
	for _, want := range []string{"Before side: workflows at `" + sha + "`; " + label, "Before side: scan at `" + sha + "`"} {
		if !strings.Contains(md, want) {
			t.Fatalf("markdown lacks %q:\n%s", want, md)
		}
	}
	// A stored report read at another ref is not this baseline's before side.
	stored.Ref = "main"
	if sb, err = json.Marshal(stored); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".muda", "scan.json"), sb, 0600); err != nil {
		t.Fatal(err)
	}
	inv["ref"] = "main"
	if gb, err = json.Marshal(inv); err != nil {
		t.Fatal(err)
	}
	histRecord(t, root, gb, sha)
	r, out = run()
	if r.Scan == nil || r.Scan.Status != compare.ScanUnavailable || !strings.Contains(r.Scan.Unavailable, sha) {
		t.Fatalf("scan at another ref: %s", out)
	}
	if r.Gates == nil || r.Gates.Passed() || len(r.Gates.Unverified) != 1 || !strings.Contains(r.Gates.Unverified[0].Detail, sha) {
		t.Fatalf("gates at another ref: %s", out)
	}
	var help bytes.Buffer
	if code := newApp(fixtureEnv(t)).Dispatch([]string{"compare", "--help"}, &help, &errs); code != 0 {
		t.Fatal(errs.String())
	}
	for _, want := range []string{"historical baseline", "before_ref", "before_settings"} {
		if !strings.Contains(help.String(), want) {
			t.Errorf("compare help lacks %q", want)
		}
	}
	// A current (or older) record has no historical before side.
	if err := os.WriteFile(filepath.Join(root, ".muda", "gates.toml"), []byte(fmt.Sprintf("schema = 1\ninventory-json = %q\n", string(gb))), 0600); err != nil {
		t.Fatal(err)
	}
	_, out = run()
	if strings.Contains(out, "before_ref") || strings.Contains(out, "before_settings") {
		t.Fatalf("current record shows a historical before side: %s", out)
	}
}
