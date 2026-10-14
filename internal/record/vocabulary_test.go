package record

import (
	"strings"
	"testing"

	"github.com/schuettc/muda/internal/content"
)

// vocab is a small injected vocabulary, so these tests do not follow the
// shipped standard as it grows.
var vocab = content.Vocabulary{Wastes: []string{"W1", "W2"}, Candidates: []string{"slow-thing"}, Recipes: []string{"R1"}}

func vocabStore(t *testing.T) *Store {
	t.Helper()
	s := &Store{Root: t.TempDir(), Vocab: &vocab}
	if err := s.Init(settings()); err != nil {
		t.Fatal(err)
	}
	return s
}
func vocabFinding(waste, recipe string) Finding {
	f := finding()
	f.Waste, f.Recipe = waste, recipe
	return f
}
func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), code) {
		t.Fatalf("want %s, got %v", code, err)
	}
}

func TestFindingWasteAndRecipeVocabulary(t *testing.T) {
	for _, tc := range []struct {
		name, waste, recipe, code string
	}{
		{"W-class with recipe", "W2", "R1", ""},
		{"listed candidate without recipe", "candidate:slow-thing", "", ""},
		{"unknown W-class", "W55", "R1", "unknown-waste"},
		{"free text waste", "waiting", "R1", "unknown-waste"},
		{"unlisted candidate", "candidate:made-up", "", "unknown-waste"},
		{"empty candidate slug", "candidate:", "", "unknown-waste"},
		{"unknown recipe", "W1", "R99", "unknown-recipe"},
		{"candidate with recipe", "candidate:slow-thing", "R1", "candidate-recipe"},
		{"W-class without recipe", "W1", "", "missing-finding-fields"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := vocabStore(t).AddFinding(vocabFinding(tc.waste, tc.recipe))
			if tc.code == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			wantCode(t, err, tc.code)
		})
	}
}

func TestSetFindingChecksVocabulary(t *testing.T) {
	s := vocabStore(t)
	id, err := s.AddFinding(vocabFinding("W1", "R1"))
	if err != nil {
		t.Fatal(err)
	}
	bad := "W55"
	wantCode(t, s.SetFinding(id, FindingPatch{Waste: &bad}), "unknown-waste")
	// Moving a finding to a candidate drops its recipe in the same patch.
	waste, none := "candidate:slow-thing", ""
	if err := s.SetFinding(id, FindingPatch{Waste: &waste, Recipe: &none}); err != nil {
		t.Fatal(err)
	}
}

func TestExemptionWasteVocabulary(t *testing.T) {
	for _, tc := range []struct{ waste, code string }{
		{"W1", ""}, {"candidate:slow-thing", ""}, {"W55", "unknown-waste"}, {"candidate:made-up", "unknown-waste"}, {"waiting", "unknown-waste"},
	} {
		e := exemption()
		e.Waste = tc.waste
		err := vocabStore(t).Exempt(e)
		if tc.code == "" {
			if err != nil {
				t.Fatalf("%s: %v", tc.waste, err)
			}
			continue
		}
		wantCode(t, err, tc.code)
	}
}

// Values are checked when written, not each time the record is read: a
// record written before the check, or against a candidate later removed from
// the standard, stays valid, and a status change does not re-judge it.
func TestStoredRecordOutlivesListChange(t *testing.T) {
	s := vocabStore(t)
	legacy := vocabFinding("candidate: lingering expand", "none (candidate waste)")
	legacy.ID, legacy.Status = "F-001", OpenStatus
	removed := vocabFinding("candidate:slow-thing", "")
	removed.ID, removed.Status = "F-002", OpenStatus
	old := exemption()
	old.Waste, old.Location = legacy.Waste, legacy.Location
	legacy.Status, legacy.ExemptionID = ExemptStatus, old.ID
	for name, doc := range map[string]any{
		"findings.toml":   findingsFile{Schema: Schema, Findings: []Finding{legacy, removed}},
		"exemptions.toml": exemptionsFile{Schema: Schema, Exemptions: []Exemption{old}},
	} {
		if err := s.writeTOML(name, doc); err != nil {
			t.Fatal(err)
		}
	}
	s.Vocab = &content.Vocabulary{Wastes: vocab.Wastes, Recipes: vocab.Recipes} // slow-thing removed
	if ps := s.Check(); len(ps) != 0 {
		t.Fatalf("stored values judged against the current lists: %+v", ps)
	}
	approved := Approved
	if err := s.SetFinding("F-002", FindingPatch{Status: &approved}); err != nil {
		t.Fatalf("a status change re-judged the stored waste: %v", err)
	}
	if _, err := s.AddFinding(vocabFinding("W1", "R1")); err != nil {
		t.Fatalf("a new finding was blocked by older ones: %v", err)
	}
	// A value being written is still checked.
	waste := "candidate:slow-thing"
	wantCode(t, s.SetFinding("F-002", FindingPatch{Waste: &waste}), "unknown-waste")
	w, r := "W1", "R1"
	if err := s.SetFinding("F-002", FindingPatch{Waste: &w, Recipe: &r}); err != nil {
		t.Fatalf("moving a finding to a current waste failed: %v", err)
	}
	if ps := s.Check(); len(ps) != 0 {
		t.Fatalf("record not clean after repair: %+v", ps)
	}
}

func TestShippedVocabularyIsTheDefault(t *testing.T) {
	s := store(t)
	if _, err := s.AddFinding(vocabFinding("W55", "R1")); err == nil {
		t.Fatal("shipped vocabulary accepted W55")
	}
	if _, err := s.AddFinding(vocabFinding("candidate:lingering-expand", "")); err != nil {
		t.Fatal(err)
	}
}

func TestFileLocation(t *testing.T) {
	for _, tc := range []struct {
		loc string
		ok  bool
	}{
		{"file:infra/lib/stack.ts", true},
		{"file:Dockerfile", true},
		{"file:docs/100%25.md", true},
		{"file:", false},
		{"file:/etc/stack.ts", false},
		{"file:infra/../stack.ts", false},
		{"file:infra//stack.ts", false},
		{"file:infra/", false},
		{"file:infra/stack.ts:12", false},
		{"file:infra%2Fstack.ts", false},
		{"file:infra/stack.ts#L12", false},
		{"file:infra\\stack.ts", false},
	} {
		if got := validLocation(tc.loc); got != tc.ok {
			t.Errorf("validLocation(%q) = %v, want %v", tc.loc, got, tc.ok)
		}
	}
	s := vocabStore(t)
	f := vocabFinding("W1", "R1")
	f.Location = "file:infra/lib/stack.ts"
	if _, err := s.AddFinding(f); err != nil {
		t.Fatal(err)
	}
	e := exemption()
	e.Waste, e.Location = "W1", f.Location
	if err := s.Exempt(e); err != nil {
		t.Fatal(err)
	}
	for _, p := range s.Check() {
		t.Errorf("unexpected problem %+v", p)
	}
}
