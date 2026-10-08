package compare

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/schuettc/muda/internal/gates"
	"github.com/schuettc/muda/internal/gh"
	"github.com/schuettc/muda/internal/ghtest"
)

// renamed answers GitHub's repository endpoint for old names: a rename
// redirects to the current repository, another name is a different repository,
// and anything else is not found.
type renamed struct{ base http.RoundTripper }

func (r renamed) RoundTrip(req *http.Request) (*http.Response, error) {
	reply := func(status int, h http.Header, body string) *http.Response {
		return &http.Response{StatusCode: status, Status: http.StatusText(status), Header: h, Body: io.NopCloser(strings.NewReader(body)), Request: req}
	}
	switch req.URL.Path {
	case "/repos/schuettc/old-tackle":
		u := *req.URL
		u.Path = "/repos/schuettc/tackle"
		return reply(http.StatusMovedPermanently, http.Header{"Location": {u.String()}}, ""), nil
	case "/repos/schuettc/other":
		return reply(http.StatusOK, http.Header{}, `{"full_name":"schuettc/other"}`), nil
	case "/repos/schuettc/gone":
		return reply(http.StatusNotFound, http.Header{}, `{"message":"Not Found"}`), nil
	}
	return r.base.RoundTrip(req)
}

// A baseline recorded before GitHub renamed the repository is the same
// repository: its gates and scan compare as recorded, and the report says
// which name the record carries. A name that is not redirected here fails.
func TestRenamedRepositoryCompares(t *testing.T) {
	c := gh.New(gh.Options{NoCache: true, HTTP: &http.Client{Transport: renamed{ghtest.Replay("../ghtest/testdata/tackle")}}})
	inv, err := gates.Derive(context.Background(), c, "schuettc/tackle", "")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(inv)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte("/schuettc/tackle/")) {
		t.Fatal("fixture inventory carries no repository URLs; the test would prove nothing")
	}
	write := func(old string) string {
		t.Helper()
		b := bytes.ReplaceAll(raw, []byte("schuettc/tackle"), []byte(old))
		var doc bytes.Buffer
		if err := toml.NewEncoder(&doc).Encode(map[string]any{"schema": 1, "inventory-json": string(b)}); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "gates.toml")
		if err := os.WriteFile(path, doc.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	if d := verifyGates(context.Background(), c, "schuettc/tackle", write("schuettc/tackle"), ""); !d.Passed() || d.BeforeRepo != "" {
		t.Fatalf("same name: %+v", d)
	}
	d := verifyGates(context.Background(), c, "schuettc/tackle", write("schuettc/old-tackle"), "")
	if !d.Passed() || d.Status != "VERIFIED" || d.BeforeRepo != "schuettc/old-tackle" {
		t.Fatalf("renamed: %+v", d)
	}
	for _, old := range []string{"schuettc/other", "schuettc/gone"} {
		if d := verifyGates(context.Background(), c, "schuettc/tackle", write(old), ""); d.Passed() || d.BeforeRepo != "" {
			t.Fatalf("%s accepted: %+v", old, d)
		}
	}
}

// Only the repository name changes, and only as a whole value or a whole path
// segment; a longer name that starts with it is a different repository.
func TestRenameRepoRewritesOnlyTheName(t *testing.T) {
	in := `{"repo":"o/old","url":"https://api.github.com/repos/o/old/rulesets/1","html":"https://github.com/o/old","q":"https://github.com/o/old?x=1","other":"https://github.com/o/old-two/x","n":12345678901234567890,"note":"o/old & more"}`
	out, err := renameRepo([]byte(in), "o/old", "o/new")
	if err != nil {
		t.Fatal(err)
	}
	want := `{"html":"https://github.com/o/new","n":12345678901234567890,"note":"o/old & more","other":"https://github.com/o/old-two/x","q":"https://github.com/o/new?x=1","repo":"o/new","url":"https://api.github.com/repos/o/new/rulesets/1"}`
	if string(out) != want {
		t.Fatalf("got  %s\nwant %s", out, want)
	}
}
