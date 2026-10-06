package compare

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/schuettc/muda/internal/gates"
	"github.com/schuettc/muda/internal/gh"
	"github.com/schuettc/muda/internal/record"
)

// Drift guard: every synthetic proof ID compare emits must be accepted by
// record.IsPolicyProofID, or gate: exemptions for it would be unmatched.
func TestEmittedPolicyProofIDsAreRecognized(t *testing.T) {
	var diffs []*GateDiff
	// policy:inventory (schema/repo mismatch), policy:availability:*,
	// policy:inventory:projection, policy:gate:* (duplicate and empty ids),
	// policy:security:* (duplicate, missing side, changed), policy:workflow:*.
	a, b := inventory(`{"a":1}`), inventory(`{"a":2}`)
	a.Repo = "o/other"
	a.Branch, b.Branch = "main", "trunk"
	a.Unavailable = []gates.Unavailable{{What: "branch protection", Why: "403"}}
	a.Gates = []gates.Gate{{ID: "g"}, {ID: "g"}, {ID: ""}}
	a.Security = append(a.Security, a.Security[0], gh.SecuritySnapshot{What: "ruleset:1", URL: "u", Complete: true, Raw: json.RawMessage(`{}`)})
	b.Sources = []gates.WorkflowSource{{Path: ".github/workflows/ci.yml", SHA256: rawHash("ci")}}
	// policy:workflow:<path>@<before raw hash> for a workflow the fix removed.
	a.Sources = []gates.WorkflowSource{{Path: ".github/workflows/gone.yml", SHA256: rawHash("gone")}}
	// policy:availability:<workflow path>@<raw hash> for an unparseable file.
	b.Unavailable = []gates.Unavailable{{What: ".github/workflows/deploy.yml", Why: "unparseable workflow: bad", SHA256: rawHash("deploy")}}
	diffs = append(diffs, GateChanges(a, b))
	// An unreadable workflow directory, and a workflow file that could not be
	// read (no hash): emitted, and intentionally never exemptable.
	dir := inventory(`{}`)
	dir.Unavailable = []gates.Unavailable{{What: ".github/workflows", Why: "unknown ref"}, {What: ".github/workflows/lost.yml", Why: "fetch: 500"}}
	diffs = append(diffs, GateChanges(dir, dir))
	// policy:security (complete security inventory missing).
	empty := inventory(`{}`)
	empty.Security = nil
	diffs = append(diffs, GateChanges(empty, empty))
	// policy:inventory via UnavailableGates, and policy:inventory:raw.
	diffs = append(diffs, UnavailableGates("no record"))
	raw := newGateDiff()
	unverifiedRaw(raw)
	diffs = append(diffs, raw)

	// Emitted IDs that name missing evidence and so must never match an
	// exemption. Everything else emitted must be recognized.
	unexemptable := map[string]bool{
		record.PolicyAvailabilityPrefix + ".github/workflows":          true,
		record.PolicyAvailabilityPrefix + ".github/workflows/lost.yml": true,
	}
	emittedUnexemptable := map[string]bool{}
	seen := map[string]bool{}
	for _, d := range diffs {
		for _, list := range [][]gates.Gate{d.Removed, d.Loosened, d.Unverified, d.Strengthened} {
			for _, g := range list {
				if !strings.HasPrefix(g.ID, "policy:") {
					continue
				}
				if unexemptable[g.ID] {
					emittedUnexemptable[g.ID] = true
					if record.IsPolicyProofID(g.ID) {
						t.Errorf("intentionally unexemptable ID is recognized: %q", g.ID)
					}
					continue
				}
				if !record.IsPolicyProofID(g.ID) {
					t.Errorf("emitted proof ID not recognized: %q", g.ID)
				}
				for _, form := range []string{record.PolicyInventory, record.PolicyInventoryRaw, record.PolicyInventoryProjection, record.PolicySecurity} {
					if g.ID == form {
						seen[form] = true
					}
				}
				for _, p := range []string{record.PolicyAvailabilityPrefix, record.PolicyGatePrefix, record.PolicySecurityPrefix, record.PolicyWorkflowPrefix} {
					if strings.HasPrefix(g.ID, p) {
						seen[p] = true
					}
				}
				if g.ID == record.PolicyAvailabilityPrefix+".github/workflows/deploy.yml@"+rawHash("deploy")[:record.WorkflowDigestLen] {
					seen["hashed availability"] = true
				}
				if g.ID == record.PolicyWorkflowPrefix+".github/workflows/gone.yml@"+rawHash("gone")[:record.WorkflowDigestLen] {
					seen["removed workflow"] = true
				}
			}
		}
	}
	for id := range unexemptable {
		if !emittedUnexemptable[id] {
			t.Errorf("fixture did not emit unexemptable %q", id)
		}
	}
	// The fixture must exercise every form, or the guard is vacuous.
	for _, form := range []string{record.PolicyInventory, record.PolicyInventoryRaw, record.PolicyInventoryProjection, record.PolicySecurity,
		record.PolicyAvailabilityPrefix, record.PolicyGatePrefix, record.PolicySecurityPrefix, record.PolicyWorkflowPrefix, "hashed availability", "removed workflow"} {
		if !seen[form] {
			t.Errorf("fixture did not emit %q", form)
		}
	}
}

func TestIsPolicyProofIDIsNarrow(t *testing.T) {
	for _, id := range []string{"policy:bogus", "policy:inventory:other", "policy:inventoryx", "policy:securityx", "policy:availability:", "policy:security:", "policy:workflow:", "required-check:main:test", "policy", "",
		// Workflow proof IDs must be content-bound: no digest-less form,
		// only exactly 64 (or an older record's 12) lowercase hex digits
		// after a non-empty path.
		"policy:workflow:.github/workflows/ci.yml",
		"policy:workflow:.github/workflows/ci.yml@",
		"policy:workflow:.github/workflows/ci.yml@0123456789AB",
		"policy:workflow:.github/workflows/ci.yml@0123456789a",
		"policy:workflow:.github/workflows/ci.yml@0123456789abc",
		"policy:workflow:.github/workflows/ci.yml@0123456789ag",
		"policy:workflow:@0123456789ab",
		// An unreadable workflow directory has no bytes to bind.
		"policy:availability:.github/workflows",
	} {
		if record.IsPolicyProofID(id) {
			t.Errorf("accepted %q", id)
		}
	}
	if !record.IsPolicyProofID("policy:workflow:.github/workflows/ci.yml@0123456789ab") {
		t.Error("rejected a content-bound workflow proof ID")
	}
}

// deployWorkflow is a deploy workflow whose deploy job needs a test job.
func deployWorkflow() gates.WorkflowSource {
	url := "https://github.com/o/r/blob/main/.github/workflows/deploy.yml"
	return gates.WorkflowSource{Path: ".github/workflows/deploy.yml", URL: url, Name: "deploy", SHA256: rawHash("deploy workflow"), Jobs: []gates.JobSource{
		{ID: "test", Line: 5, URL: url + "#L5", Needs: []string{}, Steps: []gates.StepSource{{Name: "test", Run: "go test ./...", Line: 8, URL: url + "#L8"}}},
		{ID: "deploy", Line: 10, URL: url + "#L10", Needs: []string{"test"}, Steps: []gates.StepSource{{Name: "ship", Run: "./deploy.sh", Line: 13, URL: url + "#L13"}}},
	}}
}

func workflowProofIDs(d *GateDiff) []string {
	var ids []string
	for _, g := range d.Unverified {
		if strings.HasPrefix(g.ID, record.PolicyWorkflowPrefix) {
			ids = append(ids, g.ID)
		}
	}
	return ids
}

func gateExemption(id string) record.Exemption {
	return record.Exemption{ID: "E-wf", Waste: "overprocessing", Location: "gate:" + id, Reason: "unattested workflow accepted", AgreedBy: "owner", Date: "2026-09-01", ReviewBy: "2099-01-01"}
}

// Regression (final review I1): an exemption for the attested workflow
// content must not cover a later weakening of that workflow.
func TestWorkflowExemptionIsContentBound(t *testing.T) {
	today := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	a := inventory(`{}`)
	a.Sources = []gates.WorkflowSource{deployWorkflow()}
	// The attested content is an edit of an earlier file (an identical file
	// is VERIFIED and needs no exemption).
	base := inventory(`{}`)
	earlier := deployWorkflow()
	earlier.SHA256 = rawHash("deploy workflow, earlier")
	base.Sources = []gates.WorkflowSource{earlier}
	if ids := workflowProofIDs(GateChanges(a, a)); len(ids) != 0 {
		t.Fatalf("identical workflow emitted %v", ids)
	}
	pre := workflowProofIDs(GateChanges(base, a))
	if len(pre) == 0 {
		t.Fatal("no workflow proof ID emitted")
	}
	for _, id := range pre {
		if !record.IsPolicyProofID(id) {
			t.Fatalf("emitted workflow proof ID not recognized: %q", id)
		}
	}
	// Deterministic: unchanged source gives the same ID across runs.
	if again := workflowProofIDs(GateChanges(base, a)); !slices.Equal(pre, again) {
		t.Fatalf("workflow proof ID not deterministic: %v vs %v", pre, again)
	}
	// The exemption covers the attested workflow.
	same := GateChanges(base, a)
	ApplyExemptions(same, []record.Exemption{gateExemption(pre[0])}, today)
	if !same.Passed() || same.Status != record.GateExempted {
		t.Fatalf("exact exemption did not cover unchanged workflow: %+v", same)
	}
	for _, weaken := range []func(*gates.WorkflowSource){
		// Each weakening edits the raw file, so its recorded hash changes.
		func(w *gates.WorkflowSource) {
			w.Jobs[0].ContinueOnError = "\"true\""
			w.SHA256 = rawHash("deploy workflow, test continues on error")
		},
		func(w *gates.WorkflowSource) {
			w.Jobs[0].Steps[0].Run = "go test ./... || true"
			w.SHA256 = rawHash("deploy workflow, test cannot fail")
		},
	} {
		b := inventory(`{}`)
		w := deployWorkflow()
		weaken(&w)
		b.Sources = []gates.WorkflowSource{w}
		d := GateChanges(a, b)
		ApplyExemptions(d, []record.Exemption{gateExemption(pre[0])}, today)
		if d.Passed() || d.Status != record.GateUnverified {
			t.Fatalf("pre-change exemption covered a weakened workflow: %+v", d)
		}
		post := workflowProofIDs(d)
		if len(post) == 0 || post[0] == pre[0] {
			t.Fatalf("changed workflow kept its proof ID: %v", post)
		}
		// A fresh decision for the exact after content is EXEMPTED.
		fresh := GateChanges(a, b)
		ApplyExemptions(fresh, []record.Exemption{gateExemption(post[0])}, today)
		if !fresh.Passed() || fresh.Status != record.GateExempted {
			t.Fatalf("exemption for the after digest did not apply: %+v", fresh)
		}
	}
	// A workflow missing after binds its before-side content: only the
	// exact file removed can be exempted (final review I-1).
	gone := inventory(`{}`)
	d := GateChanges(a, gone)
	ids := workflowProofIDs(d)
	if len(ids) != 1 || ids[0] != pre[0] {
		t.Fatalf("removed workflow IDs %v; want [%s]", ids, pre[0])
	}
	stale := GateChanges(a, gone)
	ApplyExemptions(stale, []record.Exemption{gateExemption(record.PolicyWorkflowPrefix + ".github/workflows/deploy.yml@" + rawHash("deploy workflow, edited")[:record.WorkflowDigestLen])}, today)
	if stale.Passed() || stale.Status != record.GateUnverified {
		t.Fatalf("removed workflow exempted by another file's hash: %+v", stale)
	}
	ApplyExemptions(d, []record.Exemption{gateExemption(pre[0])}, today)
	if !d.Passed() || d.Status != record.GateExempted {
		t.Fatalf("exact removed-file exemption did not apply: %+v", d)
	}
}
