package record

import (
	"os"
	"path/filepath"
	"testing"
)

func settings() Settings {
	return Settings{Schema: 1, Roles: map[string]string{"ci.yml": "verify"}, Stacks: []string{"go"}, Window: Window{Since: "2026-09-01", Until: "2026-09-15"}, MudaVersion: "dev"}
}
func finding() Finding {
	return Finding{Waste: "waiting", Location: "workflow:ci.yml/job:test", Recipe: "slow-step", Evidence: []Evidence{{Kind: "file", Path: "ci.yml", Note: "observed"}}, Basis: "suspected", Estimate: "2 minutes", Risk: "low"}
}
func store(t *testing.T) *Store {
	t.Helper()
	s := &Store{Root: t.TempDir()}
	if err := s.Init(settings()); err != nil {
		t.Fatal(err)
	}
	return s
}
func TestInitCreatesSchemaFiles(t *testing.T) {
	s := store(t)
	for _, n := range []string{"muda.toml", "gates.toml", "baseline.json", "findings.toml", "exemptions.toml"} {
		if _, err := os.Stat(filepath.Join(s.Root, ".muda", n)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Init(settings()); err == nil {
		t.Fatal("overwrote init")
	}
	if _, err := Open(t.TempDir()); err == nil {
		t.Fatal("absent accepted")
	}
}
func TestFindingIDsSequential(t *testing.T) {
	s := store(t)
	for _, want := range []string{"F-001", "F-002"} {
		id, err := s.AddFinding(finding())
		if err != nil || id != want {
			t.Fatalf("%s %v", id, err)
		}
	}
}
func TestStatusTransitions(t *testing.T) {
	for _, a := range []Status{OpenStatus, Approved, InPR, Fixed, Reopened, ExemptStatus} {
		for _, b := range []Status{OpenStatus, Approved, InPR, Fixed, Reopened, ExemptStatus} {
			want := a == b || a == OpenStatus && b == Approved || a == Approved && (b == InPR || b == Reopened) || a == InPR && (b == Fixed || b == Reopened) || a == Reopened && b == Approved || a != Fixed && b == ExemptStatus
			if got := allowed(a, b); got != want {
				t.Fatalf("%s -> %s", a, b)
			}
		}
	}
}
func exemption() Exemption {
	return Exemption{ID: "E-001", Waste: "waiting", Location: "workflow:ci.yml/job:test", Reason: "deliberate", AgreedBy: "owner", Date: "2026-09-01", ReviewBy: "2099-01-01"}
}
func TestExemptRequiresFields(t *testing.T) {
	for _, field := range []string{"waste", "location", "reason", "agreed-by", "date", "review-by"} {
		s := store(t)
		e := exemption()
		switch field {
		case "waste":
			e.Waste = ""
		case "location":
			e.Location = ""
		case "reason":
			e.Reason = ""
		case "agreed-by":
			e.AgreedBy = ""
		case "date":
			e.Date = ""
		case "review-by":
			e.ReviewBy = ""
		}
		if err := s.Exempt(e); err == nil {
			t.Fatal(field)
		}
	}
}
func TestCheckRefusesNewerSchema(t *testing.T) {
	for _, n := range []string{"muda.toml", "gates.toml", "findings.toml", "exemptions.toml", "baseline.json", "receipts/F-001.md"} {
		t.Run(n, func(t *testing.T) {
			s := store(t)
			data := "schema = 2\n"
			if n == "baseline.json" {
				data = `{"schema":2}`
			}
			if n == "receipts/F-001.md" {
				data = "+++\nschema = 2\n+++\n"
			}
			if err := os.WriteFile(filepath.Join(s.Root, ".muda", n), []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			found := false
			for _, p := range s.Check() {
				if p.Code == "newer-schema" {
					found = true
				}
			}
			if !found {
				t.Fatal(s.Check())
			}
		})
	}
}
func TestCheckFindsDanglingExemptRef(t *testing.T) {
	s := store(t)
	id, err := s.AddFinding(finding())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Exempt(exemption()); err != nil {
		t.Fatal(err)
	}
	st := ExemptStatus
	ref := "E-001"
	if err := s.SetFinding(id, FindingPatch{Status: &st, ExemptionID: &ref}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Root, ".muda", "exemptions.toml"), []byte("schema = 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range s.Check() {
		if p.Code == "dangling-exemption" {
			found = true
		}
	}
	if !found {
		t.Fatal(s.Check())
	}
}
func TestRecordWritesOnlyUnderDotMuda(t *testing.T) {
	for _, target := range []string{".muda", ".muda/receipts", ".muda/findings.toml"} {
		t.Run(target, func(t *testing.T) {
			s := store(t)
			outside := t.TempDir()
			dest := outside
			if target == ".muda/findings.toml" {
				dest = filepath.Join(outside, "victim")
				if err := os.WriteFile(dest, []byte("untouched"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.RemoveAll(filepath.Join(s.Root, target)); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(dest, filepath.Join(s.Root, target)); err != nil {
				t.Fatal(err)
			}
			_, err := s.AddFinding(finding())
			if target == ".muda/receipts" {
				err = s.WriteReceipt("F-001", Receipt{})
			}
			if err == nil {
				t.Fatal("symlink accepted")
			}
			entries, e := os.ReadDir(outside)
			if e != nil {
				t.Fatal(e)
			}
			if target == ".muda/findings.toml" {
				b, e := os.ReadFile(dest)
				if e != nil || string(b) != "untouched" {
					t.Fatal("victim modified")
				}
			} else if len(entries) != 0 {
				t.Fatal("outside written")
			}
		})
	}
	s := store(t)
	for _, id := range []string{"../victim", "F-001/../../victim", "F-01"} {
		if err := s.WriteReceipt(id, Receipt{}); err == nil {
			t.Fatal(id)
		}
	}
}

// Fix round 4: an expired exemption that no exempt finding references is
// history, not an expired-exemption problem (it is still unmatched here).
func TestExpiredAndUnmatchedExemptions(t *testing.T) {
	clock(t, "2030-01-01")
	s := store(t)
	e := exemption()
	e.Date, e.ReviewBy = "2030-01-01", "2030-01-02"
	if err := s.Exempt(e); err != nil {
		t.Fatal(err)
	}
	clock(t, "2030-01-03")
	codes := map[string]bool{}
	for _, p := range s.Check() {
		codes[p.Code] = true
	}
	if codes["expired-exemption"] || !codes["unmatched-exemption"] {
		t.Fatal(codes)
	}
}

func TestStatusTransitionsPersisted(t *testing.T) {
	s := store(t)
	id, err := s.AddFinding(finding())
	if err != nil {
		t.Fatal(err)
	}
	// in-pr and fixed require a PR link (fix round 8d); set it with the status.
	pr := "https://github.com/o/r/pull/1"
	for _, status := range []Status{Approved, InPR, Reopened, Approved, InPR, Fixed} {
		if status == Fixed { // fixed requires a passing receipt for this PR
			if err := s.WriteReceipt(id, validReceipt()); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.SetFinding(id, FindingPatch{Status: &status, PR: &pr}); err != nil {
			t.Fatal(err)
		}
	}
	for _, status := range []Status{Reopened, ExemptStatus, OpenStatus} {
		if err := s.SetFinding(id, FindingPatch{Status: &status}); err == nil {
			t.Fatalf("fixed -> %s allowed", status)
		}
	}
	if ps := s.Check(); len(ps) != 0 {
		t.Fatal(ps)
	}
}
func TestSnapshotsPreserveUnknownFields(t *testing.T) {
	s := store(t)
	raw := []byte(`{"schema":1,"repo":"o/r","gates":[],"raw_policy":{"condition":["a","b"],"enabled":false},"workflow_conditions":{"if":"expression"}}`)
	if err := s.SetGatesJSON(raw); err != nil {
		t.Fatal(err)
	}
	got, err := s.GatesJSON()
	if err != nil || string(got) != string(raw) {
		t.Fatalf("%s %v", got, err)
	}
	b := []byte(`{"schema":1,"scan":{"unknown":true}}`)
	if err := s.WriteBaselineJSON(b); err != nil {
		t.Fatal(err)
	}
	got, err = s.read("baseline.json")
	if err != nil || string(got) != string(b) {
		t.Fatalf("%s %v", got, err)
	}
}
func TestRootSymlinkRejected(t *testing.T) {
	outside := t.TempDir()
	link := filepath.Join(t.TempDir(), "root")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	s := &Store{Root: link}
	if err := s.Init(settings()); err == nil {
		t.Fatal("root symlink accepted")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatal("outside modified", err)
	}
}
func TestSnapshotRejectsNewerReceipt(t *testing.T) {
	s := store(t)
	if err := os.WriteFile(filepath.Join(s.Root, ".muda", "receipts", "F-001.md"), []byte("+++\nschema = 2\n+++\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Snapshot(); err == nil {
		t.Fatal("newer receipt shown")
	}
}
func TestExpiredExemptionCannotApply(t *testing.T) {
	s := store(t)
	id, err := s.AddFinding(finding())
	if err != nil {
		t.Fatal(err)
	}
	e := exemption()
	e.Date, e.ReviewBy = "2030-01-01", "2030-01-02"
	clock(t, "2030-01-01")
	if err := s.Exempt(e); err != nil {
		t.Fatal(err)
	}
	clock(t, "2030-01-03")
	status := ExemptStatus
	if err := s.SetFinding(id, FindingPatch{Status: &status, ExemptionID: &e.ID}); err == nil {
		t.Fatal("expired applied")
	}
}
func TestSecretRefused(t *testing.T) {
	s := store(t)
	if err := s.SetGatesJSON([]byte(`{"schema":1,"token":"private-value"}`)); err == nil {
		t.Fatal("secret stored")
	}
}

func TestConfinementEveryWriter(t *testing.T) {
	for _, name := range []string{"baseline.json", "gates.toml", "exemptions.toml", "receipts/F-001.md"} {
		t.Run(name, func(t *testing.T) {
			s := store(t)
			if _, err := s.AddFinding(finding()); err != nil {
				t.Fatal(err)
			}
			outside := filepath.Join(t.TempDir(), "victim")
			if err := os.WriteFile(outside, []byte("untouched"), 0600); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(s.Root, ".muda", name)
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, path); err != nil {
				t.Fatal(err)
			}
			var err error
			switch name {
			case "baseline.json":
				err = s.WriteBaselineJSON([]byte(`{"schema":1}`))
			case "gates.toml":
				err = s.SetGatesJSON([]byte(`{"schema":1}`))
			case "exemptions.toml":
				err = s.Exempt(exemption())
			case "receipts/F-001.md":
				err = s.WriteReceipt("F-001", validReceipt())
			}
			if err == nil {
				t.Fatal("symlink accepted")
			}
			b, e := os.ReadFile(outside)
			if e != nil || string(b) != "untouched" {
				t.Fatal("outside modified", e)
			}
		})
	}
	s := store(t)
	if _, err := s.AddFinding(finding()); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Remove(filepath.Join(s.Root, ".muda", "receipts")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(s.Root, ".muda", "receipts")); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteReceipt("F-001", validReceipt()); err == nil {
		t.Fatal("receipts escape accepted")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatal("outside modified", err)
	}
}
func validReceipt() Receipt {
	return Receipt{Schema: 1, PR: "https://github.com/o/r/pull/1", Before: "2m", After: "1m", RunLinks: []string{"https://github.com/o/r/actions/runs/1"}, GateStatus: GateVerified, GateResult: "unchanged"}
}
func TestInitUnownedDataUntouched(t *testing.T) {
	s := &Store{Root: t.TempDir()}
	if err := os.Mkdir(filepath.Join(s.Root, ".muda"), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.Root, ".muda", "muda.toml")
	if err := os.WriteFile(path, []byte("unowned"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.Init(settings()); err == nil {
		t.Fatal("unowned overwritten")
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != "unowned" {
		t.Fatal("modified")
	}
}
func TestSetGatesRefusesNewerEmbeddedSchema(t *testing.T) {
	s := store(t)
	if err := os.WriteFile(filepath.Join(s.Root, ".muda", "gates.toml"), []byte("schema = 1\ninventory-json = '{\"schema\":2}'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.SetGatesJSON([]byte(`{"schema":1}`)); err == nil {
		t.Fatal("newer embedded inventory overwritten")
	}
}
func TestShowRefusesSecrets(t *testing.T) {
	s := store(t)
	if err := os.WriteFile(filepath.Join(s.Root, ".muda", "muda.toml"), []byte("schema = 1\ntoken = \"private-value\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Snapshot(); err == nil {
		t.Fatal("secret shown")
	}
}

func TestReceiptRoundTripAndDangling(t *testing.T) {
	s := store(t)
	id, err := s.AddFinding(finding())
	if err != nil {
		t.Fatal(err)
	}
	r := validReceipt()
	if err := s.WriteReceipt(id, r); err != nil {
		t.Fatal(err)
	}
	b, err := s.read("receipts/" + id + ".md")
	if err != nil {
		t.Fatal(err)
	}
	got, err := parseReceipt(b, "receipt")
	if err != nil || got.Schema != 1 || got.FindingID != id || got.Before != r.Before || got.After != r.After || got.GateResult != r.GateResult {
		t.Fatalf("%+v %v", got, err)
	}
	if ps := s.Check(); len(ps) != 0 {
		t.Fatal(ps)
	}
	if err := os.WriteFile(filepath.Join(s.Root, ".muda", "findings.toml"), []byte("schema = 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range s.Check() {
		if p.Code == "dangling-receipt" {
			found = true
		}
	}
	if !found {
		t.Fatal(s.Check())
	}
}
func TestDeterministicProblems(t *testing.T) {
	s := store(t)
	if err := s.Exempt(exemption()); err != nil {
		t.Fatal(err)
	}
	a := s.Check()
	b := s.Check()
	if len(a) != len(b) {
		t.Fatal(a, b)
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatal(a, b)
		}
	}
}
func TestExemptRejectsStepIndex(t *testing.T) {
	s := store(t)
	e := exemption()
	e.Location = "workflow:ci.yml/job:test/step:3"
	if err := s.Exempt(e); err == nil {
		t.Fatal("index accepted")
	}
}

func TestShowRefusesReceiptBodySecrets(t *testing.T) {
	s := store(t)
	id, err := s.AddFinding(finding())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WriteReceipt(id, validReceipt()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.Root, ".muda", "receipts", id+".md")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteString("\nghp_abcdefghijklmnopqrst\n"); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Snapshot(); err == nil {
		t.Fatal("body secret shown")
	}
}

// Git keeps no empty directory, so a record cloned before its first receipt
// has no receipts/ at all. That is a valid record with no receipts, and the
// first receipt creates the directory.
func TestClonedRecordWithoutReceiptsDir(t *testing.T) {
	s := store(t)
	if err := os.Remove(filepath.Join(s.Root, ".muda", "receipts")); err != nil {
		t.Fatal(err)
	}
	if ps := s.Check(); len(ps) != 0 {
		t.Fatal(ps)
	}
	id, err := s.AddFinding(finding())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WriteReceipt(id, validReceipt()); err != nil {
		t.Fatal(err)
	}
	if ps := s.Check(); len(ps) != 0 {
		t.Fatal(ps)
	}
}
