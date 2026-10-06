package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/schuettc/muda/internal/record"
)

func TestConcurrentRecordCLIProcesses(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "muda")
	build := exec.Command("go", "build", "-o", binary, ".")
	if b, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build %v: %s", err, b)
	}
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	s := &record.Store{Root: root}
	if err := s.Init(record.Settings{Roles: map[string]string{"ci.yml": "verify"}, Stacks: []string{"go"}, Window: record.Window{Since: "2026-09-01", Until: "2026-09-15"}, MudaVersion: "dev"}); err != nil {
		t.Fatal(err)
	}
	type request struct {
		args  []string
		input string
	}
	type result struct {
		out  string
		errs string
		err  error
	}
	run := func(requests []request) []result {
		t.Helper()
		results := make([]result, len(requests))
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i, req := range requests {
			wg.Go(func() {
				<-start
				cmd := exec.Command(binary, append([]string{"record"}, req.args...)...)
				cmd.Dir = root
				cmd.Stdin = strings.NewReader(req.input)
				var out, errs bytes.Buffer
				cmd.Stdout = &out
				cmd.Stderr = &errs
				err := cmd.Run()
				results[i] = result{out.String(), errs.String(), err}
			})
		}
		close(start)
		wg.Wait()
		for i, r := range results {
			if r.err != nil {
				var cmdErr *exec.ExitError
				if !errors.As(r.err, &cmdErr) || cmdErr.ExitCode() != 1 || !strings.Contains(r.errs, "record-busy") {
					t.Fatalf("request %d: %v %s", i, r.err, r.errs)
				}
			}
		}
		return results
	}
	const count = 40
	requests := make([]request, count)
	for i := range requests {
		requests[i] = request{[]string{"finding", "add"}, fmt.Sprintf(`{"waste":"waiting","location":"workflow:ci.yml/job:test","recipe":"slow-step","evidence":[{"kind":"file","path":"ci.yml"}],"basis":"suspected","estimate":"fact-%d","risk":"low"}`, i)}
	}
	results := run(requests)
	successes := map[string]string{}
	for i, r := range results {
		if r.err == nil {
			var v struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal([]byte(r.out), &v); err != nil {
				t.Fatal(err)
			}
			if _, ok := successes[v.ID]; ok {
				t.Fatalf("duplicate successful ID %s", v.ID)
			}
			successes[v.ID] = fmt.Sprintf("fact-%d", i)
		}
	}
	if len(successes) == 0 {
		t.Fatal("no add succeeded")
	}
	snapshot, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	fs := snapshot["findings"].([]record.Finding)
	if len(fs) != len(successes) {
		t.Fatalf("%d successful adds, %d persisted", len(successes), len(fs))
	}
	t.Logf("add processes: %d succeeded, %d busy, %d persisted", len(successes), count-len(successes), len(fs))
	for i, f := range fs {
		if f.ID != fmt.Sprintf("F-%03d", i+1) {
			t.Fatalf("nonsequential successful IDs: %s", f.ID)
		}
		if f.Estimate != successes[f.ID] {
			t.Fatalf("lost fact %+v", f)
		}
	}
	// Multiple independent updates share findings.toml: each successful patch
	// must survive, not just the last process's projection of the file.
	for len(fs) < count {
		if _, err := s.AddFinding(record.Finding{Waste: "waiting", Location: "workflow:ci.yml/job:test", Recipe: "slow-step", Evidence: []record.Evidence{{Kind: "file", Path: "ci.yml"}}, Basis: "suspected", Estimate: "seed", Risk: "low"}); err != nil {
			t.Fatal(err)
		}
		snapshot, err = s.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		fs = snapshot["findings"].([]record.Finding)
	}
	for i := range requests {
		requests[i] = request{[]string{"finding", "set", fs[i].ID}, fmt.Sprintf(`{"pr":"patch-%d"}`, i)}
	}
	results = run(requests)
	snapshot, err = s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	patched := snapshot["findings"].([]record.Finding)
	successCount := 0
	persistCount := 0
	for i, r := range results {
		if r.err == nil {
			successCount++
			if patched[i].PR != fmt.Sprintf("patch-%d", i) {
				t.Fatalf("successful patch %d lost", i)
			}
		}
	}
	for _, f := range patched {
		if f.PR != "" {
			persistCount++
		}
	}
	if successCount == 0 || successCount != persistCount {
		t.Fatalf("patches: %d successes, %d persisted", successCount, persistCount)
	}
	t.Logf("set processes: %d succeeded, %d busy, %d persisted", successCount, count-successCount, persistCount)
	for i := range requests {
		requests[i] = request{[]string{"exempt"}, fmt.Sprintf(`{"id":"E-%03d","waste":"waiting","location":"workflow:ci.yml/job:test","reason":"fact-%d","agreed-by":"owner","date":"2026-09-01","review-by":"2099-01-01"}`, i, i)}
	}
	results = run(requests)
	snapshot, err = s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	es := snapshot["exemptions"].([]record.Exemption)
	expected := map[string]string{}
	for i, r := range results {
		if r.err == nil {
			expected[fmt.Sprintf("E-%03d", i)] = fmt.Sprintf("fact-%d", i)
		}
	}
	if len(expected) == 0 || len(expected) != len(es) {
		t.Fatalf("exemptions: %d successes, %d persisted", len(expected), len(es))
	}
	t.Logf("exempt processes: %d succeeded, %d busy, %d persisted", len(expected), count-len(expected), len(es))
	for _, e := range es {
		if e.Reason != expected[e.ID] {
			t.Fatalf("lost exemption %+v", e)
		}
	}
	if _, err := os.Lstat(filepath.Join(root, ".muda", ".write-lock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("writer lock leaked: %v", err)
	}
}
