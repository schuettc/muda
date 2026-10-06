package record

import "testing"

// A gate: exemption for a compare policy proof ID is matched by check; bogus
// policy IDs and unknown inventory IDs are still unmatched.
func TestCheckPolicyProofExemptions(t *testing.T) {
	for _, tc := range []struct {
		id        string
		unmatched bool
	}{
		{"policy:inventory:raw", false},
		{"policy:inventory", false},
		{"policy:inventory:projection", false},
		{"policy:security", false},
		{"policy:availability:branch protection", false},
		{"policy:availability:.github/workflows/ci.yml@0123456789ab", false},
		{"policy:availability:.github/workflows/ci.yml@" + fullDigest, false},
		{"policy:availability:.github/workflows/ci.yml", true},
		// The unreadable workflow directory certifies no evidence at all.
		{"policy:availability:.github/workflows", true},
		{"policy:gate:required:ci / test", false},
		{"policy:gate:", false},
		{"policy:security:ruleset:42", false},
		{"policy:workflow:.github/workflows/ci.yml@0123456789ab", false},
		{"policy:workflow:.github/workflows/ci.yml@" + fullDigest, false},
		{"policy:workflow:.github/workflows/ci.yml", true},
		{"policy:workflow:.github/workflows/ci.yml@0123456789AB", true},
		{"policy:bogus", true},
		{"policy:inventory:other", true},
		{"policy:securityx", true},
		{"policy:workflow:", true},
		{"nonexistent", true},
	} {
		s := store(t)
		e := exemption()
		e.ID, e.Location = "E-p", "gate:"+tc.id
		if LegacyProofID(tc.id) {
			// An older record's 12-hex form is no longer written by Exempt.
			if err := s.writeTOML("exemptions.toml", exemptionsFile{Schema: Schema, Exemptions: []Exemption{e}}); err != nil {
				t.Fatal(err)
			}
		} else if err := s.Exempt(e); err != nil {
			t.Fatalf("%s: %v", tc.id, err)
		}
		got := false
		for _, p := range s.Check() {
			if p.Code == "unmatched-exemption" {
				got = true
			}
		}
		if got != tc.unmatched {
			t.Errorf("gate:%s unmatched=%v, want %v", tc.id, got, tc.unmatched)
		}
	}
}

// An availability proof ID for a workflow file binds the raw file hash; any
// other availability identity keeps its plain form.
func TestIsPolicyProofIDWorkflowAvailability(t *testing.T) {
	for _, id := range []string{
		"policy:availability:branch protection",
		"policy:availability:.github/workflows/ci.yml@0123456789ab",
	} {
		if !IsPolicyProofID(id) {
			t.Errorf("rejected %q", id)
		}
	}
	for _, id := range []string{
		"policy:availability:.github/workflows/ci.yml",
		"policy:availability:.github/workflows/ci.yml@",
		"policy:availability:.github/workflows/ci.yml@0123456789AB",
		"policy:availability:.github/workflows/ci.yml@0123456789a",
		"policy:availability:.github/workflows/ci.yml@0123456789abc",
	} {
		if IsPolicyProofID(id) {
			t.Errorf("accepted %q", id)
		}
	}
}
