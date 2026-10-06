package content

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"
	"unicode"
)

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
func (g *FactGuard) Find(text string) string {
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
