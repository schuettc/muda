package compare

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schuettc/muda/internal/gates"
	"github.com/schuettc/muda/internal/gh"
	"github.com/schuettc/muda/internal/ghtest"
	"github.com/schuettc/muda/internal/record"
)

const releasePath = ".github/workflows/release.yml"

const releaseBase = `name: release
on: push
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - run: go test ./...
  lint:
    runs-on: ubuntu-latest
    steps:
      - run: go vet ./...
  deploy:
    # ship after the tests
    needs: test
    if: github.ref == 'refs/heads/main'
    runs-on: ubuntu-latest
    steps:
      - run: |
          ./deploy.sh
          echo done
`

// releaseClient serves releaseBase at ref "main" and after at ref "feature"
// for releasePath, replaying the rest of the tackle fixture.
func releaseClient(after string) *gh.Client {
	replay := ghtest.Replay("../ghtest/testdata/tackle")
	return gh.New(gh.Options{NoCache: true, HTTP: &http.Client{Transport: editAtRef{editAtRef{replay, "main", releasePath, releaseBase}, "feature", releasePath, after}}})
}

// A comment-only edit is VERIFIED: no unknown entry, a comments-only diff
// bound to the after-side raw hash, and the file's evidence kept. Any change
// to what the file says stays UNVERIFIED.
func TestCommentOnlyWorkflowEditIsVerified(t *testing.T) {
	r := strings.NewReplacer
	for name, c := range map[string]struct {
		after    string
		verified bool
	}{
		"comment added":        {r("runs-on: ubuntu-latest\n    steps:\n      - run: go vet", "runs-on: ubuntu-latest # pinned\n    steps:\n      - run: go vet").Replace(releaseBase), true},
		"comment line removed": {r("    # ship after the tests\n", "").Replace(releaseBase), true},
		"comment changed":      {"# release workflow\n" + r("ship after the tests", "deploy once tests pass").Replace(releaseBase), true},
		"if changed":           {r("refs/heads/main", "refs/heads/dev").Replace(releaseBase), false},
		"needs changed":        {r("needs: test", "needs: lint").Replace(releaseBase), false},
		"run changed":          {r("./deploy.sh\n", "./deploy.sh --prod\n").Replace(releaseBase), false},
		"# inside run":         {r("echo done\n", "echo done\n          # not a comment\n").Replace(releaseBase), false},
		"key order changed":    {r("    needs: test\n    if: github.ref == 'refs/heads/main'\n", "    if: github.ref == 'refs/heads/main'\n    needs: test\n").Replace(releaseBase), false},
		"key added":            {r("    needs: test\n", "    needs: test\n    timeout-minutes: 10\n").Replace(releaseBase), false},
	} {
		d := verifyGatesBase(context.Background(), releaseClient(c.after), "schuettc/tackle", "main", "feature", "")
		if c.verified {
			w := diffFor(t, d, releasePath)
			id := record.PolicyWorkflowPrefix + releasePath + "@" + rawHash(c.after)
			if d.Status != record.GateVerified || !d.Passed() || workflowEntries(d, releasePath) != 0 {
				t.Errorf("%s: comment-only edit not VERIFIED: %+v", name, d)
			}
			if w.Change != WorkflowCommentsOnly || w.ProofID != id || !strings.Contains(w.Note, "comments") {
				t.Errorf("%s: %+v", name, w)
			}
			cited := false
			for _, e := range d.Evidence {
				cited = cited || (e.Path == releasePath && e.Note == "after workflow file")
			}
			if !cited {
				t.Errorf("%s: evidence dropped: %+v", name, d.Evidence)
			}
			continue
		}
		if d.Passed() || d.Status != record.GateUnverified || workflowEntries(d, releasePath) != 1 {
			t.Errorf("%s: %+v", name, d)
		}
		if w := diffFor(t, d, releasePath); w.Change != WorkflowChanged {
			t.Errorf("%s: %+v", name, w)
		}
	}
}

// A side without a comment-free hash (a record made before it existed)
// falls back to the raw hash: a comment-only edit stays UNVERIFIED.
func TestCommentOnlyNeedsBothCommentFreeHashes(t *testing.T) {
	c := releaseClient("# a comment\n" + releaseBase)
	for _, side := range []string{"before", "after"} {
		a, err := gates.Derive(context.Background(), c, "schuettc/tackle", "main")
		if err != nil {
			t.Fatal(err)
		}
		b, err := gates.Derive(context.Background(), c, "schuettc/tackle", "feature")
		if err != nil {
			t.Fatal(err)
		}
		inv := map[string]*gates.Inventory{"before": a, "after": b}[side]
		for i := range inv.Sources {
			if inv.Sources[i].Path == releasePath {
				inv.Sources[i].CommentFreeSHA256 = ""
			}
		}
		d := GateChanges(a, b)
		if d.Passed() || workflowEntries(d, releasePath) != 1 || diffFor(t, d, releasePath).Change != WorkflowChanged {
			t.Errorf("%s side lacks a comment-free hash: %+v", side, d)
		}
	}
}

// An inventory recorded before the comment-free hash existed still loads and
// round-trips byte for byte, and compares as before: a comment-only edit is
// UNVERIFIED. A record that carries the hash compares it.
func TestRecordedInventoryWithoutCommentFreeHash(t *testing.T) {
	after := "# a comment\n" + releaseBase
	c := releaseClient(after)
	for _, legacy := range []bool{true, false} {
		inv, err := gates.Derive(context.Background(), c, "schuettc/tackle", "main")
		if err != nil {
			t.Fatal(err)
		}
		if legacy {
			for i := range inv.Sources {
				inv.Sources[i].CommentFreeSHA256 = ""
			}
		}
		root := t.TempDir()
		s := &record.Store{Root: root}
		if err := s.Init(record.Settings{Roles: map[string]string{"verify.yml": "verify"}, Stacks: []string{"go"}, Window: record.Window{Since: "2026-09-01", Until: "2026-09-15"}, MudaVersion: "dev"}); err != nil {
			t.Fatal(err)
		}
		if err := s.SetGates(inv); err != nil {
			t.Fatal(err)
		}
		raw, err := s.GatesJSON()
		if err != nil {
			t.Fatal(err)
		}
		if legacy == strings.Contains(string(raw), "comment_free_sha256") {
			t.Fatalf("legacy %v: recorded inventory %s", legacy, raw)
		}
		var loaded gates.Inventory
		if err := json.Unmarshal(raw, &loaded); err != nil {
			t.Fatal(err)
		}
		projected, err := json.Marshal(&loaded)
		if err != nil {
			t.Fatal(err)
		}
		if !rawInventoryEqual(raw, projected) {
			t.Fatalf("legacy %v: recorded inventory does not round-trip", legacy)
		}
		d := verifyGates(context.Background(), c, "schuettc/tackle", filepath.Join(root, ".muda", "gates.toml"), "feature")
		for _, g := range d.Unverified {
			if g.ID == record.PolicyInventoryRaw {
				t.Fatalf("legacy %v: raw round-trip failed: %+v", legacy, g)
			}
		}
		w := diffFor(t, d, releasePath)
		if legacy && (d.Passed() || workflowEntries(d, releasePath) != 1 || w.Change != WorkflowChanged) {
			t.Errorf("legacy record: %+v", d)
		}
		if !legacy && (!d.Passed() || d.Status != record.GateVerified || w.Change != WorkflowCommentsOnly) {
			t.Errorf("current record: %+v", d)
		}
	}
}
