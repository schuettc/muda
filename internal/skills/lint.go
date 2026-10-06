// Package skills validates and installs the bundled agent instructions.
package skills

import (
	"bytes"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/schuettc/muda/internal/content"
	"gopkg.in/yaml.v3"
)

type Problem = content.Problem

// Names is the final distribution contract, even during partial authoring.
var names = []string{"muda", "muda-diagnose", "muda-fix", "muda-measure", "muda-verify"}

const bootstrap = "curl -fsSL https://muda.tools/install.sh | sh"

type metadata struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Version     string `yaml:"muda-version"`
	Owned       string `yaml:"muda-owned"`
}

func frontmatter(b []byte) (metadata, string, error) {
	var m metadata
	if !bytes.HasPrefix(b, []byte("---\n")) {
		return m, "", fmt.Errorf("missing frontmatter")
	}
	end := bytes.Index(b[4:], []byte("\n---\n"))
	if end < 0 {
		return m, "", fmt.Errorf("missing frontmatter terminator")
	}
	if err := yaml.Unmarshal(b[4:4+end], &m); err != nil {
		return m, "", err
	}
	return m, string(b[4+end+5:]), nil
}
func knownName(n string) bool {
	for _, s := range names {
		if n == s {
			return true
		}
	}
	return false
}
func owned(b []byte, n string) bool {
	m, _, e := frontmatter(b)
	return e == nil && m.Name == n && m.Owned == n && knownName(n)
}

var (
	inline     = regexp.MustCompile("`([^`\n]+)`")
	checkpoint = regexp.MustCompile(`(?m)^\*\*CHECKPOINT:\*\*`)
)

// commandLine returns a documented command line with an optional console
// prompt (`$ `) removed, or false when the line is not a muda/curl command.
func commandLine(line string) (string, bool) {
	c := strings.TrimSpace(line)
	if strings.HasPrefix(c, "$ ") {
		c = strings.TrimSpace(c[2:])
	}
	return c, c == "muda" || strings.HasPrefix(c, "muda ") || strings.HasPrefix(c, "curl ")
}

// evidenceTarget accepts only a non-empty literal path ending in .json: no
// tilde, glob or brace expansion (variables are already rejected by the
// parser), and never inside .muda/, in any spelling of that segment
// (case-insensitive filesystems included): only muda record writes there.
func evidenceTarget(t string) bool {
	if t == "" || !strings.HasSuffix(t, ".json") || strings.HasSuffix(t, "/.json") || t == ".json" ||
		strings.HasPrefix(t, "~") || strings.ContainsAny(t, "*?[]{}\n\t ") {
		return false
	}
	for _, seg := range strings.Split(t, "/") {
		if strings.EqualFold(seg, ".muda") {
			return false
		}
	}
	return true
}

// jsonDefault lists the commands whose default output is JSON (a --format
// flag defaulting to json). It is explicit because Lint receives only an
// argument checker; cmd/muda.TestJSONRedirectCommandsMatchCLI verifies it
// against the real CLI metadata. record writes acknowledgements or reports
// for other subcommands, so only `record show` is a redirectable snapshot.
var jsonDefault = map[string]bool{"measure": true, "scan": true, "notices": true, "gates": true, "logs": true, "compare": true}

// JSONOutputCommand reports whether muda arguments (without the leading
// "muda") name a command whose default output is JSON, so it may be
// redirected to a .json evidence file.
func JSONOutputCommand(args []string) bool {
	if len(args) == 0 {
		return false
	}
	if args[0] == "record" {
		return len(args) > 1 && args[1] == "show"
	}
	return jsonDefault[args[0]]
}

// jsonOutput reports whether the muda arguments leave the output format at
// its default (json) or select json explicitly; a .json target must get JSON.
func jsonOutput(args []string) bool {
	for i, a := range args {
		if a == "--" {
			break
		}
		name, value, inline := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if !strings.HasPrefix(a, "-") || name != "format" {
			continue
		}
		if !inline {
			if i+1 >= len(args) {
				return false
			}
			value = args[i+1]
		}
		if value != "json" {
			return false
		}
	}
	return true
}

// Lint always requires the final five-skill set. The callback is a pure
// CommandChecker built from the real app, never an executing dispatcher.
func Lint(fsys fs.FS, knownCommand func([]string) bool) []Problem {
	var ps []Problem
	add := func(p, s string) { ps = append(ps, Problem{Path: p, Message: s}) }
	entries, err := fs.ReadDir(fsys, "skills")
	if err != nil {
		add("skills", err.Error())
	}
	for _, e := range entries {
		if e.IsDir() && !knownName(e.Name()) {
			add("skills/"+e.Name(), "unknown skill directory")
		}
	}
	for _, n := range names {
		p := path.Join("skills", n, "SKILL.md")
		b, e := fs.ReadFile(fsys, p)
		if e != nil {
			add("skills", "missing skill: "+n)
			continue
		}
		m, body, e := frontmatter(b)
		if e != nil {
			add(p, e.Error())
			continue
		}
		if m.Name != n {
			add(p, "declared name must match directory")
		}
		if m.Owned != n {
			add(p, "missing exact muda-owned marker")
		}
		if !strings.HasPrefix(m.Description, "Use when ") || len(m.Description) > 1024 {
			add(p, "description must be trigger-only: Use when...")
		}
		if m.Version != ">=0.1.0" {
			add(p, "muda-version must be >=0.1.0")
		}
		if !strings.Contains(body, "## Preflight") || !strings.Contains(body, "muda version") || !strings.Contains(body, bootstrap) || !strings.Contains(body, "muda update") {
			add(p, "missing preflight/install/update")
		}
		if n != "muda" && !checkpoint.MatchString(body) {
			add(p, "missing CHECKPOINT line")
		}
		validate := func(text string) {
			if strings.TrimSpace(text) == bootstrap {
				return
			} // Only this literal bootstrap is permitted.
			// A single `muda … > literal.json` redirect is the evidence handoff:
			// the shell writes the file, the human reviews it, and only
			// `muda record … --file` records it. The muda side is still checked.
			cmds, e := content.ShellRedirectCommands(text)
			if e != nil {
				add(p, e.Error())
				return
			}
			for _, c := range cmds {
				a := c.Args
				if len(a) < 2 || a[0] != "muda" || knownCommand == nil || !knownCommand(a[1:]) {
					add(p, "unknown command: "+strings.Join(a, " "))
				}
				if c.HasRedirect && !evidenceTarget(c.Redirect) {
					add(p, "redirect target must be a literal .json path outside .muda/: "+strconv.Quote(c.Redirect))
				}
				if c.HasRedirect && (len(a) < 2 || !JSONOutputCommand(a[1:])) {
					add(p, "a redirect is only for commands whose default output is JSON: "+strings.Join(a, " "))
				}
				if c.HasRedirect && !jsonOutput(a) {
					add(p, "a redirect to .json requires JSON output (no --format md)")
				}
			}
		}
		fence, lang := "", ""
		var block strings.Builder
		for _, line := range strings.Split(body, "\n") {
			s := strings.TrimSpace(line)
			if fence != "" {
				if strings.HasPrefix(s, fence) {
					if lang == "sh" || lang == "bash" || lang == "shell" {
						validate(block.String())
					} else {
						// Any fence (unlabeled, text, console...) may not hide an
						// unchecked muda or curl command line.
						for _, l := range strings.Split(block.String(), "\n") {
							if c, ok := commandLine(l); ok {
								validate(c)
							}
						}
					}
					fence = ""
				} else {
					block.WriteString(line + "\n")
				}
				continue
			}
			if strings.HasPrefix(s, "```") || strings.HasPrefix(s, "~~~") {
				fence = s[:3]
				lang = strings.TrimSpace(s[3:])
				block.Reset()
				continue
			}
			// Indented (4-space or tab) code block lines are commands too.
			if strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t") {
				if c, ok := commandLine(line); ok {
					validate(c)
					continue
				}
			}
			for _, match := range inline.FindAllStringSubmatch(line, -1) {
				if c, ok := commandLine(match[1]); ok {
					validate(c)
				}
			}
		}
		if fence != "" {
			add(p, "unterminated fenced block")
		}
	}
	sort.Slice(ps, func(i, j int) bool {
		if ps[i].Path == ps[j].Path {
			return ps[i].Message < ps[j].Message
		}
		return ps[i].Path < ps[j].Path
	})
	return ps
}
