package cli

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/schuettc/muda"
	"github.com/schuettc/muda/internal/record"
	"github.com/schuettc/muda/internal/skills"
	tools "github.com/schuettc/tools-common"
)

func skillsFlags() *flag.FlagSet {
	f := flag.NewFlagSet("skills", flag.ContinueOnError)
	f.String("agent", "claude", "claude, pi or codex")
	f.String("scope", "user", "user or project")
	tools.SetUsage(f, "muda skills install [--agent claude|pi|codex] [--scope user|project]", "Install bundled, muda-owned skills. No GitHub authentication or network. Existing foreign files and symlinks are refused. This explicit install is an exception to the normal .muda-only record write boundary.")
	return f
}

// parseSkills is shared with the pure metadata command checker.
func parseSkills(args []string, out io.Writer) (string, string, error) {
	if len(args) == 0 || args[0] != "install" {
		return "", "", tools.UsageError{Msg: "expected skills install"}
	}
	f := skillsFlags()
	if e := tools.ParseFlags(f, args[1:], out); e != nil {
		return "", "", e
	}
	a, s := f.Lookup("agent").Value.String(), f.Lookup("scope").Value.String()
	if f.NArg() != 0 || (a != "claude" && a != "pi" && a != "codex") || (s != "user" && s != "project") {
		return "", "", tools.UsageError{Msg: "expected --agent claude|pi|codex and --scope user|project; no positional arguments"}
	}
	return a, s, nil
}
func Skills() tools.Command {
	return tools.Command{Name: "skills", Summary: "install bundled agent skills", Synopsis: "install [--agent claude|pi|codex] [--scope user|project]", NewFlags: skillsFlags, Run: func(args []string, out, _ io.Writer) error {
		if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
			return tools.ParseFlags(skillsFlags(), args, out)
		}
		a, s, e := parseSkills(args, out)
		if e != nil {
			return e
		}
		var home, root string
		if s == "user" {
			home, e = os.UserHomeDir()
		} else {
			var cwd string
			cwd, e = os.Getwd()
			if e == nil {
				root, e = record.Discover(cwd)
			}
		}
		if e != nil {
			return e
		}
		paths, e := skills.Install(muda.Content, a, s, home, root)
		if e != nil {
			return e
		}
		for _, p := range paths {
			if _, e = fmt.Fprintln(out, p); e != nil {
				return e
			}
		}
		return nil
	}}
}
