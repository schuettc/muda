package cli

import (
	"testing"
)

func TestComparePhaseFlags(t *testing.T) {
	for _, tc := range []struct {
		window, ids string
		valid       bool
	}{
		{"2026-09-01..2026-09-02", "", true}, {"2026-09-01T00:00:00Z..2026-09-02T00:00:00Z", "", true}, {"", "1,42", true},
		{"", "", false}, {"2026-09-02..2026-09-01", "", false}, {"2026-09-01..2026-09-01", "", false}, {"2026-09-01..2026-09-02", "1", false}, {"", "0", false}, {"", "-1", false}, {"", "1,1", false}, {"", "1,", false}, {"garbage", "", false},
	} {
		_, err := parsePhase(tc.window, tc.ids)
		if (err == nil) != tc.valid {
			t.Errorf("%q %q: %v", tc.window, tc.ids, err)
		}
	}
}
