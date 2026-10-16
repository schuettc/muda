package compare

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/subaud-tools/muda/internal/gates"
	"github.com/subaud-tools/muda/internal/gh"
	"github.com/subaud-tools/muda/internal/ghtest"
	"github.com/subaud-tools/muda/internal/record"
)

// refsClient serves before at ref "main" and after at ref "feature" for
// releasePath, replaying the rest of the tackle fixture.
func refsClient(before, after string) *gh.Client {
	replay := ghtest.Replay("../ghtest/testdata/tackle")
	return gh.New(gh.Options{NoCache: true, HTTP: &http.Client{Transport: editAtRef{editAtRef{replay, "main", releasePath, before}, "feature", releasePath, after}}})
}

func change(where, old, new string) FieldChange {
	return FieldChange{Where: where, Old: &old, New: &new}
}

// pin replaces the first n "runs-on: ubuntu-latest" lines with label.
func pin(s, label string, n int) string {
	return strings.Replace(s, "runs-on: ubuntu-latest", "runs-on: "+label, n)
}

// A workflow edit that only swaps GitHub-hosted runner labels at
// jobs.<id>.runs-on is VERIFIED through --gates-base, listed as
// runner-label-only with each runs-on change and its evidence kept. Any other
// change, or any value that is not a plain hosted label, stays UNVERIFIED.
func TestRunnerLabelOnlyWorkflowEditIsVerified(t *testing.T) {
	r := strings.NewReplacer
	withStep := strings.Replace(releaseBase, "      - run: go vet ./...\n", "      - uses: some/action@v1\n        with:\n          runs-on: ubuntu-latest\n", 1)
	reusable := releaseBase + "  call:\n    uses: ./.github/workflows/build.yml\n    with:\n      runs-on: ubuntu-latest\n"
	dotted := "jobs.test.runs-on: ubuntu-latest\n" + releaseBase
	noRunsOn := strings.Replace(releaseBase, "  lint:\n    runs-on: ubuntu-latest\n", "  lint:\n", 1)
	for name, c := range map[string]struct {
		before, after string
		fields        []FieldChange // nil: UNVERIFIED with today's changed result
	}{
		"one job pinned":     {releaseBase, pin(releaseBase, "ubuntu-24.04", 1), []FieldChange{change("jobs.test.runs-on", "ubuntu-latest", "ubuntu-24.04")}},
		"two jobs pinned":    {releaseBase, pin(releaseBase, "ubuntu-24.04", 2), []FieldChange{change("jobs.test.runs-on", "ubuntu-latest", "ubuntu-24.04"), change("jobs.lint.runs-on", "ubuntu-latest", "ubuntu-24.04")}},
		"pin and comment":    {releaseBase, r("ship after the tests", "deploy once tests pass").Replace(pin(releaseBase, "ubuntu-24.04", 1)), []FieldChange{change("jobs.test.runs-on", "ubuntu-latest", "ubuntu-24.04")}},
		"arm label":          {releaseBase, pin(releaseBase, "ubuntu-24.04-arm", 1), []FieldChange{change("jobs.test.runs-on", "ubuntu-latest", "ubuntu-24.04-arm")}},
		"macos and windows":  {pin(releaseBase, "macos-15", 1), pin(releaseBase, "windows-2025", 1), []FieldChange{change("jobs.test.runs-on", "macos-15", "windows-2025")}},
		"to self-hosted":     {releaseBase, pin(releaseBase, "self-hosted", 1), nil},
		"to a list":          {releaseBase, pin(releaseBase, "[self-hosted, linux]", 1), nil},
		"to an expression":   {releaseBase, pin(releaseBase, "${{ matrix.os }}", 1), nil},
		"to a group":         {releaseBase, pin(releaseBase, "\n      group: large-runners", 1), nil},
		"to a quoted label":  {releaseBase, pin(releaseBase, `"ubuntu-24.04"`, 1), nil},
		"to another string":  {releaseBase, pin(releaseBase, "ubuntu-big", 1), nil},
		"pin and if":         {releaseBase, r("refs/heads/main", "refs/heads/dev").Replace(pin(releaseBase, "ubuntu-24.04", 1)), nil},
		"pin and needs":      {releaseBase, r("needs: test", "needs: lint").Replace(pin(releaseBase, "ubuntu-24.04", 1)), nil},
		"pin and run":        {releaseBase, r("./deploy.sh\n", "./deploy.sh --prod\n").Replace(pin(releaseBase, "ubuntu-24.04", 1)), nil},
		"pin and new key":    {releaseBase, r("    needs: test\n", "    needs: test\n    timeout-minutes: 10\n").Replace(pin(releaseBase, "ubuntu-24.04", 1)), nil},
		"runs-on added":      {noRunsOn, releaseBase, nil},
		"step with runs-on":  {withStep, pin(withStep, "ubuntu-24.04", 3), nil},
		"reusable runs-on":   {reusable, strings.Replace(reusable, "      runs-on: ubuntu-latest\n", "      runs-on: ubuntu-24.04\n", 1), nil},
		"top-level dot key":  {dotted, pin(dotted, "ubuntu-24.04", 1), nil},
		"pin behind anchors": {r("runs-on: ubuntu-latest\n    steps:\n      - run: go test", "runs-on: &os ubuntu-latest\n    steps:\n      - run: go test").Replace(releaseBase), r("runs-on: ubuntu-latest\n    steps:\n      - run: go test", "runs-on: &os ubuntu-24.04\n    steps:\n      - run: go test").Replace(releaseBase), nil},
	} {
		d := verifyGatesBase(context.Background(), refsClient(c.before, c.after), "schuettc/tackle", "main", "feature", "")
		w := diffFor(t, d, releasePath)
		if c.fields == nil {
			if d.Passed() || d.Status != record.GateUnverified || workflowEntries(d, releasePath) != 1 || w.Change != WorkflowChanged {
				t.Errorf("%s: want UNVERIFIED changed: %+v %+v", name, d, w)
			}
			continue
		}
		id := record.PolicyWorkflowPrefix + releasePath + "@" + rawHash(c.after)
		if d.Status != record.GateVerified || !d.Passed() || workflowEntries(d, releasePath) != 0 {
			t.Errorf("%s: runner-label-only edit not VERIFIED: %+v", name, d)
		}
		if w.Change != WorkflowRunnerLabelOnly || w.ProofID != id || w.Structure != StructureCompared || !strings.Contains(w.Note, "runs-on") {
			t.Errorf("%s: %+v", name, w)
		}
		if !slices.EqualFunc(w.Fields, c.fields, func(x, y FieldChange) bool {
			return x.Where == y.Where && x.Old != nil && y.Old != nil && *x.Old == *y.Old && x.New != nil && y.New != nil && *x.New == *y.New
		}) {
			t.Errorf("%s: fields %s", name, mustJSON(w.Fields))
		}
		cited := false
		for _, e := range d.Evidence {
			cited = cited || (e.Path == releasePath && e.Note == "after workflow file")
		}
		if !cited {
			t.Errorf("%s: evidence dropped: %+v", name, d.Evidence)
		}
	}
}

// A recorded baseline carries no workflow bytes, so the same label-only edit
// compared against it stays UNVERIFIED as before.
func TestRunnerLabelOnlyNeedsFreshSides(t *testing.T) {
	c := refsClient(releaseBase, pin(releaseBase, "ubuntu-24.04", 1))
	inv, err := gates.Derive(context.Background(), c, "schuettc/tackle", "main")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	s := &record.Store{Root: root}
	if err := s.Init(record.Settings{Roles: map[string]string{"verify.yml": "verify"}, Stacks: []string{"go"}, Window: record.Window{Since: "2026-09-01", Until: "2026-09-15"}, MudaVersion: "dev"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetGates(inv); err != nil {
		t.Fatal(err)
	}
	d := verifyGates(context.Background(), c, "schuettc/tackle", filepath.Join(root, ".muda", "gates.toml"), "feature")
	if d.Passed() || d.Status != record.GateUnverified || workflowEntries(d, releasePath) != 1 || diffFor(t, d, releasePath).Change != WorkflowChanged {
		t.Errorf("recorded baseline: %+v", d)
	}
}

// The bytes a fresh derive keeps for the tree comparison never reach JSON:
// each workflow source serializes exactly the fields it did before.
func TestWorkflowBytesAreNotSerialized(t *testing.T) {
	c := refsClient(releaseBase, releaseBase)
	inv, err := gates.Derive(context.Background(), c, "schuettc/tackle", "main")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(inv)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), base64.StdEncoding.EncodeToString([]byte(releaseBase))) {
		t.Fatal("workflow bytes serialized")
	}
	var doc struct {
		Workflows []map[string]json.RawMessage `json:"workflows"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	want := []string{"comment_free_sha256", "defaults", "env", "jobs", "name", "path", "sha256", "url"}
	if len(doc.Workflows) == 0 {
		t.Fatal("no workflows")
	}
	for _, w := range doc.Workflows {
		got := sortedKeys(w)
		if !slices.Equal(got, want) {
			t.Errorf("workflow source keys %v; want %v", got, want)
		}
	}
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

// #39 review: an anchor anywhere lets a runs-on change reach another place
// (here a matrix value a step reads), and a label suffix other than -arm can
// name a self-hosted runner. Both stay UNVERIFIED.
func TestRunnerLabelRuleRefusesAnchorsAndUnknownSuffixes(t *testing.T) {
	wf := func(label, extra string) []byte {
		return []byte("on: push\njobs:\n  test: &test\n    runs-on: " + label + "\n    steps:\n      - run: echo test\n  deploy:\n    runs-on: ubuntu-24.04\n    strategy:\n      matrix:\n        config: [*test]\n    steps:\n      - run: ./deploy.sh '${{ matrix.config.runs-on }}'\n" + extra)
	}
	if _, ok := runnerLabelChanges(wf("ubuntu-latest", ""), wf("ubuntu-24.04", "")); ok {
		t.Error("a runs-on change reachable through an alias passed as runner-label-only")
	}
	plain := func(label string) []byte {
		return []byte("on: push\njobs:\n  test:\n    runs-on: " + label + "\n    steps:\n      - run: echo test\n")
	}
	for _, label := range []string{"ubuntu-24.04-private", "ubuntu-24.04-gpu", "windows-2025-large"} {
		if _, ok := runnerLabelChanges(plain("ubuntu-latest"), plain(label)); ok {
			t.Errorf("ubuntu-latest -> %s passed as runner-label-only", label)
		}
	}
	if _, ok := runnerLabelChanges(plain("ubuntu-latest"), plain("ubuntu-24.04-arm")); !ok {
		t.Error("ubuntu-latest -> ubuntu-24.04-arm should pass")
	}
}
