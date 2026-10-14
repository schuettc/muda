package content

import (
	"fmt"
	"io/fs"
	"regexp"
	"strings"
)

// Vocabulary is what a finding may name, read from the content and never
// kept in Go: the standard's catalog classes, its candidate-waste slugs and
// the recipe IDs.
type Vocabulary struct {
	Wastes     []string
	Candidates []string
	Recipes    []string
}

// ReadVocabulary reads the standard and the recipes.
func ReadVocabulary(fsys fs.FS) (Vocabulary, error) {
	var v Vocabulary
	b, err := fs.ReadFile(fsys, "standard/standard.md")
	if err != nil {
		return v, err
	}
	if v.Wastes, err = wastes(string(b)); err != nil {
		return v, err
	}
	if v.Candidates, err = candidates(string(b)); err != nil {
		return v, err
	}
	rs, err := Recipes(fsys)
	if err != nil {
		return v, err
	}
	for _, r := range rs {
		v.Recipes = append(v.Recipes, r.ID)
	}
	return v, nil
}

// KnownWaste reports whether w is a class in the standard's catalog.
func KnownWaste(fsys fs.FS, w string) (bool, error) {
	b, err := fs.ReadFile(fsys, "standard/standard.md")
	if err != nil {
		return false, err
	}
	ws, err := wastes(string(b))
	return contains(ws, w), err
}

var (
	catalogRow    = regexp.MustCompile(`^\| (W[1-9][0-9]*) \|`)
	candidateSlug = regexp.MustCompile("^- `([a-z0-9]+(?:-[a-z0-9]+)*)`")
)

// section returns the lines under a "## " heading, up to the next one.
func section(text, heading string) ([]string, bool) {
	var lines []string
	in, found := false, false
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(line, "## ") {
			in = strings.TrimSpace(strings.TrimPrefix(line, "## ")) == heading
			found = found || in
			continue
		}
		if in {
			lines = append(lines, line)
		}
	}
	return lines, found
}

// wastes reads the W-classes from the catalog table's rows.
func wastes(text string) ([]string, error) {
	lines, _ := section(text, "Catalog")
	var ws []string
	for _, line := range lines {
		if m := catalogRow.FindStringSubmatch(line); m != nil {
			if contains(ws, m[1]) {
				return nil, fmt.Errorf("duplicate catalog class: %s", m[1])
			}
			ws = append(ws, m[1])
		}
	}
	if len(ws) == 0 {
		return nil, fmt.Errorf("standard has no catalog classes")
	}
	return ws, nil
}

// candidates reads the slug that starts each candidate-waste bullet.
func candidates(text string) ([]string, error) {
	lines, ok := section(text, "Candidate wastes")
	if !ok {
		return nil, fmt.Errorf("standard has no candidate wastes section")
	}
	var cs []string
	for _, line := range lines {
		if !strings.HasPrefix(line, "- ") {
			continue
		}
		m := candidateSlug.FindStringSubmatch(line)
		if m == nil {
			return nil, fmt.Errorf("candidate waste without a slug: %s", line)
		}
		if contains(cs, m[1]) {
			return nil, fmt.Errorf("duplicate candidate waste: %s", m[1])
		}
		cs = append(cs, m[1])
	}
	return cs, nil
}
