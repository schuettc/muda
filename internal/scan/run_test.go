package scan_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/schuettc/muda/internal/gh"
	"github.com/schuettc/muda/internal/ghtest"
	"github.com/schuettc/muda/internal/scan"
)

type scanTransport struct {
	base   http.RoundTripper
	broken bool
}

func (s scanTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if s.broken && strings.Contains(r.URL.Path, "/contents/.github/workflows/") {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"type":"file","encoding":"base64","content":"b246IFs="}`)), Request: r}, nil
	}
	return s.base.RoundTrip(r)
}

// fixtureUntil is the recorded snapshot's clock: every recorded run precedes it.
var fixtureUntil = time.Date(2026, 9, 29, 18, 0, 0, 0, time.UTC)

func TestRunTackleReplay(t *testing.T) {
	base := ghtest.Replay(filepath.Join("..", "ghtest", "testdata", "tackle"))
	since := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for _, broken := range []bool{false, true} {
		c := gh.New(gh.Options{NoCache: true, HTTP: &http.Client{Transport: scanTransport{base: base, broken: broken}}})
		report, err := scan.Run(context.Background(), c, "schuettc/tackle", "", since, fixtureUntil)
		if err != nil {
			t.Fatal(err)
		}
		if report.Ref != "main" {
			t.Fatalf("ref %s", report.Ref)
		}
		if broken {
			if len(report.Unavailable) == 0 || !strings.Contains(report.Unavailable[0].Why, "parse:") {
				t.Fatalf("parse failure: %+v", report)
			}
		} else {
			if len(report.Files) == 0 {
				t.Fatal("no workflows")
			}
			for _, s := range report.Signals {
				for _, e := range s.Evidence {
					if e.URL == "" {
						t.Fatal("missing evidence URL")
					}
				}
			}
		}
	}
	c := gh.New(gh.Options{NoCache: true, HTTP: &http.Client{Transport: base}})
	report, err := scan.Run(context.Background(), c, "schuettc/tackle", "main", since.Add(time.Hour), fixtureUntil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, u := range report.Unavailable {
		if u.What == "run history" {
			found = true
		}
	}
	if !found || len(report.Skipped) == 0 {
		t.Fatalf("duration fetch failure not named: %+v", report)
	}
}

// refRewrite serves a ref's contents requests from the fixture's main-branch
// recordings, so a ref with '/' or '#' can be exercised against replay.
type refRewrite struct {
	base  http.RoundTripper
	query string
}

func (r refRewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.Contains(req.URL.RawQuery, "ref="+r.query) {
		req = req.Clone(req.Context())
		req.URL.RawQuery = strings.Replace(req.URL.RawQuery, "ref="+r.query, "ref=main", 1)
	}
	return r.base.RoundTrip(req)
}

// Final review m-1: scan evidence links escape each ref segment exactly as
// gates does, so one --ref gives one link style in a compare report.
func TestRunEvidenceLinksEscapeRefSegments(t *testing.T) {
	base := ghtest.Replay(filepath.Join("..", "ghtest", "testdata", "tackle"))
	since := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct{ ref, query, blob string }{
		{"feature/x", "feature%2Fx", "https://github.com/schuettc/tackle/blob/feature/x/.github/workflows/"},
		{"feature#12", "feature%2312", "https://github.com/schuettc/tackle/blob/feature%2312/.github/workflows/"},
	} {
		c := gh.New(gh.Options{NoCache: true, HTTP: &http.Client{Transport: refRewrite{base: base, query: tc.query}}})
		report, err := scan.Run(context.Background(), c, "schuettc/tackle", tc.ref, since, fixtureUntil)
		if err != nil {
			t.Fatal(err)
		}
		links := 0
		for _, s := range report.Signals {
			for _, e := range s.Evidence {
				if e.Path == "" {
					continue
				}
				links++
				if !strings.HasPrefix(e.URL, tc.blob) {
					t.Errorf("%s: evidence link %s, want prefix %s", tc.ref, e.URL, tc.blob)
				}
			}
		}
		if links == 0 {
			t.Fatalf("%s: no file evidence; the fixture does not exercise links: %+v", tc.ref, report)
		}
	}
}

// overrideFile serves one workflow file's contents from yaml instead of the
// fixture.
type overrideFile struct {
	base       http.RoundTripper
	file, yaml string
}

func (o overrideFile) RoundTrip(r *http.Request) (*http.Response, error) {
	if strings.HasSuffix(r.URL.Path, "/contents/.github/workflows/"+o.file) {
		body := fmt.Sprintf(`{"type":"file","encoding":"base64","content":%q}`, base64.StdEncoding.EncodeToString([]byte(o.yaml)))
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	}
	return o.base.RoundTrip(r)
}

// Final review m-4: Run reports an expression-valued pin as unavailable.
func TestRunNamesExpressionPin(t *testing.T) {
	yaml := "on: push\njobs:\n  test:\n    runs-on: ubuntu-24.04\n    steps:\n      - uses: actions/setup-node@v4.1.0\n        with:\n          node-version: ${{ matrix.node }}\n"
	base := ghtest.Replay(filepath.Join("..", "ghtest", "testdata", "tackle"))
	c := gh.New(gh.Options{NoCache: true, HTTP: &http.Client{Transport: overrideFile{base: base, file: "verify.yml", yaml: yaml}}})
	report, err := scan.Run(context.Background(), c, "schuettc/tackle", "", time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), fixtureUntil)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range report.Unavailable {
		if u.What == ".github/workflows/verify.yml job test node version (expression)" {
			return
		}
	}
	t.Fatalf("expression pin not named: %+v", report.Unavailable)
}

// requestLog records every replayed request path.
type requestLog struct {
	base  http.RoundTripper
	paths []string
}

func (l *requestLog) RoundTrip(r *http.Request) (*http.Response, error) {
	l.paths = append(l.paths, r.URL.Path)
	return l.base.RoundTrip(r)
}

// TestRunUntilIsExclusive: no-path-filter durations read only runs in
// [since, until). A run created at --until is never timed; one created just
// before it is. The report carries the window end; workflows still come from
// the ref.
func TestRunUntilIsExclusive(t *testing.T) {
	since := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	bound := time.Date(2026, 9, 29, 17, 39, 43, 0, time.UTC)
	const jobs = "/repos/schuettc/tackle/actions/runs/36606519472/attempts/1/jobs"
	timed := func(until time.Time) bool {
		t.Helper()
		l := &requestLog{base: ghtest.Replay(filepath.Join("..", "ghtest", "testdata", "tackle"))}
		c := gh.New(gh.Options{NoCache: true, HTTP: &http.Client{Transport: l}})
		report, err := scan.Run(context.Background(), c, "schuettc/tackle", "", since, until)
		if err != nil {
			t.Fatal(err)
		}
		if !report.Until.Equal(until) || report.Ref != "main" {
			t.Fatalf("until %v ref %s", report.Until, report.Ref)
		}
		for _, u := range report.Unavailable {
			if u.What == "run history" {
				t.Fatalf("run history unavailable: %s", u.Why)
			}
		}
		for _, p := range l.paths {
			if p == jobs {
				return true
			}
		}
		return false
	}
	if timed(bound) {
		t.Fatal("run created at --until was timed")
	}
	if !timed(bound.Add(time.Second)) {
		t.Fatal("run created before --until was not timed")
	}
}

// A scan report states its whole run-history window, in JSON and in the
// Markdown header.
func TestRunReportStatesWindow(t *testing.T) {
	since := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 9, 29, 17, 39, 43, 0, time.UTC)
	c := gh.New(gh.Options{NoCache: true, HTTP: &http.Client{Transport: ghtest.Replay(filepath.Join("..", "ghtest", "testdata", "tackle"))}})
	report, err := scan.Run(context.Background(), c, "schuettc/tackle", "", since, until)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if err := scan.WriteJSON(&b, report); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"since": "2026-09-01T00:00:00Z"`, `"until": "2026-09-29T17:39:43Z"`} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("JSON lacks %s", want)
		}
	}
	header := strings.SplitN(scan.RenderMarkdown(report), "\n## ", 2)[0]
	if !strings.Contains(header, "**Run history window:** [2026-09-01T00:00:00Z, 2026-09-29T17:39:43Z)") {
		t.Errorf("Markdown header lacks the window: %q", header)
	}
}

// Final re-review N-2: a workflow path that is a symlink or a submodule is
// named unavailable, never dropped (which reads as removed) and never read
// through to the symlink's target.
func TestRunNonFileWorkflowEntriesAreUnavailable(t *testing.T) {
	link, sub := ".github/workflows/release.yml", ".github/workflows/vendored.yml"
	rt := ghtest.NonFileWorkflows(ghtest.Replay(filepath.Join("..", "ghtest", "testdata", "tackle")), []string{link}, []string{sub})
	c := gh.New(gh.Options{NoCache: true, HTTP: &http.Client{Transport: rt}})
	report, err := scan.Run(context.Background(), c, "schuettc/tackle", "", time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), fixtureUntil)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range report.Files {
		if f == link || f == sub {
			t.Fatalf("non-file entry %s scanned as a workflow", f)
		}
	}
	got := map[string]string{}
	for _, u := range report.Unavailable {
		got[u.What] = u.Why
	}
	for path, kind := range map[string]string{link: "symlink", sub: "submodule"} {
		if !strings.Contains(got[path], kind) {
			t.Fatalf("%s: unavailable %q; want it named as a %s (all: %+v)", path, got[path], kind, report.Unavailable)
		}
	}
}
