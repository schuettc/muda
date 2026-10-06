package scan

import (
	"testing"

	"github.com/schuettc/muda/internal/signal"
	"github.com/schuettc/muda/internal/workflow"
)

// The (file, job, manager) of an uncached-install claim and of an unknown
// cache coverage note are read from the producer's own output, so consumers
// never parse its wording.
func TestCacheSitesFromProducerOutput(t *testing.T) {
	f, err := workflow.Parse(".github/workflows/ci.yml", []byte(`on: push
jobs:
  a:
    runs-on: ubuntu-24.04
    steps:
      - uses: actions/cache@v4
        with:
          path: node_modules
          key: k
      - run: npm ci
  b:
    runs-on: ubuntu-24.04
    steps:
      - run: npm ci && pip install -r requirements.txt
`))
	if err != nil {
		t.Fatal(err)
	}
	got := map[CacheSite]bool{}
	for _, s := range Files([]*workflow.File{f}, nil) {
		if site, ok := UncachedInstallSite(s); ok {
			got[site] = true
		} else if s.ID == "uncached-install" {
			t.Errorf("producer signal not recognized: %+v", s)
		}
	}
	want := map[CacheSite]bool{{".github/workflows/ci.yml", "b", "npm"}: true, {".github/workflows/ci.yml", "b", "pip"}: true}
	if len(got) != len(want) {
		t.Fatalf("uncached-install sites %v; want %v", got, want)
	}
	for site := range want {
		if !got[site] {
			t.Fatalf("uncached-install sites %v; want %v", got, want)
		}
	}
	unknown := map[CacheSite]bool{}
	for _, u := range cacheUnavailable([]*workflow.File{f}) {
		if site, ok := UnknownCacheSite(u); ok {
			unknown[site] = true
		} else {
			t.Errorf("producer note not recognized: %+v", u)
		}
	}
	if !unknown[CacheSite{".github/workflows/ci.yml", "a", "npm"}] {
		t.Fatalf("unknown cache sites %v lack job a npm", unknown)
	}
	for site := range unknown {
		if site.Job != "a" {
			t.Fatalf("unknown coverage attributed to another job: %v", site)
		}
	}
	// Anything else is not a site.
	if _, ok := UncachedInstallSite(signal.New("floating-ref", "medium", "job b: npm ci without cache", []signal.Evidence{{Path: "x.yml"}})); ok {
		t.Error("another signal read as uncached-install")
	}
	if _, ok := UncachedInstallSite(signal.New("uncached-install", "medium", "reworded", []signal.Evidence{{Path: "x.yml"}})); ok {
		t.Error("unrecognized summary read as a site")
	}
	for _, u := range []Unavailable{{What: "run history"}, {What: ".github/workflows/ci.yml"}, {What: ".github/workflows/ci.yml run 7 duration"}, {What: "x.yml job a cobol cache coverage"}} {
		if _, ok := UnknownCacheSite(u); ok {
			t.Errorf("%q read as a cache site", u.What)
		}
	}
}

// Final review m-4: an expression-valued toolchain pin is a named unavailable
// entry, and its tool is read back from the producer's own wording, as is a
// toolchain-drift signal's tool.
func TestToolchainExpressionPinIsNamed(t *testing.T) {
	parse := func(path, version string) *workflow.File {
		f, err := workflow.Parse(path, []byte("on: push\njobs:\n  test:\n    runs-on: ubuntu-24.04\n    steps:\n      - uses: actions/setup-node@v4.1.0\n        with:\n          node-version: "+version+"\n      - uses: actions/setup-python@v5\n        with:\n          python-version: '3.12.4'\n"))
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	expr := parse(".github/workflows/matrix.yml", "${{ matrix.node }}")
	got := toolchainUnavailable([]*workflow.File{expr, parse(".github/workflows/ci.yml", "'20.11.1'")})
	if len(got) != 1 || got[0].What != ".github/workflows/matrix.yml job test node version (expression)" || got[0].Why == "" {
		t.Fatalf("expression pin notes %+v", got)
	}
	if tool, ok := ExpressionPinTool(got[0]); !ok || tool != "node" {
		t.Fatalf("ExpressionPinTool(%+v) = %q, %v", got[0], tool, ok)
	}
	for _, u := range []Unavailable{{What: ".github/workflows/ci.yml"}, {What: ".github/workflows/ci.yml job a npm cache coverage"}, {What: "x job a cobol version (expression)"}} {
		if _, ok := ExpressionPinTool(u); ok {
			t.Errorf("read a tool from %+v", u)
		}
	}
	drift := Files([]*workflow.File{parse(".github/workflows/a.yml", "'18.20.4'"), parse(".github/workflows/b.yml", "'20.11.1'")}, nil)
	found := false
	for _, s := range drift {
		if tool, ok := DriftTool(s); ok {
			found = tool == "node"
		} else if s.ID == "toolchain-drift" {
			t.Errorf("producer drift signal not recognized: %+v", s)
		}
	}
	if !found {
		t.Fatalf("no node drift read from %+v", drift)
	}
}

// N-1: compare treats an expression pin note as unchanged only when it is
// equal, so the note names every expression pin of the tool in the job, in
// step order. A one-pin note keeps its earlier wording, so a recorded note
// still equals a fresh one.
func TestToolchainExpressionPinNoteListsEveryPin(t *testing.T) {
	steps := func(versions ...string) *workflow.File {
		y := "on: push\njobs:\n  test:\n    runs-on: ubuntu-24.04\n    steps:\n"
		for _, v := range versions {
			y += "      - uses: actions/setup-node@v4.1.0\n        with:\n          node-version: " + v + "\n"
		}
		f, err := workflow.Parse(".github/workflows/matrix.yml", []byte(y))
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	one := toolchainUnavailable([]*workflow.File{steps("${{ matrix.node }}", "'20.11.1'")})
	if len(one) != 1 || one[0].Why != "node-version=${{ matrix.node }} is an expression; its value is unknown, so it is not compared for toolchain drift" {
		t.Fatalf("one-pin note %+v", one)
	}
	two := toolchainUnavailable([]*workflow.File{steps("${{ matrix.node }}", "'20.11.1'", "${{ vars.node }}")})
	if len(two) != 1 || two[0].What != one[0].What || two[0].Why != `node-version="${{ matrix.node }}", node-version="${{ vars.node }}" are expressions; their values are unknown, so they are not compared for toolchain drift` {
		t.Errorf("two-pin note %+v", two)
	}
	// Quoting keeps a value that contains the separator from reading as a
	// different list of pins.
	three := toolchainUnavailable([]*workflow.File{steps("${{ matrix.a }}", "${{ matrix.b }}", "${{ matrix.c }}")})
	joined := toolchainUnavailable([]*workflow.File{steps("${{ matrix.a }}, node-version=${{ matrix.b }}", "${{ matrix.c }}")})
	if len(three) != 1 || len(joined) != 1 || three[0].Why == joined[0].Why {
		t.Fatalf("different pin lists share a note: %+v %+v", three, joined)
	}
}
