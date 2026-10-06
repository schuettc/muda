package measure

import (
	"math"
	"sort"
	"time"

	"github.com/schuettc/muda/internal/signal"
)

// Sample is one measured duration and the source it was measured from.
type Sample struct {
	D  time.Duration
	Ev signal.Evidence
}

// Percentile is the nearest-rank percentile (the port's runs.go): the sample
// at index ceil(q·n)−1 of the samples sorted by duration. It is always a real
// sample, so its evidence is the evidence for the number. Ties sort by run,
// job and step so the choice is deterministic. ok is false for no samples.
func Percentile(samples []Sample, q float64) (Sample, bool) {
	if len(samples) == 0 {
		return Sample{}, false
	}
	s := append([]Sample(nil), samples...)
	sortSamples(s)
	i := int(math.Ceil(q*float64(len(s)))) - 1
	if i < 0 {
		i = 0
	}
	if i >= len(s) {
		i = len(s) - 1
	}
	return s[i], true
}

func sortSamples(s []Sample) {
	sort.SliceStable(s, func(i, j int) bool {
		a, b := s[i], s[j]
		if a.D != b.D {
			return a.D < b.D
		}
		if a.Ev.RunID != b.Ev.RunID {
			return a.Ev.RunID < b.Ev.RunID
		}
		if a.Ev.JobID != b.Ev.JobID {
			return a.Ev.JobID < b.Ev.JobID
		}
		return a.Ev.Step < b.Ev.Step
	})
}

// sampleEvidence returns the sample's evidence, annotated with what it proves.
func sampleEvidence(s Sample, label string) signal.Evidence {
	e := s.Ev
	note := label + " sample (" + s.D.String() + ")"
	if e.Note != "" {
		note += ": " + e.Note
	}
	e.Note = note
	return e
}

// percentiles returns p50 and p90 and the evidence for each.
func percentiles(samples []Sample, label string) (p50, p90 time.Duration, ev []signal.Evidence) {
	ev = []signal.Evidence{}
	if a, ok := Percentile(samples, 0.5); ok {
		p50 = a.D
		ev = append(ev, sampleEvidence(a, "p50 "+label))
	}
	if b, ok := Percentile(samples, 0.9); ok {
		p90 = b.D
		ev = append(ev, sampleEvidence(b, "p90 "+label))
	}
	return p50, p90, ev
}

// Confidence-label thresholds, in runs.
const (
	MediumConfidenceRuns = 10
	HighConfidenceRuns   = 30
)

// Confidence labels a statistic by its sample size n: low below
// MediumConfidenceRuns, medium below HighConfidenceRuns, else high. Small
// samples are normal, so the label informs: it never blocks, drops or hides
// a number.
func Confidence(n int) string {
	switch {
	case n >= HighConfidenceRuns:
		return "high"
	case n >= MediumConfidenceRuns:
		return "medium"
	}
	return "low"
}

func nonNegative(d time.Duration) time.Duration {
	if d < 0 {
		return 0
	}
	return d
}
