package record

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testPR = "https://github.com/o/r/pull/1"

func gateReceipt(status, pr string) Receipt {
	r := validReceipt()
	r.GateStatus, r.PR = status, pr
	return r
}

// inPR returns a store with F-001 in-pr under testPR.
func inPR(t *testing.T) (*Store, string) {
	t.Helper()
	s := store(t)
	id, err := s.AddFinding(finding())
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range []Status{Approved, InPR} {
		pr := testPR
		if err := s.SetFinding(id, FindingPatch{Status: &st, PR: &pr}); err != nil {
			t.Fatal(err)
		}
	}
	return s, id
}

func hasCode(err error, code string) bool { return err != nil && strings.Contains(err.Error(), code) }

func TestReceiptGateStatusAndPRValidated(t *testing.T) {
	s, id := inPR(t)
	for _, bad := range []Receipt{gateReceipt("", testPR), gateReceipt("PASS", testPR), gateReceipt("verified", testPR), gateReceipt(GateVerified, "")} {
		if err := s.WriteReceipt(id, bad); err == nil {
			t.Fatalf("receipt accepted: status=%q pr=%q", bad.GateStatus, bad.PR)
		}
	}
	for _, ok := range []string{GateVerified, GateExempted, GateUnverified, GateWeakened} {
		if err := s.WriteReceipt(id, gateReceipt(ok, testPR)); err != nil {
			t.Fatalf("%s: %v", ok, err)
		}
	}
}

func TestFixedRequiresPassingReceipt(t *testing.T) {
	fixed := Fixed
	s, id := inPR(t)
	if err := s.SetFinding(id, FindingPatch{Status: &fixed}); !hasCode(err, "unverified-fix") {
		t.Fatalf("fixed without receipt: %v", err)
	}
	for _, failing := range []string{GateUnverified, GateWeakened} {
		if err := s.WriteReceipt(id, gateReceipt(failing, testPR)); err != nil {
			t.Fatal(err)
		}
		if err := s.SetFinding(id, FindingPatch{Status: &fixed}); !hasCode(err, "unverified-fix") {
			t.Fatalf("fixed on %s: %v", failing, err)
		}
	}
	if err := s.WriteReceipt(id, gateReceipt(GateExempted, testPR)); err != nil {
		t.Fatal(err)
	}
	if err := s.SetFinding(id, FindingPatch{Status: &fixed}); err != nil {
		t.Fatal(err)
	}
	if ps := s.Check(); len(ps) != 0 {
		t.Fatal(ps)
	}
}

// A passing receipt from an earlier PR cycle cannot fix a later cycle.
func TestFixedRejectsStaleReceipt(t *testing.T) {
	fixed := Fixed
	s, id := inPR(t)
	if err := s.WriteReceipt(id, gateReceipt(GateVerified, "https://github.com/o/r/pull/0")); err != nil {
		t.Fatal(err)
	}
	if err := s.SetFinding(id, FindingPatch{Status: &fixed}); !hasCode(err, "unverified-fix") {
		t.Fatalf("stale receipt accepted: %v", err)
	}
}

func TestCheckFlagsUnverifiedFix(t *testing.T) {
	fixed := Fixed
	s, id := inPR(t)
	if err := s.WriteReceipt(id, gateReceipt(GateVerified, testPR)); err != nil {
		t.Fatal(err)
	}
	if err := s.SetFinding(id, FindingPatch{Status: &fixed}); err != nil {
		t.Fatal(err)
	}
	flagged := func() bool {
		for _, p := range s.Check() {
			if p.Code == "unverified-fix" {
				return true
			}
		}
		return false
	}
	if flagged() {
		t.Fatal("passing fixed flagged")
	}
	for _, r := range []Receipt{gateReceipt(GateUnverified, testPR), gateReceipt(GateVerified, "https://github.com/o/r/pull/9")} {
		if err := s.WriteReceipt(id, r); err != nil {
			t.Fatal(err)
		}
		if !flagged() {
			t.Fatalf("fixed with receipt status=%s pr=%s not flagged", r.GateStatus, r.PR)
		}
	}
	if err := os.Remove(filepath.Join(s.Root, ".muda", "receipts", id+".md")); err != nil {
		t.Fatal(err)
	}
	if !flagged() {
		t.Fatal("fixed without receipt not flagged")
	}
}
