package gh

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDirectRunLiveIdentity(t *testing.T) {
	for _, body := range []string{`{"id":1,"run_attempt":2}`, `{"id":2,"run_attempt":2}`, `{"id":1}`, `{"id":1,"run_attempt":-1}`} {
		calls := 0
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			if r.URL.Path != "/repos/o/r/actions/runs/1" {
				t.Errorf("history discovery: %s", r.URL)
			}
			_, _ = fmt.Fprint(w, body)
		}))
		c := New(Options{BaseURL: s.URL, CacheDir: t.TempDir()})
		for i := 0; i < 2; i++ {
			run, err := c.Run(context.Background(), "o/r", 1)
			if body == `{"id":1,"run_attempt":2}` {
				if err != nil || run.ID != 1 || run.RunAttempt != 2 {
					t.Fatalf("%+v %v", run, err)
				}
			} else if err == nil {
				t.Fatal("invalid run accepted", body)
			}
		}
		if calls != 2 {
			t.Fatal("mutable metadata cached", calls)
		}
		s.Close()
	}
}

type streamTransport struct{ calls int }
type repeatedReader struct{ remaining int64 }

func (r *repeatedReader) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	n := len(p)
	if int64(n) > r.remaining {
		n = int(r.remaining)
	}
	for i := 0; i < n; i++ {
		p[i] = 'x'
	}
	r.remaining -= int64(n)
	return n, nil
}
func (s *streamTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	s.calls++
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(&repeatedReader{remaining: (64 << 20) + 1}), Request: r}, nil
}
func TestResponseOverflowNeverPartialOrCached(t *testing.T) {
	for _, log := range []bool{false, true} {
		rt := &streamTransport{}
		c := New(Options{HTTP: &http.Client{Transport: rt}, CacheDir: t.TempDir()})
		for i := 0; i < 2; i++ {
			body, _, err := c.get(context.Background(), "https://api.github.com/oversized", func([]byte) bool { return true }, log)
			if err == nil || !strings.Contains(err.Error(), "/oversized") || !strings.Contains(err.Error(), "exceeds") || len(body) != 0 {
				t.Fatalf("partial %d bytes: %v", len(body), err)
			}
		}
		if rt.calls != 2 {
			t.Fatal("overflow cached")
		}
	}
}

type jobLogStreamTransport struct{ logs int }

func (s *jobLogStreamTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	body := io.NopCloser(strings.NewReader(`{"status":"completed"}`))
	if strings.HasSuffix(r.URL.Path, "/logs") {
		s.logs++
		body = io.NopCloser(&repeatedReader{remaining: (64 << 20) + 1})
	}
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: body, Request: r}, nil
}
func TestJobLogOverflowEndpointAndCache(t *testing.T) {
	rt := &jobLogStreamTransport{}
	c := New(Options{HTTP: &http.Client{Transport: rt}, CacheDir: t.TempDir()})
	for i := 0; i < 2; i++ {
		raw, err := c.JobLog(context.Background(), "o/r", 22)
		if raw != "" || err == nil || !strings.Contains(err.Error(), "/repos/o/r/actions/jobs/22/logs") || !strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("partial log of %d bytes: %v", len(raw), err)
		}
	}
	if rt.logs != 2 {
		t.Fatal("oversized completed log cached")
	}
}
