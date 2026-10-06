package record

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixedFinding returns a store with F-001 fixed under testPR.
func fixedFinding(t *testing.T) (*Store, string) {
	t.Helper()
	s, id := inPR(t)
	if err := s.WriteReceipt(id, gateReceipt(GateVerified, testPR)); err != nil {
		t.Fatal(err)
	}
	fixed := Fixed
	if err := s.SetFinding(id, FindingPatch{Status: &fixed}); err != nil {
		t.Fatal(err)
	}
	return s, id
}

// Release prep P6 (decision 6): fixed -> reopened is allowed only with a
// reopen-reason, for a finding whose PR did not merge. The CLI is offline
// and cannot check the PR; the reason records the human's word.
func TestFixedReopensOnlyWithReason(t *testing.T) {
	reopened := Reopened
	for _, reason := range []*string{nil, new(string), func() *string { v := "  "; return &v }()} {
		s, id := fixedFinding(t)
		if err := s.SetFinding(id, FindingPatch{Status: &reopened, ReopenReason: reason}); !hasCode(err, "missing-reopen-reason") {
			t.Fatalf("reason %v: got %v", reason, err)
		}
	}
	s, id := fixedFinding(t)
	why := "PR 1 was closed without merging (gh pr view: mergedAt null)"
	if err := s.SetFinding(id, FindingPatch{Status: &reopened, ReopenReason: &why}); err != nil {
		t.Fatal(err)
	}
	var doc findingsFile
	if err := s.load("findings.toml", &doc); err != nil {
		t.Fatal(err)
	}
	if f := doc.Findings[0]; f.Status != Reopened || f.ReopenReason != why || f.PR != testPR {
		t.Fatalf("%+v", f)
	}
	if ps := s.Check(); len(ps) != 0 {
		t.Fatal(ps)
	}
	// The reopened finding takes a new cycle: approved, a new PR, and a
	// fix that needs that PR's own receipt.
	approved, inpr, fixed := Approved, InPR, Fixed
	pr2 := "https://github.com/o/r/pull/2"
	if err := s.SetFinding(id, FindingPatch{Status: &approved}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetFinding(id, FindingPatch{Status: &inpr, PR: &pr2}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetFinding(id, FindingPatch{Status: &fixed}); !hasCode(err, "unverified-fix") {
		t.Fatalf("old receipt fixed the new PR: %v", err)
	}
}

// reopen-reason belongs to fixed -> reopened alone, and new findings never
// carry one.
func TestReopenReasonOnlyOnFixedToReopened(t *testing.T) {
	why := "not merged"
	reopened, approved := Reopened, Approved
	s, id := inPR(t)
	if err := s.SetFinding(id, FindingPatch{Status: &reopened, ReopenReason: &why}); !hasCode(err, "invalid-status-transition") {
		t.Fatalf("in-pr -> reopened with reopen-reason: %v", err)
	}
	if err := s.SetFinding(id, FindingPatch{Status: &reopened}); err != nil {
		t.Fatalf("in-pr -> reopened without a reason: %v", err)
	}
	if err := s.SetFinding(id, FindingPatch{Status: &approved, ReopenReason: &why}); !hasCode(err, "invalid-status-transition") {
		t.Fatalf("reopen-reason on approval: %v", err)
	}
	f := finding()
	f.ReopenReason = why
	if _, err := store(t).AddFinding(f); !hasCode(err, "invalid-new-finding") {
		t.Fatalf("new finding with reopen-reason: %v", err)
	}
	// Findings without a reason write none.
	b, err := os.ReadFile(filepath.Join(s.Root, ".muda", "findings.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "reopen-reason") {
		t.Fatalf("empty reopen-reason written:\n%s", b)
	}
}

// Fix round: a failed experiment's PR closes unmerged with the base still
// approved; approved -> reopened routes it back to diagnosis, without a
// reopen-reason (that belongs to fixed -> reopened).
func TestApprovedReopensForFailedExperiment(t *testing.T) {
	s := store(t)
	id, err := s.AddFinding(finding())
	if err != nil {
		t.Fatal(err)
	}
	approved, reopened := Approved, Reopened
	if err := s.SetFinding(id, FindingPatch{Status: &approved}); err != nil {
		t.Fatal(err)
	}
	why := "experiment failed"
	if err := s.SetFinding(id, FindingPatch{Status: &reopened, ReopenReason: &why}); !hasCode(err, "invalid-status-transition") {
		t.Fatalf("approved -> reopened with reopen-reason: %v", err)
	}
	ev := []Evidence{{Kind: "run", RunID: 42, Note: "experiment missed its pass criteria"}}
	if err := s.SetFinding(id, FindingPatch{Status: &reopened, Evidence: &ev}); err != nil {
		t.Fatalf("approved -> reopened: %v", err)
	}
	if ps := s.Check(); len(ps) != 0 {
		t.Fatal(ps)
	}
}
