package record

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Release prep P4 (decision 4): muda.toml's optional record-pr setting holds
// the labels every record PR carries. It is set through the checked
// record baseline --settings path, validated, and absent from older records.

func recordPRSettings(t *testing.T, s *Store, body string) []byte {
	t.Helper()
	b := settingsJSON(t, "2026-09-01", "2026-09-15")
	return []byte(strings.TrimSuffix(string(b), "}") + `,"record-pr":` + body + `}`)
}

func TestRecordPRLabelsSetThroughBaselineSettings(t *testing.T) {
	s := store(t)
	in := BaselineSet{Measure: measureJSON("o/r", "2026-09-01", "2026-09-15"), Settings: recordPRSettings(t, s, `{"labels":["no-changelog","ci: skip-release"]}`)}
	if err := s.WriteBaselineSet(in); err != nil {
		t.Fatal(err)
	}
	var got Settings
	if err := s.load("muda.toml", &got); err != nil {
		t.Fatal(err)
	}
	if got.RecordPR == nil || !reflect.DeepEqual(got.RecordPR.Labels, []string{"no-changelog", "ci: skip-release"}) {
		t.Fatalf("record-pr %+v", got.RecordPR)
	}
	if ps := s.Check(); len(ps) != 0 {
		t.Fatal(ps)
	}
	snap, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if v := snap["settings"].(Settings); v.RecordPR == nil || len(v.RecordPR.Labels) != 2 {
		t.Fatalf("show settings %+v", v)
	}
}

func TestRecordPRLabelsValidated(t *testing.T) {
	for _, body := range []string{
		`{"labels":[""]}`,
		`{"labels":[" padded"]}`,
		`{"labels":["a,b"]}`,
		`{"labels":["dup","dup"]}`,
	} {
		s := store(t)
		before := snapshotFiles(t, s)
		err := s.WriteBaselineSet(BaselineSet{Measure: measureJSON("o/r", "2026-09-01", "2026-09-15"), Settings: recordPRSettings(t, s, body)})
		if !hasCode(err, "invalid-record-pr") {
			t.Fatalf("%s: got %v", body, err)
		}
		after := snapshotFiles(t, s)
		for _, n := range setFiles {
			if string(before[n]) != string(after[n]) {
				t.Fatalf("%s: %s changed", body, n)
			}
		}
	}
	// Only labels: a misspelled or unsupported key is refused, never dropped.
	for _, body := range []string{`{"label":["x"]}`, `{"labels":["x"],"title":"chore:"}`, `{"labels":"x"}`} {
		s := store(t)
		if err := s.WriteBaselineSet(BaselineSet{Measure: measureJSON("o/r", "2026-09-01", "2026-09-15"), Settings: recordPRSettings(t, s, body)}); !hasCode(err, "invalid-json") {
			t.Fatalf("%s: got %v", body, err)
		}
	}
	// A hand-edited muda.toml with a bad label fails check.
	s := store(t)
	path := filepath.Join(s.Root, ".muda", "muda.toml")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(b, []byte("\n[record-pr]\nlabels = [\"\"]\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	if ps := s.Check(); !hasProblem(ps, "invalid-record-pr", "muda.toml", "") {
		t.Fatalf("check missed the empty label: %+v", ps)
	}
}

// A record from before record-pr existed loads and checks clean, and a
// write without it adds no record-pr table.
func TestRecordPRAbsentInOlderRecords(t *testing.T) {
	s := store(t)
	b, err := os.ReadFile(filepath.Join(s.Root, ".muda", "muda.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "record-pr") {
		t.Fatalf("record-pr written without being set:\n%s", b)
	}
	if ps := s.Check(); len(ps) != 0 {
		t.Fatal(ps)
	}
	snap, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if v := snap["settings"].(Settings); v.RecordPR != nil {
		t.Fatalf("record-pr %+v", v.RecordPR)
	}
}
