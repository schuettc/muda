package record

import (
	"slices"
	"strings"
	"sync"

	"github.com/subaud-tools/muda"
	"github.com/subaud-tools/muda/internal/content"
)

// candidatePrefix marks a waste from the standard's candidate list.
const candidatePrefix = "candidate:"

var shipped = sync.OnceValues(func() (content.Vocabulary, error) { return content.ReadVocabulary(muda.Content) })

func (s *Store) vocabulary(file string) (content.Vocabulary, error) {
	if s.Vocab != nil {
		return *s.Vocab, nil
	}
	v, err := shipped()
	if err != nil {
		return v, problem("invalid-vocabulary", file, err.Error())
	}
	return v, nil
}

// checkWaste accepts a catalog W-class or candidate:<slug> for a listed
// candidate, and reports which it is.
func checkWaste(v content.Vocabulary, file, id, waste string) (candidate bool, err error) {
	if slug, ok := strings.CutPrefix(waste, candidatePrefix); ok {
		if slices.Contains(v.Candidates, slug) {
			return true, nil
		}
	} else if slices.Contains(v.Wastes, waste) {
		return false, nil
	}
	return false, problem("unknown-waste", file, id+": "+waste)
}
