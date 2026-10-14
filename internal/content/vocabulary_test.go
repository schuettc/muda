package content_test

import (
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/schuettc/muda"
	"github.com/schuettc/muda/internal/content"
)

// The record checks a finding against these lists, so they are pinned here:
// a change to the standard's catalog, its candidate wastes or the recipes must
// show up as a deliberate change to this test.
func TestShippedVocabulary(t *testing.T) {
	v, err := content.ReadVocabulary(muda.Content)
	if err != nil {
		t.Fatal(err)
	}
	want := content.Vocabulary{
		Wastes:     []string{"W1", "W2", "W3", "W4", "W5", "W6", "W7", "W8", "W9", "W10"},
		Candidates: []string{"deploy-cancel", "build-context", "unscoped-post-deploy", "network-timeout", "artifact-lifecycle", "stale-automation-pr", "lingering-expand"},
		Recipes:    []string{"R1", "R2", "R3", "R4", "R5", "R6", "R7", "R8", "R9", "R10", "R11", "R12"},
	}
	for name, pair := range map[string][2][]string{
		"wastes": {v.Wastes, want.Wastes}, "candidates": {v.Candidates, want.Candidates}, "recipes": {v.Recipes, want.Recipes},
	} {
		if !slices.Equal(pair[0], pair[1]) {
			t.Errorf("%s = %v, want %v", name, pair[0], pair[1])
		}
	}
}

func standardWith(t *testing.T, edit func(string) string) fstest.MapFS {
	t.Helper()
	f := libraryFixture(t)
	f["standard/standard.md"] = &fstest.MapFile{Data: []byte(edit(string(f["standard/standard.md"].Data)))}
	return f
}

func TestCheckFlagsCandidateWithoutSlug(t *testing.T) {
	f := standardWith(t, func(s string) string {
		return strings.Replace(s, "## Exemptions", "- A new suspicion with no slug.\n\n## Exemptions", 1)
	})
	wantProblem(t, content.Check(f, func([]string) bool { return true }), "standard/standard.md", "candidate waste without a slug")
}

func TestCheckFlagsDuplicateCandidateSlug(t *testing.T) {
	f := standardWith(t, func(s string) string {
		return strings.Replace(s, "## Exemptions", "- `lingering-expand`: again.\n\n## Exemptions", 1)
	})
	wantProblem(t, content.Check(f, func([]string) bool { return true }), "standard/standard.md", "duplicate candidate waste: lingering-expand")
}

func TestCatalogComesFromTheStandard(t *testing.T) {
	// R1 names W2; without W2's catalog row it names an unknown waste.
	f := standardWith(t, func(s string) string {
		var keep []string
		for _, line := range strings.Split(s, "\n") {
			if !strings.HasPrefix(line, "| W2 |") {
				keep = append(keep, line)
			}
		}
		return strings.Join(keep, "\n")
	})
	wantProblem(t, content.Check(f, func([]string) bool { return true }), "recipes/R1.md", "unknown waste: W2")
}
