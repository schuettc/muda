package notices

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/schuettc/muda/internal/gh"
	"github.com/schuettc/muda/internal/ghtest"
	"github.com/schuettc/muda/internal/signal"
)

var update = flag.Bool("update", false, "rewrite golden files")

// fixtureUntil is the recorded snapshot's clock: every recorded run precedes it.
var fixtureUntil = time.Date(2026, 9, 29, 18, 0, 0, 0, time.UTC)

func client(rt http.RoundTripper) *gh.Client {
	return gh.New(gh.Options{Token: "t", BaseURL: "https://api.github.com", NoCache: true, HTTP: &http.Client{Transport: rt}})
}

// noticesAPI: run 1 (one attempt) carries a warning, a notice and a failure;
// run 2 has two attempts, the first repeating the warning with bumped action
// majors and the second's annotations forbidden; run 3's jobs are not found.
func noticesAPI() fakeAPI {
	return fakeAPI{
		"/repos/o/r/actions/runs": {body: `{"total_count":3,"workflow_runs":[
			{"id":3,"name":"Nightly","path":".github/workflows/n.yml","status":"completed","run_attempt":1,"created_at":"2026-09-27T00:00:00Z","updated_at":"2026-09-27T00:10:00Z","html_url":"https://github.com/o/r/actions/runs/3"},
			{"id":2,"name":"Deploy","path":".github/workflows/d.yml","status":"completed","run_attempt":2,"created_at":"2026-09-26T00:00:00Z","updated_at":"2026-09-26T01:00:00Z","html_url":"https://github.com/o/r/actions/runs/2"},
			{"id":1,"name":"CI","path":".github/workflows/ci.yml","status":"completed","run_attempt":1,"created_at":"2026-09-20T00:00:00Z","updated_at":"2026-09-20T00:10:00Z","html_url":"https://github.com/o/r/actions/runs/1"}]}`},
		"/repos/o/r/actions/runs/1/attempts/1/jobs": {body: `{"total_count":1,"jobs":[{"id":11,"run_id":1,"run_attempt":1,"name":"test","status":"completed","started_at":"2026-09-20T00:01:00Z","completed_at":"2026-09-20T00:05:00Z","check_run_url":"https://api.github.com/repos/o/r/check-runs/11","html_url":"https://github.com/o/r/actions/runs/1/job/11"}]}`},
		"/repos/o/r/actions/runs/2/attempts/1/jobs": {body: `{"total_count":1,"jobs":[{"id":21,"run_id":2,"run_attempt":1,"name":"deploy","status":"completed","started_at":"2026-09-26T00:01:00Z","completed_at":"2026-09-26T00:20:00Z","check_run_url":"https://api.github.com/repos/o/r/check-runs/21","html_url":"https://github.com/o/r/actions/runs/2/job/21"}]}`},
		"/repos/o/r/actions/runs/2/attempts/2/jobs": {body: `{"total_count":1,"jobs":[{"id":22,"run_id":2,"run_attempt":2,"name":"deploy","status":"completed","started_at":"2026-09-26T00:30:00Z","completed_at":"2026-09-26T00:50:00Z","check_run_url":"https://api.github.com/repos/o/r/check-runs/22","html_url":"https://github.com/o/r/actions/runs/2/job/22"}]}`},
		"/repos/o/r/actions/runs/3/attempts/1/jobs": {status: 404, body: `{"message":"Not Found"}`},
		"/repos/o/r/check-runs/11":                  {body: `{"status":"completed"}`},
		"/repos/o/r/check-runs/21":                  {body: `{"status":"completed"}`},
		"/repos/o/r/check-runs/22":                  {body: `{"status":"completed"}`},
		"/repos/o/r/check-runs/11/annotations": {body: `[
			{"path":".github","start_line":1,"annotation_level":"warning","message":` + quote(node20A) + `},
			{"path":".github/workflows/ci.yml","start_line":12,"annotation_level":"failure","message":"boom"},
			{"path":".github","start_line":1,"annotation_level":"notice","message":` + quote(ubuntu) + `}]`},
		"/repos/o/r/check-runs/21/annotations": {body: `[{"path":".github","start_line":1,"annotation_level":"warning","message":` + quote(node20B) + `}]`},
		"/repos/o/r/check-runs/22/annotations": {status: 403, body: `{"message":"Resource not accessible by integration"}`},
	}
}

func quote(s string) string {
	var b bytes.Buffer
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func TestNoticesGroup(t *testing.T) {
	groups, sigs, err := Run(context.Background(), client(noticesAPI()), "o/r", time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), fixtureUntil)
	if err != nil {
		t.Fatal(err)
	}
	var seen, missing []Group
	for _, g := range groups {
		if g.Level == LevelUnavailable {
			missing = append(missing, g)
		} else {
			seen = append(seen, g)
		}
		if g.Count != len(g.Sources) {
			t.Errorf("%q: count %d but %d sources; every observation must be cited", g.Signature, g.Count, len(g.Sources))
		}
		for _, s := range g.Sources {
			if s.URL == "" {
				t.Errorf("%q: source without URL: %+v", g.Signature, s)
			}
		}
	}
	if len(seen) != 2 {
		t.Fatalf("want node20 warning and ubuntu notice groups, got %+v", seen)
	}
	for i := 1; i < len(seen); i++ {
		if seen[i-1].Signature >= seen[i].Signature {
			t.Errorf("groups not sorted by signature: %q, %q", seen[i-1].Signature, seen[i].Signature)
		}
	}
	var node, notice Group
	for _, g := range seen {
		switch g.Signature {
		case Signature(node20A):
			node = g
		case Signature(ubuntu):
			notice = g
		default:
			t.Errorf("unexpected group %q (failure-level annotations must be ignored)", g.Signature)
		}
	}
	if node.Count != 2 || node.Level != "warning" || node.Example != node20A {
		t.Errorf("node20 group: %+v", node)
	}
	if !node.FirstSeen.Equal(time.Date(2026, 9, 20, 0, 5, 0, 0, time.UTC)) || !node.LastSeen.Equal(time.Date(2026, 9, 26, 0, 20, 0, 0, time.UTC)) {
		t.Errorf("node20 first/last %v %v", node.FirstSeen, node.LastSeen)
	}
	if node.Sources[0].RunID != 1 || node.Sources[0].JobID != 11 || node.Sources[1].RunID != 2 || node.Sources[1].JobID != 21 ||
		node.Sources[0].Kind != signal.KindAnnotation || node.Sources[0].URL != "https://github.com/o/r/actions/runs/1/job/11" {
		t.Errorf("node20 sources: %+v", node.Sources)
	}
	if notice.Count != 1 || notice.Level != "notice" {
		t.Errorf("ubuntu group: %+v", notice)
	}

	// Unreadable sources are named, counted and cited; never a silent zero.
	if len(missing) != 2 {
		t.Fatalf("want annotations and jobs-listing unavailable groups, got %+v", missing)
	}
	for _, g := range missing {
		switch {
		case strings.Contains(g.Signature, "annotations unavailable") && strings.Contains(g.Signature, "HTTP 403"):
			if g.Sources[0].JobID != 22 {
				t.Errorf("403 group should cite job 22: %+v", g.Sources)
			}
		case strings.Contains(g.Signature, "jobs unavailable") && strings.Contains(g.Signature, "HTTP 404"):
			if g.Sources[0].RunID != 3 || g.Sources[0].URL != "https://github.com/o/r/actions/runs/3" {
				t.Errorf("404 group should cite run 3: %+v", g.Sources)
			}
		default:
			t.Errorf("unexpected unavailable group %+v", g)
		}
	}

	if len(sigs) != 2 {
		t.Fatalf("want one advance-notice per seen signature, got %+v", sigs)
	}
	for _, s := range sigs {
		if s.ID != "advance-notice" || len(s.Waste) != 1 || s.Waste[0] != "W10" || len(s.Evidence) == 0 {
			t.Errorf("signal %+v", s)
		}
		want := signal.SeverityInfo
		if strings.HasPrefix(s.Summary, "warning") {
			want = signal.SeverityWarn
		}
		if s.Severity != want {
			t.Errorf("severity %s for %q", s.Severity, s.Summary)
		}
	}
}

func TestNoticesTransportErrorIsFatal(t *testing.T) {
	api := noticesAPI()
	api["/repos/o/r/check-runs/11/annotations"] = fakeResp{err: errors.New("connection reset")}
	if _, _, err := Run(context.Background(), client(api), "o/r", time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), fixtureUntil); err == nil || !strings.Contains(err.Error(), "connection reset") {
		t.Fatalf("a non-API failure must not become an empty result: %v", err)
	}
	api = noticesAPI()
	api["/repos/o/r/actions/runs"] = fakeResp{status: 404, body: `{}`}
	if _, _, err := Run(context.Background(), client(api), "o/r", time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), fixtureUntil); err == nil || !strings.Contains(err.Error(), "/repos/o/r/actions/runs") {
		t.Fatalf("a failed run listing names the endpoint: %v", err)
	}
}

// TestNoticesTackleFixture pins the report over the recorded public snapshot.
// Regenerate with -update and review the golden by hand.
func TestNoticesTackleFixture(t *testing.T) {
	c := client(ghtest.Replay(filepath.Join("..", "ghtest", "testdata", "tackle")))
	since := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	groups, sigs, err := Run(context.Background(), c, "schuettc/tackle", since, fixtureUntil)
	if err != nil {
		t.Fatal(err)
	}
	rep := NewReport("schuettc/tackle", since, fixtureUntil, groups, sigs)
	var got bytes.Buffer
	if err := WriteJSON(&got, rep); err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "tackle.notices.json")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, got.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Bytes(), want) {
		t.Fatal("notices JSON differs from golden (run with -update and review)")
	}
	if len(rep.Groups) == 0 || len(rep.Signals) != len(rep.Groups) {
		t.Fatalf("tackle has advance notices: groups=%d signals=%d", len(rep.Groups), len(rep.Signals))
	}
	for _, g := range rep.Groups {
		if g.Level == "failure" {
			t.Errorf("failure annotation grouped: %+v", g)
		}
	}
	md := RenderMarkdown(rep)
	for _, g := range append(append([]Group{}, rep.Groups...), rep.Unavailable...) {
		for _, s := range g.Sources {
			if !strings.Contains(md, s.URL) {
				t.Errorf("markdown lacks source %s", s.URL)
			}
		}
	}
	requireNoEmptySections(t, md)
	if !strings.Contains(string(want), `"schema": 1`) || strings.Contains(string(want), "null") {
		t.Error("JSON must carry schema 1 and no null arrays")
	}
}

func TestNoticesSameSignatureAcrossLevels(t *testing.T) {
	api := noticesAPI()
	api["/repos/o/r/check-runs/21/annotations"] = fakeResp{body: `[{"annotation_level":"notice","message":` + quote(node20B) + `}]`}
	groups, sigs, err := Run(context.Background(), client(api), "o/r", time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), fixtureUntil)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, g := range groups {
		if g.Signature == Signature(node20A) {
			count++
			if g.Count != 2 || g.Level != "warning" {
				t.Errorf("%+v", g)
			}
		}
	}
	if count != 1 || len(sigs) != 2 {
		t.Fatalf("count=%d signals=%d", count, len(sigs))
	}
}

func TestAttemptDedupOccurrenceAndOriginalTime(t *testing.T) {
	api := noticesAPI()
	api["/repos/o/r/actions/runs/2/attempts/2/jobs"] = fakeResp{body: `{"jobs":[{"id":99,"run_id":2,"run_attempt":2,"completed_at":"2026-09-26T00:50:00Z","check_run_url":"https://api.github.com/repos/o/r/check-runs/21"}]}`}
	api["/repos/o/r/check-runs/21/annotations"] = fakeResp{body: `[{"annotation_level":"warning","message":` + quote(node20B) + `},{"annotation_level":"warning","message":` + quote(node20B) + `}]`}
	groups, _, err := Run(context.Background(), client(api), "o/r", time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), fixtureUntil)
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range groups {
		if g.Signature == Signature(node20A) {
			if g.Count != 3 || !g.LastSeen.Equal(time.Date(2026, 9, 26, 0, 20, 0, 0, time.UTC)) {
				t.Fatalf("carryover inflated: %+v", g)
			}
			for _, e := range g.Sources {
				if e.JobID == 99 {
					t.Fatal("carryover source counted")
				}
			}
			return
		}
	}
	t.Fatal("missing warning")
}
func TestAttemptMismatchAndMissingTimeUnavailable(t *testing.T) {
	for _, job := range []string{`{"id":11,"run_id":1,"run_attempt":2,"completed_at":"2026-09-20T00:05:00Z","check_run_url":"https://api.github.com/repos/o/r/check-runs/11"}`, `{"id":11,"run_id":1,"run_attempt":1,"check_run_url":"https://api.github.com/repos/o/r/check-runs/11"}`} {
		api := noticesAPI()
		api["/repos/o/r/actions/runs/1/attempts/1/jobs"] = fakeResp{body: `{"jobs":[` + job + `]}`}
		groups, _, err := Run(context.Background(), client(api), "o/r", time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), fixtureUntil)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, g := range groups {
			for _, e := range g.Sources {
				if e.JobID == 11 {
					if g.Level != LevelUnavailable {
						t.Fatal("unverified observation counted", g)
					}
					if !g.FirstSeen.IsZero() || !g.LastSeen.IsZero() {
						t.Fatal("unavailable time fabricated", g)
					}
					found = true
				}
			}
		}
		if !found {
			t.Fatal("missing unavailable job evidence")
		}
	}
}

func TestMarkdownObservationTimesAndUnavailableTimes(t *testing.T) {
	groups, sigs, err := Run(context.Background(), client(noticesAPI()), "o/r", time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), fixtureUntil)
	if err != nil {
		t.Fatal(err)
	}
	rep := NewReport("o/r", time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), fixtureUntil, groups, sigs)
	md := RenderMarkdown(rep)
	for _, g := range rep.Groups {
		for _, stamp := range []time.Time{g.FirstSeen, g.LastSeen} {
			if !strings.Contains(md, stamp.Format(time.RFC3339)) {
				t.Error("observation time lost", stamp)
			}
		}
	}
	if strings.Contains(md, "0001-01-01") {
		t.Fatal("fabricated year-one timestamp")
	}
	var b bytes.Buffer
	if err := WriteJSON(&b, rep); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "0001-01-01") {
		t.Fatal("unavailable JSON fabricates timestamp")
	}
}

// TestRunUntilIsExclusive: annotations of a run created at --until are not
// counted; a run created just before it is. The recorded listing is GitHub's
// inclusive range for either bound.
func TestRunUntilIsExclusive(t *testing.T) {
	c := client(ghtest.Replay(filepath.Join("..", "ghtest", "testdata", "tackle")))
	since := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	bound := time.Date(2026, 9, 29, 17, 39, 43, 0, time.UTC)
	const run = int64(36606519472)
	cites := func(until time.Time) bool {
		t.Helper()
		groups, _, err := Run(context.Background(), c, "schuettc/tackle", since, until)
		if err != nil {
			t.Fatal(err)
		}
		for _, g := range groups {
			for _, e := range g.Sources {
				if e.RunID == run {
					return true
				}
			}
		}
		return false
	}
	if cites(bound) {
		t.Fatalf("run %d created at --until was counted", run)
	}
	if !cites(bound.Add(time.Second)) {
		t.Fatalf("run %d created before --until was not counted", run)
	}
}

// A report states its whole [since, until) window, in JSON and in the
// Markdown header, so a past-window report cannot read as running to now.
func TestReportStatesWindow(t *testing.T) {
	since := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	rep := NewReport("o/r", since, until, nil, nil)
	var b bytes.Buffer
	if err := WriteJSON(&b, rep); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"since": "2026-09-01T00:00:00Z"`, `"until": "2026-09-15T00:00:00Z"`} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("JSON lacks %s: %s", want, b.String())
		}
	}
	header := strings.SplitN(RenderMarkdown(rep), "\n## ", 2)[0]
	if !strings.Contains(header, "Window: [2026-09-01T00:00:00Z, 2026-09-15T00:00:00Z).") {
		t.Errorf("Markdown header lacks the window: %q", header)
	}
}
