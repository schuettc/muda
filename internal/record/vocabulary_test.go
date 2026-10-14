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

func TestCheckReportsUnknownWasteAndRecipe(t *testing.T) {
	s := vocabStore(t)
	// Records written before the check, or by hand, must still be reported.
	findings := findingsFile{Schema: Schema, Findings: []Finding{{ID: "F-001"}, {ID: "F-002"}}}
	findings.Findings[0] = vocabFinding("W55", "R1")
	findings.Findings[0].ID = "F-001"
	findings.Findings[1] = vocabFinding("W1", "R99")
	findings.Findings[1].ID = "F-002"
	e := exemption()
	e.Waste = "candidate:made-up"
	exemptions := exemptionsFile{Schema: Schema, Exemptions: []Exemption{e}}
	for name, doc := range map[string]any{"findings.toml": findings, "exemptions.toml": exemptions} {
		if err := s.writeTOML(name, doc); err != nil {
			t.Fatal(err)
		}
	}
	got := map[string]string{}
	for _, p := range s.Check() {
		got[p.Code+" "+p.File] += p.Detail + ";"
	}
	for key, detail := range map[string]string{
		"unknown-waste findings.toml":   "F-001: W55",
		"unknown-recipe findings.toml":  "F-002: R99",
		"unknown-waste exemptions.toml": "E-001: candidate:made-up",
	} {
		if !strings.Contains(got[key], detail) {
			t.Fatalf("missing %s %s in %v", key, detail, got)
		}
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
