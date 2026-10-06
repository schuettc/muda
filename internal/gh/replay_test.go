package gh

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/schuettc/muda/internal/ghtest"
)

// This public snapshot exercises GitHub's real envelopes, pagination and
// attempt/job shapes without calling the network in tests.
func TestTackleRecordedSnapshot(t *testing.T) {
	c := New(Options{BaseURL: "https://api.github.com", NoCache: true,
		HTTP: &http.Client{Transport: ghtest.Replay(filepath.Join("..", "ghtest", "testdata", "tackle"))}})
	ctx := context.Background()
	runs, err := c.Runs(ctx, "schuettc/tackle", time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))
	if err != nil || len(runs) != 138 {
		t.Fatalf("snapshot runs=%d err=%v", len(runs), err)
	}
	if runs[0].ID == 0 || runs[0].HTMLURL == "" {
		t.Fatalf("first real run missing id/url: %+v", runs[0])
	}
	attempts, err := c.RunAttempts(ctx, "schuettc/tackle", runs[0])
	if err != nil || len(attempts) != runs[0].RunAttempt {
		t.Fatalf("attempts=%+v err=%v", attempts, err)
	}
	jobs, err := c.Jobs(ctx, "schuettc/tackle", runs[0].ID, 1)
	if err != nil || len(jobs) == 0 || jobs[0].Name == "" {
		t.Fatalf("jobs=%+v err=%v", jobs, err)
	}
	logText, err := c.JobLog(ctx, "schuettc/tackle", 109536828086)
	if err != nil || len(logText) == 0 {
		t.Fatalf("recorded log unavailable: len=%d err=%v", len(logText), err)
	}
	files, err := c.Dir(ctx, "schuettc/tackle", "main", ".github/workflows")
	if err != nil || len(files) != 2 {
		t.Fatalf("workflow files=%v err=%v", files, err)
	}
	data, found, err := c.File(ctx, "schuettc/tackle", "main", files[0])
	if err != nil || !found || len(data) == 0 {
		t.Fatalf("file found=%v bytes=%d err=%v", found, len(data), err)
	}
}
