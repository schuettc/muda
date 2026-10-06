package record

import (
	"strings"
	"testing"
)

const (
	fullDigest   = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	legacyDigest = "0123456789ab"
)

// Final review m-10: a workflow proof ID carries the full SHA-256 of the raw
// file. The older 12-hex prefix form is still recognized when read.
func TestIsPolicyProofIDFullDigest(t *testing.T) {
	if WorkflowDigestLen != 64 || LegacyWorkflowDigestLen != 12 {
		t.Fatalf("digest lengths %d/%d; want 64/12", WorkflowDigestLen, LegacyWorkflowDigestLen)
	}
	for _, p := range []string{PolicyWorkflowPrefix, PolicyAvailabilityPrefix} {
		path := p + ".github/workflows/ci.yml@"
		for _, d := range []string{fullDigest, legacyDigest} {
			if !IsPolicyProofID(path + d) {
				t.Errorf("rejected %q", path+d)
			}
		}
		for _, d := range []string{fullDigest[:63], fullDigest + "0", fullDigest[:13], strings.ToUpper(fullDigest), fullDigest[:63] + "g"} {
			if IsPolicyProofID(path + d) {
				t.Errorf("accepted %q", path+d)
			}
		}
	}
}

// ProofCovers matches a full-form exemption exactly, and a legacy 12-hex
// exemption by prefix of the same path's full digest only.
func TestProofCovers(t *testing.T) {
	id := PolicyWorkflowPrefix + ".github/workflows/ci.yml@" + fullDigest
	for _, tc := range []struct {
		exempt string
		want   bool
	}{
		{id, true},
		{PolicyWorkflowPrefix + ".github/workflows/ci.yml@" + legacyDigest, true},
		{PolicyWorkflowPrefix + ".github/workflows/ci.yml@" + "ffffffffffff", false},
		{PolicyWorkflowPrefix + ".github/workflows/other.yml@" + legacyDigest, false},
		{PolicyAvailabilityPrefix + ".github/workflows/ci.yml@" + legacyDigest, false},
		{PolicyWorkflowPrefix + ".github/workflows/ci.yml@" + fullDigest[:13], false},
		{PolicyWorkflowPrefix + ".github/workflows/ci.yml", false},
		{"policy:inventory", false},
	} {
		if got := ProofCovers(tc.exempt, id); got != tc.want {
			t.Errorf("ProofCovers(%q) = %v; want %v", tc.exempt, got, tc.want)
		}
	}
	// Every other ID matches only exactly.
	if !ProofCovers("policy:inventory", "policy:inventory") || ProofCovers("required-check:main:t", "required-check:main:test") {
		t.Error("non-proof IDs must match exactly")
	}
	avail := PolicyAvailabilityPrefix + ".github/workflows/ci.yml@" + fullDigest
	if !ProofCovers(PolicyAvailabilityPrefix+".github/workflows/ci.yml@"+legacyDigest, avail) {
		t.Error("legacy availability exemption did not cover its file")
	}
}

// New records write the full digest: record exempt refuses a 12-hex
// location, while an older record holding one still loads and checks clean.
func TestExemptRequiresFullDigest(t *testing.T) {
	for _, p := range []string{PolicyWorkflowPrefix, PolicyAvailabilityPrefix} {
		s := store(t)
		e := exemption()
		e.Location = "gate:" + p + ".github/workflows/ci.yml@" + legacyDigest
		err := s.Exempt(e)
		if err == nil || !strings.Contains(err.Error(), "invalid-location") {
			t.Fatalf("legacy prefix location accepted for a new exemption: %v", err)
		}
		e.Location = "gate:" + p + ".github/workflows/ci.yml@" + fullDigest
		if err := s.Exempt(e); err != nil {
			t.Fatalf("full digest location refused: %v", err)
		}
	}
	s := store(t)
	e := exemption()
	e.Location = "gate:" + PolicyWorkflowPrefix + ".github/workflows/ci.yml@" + legacyDigest
	if err := s.writeTOML("exemptions.toml", exemptionsFile{Schema: Schema, Exemptions: []Exemption{e}}); err != nil {
		t.Fatal(err)
	}
	if ps := s.Check(); len(ps) != 0 {
		t.Fatalf("older record with a 12-hex exemption fails check: %v", ps)
	}
	snap, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if es, _ := snap["exemptions"].([]Exemption); len(es) != 1 || es[0].Location != e.Location {
		t.Fatalf("legacy exemption not loaded: %+v", snap["exemptions"])
	}
}
