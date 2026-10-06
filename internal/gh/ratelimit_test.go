package gh

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Final review M5: the reset is RFC3339 UTC and names the endpoint.
func TestRateLimitResetIsRFC3339(t *testing.T) {
	for _, tc := range []struct {
		header, value, want string
	}{
		{"X-RateLimit-Reset", "1780000000", time.Unix(1780000000, 0).UTC().Format(time.RFC3339)},
		{"Retry-After", "Wed, 21 Oct 2037 07:28:00 GMT", "2037-10-21T07:28:00Z"},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if tc.header == "Retry-After" {
				w.Header().Set("Retry-After", tc.value)
			} else {
				w.Header().Set("Retry-After", "400")
				w.Header().Set(tc.header, tc.value)
			}
			http.Error(w, "limited", http.StatusTooManyRequests)
		}))
		_, err := New(Options{BaseURL: srv.URL, NoCache: true, Sleep: func(time.Duration) {}}).Runs(context.Background(), "o/r", time.Now())
		srv.Close()
		var api *Error
		if !errors.As(err, &api) || !strings.Contains(err.Error(), "actions/runs") || !strings.Contains(err.Error(), "reset at "+tc.want) {
			t.Errorf("%s: %v", tc.header, err)
		}
		if tc.header == "X-RateLimit-Reset" && strings.Contains(err.Error(), tc.value) {
			t.Errorf("raw epoch shown: %v", err)
		}
	}
}

// Cancellation interrupts a rate-limit wait, with the injected or the
// default sleep.
func TestRateLimitWaitHonoursCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "120")
		http.Error(w, "limited", http.StatusTooManyRequests)
	}))
	defer srv.Close()
	block := make(chan struct{})
	defer close(block)
	for name, sleep := range map[string]func(time.Duration){"injected": func(time.Duration) { <-block }, "default": nil} {
		ctx, cancel := context.WithCancel(context.Background())
		time.AfterFunc(50*time.Millisecond, cancel)
		start := time.Now()
		_, err := New(Options{BaseURL: srv.URL, NoCache: true, Sleep: sleep}).Runs(ctx, "o/r", time.Now())
		if !errors.Is(err, context.Canceled) || time.Since(start) > 5*time.Second {
			t.Errorf("%s: err=%v after %v", name, err, time.Since(start))
		}
		cancel()
	}
}
