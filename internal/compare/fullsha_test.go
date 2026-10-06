package compare

import (
	"slices"
	"testing"
	"time"

	"github.com/schuettc/muda/internal/gates"
	"github.com/schuettc/muda/internal/record"
)

// Final review m-10: compare emits the full raw-file SHA-256 in workflow and
// workflow-availability proof IDs, and an exemption recorded with the older
// 12-hex prefix still covers exactly the file whose full hash starts with it.
func TestProofIDsCarryFullDigest(t *testing.T) {
	today := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	a := inventory(`{}`)
	a.Sources = []gates.WorkflowSource{ciSource(ciRawPush)}
	// The before side is an earlier version, so the file is UNVERIFIED and
	// its ID binds the after side (an identical file emits none).
	c := inventory(`{}`)
	c.Sources = []gates.WorkflowSource{ciSource(ciRawPR)}
	d := GateChanges(c, a)
	full := record.PolicyWorkflowPrefix + ".github/workflows/ci.yml@" + rawHash(ciRawPush)
	if ids := workflowProofIDs(d); len(ids) == 0 || !slices.Equal(ids, slices.Repeat([]string{full}, len(ids))) {
		t.Fatalf("workflow proof IDs %v; want %s", ids, full)
	}
	broken := "on: push\njobs: [\n"
	path := ".github/workflows/deploy.yml"
	b := inventory(`{}`)
	b.Unavailable = []gates.Unavailable{{What: path, Why: "unparseable workflow: bad", SHA256: rawHash(broken)}}
	avail := record.PolicyAvailabilityPrefix + path + "@" + rawHash(broken)
	if !slices.ContainsFunc(GateChanges(inventory(`{}`), b).Unverified, func(g gates.Gate) bool { return g.ID == avail }) {
		t.Fatalf("no full-digest availability ID %q", avail)
	}

	legacy := record.PolicyWorkflowPrefix + ".github/workflows/ci.yml@" + rawHash(ciRawPush)[:12]
	old := GateChanges(c, a)
	ApplyExemptions(old, []record.Exemption{gateExemption(legacy)}, today)
	if !old.Passed() || old.Status != record.GateExempted || !slices.Equal(old.Exempted, []string{full}) {
		t.Fatalf("legacy 12-hex exemption did not cover its file: %+v", old)
	}
	// The legacy prefix binds the file it was recorded for, not an edit.
	edited := GateChanges(a, c)
	ApplyExemptions(edited, []record.Exemption{gateExemption(legacy)}, today)
	if edited.Passed() || len(edited.Exempted) != 0 {
		t.Fatalf("legacy exemption covered an edited file: %+v", edited)
	}
	// The legacy availability form still covers its exact broken file.
	ab := GateChanges(inventory(`{}`), b)
	ApplyExemptions(ab, []record.Exemption{gateExemption(record.PolicyAvailabilityPrefix + path + "@" + rawHash(broken)[:12])}, today)
	if !ab.Passed() || !slices.Equal(ab.Exempted, []string{avail}) {
		t.Fatalf("legacy availability exemption did not apply: %+v", ab)
	}
}
