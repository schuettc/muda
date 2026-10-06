package cli

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"

	tools "github.com/schuettc/tools-common"
)

// CommandChecker snapshots the assembled app's commands metadata. It never
// dispatches the supplied examples or runs their handlers. This recognizes
// registered names and flags, not runtime facts (tokens, files, dates, repos).
// The only dispatched command is the family's side-effect-free metadata query.
func CommandChecker(app *tools.App) (func([]string) bool, error) {
	var out, errs bytes.Buffer
	if code := app.Dispatch([]string{"commands", "--json"}, &out, &errs); code != 0 {
		return nil, fmt.Errorf("command metadata: %s", errs.String())
	}
	type flagInfo struct{ Name, Type string }
	type commandInfo struct {
		Name, Synopsis, Help string
		Aliases, Subcommands []string
		Flags                []flagInfo
	}
	var commands []commandInfo
	if err := json.Unmarshal(out.Bytes(), &commands); err != nil {
		return nil, err
	}
	registry := map[string]commandInfo{}
	for _, c := range commands {
		registry[c.Name] = c
		for _, a := range c.Aliases {
			registry[a] = c
		}
	}
	return func(args []string) bool {
		if len(args) == 0 {
			return false
		}
		name := args[0]
		switch name {
		case "--version", "-v":
			name = "version"
		case "--help", "-h":
			name = "help"
		}
		c, ok := registry[name]
		if !ok {
			return false
		}
		rest := args[1:]
		// Help consumes raw args, rather than a synthetic FlagSet, and resolves
		// its first target exactly as App.lookup does (aliases too). The real
		// handler ignores anything after that target; documentation examples
		// must not rely on that, so only one record verb that the actual
		// record parser accepts on its own may follow (`help record check`).
		if c.Name == "help" {
			if len(rest) == 0 {
				return true
			}
			target, ok := registry[rest[0]]
			switch {
			case !ok:
				return false
			case len(rest) == 1:
				return true
			case len(rest) == 2 && target.Name == "record":
				_, _, _, err := recordAction(rest[1:])
				return err == nil
			}
			return false
		}
		// Mirror App's side-effect-free top-level help routing. Subcommands
		// still go through their own pure parser, including unknown verbs.
		topHelp := len(c.Subcommands) == 0 || len(rest) == 0 || rest[0] == "-h" || rest[0] == "--help"
		if topHelp && (c.Synopsis != "" || c.Help != "" || len(c.Flags) > 0) {
			for _, a := range rest {
				if a == "-h" || a == "--help" {
					return true
				}
			}
		}
		if c.Name == "standard" {
			return validateStandardArgs(rest) == nil
		}
		if c.Name == "skills" {
			_, _, err := parseSkills(rest, io.Discard)
			return err == nil
		}
		// Reuse the actual record subcommand parser, without reading or writing a record.
		if c.Name == "record" {
			_, _, r, e := recordAction(rest)
			if e != nil {
				return false
			}
			rest = r
		}
		// Reconstruct only the registered flag syntax. Use flag.Parse just as
		// the handlers do, including its stop-at-positional and -- behavior.
		fs := flag.NewFlagSet(c.Name, flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		for _, f := range c.Flags {
			switch f.Type {
			case "", "bool":
				fs.Bool(f.Name, false, "")
			case "int":
				fs.Int(f.Name, 0, "")
			case "int64":
				fs.Int64(f.Name, 0, "")
			case "uint":
				fs.Uint(f.Name, 0, "")
			case "uint64":
				fs.Uint64(f.Name, 0, "")
			case "float64":
				fs.Float64(f.Name, 0, "")
			case "string":
				fs.String(f.Name, "", "")
			default:
				return false // Do not guess an unsupported metadata type.
			}
		}
		// These are the shipped handlers' positional contracts, not a list of
		// future commands. Metadata alone does not express required arguments.
		if c.Name == "logs" && len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
			rest = append(append([]string{}, rest[1:]...), rest[0])
		}
		if c.Name == "recipe" {
			if len(rest) == 0 || strings.HasPrefix(rest[0], "-") {
				return false
			}
			rest = rest[1:]
		}
		if err := fs.Parse(rest); err != nil {
			return false
		}
		switch c.Name {
		case "logs":
			if fs.NArg() != 1 {
				return false
			}
			n, err := strconv.ParseInt(fs.Arg(0), 10, 64)
			return err == nil && n > 0
		case "compare":
			if fs.NArg() != 0 {
				return false
			}
			value := func(name string) string { return fs.Lookup(name).Value.String() }
			for _, phase := range []string{"before", "after"} {
				if value(phase) != "" && value(phase+"-runs") != "" {
					return false
				}
				if _, err := parsePhase(value(phase), value(phase+"-runs")); err != nil {
					return false
				}
			}
			return true
		case "measure", "scan", "gates", "notices", "record", "recipes", "recipe":
			return fs.NArg() == 0
		// Family built-ins ignore extra arguments at run time; documentation
		// examples must not rely on that.
		case "version", "update", "man", "commands":
			return fs.NArg() == 0
		default:
			return false // Registered metadata cannot prove an unknown argument contract.
		}
	}, nil
}
