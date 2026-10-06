package signal

import (
	"reflect"
	"sort"
	"testing"
)

// specTable is the design spec's §4 signal table, verbatim. The Waste column
// is split into classes: `slow-step` names no waste class ("a time sink the
// recipes match on step name or content"), and `rerun` is "W7 or flake".
var specTable = []Def{
	{ID: "slow-step", From: "measure", Waste: []string{}},
	{ID: "queue-time", From: "measure", Waste: []string{"W8"}},
	{ID: "rerun", From: "measure", Waste: []string{"W7", "flake"}},
	{ID: "tree-rechecked", From: "measure", Waste: []string{"W6"}},
	{ID: "handoff-failure", From: "measure", Waste: []string{"W7"}},
	{ID: "floating-ref", From: "scan", Waste: []string{"W2"}},
	{ID: "floating-runner", From: "scan", Waste: []string{"W2"}},
	{ID: "floating-toolchain", From: "scan", Waste: []string{"W2"}},
	{ID: "toolchain-drift", From: "scan", Waste: []string{"W2"}},
	{ID: "emulation", From: "scan", Waste: []string{"W3"}},
	{ID: "uncached-install", From: "scan", Waste: []string{"W4"}},
	{ID: "serial-independent-jobs", From: "scan", Waste: []string{"W5"}},
	{ID: "duplicate-check-set", From: "scan", Waste: []string{"W6"}},
	{ID: "no-concurrency", From: "scan", Waste: []string{"W6"}},
	{ID: "no-path-filter", From: "scan", Waste: []string{"W6"}},
	{ID: "advance-notice", From: "notices", Waste: []string{"W10"}},
}

func TestRegistryMatchesSpecTable(t *testing.T) {
	if len(Registry) != len(specTable) {
		t.Fatalf("registry has %d signals, spec table has %d", len(Registry), len(specTable))
	}
	for _, want := range specTable {
		got, ok := Registry[want.ID]
		if !ok {
			t.Errorf("registry missing %s", want.ID)
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("registry[%s] = %+v, spec %+v", want.ID, got, want)
		}
		if !Known(want.ID) {
			t.Errorf("Known(%q) = false", want.ID)
		}
	}
	for _, bad := range []string{"", "Slow-step", "slow_step", "W6"} {
		if Known(bad) {
			t.Errorf("Known(%q) = true", bad)
		}
	}
	ids := IDs()
	if !sort.StringsAreSorted(ids) || len(ids) != len(specTable) {
		t.Errorf("IDs() = %v, want sorted list of all ids", ids)
	}
}

func TestNewUsesRegistryWaste(t *testing.T) {
	s := New("rerun", SeverityWarn, "x", []Evidence{{Kind: KindRun, URL: "u"}})
	if !reflect.DeepEqual(s.Waste, []string{"W7", "flake"}) || s.Severity != "warn" {
		t.Fatalf("New: %+v", s)
	}
	s.Waste[0] = "mutated"
	if Registry["rerun"].Waste[0] != "W7" {
		t.Fatal("New must copy registry waste, not alias it")
	}
	defer func() {
		if recover() == nil {
			t.Fatal("New with an unknown id must panic: signal ids are a contract")
		}
	}()
	New("not-a-signal", SeverityInfo, "", nil)
}
