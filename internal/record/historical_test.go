package record

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// histSHA is the closed window's last commit in these tests.
const histSHA = "0123456789abcdef0123456789abcdef01234567"

func gatesAt(ref string) []byte {
	return []byte(`{"schema":1,"repo":"o/r","branch":"main","ref":"` + ref + `","security":[],"workflows":[],"gates":[],"unavailable":[],"rulesets":[],"environments":[]}`)
}
func scanAt(ref string) []byte {
	return []byte(`{"schema":1,"repo":"o/r","ref":"` + ref + `","files":[".github/workflows/ci.yml"],"signals":[]}`)
}

// The store's window is 2026-09-01..2026-09-15; its measure report matches.
func histMeasure() []byte {
	return measureJSON("o/r", "2026-09-01T00:00:00Z", "2026-09-15T00:00:00Z")
}

// A closed window's baseline may read gates and scan at its last commit: the
// record names that ref as historical, and the settings read today as current.
func TestHistoricalBaselineClosedWindowAccepted(t *testing.T) {
	clock(t, "2026-10-01")
	s := store(t)
	in := BaselineSet{Measure: histMeasure(), Gates: gatesAt(histSHA), Scan: scanAt(histSHA), Ref: histSHA}
	if err := s.WriteBaselineSet(in); err != nil {
		t.Fatal(err)
	}
	if ps := s.Check(); len(ps) != 0 {
		t.Fatal(ps)
	}
	if g, err := s.GatesJSON(); err != nil || !bytes.Equal(g, in.Gates) {
		t.Fatalf("gates %s %v", g, err)
	}
	b, err := os.ReadFile(filepath.Join(s.Root, ".muda", "gates.toml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`historical-ref = "` + histSHA + `"`, `settings-read = "2026-10-01"`} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("gates.toml lacks %s:\n%s", want, b)
		}
	}
	snap, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	h, ok := snap["historical"].(Historical)
	if !ok || h.Ref != histSHA || h.SettingsRead != "2026-10-01" || h.Settings != "settings read 2026-10-01, not historical" {
		t.Fatalf("historical %#v", snap["historical"])
	}
	// A later measure-only re-record keeps the historical record whole.
	if err := s.WriteBaselineSet(BaselineSet{Measure: histMeasure()}); err != nil {
		t.Fatal(err)
	}
	if ps := s.Check(); len(ps) != 0 {
		t.Fatal(ps)
	}
	// So does a scan re-recorded at the same ref, with or without --ref.
	for _, ref := range []string{"", histSHA} {
		if err := s.WriteBaselineSet(BaselineSet{Measure: histMeasure(), Scan: scanAt(histSHA), Ref: ref}); err != nil {
			t.Fatalf("--ref %q: %v", ref, err)
		}
	}
	// A current gate inventory replaces the historical one.
	if err := s.WriteBaselineSet(BaselineSet{Measure: histMeasure(), Gates: gatesAt("main"), Scan: scanAt("main")}); err != nil {
		t.Fatal(err)
	}
	snap, err = s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snap["historical"]; ok {
		t.Fatal("current baseline still historical")
	}
}

// While the window is open, the ref-mismatch refusal stands, --ref or not.
func TestHistoricalBaselineOpenWindowRefused(t *testing.T) {
	clock(t, "2026-09-10")
	s := store(t)
	before := snapshotFiles(t, s)
	for _, in := range []BaselineSet{
		{Measure: histMeasure(), Gates: gatesAt(histSHA), Scan: scanAt(histSHA), Ref: histSHA},
		{Measure: histMeasure(), Gates: gatesAt(histSHA), Ref: histSHA},
		{Measure: histMeasure(), Gates: gatesAt(histSHA), Scan: scanAt(histSHA)},
	} {
		err := s.WriteBaselineSet(in)
		if !hasCode(err, "ref-mismatch") || !strings.Contains(err.Error(), histSHA) {
			t.Fatalf("got %v", err)
		}
	}
	err := s.WriteBaselineSet(BaselineSet{Measure: histMeasure(), Gates: gatesAt(histSHA), Ref: histSHA})
	if !strings.Contains(err.Error(), "open") {
		t.Fatalf("open window not named: %v", err)
	}
	after := snapshotFiles(t, s)
	for _, n := range setFiles {
		if !bytes.Equal(before[n], after[n]) {
			t.Fatalf("%s changed", n)
		}
	}
}

// --ref must be the ref every supplied report names, and a commit.
func TestHistoricalBaselineRefMustMatchReports(t *testing.T) {
	clock(t, "2026-10-01")
	s := store(t)
	before := snapshotFiles(t, s)
	other := "fedcba9876543210fedcba9876543210fedcba98"
	for _, tc := range []struct {
		in         BaselineSet
		code, want string
	}{
		{BaselineSet{Measure: histMeasure(), Gates: gatesAt(other), Ref: histSHA}, "ref-mismatch", other},
		{BaselineSet{Measure: histMeasure(), Gates: gatesAt("main"), Ref: histSHA}, "ref-mismatch", histSHA},
		{BaselineSet{Measure: histMeasure(), Gates: gatesAt(histSHA), Scan: scanAt(other), Ref: histSHA}, "ref-mismatch", other},
		{BaselineSet{Measure: histMeasure(), Gates: gatesAt(histSHA), Scan: scanAt("main"), Ref: histSHA}, "ref-mismatch", histSHA},
		// A report without a ref does not name the commit.
		{BaselineSet{Measure: histMeasure(), Gates: gatesJSON("o/r"), Ref: histSHA}, "ref-mismatch", histSHA},
		// The stored inventory is current, so a scan alone cannot be historical.
		{BaselineSet{Measure: histMeasure(), Scan: scanAt(histSHA), Ref: histSHA}, "ref-mismatch", histSHA},
		// A branch name moves; a historical ref is a commit.
		{BaselineSet{Measure: histMeasure(), Gates: gatesAt("release"), Scan: scanAt("release"), Ref: "release"}, "invalid-ref", "commit"},
	} {
		err := s.WriteBaselineSet(tc.in)
		if !hasCode(err, tc.code) || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: got %v", tc.want, err)
		}
	}
	after := snapshotFiles(t, s)
	for _, n := range setFiles {
		if !bytes.Equal(before[n], after[n]) {
			t.Fatalf("%s changed", n)
		}
	}
	// Once historical, a scan at another ref is refused.
	if err := s.WriteBaselineSet(BaselineSet{Measure: histMeasure(), Gates: gatesAt(histSHA), Ref: histSHA}); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{"", other} {
		if err := s.WriteBaselineSet(BaselineSet{Measure: histMeasure(), Scan: scanAt(other), Ref: ref}); !hasCode(err, "ref-mismatch") {
			t.Fatalf("--ref %q: got %v", ref, err)
		}
	}
}

// check applies the same rules on disk: a historical record whose window is
// open, or whose scan names another ref, is flagged.
func TestCheckFlagsHistoricalMismatch(t *testing.T) {
	clock(t, "2026-10-01")
	s := store(t)
	if err := s.WriteBaselineSet(BaselineSet{Measure: histMeasure(), Gates: gatesAt(histSHA), Scan: scanAt(histSHA), Ref: histSHA}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Root, ".muda", "scan.json"), scanAt("main"), 0600); err != nil {
		t.Fatal(err)
	}
	if ps := s.Check(); !hasProblem(ps, "ref-mismatch", "scan.json", histSHA) || len(ps) != 1 {
		t.Fatal(ps)
	}
	if err := os.WriteFile(filepath.Join(s.Root, ".muda", "scan.json"), scanAt(histSHA), 0600); err != nil {
		t.Fatal(err)
	}
	clock(t, "2026-09-10")
	if ps := s.Check(); !hasProblem(ps, "ref-mismatch", "gates.toml", "open") || len(ps) != 1 {
		t.Fatal(ps)
	}
}

// A gates.toml written before historical baselines (no historical-ref or
// settings-read) loads, shows and checks as before; a half-written pair is
// invalid.
func TestOlderGatesRecordLoadsWithoutHistorical(t *testing.T) {
	s := store(t)
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(s.Root, ".muda", "gates.toml"), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("schema = 1\ninventory-json = '" + string(gatesJSON("o/r")) + "'\n")
	if g, err := s.GatesJSON(); err != nil || !bytes.Equal(g, gatesJSON("o/r")) {
		t.Fatalf("gates %s %v", g, err)
	}
	if ps := s.Check(); len(ps) != 0 {
		t.Fatal(ps)
	}
	snap, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snap["historical"]; ok {
		t.Fatal("older record shown as historical")
	}
	for _, body := range []string{
		"schema = 1\nhistorical-ref = \"" + histSHA + "\"\ninventory-json = '" + string(gatesAt(histSHA)) + "'\n",
		"schema = 1\nsettings-read = \"2026-10-01\"\ninventory-json = '" + string(gatesJSON("o/r")) + "'\n",
		"schema = 1\nhistorical-ref = \"main\"\nsettings-read = \"2026-10-01\"\ninventory-json = '" + string(gatesAt("main")) + "'\n",
	} {
		write(body)
		if _, err := s.GatesJSON(); !hasCode(err, "invalid-historical") {
			t.Fatalf("%s: got %v", body, err)
		}
	}
}

// Fix round 1, I1: a report's until is never after the moment measure ran, so
// "closed" means until is at or before the start of the current UTC day. A
// date-only until is that day's 00:00Z; RFC3339 is the exact instant.
func TestHistoricalWindowClosedAtStartOfToday(t *testing.T) {
	clock(t, "2026-10-01") // now is 2026-10-01T12:00:00Z
	for _, tc := range []struct {
		until  string
		closed bool
	}{
		{"2026-10-01T12:00:00Z", false}, // the moment measure ran
		{"2026-10-01T00:00:01Z", false},
		{"2026-10-02", false},
		{"2026-10-01T00:00:00Z", true}, // at the start of today
		{"2026-10-01", true},           // date-only: 2026-10-01T00:00:00Z
		{"2026-09-30T23:59:59Z", true},
		{"2026-09-30", true}, // yesterday
	} {
		t.Run(tc.until, func(t *testing.T) {
			s := storeWindow(t, "2026-09-01", tc.until)
			u := tc.until
			if len(u) == len("2006-01-02") {
				u += "T00:00:00Z"
			}
			in := BaselineSet{Measure: measureJSON("o/r", "2026-09-01T00:00:00Z", u), Gates: gatesAt(histSHA), Scan: scanAt(histSHA), Ref: histSHA}
			err := s.WriteBaselineSet(in)
			if tc.closed && err != nil {
				t.Fatalf("closed window refused: %v", err)
			}
			if !tc.closed && (!hasCode(err, "ref-mismatch") || !strings.Contains(err.Error(), "start of today")) {
				t.Fatalf("open window: got %v", err)
			}
		})
	}
}

// Fix round 1, M1: gates set would silently turn a historical record current;
// it refuses and names the baseline command instead.
func TestGatesSetRefusesHistoricalRecord(t *testing.T) {
	clock(t, "2026-10-01")
	s := store(t)
	if err := s.WriteBaselineSet(BaselineSet{Measure: histMeasure(), Gates: gatesAt(histSHA), Scan: scanAt(histSHA), Ref: histSHA}); err != nil {
		t.Fatal(err)
	}
	before := snapshotFiles(t, s)
	err := s.SetGatesJSON(gatesAt("main"))
	if !hasCode(err, "historical-gates") || !strings.Contains(err.Error(), "record baseline --gates") || !strings.Contains(err.Error(), "--ref") {
		t.Fatalf("got %v", err)
	}
	if after := snapshotFiles(t, s); !bytes.Equal(before["gates.toml"], after["gates.toml"]) {
		t.Fatal("gates.toml changed")
	}
}

// Fix round 1, M4: muda gates and scan echo --ref verbatim, so a historical
// ref is a full 40- or 64-hex commit SHA, never an abbreviation.
func TestHistoricalRefMustBeFullSHA(t *testing.T) {
	clock(t, "2026-10-01")
	s := store(t)
	for _, ref := range []string{"0123456", histSHA[:39], histSHA + "0", strings.ToUpper(histSHA)} {
		err := s.WriteBaselineSet(BaselineSet{Measure: histMeasure(), Gates: gatesAt(ref), Scan: scanAt(ref), Ref: ref})
		if !hasCode(err, "invalid-ref") || !strings.Contains(err.Error(), "full") {
			t.Fatalf("%s: got %v", ref, err)
		}
	}
	sha256 := strings.Repeat("ab", 32)
	if err := s.WriteBaselineSet(BaselineSet{Measure: histMeasure(), Gates: gatesAt(sha256), Scan: scanAt(sha256), Ref: sha256}); err != nil {
		t.Fatal(err)
	}
	if _, err := HistoricalOf("0123456", "2026-10-01"); !hasCode(err, "invalid-historical") {
		t.Fatalf("short stored ref: %v", err)
	}
}
