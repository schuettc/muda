package record

import (
	"testing"
	"time"
)

// clock sets the record clock to a UTC date for the rest of the test.
func clock(t *testing.T, date string) {
	t.Helper()
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		t.Fatal(err)
	}
	old := now
	now = func() time.Time { return d.Add(12 * time.Hour) }
	t.Cleanup(func() { now = old })
}

func codes(s *Store) map[string]int {
	c := map[string]int{}
	for _, p := range s.Check() {
		c[p.Code]++
	}
	return c
}

// exemptFinding records F-001 exempt under E-1 (review-by 2030-01-10),
// agreed on 2030-01-01.
func exemptFinding(t *testing.T) (*Store, string) {
	t.Helper()
	clock(t, "2030-01-01")
	s := store(t)
	id, err := s.AddFinding(finding())
	if err != nil {
		t.Fatal(err)
	}
	e := exemption()
	e.ID, e.Date, e.ReviewBy = "E-1", "2030-01-01", "2030-01-10"
	if err := s.Exempt(e); err != nil {
		t.Fatal(err)
	}
	st, eid := ExemptStatus, "E-1"
	if err := s.SetFinding(id, FindingPatch{Status: &st, ExemptionID: &eid}); err != nil {
		t.Fatal(err)
	}
	if c := codes(s); len(c) != 0 {
		t.Fatal(c)
	}
	return s, id
}

// Rule 1: renewal supersedes an expired exemption; check returns to clean.
func TestRenewalSupersedesExpiredExemption(t *testing.T) {
	s, id := exemptFinding(t)
	clock(t, "2030-01-11")
	if c := codes(s); c["expired-exemption"] != 1 {
		t.Fatalf("referenced expired exemption not reported: %v", c)
	}
	e := exemption()
	e.ID, e.Date, e.ReviewBy = "E-2", "2030-01-11", "2030-06-01"
	if err := s.Exempt(e); err != nil {
		t.Fatal(err)
	}
	st, eid := ExemptStatus, "E-2"
	if err := s.SetFinding(id, FindingPatch{Status: &st, ExemptionID: &eid}); err != nil {
		t.Fatal(err)
	}
	if ps := s.Check(); len(ps) != 0 {
		t.Fatalf("superseded E-1 still a problem: %v", ps)
	}
}

// Rule 2: a declined renewal reopens the lapsed finding and clears its
// exemption-id; the lapsed exemption becomes history.
func TestDeclinedRenewalReopensLapsedExemption(t *testing.T) {
	s, id := exemptFinding(t)
	clock(t, "2030-01-11")
	st := Reopened
	if err := s.SetFinding(id, FindingPatch{Status: &st}); err != nil {
		t.Fatalf("exempt -> reopened after expiry refused: %v", err)
	}
	var doc findingsFile
	if err := s.load("findings.toml", &doc); err != nil {
		t.Fatal(err)
	}
	if f := doc.Findings[0]; f.Status != Reopened || f.ExemptionID != "" {
		t.Fatalf("%+v", f)
	}
	if ps := s.Check(); len(ps) != 0 {
		t.Fatal(ps)
	}
	// Then treated as an existing finding: reopened -> approved is legal.
	st = Approved
	if err := s.SetFinding(id, FindingPatch{Status: &st}); err != nil {
		t.Fatal(err)
	}
}

func TestReopenRequiresLapsedExemption(t *testing.T) {
	s, id := exemptFinding(t)
	st := Reopened
	// Before expiry (review-by day itself is still valid).
	clock(t, "2030-01-10")
	if err := s.SetFinding(id, FindingPatch{Status: &st}); err == nil {
		t.Fatal("exempt -> reopened before expiry")
	}
	clock(t, "2030-01-11")
	// Reopening may not install another exemption-id.
	eid := "E-1"
	if err := s.SetFinding(id, FindingPatch{Status: &st, ExemptionID: &eid}); err == nil {
		t.Fatal("reopened kept an exemption-id")
	}
	// Every other exit from exempt stays forbidden, even after expiry.
	for _, b := range []Status{OpenStatus, Approved, InPR, Fixed} {
		if err := s.SetFinding(id, FindingPatch{Status: &b}); err == nil {
			t.Fatalf("exempt -> %s", b)
		}
	}
}

// Rule 3: record exempt rejects a review-by before today (UTC).
func TestExemptRejectsPastReviewBy(t *testing.T) {
	clock(t, "2030-01-11")
	s := store(t)
	e := exemption()
	e.Date, e.ReviewBy = "2030-01-01", "2030-01-10"
	if err := s.Exempt(e); err == nil {
		t.Fatal("past review-by accepted")
	}
	e.ReviewBy = "2030-01-11" // today is allowed
	if err := s.Exempt(e); err != nil {
		t.Fatal(err)
	}
}

// in-pr and fixed findings must carry their PR link.
func TestInPRAndFixedRequirePR(t *testing.T) {
	s := store(t)
	id, err := s.AddFinding(finding())
	if err != nil {
		t.Fatal(err)
	}
	st := Approved
	if err := s.SetFinding(id, FindingPatch{Status: &st}); err != nil {
		t.Fatal(err)
	}
	st = InPR
	if err := s.SetFinding(id, FindingPatch{Status: &st}); err == nil {
		t.Fatal("approved -> in-pr without pr accepted")
	}
	empty := ""
	if err := s.SetFinding(id, FindingPatch{Status: &st, PR: &empty}); err == nil {
		t.Fatal("in-pr with empty pr accepted")
	}
	pr := "https://github.com/o/r/pull/1"
	if err := s.SetFinding(id, FindingPatch{Status: &st, PR: &pr}); err != nil {
		t.Fatal(err)
	}
	st = Fixed
	if err := s.SetFinding(id, FindingPatch{Status: &st, PR: &empty}); err == nil {
		t.Fatal("fixed with pr cleared accepted")
	}
	if err := s.WriteReceipt(id, validReceipt()); err != nil { // fixed needs a passing receipt
		t.Fatal(err)
	}
	if err := s.SetFinding(id, FindingPatch{Status: &st}); err != nil {
		t.Fatal(err)
	}
	if ps := s.Check(); len(ps) != 0 {
		t.Fatal(ps)
	}
}
