package gates

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/schuettc/muda/internal/gh"
	"github.com/schuettc/muda/internal/ghtest"
)

const tackleFixtures = "../ghtest/testdata/tackle"

// recording notes every request path+query before replaying it.
type recording struct {
	mu   sync.Mutex
	seen []string
	next http.RoundTripper
}

func (r *recording) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	r.seen = append(r.seen, req.URL.RequestURI())
	r.mu.Unlock()
	return r.next.RoundTrip(req)
}

// overlay tries each replay directory in order; the first recorded answer wins.
type overlay []http.RoundTripper

func (o overlay) RoundTrip(req *http.Request) (*http.Response, error) {
	var last error
	for _, rt := range o {
		resp, err := rt.RoundTrip(req)
		if err == nil {
			return resp, nil
		}
		last = err
	}
	return nil, last
}

func replayClient(rt http.RoundTripper) *gh.Client {
	return gh.New(gh.Options{NoCache: true, HTTP: &http.Client{Transport: rt}})
}

func TestDeriveRefReadsWorkflowsAtRef(t *testing.T) {
	rec := &recording{next: ghtest.Replay(tackleFixtures)}
	inv, err := Derive(context.Background(), replayClient(rec), "schuettc/tackle", "feature")
	if err != nil {
		t.Fatal(err)
	}
	if inv.Ref != "feature" || inv.Branch != "main" {
		t.Fatalf("ref=%q branch=%q", inv.Ref, inv.Branch)
	}
	settings := map[string]bool{}
	contents := 0
	for _, uri := range rec.seen {
		switch {
		case strings.Contains(uri, "/contents/"):
			contents++
			if !strings.HasSuffix(uri, "?ref=feature") {
				t.Errorf("workflow read not at ref: %s", uri)
			}
		case strings.Contains(uri, "/branches/main/protection"):
			settings["protection"] = true
		case strings.Contains(uri, "/rules/branches/main"):
			settings["rulesets"] = true
		case strings.Contains(uri, "/environments"):
			settings["environments"] = true
		}
	}
	if contents != 3 || len(settings) != 3 {
		t.Fatalf("requests: %v", rec.seen)
	}
	if len(inv.Sources) != 2 || len(inv.Unavailable) != 0 {
		t.Fatalf("%+v", inv)
	}
	for _, s := range inv.Sources {
		if !strings.Contains(s.URL, "/blob/feature/") {
			t.Errorf("workflow evidence not at ref: %s", s.URL)
		}
	}
	for _, g := range inv.Gates {
		if !strings.Contains(g.ID, ":main:") {
			t.Errorf("gate identity must stay on the merge branch: %s", g.ID)
		}
	}

	def, err := Derive(context.Background(), replayClient(ghtest.Replay(tackleFixtures)), "schuettc/tackle", "")
	if err != nil {
		t.Fatal(err)
	}
	if def.Ref != "main" {
		t.Fatalf("default ref = %q, want the default branch", def.Ref)
	}
}

func fixtureFileBytes(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(tackleFixtures, ghtest.FixtureName("GET", "/repos/schuettc/tackle/contents/"+name+"?ref=main")))
	if err != nil {
		t.Fatal(err)
	}
	var item struct{ Content string }
	if err := json.Unmarshal(b, &item); err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(item.Content, "\n", ""))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func hexSHA(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func TestDeriveRecordsRawHash(t *testing.T) {
	inv, err := Derive(context.Background(), replayClient(ghtest.Replay(tackleFixtures)), "schuettc/tackle", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Sources) != 2 {
		t.Fatalf("%+v", inv.Sources)
	}
	for _, s := range inv.Sources {
		want := hexSHA(fixtureFileBytes(t, s.Path))
		if s.SHA256 != want {
			t.Errorf("%s sha256 = %q, want %q", s.Path, s.SHA256, want)
		}
	}
}

// writeFixture records one synthetic response in dir under its replay name.
func writeFixture(t *testing.T, dir, pathQuery string, status int, body string) {
	t.Helper()
	name := ghtest.FixtureName("GET", pathQuery)
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if status != 200 {
		meta := fmt.Sprintf(`{"status":%d,"headers":{}}`, status)
		if err := os.WriteFile(filepath.Join(dir, strings.TrimSuffix(name, ".json")+".meta.json"), []byte(meta), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDeriveUnparseableCarriesHash(t *testing.T) {
	broken := []byte("name: Broken\non: push\njobs: [\n")
	dir := t.TempDir()
	writeFixture(t, dir, "/repos/schuettc/tackle/contents/.github/workflows?ref=main", 200,
		`[{"type":"file","path":".github/workflows/broken.yml"},{"type":"file","path":".github/workflows/gone.yml"}]`)
	writeFixture(t, dir, "/repos/schuettc/tackle/contents/.github/workflows/broken.yml?ref=main", 200,
		fmt.Sprintf(`{"type":"file","encoding":"base64","content":%q}`, base64.StdEncoding.EncodeToString(broken)))
	writeFixture(t, dir, "/repos/schuettc/tackle/contents/.github/workflows/gone.yml?ref=main", 404, `{"message":"Not Found","status":"404"}`)
	inv, err := Derive(context.Background(), replayClient(overlay{ghtest.Replay(dir), ghtest.Replay(tackleFixtures)}), "schuettc/tackle", "")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Unavailable{}
	for _, u := range inv.Unavailable {
		got[u.What] = u
	}
	b, ok := got[".github/workflows/broken.yml"]
	if !ok || !strings.HasPrefix(b.Why, "unparseable workflow") || b.SHA256 != hexSHA(broken) {
		t.Fatalf("unparseable entry %+v, want sha256 %s", b, hexSHA(broken))
	}
	g, ok := got[".github/workflows/gone.yml"]
	if !ok || g.SHA256 != "" {
		t.Fatalf("missing file entry %+v must carry no hash", g)
	}
	raw, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "sha256") {
		t.Fatalf("missing file serialises a hash: %s", raw)
	}
}

func TestDeriveUnknownRefIsNamed(t *testing.T) {
	inv, err := Derive(context.Background(), replayClient(ghtest.Replay(tackleFixtures)), "schuettc/tackle", "no-such-ref")
	if err != nil {
		t.Fatal(err)
	}
	if inv.Ref != "no-such-ref" || len(inv.Sources) != 0 {
		t.Fatalf("ref=%q sources=%d", inv.Ref, len(inv.Sources))
	}
	found := false
	for _, u := range inv.Unavailable {
		if u.What == ".github/workflows" && strings.Contains(u.Why, "no-such-ref") {
			found = true
		}
	}
	if !found {
		t.Fatalf("unknown ref not named: %+v", inv.Unavailable)
	}
}

// Without --ref, an unreadable workflow directory has the same name, and its
// Why names the default branch it was read at.
func TestDeriveDefaultBranchWorkflowDirIsNamed(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "/repos/schuettc/tackle/contents/.github/workflows?ref=main", 404, `{"message":"Not Found","status":"404"}`)
	inv, err := Derive(context.Background(), replayClient(overlay{ghtest.Replay(dir), ghtest.Replay(tackleFixtures)}), "schuettc/tackle", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Unavailable) != 1 || inv.Unavailable[0].What != ".github/workflows" || !strings.Contains(inv.Unavailable[0].Why, `"main"`) {
		t.Fatalf("%+v", inv.Unavailable)
	}
}

func TestMarkdownStatesWorkflowRef(t *testing.T) {
	inv, err := Derive(context.Background(), replayClient(ghtest.Replay(tackleFixtures)), "schuettc/tackle", "feature")
	if err != nil {
		t.Fatal(err)
	}
	md := RenderMarkdown(inv)
	for _, want := range []string{"default branch `main`", "workflows at `feature`"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown lacks %q:\n%s", want, strings.SplitN(md, "\n## ", 2)[0])
		}
	}
}

// Refs with '/' or '#' are query-escaped for the API and path-escaped per
// segment in every evidence link, so a link never opens a different ref.
func TestDeriveRefEscaping(t *testing.T) {
	verify, err := os.ReadFile(filepath.Join(tackleFixtures, ghtest.FixtureName("GET", "/repos/schuettc/tackle/contents/.github/workflows/verify.yml?ref=main")))
	if err != nil {
		t.Fatal(err)
	}
	deployYAML := "name: Deploy\non: push\njobs:\n  test:\n    steps:\n      - run: go test ./...\n  deploy:\n    needs: test\n    environment: prod\n"
	deploy := fmt.Sprintf(`{"type":"file","encoding":"base64","content":%q}`, base64.StdEncoding.EncodeToString([]byte(deployYAML)))
	for _, tc := range []struct{ ref, query, blob string }{
		{"feature/x", "feature%2Fx", "https://github.com/schuettc/tackle/blob/feature/x/.github/workflows/"},
		{"feature#12", "feature%2312", "https://github.com/schuettc/tackle/blob/feature%2312/.github/workflows/"},
	} {
		t.Run(tc.ref, func(t *testing.T) {
			dir := t.TempDir()
			base := "/repos/schuettc/tackle/contents/.github/workflows"
			writeFixture(t, dir, base+"?ref="+tc.query, 200, `[{"type":"file","path":".github/workflows/deploy.yml"},{"type":"file","path":".github/workflows/verify.yml"}]`)
			writeFixture(t, dir, base+"/deploy.yml?ref="+tc.query, 200, deploy)
			writeFixture(t, dir, base+"/verify.yml?ref="+tc.query, 200, string(verify))
			rec := &recording{next: overlay{ghtest.Replay(dir), ghtest.Replay(tackleFixtures)}}
			inv, err := Derive(context.Background(), replayClient(rec), "schuettc/tackle", tc.ref)
			if err != nil {
				t.Fatal(err)
			}
			if inv.Ref != tc.ref || len(inv.Sources) != 2 || len(inv.Unavailable) != 0 {
				t.Fatalf("ref=%q sources=%d unavailable=%+v", inv.Ref, len(inv.Sources), inv.Unavailable)
			}
			contents := 0
			for _, uri := range rec.seen {
				if strings.Contains(uri, "/contents/") {
					contents++
					if !strings.HasSuffix(uri, "?ref="+tc.query) {
						t.Errorf("API query not escaped: %s", uri)
					}
				}
			}
			if contents != 3 {
				t.Fatalf("contents requests: %v", rec.seen)
			}
			links := []string{}
			for _, s := range inv.Sources {
				if s.URL != tc.blob+filepath.Base(s.Path) {
					t.Errorf("file link %s, want %s%s", s.URL, tc.blob, filepath.Base(s.Path))
				}
				for _, j := range s.Jobs {
					links = append(links, j.URL)
					for _, st := range j.Steps {
						links = append(links, st.URL)
					}
				}
			}
			needs := 0
			for _, g := range inv.Gates {
				if g.Kind == "needs" {
					needs++
					links = append(links, g.URL)
				}
			}
			if needs != 1 {
				t.Fatalf("needs gates: %+v", inv.Gates)
			}
			for _, link := range links {
				if !strings.HasPrefix(link, tc.blob) || strings.Count(link, "#") != 1 || !strings.Contains(link, ".yml#L") {
					t.Errorf("evidence link %s does not point at ref %q", link, tc.ref)
				}
			}
		})
	}
}

// Final re-review N-2: a workflow path that is a symlink or a submodule is
// named unavailable, with no hash, never dropped (which would read as removed)
// and never read through to the symlink's target.
func TestDeriveNonFileWorkflowEntriesAreUnavailable(t *testing.T) {
	link, sub := ".github/workflows/release.yml", ".github/workflows/vendored.yml"
	rt := ghtest.NonFileWorkflows(ghtest.Replay(tackleFixtures), []string{link}, []string{sub})
	inv, err := Derive(context.Background(), replayClient(rt), "schuettc/tackle", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range inv.Sources {
		if s.Path == link || s.Path == sub {
			t.Fatalf("non-file entry %s read as a workflow source", s.Path)
		}
	}
	got := map[string]Unavailable{}
	for _, u := range inv.Unavailable {
		got[u.What] = u
	}
	for path, kind := range map[string]string{link: "symlink", sub: "submodule"} {
		u, ok := got[path]
		if !ok || !strings.Contains(u.Why, kind) || u.SHA256 != "" {
			t.Fatalf("%s entry %+v; want unavailable naming %q with no hash (all: %+v)", path, u, kind, inv.Unavailable)
		}
	}
}
