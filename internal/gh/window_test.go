package gh

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRunsWindow(t *testing.T) {
	since := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	until := since.Add(24 * time.Hour)
	for _, total := range []string{"2", "1001", "", `"invalid"`, "-1", "null", "1.5"} {
		t.Run(total, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("created") != "2026-09-01T00:00:00Z..2026-09-02T00:00:00Z" || r.URL.Query().Get("per_page") != "100" {
					t.Errorf("query %s", r.URL)
				}
				prefix := ""
				if total != "" {
					prefix = `"total_count":` + total + `,`
				}
				_, _ = fmt.Fprintf(w, `{%s"workflow_runs":[{"id":1,"created_at":"2026-09-01T00:00:00Z"},{"id":2,"created_at":"2026-09-02T00:00:00Z"}]}`, prefix)
			}))
			defer srv.Close()
			c := New(Options{Token: "secret", BaseURL: srv.URL, NoCache: true})
			runs, err := c.RunsWindow(context.Background(), "o/r", since, until)
			if total == "2" {
				if err != nil || len(runs) != 1 || runs[0].ID != 1 {
					t.Fatalf("%v %v", runs, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "listing") {
				t.Fatalf("expected completeness error: %v", err)
			}
		})
	}
	c := New(Options{Token: "secret", NoCache: true})
	for _, bounds := range [][2]time.Time{{{}, until}, {since, {}}, {until, since}, {since, since}} {
		if _, err := c.RunsWindow(context.Background(), "o/r", bounds[0], bounds[1]); err == nil {
			t.Fatal("invalid bounds accepted")
		}
	}
}

func TestRunsWindowLivePaginationAndDedup(t *testing.T) {
	since := time.Date(2026, 9, 1, 0, 0, 0, 123, time.UTC)
	until := since.Add(time.Hour)
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if got := r.URL.Query().Get("created"); got != "2026-09-01T00:00:00.000000123Z..2026-09-01T01:00:00.000000123Z" {
			t.Errorf("window precision lost: %s", got)
		}
		if r.URL.Query().Get("page") == "2" {
			_, _ = fmt.Fprint(w, `{"total_count":3,"workflow_runs":[{"id":2,"created_at":"2026-09-01T00:00:01Z"},{"id":3,"created_at":"2026-09-01T01:00:00.000000123Z"}]}`)
		} else {
			w.Header().Set("Link", fmt.Sprintf(`<http://%s%s?created=%s&per_page=100&page=2>; rel="next"`, r.Host, r.URL.Path, r.URL.Query().Get("created")))
			_, _ = fmt.Fprint(w, `{"total_count":3,"workflow_runs":[{"id":1,"created_at":"2026-09-01T00:00:00Z"},{"id":2,"created_at":"2026-09-01T00:00:01Z"}]}`)
		}
	}))
	defer srv.Close()
	c := New(Options{Token: "secret", BaseURL: srv.URL, CacheDir: t.TempDir()})
	for range 2 {
		runs, err := c.RunsWindow(context.Background(), "o/r", since, until)
		if err != nil || len(runs) != 1 || runs[0].ID != 2 {
			t.Fatalf("%v %v", runs, err)
		}
	}
	if calls != 4 {
		t.Fatalf("listing cached: %d", calls)
	}
}
