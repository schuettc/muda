package compare

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/schuettc/muda/internal/gh"
	"github.com/schuettc/muda/internal/ghtest"
	"github.com/schuettc/muda/internal/scan"
	"github.com/schuettc/muda/internal/signal"
	"github.com/schuettc/muda/internal/workflow"
)

const ciPath = ".github/workflows/ci.yml"

func sig(id, path string, line int) signal.Signal {
	return signal.New(id, "medium", id+" in "+path, []signal.Evidence{{Kind: signal.KindFile, Path: path, Line: line, URL: "https://github.com/o/r/blob/main/" + path}})
}

func report(sigs ...signal.Signal) *scan.Report {
	return &scan.Report{Schema: scan.Schema, Repo: "o/r", Ref: "main", Files: []string{ciPath}, Signals: append([]signal.Signal{}, sigs...)}
}

func ids(ss []signal.Signal) []string {
	out := []string{}
	for _, s := range ss {
		out = append(out, s.ID)
	}
	return out
}

func TestScanDiffRemovedAddedUnchanged(t *testing.T) {
	stored := report(sig("floating-ref", ciPath, 8), sig("no-concurrency", ciPath, 1))
	fresh := report(sig("no-concurrency", ciPath, 1), sig("uncached-install", ciPath, 20))
	d := ScanChanges(stored, fresh)
	if d.Status != ScanCompared || d.Unavailable != "" {
		t.Fatalf("status %q %q", d.Status, d.Unavailable)
	}
	if got := ids(d.Removed); len(got) != 1 || got[0] != "floating-ref" {
		t.Fatalf("removed %v", got)
	}
	if got := ids(d.Added); len(got) != 1 || got[0] != "uncached-install" {
		t.Fatalf("added %v", got)
	}
	if got := ids(d.Unchanged); len(got) != 1 || got[0] != "no-concurrency" {
		t.Fatalf("unchanged %v", got)
	}
}

func TestScanDiffIgnoresLineShift(t *testing.T) {
	d := ScanChanges(report(sig("floating-ref", ciPath, 12)), report(sig("floating-ref", ciPath, 15)))
	if d.Status != ScanCompared || len(d.Removed)+len(d.Added) != 0 || len(d.Unchanged) != 1 {
		t.Fatalf("line shift reported as a change: %+v", d)
	}
	// The current line is what is shown.
	if d.Unchanged[0].Evidence[0].Line != 15 {
		t.Fatalf("unchanged shows stale line: %+v", d.Unchanged[0])
	}
	// A different file or step is a different signal.
	moved := sig("floating-ref", ".github/workflows/release.yml", 12)
	if d := ScanChanges(report(sig("floating-ref", ciPath, 12)), report(moved)); len(d.Removed) != 1 || len(d.Added) != 1 {
		t.Fatalf("file change matched: %+v", d)
	}
	a, b := sig("floating-ref", ciPath, 12), sig("floating-ref", ciPath, 12)
	a.Evidence[0].Step, b.Evidence[0].Step = "checkout", "setup"
	if d := ScanChanges(report(a), report(b)); len(d.Removed) != 1 || len(d.Added) != 1 {
		t.Fatalf("step change matched: %+v", d)
	}
}

func TestScanDiffCountsDuplicates(t *testing.T) {
	stored := report(sig("floating-ref", ciPath, 8), sig("floating-ref", ciPath, 14))
	fresh := report(sig("floating-ref", ciPath, 8))
	d := ScanChanges(stored, fresh)
	if len(d.Removed) != 1 || len(d.Unchanged) != 1 || len(d.Added) != 0 {
		t.Fatalf("duplicates: %+v", d)
	}
}

// Evidence order is part of the identity: a multi-file signal matches only
// the same files in the same order.
func TestScanDiffMultiFileEvidence(t *testing.T) {
	drift := func(paths ...string) signal.Signal {
		var ev []signal.Evidence
		for i, p := range paths {
			ev = append(ev, signal.Evidence{Kind: signal.KindFile, Path: p, Line: 10 + i})
		}
		return signal.New("toolchain-drift", "medium", "drift", ev)
	}
	rel := ".github/workflows/release.yml"
	d := ScanChanges(report(drift(ciPath, rel)), report(drift(ciPath, rel)))
	if len(d.Unchanged) != 1 {
		t.Fatalf("same files unmatched: %+v", d)
	}
	d = ScanChanges(report(drift(ciPath, rel)), report(drift(ciPath)))
	if len(d.Removed) != 1 || len(d.Added) != 1 {
		t.Fatalf("different file set matched: %+v", d)
	}
}

func TestScanDiffMissingRecordUnavailable(t *testing.T) {
	d := ScanChanges(nil, report(sig("floating-ref", ciPath, 8)))
	if d.Status != ScanUnavailable || !strings.Contains(d.Unavailable, ".muda/scan.json") {
		t.Fatalf("missing record: %+v", d)
	}
	if d.Removed == nil || d.Added == nil || d.Unchanged == nil || len(d.Removed)+len(d.Added)+len(d.Unchanged) != 0 {
		t.Fatalf("unavailable lists must be empty and non-null: %+v", d)
	}
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"removed":[]`, `"added":[]`, `"unchanged":[]`, `"status":"unavailable"`} {
		if !strings.Contains(string(b), key) {
			t.Fatalf("JSON lacks %s: %s", key, b)
		}
	}
	if u := UnavailableScan("why"); u.Status != ScanUnavailable || u.Unavailable != "why" || u.Removed == nil {
		t.Fatalf("UnavailableScan: %+v", u)
	}
}

// A stored signal that vanishes only because the fresh scan could not read
// its evidence is not a fix: the comparison is unavailable, naming why.
func TestScanDiffUnprovableRemovalUnavailable(t *testing.T) {
	stored := report(sig("floating-ref", ciPath, 8))
	fresh := report()
	fresh.Files = []string{}
	fresh.Unavailable = []scan.Unavailable{{What: ciPath, Why: "parse: bad"}}
	if d := ScanChanges(stored, fresh); d.Status != ScanUnavailable || !strings.Contains(d.Unavailable, ciPath) || len(d.Removed) != 0 {
		t.Fatalf("unreadable file reported as removed signals: %+v", d)
	}
	stored = report(sig("no-path-filter", ciPath, 1))
	fresh = report()
	fresh.Skipped = []string{ciPath}
	if d := ScanChanges(stored, fresh); d.Status != ScanUnavailable || !strings.Contains(d.Unavailable, ciPath) {
		t.Fatalf("skipped timing reported as a removed signal: %+v", d)
	}
	// A deleted workflow file really removes its signals.
	fresh = report()
	fresh.Files = []string{}
	if d := ScanChanges(report(sig("floating-ref", ciPath, 8)), fresh); d.Status != ScanCompared || len(d.Removed) != 1 {
		t.Fatalf("deleted file: %+v", d)
	}
	// A recorded scan of another repository is not comparable.
	other := report(sig("floating-ref", ciPath, 8))
	other.Repo = "o/other"
	if d := ScanChanges(other, report()); d.Status != ScanUnavailable || !strings.Contains(d.Unavailable, "o/other") {
		t.Fatalf("repo mismatch: %+v", d)
	}
}

func TestScanDiffMarkdown(t *testing.T) {
	d := ScanChanges(report(sig("floating-ref", ciPath, 8), sig("no-concurrency", ciPath, 1)), report(sig("uncached-install", ciPath, 20), sig("no-concurrency", ciPath, 2)))
	md := RenderMarkdown(&Result{Schema: 1, Scan: d})
	for _, want := range []string{"## Scan: compared", "### Removed (1)", "floating-ref", ciPath + ":8", "### Added (1)", "uncached-install", ciPath + ":20", "### Unchanged (1)", "`no-concurrency`", ciPath + ":2"} {
		if !strings.Contains(md, want) {
			t.Fatalf("markdown lacks %q:\n%s", want, md)
		}
	}
	md = RenderMarkdown(&Result{Schema: 1, Scan: ScanChanges(nil, report())})
	if !strings.Contains(md, "## Scan: unavailable") || !strings.Contains(md, ".muda/scan.json") {
		t.Fatalf("unavailable markdown:\n%s", md)
	}
}

// Q1 probe: a fixed action ref and a new floating one in the same file are
// two different signals, not one unchanged signal.
func TestScanDiffSummaryIdentity(t *testing.T) {
	floating := func(uses string, line int) signal.Signal {
		return signal.New("floating-ref", "medium", "action ref is not a SHA or exact vX.Y.Z: "+uses, []signal.Evidence{{Kind: signal.KindFile, Path: ciPath, Line: line, Note: uses}})
	}
	d := ScanChanges(report(floating("actions/checkout@v4", 8)), report(floating("evil/action@main", 8)))
	if d.Status != ScanCompared || len(d.Removed) != 1 || len(d.Added) != 1 || len(d.Unchanged) != 0 {
		t.Fatalf("swapped floating refs matched: %+v", d)
	}
	// no-path-filter's summary carries a rolling p50: it is not identity.
	npf := func(p50 string) signal.Signal {
		return signal.New("no-path-filter", "medium", ciPath+" runs on every push/pull_request without paths filter; p50="+p50+" (≥5 min)", []signal.Evidence{{Kind: signal.KindFile, Path: ciPath, Line: 1}})
	}
	if d := ScanChanges(report(npf("6m0s")), report(npf("9m30s"))); len(d.Unchanged) != 1 || len(d.Removed)+len(d.Added) != 0 {
		t.Fatalf("p50 change read as a signal change: %+v", d)
	}
}

// Guard: no rule may put shift- or timing-dependent text in the identity.
// Every scan fixture, re-scanned with comment and blank lines prepended and
// with different run timings, yields the same multiset of keys.
func TestScanKeyStableAcrossShiftAndTimings(t *testing.T) {
	dir := filepath.Join("..", "scan", "testdata")
	names, err := filepath.Glob(filepath.Join(dir, "*.yml"))
	if err != nil || len(names) == 0 {
		t.Fatalf("no scan fixtures: %v", err)
	}
	run := func(prefix string, p50 time.Duration) []signal.Signal {
		var files []*workflow.File
		durations := map[string]time.Duration{}
		for _, n := range names {
			b, err := os.ReadFile(n)
			if err != nil {
				t.Fatal(err)
			}
			path := ".github/workflows/" + filepath.Base(n)
			f, err := workflow.Parse(path, append([]byte(prefix), b...))
			if err != nil {
				t.Fatalf("%s: %v", n, err)
			}
			files = append(files, f)
			durations[path] = p50
		}
		return scan.Files(files, durations)
	}
	a := run("", 7*time.Minute)
	b := run("# shifted\n\n# by a comment block\n\n", 11*time.Minute+30*time.Second)
	keys := func(ss []signal.Signal) map[string]int {
		m := map[string]int{}
		for _, s := range ss {
			m[scanKey(s)]++
		}
		return m
	}
	ka, kb := keys(a), keys(b)
	if len(ka) != len(kb) {
		t.Fatalf("key multisets differ in size: %d vs %d", len(ka), len(kb))
	}
	for k, n := range ka {
		if kb[k] != n {
			t.Errorf("key %s: %d before, %d after the shift", k, n, kb[k])
		}
	}
	// Not vacuous: every scan rule fired, lines moved, and a timing-bearing
	// summary changed.
	fired := map[string]bool{}
	for _, s := range a {
		fired[s.ID] = true
	}
	for _, id := range signal.IDs() {
		if signal.Registry[id].From == "scan" && !fired[id] {
			t.Errorf("fixtures do not exercise %s", id)
		}
	}
	moved, retimed := false, false
	for i := range a {
		if i < len(b) && len(a[i].Evidence) > 0 && len(b[i].Evidence) > 0 && a[i].Evidence[0].Line != b[i].Evidence[0].Line {
			moved = true
		}
		if i < len(b) && a[i].ID == "no-path-filter" && a[i].Summary != b[i].Summary {
			retimed = true
		}
	}
	if !moved || !retimed {
		t.Fatalf("guard is vacuous: moved=%v retimed=%v", moved, retimed)
	}
}

// I-1: unknown cache coverage blocks only a removal claim for the same file,
// job and manager.
func TestScanDiffUncachedInstallPerJob(t *testing.T) {
	uncached := func(job, cmd string) signal.Signal {
		return signal.New("uncached-install", "warn", "job "+job+": "+cmd+" without cache", []signal.Evidence{{Kind: signal.KindFile, Path: ciPath, Line: 9, Note: cmd}})
	}
	fresh := func(extra ...signal.Signal) *scan.Report {
		r := report(extra...)
		r.Unavailable = []scan.Unavailable{{What: ciPath + " job a npm cache coverage", Why: "cache path/input or runner environment is unknown; no uncached-install claim"}}
		return r
	}
	// The removal is in the job whose coverage is unknown: unprovable.
	if d := ScanChanges(report(uncached("a", "npm ci")), fresh()); d.Status != ScanUnavailable || !strings.Contains(d.Unavailable, ciPath) {
		t.Fatalf("removal in the unknown job was claimed: %+v", d)
	}
	// Another job, or another manager in the same job: a real, provable fix.
	for _, s := range []signal.Signal{uncached("b", "npm ci"), uncached("a", "pip install")} {
		d := ScanChanges(report(s), fresh(sig("floating-ref", ".github/workflows/new.yml", 3)))
		if d.Status != ScanCompared || len(d.Removed) != 1 || len(d.Added) != 1 {
			t.Fatalf("%s: fix hidden by unrelated unknown coverage: %+v", s.Summary, d)
		}
	}
}

// Final review m-3: a stored signal whose ID is no longer a registered scan
// signal (renamed or removed, or never a scan signal) cannot be matched by a
// fresh scan, so it is unprovable, never "removed".
func TestScanDiffUnregisteredStoredSignalUnavailable(t *testing.T) {
	renamed := sig("floating-ref", ciPath, 8)
	renamed.ID = "floating-action-ref"
	measured := sig("floating-ref", ciPath, 9)
	measured.ID = "slow-step"
	for _, stored := range []signal.Signal{renamed, measured} {
		d := ScanChanges(report(stored, sig("no-concurrency", ciPath, 1)), report(sig("no-concurrency", ciPath, 1)))
		if d.Status != ScanUnavailable || !strings.Contains(d.Unavailable, stored.ID) || len(d.Removed) != 0 {
			t.Fatalf("%s: unregistered stored signal read as removed: %+v", stored.ID, d)
		}
	}
}

// Final review m-4: a literal pin changed to an expression is not a drift
// fix. The fresh scan cannot compare that tool's versions, so a removed
// toolchain-drift signal for it is unprovable; another tool's is not.
func TestScanDiffDriftRemovalBehindExpressionPin(t *testing.T) {
	pin := func(path, tool, version string) *workflow.File {
		action := map[string]string{"node": "actions/setup-node@v4.1.0", "python": "actions/setup-python@v5"}[tool]
		f, err := workflow.Parse(path, []byte("on: push\njobs:\n  test:\n    runs-on: ubuntu-24.04\n    steps:\n      - uses: "+action+"\n        with:\n          "+tool+"-version: "+version+"\n"))
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	rel := ".github/workflows/release.yml"
	stored := report(scan.Files([]*workflow.File{pin(ciPath, "node", "'18.20.4'"), pin(rel, "node", "'20.11.1'")}, nil)...)
	if got := ids(stored.Signals); !strings.Contains(strings.Join(got, " "), "toolchain-drift") {
		t.Fatalf("fixture: %+v", stored.Signals)
	}
	fresh := func(tool string) *scan.Report {
		r := report(scan.Files([]*workflow.File{pin(ciPath, tool, "${{ matrix.v }}"), pin(rel, "node", "'20.11.1'")}, nil)...)
		r.Unavailable = []scan.Unavailable{{What: ciPath + " job test " + tool + " version (expression)", Why: "expression"}}
		return r
	}
	if d := ScanChanges(stored, fresh("node")); d.Status != ScanUnavailable || !strings.Contains(d.Unavailable, "node version (expression)") || len(d.Removed) != 0 {
		t.Fatalf("drift behind an expression pin read as removed: %+v", d)
	}
	if d := ScanChanges(stored, fresh("python")); d.Status != ScanCompared || len(d.Removed) != 1 {
		t.Fatalf("another tool's expression pin blocked a real fix: %+v", d)
	}
}

// A fresh scan needs an ordered [Since, Until) run-history window; a missing
// bound is a caller error, never a quietly degraded scan.
func TestRunScanRequiresWindow(t *testing.T) {
	c := gh.New(gh.Options{NoCache: true, HTTP: &http.Client{Transport: ghtest.Replay("../ghtest/testdata/tackle")}})
	w := Window{RunIDs: []int64{36606519472}}
	since := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for _, o := range []Options{{Scan: true, Since: since}, {Scan: true, Until: since}, {Scan: true, Since: since, Until: since}} {
		if _, err := Run(context.Background(), c, "schuettc/tackle", w, w, o); err == nil || !strings.Contains(err.Error(), "scan") {
			t.Errorf("%+v: %v", o, err)
		}
	}
}

// Final re-review N-2: signals recorded in a workflow that later becomes a
// symlink are not removed; the fresh scan lacks the evidence to say so.
func TestScanDiffWorkflowTurnedSymlinkIsNotRemoved(t *testing.T) {
	const link = ".github/workflows/release.yml"
	base := ghtest.Replay("../ghtest/testdata/tackle")
	since, until := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 29, 18, 0, 0, 0, time.UTC)
	run := func(rt http.RoundTripper) *scan.Report {
		t.Helper()
		r, err := scan.Run(context.Background(), gh.New(gh.Options{NoCache: true, HTTP: &http.Client{Transport: rt}}), "schuettc/tackle", "", since, until)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	stored := run(base)
	inLink := false
	for _, s := range stored.Signals {
		for _, e := range s.Evidence {
			inLink = inLink || e.Path == link
		}
	}
	if !inLink {
		t.Fatalf("fixture has no signal in %s; the test exercises nothing: %+v", link, stored.Signals)
	}
	d := ScanChanges(stored, run(ghtest.NonFileWorkflows(base, []string{link}, nil)))
	if d.Status != ScanUnavailable || !strings.Contains(d.Unavailable, link) || !strings.Contains(d.Unavailable, "symlink") {
		t.Fatalf("symlinked workflow's signals read as removed: %+v", d)
	}
}

// workflowOverride serves the named workflow files' contents from yaml
// instead of the replay fixture, so a test scans exactly the pins it writes.
type workflowOverride struct {
	base  http.RoundTripper
	files map[string]string
}

func (o workflowOverride) RoundTrip(r *http.Request) (*http.Response, error) {
	for name, yaml := range o.files {
		if strings.HasSuffix(r.URL.Path, "/contents/.github/workflows/"+name) {
			body, _ := json.Marshal(map[string]string{"type": "file", "encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte(yaml))})
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body)), Request: r}, nil
		}
	}
	return o.base.RoundTrip(r)
}

// scanWorkflows runs the real scan producer over the replay fixture with
// verify.yml and release.yml replaced.
func scanWorkflows(t *testing.T, verify, release string) *scan.Report {
	t.Helper()
	tr := workflowOverride{base: ghtest.Replay("../ghtest/testdata/tackle"), files: map[string]string{"verify.yml": verify, "release.yml": release}}
	c := gh.New(gh.Options{NoCache: true, HTTP: &http.Client{Transport: tr}})
	r, err := scan.Run(context.Background(), c, "schuettc/tackle", "", time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 29, 18, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// setup is one setup step for tool (node or python) pinned to version.
func setup(tool, version string) string {
	action := map[string]string{"node": "actions/setup-node@v4.1.0", "python": "actions/setup-python@v5"}[tool]
	return "      - uses: " + action + "\n        with:\n          " + tool + "-version: " + version + "\n"
}

// job is one workflow job with the given steps.
func job(id string, steps ...string) string {
	return "  " + id + ":\n    runs-on: ubuntu-24.04\n    steps:\n" + strings.Join(steps, "")
}

// nodeJob is one workflow job whose steps are setup-node with each version.
func nodeJob(id string, versions ...string) string {
	steps := []string{}
	for _, v := range versions {
		steps = append(steps, setup("node", v))
	}
	return job(id, steps...)
}

func workflowYAML(jobs ...string) string {
	return "on: workflow_dispatch\njobs:\n" + strings.Join(jobs, "")
}

func driftRemoved(d *ScanDiff) bool {
	for _, s := range d.Removed {
		if s.ID == "toolchain-drift" {
			return true
		}
	}
	return false
}

// N-1 (final re-review of the pre-validation build): an expression-valued pin
// that is byte-identical in the recorded and fresh scans was excluded from
// both drift comparisons, so it cannot hide drift that the fix cleared. The
// fix is proven. An expression that changed, or a literal pin that became an
// expression (even in a job that already had one), still blocks.
func TestScanDiffDriftFixBesideUnchangedExpressionPin(t *testing.T) {
	matrix := nodeJob("matrix", "${{ matrix.node }}")
	unfixed := workflowYAML(nodeJob("test", "'18.20.4'"))
	fixedVerify := workflowYAML(nodeJob("test", "'20.11.1'"))
	stored := scanWorkflows(t, unfixed, workflowYAML(nodeJob("release", "'20.11.1'"), matrix))
	if !strings.Contains(strings.Join(ids(stored.Signals), " "), "toolchain-drift") {
		t.Fatalf("fixture has no drift: %+v", stored.Signals)
	}

	fixed := scanWorkflows(t, fixedVerify, workflowYAML(nodeJob("release", "'20.11.1'"), matrix))
	if d := ScanChanges(stored, fixed); d.Status != ScanCompared || !driftRemoved(d) {
		t.Fatalf("drift fix beside an unchanged expression pin not proven: %+v", d)
	}
	// A recorded scan without expression notes (from before they existed)
	// cannot show the expression was unchanged.
	noNotes := *stored
	noNotes.Unavailable = nil
	if d := ScanChanges(&noNotes, fixed); d.Status != ScanUnavailable || driftRemoved(d) {
		t.Fatalf("stored scan without notes proved a drift fix: %+v", d)
	}

	for _, tc := range []struct {
		name, verify, release string
	}{
		{"expression changed", fixedVerify, workflowYAML(nodeJob("release", "'20.11.1'"), nodeJob("matrix", "${{ matrix.other }}"))},
		{"literal became an expression", unfixed, workflowYAML(nodeJob("release", "${{ vars.node }}"), matrix)},
		{"literal became a second expression in the job", unfixed, workflowYAML(nodeJob("matrix", "${{ matrix.node }}", "${{ vars.node }}"))},
	} {
		fresh := scanWorkflows(t, tc.verify, tc.release)
		if d := ScanChanges(stored, fresh); d.Status != ScanUnavailable || !strings.Contains(d.Unavailable, "node version (expression)") || driftRemoved(d) {
			t.Errorf("%s: drift removal proven: %+v", tc.name, d)
		}
	}
}

// N-1 review: a note recorded by the first-pin-only producer names one of a
// job's two expression pins. It cannot show the second was unchanged, so it
// never equals the fresh note that lists both, and the removal blocks.
func TestScanDiffDriftFixBesideFirstPinOnlyNoteBlocks(t *testing.T) {
	matrix := nodeJob("matrix", "${{ matrix.node }}", "${{ vars.node }}")
	stored := scanWorkflows(t, workflowYAML(nodeJob("test", "'18.20.4'")), workflowYAML(nodeJob("release", "'20.11.1'"), matrix))
	rewritten := 0
	for i, u := range stored.Unavailable {
		if tool, ok := scan.ExpressionPinTool(u); ok && tool == "node" {
			stored.Unavailable[i].Why = "node-version=${{ matrix.node }} is an expression; its value is unknown, so it is not compared for toolchain drift"
			rewritten++
		}
	}
	if rewritten != 1 {
		t.Fatalf("fixture has %d node expression notes, want 1: %+v", rewritten, stored.Unavailable)
	}
	fresh := scanWorkflows(t, workflowYAML(nodeJob("test", "'20.11.1'")), workflowYAML(nodeJob("release", "'20.11.1'"), matrix))
	if d := ScanChanges(stored, fresh); d.Status != ScanUnavailable || !strings.Contains(d.Unavailable, "job matrix node version (expression)") || driftRemoved(d) {
		t.Fatalf("first-pin-only recorded note proved a drift fix: %+v", d)
	}
}

// N-1 review: another tool's expression pin, unchanged or not, says nothing
// about node's pins, so it never blocks proof of node's drift fix.
func TestScanDiffDriftFixBesideOtherToolExpression(t *testing.T) {
	both := func(py string) string {
		return job("matrix", setup("node", "${{ matrix.node }}"), setup("python", py))
	}
	stored := scanWorkflows(t, workflowYAML(nodeJob("test", "'18.20.4'")), workflowYAML(nodeJob("release", "'20.11.1'"), both("${{ matrix.python }}")))
	notes := 0
	for _, u := range stored.Unavailable {
		if _, ok := scan.ExpressionPinTool(u); ok {
			notes++
		}
	}
	if notes != 2 {
		t.Fatalf("fixture wants node and python expression notes: %+v", stored.Unavailable)
	}
	for _, tc := range []struct{ name, python string }{
		{"python expression unchanged", "${{ matrix.python }}"},
		{"python expression changed", "${{ vars.python }}"},
	} {
		fresh := scanWorkflows(t, workflowYAML(nodeJob("test", "'20.11.1'")), workflowYAML(nodeJob("release", "'20.11.1'"), both(tc.python)))
		if d := ScanChanges(stored, fresh); d.Status != ScanCompared || !driftRemoved(d) {
			t.Errorf("%s: blocked node's drift fix: %+v", tc.name, d)
		}
	}
}
