package compare

import (
	"encoding/json"
	"testing"

	"github.com/schuettc/muda/internal/gates"
	"github.com/schuettc/muda/internal/gh"
	"github.com/schuettc/muda/internal/record"
)

// GitHub renders a ruleset's timestamps in the requester's timezone, so the
// same ruleset read from another timezone differs only in its offsets (#23).
func TestRulesetTimestampOffsetIsNotASecurityChange(t *testing.T) {
	side := func(created, updated string) string {
		return `{"id":7,"name":"main","enforcement":"active","created_at":"` + created + `","updated_at":"` + updated + `","rules":[{"type":"deletion"}]}`
	}
	build := func(raw string) *gates.Inventory {
		inv := inventory(`{}`)
		inv.Security = append(inv.Security, gh.SecuritySnapshot{What: "ruleset main", URL: "https://example.test/rulesets/7", Complete: true, Raw: json.RawMessage(raw)})
		inv.Rulesets = []gh.Ruleset{{ID: 7, Name: "main", Enforcement: "active", RawSecurity: json.RawMessage(raw)}}
		return inv
	}
	before := build(side("2026-08-26T15:43:02.920-05:00", "2026-08-26T15:43:02.920-05:00"))
	sameInstant := build(side("2026-08-26T22:43:02.920+02:00", "2026-08-26T22:43:02.920+02:00"))
	if d := GateChanges(before, sameInstant); d.Status != record.GateVerified || len(d.Unverified) != 0 {
		t.Fatalf("same instant in another timezone not VERIFIED: %+v", d.Unverified)
	}
	edited := build(side("2026-08-26T22:43:02.920+02:00", "2026-10-14T09:00:00+02:00"))
	if d := GateChanges(before, edited); d.Status == record.GateVerified {
		t.Fatal("a later updated_at must still read as a change")
	}
}
