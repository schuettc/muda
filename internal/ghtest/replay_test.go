package ghtest

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReplayStatusFieldConforms verifies that Response.Status matches Go's
// net/http convention: "<code> <text>" (e.g. "200 OK", "404 Not Found").
func TestReplayStatusFieldConforms(t *testing.T) {
	for _, tc := range []struct {
		code int
		want string
	}{
		{200, "200 OK"},
		{404, "404 Not Found"},
	} {
		dir := t.TempDir()
		name := FixtureName("GET", "/repos/o/r/test")
		if err := os.WriteFile(filepath.Join(dir, name), []byte(`{}`), 0600); err != nil {
			t.Fatal(err)
		}
		if tc.code != 200 {
			if err := os.WriteFile(filepath.Join(dir, strings.TrimSuffix(name, ".json")+".meta.json"),
				[]byte(`{"status":404,"headers":{}}`), 0600); err != nil {
				t.Fatal(err)
			}
		}
		req, _ := http.NewRequest(http.MethodGet, "https://api.github.com/repos/o/r/test", nil)
		resp, err := Replay(dir).RoundTrip(req)
		if err != nil {
			t.Fatalf("code=%d: %v", tc.code, err)
		}
		_ = resp.Body.Close()
		if resp.Status != tc.want {
			t.Errorf("code=%d: Status=%q want %q", tc.code, resp.Status, tc.want)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRecordPreservesPublicHTTPErrorWithoutAuth(t *testing.T) {
	dir := t.TempDir()
	transport := Record(dir, "api.github.com", roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 404, Header: http.Header{"Authorization": []string{"Bearer ghp_SECRET"}}, Body: io.NopCloser(strings.NewReader(`{"message":"Not Found"}`)), Request: r}, nil
	}))
	req, _ := http.NewRequest(http.MethodGet, "https://api.github.com/repos/o/r/branches/main/protection", nil)
	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := Replay(dir).RoundTrip(req)
	if err != nil || got.StatusCode != 404 {
		t.Fatalf("replayed HTTP status=%v err=%v", got, err)
	}
	if err := got.Body.Close(); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		b, _ := os.ReadFile(filepath.Join(dir, entry.Name()))
		if strings.Contains(string(b), "ghp_SECRET") {
			t.Errorf("fixture leaks token in %s", entry.Name())
		}
	}
}

func TestReplayAndUnknownRequest(t *testing.T) {
	dir := t.TempDir()
	path := FixtureName("GET", "/repos/o/r/actions/runs?per_page=100")
	if err := os.WriteFile(filepath.Join(dir, path), []byte(`{"workflow_runs":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, strings.TrimSuffix(path, ".json")+".meta.json"), []byte(`{"status":200,"headers":{"Link":["<https://api.github.com/next>; rel=\"next\""]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodGet, "https://api.github.com/repos/o/r/actions/runs?per_page=100", nil)
	resp, err := Replay(dir).RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"workflow_runs":[]}` || resp.Header.Get("Link") == "" {
		t.Fatalf("body=%s headers=%v", body, resp.Header)
	}
	unknown, _ := http.NewRequest(http.MethodGet, "https://api.github.com/missing?private=1", nil)
	missing, err := Replay(dir).RoundTrip(unknown)
	if missing != nil {
		_ = missing.Body.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "/missing?private=1") {
		t.Fatalf("missing fixture error = %v", err)
	}
}
