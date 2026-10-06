package record

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/schuettc/muda/internal/gates"
	"github.com/schuettc/muda/internal/gh"
	"github.com/schuettc/muda/internal/ghtest"
)

func TestExistingLockRejectsEveryMutation(t *testing.T) {
	s := store(t)
	id, err := s.AddFinding(finding())
	if err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(s.Root, ".muda", ".write-lock")
	if err := os.WriteFile(lock, []byte("unknown owner"), 0600); err != nil {
		t.Fatal(err)
	}
	calls := []struct {
		name string
		run  func() error
	}{
		{"add", func() error { _, err := s.AddFinding(finding()); return err }},
		{"set", func() error { v := "updated"; return s.SetFinding(id, FindingPatch{Estimate: &v}) }},
		{"exempt", func() error { return s.Exempt(exemption()) }},
		{"baseline", func() error { return s.WriteBaselineJSON([]byte(`{"schema":1,"repo":"updated"}`)) }},
		{"gates", func() error { return s.SetGatesJSON([]byte(`{"schema":1,"repo":"updated"}`)) }},
		{"receipt", func() error { return s.WriteReceipt(id, validReceipt()) }},
	}
	beforeFiles := map[string][]byte{}
	for _, name := range []string{"muda.toml", "findings.toml", "exemptions.toml", "gates.toml", "baseline.json"} {
		b, err := s.read(name)
		if err != nil {
			t.Fatal(err)
		}
		beforeFiles[name] = b
	}
	before, err := s.read("findings.toml")
	if err != nil {
		t.Fatal(err)
	}
	for _, call := range calls {
		t.Run(call.name, func(t *testing.T) {
			err := call.run()
			var p Problem
			if !errors.As(err, &p) || p.Code != "record-busy" {
				t.Fatalf("want record-busy, got %v", err)
			}
			b, err := os.ReadFile(lock)
			if err != nil || string(b) != "unknown owner" {
				t.Fatal("cleared someone else's lock", err)
			}
		})
	}
	for name, before := range beforeFiles {
		after, err := s.read(name)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatal("mutated under lock", name, err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(s.Root, ".muda", "receipts"))
	if err != nil || len(entries) != 0 {
		t.Fatal("receipt written under lock", err)
	}
	after, err := s.read("findings.toml")
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("findings mutated under lock", err)
	}
}
func TestStableLocationGrammar(t *testing.T) {
	for _, v := range []string{"gate:required-check:main:Unit Tests", "gate:workflow:.github/workflows/ci.yml:test", "gate:ruleset-check:main:protect/tests", "workflow:ci.yml", "workflow:.github/workflows/ci.yml/job:test", "workflow:ci.yml/job:test/step:Run tests: fast", "workflow:ci.yml/job:test/step:Run%2Fverify"} {
		if !validLocation(v) {
			t.Errorf("valid rejected: %q", v)
		}
	}
	for _, v := range []string{"gate:", "gate: ", "gate:x/../y", "workflow:", "workflow: ", "workflow:/ci.yml", "workflow:../ci.yml", "workflow:dir/../ci.yml", "workflow:ci.yml/job:", "workflow:ci.yml/step:Run tests", "workflow:ci.yml/step:3/garbage", "workflow:ci.yml/job:test/step:3", "workflow:ci.yml/job:test/step:", "workflow:ci.yml/job:test/job:again", "workflow:ci.yml/job:test/step:name/job:again", "workflow:ci.yml/job:test/step:name/garbage", "workflow:ci.yml/job:test/garbage", "workflow:ci.yml//job:test", "workflow:ci.yml/job:test/step:name/step:again", "workflow:ci.yml/job:../step:abc", "workflow:ci.yml/job:test/step:%33", "workflow:ci.yml/job:test/step:3.0", "workflow:ci.yml/job:test/step:-3", "workflow:ci.yml/job:test/step:name%2F..", "workflow:dir/%2E%2E/ci.yml", "workflow:dir/ci.yml%", "workflow:ci.yml/job:test/step:name\n", "workflow:dir%2Fci.yml"} {
		if validLocation(v) {
			t.Errorf("invalid accepted: %q", v)
		}
	}
}

func TestBoundaryReplacementBetweenValidationAndOpen(t *testing.T) {
	for _, initialized := range []bool{false, true} {
		t.Run(map[bool]string{false: "init-boundary", true: "existing-boundary"}[initialized], func(t *testing.T) {
			repo := t.TempDir()
			s := &Store{Root: repo}
			if initialized {
				if err := s.Init(settings()); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Mkdir(filepath.Join(repo, ".muda"), 0700); err != nil {
				t.Fatal(err)
			}
			parent, err := os.OpenRoot(repo)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = parent.Close() }()
			validated, err := parent.Lstat(".muda")
			if err != nil {
				t.Fatal(err)
			}
			// Swap the actual boundary in the exact Lstat→OpenRoot window. The
			// sibling stays INSIDE the repo, so parent os.Root alone permits it.
			victim := filepath.Join(repo, "victim")
			if err := os.Mkdir(victim, 0700); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(victim, "findings.toml")
			if err := os.WriteFile(file, []byte("untouched"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := parent.Rename(".muda", ".muda-original"); err != nil {
				t.Fatal(err)
			}
			if err := parent.Symlink("victim", ".muda"); err != nil {
				t.Fatal(err)
			}
			opened, err := openRecordRoot(parent, validated)
			if err == nil {
				defer func() { _ = opened.Close() }()
				if writeErr := atomic(opened, "findings.toml", []byte("WRONG DIRECTORY")); writeErr != nil {
					t.Fatal(writeErr)
				}
				t.Error("replacement boundary accepted")
			}
			data, e := os.ReadFile(file)
			if e != nil || string(data) != "untouched" {
				t.Fatal("sibling victim modified", e)
			}
		})
	}
}

func TestPinnedMutationRejectsChangedParentEntry(t *testing.T) {
	s := store(t)
	if _, err := s.AddFinding(finding()); err != nil {
		t.Fatal(err)
	}
	session, release, err := s.mutation()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := release(); err != nil {
			t.Error(err)
		}
	}()
	var doc findingsFile
	if err := session.load("findings.toml", &doc); err != nil {
		t.Fatal(err)
	}
	sibling := filepath.Join(s.Root, "sibling")
	if err := os.Mkdir(sibling, 0700); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(sibling, "findings.toml")
	if err := os.WriteFile(victim, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := session.parent.Rename(".muda", ".muda-original"); err != nil {
		t.Fatal(err)
	}
	if err := session.parent.Symlink("sibling", ".muda"); err != nil {
		t.Fatal(err)
	}
	doc.Findings[0].Estimate = "changed"
	var p Problem
	err = session.writeTOML("findings.toml", doc)
	if !errors.As(err, &p) || p.Code != "unsafe-path" {
		t.Fatalf("want unsafe-path, got %v", err)
	}
	b, err := os.ReadFile(victim)
	if err != nil || string(b) != "untouched" {
		t.Fatal("sibling touched", err)
	}
	b, err = os.ReadFile(filepath.Join(s.Root, ".muda-original", "findings.toml"))
	if err != nil || bytes.Contains(b, []byte("changed")) {
		t.Fatal("pinned facts changed after entry swap", err)
	}
}
func TestBoundaryReplacementByRealDirectory(t *testing.T) {
	s := store(t)
	parent, err := os.OpenRoot(s.Root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = parent.Close() }()
	info, err := parent.Lstat(".muda")
	if err != nil {
		t.Fatal(err)
	}
	if err := parent.Rename(".muda", ".muda-original"); err != nil {
		t.Fatal(err)
	}
	if err := parent.Mkdir(".muda", 0700); err != nil {
		t.Fatal(err)
	}
	opened, err := openRecordRoot(parent, info)
	if err == nil {
		_ = opened.Close()
		t.Fatal("different real directory accepted")
	}
	entries, err := os.ReadDir(filepath.Join(s.Root, ".muda"))
	if err != nil || len(entries) != 0 {
		t.Fatal("replacement changed", err)
	}
}
func TestLockOwnershipChangedIsNotCleared(t *testing.T) {
	s := store(t)
	session, release, err := s.mutation()
	if err != nil {
		t.Fatal(err)
	}
	// Keep the original inode alive while putting a different owner at the
	// fixed lock name; release must not remove that other owner's lock.
	if err := session.dir.Rename(lockName, ".previous-lock"); err != nil {
		t.Fatal(err)
	}
	if err := session.dir.WriteFile(lockName, []byte("new owner"), 0600); err != nil {
		t.Fatal(err)
	}
	var p Problem
	if err := release(); !errors.As(err, &p) || p.Code != "record-busy" {
		t.Fatalf("want busy on release, got %v", err)
	}
	b, err := s.read(lockName)
	if err != nil || string(b) != "new owner" {
		t.Fatal("foreign lock removed", err)
	}
}
func TestPartialInitRemainsExplicitlyInvalid(t *testing.T) {
	s := &Store{Root: t.TempDir()}
	if err := os.Mkdir(filepath.Join(s.Root, ".muda"), 0700); err != nil {
		t.Fatal(err)
	}
	// An interrupted init has no complete schemas. Never guess permission to
	// overwrite it, even when the directory is empty and lock is absent.
	err := s.Init(settings())
	var p Problem
	if !errors.As(err, &p) || p.Code != "record-exists" {
		t.Fatalf("want record-exists: %v", err)
	}
	if len(s.Check()) == 0 {
		t.Fatal("incomplete record accepted")
	}
	entries, err := os.ReadDir(filepath.Join(s.Root, ".muda"))
	if err != nil || len(entries) != 0 {
		t.Fatal("partial init overwritten", err)
	}
}

func TestLockSymlinkCannotWriteOutsideRecord(t *testing.T) {
	s := store(t)
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(s.Root, ".muda", lockName)); err != nil {
		t.Fatal(err)
	}
	_, err := s.AddFinding(finding())
	var p Problem
	if !errors.As(err, &p) || p.Code != "record-busy" {
		t.Fatalf("want busy, got %v", err)
	}
	b, err := os.ReadFile(victim)
	if err != nil || string(b) != "untouched" {
		t.Fatal("lock symlink followed", err)
	}
}

func TestStableLocationsAcceptGateProducerIDs(t *testing.T) {
	c := gh.New(gh.Options{NoCache: true, HTTP: &http.Client{Transport: ghtest.Replay("../ghtest/testdata/tackle")}})
	inv, err := gates.Derive(context.Background(), c, "schuettc/tackle", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Gates) == 0 {
		t.Fatal("fixture has no gates")
	}
	s := store(t)
	if err := s.SetGates(inv); err != nil {
		t.Fatal(err)
	}
	want, err := json.Marshal(inv)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GatesJSON()
	if err != nil || !bytes.Equal(want, got) {
		t.Fatal("full public inventory changed during save", err)
	}
	snapshot, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(want, snapshot["gates"].(json.RawMessage)) {
		t.Fatal("show changed full public inventory")
	}
	// Inspect credential matches without logging source text or values.
	matches := credentialAssignment.FindAllSubmatch(want, -1)
	if len(matches) == 0 {
		t.Fatal("public fixture does not exercise credential guard")
	}
	for _, match := range matches {
		value := match[2]
		if len(value) == 0 {
			value = match[3]
		}
		if !credentialReference.Match(bytes.TrimSpace(value)) {
			t.Fatal("public fixture credential match is not a complete reference")
		}
	}
	for i, g := range inv.Gates {
		location := "gate:" + g.ID
		if !validLocation(location) {
			t.Fatalf("producer ID rejected: %q", g.ID)
		}
		e := exemption()
		e.ID = fmt.Sprintf("E-gate-%d", i)
		e.Location = location
		if err := s.Exempt(e); err != nil {
			t.Fatal(err)
		}
	}
	if ps := s.Check(); len(ps) != 0 {
		t.Fatal(ps)
	}
}
