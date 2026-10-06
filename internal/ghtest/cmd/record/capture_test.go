package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/schuettc/muda/internal/gh"
)

// fullCaptureHandler returns a handler that serves a minimal valid fixture for
// every capture endpoint. Requests matching pathSuffix are served with the
// overrideCode instead.
func fullCaptureHandler(pathSuffix string, overrideCode int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if pathSuffix != "" && strings.HasSuffix(path, pathSuffix) {
			http.Error(w, `{"message":"error"}`, overrideCode)
			return
		}
		switch {
		case path == "/repos/o/r":
			_, _ = io.WriteString(w, `{"default_branch":"main"}`)
		case strings.HasSuffix(path, "/protection"):
			_, _ = io.WriteString(w, `{"required_status_checks":{"contexts":[]}}`)
		case strings.HasSuffix(path, "/rules/branches/main"):
			_, _ = io.WriteString(w, `[]`)
		case strings.HasSuffix(path, "/environments"):
			_, _ = io.WriteString(w, `{"environments":[]}`)
		case strings.HasSuffix(path, "/actions/workflows"):
			_, _ = io.WriteString(w, `{"workflows":[]}`)
		case strings.HasSuffix(path, "/.github/workflows"):
			_, _ = io.WriteString(w, `[]`)
		case strings.HasSuffix(path, "/actions/runs"):
			_, _ = io.WriteString(w, `{"workflow_runs":[]}`)
		default:
			http.Error(w, "unexpected: "+path, 500)
		}
	})
}

// TestCaptureBranchProtection404IsIgnored verifies that a 404 from
// BranchProtection is treated as "unavailable on public repo" and does not
// cause capture to fail.
func TestCaptureBranchProtection404IsIgnored(t *testing.T) {
	srv := httptest.NewServer(fullCaptureHandler("/protection", 404))
	defer srv.Close()

	c := gh.New(gh.Options{Token: "test", BaseURL: srv.URL, NoCache: true})
	err := capture(context.Background(), c, "o/r", time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("404 BranchProtection should be ignored, got: %v", err)
	}
}

// TestCaptureBranchProtectionNon404Fails verifies that a non-404 error from
// BranchProtection (e.g. 403, 500) is propagated rather than silently swallowed.
// M4 regression guard: only named 404 (unavailable) must be ignored.
func TestCaptureBranchProtectionNon404Fails(t *testing.T) {
	for _, code := range []int{403, 500} {
		t.Run(fmt.Sprintf("HTTP%d", code), func(t *testing.T) {
			// All endpoints succeed except /protection which returns code.
			// This isolates M4: the only error source is BranchProtection.
			srv := httptest.NewServer(fullCaptureHandler("/protection", code))
			defer srv.Close()

			c := gh.New(gh.Options{Token: "test", BaseURL: srv.URL, NoCache: true})
			err := capture(context.Background(), c, "o/r", time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))
			if err == nil {
				t.Fatalf("HTTP %d from BranchProtection must propagate as error, but got nil", code)
			}
		})
	}
}
