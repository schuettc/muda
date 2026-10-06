// Package content reads and validates the embedded delivery-waste library.
package content

import (
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Recipe is a recipe's metadata and Markdown, without frontmatter.
type Recipe struct {
	ID            string            `yaml:"id"`
	Title         string            `yaml:"title"`
	Waste         []string          `yaml:"waste"`
	Signals       []string          `yaml:"signals"`
	Stacks        []string          `yaml:"stacks"`
	Body          string            `yaml:"-"`
	Sections      map[string]string `yaml:"-"`
	StackSections map[string]string `yaml:"-"`
}

var SectionNames = []string{"Symptom", "Detect", "Fix", "Stacks", "Traps", "Verify", "Evidence"}

// Recipes parses recipes in numeric ID order. Malformed metadata is an error;
// missing or unknown sections are reported by Check, not hidden by parsing.
func Recipes(fsys fs.FS) ([]Recipe, error) {
	entries, err := fs.ReadDir(fsys, "recipes")
	if err != nil {
		return nil, fmt.Errorf("read content: %w", err)
	}
	var paths []string
	for _, entry := range entries {
		if !entry.IsDir() && path.Ext(entry.Name()) == ".md" {
			paths = append(paths, path.Join("recipes", entry.Name()))
		}
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("empty recipe inventory")
	}
	rs := make([]Recipe, 0, len(paths))
	ids := map[string]bool{}
	for _, p := range paths {
		b, err := fs.ReadFile(fsys, p)
		if err != nil {
			return nil, err
		}
		r, err := parse(string(b))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		if ids[r.ID] {
			return nil, fmt.Errorf("%s: duplicate recipe ID %s", p, r.ID)
		}
		ids[r.ID] = true
		rs = append(rs, r)
	}
	sort.Slice(rs, func(i, j int) bool {
		a, _ := strconv.Atoi(strings.TrimPrefix(rs[i].ID, "R"))
		b, _ := strconv.Atoi(strings.TrimPrefix(rs[j].ID, "R"))
		if a == b {
			return rs[i].ID < rs[j].ID
		}
		return a < b
	})
	return rs, nil
}
func parse(text string) (Recipe, error) {
	r := Recipe{Sections: map[string]string{}, StackSections: map[string]string{}}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return r, fmt.Errorf("missing frontmatter")
	}
	parts := strings.SplitN(strings.TrimPrefix(text, "---\n"), "\n---\n", 2)
	if len(parts) != 2 {
		return r, fmt.Errorf("unterminated frontmatter")
	}
	dec := yaml.NewDecoder(strings.NewReader(parts[0]))
	dec.KnownFields(true)
	if err := dec.Decode(&r); err != nil {
		return r, err
	}
	if r.ID == "" || r.Title == "" || len(r.Waste) == 0 || len(r.Signals) == 0 || len(r.Stacks) == 0 {
		return r, fmt.Errorf("missing required metadata")
	}
	r.Body = strings.TrimSpace(parts[1]) + "\n"
	section, stack, fence := "", "", ""
	lastSection := -1
	for _, line := range strings.Split(r.Body, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case fence != "":
			if strings.HasPrefix(trimmed, fence) {
				fence = ""
			}
		case strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~"):
			fence = trimmed[:3]
		case strings.HasPrefix(line, "## "):
			section = strings.TrimPrefix(line, "## ")
			for i, name := range SectionNames {
				if section == name {
					if i <= lastSection {
						return r, fmt.Errorf("out-of-order section %s", section)
					}
					lastSection = i
				}
			}
			stack = ""
			if _, ok := r.Sections[section]; ok {
				return r, fmt.Errorf("duplicate section %s", section)
			}
			r.Sections[section] = ""
			continue
		case section == "Stacks" && strings.HasPrefix(line, "### "):
			stack = strings.TrimPrefix(line, "### ")
			if _, ok := r.StackSections[stack]; ok {
				return r, fmt.Errorf("duplicate stack section %s", stack)
			}
			r.StackSections[stack] = ""
		}
		if section != "" {
			r.Sections[section] += line + "\n"
		}
		if stack != "" {
			r.StackSections[stack] += line + "\n"
		}
	}
	return r, nil
}

// Stacks reads the shipped stack catalog.
func Stacks(fsys fs.FS) ([]string, error) {
	b, err := fs.ReadFile(fsys, "recipes/stacks.txt")
	if err != nil {
		return nil, err
	}
	return strings.Fields(string(b)), nil
}

// KnownWaste reports membership of the standard's W1–W10 catalog.
func KnownWaste(w string) bool {
	n, err := strconv.Atoi(strings.TrimPrefix(w, "W"))
	return err == nil && n >= 1 && n <= 10 && w == fmt.Sprintf("W%d", n)
}
