package measure

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/schuettc/muda/internal/gh"
	"github.com/schuettc/muda/internal/signal"
)

const testRepo = "o/r"

// serverError, as a fakeGitHub file, answers that read with HTTP 500.
const serverError = "\x00500"

var t0 = time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

func mkRun(id int64, path, event, branch, tree, conclusion string, attempt int, start time.Time, dur time.Duration) gh.Run {
	r := gh.Run{ID: id, Name: path, Path: path, Event: event, HeadBranch: branch, HeadSHA: fmt.Sprintf("sha%d", id),
		Status: "completed", Conclusion: conclusion, RunAttempt: attempt, CreatedAt: start, RunStartedAt: start,
		UpdatedAt: start.Add(dur), HTMLURL: fmt.Sprintf("https://github.com/o/r/actions/runs/%d", id)}
	r.HeadCommit.TreeID = tree
	return r
}

func mkJob(run gh.Run, id int64, name, conclusion string, start time.Time, queue, dur time.Duration, steps ...gh.Step) gh.Job {
	if steps == nil {
		steps = []gh.Step{}
	}
	j := gh.Job{ID: id, RunID: run.ID, RunAttempt: run.RunAttempt, Name: name, Status: "completed", Conclusion: conclusion,
		CreatedAt: start.Add(-queue), StartedAt: start, CompletedAt: start.Add(dur), Steps: steps,
		HTMLURL: fmt.Sprintf("%s/job/%d", run.HTMLURL, id)}
	if conclusion == "skipped" {
		j.CreatedAt, j.StartedAt, j.CompletedAt = start, start, start
	}
	return j
}

// mkSteps builds consecutive executed steps with the given names, each 1m.
func mkSteps(start time.Time, names ...string) []gh.Step {
	steps := make([]gh.Step, 0, len(names))
	for i, n := range names {
		s := start.Add(time.Duration(i) * time.Minute)
		steps = append(steps, gh.Step{Name: n, Number: i + 1, Status: "completed", Conclusion: "success", StartedAt: s, CompletedAt: s.Add(time.Minute)})
	}
	return steps
}

// fakeGitHub serves the REST endpoints measure uses from in-memory data and
// counts the requests it answered.
type fakeGitHub struct {
	mu       sync.Mutex
	runs     []gh.Run
	attempts map[int64][]gh.Attempt
	jobs     map[string][]gh.Job // "<run>/<attempt>"
	files    map[string]string   // "<ref>:<path>"
	hits     map[string]int
}

var (
	reAttempt = regexp.MustCompile(`^/repos/o/r/actions/runs/(\d+)/attempts/(\d+)$`)
	reJobs    = regexp.MustCompile(`^/repos/o/r/actions/runs/(\d+)/attempts/(\d+)/jobs$`)
	reFile    = regexp.MustCompile(`^/repos/o/r/contents/(.+)$`)
)

func (f *fakeGitHub) RoundTrip(req *http.Request) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.hits == nil {
		f.hits = map[string]int{}
	}
	p := req.URL.Path
	f.hits[p]++
	var body any
	switch {
	case p == "/repos/o/r":
		body = map[string]any{"default_branch": "main"}
	case p == "/repos/o/r/actions/workflows":
		body = map[string]any{"workflows": []any{}}
	case p == "/repos/o/r/actions/runs":
		body = map[string]any{"total_count": len(f.runs), "workflow_runs": f.runs}
	case reJobs.MatchString(p):
		m := reJobs.FindStringSubmatch(p)
		jobs, ok := f.jobs[m[1]+"/"+m[2]]
		if !ok {
			return respond(req, 404, `{"message":"Not Found"}`), nil
		}
		body = map[string]any{"jobs": jobs}
	case reAttempt.MatchString(p):
		m := reAttempt.FindStringSubmatch(p)
		id, _ := strconv.ParseInt(m[1], 10, 64)
		n, _ := strconv.Atoi(m[2])
		if n < 1 || n > len(f.attempts[id]) {
			return respond(req, 404, `{"message":"Not Found"}`), nil
		}
		body = f.attempts[id][n-1]
	case reFile.MatchString(p):
		key := req.URL.Query().Get("ref") + ":" + reFile.FindStringSubmatch(p)[1]
		content, ok := f.files[key]
		if !ok {
			return respond(req, 404, `{"message":"Not Found"}`), nil
		}
		if content == serverError {
			return respond(req, 500, `{"message":"Server Error"}`), nil
		}
		body = map[string]string{"type": "file", "encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte(content))}
	default:
		return respond(req, 404, `{"message":"Not Found"}`), nil
	}
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return respond(req, 200, string(b)), nil
}

func respond(req *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Status: http.StatusText(status), Header: http.Header{},
		Body: io.NopCloser(bytes.NewReader([]byte(body))), Request: req}
}

func (f *fakeGitHub) client() *gh.Client {
	return gh.New(gh.Options{BaseURL: "https://api.github.com", NoCache: true, HTTP: &http.Client{Transport: f}})
}

func (f *fakeGitHub) addRun(r gh.Run, jobs ...gh.Job) {
	f.runs = append(f.runs, r)
	if f.jobs == nil {
		f.jobs = map[string][]gh.Job{}
	}
	f.jobs[fmt.Sprintf("%d/%d", r.ID, r.RunAttempt)] = jobs
}

func findWorkflow(t *testing.T, r *Report, path string) WorkflowStats {
	t.Helper()
	for _, w := range r.Workflows {
		if w.Path == path {
			return w
		}
	}
	t.Fatalf("workflow %s not in report", path)
	return WorkflowStats{}
}

func findJob(t *testing.T, w WorkflowStats, name string) JobStats {
	t.Helper()
	for _, j := range w.Jobs {
		if j.Name == name {
			return j
		}
	}
	t.Fatalf("job %s not in %s", name, w.Path)
	return JobStats{}
}

func signalsWithID(r *Report, id string) []signal.Signal {
	var out []signal.Signal
	for _, s := range r.Signals {
		if s.ID == id {
			out = append(out, s)
		}
	}
	return out
}
