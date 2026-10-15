package record

import "testing"

// #29: an exempt finding whose waste was removed becomes fixed on the same
// proof as an in-pr one, before its exemption lapses, and the exemption is
// then history.
func TestExemptBecomesFixedWithProof(t *testing.T) {
	s, id := exemptFinding(t)
	clock(t, "2030-01-05") // before review-by
	fixed, pr := Fixed, testPR
	if err := s.SetFinding(id, FindingPatch{Status: &fixed, PR: &pr}); !hasCode(err, "unverified-fix") {
		t.Fatalf("exempt -> fixed without a receipt: %v", err)
	}
	if err := s.WriteReceipt(id, gateReceipt(GateWeakened, testPR)); err != nil {
		t.Fatal(err)
	}
	if err := s.SetFinding(id, FindingPatch{Status: &fixed, PR: &pr}); !hasCode(err, "unverified-fix") {
		t.Fatalf("exempt -> fixed on a weakened receipt: %v", err)
	}
	if err := s.WriteReceipt(id, gateReceipt(GateVerified, "https://github.com/o/r/pull/2")); err != nil {
		t.Fatal(err)
	}
	if err := s.SetFinding(id, FindingPatch{Status: &fixed, PR: &pr}); !hasCode(err, "unverified-fix") {
		t.Fatalf("exempt -> fixed on another PR's receipt: %v", err)
	}
	if err := s.WriteReceipt(id, gateReceipt(GateVerified, testPR)); err != nil {
		t.Fatal(err)
	}
	if err := s.SetFinding(id, FindingPatch{Status: &fixed, PR: &pr}); err != nil {
		t.Fatalf("exempt -> fixed with proof: %v", err)
	}
	var doc findingsFile
	if err := s.load("findings.toml", &doc); err != nil {
		t.Fatal(err)
	}
	if f := doc.Findings[0]; f.Status != Fixed || f.ExemptionID != "" || f.PR != testPR {
		t.Fatalf("%+v", f)
	}
	clock(t, "2030-02-01") // the old exemption lapses; it is history, not a problem
	if ps := s.Check(); len(ps) != 0 {
		t.Fatal(ps)
	}
}
