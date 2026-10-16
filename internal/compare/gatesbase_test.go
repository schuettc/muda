package compare

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/subaud-tools/muda/internal/gates"
	"github.com/subaud-tools/muda/internal/gh"
	"github.com/subaud-tools/muda/internal/ghtest"
	"github.com/subaud-tools/muda/internal/record"
)

// editAtRef serves raw as the content of one workflow path at one ref, and
// replays everything else: a pull request that changed that file.
type editAtRef struct {
	base           http.RoundTripper
	ref, path, raw string
}

func (e editAtRef) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.HasSuffix(req.URL.Path, "/contents/"+e.path) && req.URL.Query().Get("ref") == e.ref {
		body := fmt.Sprintf(`{"type":"file","encoding":"base64","path":%q,"content":%q}`, e.path, base64.StdEncoding.EncodeToString([]byte(e.raw)))
		return &http.Response{StatusCode: 200, Status: "200 OK", Header: http.Header{}, Body: io.NopCloser(bytes.NewReader([]byte(body))), Request: req}, nil
	}
	return e.base.RoundTrip(req)
}

// Issue #7: with --gates-base the before side is derived at the merge base,
// so a workflow file changed on the base branch since the baseline (identical
// at the merge base and the PR head) is VERIFIED, while the file the PR
// changed stays UNVERIFIED and the record's exemptions still apply. The
// recorded inventory, compared as before, still shows both.
func TestGatesBaseComparesWithMergeBase(t *testing.T) {
	const changed, baseOnly = ".github/workflows/release.yml", ".github/workflows/verify.yml"
	edited := "name: release\non: push\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: go test ./...\n"
	c := gh.New(gh.Options{NoCache: true, HTTP: &http.Client{Transport: editAtRef{ghtest.Replay("../ghtest/testdata/tackle"), "feature", changed, edited}}})
	recorded, err := gates.Derive(context.Background(), c, "schuettc/tackle", "")
	if err != nil {
		t.Fatal(err)
	}
	// The recorded baseline predates a base-branch change to baseOnly.
	found := false
	for i := range recorded.Sources {
		if recorded.Sources[i].Path == baseOnly {
			recorded.Sources[i].SHA256 = rawHash("before a merged pull request")
			found = true
		}
	}
	if !found {
		t.Fatalf("fixture lacks %s", baseOnly)
	}
	root := t.TempDir()
	s := &record.Store{Root: root}
	if err := s.Init(record.Settings{Roles: map[string]string{"verify.yml": "verify"}, Stacks: []string{"go"}, Window: record.Window{Since: "2026-09-01", Until: "2026-09-15"}, MudaVersion: "dev"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetGates(recorded); err != nil {
		t.Fatal(err)
	}
	paths := func(d *GateDiff) []string {
		out := []string{}
		for _, w := range d.WorkflowDiffs {
			out = append(out, w.Path)
		}
		return out
	}
	old := verifyGates(context.Background(), c, "schuettc/tackle", filepath.Join(root, ".muda", "gates.toml"), "feature")
	if got := paths(old); !slices.Contains(got, changed) || !slices.Contains(got, baseOnly) {
		t.Fatalf("recorded before side should show both files: %v", got)
	}
	d := verifyGatesBase(context.Background(), c, "schuettc/tackle", "main", "feature", root)
	if got := paths(d); !slices.Equal(got, []string{changed}) {
		t.Fatalf("merge-base before side: unverified files %v; want only %s", got, changed)
	}
	if d.Status != record.GateUnverified || d.BeforeRef != "main" || d.BeforeSettings != "" || d.BeforeRepo != "" {
		t.Fatalf("%+v", d)
	}
	for _, g := range d.Unverified {
		if g.ID == record.PolicyInventoryRaw {
			t.Fatalf("raw round-trip applied to a fresh derive: %+v", g)
		}
	}
	id := record.PolicyWorkflowPrefix + changed + "@" + rawHash(edited)
	today := time.Now().UTC()
	if err := s.Exempt(record.Exemption{ID: "E-fix", Waste: "W6", Location: "gate:" + id, Reason: "the fix", AgreedBy: "owner", Date: today.Format("2006-01-02"), ReviewBy: today.AddDate(0, 0, 7).Format("2006-01-02")}); err != nil {
		t.Fatal(err)
	}
	d = verifyGatesBase(context.Background(), c, "schuettc/tackle", "main", "feature", root)
	if !d.Passed() || d.Status != record.GateExempted || !slices.Equal(d.Exempted, []string{id}) {
		t.Fatalf("record exemption did not apply: %+v", d)
	}
	// Without a record there are no exemptions.
	if d = verifyGatesBase(context.Background(), c, "schuettc/tackle", "main", "feature", ""); d.Passed() || len(d.Exempted) != 0 {
		t.Fatalf("exemptions without a record: %+v", d)
	}
	// Identical sides are VERIFIED.
	if d = verifyGatesBase(context.Background(), c, "schuettc/tackle", "main", "main", ""); d.Status != record.GateVerified {
		t.Fatalf("identical refs: %+v", d)
	}
}
