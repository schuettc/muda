package gh

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/schuettc/muda/internal/ghtest"
)

func TestResolveTokenOrder(t *testing.T) {
	for _, tc := range []struct {
		name, gh, github, cli, want string
		called                      bool
	}{
		{"GH_TOKEN", "first", "second", "third", "first", false},
		{"GITHUB_TOKEN", "", "second", "third", "second", false},
		{"gh fallback", "", "", "third", "third", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			token, err := ResolveToken(func(k string) string {
				if k == "GH_TOKEN" {
					return tc.gh
				}
				return tc.github
			}, func() (string, error) { called = true; return tc.cli, nil })
			if err != nil || token != tc.want || called != tc.called {
				t.Fatalf("token=%q err=%v gh-called=%v", token, err, called)
			}
		})
	}
	_, err := ResolveToken(func(string) string { return "" }, func() (string, error) { return "", errors.New("not logged in") })
	if err == nil || !strings.Contains(err.Error(), "GH_TOKEN") || !strings.Contains(err.Error(), "GITHUB_TOKEN") || !strings.Contains(err.Error(), "gh auth token") {
		t.Fatalf("missing auth instructions: %v", err)
	}
}

func TestRunsPaginates(t *testing.T) {
	calls := 0
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/repos/o/r/actions/runs" || r.URL.Query().Get("per_page") != "100" || r.URL.Query().Get("created") != ">=2026-09-01T00:00:00Z" {
			t.Errorf("bad query: %s", r.URL.String())
		}
		page := r.URL.Query().Get("page")
		if page == "" {
			page = "1"
		}
		if page != "3" {
			w.Header().Set("Link", fmt.Sprintf(`<%s/repos/o/r/actions/runs?created=%%3E%%3D2026-09-01T00%%3A00%%3A00Z&per_page=100&page=%s>; rel="next"`, srv.URL, map[string]string{"1": "2", "2": "3"}[page]))
		}
		_, _ = fmt.Fprintf(w, `{"workflow_runs":[{"id":%s,"status":"completed","run_attempt":1}]}`, page)
	}))
	defer srv.Close()
	c := New(Options{Token: "secret", BaseURL: srv.URL, NoCache: true})
	runs, err := c.Runs(context.Background(), "o/r", time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))
	if err != nil || len(runs) != 3 || runs[0].ID != 1 || runs[2].ID != 3 || calls != 3 {
		t.Fatalf("runs=%+v err=%v calls=%d", runs, err, calls)
	}
}

func TestClientNotFoundIsNamed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, `{"message":"Not Found"}`, 404) }))
	defer srv.Close()
	_, err := New(Options{Token: "secret", BaseURL: srv.URL, NoCache: true}).Runs(context.Background(), "o/r", time.Now())
	var apiErr *Error
	if !errors.As(err, &apiErr) || apiErr.Status != 404 || !strings.Contains(apiErr.Endpoint, "actions/runs") || !strings.Contains(apiErr.Error(), "not found or no access") {
		t.Fatalf("not-found error = %v", err)
	}
	if strings.Contains(apiErr.Error(), "secret") {
		t.Fatal("token leaked")
	}
}

func TestLogRedirectDropsAuth(t *testing.T) {
	blob := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("token sent to blob host: %s", r.Header.Get("Authorization"))
		}
		_, _ = fmt.Fprint(w, "timed log")
	}))
	defer blob.Close()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("missing API auth")
		}
		if !strings.HasSuffix(r.URL.Path, "/logs") {
			_, _ = io.WriteString(w, `{"status":"completed"}`)
			return
		}
		http.Redirect(w, r, strings.Replace(blob.URL, "127.0.0.1", "localhost", 1), http.StatusFound)
	}))
	defer api.Close()
	log, err := New(Options{Token: "secret", BaseURL: api.URL, NoCache: true}).JobLog(context.Background(), "o/r", 7)
	if err != nil || log != "timed log" {
		t.Fatalf("log %q err %v", log, err)
	}
}

func TestRecordLogAfterRedirectKeepsOnlyBody(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MUDA_RECORD", dir)
	blob := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "##[group]build\nAUTHORIZATION: basic ***\nP12_PASSWORD: masked\nstep passed  \n")
	}))
	defer blob.Close()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/logs") {
			_, _ = io.WriteString(w, `{"status":"completed"}`)
			return
		}
		http.Redirect(w, r, blob.URL+"/signed?token=SIGNED_SECRET", http.StatusFound)
	}))
	defer api.Close()
	c := New(Options{BaseURL: api.URL, NoCache: true})
	if _, err := c.JobLog(context.Background(), "o/r", 7); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ghtest.FixtureName("GET", "/repos/o/r/actions/jobs/7/logs"))
	body, err := os.ReadFile(path)
	if err != nil || string(body) != "##[group]build\n[redacted credential line]\n[redacted credential line]\nstep passed\n" {
		t.Fatalf("recorded log=%q err=%v", body, err)
	}
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		b, _ := os.ReadFile(filepath.Join(dir, entry.Name()))
		if strings.Contains(string(b), "SIGNED_SECRET") {
			t.Errorf("signed URL leaked in %s", entry.Name())
		}
	}
}

func TestRetryAfterHTTPDate(t *testing.T) {
	reset := time.Now().Add(4 * time.Second).UTC().Format(http.TimeFormat)
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", reset)
			http.Error(w, "limited", http.StatusTooManyRequests)
			return
		}
		_, _ = io.WriteString(w, `{"workflow_runs":[]}`)
	}))
	defer srv.Close()
	var slept []time.Duration
	_, err := New(Options{BaseURL: srv.URL, NoCache: true, Sleep: func(d time.Duration) { slept = append(slept, d) }}).Runs(context.Background(), "o/r", time.Now())
	if err != nil || calls != 2 || len(slept) != 1 || slept[0] < 2*time.Second {
		t.Fatalf("HTTP-date Retry-After: calls=%d sleeps=%v err=%v", calls, slept, err)
	}
}

func TestForbiddenWithRetryAfterRetries(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "limited", http.StatusForbidden)
			return
		}
		_, _ = io.WriteString(w, `{"workflow_runs":[]}`)
	}))
	defer srv.Close()
	var slept []time.Duration
	c := New(Options{BaseURL: srv.URL, NoCache: true, Sleep: func(d time.Duration) { slept = append(slept, d) }})
	_, err := c.Runs(context.Background(), "o/r", time.Now())
	if err != nil || calls != 2 || len(slept) != 1 || slept[0] != time.Second {
		t.Fatalf("403 Retry-After: calls=%d sleeps=%v err=%v", calls, slept, err)
	}
}

func TestRateLimitRetriesThenFails(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls != 2 {
			w.Header().Set("Retry-After", "2")
			w.Header().Set("X-RateLimit-Reset", "1780000000")
			http.Error(w, "limited", http.StatusTooManyRequests)
			return
		}
		_, _ = fmt.Fprint(w, `{"total_count":0,"workflow_runs":[]}`)
	}))
	defer srv.Close()
	var slept []time.Duration
	c := New(Options{Token: "secret", BaseURL: srv.URL, NoCache: true, Sleep: func(d time.Duration) { slept = append(slept, d) }})
	if _, err := c.Runs(context.Background(), "o/r", time.Now()); err != nil || calls != 2 || len(slept) != 1 || slept[0] != 2*time.Second {
		t.Fatalf("retry calls=%d sleeps=%v err=%v", calls, slept, err)
	}

	limit := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "180")
		w.Header().Set("X-RateLimit-Reset", "1780000000")
		http.Error(w, "limited", http.StatusTooManyRequests)
	}))
	defer limit.Close()
	slept = nil
	c = New(Options{Token: "secret", BaseURL: limit.URL, NoCache: true, Sleep: func(d time.Duration) { slept = append(slept, d) }})
	_, err := c.Runs(context.Background(), "o/r", time.Now())
	if err == nil || !strings.Contains(err.Error(), "actions/runs") || !strings.Contains(err.Error(), time.Unix(1780000000, 0).UTC().Format(time.RFC3339)) || len(slept) > 2 {
		t.Fatalf("limit err=%v waits=%v", err, slept)
	}
}

func TestCacheCompletedAnnotationsOnly(t *testing.T) {
	status := "completed"
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/annotations") {
			calls++
			_, _ = io.WriteString(w, `[{"path":"main.go","message":"warning"}]`)
			return
		}
		_, _ = fmt.Fprintf(w, `{"status":%q}`, status)
	}))
	defer srv.Close()
	c := New(Options{BaseURL: srv.URL, CacheDir: t.TempDir()})
	for range 2 {
		if _, err := c.Annotations(context.Background(), "o/r", 1); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatalf("completed annotation page fetched %d times", calls)
	}
	status = "in_progress"
	c = New(Options{BaseURL: srv.URL, CacheDir: t.TempDir()})
	for range 2 {
		if _, err := c.Annotations(context.Background(), "o/r", 2); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 3 {
		t.Fatalf("in-progress annotations fetched %d times total", calls)
	}
}

func TestCacheCompletedLogsOnly(t *testing.T) {
	status := "completed"
	logs := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/logs") {
			logs++
			_, _ = io.WriteString(w, "build output")
			return
		}
		_, _ = fmt.Fprintf(w, `{"status":%q}`, status)
	}))
	defer srv.Close()
	c := New(Options{BaseURL: srv.URL, CacheDir: t.TempDir()})
	for range 2 {
		if _, err := c.JobLog(context.Background(), "o/r", 7); err != nil {
			t.Fatal(err)
		}
	}
	if logs != 1 {
		t.Fatalf("completed log downloaded %d times", logs)
	}
	status = "in_progress"
	c = New(Options{BaseURL: srv.URL, CacheDir: t.TempDir()})
	for range 2 {
		if _, err := c.JobLog(context.Background(), "o/r", 8); err != nil {
			t.Fatal(err)
		}
	}
	if logs != 3 {
		t.Fatalf("in-progress log downloaded %d times total", logs)
	}
}

func TestCacheCompletedOnly(t *testing.T) {
	status := "completed"
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = fmt.Fprintf(w, `{"jobs":[{"id":3,"status":%q}]}`, status)
	}))
	defer srv.Close()
	c := New(Options{Token: "secret", BaseURL: srv.URL, CacheDir: t.TempDir()})
	for range 2 {
		if _, err := c.Jobs(context.Background(), "o/r", 1, 1); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatalf("completed jobs fetched %d times", calls)
	}
	status = "in_progress"
	c = New(Options{Token: "secret", BaseURL: srv.URL, CacheDir: t.TempDir()})
	for range 2 {
		if _, err := c.Jobs(context.Background(), "o/r", 2, 1); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 3 {
		t.Fatalf("in-progress jobs fetched %d times in total", calls)
	}
}

func TestTokenNeverCached(t *testing.T) {
	dir := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"jobs":[{"id":4,"status":"completed"}]}`)
	}))
	defer srv.Close()
	c := New(Options{Token: "ghp_SECRET", BaseURL: srv.URL, CacheDir: dir})
	if _, err := c.Jobs(context.Background(), "o/r", 4, 1); err != nil {
		t.Fatal(err)
	}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		b, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		if strings.Contains(string(b), "ghp_SECRET") {
			t.Errorf("token in cache: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRulesetsSortedByID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/rulesets/") {
			id := strings.TrimPrefix(r.URL.Path, "/repos/o/r/rulesets/")
			_, _ = fmt.Fprintf(w, `{"id":%s,"enforcement":"active"}`, id)
			return
		}
		_, _ = io.WriteString(w, `[{"ruleset_id":20,"ruleset_name":"B","type":"required_status_checks","parameters":{"required_status_checks":[{"context":"build"}]}},{"ruleset_id":10,"ruleset_name":"A","type":"required_status_checks","parameters":{"required_status_checks":[{"context":"test"}]}}]`)
	}))
	defer srv.Close()
	for range 20 {
		sets, err := New(Options{BaseURL: srv.URL, NoCache: true}).Rulesets(context.Background(), "o/r", "main")
		if err != nil || len(sets) != 2 || sets[0].ID != 10 || sets[1].ID != 20 {
			t.Fatalf("unstable rulesets=%+v err=%v", sets, err)
		}
	}
}

func TestBranchProtectionSlashedBranchIsEscapedOnce(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/repos/o/r/branches/feature%2Fshipping/protection" {
			t.Errorf("escaped branch path = %q", r.URL.EscapedPath())
		}
		_, _ = io.WriteString(w, `{"required_status_checks":{"contexts":[]}}`)
	}))
	defer srv.Close()
	_, err := New(Options{BaseURL: srv.URL, NoCache: true}).BranchProtection(context.Background(), "o/r", "feature/shipping")
	if err != nil {
		t.Fatal(err)
	}
}

func TestAPILinkDoesNotSendTokenToOtherHost(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("visited untrusted next page")
		_, _ = io.WriteString(w, `{"workflow_runs":[]}`)
	}))
	defer other.Close()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Link", `<`+other.URL+`/repos/o/r/actions/runs?page=2>; rel="next"`)
		_, _ = io.WriteString(w, `{"workflow_runs":[]}`)
	}))
	defer api.Close()
	_, err := New(Options{Token: "secret", BaseURL: api.URL, NoCache: true}).Runs(context.Background(), "o/r", time.Now())
	if err == nil || !strings.Contains(err.Error(), "host") {
		t.Fatalf("untrusted pagination must fail: %v", err)
	}
}

// TestRunsAlwaysFetchesLive is a regression guard for M2: the Runs listing must
// never be served from cache. A run that was completed on the first call and
// is rerun (in_progress, run_attempt 2) on the second call must be visible
// with the new state. This also covers the last-page scenario where a cached
// result would freeze rerun status and attempt count forever.
func TestRunsAlwaysFetchesLive(t *testing.T) {
	var srv *httptest.Server
	call := 0
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/o/r/actions/runs" {
			http.Error(w, "not found", 404)
			return
		}
		call++
		switch call {
		case 1:
			// page 1 with link to page 2
			w.Header().Set("Link", fmt.Sprintf(`<%s/repos/o/r/actions/runs?page=2>; rel="next"`, srv.URL))
			_, _ = fmt.Fprint(w, `{"workflow_runs":[{"id":10,"status":"completed","run_attempt":1}]}`)
		case 2:
			// page 2 (last) - no Link header; would be cached if completedRuns were used
			_, _ = fmt.Fprint(w, `{"workflow_runs":[{"id":11,"status":"completed","run_attempt":1}]}`)
		case 3:
			// second fetch of page 1 - run 10 now rerun
			w.Header().Set("Link", fmt.Sprintf(`<%s/repos/o/r/actions/runs?page=2>; rel="next"`, srv.URL))
			_, _ = fmt.Fprint(w, `{"workflow_runs":[{"id":10,"status":"in_progress","run_attempt":2}]}`)
		case 4:
			// second fetch of page 2 (last) - must be live, not cached
			_, _ = fmt.Fprint(w, `{"workflow_runs":[{"id":11,"status":"in_progress","run_attempt":2}]}`)
		}
	}))
	defer srv.Close()

	cacheDir := t.TempDir()
	c := New(Options{Token: "secret", BaseURL: srv.URL, CacheDir: cacheDir})
	since := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

	// First call: all completed
	runs1, err := c.Runs(context.Background(), "o/r", since)
	if err != nil || len(runs1) != 2 {
		t.Fatalf("first Runs: len=%d err=%v", len(runs1), err)
	}
	if runs1[0].Status != "completed" || runs1[0].RunAttempt != 1 {
		t.Fatalf("first call run[0]: %+v", runs1[0])
	}

	// Second call: runs have been requeued — must see new state, never stale cache
	runs2, err := c.Runs(context.Background(), "o/r", since)
	if err != nil || len(runs2) != 2 {
		t.Fatalf("second Runs: len=%d err=%v", len(runs2), err)
	}
	if runs2[0].Status != "in_progress" || runs2[0].RunAttempt != 2 {
		t.Fatalf("second call must see rerun state; run[0]=%+v", runs2[0])
	}
	if runs2[1].Status != "in_progress" || runs2[1].RunAttempt != 2 {
		t.Fatalf("last-page run must also be live; run[1]=%+v", runs2[1])
	}
	if call != 4 {
		t.Fatalf("expected 4 server calls (2 pages × 2 fetches), got %d", call)
	}
}

// TestRunsTruncatedListingIsAnError: GitHub caps a created-filtered runs
// listing (1,000 results). A total_count above what pagination returned must
// fail naming the endpoint, never be returned as the complete window.
func TestRunsTruncatedListingIsAnError(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "" {
			w.Header().Set("Link", fmt.Sprintf(`<%s/repos/o/r/actions/runs?page=2>; rel="next"`, srv.URL))
			_, _ = fmt.Fprint(w, `{"total_count":1500,"workflow_runs":[{"id":1,"status":"completed","run_attempt":1}]}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"total_count":1500,"workflow_runs":[{"id":2,"status":"completed","run_attempt":1}]}`)
	}))
	defer srv.Close()
	c := New(Options{BaseURL: srv.URL, NoCache: true})
	runs, err := c.Runs(context.Background(), "o/r", time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))
	if err == nil || runs != nil || !strings.Contains(err.Error(), "/repos/o/r/actions/runs") || !strings.Contains(err.Error(), "1500") {
		t.Fatalf("want a named truncation error, got runs=%d err=%v", len(runs), err)
	}
}

func TestRunsCompleteMultiPageWithTotalCount(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("page") {
		case "":
			w.Header().Set("Link", fmt.Sprintf(`<%s/repos/o/r/actions/runs?page=2>; rel="next"`, srv.URL))
			_, _ = fmt.Fprint(w, `{"total_count":3,"workflow_runs":[{"id":1,"run_attempt":1},{"id":2,"run_attempt":1}]}`)
		default:
			_, _ = fmt.Fprint(w, `{"total_count":3,"workflow_runs":[{"id":3,"run_attempt":1}]}`)
		}
	}))
	defer srv.Close()
	c := New(Options{BaseURL: srv.URL, NoCache: true})
	runs, err := c.Runs(context.Background(), "o/r", time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))
	if err != nil || len(runs) != 3 {
		t.Fatalf("runs=%d err=%v", len(runs), err)
	}
}

// TestCacheFileOnlyAtRunHead: only a run's own workflow file, read at the
// head commit the API reported for that run, is cached. File never caches,
// whatever its ref looks like: a 40-hex string is not proof of a commit.
func TestCacheFileOnlyAtRunHead(t *testing.T) {
	calls := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls[r.URL.Query().Get("ref")]++
		_, _ = io.WriteString(w, `{"type":"file","encoding":"base64","content":"b246IHB1c2gK"}`)
	}))
	defer srv.Close()
	const sha = "0123456789abcdef0123456789abcdef01234567"
	const path = ".github/workflows/ci.yml"
	c := New(Options{BaseURL: srv.URL, CacheDir: t.TempDir()})
	for _, ref := range []string{sha, "main", "0123456"} {
		for range 2 {
			data, found, err := c.File(context.Background(), "o/r", ref, path)
			if err != nil || !found || string(data) != "on: push\n" {
				t.Fatalf("ref %s: %q %v %v", ref, data, found, err)
			}
		}
		if calls[ref] != 2 {
			t.Fatalf("File at %s fetched %d times, want live each time", ref, calls[ref])
		}
	}
	run := Run{ID: 7, Path: path, HeadSHA: sha}
	for range 2 {
		data, found, err := c.RunFile(context.Background(), "o/r", run)
		if err != nil || !found || string(data) != "on: push\n" {
			t.Fatalf("RunFile: %q %v %v", data, found, err)
		}
	}
	if calls[sha] != 3 {
		t.Fatalf("a run's file at its reported head commit must be fetched once, then cached: %d calls in total at %s", calls[sha], sha)
	}
	if _, _, err := c.RunFile(context.Background(), "o/r", Run{ID: 8, Path: path}); err == nil {
		t.Fatal("a run without a head commit has no file to read")
	}
}

// TestRunFileCacheHoldsOnlyContent: a contents response can carry
// download_url with a token (private repositories) and other URLs. The cache
// keeps only the file's content and blob sha, so no token reaches the disk.
func TestRunFileCacheHoldsOnlyContent(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = io.WriteString(w, `{"type":"file","encoding":"base64","content":"b246IHB1c2gK","sha":"abc123","name":"ci.yml",`+
			`"download_url":"https://raw.example.test/o/r/x/ci.yml?token=SECRETTOKEN","git_url":"https://api.example.test/git/blobs/abc123",`+
			`"_links":{"self":"https://api.example.test/self?token=SECRETTOKEN"}}`)
	}))
	defer srv.Close()
	dir := t.TempDir()
	c := New(Options{BaseURL: srv.URL, CacheDir: dir})
	run := Run{ID: 7, Path: ".github/workflows/ci.yml", HeadSHA: "0123456789abcdef0123456789abcdef01234567"}
	for range 2 {
		data, found, err := c.RunFile(context.Background(), "o/r", run)
		if err != nil || !found || string(data) != "on: push\n" {
			t.Fatalf("RunFile: %q %v %v", data, found, err)
		}
	}
	if calls != 1 {
		t.Fatalf("fetched %d times; the second read must come from the cache", calls)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("cache entries %v %v", entries, err)
	}
	b, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"SECRETTOKEN", "download_url", "git_url", "_links", "token"} {
		if strings.Contains(string(b), bad) {
			t.Fatalf("cache entry holds %q: %s", bad, b)
		}
	}
	if !strings.Contains(string(b), "b246IHB1c2gK") || !strings.Contains(string(b), "abc123") {
		t.Fatalf("cache entry must hold the content and its sha: %s", b)
	}
}
