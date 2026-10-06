// Package pyfmt preserves Python Unicode semantics for the archived signature normalizer.
package pyfmt

import (
	"strings"
	"unicode"
)

func IsSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', 0x1c, 0x1d, 0x1e, 0x1f, ' ', 0x85, 0xa0,
		0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// IsWord is re's \w on a str pattern: str.isalnum() or "_".
func IsWord(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsNumber(r)
}

// IsDigit is re's \d on a str pattern: a Unicode decimal digit (Nd).
func IsDigit(r rune) bool { return unicode.IsDigit(r) }

func Strip(s string) string { return strings.TrimFunc(s, IsSpace) }
