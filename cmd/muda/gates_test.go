package main

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/schuettc/muda/internal/cli"
)

// Final review M3: `muda gates` (the skills' preflight probe) still exits 0
// with an unparseable workflow, listing it as unavailable.
func TestGatesCommandListsUnparseableWorkflow(t *testing.T) {
	yaml := "name: Deploy\non: push\njobs:\n  base: &b\n    steps:\n      - run: go test ./...\n  test:\n    <<: *b\n"
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/o/r":
			_, _ = fmt.Fprint(w, `{"default_branch":"main"}`)
		case "/repos/o/r/branches/main/protection":
			w.WriteHeader(404)
			_, _ = fmt.Fprint(w, `{"message":"Branch not protected"}`)
		case "/repos/o/r/rules/branches/main":
			_, _ = fmt.Fprint(w, `[]`)
		case "/repos/o/r/environments":
			_, _ = fmt.Fprint(w, `{"environments":[]}`)
		case "/repos/o/r/contents/.github/workflows":
			_, _ = fmt.Fprint(w, `[{"type":"file","path":".github/workflows/deploy.yml"}]`)
		case "/repos/o/r/contents/.github/workflows/deploy.yml":
			_, _ = fmt.Fprintf(w, `{"type":"file","encoding":"base64","content":%q}`, base64.StdEncoding.EncodeToString([]byte(yaml)))
		default:
			w.WriteHeader(404)
		}
	}))
	defer s.Close()
	env := cli.Env{
		Getenv:    func(k string) string { return map[string]string{"GH_TOKEN": "fixture-token"}[k] },
		GHToken:   func() (string, error) { return "", errors.New("gh not used") },
		GitRemote: func() (string, error) { return "", errors.New("git not used") },
		Now:       func() time.Time { return time.Date(2026, 9, 29, 18, 0, 0, 0, time.UTC) },
		BaseURL:   s.URL,
		CacheDir:  t.TempDir(),
		HTTP:      s.Client(),
	}
	var out, errs bytes.Buffer
	if code := newApp(env).Dispatch([]string{"gates", "--repo", "o/r", "--format", "md", "--no-cache"}, &out, &errs); code != 0 {
		t.Fatalf("exit %d: %s", code, errs.String())
	}
	if !strings.Contains(out.String(), "- .github/workflows/deploy.yml: ") || !strings.Contains(out.String(), "merge key") {
		t.Fatalf("unparseable workflow not listed:\n%s", out.String())
	}
}

// `muda gates --ref` reads workflows at the ref through the real app wiring;
// settings still come from the default branch.
func TestGatesRefFlag(t *testing.T) {
	var out, errs bytes.Buffer
	if code := newApp(fixtureEnv(t)).Dispatch([]string{"gates", "--ref", "feature", "--no-cache"}, &out, &errs); code != 0 {
		t.Fatalf("exit %d: %s", code, errs.String())
	}
	for _, want := range []string{`"ref": "feature"`, `"branch": "main"`, `/blob/feature/.github/workflows/verify.yml`, `"sha256": "`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("gates --ref output lacks %s", want)
		}
	}
	out.Reset()
	if code := newApp(fixtureEnv(t)).Dispatch([]string{"gates", "--no-cache"}, &out, &errs); code != 0 {
		t.Fatalf("exit %d: %s", code, errs.String())
	}
	if !strings.Contains(out.String(), `"ref": "main"`) {
		t.Error("default ref is not the default branch")
	}
	out.Reset()
	if code := newApp(fixtureEnv(t)).Dispatch([]string{"gates", "--ref", "feature", "--format", "md", "--no-cache"}, &out, &errs); code != 0 {
		t.Fatalf("md exit %d: %s", code, errs.String())
	}
	if !strings.Contains(out.String(), "workflows at `feature`") || !strings.Contains(out.String(), "default branch `main`") {
		t.Errorf("gates --ref Markdown does not state the ref:\n%s", out.String())
	}
}

// An explicit empty --ref would silently mean the default branch: usage error.
func TestGatesEmptyRefIsUsage(t *testing.T) {
	for _, value := range []string{"", "  "} {
		var out, errs bytes.Buffer
		if code := newApp(fixtureEnv(t)).Dispatch([]string{"gates", "--ref", value, "--no-cache"}, &out, &errs); code != 2 || !strings.Contains(errs.String(), "--ref") {
			t.Errorf("--ref %q: exit %d, stderr %q", value, code, errs.String())
		}
		if out.Len() != 0 {
			t.Errorf("--ref %q produced output", value)
		}
	}
}

// Help says where settings and workflows come from and that --since is ignored.
func TestGatesHelpNamesRefSources(t *testing.T) {
	var out, errs bytes.Buffer
	code := newApp(fixtureEnv(t)).Dispatch([]string{"gates", "--help"}, &out, &errs)
	help := strings.ToLower(strings.Join(strings.Fields(out.String()+errs.String()), " "))
	if code != 0 {
		t.Fatalf("exit %d: %s", code, help)
	}
	for _, want := range []string{"--since is ignored", "repository settings come from the default branch", "workflows are read at --ref (the default branch if omitted)"} {
		if !strings.Contains(help, want) {
			t.Errorf("help lacks %q: %s", want, help)
		}
	}
	if strings.Contains(help, "gates read the current default branch") {
		t.Errorf("stale help: %s", help)
	}
}
