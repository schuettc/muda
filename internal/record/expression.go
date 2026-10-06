package record

import "strings"

// Context roots and functions a credential-named value may reference. The
// grammar is deliberately narrow: no string, numeric or other literals beyond
// true, false and null; no index syntax, wildcards or unknown names.
var (
	expressionContexts  = map[string]bool{"secrets": true, "github": true, "inputs": true, "env": true, "vars": true, "needs": true, "steps": true, "matrix": true, "jobs": true, "runner": true, "strategy": true}
	expressionFunctions = map[string]bool{"contains": true, "startsWith": true, "endsWith": true, "format": true, "join": true, "toJSON": true, "fromJSON": true, "hashFiles": true, "success": true, "always": true, "cancelled": true, "failure": true}
	expressionLiterals  = map[string]bool{"true": true, "false": true, "null": true}
	expressionOperators = []string{"||", "&&", "==", "!=", "<=", ">=", "<", ">"}
)

// credentialExpression reports whether value is exactly one whole
// `${{ … }}` expression composed only of context references (a.b.c),
// operators, parentheses and calls of known functions whose arguments are
// themselves such expressions.
func credentialExpression(value string) bool {
	inner, ok := strings.CutPrefix(value, "${{")
	if !ok {
		return false
	}
	inner, ok = strings.CutSuffix(inner, "}}")
	if !ok || strings.Contains(inner, "${{") || strings.Contains(inner, "}}") {
		return false
	}
	tokens, ok := expressionTokens(inner)
	if !ok || len(tokens) == 0 {
		return false
	}
	p := &exprParser{tokens: tokens}
	return p.expr(0) && p.pos == len(p.tokens)
}

// expressionTokens splits into identifiers (dotted paths), punctuation and
// operators. Any other character (quotes, digits, brackets, '*') fails.
func expressionTokens(s string) ([]string, bool) {
	var out []string
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '(' || c == ')' || c == ',':
			out = append(out, string(c))
			i++
		case isIdentStart(c):
			j := i
			for j < len(s) && (isIdentStart(s[j]) || s[j] == '-' || s[j] == '.' || (s[j] >= '0' && s[j] <= '9')) {
				j++
			}
			out = append(out, s[i:j])
			i = j
		default:
			matched := false
			for _, op := range expressionOperators {
				if strings.HasPrefix(s[i:], op) {
					out = append(out, op)
					i += len(op)
					matched = true
					break
				}
			}
			if !matched && c == '!' {
				out = append(out, "!")
				i++
				matched = true
			}
			if !matched {
				return nil, false
			}
		}
	}
	return out, true
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

type exprParser struct {
	tokens []string
	pos    int
}

func (p *exprParser) peek() string {
	if p.pos < len(p.tokens) {
		return p.tokens[p.pos]
	}
	return ""
}

func isBinary(t string) bool {
	for _, op := range expressionOperators {
		if t == op {
			return true
		}
	}
	return false
}

// expr := unary (binary unary)*
func (p *exprParser) expr(depth int) bool {
	if depth > 64 {
		return false
	}
	if !p.unary(depth) {
		return false
	}
	for isBinary(p.peek()) {
		p.pos++
		if !p.unary(depth) {
			return false
		}
	}
	return true
}

// unary := '!'* primary
func (p *exprParser) unary(depth int) bool {
	for p.peek() == "!" {
		p.pos++
	}
	return p.primary(depth)
}

// primary := '(' expr ')' | function '(' [expr (',' expr)*] ')' | reference | true | false | null
func (p *exprParser) primary(depth int) bool {
	t := p.peek()
	switch {
	case t == "(":
		p.pos++
		if !p.expr(depth+1) || p.peek() != ")" {
			return false
		}
		p.pos++
		return true
	case t == "" || !isIdentStart(t[0]):
		return false
	}
	p.pos++
	if p.peek() == "(" {
		if !expressionFunctions[t] {
			return false
		}
		p.pos++
		if p.peek() == ")" {
			p.pos++
			return true
		}
		for {
			if !p.expr(depth + 1) {
				return false
			}
			switch p.peek() {
			case ",":
				p.pos++
			case ")":
				p.pos++
				return true
			default:
				return false
			}
		}
	}
	if expressionLiterals[t] {
		return true
	}
	return validReference(t)
}

// validReference is a known context root followed by one or more
// identifier segments, as in secrets.PAT or steps.app.outputs.token.
func validReference(t string) bool {
	parts := strings.Split(t, ".")
	if len(parts) < 2 || !expressionContexts[parts[0]] {
		return false
	}
	for _, part := range parts[1:] {
		if part == "" || !isIdentStart(part[0]) {
			return false
		}
	}
	return true
}
