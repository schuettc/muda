package record

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func measureJSON(repo, since, until string) []byte {
	return []byte(`{"schema":1,"repo":"` + repo + `","since":"` + since + `","until":"` + until + `","workflows":[],"sinks":[],"signals":[],"unavailable":[]}`)
}
func gatesJSON(repo string) []byte {
	return []byte(`{"schema":1,"repo":"` + repo + `","branch":"main","security":[],"workflows":[],"gates":[],"unavailable":[],"rulesets":[],"environments":[]}`)
}
func scanJSON(repo string) []byte {
	return []byte(`{"schema":1,"repo":"` + repo + `","ref":"main","files":[".github/workflows/ci.yml"],"signals":[]}`)
}
func settingsJSON(t *testing.T, since, until string) []byte {
	t.Helper()
	v := settings()
	v.Window = Window{Since: since, Until: until}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

var setFiles = []string{"muda.toml", "gates.toml", "baseline.json", "scan.json", ".baseline-set-pending"}

// snapshotFiles returns each set file's bytes; an absent file maps to nil.
func snapshotFiles(t *testing.T, s *Store) map[string][]byte {
	t.Helper()
	m := map[string][]byte{}
	for _, n := range setFiles {
		b, err := os.ReadFile(filepath.Join(s.Root, ".muda", n))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		m[n] = b
	}
	return m
}

func storeWindow(t *testing.T, since, until string) *Store {
	t.Helper()
	s := &Store{Root: t.TempDir()}
	v := settings()
	v.Window = Window{Since: since, Until: until}
	if err := s.Init(v); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestBaselineSetWritesAll(t *testing.T) {
	s := store(t)
	before := snapshotFiles(t, s)
	since, until := "2026-09-16T00:00:00Z", "2026-09-30T12:00:00Z"
	in := BaselineSet{Measure: measureJSON("o/r", since, until), Settings: settingsJSON(t, since, until), Gates: gatesJSON("o/r"), Scan: scanJSON("o/r")}
	if err := s.WriteBaselineSet(in); err != nil {
		t.Fatal(err)
	}
	after := snapshotFiles(t, s)
	for _, n := range setFiles[:4] {
		if bytes.Equal(before[n], after[n]) {
			t.Fatalf("%s not written", n)
		}
	}
	if !bytes.Equal(after["baseline.json"], in.Measure) || !bytes.Equal(after["scan.json"], in.Scan) {
		t.Fatal("reports not stored whole")
	}
	if g, err := s.GatesJSON(); err != nil || !bytes.Equal(g, in.Gates) {
		t.Fatalf("gates %s %v", g, err)
	}
	var got Settings
	if err := s.load("muda.toml", &got); err != nil || got.Window.Since != since || got.Window.Until != until {
		t.Fatalf("settings %+v %v", got, err)
	}
	if ps := s.Check(); len(ps) != 0 {
		t.Fatal(ps)
	}
	if _, err := os.Lstat(filepath.Join(s.Root, ".muda", pendingName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("marker left after success: %v", err)
	}
}

func TestBaselineSetInvalidInputWritesNothing(t *testing.T) {
	s := store(t)
	since, until := "2026-09-01T00:00:00Z", "2026-09-15T00:00:00Z"
	if err := s.WriteBaselineSet(BaselineSet{Measure: measureJSON("o/r", since, until), Scan: scanJSON("o/r")}); err != nil {
		t.Fatal(err)
	}
	before := snapshotFiles(t, s)
	since, until = "2026-09-16T00:00:00Z", "2026-09-30T00:00:00Z"
	err := s.WriteBaselineSet(BaselineSet{Measure: measureJSON("o/r", since, until), Settings: settingsJSON(t, since, until), Gates: gatesJSON("o/r"), Scan: measureJSON("o/r", since, until)})
	if !hasCode(err, "wrong-report-type") || !strings.Contains(err.Error(), "--scan") {
		t.Fatalf("got %v", err)
	}
	after := snapshotFiles(t, s)
	for _, n := range setFiles {
		if !bytes.Equal(before[n], after[n]) {
			t.Fatalf("%s changed", n)
		}
	}
	// Every input is typed by shape, naming the flag it came from.
	for _, tc := range []struct {
		in   BaselineSet
		flag string
	}{
		{BaselineSet{Measure: gatesJSON("o/r")}, "--file"},
		{BaselineSet{Measure: []byte(`{"schema":1,"repo":"o/r"}`)}, "--file"},
		{BaselineSet{Measure: measureJSON("o/r", since, until), Settings: settingsJSON(t, since, until), Gates: scanJSON("o/r")}, "--gates"},
		{BaselineSet{Measure: measureJSON("o/r", since, until), Settings: settingsJSON(t, since, until), Scan: []byte(`{"schema":1}`)}, "--scan"},
		{BaselineSet{Measure: measureJSON("o/r", since, until), Settings: measureJSON("o/r", since, until)}, "--settings"},
	} {
		err := s.WriteBaselineSet(tc.in)
		if !hasCode(err, "wrong-report-type") || !strings.Contains(err.Error(), tc.flag) {
			t.Fatalf("%s: %v", tc.flag, err)
		}
	}
	for _, tc := range []struct {
		in         BaselineSet
		code, want string
	}{
		{BaselineSet{}, "missing-baseline", ""},
		{BaselineSet{Measure: measureJSON("o/r", since, until), Settings: settingsJSON(t, until, since)}, "invalid-window", ""},
		{BaselineSet{Measure: measureJSON("o/r", since, until), Settings: settingsJSON(t, since, until), Scan: []byte(`{"schema":1,"repo":"o/r","files":[],"signals":[],"surprise":true}`)}, "invalid-json", "--scan: json: unknown field \"surprise\""},
		{BaselineSet{Measure: measureJSON("o/r", since, until), Settings: settingsJSON(t, since, until), Scan: []byte(`{"schema":1,"repo":"o/r","files":["password: 'hunter22'"],"signals":[]}`)}, "secret-detected", ""},
		// A measure input must carry non-empty string repo, since and until.
		{BaselineSet{Measure: measureJSON("", since, until), Settings: settingsJSON(t, since, until)}, "invalid-json", "--file"},
		{BaselineSet{Measure: []byte(`{"schema":1,"since":"` + since + `","until":"` + until + `","workflows":[]}`), Settings: settingsJSON(t, since, until)}, "invalid-json", "repo"},
		{BaselineSet{Measure: []byte(`{"schema":1,"repo":"o/r","since":"` + since + `","workflows":[]}`), Settings: settingsJSON(t, since, until)}, "invalid-json", "until"},
		{BaselineSet{Measure: []byte(`{"schema":1,"repo":"o/r","since":7,"until":"` + until + `","workflows":[]}`), Settings: settingsJSON(t, since, until)}, "invalid-json", "since"},
	} {
		err := s.WriteBaselineSet(tc.in)
		if !hasCode(err, tc.code) || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("want %s %q, got %v", tc.code, tc.want, err)
		}
	}
	after = snapshotFiles(t, s)
	for _, n := range setFiles {
		if !bytes.Equal(before[n], after[n]) {
			t.Fatalf("%s changed", n)
		}
	}
}

func TestBaselineRerecordRequiresSettings(t *testing.T) {
	s := storeWindow(t, "2026-09-01", "2026-09-30")
	before := snapshotFiles(t, s)
	err := s.WriteBaselineSet(BaselineSet{Measure: measureJSON("o/r", "2026-09-05T00:00:00Z", "2026-10-05T00:00:00Z")})
	if !hasCode(err, "window-mismatch") || !strings.Contains(err.Error(), wantHint) {
		t.Fatalf("got %v", err)
	}
	after := snapshotFiles(t, s)
	for _, n := range setFiles {
		if !bytes.Equal(before[n], after[n]) {
			t.Fatalf("%s changed", n)
		}
	}
	// A supplied window that disagrees with the report is refused too.
	err = s.WriteBaselineSet(BaselineSet{Measure: measureJSON("o/r", "2026-09-05T00:00:00Z", "2026-10-05T00:00:00Z"), Settings: settingsJSON(t, "2026-09-05", "2026-10-04")})
	if !hasCode(err, "window-mismatch") || !strings.Contains(err.Error(), wantHint) {
		t.Fatalf("got %v", err)
	}
}

// The hint wording is pinned verbatim (review M-4).
const wantHint = "muda.toml's window must match the report; pass --settings carrying the report's window"

func TestBaselineSetDateWindowMatchesRFC3339(t *testing.T) {
	s := storeWindow(t, "2026-09-01", "2026-09-15")
	if err := s.WriteBaselineSet(BaselineSet{Measure: measureJSON("o/r", "2026-09-01T08:00:00Z", "2026-09-15T17:30:00.25Z")}); err != nil {
		t.Fatal(err)
	}
	if ps := s.Check(); len(ps) != 0 {
		t.Fatal(ps)
	}
	// RFC3339 settings compare as exact instants.
	since, until := "2026-09-01T08:00:00Z", "2026-09-15T17:30:00Z"
	if err := s.WriteBaselineSet(BaselineSet{Measure: measureJSON("o/r", since, "2026-09-15T17:30:01Z"), Settings: settingsJSON(t, since, until)}); !hasCode(err, "window-mismatch") {
		t.Fatalf("got %v", err)
	}
	if err := s.WriteBaselineSet(BaselineSet{Measure: measureJSON("o/r", "2026-09-01T09:00:00+01:00", until), Settings: settingsJSON(t, since, until)}); err != nil {
		t.Fatal(err)
	}
}

func TestBaselineSetRepoMismatch(t *testing.T) {
	s := store(t)
	before := snapshotFiles(t, s)
	m := measureJSON("o/r", "2026-09-01T00:00:00Z", "2026-09-15T00:00:00Z")
	for _, in := range []BaselineSet{
		{Measure: m, Gates: gatesJSON("o/other")},
		{Measure: m, Scan: scanJSON("o/other")},
	} {
		if err := s.WriteBaselineSet(in); !hasCode(err, "repo-mismatch") {
			t.Fatalf("got %v", err)
		}
	}
	after := snapshotFiles(t, s)
	for _, n := range setFiles {
		if !bytes.Equal(before[n], after[n]) {
			t.Fatalf("%s changed", n)
		}
	}
}

func TestCheckFlagsInconsistentBaselineSet(t *testing.T) {
	s := store(t)
	if err := s.WriteBaselineSet(BaselineSet{Measure: measureJSON("o/r", "2026-09-01T00:00:00Z", "2026-09-15T00:00:00Z"), Gates: gatesJSON("o/r"), Scan: scanJSON("o/r")}); err != nil {
		t.Fatal(err)
	}
	if ps := s.Check(); len(ps) != 0 {
		t.Fatal(ps)
	}
	write := func(name string, b []byte) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(s.Root, ".muda", name), b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("scan.json", scanJSON("o/other"))
	if ps := s.Check(); !hasProblem(ps, "repo-mismatch", "scan.json", "o/other") || len(ps) != 1 {
		t.Fatal(ps)
	}
	// A crash between baseline.json and muda.toml leaves a window mismatch.
	write("scan.json", scanJSON("o/r"))
	write("baseline.json", measureJSON("o/r", "2026-09-16T00:00:00Z", "2026-09-30T00:00:00Z"))
	if ps := s.Check(); !hasProblem(ps, "window-mismatch", "baseline.json", "2026-09-30") || len(ps) != 1 {
		t.Fatal(ps)
	}
	write("baseline.json", measureJSON("o/r", "2026-09-01T00:00:00Z", "2026-09-15T00:00:00Z"))
	write("scan.json", gatesJSON("o/r"))
	if ps := s.Check(); !hasProblem(ps, "wrong-report-type", "scan.json", "") {
		t.Fatal(ps)
	}
	write("scan.json", scanJSON("o/r"))
	if err := s.SetGatesJSON(measureJSON("o/r", "2026-09-01T00:00:00Z", "2026-09-15T00:00:00Z")); err != nil {
		t.Fatal(err)
	}
	if ps := s.Check(); !hasProblem(ps, "wrong-report-type", "gates.toml", "") || len(ps) != 1 {
		t.Fatal(ps)
	}
	// An older gates record (no security, no repo) stays clean.
	if err := s.SetGatesJSON([]byte(`{"schema":1,"gates":[{"id":"check:main:test"}]}`)); err != nil {
		t.Fatal(err)
	}
	if ps := s.Check(); len(ps) != 0 {
		t.Fatal(ps)
	}
	if err := s.SetGatesJSON(gatesJSON("o/other")); err != nil {
		t.Fatal(err)
	}
	if ps := s.Check(); !hasProblem(ps, "repo-mismatch", "gates.toml", "o/other") || len(ps) != 1 {
		t.Fatal(ps)
	}
}

func TestCheckFreshRecordClean(t *testing.T) {
	s := store(t)
	if ps := s.Check(); len(ps) != 0 {
		t.Fatal(ps)
	}
	snap, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snap["scan"]; ok {
		t.Fatal("absent scan.json shown")
	}
}

func TestShowIncludesScan(t *testing.T) {
	s := store(t)
	sc := scanJSON("o/r")
	if err := s.WriteBaselineSet(BaselineSet{Measure: measureJSON("o/r", "2026-09-01T00:00:00Z", "2026-09-15T00:00:00Z"), Scan: sc}); err != nil {
		t.Fatal(err)
	}
	snap, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	got, ok := snap["scan"].(json.RawMessage)
	if !ok || !bytes.Equal(got, sc) {
		t.Fatalf("scan %v", snap["scan"])
	}
}

func TestBaselineSetBusyLock(t *testing.T) {
	s := store(t)
	if err := os.WriteFile(filepath.Join(s.Root, ".muda", lockName), nil, 0600); err != nil {
		t.Fatal(err)
	}
	err := s.WriteBaselineSet(BaselineSet{Measure: measureJSON("o/r", "2026-09-01T00:00:00Z", "2026-09-15T00:00:00Z")})
	if !hasCode(err, "record-busy") {
		t.Fatalf("got %v", err)
	}
}

// Review I-1: a stop part-way through the write loop, whether a crash or a
// returned error, leaves a marker that check reports until a re-run completes.
func TestBaselineSetInterruptedIsFlagged(t *testing.T) {
	for _, tc := range []struct {
		after     string
		unapplied []string
		applied   []string
	}{
		{"scan.json", []string{"baseline.json", "gates.toml", "muda.toml"}, []string{"scan.json"}},
		{"gates.toml", []string{"baseline.json", "muda.toml"}, []string{"scan.json", "gates.toml"}},
	} {
		t.Run(tc.after, func(t *testing.T) {
			s := store(t)
			since, until := "2026-09-16T00:00:00Z", "2026-09-30T00:00:00Z"
			in := BaselineSet{Measure: measureJSON("o/r", since, until), Settings: settingsJSON(t, since, until), Gates: gatesJSON("o/r"), Scan: scanJSON("o/r")}
			writeHook = func(name string) error {
				if name == tc.after {
					return errors.New("injected failure")
				}
				return nil
			}
			t.Cleanup(func() { writeHook = nil })
			if err := s.WriteBaselineSet(in); err == nil || !strings.Contains(err.Error(), "injected failure") {
				t.Fatalf("got %v", err)
			}
			writeHook = nil
			marker := filepath.Join(s.Root, ".muda", pendingName)
			if _, err := os.Stat(marker); err != nil {
				t.Fatalf("marker gone: %v", err)
			}
			if _, err := os.Stat(filepath.Join(s.Root, ".muda", lockName)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("lock left: %v", err)
			}
			ps := s.Check()
			if len(ps) != 1 || ps[0].Code != "incomplete-baseline-set" || ps[0].File != pendingName {
				t.Fatal(ps)
			}
			detail := ps[0].Detail
			for _, n := range tc.unapplied {
				if !strings.Contains(detail, n) {
					t.Fatalf("%s not named: %s", n, detail)
				}
			}
			for _, n := range tc.applied {
				if strings.Contains(detail, n) {
					t.Fatalf("applied %s named: %s", n, detail)
				}
			}
			if !strings.Contains(detail, "sha256:") || !strings.Contains(detail, "re-run the same record baseline command") || strings.Contains(detail, "o/r") || strings.Contains(detail, since) {
				t.Fatalf("detail: %s", detail)
			}
			// Partial state is not built on by the single-file writers.
			if err := s.WriteBaselineJSON(measureJSON("o/r", since, until)); !hasCode(err, "incomplete-baseline-set") {
				t.Fatalf("baseline over marker: %v", err)
			}
			if err := s.SetGatesJSON(gatesJSON("o/r")); !hasCode(err, "incomplete-baseline-set") {
				t.Fatalf("gates over marker: %v", err)
			}
			// Re-running the same command completes the set and clears the marker.
			if err := s.WriteBaselineSet(in); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("marker remains: %v", err)
			}
			if ps := s.Check(); len(ps) != 0 {
				t.Fatal(ps)
			}
			if err := s.SetGatesJSON(gatesJSON("o/r")); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// Re-review N-1: a narrower run must not clear an unfinished set's marker.
func TestBaselineSetNarrowerRerunRefused(t *testing.T) {
	s := store(t)
	since, until := "2026-09-16T00:00:00Z", "2026-09-30T00:00:00Z"
	full := BaselineSet{Measure: measureJSON("o/r", since, until), Settings: settingsJSON(t, since, until), Gates: gatesJSON("o/r"), Scan: scanJSON("o/r")}
	writeHook = func(name string) error {
		if name == "scan.json" {
			return errors.New("injected failure")
		}
		return nil
	}
	t.Cleanup(func() { writeHook = nil })
	if err := s.WriteBaselineSet(full); err == nil {
		t.Fatal("no injected failure")
	}
	writeHook = nil
	before := snapshotFiles(t, s)
	for _, tc := range []struct {
		in      BaselineSet
		missing []string
		present []string
	}{
		{BaselineSet{Measure: full.Measure, Settings: full.Settings}, []string{"--gates", "--scan"}, nil},
		{BaselineSet{Measure: full.Measure, Settings: full.Settings, Gates: full.Gates}, []string{"--scan"}, []string{"--gates"}},
		{BaselineSet{Measure: full.Measure, Settings: full.Settings, Scan: full.Scan}, []string{"--gates"}, []string{"--scan"}},
	} {
		err := s.WriteBaselineSet(tc.in)
		if !hasCode(err, "incomplete-baseline-set") {
			t.Fatalf("got %v", err)
		}
		for _, f := range tc.missing {
			if !strings.Contains(err.Error(), f) {
				t.Fatalf("%s not named: %v", f, err)
			}
		}
		for _, f := range tc.present {
			if strings.Contains(err.Error(), f) {
				t.Fatalf("%s named: %v", f, err)
			}
		}
		after := snapshotFiles(t, s)
		for _, n := range setFiles {
			if !bytes.Equal(before[n], after[n]) {
				t.Fatalf("%s changed", n)
			}
		}
	}
	if err := s.WriteBaselineSet(full); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.Root, ".muda", pendingName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("marker remains: %v", err)
	}
	if ps := s.Check(); len(ps) != 0 {
		t.Fatal(ps)
	}
}

// Re-review m-2: the marker is validated on read and never echoed.
func TestPendingMarkerValidated(t *testing.T) {
	hash := strings.Repeat("ab", 32)
	for _, tc := range []struct {
		name, body, code, leak string
	}{
		{"credential value", `{"schema":1,"files":{"scan.json":"password: 'hunter22'"}}`, "incomplete-baseline-set", "hunter22"},
		{"credential name", `{"schema":1,"files":{"x":"password: 'hunter22'"}}`, "incomplete-baseline-set", "hunter22"},
		{"token", `{"schema":1,"files":{"scan.json":"ghp_abcdefghijklmnop1234"}}`, "incomplete-baseline-set", "ghp_"},
		{"traversal", `{"schema":1,"files":{"../../etc/passwd":"` + hash + `"}}`, "incomplete-baseline-set", "passwd"},
		{"unknown file", `{"schema":1,"files":{"findings.toml":"` + hash + `"}}`, "incomplete-baseline-set", "findings.toml"},
		{"uppercase hash", `{"schema":1,"files":{"scan.json":"` + strings.ToUpper(hash) + `"}}`, "incomplete-baseline-set", "AB"},
		{"short hash", `{"schema":1,"files":{"scan.json":"abc123"}}`, "incomplete-baseline-set", "abc123"},
		{"no files", `{"schema":1}`, "incomplete-baseline-set", ""},
		{"empty files", `{"schema":1,"files":{}}`, "incomplete-baseline-set", ""},
		{"unknown key", `{"schema":1,"files":{"scan.json":"` + hash + `"},"note":"surprise"}`, "incomplete-baseline-set", "surprise"},
		{"not json", `surprise`, "incomplete-baseline-set", "surprise"},
		{"schema 0", `{"schema":0,"files":{"scan.json":"` + hash + `"}}`, "incomplete-baseline-set", ""},
		{"newer schema", `{"schema":2,"files":{"scan.json":"` + hash + `"}}`, "newer-schema", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := store(t)
			if err := os.WriteFile(filepath.Join(s.Root, ".muda", pendingName), []byte(tc.body), 0600); err != nil {
				t.Fatal(err)
			}
			ps := s.Check()
			if len(ps) != 1 || ps[0].Code != tc.code || ps[0].File != pendingName || (tc.leak != "" && strings.Contains(ps[0].Detail, tc.leak)) {
				t.Fatal(ps)
			}
			if tc.code == "incomplete-baseline-set" && !strings.Contains(ps[0].Detail, "invalid marker") {
				t.Fatal(ps)
			}
			// The writer refuses an invalid marker; it is removed by hand.
			err := s.WriteBaselineSet(BaselineSet{Measure: measureJSON("o/r", "2026-09-01T00:00:00Z", "2026-09-15T00:00:00Z"), Gates: gatesJSON("o/r"), Scan: scanJSON("o/r"), Settings: settingsJSON(t, "2026-09-01", "2026-09-15")})
			if !hasCode(err, "incomplete-baseline-set") || !strings.Contains(err.Error(), "by hand through a reviewed record PR") || (tc.leak != "" && strings.Contains(err.Error(), tc.leak)) {
				t.Fatalf("got %v", err)
			}
			if b, err := os.ReadFile(filepath.Join(s.Root, ".muda", pendingName)); err != nil || string(b) != tc.body {
				t.Fatal("marker changed", err)
			}
		})
	}
}

// Re-review m-3: show refuses a possibly mixed record; check still reports it.
func TestShowRefusesPendingSet(t *testing.T) {
	s := store(t)
	writeHook = func(name string) error {
		if name == "gates.toml" {
			return errors.New("injected failure")
		}
		return nil
	}
	t.Cleanup(func() { writeHook = nil })
	if err := s.WriteBaselineSet(BaselineSet{Measure: measureJSON("o/r", "2026-09-01T00:00:00Z", "2026-09-15T00:00:00Z"), Gates: gatesJSON("o/r"), Scan: scanJSON("o/r")}); err == nil {
		t.Fatal("no injected failure")
	}
	writeHook = nil
	if _, err := s.Snapshot(); !hasCode(err, "incomplete-baseline-set") {
		t.Fatalf("got %v", err)
	}
	if ps := s.Check(); !hasProblem(ps, "incomplete-baseline-set", pendingName, "baseline.json") {
		t.Fatal(ps)
	}
}

// Final review m-8: gates set, the single-file baseline writer and show
// refuse an invalid or newer-schema marker with the remove-by-hand recovery,
// not a re-run that would itself be refused.
func TestRefusePendingInvalidMarkerSaysRemoveByHand(t *testing.T) {
	hash := strings.Repeat("ab", 32)
	for name, body := range map[string]string{
		"invalid": `surprise`,
		"newer":   `{"schema":2,"files":{"scan.json":"` + hash + `"}}`,
		"valid":   `{"schema":1,"files":{"scan.json":"` + hash + `"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			s := store(t)
			if err := os.WriteFile(filepath.Join(s.Root, ".muda", pendingName), []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			_, showErr := s.Snapshot()
			for _, err := range []error{s.SetGatesJSON(gatesJSON("o/r")), s.WriteBaselineJSON(measureJSON("o/r", "2026-09-01T00:00:00Z", "2026-09-15T00:00:00Z")), showErr} {
				if !hasCode(err, "incomplete-baseline-set") || strings.Contains(err.Error(), "surprise") {
					t.Fatalf("got %v", err)
				}
				byHand := strings.Contains(err.Error(), "by hand through a reviewed record PR")
				rerunMsg := strings.Contains(err.Error(), rerun)
				if name == "valid" && (byHand || !rerunMsg) || name != "valid" && (!byHand || rerunMsg) {
					t.Fatalf("%s marker: wrong recovery: %v", name, err)
				}
			}
		})
	}
}

// Final review m-2: a baseline gate inventory must read workflows at its own
// default branch, and a baseline scan at that branch too.
func TestBaselineSetRefMismatch(t *testing.T) {
	s := store(t)
	before := snapshotFiles(t, s)
	m := measureJSON("o/r", "2026-09-01T00:00:00Z", "2026-09-15T00:00:00Z")
	prGates := []byte(`{"schema":1,"repo":"o/r","branch":"main","ref":"muda/F-001-cache","security":[],"workflows":[],"gates":[],"unavailable":[],"rulesets":[],"environments":[]}`)
	mainGates := []byte(`{"schema":1,"repo":"o/r","branch":"main","ref":"main","security":[],"workflows":[],"gates":[],"unavailable":[],"rulesets":[],"environments":[]}`)
	prScan := []byte(`{"schema":1,"repo":"o/r","ref":"muda/F-001-cache","files":[".github/workflows/ci.yml"],"signals":[]}`)
	for _, in := range []BaselineSet{
		{Measure: m, Gates: prGates},
		{Measure: m, Gates: mainGates, Scan: prScan},
	} {
		err := s.WriteBaselineSet(in)
		if !hasCode(err, "ref-mismatch") || !strings.Contains(err.Error(), "muda/F-001-cache") {
			t.Fatalf("got %v", err)
		}
	}
	after := snapshotFiles(t, s)
	for _, n := range setFiles {
		if !bytes.Equal(before[n], after[n]) {
			t.Fatalf("%s changed", n)
		}
	}
	// Default-branch inputs, and a scan alone, are accepted.
	if err := s.WriteBaselineSet(BaselineSet{Measure: m, Gates: mainGates, Scan: scanJSON("o/r")}); err != nil {
		t.Fatal(err)
	}
	if ps := s.Check(); len(ps) != 0 {
		t.Fatal(ps)
	}
	// check reports the same on disk.
	if err := os.WriteFile(filepath.Join(s.Root, ".muda", "scan.json"), prScan, 0600); err != nil {
		t.Fatal(err)
	}
	if ps := s.Check(); !hasProblem(ps, "ref-mismatch", "scan.json", "muda/F-001-cache") || len(ps) != 1 {
		t.Fatal(ps)
	}
	if err := os.WriteFile(filepath.Join(s.Root, ".muda", "scan.json"), scanJSON("o/r"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.SetGatesJSON(prGates); err != nil {
		t.Fatal(err)
	}
	if ps := s.Check(); !hasProblem(ps, "ref-mismatch", "gates.toml", "muda/F-001-cache") || len(ps) != 1 {
		t.Fatal(ps)
	}
}
