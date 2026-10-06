package scan_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/schuettc/muda/internal/scan"
	"github.com/schuettc/muda/internal/signal"
	"github.com/schuettc/muda/internal/workflow"
)

// parseFixture reads testdata/<name> and returns the parsed workflow file.
// Parse errors fail immediately; we never silently skip unparseable fixtures.
func parseFixture(t *testing.T, name string) *workflow.File {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	f, err := workflow.Parse(".github/workflows/"+name, data)
	if err != nil {
		t.Fatalf("parse fixture %s: %v", name, err)
	}
	return f
}

func signalsWithID(sigs []signal.Signal, id string) []signal.Signal {
	var out []signal.Signal
	for _, s := range sigs {
		if s.ID == id {
			out = append(out, s)
		}
	}
	return out
}

func requireSignal(t *testing.T, sigs []signal.Signal, id string) []signal.Signal {
	t.Helper()
	got := signalsWithID(sigs, id)
	if len(got) == 0 {
		t.Errorf("expected signal %q, got none; all signals: %v", id, signalIDs(sigs))
	}
	return got
}

func requireNoSignal(t *testing.T, sigs []signal.Signal, id string) {
	t.Helper()
	got := signalsWithID(sigs, id)
	if len(got) > 0 {
		t.Errorf("expected no signal %q, got %d: %v", id, len(got), got)
	}
}

func signalIDs(sigs []signal.Signal) []string {
	ids := make([]string, len(sigs))
	for i, s := range sigs {
		ids[i] = s.ID
	}
	return ids
}

// TestScan_FloatingRef verifies floating-ref detection on step uses: and
// reusable-job uses:. Local actions (./), 40-hex SHAs, exact vX.Y.Z refs,
// docker sha256 digests and expression refs are not floating.
func TestScan_FloatingRef(t *testing.T) {
	t.Run("pos", func(t *testing.T) {
		f := parseFixture(t, "floating-ref-pos.yml")
		sigs := scan.Files([]*workflow.File{f}, nil)
		got := requireSignal(t, sigs, "floating-ref")
		// Both @v3 and @main must appear as evidence.
		found := map[string]bool{}
		for _, s := range got {
			for _, e := range s.Evidence {
				found[e.Note] = true
			}
		}
		for _, want := range []string{"actions/checkout@v3", "actions/upload-artifact@main"} {
			ok := false
			for note := range found {
				if strings.Contains(note, want) {
					ok = true
				}
			}
			if !ok {
				t.Errorf("missing evidence for %q; notes: %v", want, found)
			}
		}
		// Line evidence must be exact, not whole-file (line > 0).
		for _, s := range got {
			for _, e := range s.Evidence {
				if e.Line == 0 {
					t.Errorf("evidence missing line number: %+v", e)
				}
			}
		}
	})

	t.Run("neg", func(t *testing.T) {
		f := parseFixture(t, "floating-ref-neg.yml")
		sigs := scan.Files([]*workflow.File{f}, nil)
		requireNoSignal(t, sigs, "floating-ref")
	})
}

// TestScan_FloatingRunner checks -latest labels; expressions and pinned labels
// must not fire.
func TestScan_FloatingRunner(t *testing.T) {
	t.Run("pos", func(t *testing.T) {
		f := parseFixture(t, "floating-runner-pos.yml")
		sigs := scan.Files([]*workflow.File{f}, nil)
		got := requireSignal(t, sigs, "floating-runner")
		// Both ubuntu-latest and macos-latest must be cited.
		notes := ""
		for _, s := range got {
			for _, e := range s.Evidence {
				notes += e.Note + " "
			}
		}
		if !strings.Contains(notes, "ubuntu-latest") {
			t.Errorf("missing ubuntu-latest evidence; notes: %s", notes)
		}
		if !strings.Contains(notes, "macos-latest") {
			t.Errorf("missing macos-latest evidence; notes: %s", notes)
		}
		for _, s := range got {
			for _, e := range s.Evidence {
				if e.Line == 0 {
					t.Errorf("evidence missing line number: %+v", e)
				}
			}
		}
	})

	t.Run("neg", func(t *testing.T) {
		f := parseFixture(t, "floating-runner-neg.yml")
		sigs := scan.Files([]*workflow.File{f}, nil)
		requireNoSignal(t, sigs, "floating-runner")
	})
}

// TestScan_FloatingToolchain checks setup-{node,python,go,java} version
// pinning. version-file and exact X.Y.Z must not fire.
func TestScan_FloatingToolchain(t *testing.T) {
	t.Run("pos", func(t *testing.T) {
		f := parseFixture(t, "floating-toolchain-pos.yml")
		sigs := scan.Files([]*workflow.File{f}, nil)
		got := requireSignal(t, sigs, "floating-toolchain")
		// All four setup actions should be flagged.
		if len(got) < 4 {
			t.Errorf("expected ≥4 floating-toolchain signals, got %d", len(got))
		}
		for _, s := range got {
			for _, e := range s.Evidence {
				if e.Line == 0 {
					t.Errorf("evidence missing line number: %+v", e)
				}
			}
		}
	})

	t.Run("neg", func(t *testing.T) {
		f := parseFixture(t, "floating-toolchain-neg.yml")
		sigs := scan.Files([]*workflow.File{f}, nil)
		requireNoSignal(t, sigs, "floating-toolchain")
	})
}

// TestScan_Emulation checks docker/setup-qemu-action detection. Buildx alone
// must not fire. QEMU must not invent an arm64 runner mapping.
func TestScan_Emulation(t *testing.T) {
	t.Run("pos", func(t *testing.T) {
		f := parseFixture(t, "emulation-pos.yml")
		sigs := scan.Files([]*workflow.File{f}, nil)
		got := requireSignal(t, sigs, "emulation")
		for _, s := range got {
			for _, e := range s.Evidence {
				if e.Line == 0 {
					t.Errorf("evidence missing line: %+v", e)
				}
			}
		}
	})

	t.Run("neg", func(t *testing.T) {
		f := parseFixture(t, "emulation-neg.yml")
		sigs := scan.Files([]*workflow.File{f}, nil)
		requireNoSignal(t, sigs, "emulation")
	})

	t.Run("arm64-runner-no-flag", func(t *testing.T) {
		// An arm64 runner running arm64 builds is not emulation.
		const src = `on: push
jobs:
  arm:
    runs-on: ubuntu-24.04-arm
    steps:
      - uses: docker/setup-buildx-action@v3
      - run: docker buildx build --platform linux/arm64 .
`
		f, err := workflow.Parse(".github/workflows/arm.yml", []byte(src))
		if err != nil {
			t.Fatal(err)
		}
		sigs := scan.Files([]*workflow.File{f}, nil)
		requireNoSignal(t, sigs, "emulation")
	})
}

// TestScan_UncachedInstall verifies npm ci, pip install, uv sync and
// go mod download detection. actions/cache, cache: inputs and setup-uv must
// suppress the signal; cache: false and enable-cache: false must not suppress.
func TestScan_UncachedInstall(t *testing.T) {
	t.Run("pos", func(t *testing.T) {
		f := parseFixture(t, "uncached-install-pos.yml")
		sigs := scan.Files([]*workflow.File{f}, nil)
		got := requireSignal(t, sigs, "uncached-install")
		// build (npm ci), python (pip install), gomod (go mod download, cache:false) all flagged.
		if len(got) < 3 {
			t.Errorf("expected ≥3 uncached-install signals, got %d: %v", len(got), got)
		}
		for _, s := range got {
			for _, e := range s.Evidence {
				if e.Line == 0 {
					t.Errorf("evidence missing line: %+v", e)
				}
			}
		}
	})

	t.Run("neg", func(t *testing.T) {
		f := parseFixture(t, "uncached-install-neg.yml")
		sigs := scan.Files([]*workflow.File{f}, nil)
		requireNoSignal(t, sigs, "uncached-install")
	})

	t.Run("setup-go-default-cache", func(t *testing.T) {
		// setup-go without explicit cache: false caches modules by default.
		const src = `on: push
jobs:
  build:
    runs-on: ubuntu-24.04
    steps:
      - uses: actions/setup-go@v5
        with:
          go-version: '1.22.5'
      - run: go mod download
`
		f, err := workflow.Parse(".github/workflows/go.yml", []byte(src))
		if err != nil {
			t.Fatal(err)
		}
		sigs := scan.Files([]*workflow.File{f}, nil)
		requireNoSignal(t, sigs, "uncached-install")
	})

	t.Run("setup-uv-default-cache", func(t *testing.T) {
		// setup-uv without explicit enable-cache: false caches by default.
		const src = `on: push
jobs:
  build:
    runs-on: ubuntu-24.04
    steps:
      - uses: astral-sh/setup-uv@v5
        with:
          version: '0.5.18'
      - run: uv sync
`
		f, err := workflow.Parse(".github/workflows/uv.yml", []byte(src))
		if err != nil {
			t.Fatal(err)
		}
		sigs := scan.Files([]*workflow.File{f}, nil)
		requireNoSignal(t, sigs, "uncached-install")
	})

	t.Run("enable-cache-false-fires", func(t *testing.T) {
		// setup-uv with enable-cache: false → no cache → signal.
		const src = `on: push
jobs:
  build:
    runs-on: ubuntu-24.04
    steps:
      - uses: astral-sh/setup-uv@v5
        with:
          enable-cache: 'false'
      - run: uv sync
`
		f, err := workflow.Parse(".github/workflows/uv-nocache.yml", []byte(src))
		if err != nil {
			t.Fatal(err)
		}
		sigs := scan.Files([]*workflow.File{f}, nil)
		requireSignal(t, sigs, "uncached-install")
	})
}

// TestScan_SerialIndependentJobs verifies that jobs needing each other without
// output references are flagged. if:/environment: output references prevent the
// signal; do not discard them.
func TestScan_SerialIndependentJobs(t *testing.T) {
	t.Run("pos", func(t *testing.T) {
		f := parseFixture(t, "serial-independent-pos.yml")
		sigs := scan.Files([]*workflow.File{f}, nil)
		requireSignal(t, sigs, "serial-independent-jobs")
	})

	t.Run("neg-run-ref", func(t *testing.T) {
		f := parseFixture(t, "serial-independent-neg.yml")
		sigs := scan.Files([]*workflow.File{f}, nil)
		requireNoSignal(t, sigs, "serial-independent-jobs")
	})

	t.Run("neg-only-one-job-with-steps", func(t *testing.T) {
		// If only one of the pair has steps, no signal.
		const src = `on: push
jobs:
  build:
    runs-on: ubuntu-24.04
    steps:
      - run: make build
  call:
    needs: build
    uses: ./.github/workflows/deploy.yml
`
		f, err := workflow.Parse(".github/workflows/w.yml", []byte(src))
		if err != nil {
			t.Fatal(err)
		}
		sigs := scan.Files([]*workflow.File{f}, nil)
		requireNoSignal(t, sigs, "serial-independent-jobs")
	})
}

// TestScan_DuplicateCheckSet verifies that identical run-command lists shared
// across workflows with overlapping branch triggers are flagged. Conservative
// glob handling: only literal branch names overlap.
func TestScan_DuplicateCheckSet(t *testing.T) {
	t.Run("pos", func(t *testing.T) {
		a := parseFixture(t, "dup-check-set-a.yml")
		b := parseFixture(t, "dup-check-set-b.yml")
		sigs := scan.Files([]*workflow.File{a, b}, nil)
		requireSignal(t, sigs, "duplicate-check-set")
	})

	t.Run("neg-different-cmds", func(t *testing.T) {
		a := parseFixture(t, "dup-check-set-a.yml")
		const bSrc = `name: CI B2
on:
  push:
    branches: [main]
jobs:
  lint:
    runs-on: ubuntu-24.04
    steps:
      - run: npm ci
      - run: npm run typecheck
`
		b, err := workflow.Parse(".github/workflows/b2.yml", []byte(bSrc))
		if err != nil {
			t.Fatal(err)
		}
		sigs := scan.Files([]*workflow.File{a, b}, nil)
		requireNoSignal(t, sigs, "duplicate-check-set")
	})

	t.Run("neg-different-branches", func(t *testing.T) {
		// a.yml pushes to main; b3 pushes to develop only. No overlap.
		a := parseFixture(t, "dup-check-set-a.yml")
		const bSrc = `name: CI B3
on:
  push:
    branches: [develop]
jobs:
  lint:
    runs-on: ubuntu-24.04
    steps:
      - run: npm ci
      - run: npm run lint
`
		b, err := workflow.Parse(".github/workflows/b3.yml", []byte(bSrc))
		if err != nil {
			t.Fatal(err)
		}
		sigs := scan.Files([]*workflow.File{a, b}, nil)
		requireNoSignal(t, sigs, "duplicate-check-set")
	})

	t.Run("neg-glob-conservative", func(t *testing.T) {
		// Glob 'feature/**' vs literal 'feature/main': conservative → no flag.
		const aSrc = `name: Glob A
on:
  push:
    branches: ['feature/**']
jobs:
  lint:
    runs-on: ubuntu-24.04
    steps:
      - run: npm ci
      - run: npm run lint
`
		const bSrc = `name: Glob B
on:
  push:
    branches: ['feature/main']
jobs:
  lint:
    runs-on: ubuntu-24.04
    steps:
      - run: npm ci
      - run: npm run lint
`
		a, err := workflow.Parse(".github/workflows/glob-a.yml", []byte(aSrc))
		if err != nil {
			t.Fatal(err)
		}
		b, err := workflow.Parse(".github/workflows/glob-b.yml", []byte(bSrc))
		if err != nil {
			t.Fatal(err)
		}
		sigs := scan.Files([]*workflow.File{a, b}, nil)
		requireNoSignal(t, sigs, "duplicate-check-set")
	})

	t.Run("neg-single-workflow", func(t *testing.T) {
		a := parseFixture(t, "dup-check-set-a.yml")
		sigs := scan.Files([]*workflow.File{a}, nil)
		requireNoSignal(t, sigs, "duplicate-check-set")
	})
}

// TestScan_NoConcurrency checks push/pull_request workflows missing any
// concurrency control. workflow_dispatch alone must not fire.
func TestScan_NoConcurrency(t *testing.T) {
	t.Run("pos", func(t *testing.T) {
		f := parseFixture(t, "no-concurrency-pos.yml")
		sigs := scan.Files([]*workflow.File{f}, nil)
		got := requireSignal(t, sigs, "no-concurrency")
		for _, s := range got {
			for _, e := range s.Evidence {
				if e.Line == 0 {
					t.Errorf("evidence missing line: %+v", e)
				}
			}
		}
	})

	t.Run("neg-top-level-concurrency", func(t *testing.T) {
		f := parseFixture(t, "no-concurrency-neg.yml")
		sigs := scan.Files([]*workflow.File{f}, nil)
		requireNoSignal(t, sigs, "no-concurrency")
	})

	t.Run("neg-job-concurrency", func(t *testing.T) {
		const src = `on: push
jobs:
  build:
    runs-on: ubuntu-24.04
    concurrency:
      group: build-${{ github.ref }}
      cancel-in-progress: true
    steps:
      - run: make build
`
		f, err := workflow.Parse(".github/workflows/jc.yml", []byte(src))
		if err != nil {
			t.Fatal(err)
		}
		sigs := scan.Files([]*workflow.File{f}, nil)
		requireNoSignal(t, sigs, "no-concurrency")
	})

	t.Run("neg-workflow-dispatch-only", func(t *testing.T) {
		const src = `on: workflow_dispatch
jobs:
  build:
    runs-on: ubuntu-24.04
    steps:
      - run: make build
`
		f, err := workflow.Parse(".github/workflows/wd.yml", []byte(src))
		if err != nil {
			t.Fatal(err)
		}
		sigs := scan.Files([]*workflow.File{f}, nil)
		requireNoSignal(t, sigs, "no-concurrency")
	})
}

// TestScan_NoPathFilter verifies push/pull_request workflows without
// paths/paths-ignore are flagged when their p50 is ≥5 min. Duration below
// threshold, path filter present, or missing from durations map must suppress.
func TestScan_NoPathFilter(t *testing.T) {
	const tenMin = 10 * time.Minute
	const fiveMin = 5 * time.Minute

	posPath := ".github/workflows/no-path-filter-pos.yml"
	negPath := ".github/workflows/no-path-filter-neg.yml"

	t.Run("pos", func(t *testing.T) {
		f := parseFixture(t, "no-path-filter-pos.yml")
		durations := map[string]time.Duration{posPath: tenMin}
		sigs := scan.Files([]*workflow.File{f}, durations)
		got := requireSignal(t, sigs, "no-path-filter")
		for _, s := range got {
			for _, e := range s.Evidence {
				if e.Line == 0 {
					t.Errorf("evidence missing line: %+v", e)
				}
			}
		}
	})

	t.Run("neg-has-paths", func(t *testing.T) {
		f := parseFixture(t, "no-path-filter-neg.yml")
		durations := map[string]time.Duration{negPath: tenMin}
		sigs := scan.Files([]*workflow.File{f}, durations)
		requireNoSignal(t, sigs, "no-path-filter")
	})

	t.Run("neg-short-p50", func(t *testing.T) {
		f := parseFixture(t, "no-path-filter-pos.yml")
		durations := map[string]time.Duration{posPath: fiveMin - time.Second}
		sigs := scan.Files([]*workflow.File{f}, durations)
		requireNoSignal(t, sigs, "no-path-filter")
	})

	t.Run("pos-exact-threshold", func(t *testing.T) {
		// p50 of exactly 5 min is "at least 5 min" → fire.
		f := parseFixture(t, "no-path-filter-pos.yml")
		durations := map[string]time.Duration{posPath: fiveMin}
		sigs := scan.Files([]*workflow.File{f}, durations)
		requireSignal(t, sigs, "no-path-filter")
	})

	t.Run("neg-missing-from-map", func(t *testing.T) {
		// No duration data → no signal (skipped, not silent green).
		f := parseFixture(t, "no-path-filter-pos.yml")
		sigs := scan.Files([]*workflow.File{f}, nil)
		requireNoSignal(t, sigs, "no-path-filter")
	})

	t.Run("skipped-reported", func(t *testing.T) {
		// scan.Skipped lists workflows needing duration data but absent from map.
		f := parseFixture(t, "no-path-filter-pos.yml")
		skipped := scan.Skipped([]*workflow.File{f}, nil)
		found := false
		for _, s := range skipped {
			if s == posPath {
				found = true
			}
		}
		if !found {
			t.Errorf("expected %q in skipped, got %v", posPath, skipped)
		}
	})

	t.Run("skipped-not-reported-when-has-paths", func(t *testing.T) {
		// A workflow with path filter is not skipped even if missing from map.
		f := parseFixture(t, "no-path-filter-neg.yml")
		skipped := scan.Skipped([]*workflow.File{f}, nil)
		for _, s := range skipped {
			if s == negPath {
				t.Errorf("workflow with paths should not appear in skipped: %v", skipped)
			}
		}
	})
}

// TestScan_ReportSchema verifies all signals carry non-empty path + line
// evidence (exact line, not whole-file placeholder).
func TestScan_ReportSchema(t *testing.T) {
	files := []*workflow.File{
		parseFixture(t, "floating-ref-pos.yml"),
		parseFixture(t, "floating-runner-pos.yml"),
	}
	sigs := scan.Files(files, nil)
	if len(sigs) == 0 {
		t.Fatal("expected signals, got none")
	}
	for _, s := range sigs {
		if s.ID == "" {
			t.Error("signal missing ID")
		}
		if len(s.Waste) == 0 {
			t.Errorf("signal %s missing waste classes", s.ID)
		}
		if s.Severity == "" {
			t.Errorf("signal %s missing severity", s.ID)
		}
		for _, e := range s.Evidence {
			if e.Path == "" {
				t.Errorf("signal %s evidence missing path: %+v", s.ID, e)
			}
			if e.Line == 0 {
				t.Errorf("signal %s evidence missing line (whole-file placeholder forbidden): %+v", s.ID, e)
			}
		}
	}
}

// parseFixtureAs reads testdata/<name> and parses it as the workflow at path,
// so one fixture can stand in for several workflow files.
func parseFixtureAs(t *testing.T, name, path string) *workflow.File {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	f, err := workflow.Parse(path, data)
	if err != nil {
		t.Fatalf("parse fixture %s: %v", name, err)
	}
	return f
}

// requireEvidenceAt fails unless s cites path at a line > 0 whose note
// contains want.
func requireEvidenceAt(t *testing.T, s signal.Signal, path, want string) {
	t.Helper()
	for _, e := range s.Evidence {
		if e.Path == path && e.Line > 0 && strings.Contains(e.Note, want) {
			return
		}
	}
	t.Errorf("signal %s: no evidence at %s citing %q; evidence: %+v", s.ID, path, want, s.Evidence)
}

// TestScan_toolchain_drift: the same setup action pinned to different
// versions in two workflow files is one signal citing both steps.
func TestScan_toolchain_drift(t *testing.T) {
	a := parseFixture(t, "toolchain-drift-node-a.yml")
	b := parseFixture(t, "toolchain-drift-node-b.yml")
	got := requireSignal(t, scan.Files([]*workflow.File{a, b}, nil), "toolchain-drift")
	if len(got) != 1 {
		t.Fatalf("want exactly one toolchain-drift signal, got %d: %+v", len(got), got)
	}
	s := got[0]
	if len(s.Evidence) != 2 {
		t.Errorf("want 2 evidence entries, got %+v", s.Evidence)
	}
	requireEvidenceAt(t, s, a.Path, "20.11.1")
	requireEvidenceAt(t, s, b.Path, "22.4.0")
	// Line is the setup step's line, not the job or the file.
	for _, e := range s.Evidence {
		if e.Line != 7 && e.Line != 9 {
			t.Errorf("evidence line %d is not the setup-node step line: %+v", e.Line, e)
		}
	}
	if !strings.Contains(s.Summary, "20.11.1") || !strings.Contains(s.Summary, "22.4.0") {
		t.Errorf("summary must name both versions: %q", s.Summary)
	}
	if strings.Contains(s.Summary, "version file") {
		t.Errorf("no version file is involved: %q", s.Summary)
	}
	if !reflectEqual(s.Waste, []string{"W2"}) {
		t.Errorf("waste = %v, want [W2]", s.Waste)
	}
}

func reflectEqual(a, b []string) bool {
	return strings.Join(a, "\x00") == strings.Join(b, "\x00")
}

// TestScan_toolchain_drift_negative: a tool pinned identically everywhere,
// or used in a single file, emits nothing.
func TestScan_toolchain_drift_negative(t *testing.T) {
	t.Run("same-version-two-files", func(t *testing.T) {
		ci := parseFixtureAs(t, "toolchain-drift-node-a.yml", ".github/workflows/ci.yml")
		rel := parseFixtureAs(t, "toolchain-drift-node-a.yml", ".github/workflows/release.yml")
		requireNoSignal(t, scan.Files([]*workflow.File{ci, rel}, nil), "toolchain-drift")
	})
	t.Run("single-file", func(t *testing.T) {
		a := parseFixture(t, "toolchain-drift-node-a.yml")
		requireNoSignal(t, scan.Files([]*workflow.File{a}, nil), "toolchain-drift")
	})
	t.Run("different-versions-within-one-file", func(t *testing.T) {
		// Two versions inside one workflow is a deliberate matrix-like choice,
		// not drift between workflow files.
		const src = `on: push
jobs:
  old:
    runs-on: ubuntu-24.04
    steps:
      - uses: actions/setup-node@v4.1.0
        with: {node-version: '20.11.1'}
  new:
    runs-on: ubuntu-24.04
    steps:
      - uses: actions/setup-node@v4.1.0
        with: {node-version: '22.4.0'}
`
		f := parseText(t, ".github/workflows/ci.yml", src)
		requireNoSignal(t, scan.Files([]*workflow.File{f}, nil), "toolchain-drift")
	})
	t.Run("different-tools", func(t *testing.T) {
		a := parseFixture(t, "toolchain-drift-node-a.yml")
		uv := parseFixture(t, "toolchain-drift-uv-ci.yml")
		requireNoSignal(t, scan.Files([]*workflow.File{a, uv}, nil), "toolchain-drift")
	})
	t.Run("expression-version-is-unknown", func(t *testing.T) {
		// ${{ matrix.node }} is not a pin: its value is unknown, so it is
		// excluded from drift comparison rather than compared as a literal.
		a := parseFixture(t, "toolchain-drift-node-a.yml")
		expr := parseFixture(t, "toolchain-drift-node-expr.yml")
		requireNoSignal(t, scan.Files([]*workflow.File{a, expr}, nil), "toolchain-drift")
	})
}

// TestScan_toolchain_drift_version_file: a version file in one workflow and a
// literal pin in another may disagree; the signal says a version file is
// involved.
func TestScan_toolchain_drift_version_file(t *testing.T) {
	file := parseFixture(t, "toolchain-drift-node-file.yml")
	a := parseFixture(t, "toolchain-drift-node-a.yml")
	got := requireSignal(t, scan.Files([]*workflow.File{file, a}, nil), "toolchain-drift")
	if len(got) != 1 {
		t.Fatalf("want exactly one toolchain-drift signal, got %d: %+v", len(got), got)
	}
	s := got[0]
	if !strings.Contains(s.Summary, "version file") {
		t.Errorf("summary must say a version file is involved: %q", s.Summary)
	}
	requireEvidenceAt(t, s, file.Path, ".nvmrc")
	requireEvidenceAt(t, s, a.Path, "20.11.1")
}

// TestScan_toolchain_drift_uv: setup-uv pinned to different versions in
// release.yml and ci.yml is drift; release-only workflows are included.
func TestScan_toolchain_drift_uv(t *testing.T) {
	rel := parseFixtureAs(t, "toolchain-drift-uv-release.yml", ".github/workflows/release.yml")
	ci := parseFixtureAs(t, "toolchain-drift-uv-ci.yml", ".github/workflows/ci.yml")
	got := requireSignal(t, scan.Files([]*workflow.File{rel, ci}, nil), "toolchain-drift")
	if len(got) != 1 {
		t.Fatalf("want exactly one toolchain-drift signal, got %d: %+v", len(got), got)
	}
	requireEvidenceAt(t, got[0], rel.Path, "0.8.17")
	requireEvidenceAt(t, got[0], ci.Path, "0.12.19")

	t.Run("actions-setup-uv-groups-with-astral", func(t *testing.T) {
		const src = `on: push
jobs:
  test:
    runs-on: ubuntu-24.04
    steps:
      - uses: actions/setup-uv@v6.0.1
        with:
          version: '0.12.19'
`
		other := parseText(t, ".github/workflows/other.yml", src)
		got := requireSignal(t, scan.Files([]*workflow.File{rel, other}, nil), "toolchain-drift")
		if len(got) != 1 {
			t.Fatalf("want one signal across both setup-uv spellings, got %d", len(got))
		}
	})
}

// TestScan_floating_toolchain_uv: setup-uv with version: latest floats.
func TestScan_floating_toolchain_uv(t *testing.T) {
	for _, action := range []string{"astral-sh/setup-uv@v6.0.1", "actions/setup-uv@v6.0.1"} {
		t.Run(action, func(t *testing.T) {
			src := "on: push\njobs:\n  test:\n    runs-on: ubuntu-24.04\n    steps:\n      - uses: " + action + "\n        with:\n          version: latest\n"
			f := parseText(t, ".github/workflows/ci.yml", src)
			got := requireSignal(t, scan.Files([]*workflow.File{f}, nil), "floating-toolchain")
			if len(got) != 1 {
				t.Fatalf("want one floating-toolchain signal, got %d", len(got))
			}
			if got[0].Evidence[0].Line != 6 || !strings.Contains(got[0].Evidence[0].Note, "version=latest") {
				t.Errorf("evidence = %+v", got[0].Evidence)
			}
		})
	}
	t.Run("exact-version-ok", func(t *testing.T) {
		f := parseFixture(t, "toolchain-drift-uv-ci.yml")
		requireNoSignal(t, scan.Files([]*workflow.File{f}, nil), "floating-toolchain")
	})
}

// nodePin is a one-job workflow pinning setup-node to version.
func nodePin(t *testing.T, path, version string) *workflow.File {
	t.Helper()
	return parseText(t, path, "on: push\njobs:\n  build:\n    runs-on: ubuntu-24.04\n    steps:\n      - uses: actions/setup-node@v4.1.0\n        with:\n          node-version: "+version+"\n")
}

// T4 M2: the summary shows normalised values, so a leading v never makes the
// summary (part of the scan-diff identity) depend on workflow order.
func TestScan_toolchain_drift_summary_normalised(t *testing.T) {
	v := nodePin(t, ".github/workflows/a.yml", "'v20.11.1'")
	plain := nodePin(t, ".github/workflows/b.yml", "'20.11.1'")
	other := nodePin(t, ".github/workflows/c.yml", "'22.4.0'")
	want := "node toolchain versions differ across workflow files: 20.11.1, 22.4.0"
	for _, order := range [][]*workflow.File{{v, plain, other}, {plain, v, other}, {other, v, plain}} {
		got := requireSignal(t, scan.Files(order, nil), "toolchain-drift")
		if len(got) != 1 || got[0].Summary != want {
			t.Fatalf("summary %q; want %q", got[0].Summary, want)
		}
		// Evidence keeps each step's literal value.
		requireEvidenceAt(t, got[0], v.Path, "v20.11.1")
	}
}

// T4 M3: three files given out of order cite evidence in path order, and any
// input order yields the identical signal (evidence order is part of the
// scan-diff identity).
func TestScan_toolchain_drift_evidence_order(t *testing.T) {
	a := nodePin(t, ".github/workflows/a.yml", "'18.20.4'")
	b := nodePin(t, ".github/workflows/b.yml", "'20.11.1'")
	c := nodePin(t, ".github/workflows/c.yml", "'22.4.0'")
	var first signal.Signal
	for i, order := range [][]*workflow.File{{c, a, b}, {b, c, a}, {a, b, c}} {
		got := requireSignal(t, scan.Files(order, nil), "toolchain-drift")
		if len(got) != 1 || len(got[0].Evidence) != 3 {
			t.Fatalf("want one signal citing 3 files: %+v", got)
		}
		for j, want := range []string{a.Path, b.Path, c.Path} {
			if got[0].Evidence[j].Path != want {
				t.Fatalf("order %d: evidence %d is %s, want %s", i, j, got[0].Evidence[j].Path, want)
			}
		}
		if i == 0 {
			first = got[0]
		} else if !reflect.DeepEqual(first, got[0]) {
			t.Fatalf("input order changed the signal:\n%+v\n%+v", first, got[0])
		}
	}
}
