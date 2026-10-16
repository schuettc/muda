package compare

import (
	"encoding/json"
	"testing"

	"github.com/subaud-tools/muda/internal/gates"
	"github.com/subaud-tools/muda/internal/gh"
	"github.com/subaud-tools/muda/internal/record"
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

// Review finding 1: a recorded inventory still round-trips byte-for-byte
// through load and re-marshal, so the raw-inventory check stays quiet for a
// baseline whose rulesets carry a non-UTC offset.
func TestRecordedTimezoneSurvivesRawRoundTrip(t *testing.T) {
	inv := inventory(`{}`)
	raw := `{"id":7,"name":"main","created_at":"2026-08-26T15:43:02.920-05:00","updated_at":"2026-08-26T15:43:02.920-05:00"}`
	inv.Rulesets = []gh.Ruleset{{ID: 7, Name: "main", RawSecurity: json.RawMessage(raw)}}
	recorded, err := json.Marshal(inv)
	if err != nil {
		t.Fatal(err)
	}
	var loaded gates.Inventory
	if err := json.Unmarshal(recorded, &loaded); err != nil {
		t.Fatal(err)
	}
	projected, err := json.Marshal(&loaded)
	if err != nil {
		t.Fatal(err)
	}
	if !rawInventoryEqual(recorded, projected) {
		t.Fatalf("round trip changed the record:\n%s\n%s", recorded, projected)
	}
}
