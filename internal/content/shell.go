package content

import (
	"fmt"
	"strings"
)

// shellCommands accepts simple literal commands, quoted arguments, comments,
// and line continuations. Operators/expansions are deliberately rejected: a
// checker must not silently bless a command it cannot understand.
func shellCommands(text string) ([][]string, error) {
	cmds, err := parseShell(text, false)
	if err != nil {
		return nil, err
	}
	out := make([][]string, 0, len(cmds))
	for _, c := range cmds {
		out = append(out, c.Args)
	}
	return out, nil
}

// ShellCommand is one parsed literal command. Redirect is the literal target
// of a single trailing `>` redirect, or empty.
type ShellCommand struct {
	Args []string
	// HasRedirect marks a redirect explicitly: a quoted empty target ("" or
	// '') has Redirect == "" and must still be validated, never skipped.
	HasRedirect bool
	Redirect    string
}

// parseShell optionally accepts exactly one trailing ` > word` per command:
// the `>` must stand alone (not `2>`, `>>`, `>&`, `>|`), be followed by exactly
// one target word and nothing else. All other operators stay rejected.
func parseShell(text string, allowRedirect bool) ([]ShellCommand, error) {
	var commands []ShellCommand
	var words []string
	var word strings.Builder
	quote := byte(0)
	active := false
	// redirect: 0 none, 1 awaiting target, 2 target read.
	redirect := 0
	target := ""
	var fail error
	flushWord := func() {
		if active {
			switch redirect {
			case 0:
				words = append(words, word.String())
			case 1:
				target = word.String()
				redirect = 2
			default:
				fail = fmt.Errorf("only one redirect target, at the end of the command")
			}
			word.Reset()
			active = false
		}
	}
	flushLine := func() {
		flushWord()
		if redirect == 1 {
			fail = fmt.Errorf("redirect without a target")
		}
		if redirect != 0 && len(words) == 0 {
			fail = fmt.Errorf("redirect without a command")
		}
		if len(words) > 0 {
			commands = append(commands, ShellCommand{Args: words, HasRedirect: redirect == 2, Redirect: target})
			words = nil
		}
		redirect, target = 0, ""
	}
	for i := 0; i < len(text) && fail == nil; i++ {
		c := text[i]
		if quote == '\'' {
			if c == '\'' {
				quote = 0
			} else {
				word.WriteByte(c)
			}
			continue
		}
		if c == '\\' {
			if i+1 == len(text) {
				return nil, fmt.Errorf("dangling shell escape")
			}
			i++
			next := text[i]
			if next == '\n' {
				continue
			}
			if quote == '"' && !strings.ContainsRune("$`\"\\", rune(next)) {
				word.WriteByte('\\')
			}
			word.WriteByte(next)
			active = true
			continue
		}
		if quote == '"' {
			if c == '"' {
				quote = 0
				continue
			}
			if c == '$' || c == '`' {
				return nil, fmt.Errorf("unsupported shell expansion")
			}
			word.WriteByte(c)
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
			active = true
		case '#':
			if !active {
				for i < len(text) && text[i] != '\n' {
					i++
				}
				flushLine()
			} else {
				word.WriteByte(c)
			}
		case ' ', '\t', '\r':
			flushWord()
		case '\n':
			flushLine()
		case '>':
			if !allowRedirect {
				return nil, fmt.Errorf("unsupported shell operator or expansion %q", c)
			}
			if active || redirect != 0 {
				return nil, fmt.Errorf("unsupported redirect: one standalone > per command")
			}
			if i+1 < len(text) && text[i+1] != ' ' && text[i+1] != '\t' {
				return nil, fmt.Errorf("unsupported redirect form >%c", text[i+1])
			}
			redirect = 1
		case ';', '|', '&', '<', '(', ')', '$', '`':
			return nil, fmt.Errorf("unsupported shell operator or expansion %q", c)
		default:
			word.WriteByte(c)
			active = true
		}
	}
	if fail != nil {
		return nil, fail
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated shell quote")
	}
	flushLine()
	if fail != nil {
		return nil, fail
	}
	return commands, nil
}

// ShellCommands parses literal documentation examples without executing them.
func ShellCommands(text string) ([][]string, error) { return shellCommands(text) }

// ShellRedirectCommands is ShellCommands plus at most one trailing standalone
// `>` redirect per command. Callers must still validate the target.
func ShellRedirectCommands(text string) ([]ShellCommand, error) { return parseShell(text, true) }
