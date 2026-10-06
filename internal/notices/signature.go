package notices

import (
	"github.com/schuettc/muda/internal/pyfmt"
	"strings"
	"unicode/utf8"
)

// W10 advance notices: CI warning annotations normalised to signatures.
//
// An advance notice is a warning annotation GitHub Actions attaches to a
// check run (a deprecation, a scheduled migration, a toolchain heads-up):
// the CI telling us ahead of time that something will break. This module
// never shells out itself; the caller passes a fetch func wrapping
// Gh.Annotations. "warning" and "notice" levels are both kept, since GitHub's
// own notices can arrive at "notice" level (runner-images#14748); a
// "failure" is already a failed run, so it is ignored.
//
// Python's regexes are ported as explicit scans with Python's Unicode \d,
// \w, \s and \b (RE2 has no lookbehind for _PATH), applied in Python's
// order: dates, then hex, then paths, then versions, then whitespace.

// Signature is signature(): cosmetic differences (versions, dates, hashes,
// absolute paths, whitespace) become placeholders; cut to 200 characters.
func Signature(message string) string {
	s := subDates(message)
	s = subHex(s)
	s = subPaths(s)
	s = subVersions(s)
	s = pyfmt.Strip(collapseSpace(s))
	if utf8.RuneCountInString(s) > 200 {
		s = string([]rune(s)[:200])
	}
	return s
}

// digitsAt counts n Unicode decimal digits at rs[i:].
func digitsAt(rs []rune, i, n int) bool {
	if i+n > len(rs) {
		return false
	}
	for _, r := range rs[i : i+n] {
		if !pyfmt.IsDigit(r) {
			return false
		}
	}
	return true
}

// subDates is _DATE.sub("<date>"):
// \d{4}-\d{2}-\d{2}(?:[T ]\d{2}:\d{2}:\d{2}\S*)?
func subDates(s string) string {
	rs := []rune(s)
	var b strings.Builder
	for i := 0; i < len(rs); {
		end := -1
		if digitsAt(rs, i, 4) && i+4 < len(rs) && rs[i+4] == '-' && digitsAt(rs, i+5, 2) &&
			i+7 < len(rs) && rs[i+7] == '-' && digitsAt(rs, i+8, 2) {
			end = i + 10
			j := end
			if j < len(rs) && (rs[j] == 'T' || rs[j] == ' ') && digitsAt(rs, j+1, 2) && j+3 < len(rs) && rs[j+3] == ':' &&
				digitsAt(rs, j+4, 2) && j+6 < len(rs) && rs[j+6] == ':' && digitsAt(rs, j+7, 2) {
				end = j + 9
				for end < len(rs) && !pyfmt.IsSpace(rs[end]) {
					end++
				}
			}
		}
		if end < 0 {
			b.WriteRune(rs[i])
			i++
			continue
		}
		b.WriteString("<date>")
		i = end
	}
	return b.String()
}

func isHex(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
}

// subHex is _HEX.sub("<hex>"): \b[0-9a-fA-F]{12,}\b with Unicode \b. A hex
// run matches only whole: a word character (Python's \w) on either side
// blocks it.
func subHex(s string) string {
	rs := []rune(s)
	var b strings.Builder
	for i := 0; i < len(rs); {
		if isHex(rs[i]) && (i == 0 || !pyfmt.IsWord(rs[i-1])) {
			j := i
			for j < len(rs) && isHex(rs[j]) {
				j++
			}
			if j-i >= 12 && (j == len(rs) || !pyfmt.IsWord(rs[j])) {
				b.WriteString("<hex>")
				i = j
				continue
			}
		}
		b.WriteRune(rs[i])
		i++
	}
	return b.String()
}

func isPathChar(r rune) bool { return pyfmt.IsWord(r) || r == '.' || r == '-' || r == '/' }

// subPaths is _PATH.sub("<path>"): (?<![\w:])/[\w.\-/]+ — a slash not
// preceded by a word character or ':', then at least one path character.
func subPaths(s string) string {
	rs := []rune(s)
	var b strings.Builder
	for i := 0; i < len(rs); {
		if rs[i] == '/' && (i == 0 || (!pyfmt.IsWord(rs[i-1]) && rs[i-1] != ':')) {
			j := i + 1
			for j < len(rs) && isPathChar(rs[j]) {
				j++
			}
			if j > i+1 {
				b.WriteString("<path>")
				i = j
				continue
			}
		}
		b.WriteRune(rs[i])
		i++
	}
	return b.String()
}

// digitRun is the end of the run of Unicode decimal digits at rs[i:].
func digitRun(rs []rune, i int) int {
	for i < len(rs) && pyfmt.IsDigit(rs[i]) {
		i++
	}
	return i
}

// dottedTail consumes (?:\.\d+)* from j and reports how many groups it took.
func dottedTail(rs []rune, j int) (int, int) {
	groups := 0
	for j+1 < len(rs) && rs[j] == '.' && pyfmt.IsDigit(rs[j+1]) {
		j = digitRun(rs, j+1)
		groups++
	}
	return j, groups
}

// versionAt matches _VERSION at rs[i:] and returns its end, or -1:
// @?v\d+(?:\.\d+)* | @?\d+(?:\.\d+)+
func versionAt(rs []rune, i int) int {
	k := i
	if k < len(rs) && rs[k] == '@' {
		k++
	}
	// First alternative: v-prefixed, any number of dotted groups.
	if k+1 < len(rs) && rs[k] == 'v' && pyfmt.IsDigit(rs[k+1]) {
		end, _ := dottedTail(rs, digitRun(rs, k+1))
		return end
	}
	// Second alternative: bare digits with at least one dotted group.
	if k < len(rs) && pyfmt.IsDigit(rs[k]) {
		end, groups := dottedTail(rs, digitRun(rs, k))
		if groups > 0 {
			return end
		}
	}
	return -1
}

// subVersions is _VERSION.sub("<v>").
func subVersions(s string) string {
	rs := []rune(s)
	var b strings.Builder
	for i := 0; i < len(rs); {
		if end := versionAt(rs, i); end > i {
			b.WriteString("<v>")
			i = end
			continue
		}
		b.WriteRune(rs[i])
		i++
	}
	return b.String()
}

// collapseSpace is _WS.sub(" "): each run of Python whitespace becomes one space.
func collapseSpace(s string) string {
	var b strings.Builder
	inSpace := false
	for _, r := range s {
		if pyfmt.IsSpace(r) {
			if !inSpace {
				b.WriteByte(' ')
			}
			inSpace = true
			continue
		}
		inSpace = false
		b.WriteRune(r)
	}
	return b.String()
}
