package content

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

// Home is muda's own repository, where recipe submissions go. Shipped content
// may name it exactly; any other use of its owner is still a project fact.
const Home = "subaud-tools/muda"

var homeRef = regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(Home) + `(?:$|[^A-Za-z0-9_-])`)

// shippedFactHashes are SHA-256 digests (lowercase hex) of the lowercased
// project words that must not appear in shipped content. Only digests are
// kept so the guard works without naming the words. To add a word, append
// the digest of its lowercase singular form:
//
//	printf %s word | shasum -a 256
var shippedFactHashes = []string{
	"7ac3743740771bd0fdab9fe3865e50beb8a01b712ed764edeced3e0f3729f3d9",
	"196e2aa52f81dffeeaad4e6ee89acbf57bc6a98e31f2e286fdf57bc7f3258190",
	"0bda61a54af46aaba244fa8773128e07b5b12a1ad4f65b985698f668542c97d7",
	"8c268e28807bd7672f203082152060e8ea43f3475b6318aa6536b3cf30525e36",
	"8575e43e52cfebdcf89f5f0fd05d8291b5aa3847836edde2705b86b4ca9f524f",
	"9d372c3e496366825b71d5192ceaf6b8e00cbe0b6f33e882b78948597bdb39e1",
}

// ShippedFactHashes returns a copy of the shipped digest set.
func ShippedFactHashes() []string { return slices.Clone(shippedFactHashes) }

// FactGuard finds words whose lowercase SHA-256 digest is in its set.
type FactGuard struct{ hashes map[string]bool }

// NewFactGuard returns a guard over the given lowercase hex digests.
func NewFactGuard(hashes ...string) *FactGuard {
	g := &FactGuard{hashes: make(map[string]bool, len(hashes))}
	for _, h := range hashes {
		g.hashes[strings.ToLower(h)] = true
	}
	return g
}

// ProjectFacts is the guard Check applies to shipped content.
var ProjectFacts = NewFactGuard(shippedFactHashes...)

// Find returns the first word of text that is a project fact, or "". Words
// are maximal runs of letters and digits, compared lowercased; a word that
// matches after dropping one trailing "s" also counts, covering plurals.
// An exact reference to Home is not a word.
func (g *FactGuard) Find(text string) string {
	text = homeRef.ReplaceAllString(text, " ")
	for _, word := range strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		lower := strings.ToLower(word)
		if g.has(lower) {
			return word
		}
		if stem, ok := strings.CutSuffix(lower, "s"); ok && stem != "" && g.has(stem) {
			return word
		}
	}
	return ""
}

func (g *FactGuard) has(word string) bool {
	sum := sha256.Sum256([]byte(word))
	return g.hashes[hex.EncodeToString(sum[:])]
}
