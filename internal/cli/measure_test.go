package cli

import (
	"strings"
	"testing"
)

// TestMeasureHelpStatesTheHistoryBasis: the help says where changes are
// placed and that a pull request's version is approximate.
func TestMeasureHelpStatesTheHistoryBasis(t *testing.T) {
	help := strings.Join(strings.Fields(measureHelp), " ")
	for _, s := range []string{"default branch's push, schedule and workflow_dispatch runs", "branch-only, never window changes",
		"a pull request's is approximate, since GitHub runs its merge ref, not its head", "never \"no change\"", "low below 10, medium below 30, high from 30"} {
		if !strings.Contains(help, s) {
			t.Errorf("measure help lacks %q", s)
		}
	}
}
