package record

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func hasProblem(ps []Problem, code, file, detail string) bool {
	for _, p := range ps {
		if p.Code == code && p.File == file && strings.Contains(p.Detail, detail) {
			return true
		}
	}
	return false
}

// Final review I4: unknown TOML keys fail check and block writes instead of
// being silently dropped.
func TestUnknownTOMLKeysAreProblems(t *testing.T) {
	for _, tc := range []struct {
		name, from, to, key string
	}{
		{"finding key", `status = "open"`, "status = \"open\"\nstatuz = \"fixed\"", "statuz"},
		{"nested evidence key", `kind = "file"`, "kind = \"file\"\nrunid = 7", "runid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := store(t)
			if _, err := s.AddFinding(finding()); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(s.Root, ".muda", "findings.toml")
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(b), tc.from) {
				t.Fatalf("fixture lacks %q:\n%s", tc.from, b)
			}
			edited := strings.Replace(string(b), tc.from, tc.to, 1)
			if err := os.WriteFile(path, []byte(edited), 0600); err != nil {
				t.Fatal(err)
			}
			if ps := s.Check(); !hasProblem(ps, "unknown-key", "findings.toml", tc.key) {
				t.Fatalf("check missed %s: %+v", tc.key, ps)
			}
			// A write on top refuses rather than silently dropping the key.
			status := Approved
			err = s.SetFinding("F-001", FindingPatch{Status: &status})
			var p Problem
			if !errors.As(err, &p) || p.Code != "unknown-key" {
				t.Fatalf("write on top of unknown key: %v", err)
			}
			after, _ := os.ReadFile(path)
			if string(after) != edited {
				t.Fatal("refused write changed the file")
			}
		})
	}
}

func TestUnknownReceiptFrontBlockKeyIsProblem(t *testing.T) {
	s := store(t)
	if _, err := s.AddFinding(finding()); err != nil {
		t.Fatal(err)
	}
	r := Receipt{PR: "https://github.com/o/r/pull/1", Before: "b", After: "a", RunLinks: []string{"https://github.com/o/r/actions/runs/1"}, GateStatus: GateVerified, GateResult: "ok"}
	if err := s.WriteReceipt("F-001", r); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.Root, ".muda", "receipts", "F-001.md")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(b), "gate-status = ", "gate-statuz = \"VERIFIED\"\ngate-status = ", 1)
	if edited == string(b) {
		t.Fatalf("fixture lacks gate-status:\n%s", b)
	}
	if err := os.WriteFile(path, []byte(edited), 0600); err != nil {
		t.Fatal(err)
	}
	if ps := s.Check(); !hasProblem(ps, "unknown-key", "receipts/F-001.md", "gate-statuz") {
		t.Fatalf("check missed receipt key: %+v", s.Check())
	}
	err = s.WriteReceipt("F-001", r)
	var p Problem
	if !errors.As(err, &p) || p.Code != "unknown-key" {
		t.Fatalf("receipt write on top of unknown key: %v", err)
	}
}

// Final review M1: evidence is written with snake_case keys matching JSON,
// without zero-valued optional keys, and round-trips.
func TestFindingEvidenceTOMLKeys(t *testing.T) {
	s := store(t)
	f := finding()
	f.Evidence = []Evidence{{Kind: "step", URL: "https://github.com/o/r/actions/runs/1/job/2", RunID: 1, JobID: 2, Step: "test", Note: "p50"}, {Kind: "file", Path: "ci.yml", Line: 3}}
	if _, err := s.AddFinding(f); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(s.Root, ".muda", "findings.toml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	for _, want := range []string{`kind = "step"`, `url = "https://github.com/o/r/actions/runs/1/job/2"`, "run_id = 1", "job_id = 2", `step = "test"`, `note = "p50"`, `path = "ci.yml"`, "line = 3"} {
		if !strings.Contains(text, want) {
			t.Errorf("findings.toml lacks %q:\n%s", want, text)
		}
	}
	for _, bad := range []string{"Kind", "URL =", "RunID", "JobID", "Step =", "Path =", "Line =", "Note =", "run_id = 0", "line = 0"} {
		if strings.Contains(text, bad) {
			t.Errorf("findings.toml has %q:\n%s", bad, text)
		}
	}
	snap, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	got := snap["findings"].([]Finding)
	if len(got) != 1 || !reflect.DeepEqual(got[0].Evidence, f.Evidence) {
		t.Fatalf("evidence did not round-trip: %+v", got)
	}
	if ps := s.Check(); len(ps) != 0 {
		t.Fatal(ps)
	}
}
