package measure

import (
	"testing"
	"time"

	"github.com/schuettc/muda/internal/signal"
)

func TestStatsPercentiles(t *testing.T) {
	mk := func(minutes ...int) []Sample {
		out := make([]Sample, 0, len(minutes))
		for i, m := range minutes {
			out = append(out, Sample{D: time.Duration(m) * time.Minute, Ev: signal.Evidence{Kind: signal.KindRun, RunID: int64(i + 1), URL: "u"}})
		}
		return out
	}
	for _, tc := range []struct {
		name     string
		in       []Sample
		q        float64
		want     time.Duration
		wantRun  int64
		wantNone bool
	}{
		{"empty", nil, 0.5, 0, 0, true},
		{"single p50", mk(7), 0.5, 7 * time.Minute, 1, false},
		{"single p90", mk(7), 0.9, 7 * time.Minute, 1, false},
		// nearest rank: index ceil(q*n)-1 of the sorted samples
		{"ten p50", mk(10, 9, 8, 7, 6, 5, 4, 3, 2, 1), 0.5, 5 * time.Minute, 6, false},
		{"ten p90", mk(1, 2, 3, 4, 5, 6, 7, 8, 9, 10), 0.9, 9 * time.Minute, 9, false},
		{"three p50", mk(3, 1, 2), 0.5, 2 * time.Minute, 3, false},
		{"four p50 is the lower middle, never an average", mk(1, 2, 3, 4), 0.5, 2 * time.Minute, 2, false},
		{"twenty p90", mk(1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20), 0.9, 18 * time.Minute, 18, false},
		{"ties break by evidence id", mk(5, 5, 5), 0.5, 5 * time.Minute, 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := Percentile(tc.in, tc.q)
			if ok == tc.wantNone {
				t.Fatalf("ok=%v", ok)
			}
			if tc.wantNone {
				return
			}
			if got.D != tc.want || got.Ev.RunID != tc.wantRun {
				t.Fatalf("got %v (run %d), want %v (run %d)", got.D, got.Ev.RunID, tc.want, tc.wantRun)
			}
		})
	}
	in := mk(3, 1, 2)
	Percentile(in, 0.5)
	if in[0].D != 3*time.Minute {
		t.Fatal("Percentile must not reorder its input")
	}
}
