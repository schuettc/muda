// Package ghtest records and replays public GitHub API responses. Fixture
// filenames include only method and endpoint; neither a bearer token nor a
// signed redirect target is stored.
package ghtest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var safe = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// FixtureName is stable for each path and query, while the suffix protects
// against sanitisation collisions (e.g. `x=a-b` vs `x=a/b`).
func FixtureName(method, pathQuery string) string {
	prefix := safe.ReplaceAllString(method+"_"+pathQuery, "_")
	if len(prefix) > 150 {
		prefix = prefix[:150]
	}
	sum := sha256.Sum256([]byte(method + " " + pathQuery))
	return prefix + "_" + hex.EncodeToString(sum[:6]) + ".json"
}

type meta struct {
	Status  int         `json:"status"`
	Headers http.Header `json:"headers"`
}

type replay struct{ dir string }

func Replay(dir string) http.RoundTripper { return replay{dir: dir} }

func (r replay) RoundTrip(req *http.Request) (*http.Response, error) {
	path := req.URL.EscapedPath()
	if req.URL.RawQuery != "" {
		path += "?" + req.URL.RawQuery
	}
	name := FixtureName(req.Method, path)
	body, err := os.ReadFile(filepath.Join(r.dir, name))
	if err != nil {
		return nil, fmt.Errorf("no GitHub fixture for %s %s: %w", req.Method, path, err)
	}
	m := meta{Status: 200, Headers: make(http.Header)}
	if b, err := os.ReadFile(filepath.Join(r.dir, strings.TrimSuffix(name, ".json")+".meta.json")); err == nil {
		if err := json.Unmarshal(b, &m); err != nil {
			return nil, fmt.Errorf("fixture %s metadata: %w", name, err)
		}
	}
	if m.Status == 0 {
		m.Status = 200
	}
	return &http.Response{StatusCode: m.Status, Status: fmt.Sprintf("%d %s", m.Status, http.StatusText(m.Status)), Header: m.Headers, Body: io.NopCloser(bytes.NewReader(body)), Request: req}, nil
}

// SaveLog writes the final log body under the original API endpoint's key.
// A signed redirect URL and its query string are never part of that key.
func SaveLog(dir, endpoint string, body []byte) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	// Logs can contain masked credential metadata and, on a misconfigured
	// public run, actual secrets. Drop whole credential-bearing lines before
	// persisting; the fixture still requires a human review before commit.
	lines := strings.SplitAfter(string(body), "\n")
	for i, line := range lines {
		ending := ""
		if strings.HasSuffix(line, "\n") {
			ending = "\n"
		}
		lines[i] = strings.TrimRight(line, " \t\r\n") + ending
		lower := strings.ToLower(line)
		if strings.Contains(lower, "authorization") || strings.Contains(lower, "secret") || strings.Contains(lower, "token") || strings.Contains(lower, "password") {
			if strings.HasSuffix(line, "\n") {
				lines[i] = "[redacted credential line]\n"
			} else {
				lines[i] = "[redacted credential line]"
			}
		}
	}
	return os.WriteFile(filepath.Join(dir, FixtureName(http.MethodGet, endpoint)), []byte(strings.Join(lines, "")), 0600)
}

type recorder struct {
	dir, apiHost string
	next         http.RoundTripper
}

// Record captures only responses from the configured API host, never signed
// blob URLs or redirect Location headers. Do not commit recordings without
// reviewing them for private data and provider-added secrets in response bodies.
func Record(dir, apiHost string, next http.RoundTripper) http.RoundTripper {
	if next == nil {
		next = http.DefaultTransport
	}
	return recorder{dir: dir, apiHost: apiHost, next: next}
}

func (r recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := r.next.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if req.URL.Host != r.apiHost || resp.StatusCode < 200 || (resp.StatusCode >= 300 && resp.StatusCode < 400) {
		return resp, nil
	}
	body, err := io.ReadAll(resp.Body)
	closeErr := resp.Body.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	path := req.URL.EscapedPath()
	if req.URL.RawQuery != "" {
		path += "?" + req.URL.RawQuery
	}
	name := FixtureName(req.Method, path)
	if err := os.MkdirAll(r.dir, 0700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(r.dir, name), body, 0600); err != nil {
		return nil, err
	}
	// Only pagination metadata is necessary. Never persist Authorization,
	// Set-Cookie, or a signed Location.
	if link := resp.Header.Get("Link"); link != "" || resp.StatusCode != 200 {
		headers := make(http.Header)
		if link != "" {
			headers.Set("Link", link)
		}
		m, _ := json.Marshal(meta{Status: resp.StatusCode, Headers: headers})
		if err := os.WriteFile(filepath.Join(r.dir, strings.TrimSuffix(name, ".json")+".meta.json"), m, 0600); err != nil {
			return nil, err
		}
	}
	return resp, nil
}
