package compare

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/schuettc/muda/internal/gates"
	"github.com/schuettc/muda/internal/gh"
	"github.com/schuettc/muda/internal/ghtest"
	"github.com/schuettc/muda/internal/record"
)

// workflowEntries counts every Unverified entry naming path, digest-bound or not.
func workflowEntries(d *GateDiff, path string) int {
	n := 0
	for _, g := range d.Unverified {
		if g.ID == record.PolicyWorkflowPrefix+path || strings.HasPrefix(g.ID, record.PolicyWorkflowPrefix+path+"@") {
			n++
		}
	}
	return n
}

func diffFor(t *testing.T, d *GateDiff, path string) WorkflowDiff {
	t.Helper()
	var found []WorkflowDiff
	for _, w := range d.WorkflowDiffs {
		if w.Path == path {
			found = append(found, w)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want one workflow diff for %s, got %+v", path, d.WorkflowDiffs)
	}
	return found[0]
}

func str(s string) *string { return &s }

// Decision 1: a workflow file whose recorded full SHA-256 is equal on both
// sides is proven unchanged: VERIFIED, no unknown entry, no exemption needed.
func TestIdenticalWorkflowIsVerified(t *testing.T) {
	a := inventory(`{}`)
	a.Sources = []gates.WorkflowSource{ciSource(ciRawPush), deployWorkflow()}
	b := inventory(`{}`)
	b.Sources = []gates.WorkflowSource{ciSource(ciRawPush), deployWorkflow()}
	d := GateChanges(a, b)
	if ids := workflowProofIDs(d); len(ids) != 0 {
		t.Fatalf("identical files left unknown entries: %v", ids)
	}
	if !d.Passed() || d.Status != record.GateVerified || len(d.WorkflowDiffs) != 0 {
		t.Fatalf("identical files not VERIFIED: %+v", d)
	}
	// Evidence still cites each side's file, labelled by side.
	notes := map[string]bool{}
	for _, e := range d.Evidence {
		if e.Path == ".github/workflows/deploy.yml" {
			notes[e.Note] = true
		}
	}
	if !notes["before workflow file"] || !notes["after workflow file"] {
		t.Fatalf("evidence does not name each side: %+v", d.Evidence)
	}
}

// One changed file beside an unchanged one: only the changed file is
// UNVERIFIED, listed once, bound to its after hash, with a key-level diff.
func TestOneByteChangeIsUnverifiedWithDiff(t *testing.T) {
	a := inventory(`{}`)
	a.Sources = []gates.WorkflowSource{ciSource(ciRawPush), deployWorkflow()}
	edited := deployWorkflow()
	edited.Jobs[0].Steps[0].Run = "go test ./...."
	edited.SHA256 = rawHash("deploy workflow.")
	// Line numbers and URLs move with any edit; they are not a change.
	edited.Jobs[1].Line, edited.Jobs[1].URL = 11, edited.URL+"#L11"
	b := inventory(`{}`)
	b.Sources = []gates.WorkflowSource{ciSource(ciRawPush), edited}
	d := GateChanges(a, b)
	want := record.PolicyWorkflowPrefix + edited.Path + "@" + edited.SHA256
	if ids := workflowProofIDs(d); len(ids) != 1 || ids[0] != want {
		t.Fatalf("workflow proof IDs %v; want only %s", ids, want)
	}
	if d.Passed() || d.Status != record.GateUnverified {
		t.Fatalf("changed file passed: %+v", d)
	}
	w := diffFor(t, d, edited.Path)
	if w.ProofID != want || w.Change != WorkflowChanged || w.Structure != StructureCompared {
		t.Fatalf("diff %+v", w)
	}
	if len(w.Fields) != 1 || w.Fields[0].Where != "jobs.test.steps[0].run" || *w.Fields[0].Old != "go test ./..." || *w.Fields[0].New != "go test ./...." {
		t.Fatalf("fields %+v", w.Fields)
	}
	if !strings.Contains(w.Note, "not compared") {
		t.Fatalf("note claims more than it parsed: %q", w.Note)
	}
}

// Every key the projection carries is compared, at workflow, job and step level.
func TestStructuredDiffKeys(t *testing.T) {
	a := inventory(`{}`)
	a.Sources = []gates.WorkflowSource{deployWorkflow()}
	w := deployWorkflow()
	w.SHA256 = rawHash("deploy workflow, edited")
	w.Name = "ship"
	w.Jobs[0].ContinueOnError = "true"
	w.Jobs[1].Needs = []string{}
	w.Jobs[1].Steps[0].With = map[string]string{"token": "x"}
	w.Jobs[1].Steps = append(w.Jobs[1].Steps, gates.StepSource{Name: "notify", Run: "./notify.sh"})
	w.Jobs = append(w.Jobs, gates.JobSource{ID: "lint", Needs: []string{}})
	b := inventory(`{}`)
	b.Sources = []gates.WorkflowSource{w}
	got := diffFor(t, GateChanges(a, b), w.Path).Fields
	want := []FieldChange{
		{Where: "name", Old: str("deploy"), New: str("ship")},
		{Where: "jobs.deploy.needs", Old: str("test"), New: nil},
		{Where: "jobs.deploy.steps[0].with.token", Old: nil, New: str("x")},
		{Where: "jobs.deploy.steps[1]", Old: nil, New: str("notify")},
		{Where: "jobs.lint", Old: nil, New: str("lint")},
		{Where: "jobs.test.continue-on-error", Old: nil, New: str("true")},
	}
	if len(got) != len(want) {
		t.Fatalf("fields %s", fieldsText(got))
	}
	for i := range want {
		if got[i].Where != want[i].Where || !sameValue(got[i].Old, want[i].Old) || !sameValue(got[i].New, want[i].New) {
			t.Fatalf("field %d: got %s", i, fieldsText(got))
		}
	}
}

func sameValue(a, b *string) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }

func fieldsText(fs []FieldChange) string {
	b, _ := json.Marshal(fs)
	return string(b)
}

// A change outside the parsed fields (a comment, or a trigger muda does not
// project) is reported as a raw file change, never as "no change".
func TestRawOnlyChangeSaysRawFileChanged(t *testing.T) {
	for name, raw := range map[string]string{"comment": ciRawPush + "# a comment\n", "trigger": ciRawPR} {
		a := inventory(`{}`)
		a.Sources = []gates.WorkflowSource{ciSource(ciRawPush)}
		b := inventory(`{}`)
		b.Sources = []gates.WorkflowSource{ciSource(raw)}
		d := GateChanges(a, b)
		if d.Passed() || workflowEntries(d, ".github/workflows/ci.yml") != 1 {
			t.Fatalf("%s: %+v", name, d)
		}
		w := diffFor(t, d, ".github/workflows/ci.yml")
		if w.Change != WorkflowChanged || w.Structure != StructureRawOnly || len(w.Fields) != 0 || !strings.Contains(w.Note, "raw file changed") {
			t.Fatalf("%s: %+v", name, w)
		}
	}
}

// A side that was not parsed is never compared structurally.
func TestUnparsedSideIsNotCompared(t *testing.T) {
	a := inventory(`{}`)
	a.Sources = []gates.WorkflowSource{ciSource(ciRawPush)}
	b := inventory(`{}`)
	b.Unavailable = []gates.Unavailable{{What: ".github/workflows/ci.yml", Why: "unparseable workflow: bad", SHA256: rawHash("jobs: [")}}
	d := GateChanges(a, b)
	w := diffFor(t, d, ".github/workflows/ci.yml")
	if w.Structure != StructureUnparsed || len(w.Fields) != 0 || !strings.Contains(w.Note, "raw file changed") || !strings.Contains(w.Note, "after side was not parsed") {
		t.Fatalf("%+v", w)
	}
	if d.Passed() || workflowEntries(d, ".github/workflows/ci.yml") != 1 {
		t.Fatalf("%+v", d)
	}
}

// Only a recorded full hash proves identity: a legacy 12-hex digest or a
// missing hash on either side stays UNVERIFIED, even for equal content.
func TestLegacyOrMissingHashStaysUnverified(t *testing.T) {
	full := ciSource(ciRawPush)
	for name, mod := range map[string]func(a, b *gates.WorkflowSource){
		"legacy before":  func(a, b *gates.WorkflowSource) { a.SHA256 = full.SHA256[:12] },
		"missing before": func(a, b *gates.WorkflowSource) { a.SHA256 = "" },
		"missing after":  func(a, b *gates.WorkflowSource) { b.SHA256 = "" },
		"legacy both":    func(a, b *gates.WorkflowSource) { a.SHA256, b.SHA256 = full.SHA256[:12], full.SHA256[:12] },
	} {
		x, y := ciSource(ciRawPush), ciSource(ciRawPush)
		mod(&x, &y)
		a, b := inventory(`{}`), inventory(`{}`)
		a.Sources, b.Sources = []gates.WorkflowSource{x}, []gates.WorkflowSource{y}
		d := GateChanges(a, b)
		if d.Passed() || d.Status != record.GateUnverified || workflowEntries(d, x.Path) != 1 {
			t.Fatalf("%s: identity claimed without full hashes: %+v", name, d)
		}
		w := diffFor(t, d, x.Path)
		if w.Change != WorkflowUnproven || strings.Contains(w.Note, "raw file changed") || !strings.Contains(w.Note, "identity cannot be proven") {
			t.Fatalf("%s: %+v", name, w)
		}
	}
}

// A deleted path still binds its before-side hash; the diff says so.
func TestDeletedWorkflowDiffBindsBeforeHash(t *testing.T) {
	a := inventory(`{}`)
	a.Sources = []gates.WorkflowSource{ciSource(ciRawPush), deployWorkflow()}
	b := inventory(`{}`)
	b.Sources = []gates.WorkflowSource{ciSource(ciRawPush)}
	d := GateChanges(a, b)
	want := record.PolicyWorkflowPrefix + ".github/workflows/deploy.yml@" + rawHash("deploy workflow")
	if ids := workflowProofIDs(d); len(ids) != 1 || ids[0] != want {
		t.Fatalf("IDs %v; want only %s", ids, want)
	}
	w := diffFor(t, d, ".github/workflows/deploy.yml")
	if w.Change != WorkflowRemoved || w.ProofID != want || !strings.Contains(w.Note, "before-side hash") {
		t.Fatalf("%+v", w)
	}
	added := diffFor(t, GateChanges(b, a), ".github/workflows/deploy.yml")
	if added.Change != WorkflowAdded || !strings.Contains(added.Note, "after-side hash") {
		t.Fatalf("%+v", added)
	}
}

// Each workflow path is listed once, not once per side (before on main and
// after on the branch), and an identical unparseable file once too.
func TestWorkflowListedOnce(t *testing.T) {
	a := inventory(`{}`)
	a.Sources = []gates.WorkflowSource{deployWorkflow()}
	legacy := deployWorkflow()
	legacy.SHA256 = ""
	b := inventory(`{}`)
	b.Sources = []gates.WorkflowSource{legacy}
	if n := workflowEntries(GateChanges(a, b), legacy.Path); n != 1 {
		t.Fatalf("digest-less path listed %d times", n)
	}
	a.Sources = []gates.WorkflowSource{legacy}
	if n := workflowEntries(GateChanges(a, a), legacy.Path); n != 1 {
		t.Fatalf("path with no hash on both sides listed %d times", n)
	}
	broken := gates.Unavailable{What: ".github/workflows/x.yml", Why: "unparseable workflow: bad", SHA256: rawHash("jobs: [")}
	u := inventory(`{}`)
	u.Unavailable = []gates.Unavailable{broken}
	n := 0
	for _, g := range GateChanges(u, u).Unverified {
		if strings.HasPrefix(g.ID, record.PolicyAvailabilityPrefix+broken.What) {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("unparseable file listed %d times", n)
	}
	// The same security citation on both sides is cited once.
	if ev := GateChanges(u, u).Evidence; len(ev) != 1 {
		t.Fatalf("evidence %+v", ev)
	}
}

// The real producer: the replayed inventory against itself is VERIFIED for
// its workflow files, and a one-byte edit of one file is listed once with a
// diff while the others stay VERIFIED.
func TestDerivedInventoryIdenticalAndEdited(t *testing.T) {
	c := gh.New(gh.Options{NoCache: true, HTTP: &http.Client{Transport: ghtest.Replay("../ghtest/testdata/tackle")}})
	a, err := gates.Derive(context.Background(), c, "schuettc/tackle", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Sources) < 2 {
		t.Fatal("fixture needs two workflow files")
	}
	b, err := gates.Derive(context.Background(), c, "schuettc/tackle", "")
	if err != nil {
		t.Fatal(err)
	}
	if ids := workflowProofIDs(GateChanges(a, b)); len(ids) != 0 {
		t.Fatalf("identical derived workflows unverified: %v", ids)
	}
	b.Sources[0].SHA256 = rawHash("one byte")
	d := GateChanges(a, b)
	if ids := workflowProofIDs(d); len(ids) != 1 || workflowEntries(d, b.Sources[0].Path) != 1 {
		t.Fatalf("edit of one file: %v", ids)
	}
	if w := diffFor(t, d, b.Sources[0].Path); w.Structure != StructureRawOnly {
		t.Fatalf("%+v", w)
	}
}

// The report carries the diff in JSON and Markdown.
func TestWorkflowDiffRenders(t *testing.T) {
	a := inventory(`{}`)
	a.Sources = []gates.WorkflowSource{ciSource(ciRawPush), deployWorkflow()}
	edited := deployWorkflow()
	edited.Jobs[0].Steps[0].Run = "go test ./...."
	edited.SHA256 = rawHash("deploy workflow.")
	b := inventory(`{}`)
	b.Sources = []gates.WorkflowSource{ciSource(ciRawPR), edited}
	r := &Result{Gates: GateChanges(a, b)}
	var j bytes.Buffer
	if err := WriteJSON(&j, r); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"workflow_diffs"`, `"where": "jobs.test.steps[0].run"`, `"old": "go test ./..."`, `"new": "go test ./...."`, `"structure": "raw-only"`} {
		if !strings.Contains(j.String(), want) {
			t.Errorf("JSON lacks %s:\n%s", want, j.String())
		}
	}
	md := RenderMarkdown(r)
	for _, want := range []string{
		"### Workflow changes",
		"#### `.github/workflows/deploy.yml` (changed)",
		"Proof: `gate:" + record.PolicyWorkflowPrefix + ".github/workflows/deploy.yml@" + edited.SHA256 + "`",
		"- `jobs.test.steps[0].run`: \"go test ./...\" → \"go test ./....\"",
		"#### `.github/workflows/ci.yml` (changed)",
		"raw file changed",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown lacks %q:\n%s", want, md)
		}
	}
	// An absent side renders as absent, never as an empty value.
	if got := renderValue(nil); got != "(absent)" {
		t.Errorf("absent renders %q", got)
	}
	// Identical files add no workflow section.
	if strings.Contains(RenderMarkdown(&Result{Gates: GateChanges(a, a)}), "### Workflow changes") {
		t.Error("identical files rendered a workflow section")
	}
}

// A side that cannot show the file names why identity is unproven.
func TestUnprovenReasons(t *testing.T) {
	a := inventory(`{}`)
	a.Sources = []gates.WorkflowSource{ciSource(ciRawPush)}
	dir := inventory(`{}`)
	dir.Unavailable = []gates.Unavailable{{What: ".github/workflows", Why: "unknown ref"}}
	dup := inventory(`{}`)
	dup.Sources = []gates.WorkflowSource{ciSource(ciRawPush), ciSource(ciRawPush)}
	for want, b := range map[string]*gates.Inventory{"after side's workflow directory was unreadable": dir, "after side lists the path more than once": dup} {
		d := GateChanges(a, b)
		w := diffFor(t, d, ".github/workflows/ci.yml")
		if d.Passed() || w.Change != WorkflowUnproven || !strings.Contains(w.Note, want) || workflowEntries(d, w.Path) != 1 {
			t.Fatalf("%s: %+v", want, w)
		}
	}
}

// Equal recorded hashes with one side not parsed (parsed on one side,
// unparseable on the other) are not a change: the note says the hashes are
// equal and the structure could not be compared on the side that was not
// parsed. It stays UNVERIFIED, since that side was not parsed. (Unparseable
// on both sides is a policy:availability entry, covered elsewhere.)
func TestEqualHashUnparsedSideIsNotChanged(t *testing.T) {
	src := ciSource(ciRawPush)
	broken := gates.Unavailable{What: src.Path, Why: "unparseable workflow: bad", SHA256: src.SHA256}
	parsed := inventory(`{}`)
	parsed.Sources = []gates.WorkflowSource{src}
	unparsed := inventory(`{}`)
	unparsed.Unavailable = []gates.Unavailable{broken}
	for name, c := range map[string]struct {
		a, b *gates.Inventory
		want string
	}{
		"after unparsed":  {parsed, unparsed, "structure could not be compared: the after side was not parsed"},
		"before unparsed": {unparsed, parsed, "structure could not be compared: the before side was not parsed"},
	} {
		d := GateChanges(c.a, c.b)
		w := diffFor(t, d, src.Path)
		if w.Change == WorkflowChanged || strings.Contains(w.Note, "raw file changed") || strings.Contains(w.Note, " changed") {
			t.Errorf("%s: equal hashes reported as a change: %+v", name, w)
		}
		if w.Change != WorkflowSameHash || w.Structure != StructureUnparsed || !strings.Contains(w.Note, "recorded SHA-256 is equal on both sides") || !strings.Contains(w.Note, c.want) {
			t.Errorf("%s: %+v", name, w)
		}
		if d.Passed() || workflowEntries(d, src.Path) != 1 {
			t.Errorf("%s: %+v", name, d)
		}
		for _, g := range d.Unverified {
			if strings.HasPrefix(g.ID, record.PolicyWorkflowPrefix) && strings.Contains(g.Detail, "changed") {
				t.Errorf("%s: gate detail claims a change: %s", name, g.Detail)
			}
		}
	}
}
