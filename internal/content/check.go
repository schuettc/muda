package content

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/schuettc/muda/internal/signal"
)

// Problem identifies invalid reference content. Messages are deterministic.
type Problem struct {
	Path    string
	Message string
}

var recipeID = regexp.MustCompile(`^R[1-9][0-9]*$`)

// Check checks metadata, sections, catalog references and literal sh examples.
// knownCommand must recognize arguments without executing a command.
func Check(fsys fs.FS, knownCommand func(args []string) bool) []Problem {
	var problems []Problem
	add := func(p, msg string) { problems = append(problems, Problem{p, msg}) }
	if _, err := fs.ReadFile(fsys, "standard/standard.md"); err != nil {
		add("standard/standard.md", fmt.Sprintf("read required standard: %v", err))
	}
	stacks, err := Stacks(fsys)
	if err != nil {
		add("recipes/stacks.txt", fmt.Sprintf("read stack catalog: %v", err))
	}
	rs, err := Recipes(fsys)
	if err != nil {
		add("recipes", err.Error())
	}
	for _, r := range rs {
		p := "recipes/" + r.ID + ".md"
		if !recipeID.MatchString(r.ID) {
			add(p, "invalid recipe ID")
		}
		for _, w := range r.Waste {
			if !KnownWaste(w) {
				add(p, "unknown waste: "+w)
			}
		}
		for _, s := range r.Signals {
			if !signal.Known(s) {
				add(p, "unknown signal: "+s)
			}
		}
		for _, s := range r.Stacks {
			if !contains(stacks, s) {
				add(p, "unknown stack: "+s)
			}
			if strings.TrimSpace(r.StackSections[s]) == "" {
				add(p, "missing stack section: "+s)
			}
		}
		for s := range r.StackSections {
			if !contains(r.Stacks, s) {
				add(p, "undeclared stack section: "+s)
			}
		}
		for _, s := range SectionNames {
			if strings.TrimSpace(r.Sections[s]) == "" {
				add(p, "missing section: "+s)
			}
		}
		for s := range r.Sections {
			if !contains(SectionNames, s) {
				add(p, "unknown section: "+s)
			}
		}
	}
	// Walk original files too: malformed recipes must not hide bad commands or facts.
	for _, dir := range []string{"standard", "recipes", "skills"} {
		if _, e := fs.Stat(fsys, dir); e != nil {
			if dir != "skills" || !errors.Is(e, fs.ErrNotExist) {
				add(dir, fmt.Sprintf("read content: %v", e))
			}
			continue
		}
		err := fs.WalkDir(fsys, dir, func(p string, d fs.DirEntry, e error) error {
			if e != nil {
				add(p, fmt.Sprintf("read content: %v", e))
				return nil
			}
			if d.IsDir() || path.Ext(p) != ".md" {
				return nil
			}
			b, e := fs.ReadFile(fsys, p)
			if e != nil {
				add(p, fmt.Sprintf("read content: %v", e))
				return nil
			}
			text := string(b)
			if ProjectFacts.Find(text) != "" {
				add(p, "project facts are forbidden")
			}
			// Only a direct skills/<name>/SKILL.md may use the single
			// evidence-handoff redirect, because skills.Lint validates exactly
			// those files (target, JSON output, command). Every other Markdown
			// file, including supporting files under skills/, may not redirect.
			checkExamples(text, dir == "recipes", skillEntry(p), knownCommand, func(msg string) { add(p, msg) })
			return nil
		})
		if err != nil {
			add(dir, fmt.Sprintf("read content: %v", err))
		}
	}
	sort.Slice(problems, func(i, j int) bool {
		if problems[i].Path == problems[j].Path {
			return problems[i].Message < problems[j].Message
		}
		return problems[i].Path < problems[j].Path
	})
	return problems
}

// Check the original Markdown independently of frontmatter parsing, so a
// malformed recipe cannot hide bad examples. Only a recipe's Detect and Verify
// sections carry the example contract: fenced sh, muda commands only, and at
// least one example each. Elsewhere (the standard, skills and other recipe
// sections) an sh fence may show an external bootstrap command, but every muda
// command it documents must be real, and only allowRedirect permits the single
// evidence-handoff redirect.
func checkExamples(text string, recipe, allowRedirect bool, knownCommand func([]string) bool, add func(string)) {
	section, fence, language := "", "", ""
	var block strings.Builder
	examples := map[string]int{}
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		if fence == "" {
			if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
				fence, language = t[:3], strings.TrimSpace(t[3:])
				block.Reset()
			} else if strings.HasPrefix(line, "## ") {
				section = strings.TrimSpace(strings.TrimPrefix(line, "## "))
			}
			continue
		}
		if !strings.HasPrefix(t, fence) {
			block.WriteString(line)
			block.WriteByte('\n')
			continue
		}
		contract := recipe && (section == "Detect" || section == "Verify")
		if contract && language != "sh" {
			add("unsupported example fence language: " + section + ": " + language)
		}
		if contract || language == "sh" {
			commands, err := parseShell(block.String(), allowRedirect)
			if err != nil {
				add(err.Error())
			} else {
				for _, c := range commands {
					args := c.Args
					switch {
					case args[0] != "muda":
						if contract {
							add("unsupported sh command: " + args[0])
						}
					case len(args) == 1:
						add("missing muda command")
					case knownCommand == nil || !knownCommand(args[1:]):
						add("unknown command: " + strings.Join(args[1:], " "))
					case contract && language == "sh":
						examples[section]++
					}
				}
			}
		}
		fence = ""
	}
	if fence != "" {
		add("unterminated fenced block")
	}
	if recipe {
		for _, name := range []string{"Detect", "Verify"} {
			if examples[name] == 0 {
				add("missing sh command example: " + name)
			}
		}
	}
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// skillEntry reports whether p is a direct skills/<name>/SKILL.md.
func skillEntry(p string) bool {
	parts := strings.Split(p, "/")
	return len(parts) == 3 && parts[0] == "skills" && parts[1] != "" && parts[2] == "SKILL.md"
}
