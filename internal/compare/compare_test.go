package compare

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/BurntSushi/toml"
	"github.com/schuettc/muda/internal/gates"
	"github.com/schuettc/muda/internal/gh"
	"github.com/schuettc/muda/internal/ghtest"
	"github.com/schuettc/muda/internal/measure"
	"github.com/schuettc/muda/internal/record"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestCompareDeltas(t *testing.T) {
	a := &measure.Report{Workflows: []measure.WorkflowStats{{Path: "p", Runs: 1, P50: 10, Jobs: []measure.JobStats{{Name: "j", Steps: []measure.StepStats{{Name: "test", Occurrence: 1, Runs: 1, P50: 4}, {Name: "test", Occurrence: 2, Runs: 1, P50: 6}}}}}}}
	b := &measure.Report{Workflows: []measure.WorkflowStats{{Path: "p", Runs: 1, P50: 5, Jobs: []measure.JobStats{{Name: "j", Steps: []measure.StepStats{{Name: "test", Occurrence: 2, Runs: 1, P50: 3}}}}}}}
	r := Deltas(a, b, Window{}, Window{})
	if len(r.Workflows) != 1 || *r.Workflows[0].ChangeNS != -5 || *r.Workflows[0].RelativePercent != -50 || len(r.Steps) != 2 || r.Steps[0].After != nil || *r.Steps[1].ChangeNS != -3 {
		t.Fatalf("%+v", r)
	}
	a.Workflows[0].P50 = 0
	if Deltas(a, b, Window{}, Window{}).Workflows[0].RelativePercent != nil {
		t.Fatal("zero denominator")
	}
	if len(Deltas(&measure.Report{}, &measure.Report{}, Window{}, Window{}).Workflows) != 0 {
		t.Fatal("invented timings")
	}
}
func inventory(raw string) *gates.Inventory {
	return &gates.Inventory{Schema: 1, Repo: "o/r", Security: []gh.SecuritySnapshot{{What: "branch protection", URL: "https://example.test/security", Complete: true, Raw: json.RawMessage(raw)}}, Sources: []gates.WorkflowSource{}, Gates: []gates.Gate{}}
}
func TestCompareGatesRemovedFailsUnlessExempt(t *testing.T) {
	a, b := inventory(`{}`), inventory(`{}`)
	a.Gates = []gates.Gate{{ID: "required-check:main:test"}}
	diff := GateChanges(a, b)
	if diff.Passed() {
		t.Fatal("removed passed")
	}
	today := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	e := record.Exemption{ID: "E-test", Waste: "overprocessing", Location: "gate:required-check:main:test", Reason: "agreed", AgreedBy: "owner", Date: "2026-09-01", ReviewBy: "2026-10-01"}
	ApplyExemptions(diff, []record.Exemption{e}, today)
	if !diff.Passed() {
		t.Fatalf("not exempt: %+v", diff)
	}
	for _, variant := range []string{"expired", "unmatched", "missing", "future"} {
		d := GateChanges(a, b)
		x := e
		switch variant {
		case "expired":
			x.ReviewBy = "2026-09-27"
		case "unmatched":
			x.Location = "gate:other"
		case "missing":
			x.AgreedBy = ""
		case "future":
			x.Date = "2026-09-29"
		}
		ApplyExemptions(d, []record.Exemption{x}, today)
		if d.Passed() {
			t.Fatal(variant)
		}
	}
}
func TestSecurityChanges(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		kind string
	}{
		{`{"required_pull_request_reviews":{"required_approving_review_count":1}}`, `{"required_pull_request_reviews":{"required_approving_review_count":2}}`, "strong"},
		{`{"required_status_checks":{"strict":true}}`, `{"required_status_checks":{"strict":false}}`, "weak"},
		{`{"required_status_checks":{"contexts":["a"]}}`, `{"required_status_checks":{"contexts":["a","b"]}}`, "strong"},
		{`{"mystery":9007199254740993}`, `{"mystery":9007199254740992}`, "unknown"},
		{`{"actors":[1]}`, `{"actors":[2]}`, "unknown"},
		{`{"unknown":null}`, `{}`, "unknown"},
	} {
		d := GateChanges(inventory(tc.a), inventory(tc.b))
		switch tc.kind {
		case "strong":
			if !d.Passed() || len(d.Strengthened) != 1 {
				t.Fatalf("%+v", d)
			}
		case "weak":
			if len(d.Loosened) != 1 || d.Passed() {
				t.Fatalf("%+v", d)
			}
		case "unknown":
			if len(d.Unverified) != 1 || d.Passed() {
				t.Fatalf("%+v", d)
			}
		}
	}
	a, b := inventory(`{}`), inventory(`{}`)
	b.Security[0].Complete = false
	if GateChanges(a, b).Passed() {
		t.Fatal("unavailable passed")
	}
	a.Sources = []gates.WorkflowSource{{Path: "p", Jobs: []gates.JobSource{{ID: "test"}}}}
	b = a
	if GateChanges(a, b).Passed() {
		t.Fatal("incomplete source proof passed")
	}
}

func TestRawInventoryProof(t *testing.T) {
	for _, pair := range [][2]string{
		{`{"workflows":[{"defaults":""}]}`, `{"workflows":[{}]}`},
		{`{"workflows":[{"jobs":[{"continue_on_error":"false"}]}]}`, `{"workflows":[{"jobs":[{"continue_on_error":"true"}]}]}`},
		{`{"workflows":[{"jobs":[{"steps":[{"shell":"bash","working_directory":".","continue_on_error":"false"}]}]}]}`, `{"workflows":[{"jobs":[{"steps":[{"shell":"sh","working_directory":".","continue_on_error":"false"}]}]}]}`},
		{`{"workflows":[{"unknown":1}]}`, `{"workflows":[{}]}`},
		{`{"unknown":9007199254740993}`, `{"unknown":9007199254740992}`},
	} {
		if rawInventoryEqual([]byte(pair[0]), []byte(pair[1])) {
			t.Fatalf("discarded raw proof: %v", pair)
		}
	}
	if !rawInventoryEqual([]byte(`{"a":1,"b":[]}`), []byte(`{"b":[],"a":1}`)) {
		t.Fatal("object order is not policy")
	}
}

func TestMarkdownOmitsEmptyGateSections(t *testing.T) {
	d := newGateDiff()
	d.status()
	text := RenderMarkdown(&Result{Schema: 1, Gates: d})
	if strings.Contains(text, "### Removed") || strings.Contains(text, "### Loosened") {
		t.Fatal(text)
	}
}

func TestDuplicateGateIdentityFailsClosed(t *testing.T) {
	a := inventory(`{}`)
	a.Gates = []gates.Gate{{ID: "gate:test"}, {ID: "gate:test"}}
	if GateChanges(a, a).Passed() {
		t.Fatal("duplicate gate identity passed")
	}
}

func TestSecurityPolicyTokenBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, a, b string
		unknown    bool
	}{
		{"numeric slash root", `{"required_pull_request_reviews/required_approving_review_count":1}`, `{"required_pull_request_reviews/required_approving_review_count":2}`, true},
		{"boolean slash root", `{"required_status_checks/strict":false}`, `{"required_status_checks/strict":true}`, true},
		{"numeric mixed nesting", `{"required_pull_request_reviews":{"required_approving_review_count/":1}}`, `{"required_pull_request_reviews":{"required_approving_review_count/":2}}`, true},
		{"boolean mixed nesting", `{"required_status_checks/strict":{"":false}}`, `{"required_status_checks/strict":{"":true}}`, true},
		{"numeric genuine and impersonator", `{"required_pull_request_reviews":{"required_approving_review_count":1},"required_pull_request_reviews/required_approving_review_count":1}`, `{"required_pull_request_reviews":{"required_approving_review_count":2},"required_pull_request_reviews/required_approving_review_count":2}`, true},
		{"boolean genuine and impersonator", `{"required_status_checks":{"strict":false},"required_status_checks/strict":false}`, `{"required_status_checks":{"strict":true},"required_status_checks/strict":true}`, true},
		{"numeric genuine", `{"required_pull_request_reviews":{"required_approving_review_count":1}}`, `{"required_pull_request_reviews":{"required_approving_review_count":2}}`, false},
		{"boolean genuine", `{"required_status_checks":{"strict":false}}`, `{"required_status_checks":{"strict":true}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := GateChanges(inventory(tc.a), inventory(tc.b))
			if tc.unknown {
				if d.Passed() || d.Status != "UNVERIFIED" || len(d.Unverified) == 0 {
					t.Fatalf("unknown path certified: %+v", d)
				}
			} else if !d.Passed() || len(d.Strengthened) != 1 || len(d.Unverified) != 0 {
				t.Fatalf("genuine direction lost: %+v", d)
			}
		})
	}
}

func TestMarkdownFractionalWindowScope(t *testing.T) {
	since := time.Date(2026, 9, 1, 0, 0, 0, 123, time.UTC)
	until := since.Add(time.Nanosecond)
	r := &Result{Schema: 1, Before: Window{Since: since, Until: until}, After: Window{Since: until, Until: until.Add(time.Nanosecond)}}
	md := RenderMarkdown(r)
	for _, scope := range []string{"2026-09-01T00:00:00.000000123Z..2026-09-01T00:00:00.000000124Z", "2026-09-01T00:00:00.000000124Z..2026-09-01T00:00:00.000000125Z"} {
		if !strings.Contains(md, scope) {
			t.Fatalf("fractional scope %s lost: %s", scope, md)
		}
	}
}

// Final review I2: an explicit no-protection state is comparable.
func TestCompareNoBranchProtection(t *testing.T) {
	url := "https://api.github.com/repos/o/r/branches/main/protection"
	absent := func() *gates.Inventory {
		inv := inventory(`{}`)
		inv.Security = []gh.SecuritySnapshot{{What: "branch protection", URL: url, Complete: true, Absent: true}}
		return inv
	}
	present := func() *gates.Inventory {
		inv := inventory(`{"enforce_admins":{"enabled":true}}`)
		inv.Security[0].URL = url
		inv.Protection = &gh.Protection{EvidenceURL: url, RawSecurity: json.RawMessage(`{"enforce_admins":{"enabled":true}}`)}
		inv.Protection.EnforceAdmins.Enabled = true
		return inv
	}
	if d := GateChanges(absent(), absent()); !d.Passed() || d.Status != record.GateVerified || len(d.Unverified)+len(d.Loosened)+len(d.Strengthened) != 0 {
		t.Fatalf("absent→absent: %+v", d)
	}
	if d := GateChanges(absent(), present()); !d.Passed() || d.Status != record.GateVerified || len(d.Strengthened) != 1 || len(d.Unverified) != 0 {
		t.Fatalf("absent→present: %+v", d)
	}
	d := GateChanges(present(), absent())
	if d.Passed() || d.Status != record.GateWeakened || len(d.Loosened) != 1 || d.Loosened[0].ID != record.PolicySecurityPrefix+"branch protection" || len(d.Unverified) != 0 {
		t.Fatalf("present→absent: %+v", d)
	}
	today := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	ApplyExemptions(d, []record.Exemption{{ID: "E-bp", Waste: "overprocessing", Location: "gate:" + record.PolicySecurityPrefix + "branch protection", Reason: "agreed", AgreedBy: "owner", Date: "2026-09-01", ReviewBy: "2099-01-01"}}, today)
	if !d.Passed() || d.Status != record.GateExempted {
		t.Fatalf("loosened protection exemption: %+v", d)
	}
	// An absent marker on a present-looking record, or on another identity,
	// fails closed.
	odd := absent()
	odd.Protection = &gh.Protection{EvidenceURL: url}
	if GateChanges(odd, absent()).Passed() {
		t.Fatal("absent marker with a protection projection passed")
	}
	env := absent()
	env.Security[0].What = "environment:prod"
	if GateChanges(env, present()).Passed() {
		t.Fatal("absent marker on another identity passed")
	}
}

// The recorded unprotected tackle branch compares equal to itself: no
// availability or security proof gap for branch protection.
func TestTackleUnprotectedBranchComparesEqual(t *testing.T) {
	c := gh.New(gh.Options{NoCache: true, HTTP: &http.Client{Transport: ghtest.Replay("../ghtest/testdata/tackle")}})
	a, err := gates.Derive(context.Background(), c, "schuettc/tackle", "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := gates.Derive(context.Background(), c, "schuettc/tackle", "")
	if err != nil {
		t.Fatal(err)
	}
	d := GateChanges(a, b)
	for _, g := range append(append(d.Unverified, d.Loosened...), d.Removed...) {
		if strings.Contains(g.ID, "branch protection") {
			t.Fatalf("unprotected branch produced a proof gap: %+v", g)
		}
	}
}

// Final review M3: an unparseable workflow recorded as unavailable makes the
// gate comparison UNVERIFIED, never a pass.
func TestUnavailableWorkflowIsUnverified(t *testing.T) {
	a, b := inventory(`{}`), inventory(`{}`)
	b.Unavailable = []gates.Unavailable{{What: ".github/workflows/deploy.yml", Why: "line 8: YAML merge key (<<) is not supported"}}
	d := GateChanges(a, b)
	if d.Passed() || d.Status != record.GateUnverified {
		t.Fatalf("unavailable workflow passed: %+v", d)
	}
	found := false
	for _, g := range d.Unverified {
		found = found || g.ID == record.PolicyAvailabilityPrefix+".github/workflows/deploy.yml"
	}
	if !found {
		t.Fatalf("no availability proof gap: %+v", d.Unverified)
	}
}

// rawHash is the lowercase hex SHA-256 of raw file bytes, as gates records it.
func rawHash(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

const ciRawPush = "name: ci\non: push\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: go test ./...\n"
const ciRawPR = "name: ci\non: pull_request\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: go test ./...\n"

// ciSource is the parsed source both raw files above produce: gates does not
// project triggers, so only the raw hash tells them apart.
func ciSource(raw string) gates.WorkflowSource {
	return gates.WorkflowSource{Path: ".github/workflows/ci.yml", Name: "ci", SHA256: rawHash(raw), Jobs: []gates.JobSource{{ID: "test", Needs: []string{}, Steps: []gates.StepSource{{Run: "go test ./..."}}}}}
}

func TestWorkflowProofIDUsesRawHash(t *testing.T) {
	a := inventory(`{}`)
	a.Sources = []gates.WorkflowSource{ciSource(ciRawPush)}
	b := inventory(`{}`)
	b.Sources = []gates.WorkflowSource{ciSource(ciRawPush)}
	want := record.PolicyWorkflowPrefix + ".github/workflows/ci.yml@" + rawHash(ciRawPush)[:record.WorkflowDigestLen]
	// Decision 1: equal raw hashes are byte-identical files, VERIFIED.
	if ids := workflowProofIDs(GateChanges(a, b)); len(ids) != 0 {
		t.Fatalf("identical file emitted %v", ids)
	}
	// Only a trigger line changes: parsed sources are equal, the ID is not.
	c := inventory(`{}`)
	c.Sources = []gates.WorkflowSource{ciSource(ciRawPR)}
	x, y := ciSource(ciRawPush), ciSource(ciRawPR)
	x.SHA256, y.SHA256 = "", ""
	if !reflect.DeepEqual(x, y) {
		t.Fatal("fixture: parsed sources differ")
	}
	changed := workflowProofIDs(GateChanges(a, c))
	wantChanged := record.PolicyWorkflowPrefix + ".github/workflows/ci.yml@" + rawHash(ciRawPR)[:record.WorkflowDigestLen]
	if !slices.Contains(changed, wantChanged) || slices.Contains(changed, want) {
		t.Fatalf("trigger-only edit kept its ID: %v", changed)
	}
}

func TestUnparseableWorkflowProofBindsHash(t *testing.T) {
	today := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	broken := "on: push\njobs: [\n"
	path := ".github/workflows/deploy.yml"
	id := record.PolicyAvailabilityPrefix + path + "@" + rawHash(broken)[:record.WorkflowDigestLen]
	build := func(hash string) *GateDiff {
		a, b := inventory(`{}`), inventory(`{}`)
		b.Unavailable = []gates.Unavailable{{What: path, Why: "unparseable workflow: bad", SHA256: hash}}
		return GateChanges(a, b)
	}
	d := build(rawHash(broken))
	found := false
	for _, g := range d.Unverified {
		found = found || g.ID == id
	}
	if !found || !record.IsPolicyProofID(id) {
		t.Fatalf("no hashed availability ID %q: %+v", id, d.Unverified)
	}
	ApplyExemptions(d, []record.Exemption{gateExemption(id)}, today)
	if !d.Passed() || d.Status != record.GateExempted {
		t.Fatalf("exemption for the exact broken file did not apply: %+v", d)
	}
	other := build(rawHash(broken + "# edited\n"))
	ApplyExemptions(other, []record.Exemption{gateExemption(id)}, today)
	if other.Passed() || other.Status != record.GateUnverified {
		t.Fatalf("exemption covered a different file: %+v", other)
	}
	// A file that could not be read has no hash and no exemptable ID.
	unread := build("")
	for _, g := range unread.Unverified {
		if strings.HasPrefix(g.ID, record.PolicyAvailabilityPrefix+path) && record.IsPolicyProofID(g.ID) {
			t.Fatalf("hashless availability ID is exemptable: %q", g.ID)
		}
	}
	ApplyExemptions(unread, []record.Exemption{gateExemption(record.PolicyAvailabilityPrefix + path), gateExemption(id)}, today)
	if unread.Passed() {
		t.Fatalf("unreadable workflow exempted: %+v", unread)
	}
}

// TestLegacyInventoryWorkflowIDsUnexemptable pins the after-side branch: an
// after inventory whose source lacks a (valid) raw hash gets a digest-less,
// unexemptable workflow ID. In production the after side always comes from
// Derive, which hashes every parsed file; a legacy *recorded* inventory is
// covered by TestLegacyRecordedInventoryIsRawUnverified.
func TestLegacyInventoryWorkflowIDsUnexemptable(t *testing.T) {
	today := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	a := inventory(`{}`)
	a.Sources = []gates.WorkflowSource{ciSource(ciRawPush)}
	b := inventory(`{}`)
	legacy := ciSource(ciRawPush)
	legacy.SHA256 = ""
	b.Sources = []gates.WorkflowSource{legacy}
	d := GateChanges(a, b)
	ids := workflowProofIDs(d)
	if len(ids) == 0 {
		t.Fatal("no workflow proof ID")
	}
	var es []record.Exemption
	for _, id := range ids {
		if id != record.PolicyWorkflowPrefix+legacy.Path || record.IsPolicyProofID(id) {
			t.Fatalf("legacy source got a digest or exemptable ID: %q", id)
		}
		es = append(es, gateExemption(id))
	}
	// Neither the digest-less ID nor the before side's hashed ID matches.
	es = append(es, gateExemption(record.PolicyWorkflowPrefix+legacy.Path+"@"+rawHash(ciRawPush)[:record.WorkflowDigestLen]))
	ApplyExemptions(d, es, today)
	if d.Passed() || d.Status != record.GateUnverified || len(d.Exempted) != 0 {
		t.Fatalf("legacy workflow exempted: %+v", d)
	}
	// A malformed hash is no digest either.
	bad := ciSource(ciRawPush)
	bad.SHA256 = "XYZ"
	b.Sources = []gates.WorkflowSource{bad}
	for _, id := range workflowProofIDs(GateChanges(a, b)) {
		if record.IsPolicyProofID(id) {
			t.Fatalf("malformed hash gave an exemptable ID %q", id)
		}
	}
}

// The real legacy path: a gates record written before raw hashes has
// workflow sources without "sha256". verifyGates must not certify it: the
// typed projection adds the key, so the raw inventory is UNVERIFIED
// (policy:inventory:raw) and only an explicit exemption could accept it.
func TestLegacyRecordedInventoryIsRawUnverified(t *testing.T) {
	c := gh.New(gh.Options{NoCache: true, HTTP: &http.Client{Transport: ghtest.Replay("../ghtest/testdata/tackle")}})
	inv, err := gates.Derive(context.Background(), c, "schuettc/tackle", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Sources) == 0 {
		t.Fatal("fixture has no workflow sources; the legacy shape is not exercised")
	}
	current, err := json.Marshal(inv)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err = json.Unmarshal(current, &doc); err != nil {
		t.Fatal(err)
	}
	for _, w := range doc["workflows"].([]any) {
		delete(w.(map[string]any), "sha256")
	}
	legacy, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	write := func(raw []byte) string {
		var b bytes.Buffer
		if err := toml.NewEncoder(&b).Encode(map[string]any{"schema": 1, "inventory-json": string(raw)}); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "legacy.toml")
		if err := os.WriteFile(path, b.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	hasRaw := func(d *GateDiff) bool {
		for _, g := range d.Unverified {
			if g.ID == record.PolicyInventoryRaw {
				return true
			}
		}
		return false
	}
	if d := verifyGates(context.Background(), c, "schuettc/tackle", write(current), ""); hasRaw(d) {
		t.Fatalf("control: a current record is raw-unverified: %+v", d.Unverified)
	}
	d := verifyGates(context.Background(), c, "schuettc/tackle", write(legacy), "")
	if !hasRaw(d) || d.Passed() || d.Status != record.GateUnverified {
		t.Fatalf("legacy record without raw hashes was not raw-unverified: %+v", d)
	}
}

// exemptAllBut exempts every unverified proof ID except skip,
// so a test isolates one ID's exemptability.
func exemptAllBut(d *GateDiff, skip string) []record.Exemption {
	var es []record.Exemption
	for _, g := range d.Unverified {
		if g.ID != skip {
			es = append(es, gateExemption(g.ID))
		}
	}
	return es
}

// Final review I-1: a workflow present only on the before side (deleted by
// the fix) binds the before-side raw hash, so the exact file removed can be
// exempted, and nothing else can.
func TestRemovedWorkflowBindsBeforeHash(t *testing.T) {
	today := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	dupRaw := "name: dup\non: push\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: go test ./...\n"
	dup := ciSource(dupRaw)
	dup.Path, dup.Name = ".github/workflows/dup.yml", "dup"
	a := inventory(`{}`)
	a.Sources = []gates.WorkflowSource{ciSource(ciRawPush), dup}
	b := inventory(`{}`)
	b.Sources = []gates.WorkflowSource{ciSource(ciRawPush)}
	want := record.PolicyWorkflowPrefix + dup.Path + "@" + rawHash(dupRaw)[:record.WorkflowDigestLen]
	d := GateChanges(a, b)
	if !slices.Contains(workflowProofIDs(d), want) || !record.IsPolicyProofID(want) {
		t.Fatalf("removed workflow lacks before-bound ID %q: %v", want, workflowProofIDs(d))
	}
	for _, id := range workflowProofIDs(d) {
		if strings.HasPrefix(id, record.PolicyWorkflowPrefix+dup.Path) && id != want {
			t.Fatalf("removed workflow emitted another ID %q", id)
		}
	}
	ApplyExemptions(d, append(exemptAllBut(d, ""), gateExemption(want)), today)
	if !d.Passed() || d.Status != record.GateExempted {
		t.Fatalf("exemption for the exact removed file did not apply: %+v", d)
	}
	// Any other hash for the removed path, or the digest-less form, does not.
	other := GateChanges(a, b)
	es := exemptAllBut(other, want)
	es = append(es, gateExemption(record.PolicyWorkflowPrefix+dup.Path+"@"+rawHash(dupRaw + "# edited\n")[:record.WorkflowDigestLen]), gateExemption(record.PolicyWorkflowPrefix+dup.Path))
	ApplyExemptions(other, es, today)
	if other.Passed() || other.Status != record.GateUnverified {
		t.Fatalf("removed workflow exempted by another hash: %+v", other)
	}
}

// A renamed workflow: the old path binds the before hash, the new path the
// after hash; each is exemptable only by its exact hash.
func TestRenamedWorkflowBindsBeforeHash(t *testing.T) {
	today := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	old := ciSource(ciRawPush)
	renamed := ciSource(ciRawPR)
	renamed.Path = ".github/workflows/test.yml"
	a := inventory(`{}`)
	a.Sources = []gates.WorkflowSource{old}
	b := inventory(`{}`)
	b.Sources = []gates.WorkflowSource{renamed}
	oldID := record.PolicyWorkflowPrefix + old.Path + "@" + rawHash(ciRawPush)[:record.WorkflowDigestLen]
	newID := record.PolicyWorkflowPrefix + renamed.Path + "@" + rawHash(ciRawPR)[:record.WorkflowDigestLen]
	d := GateChanges(a, b)
	ids := workflowProofIDs(d)
	if !slices.Contains(ids, oldID) || !slices.Contains(ids, newID) {
		t.Fatalf("rename IDs %v; want %q and %q", ids, oldID, newID)
	}
	ApplyExemptions(d, []record.Exemption{gateExemption(oldID), gateExemption(newID)}, today)
	if !d.Passed() || d.Status != record.GateExempted {
		t.Fatalf("exact exemptions did not cover the rename: %+v", d)
	}
	// The old path under the new file's hash is not the file removed.
	wrong := GateChanges(a, b)
	ApplyExemptions(wrong, []record.Exemption{gateExemption(record.PolicyWorkflowPrefix + old.Path + "@" + rawHash(ciRawPR)[:record.WorkflowDigestLen]), gateExemption(newID)}, today)
	if wrong.Passed() || wrong.Status != record.GateUnverified {
		t.Fatalf("renamed-away file exempted by another hash: %+v", wrong)
	}
}

// A before-only path stays digest-less (fails closed) when the before side
// has no hash, or when the after side could not show the file is gone.
func TestRemovedWorkflowWithoutProofStaysUnexemptable(t *testing.T) {
	legacy := ciSource(ciRawPush)
	legacy.SHA256 = ""
	a := inventory(`{}`)
	a.Sources = []gates.WorkflowSource{legacy}
	cases := map[string]*gates.Inventory{"legacy before": inventory(`{}`)}
	hashed := inventory(`{}`)
	hashed.Sources = []gates.WorkflowSource{ciSource(ciRawPush)}
	dir := inventory(`{}`)
	dir.Unavailable = []gates.Unavailable{{What: ".github/workflows", Why: "unknown ref"}}
	cases["after directory unreadable"] = dir
	file := inventory(`{}`)
	file.Unavailable = []gates.Unavailable{{What: legacy.Path, Why: "fetch: 500"}}
	cases["after file unreadable"] = file
	for name, b := range cases {
		before := a
		if name != "legacy before" {
			before = hashed
		}
		for _, id := range workflowProofIDs(GateChanges(before, b)) {
			if id != record.PolicyWorkflowPrefix+legacy.Path {
				t.Fatalf("%s: got a digest-bound ID %q", name, id)
			}
		}
	}
}

// A --gates-file override carries the same historical pair as gates.toml: its
// recorded commit is the before side, and a half-written pair is unavailable.
func TestGatesFileHistoricalBeforeSide(t *testing.T) {
	c := gh.New(gh.Options{NoCache: true, HTTP: &http.Client{Transport: ghtest.Replay("../ghtest/testdata/tackle")}})
	inv, err := gates.Derive(context.Background(), c, "schuettc/tackle", "")
	if err != nil {
		t.Fatal(err)
	}
	sha := "0123456789abcdef0123456789abcdef01234567"
	inv.Ref = sha
	raw, err := json.Marshal(inv)
	if err != nil {
		t.Fatal(err)
	}
	write := func(doc map[string]any) string {
		var b bytes.Buffer
		if err := toml.NewEncoder(&b).Encode(doc); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "gates.toml")
		if err := os.WriteFile(path, b.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	d := verifyGates(context.Background(), c, "schuettc/tackle", write(map[string]any{"schema": 1, "inventory-json": string(raw), "historical-ref": sha, "settings-read": "2026-10-01"}), "")
	if d.BeforeRef != sha || d.BeforeSettings != "settings read 2026-10-01, not historical" {
		t.Fatalf("before side %+v", d)
	}
	d = verifyGates(context.Background(), c, "schuettc/tackle", write(map[string]any{"schema": 1, "inventory-json": string(raw), "historical-ref": sha}), "")
	if d.Passed() || len(d.Unverified) != 1 || !strings.Contains(d.Unverified[0].Detail, "invalid-historical") {
		t.Fatalf("half pair %+v", d)
	}
}

// Final re-review N-2: a workflow path that becomes a symlink or a submodule
// on the after side is not a removal; its ID stays digest-less, so the before
// file's hash (and any exemption for it) cannot cover what replaced it.
func TestWorkflowTurnedNonFileIsNotRemoved(t *testing.T) {
	const fixtures = "../ghtest/testdata/tackle"
	derive := func(rt http.RoundTripper) *gates.Inventory {
		t.Helper()
		inv, err := gates.Derive(context.Background(), gh.New(gh.Options{NoCache: true, HTTP: &http.Client{Transport: rt}}), "schuettc/tackle", "")
		if err != nil {
			t.Fatal(err)
		}
		return inv
	}
	before := derive(ghtest.Replay(fixtures))
	for name, rt := range map[string]http.RoundTripper{
		"symlink":   ghtest.NonFileWorkflows(ghtest.Replay(fixtures), []string{".github/workflows/release.yml"}, nil),
		"submodule": ghtest.NonFileWorkflows(dropWorkflow{ghtest.Replay(fixtures), ".github/workflows/release.yml"}, nil, []string{".github/workflows/release.yml"}),
	} {
		d := GateChanges(before, derive(rt))
		ids := workflowProofIDs(d)
		bare := record.PolicyAvailabilityPrefix + ".github/workflows/release.yml"
		named := []string{}
		for _, g := range d.Unverified {
			named = append(named, g.ID)
		}
		if !slices.Contains(named, bare) {
			t.Fatalf("%s: after side does not name the path unavailable: %v", name, named)
		}
		for _, id := range ids {
			if strings.HasPrefix(id, record.PolicyWorkflowPrefix+".github/workflows/release.yml@") {
				t.Fatalf("%s: non-file entry bound the before hash as a removal: %q", name, id)
			}
		}
	}
}

// dropWorkflow removes one path from the workflow directory listing, so a
// submodule can take its place.
type dropWorkflow struct {
	base http.RoundTripper
	path string
}

func (d dropWorkflow) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := d.base.RoundTrip(req)
	if err != nil || !strings.HasSuffix(req.URL.Path, "/contents/.github/workflows") {
		return resp, err
	}
	var entries []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return nil, err
	}
	_ = resp.Body.Close()
	kept := []map[string]any{}
	for _, e := range entries {
		if e["path"] != d.path {
			kept = append(kept, e)
		}
	}
	raw, err := json.Marshal(kept)
	if err != nil {
		return nil, err
	}
	resp.Body = io.NopCloser(bytes.NewReader(raw))
	return resp, nil
}

// Issue #74: Markdown shows durations as Go duration strings; JSON keeps ns.
func TestMarkdownDurationsReadable(t *testing.T) {
	change := -12 * time.Minute
	rel := -46.15
	r := &Result{Schema: 1, Workflows: []Delta{{
		Workflow: "ci", Name: "ci", Presence: "both",
		Before:   &Timing{Runs: 3, P50: 26 * time.Minute, P90: 30 * time.Minute},
		After:    &Timing{Runs: 3, P50: 14 * time.Minute, P90: 15*time.Minute + 30*time.Second},
		ChangeNS: &change, RelativePercent: &rel,
	}}}
	md := RenderMarkdown(r)
	for _, want := range []string{"Before: 3 samples; p50 26m0s; p90 30m0s", "After: 3 samples; p50 14m0s; p90 15m30s", "Change: -12m0s; relative: -46.15%"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown lacks %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, " ns") {
		t.Errorf("markdown prints raw nanoseconds:\n%s", md)
	}
	var buf bytes.Buffer
	if err := WriteJSON(&buf, r); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"p50_ns": 840000000000`, `"change_ns": -720000000000`} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("json lacks %q:\n%s", want, buf.String())
		}
	}
}
