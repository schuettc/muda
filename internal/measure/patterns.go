package measure

import (
	"fmt"
	"sort"
	"time"

	"github.com/schuettc/muda/internal/signal"
)

// Signal thresholds.
const (
	// RerunRateMin: a workflow signals `rerun` when at least this share of its
	// runs needed more than one attempt.
	RerunRateMin = 0.10
	// QueueP90Min: a job signals `queue-time` when its queue p90 reaches this.
	QueueP90Min = 5 * time.Minute
	// HandoffMinRuns and HandoffFailureRateMin: a bump or deploy workflow
	// signals `handoff-failure` when at least this many of its hand-off runs
	// exist and at least this share of them failed.
	HandoffMinRuns        = 5
	HandoffFailureRateMin = 0.20
	// SlowStepTop: the number of steps, ranked by p50 × runs, reported as
	// sinks and `slow-step` signals.
	SlowStepTop = 10
	// MinMirroredCheckSteps: a job must execute at least this many of its own
	// steps (not GitHub's setup, teardown or post steps) to count as a check
	// set another job can mirror.
	MinMirroredCheckSteps = 2
)

// handoffEvents trigger a workflow from another workflow or from outside.
var handoffEvents = map[string]bool{"workflow_run": true, "repository_dispatch": true, "workflow_dispatch": true}

func rerunSignals(w WorkflowStats, runs []RunData) []signal.Signal {
	if w.Runs == 0 || float64(w.Reruns)/float64(w.Runs) < RerunRateMin {
		return nil
	}
	ev := []signal.Evidence{}
	for _, rd := range runs {
		if rd.Run.RunAttempt > 1 {
			ev = append(ev, runEvidence(rd.Run, fmt.Sprintf("final attempt %d: %s", rd.Run.RunAttempt, orNone(rd.Run.Conclusion))))
			ev = append(ev, supersededAttempts(rd)...)
		}
	}
	return []signal.Signal{signal.New("rerun", signal.SeverityWarn,
		fmt.Sprintf("%s: %d of %d runs needed a re-run (%.0f%%)", w.Path, w.Reruns, w.Runs, 100*float64(w.Reruns)/float64(w.Runs)), ev)}
}

func handoffSignals(w WorkflowStats, runs []RunData) []signal.Signal {
	if w.Role != RoleBump && w.Role != RoleDeploy {
		return nil
	}
	var considered, failures int
	ev := []signal.Evidence{}
	for _, rd := range runs {
		r := rd.Run
		if !handoffEvents[r.Event] {
			continue
		}
		considered++
		note := fmt.Sprintf("%s hand-off: %s", r.Event, orNone(r.Conclusion))
		if failed[r.Conclusion] {
			failures++
			note = fmt.Sprintf("%s hand-off: failure (%s)", r.Event, r.Conclusion)
		}
		ev = append(ev, runEvidence(r, note))
	}
	if considered < HandoffMinRuns || float64(failures)/float64(considered) < HandoffFailureRateMin {
		return nil
	}
	return []signal.Signal{signal.New("handoff-failure", signal.SeverityHigh,
		fmt.Sprintf("%s (%s): %d of %d hand-off runs failed (%.0f%%)", w.Path, w.Role, failures, considered, 100*float64(failures)/float64(considered)), ev)}
}

func queueSignals(w WorkflowStats) []signal.Signal {
	var out []signal.Signal
	for _, j := range w.Jobs {
		if j.QueueP90 < QueueP90Min {
			continue
		}
		out = append(out, signal.New("queue-time", signal.SeverityWarn,
			fmt.Sprintf("%s / %s waited for a runner: queue p90 %s, p50 %s over %d runs", w.Path, j.Name, j.QueueP90, j.QueueP50, j.Runs),
			append([]signal.Evidence{}, j.QueueEvidence...)))
	}
	return out
}

// sinks ranks every step by p50 × runs and keeps the top SlowStepTop.
func sinks(ws []WorkflowStats) []Sink {
	all := []Sink{}
	for _, w := range ws {
		for _, j := range w.Jobs {
			for _, s := range j.Steps {
				weight := s.P50 * time.Duration(s.Runs)
				if weight <= 0 {
					continue
				}
				all = append(all, Sink{Workflow: w.Path, Job: j.Name, Step: s.Name, Occurrence: s.Occurrence, Runs: s.Runs, Confidence: s.Confidence, P50: s.P50, P90: s.P90,
					Weight: weight, Evidence: append([]signal.Evidence{}, s.Evidence...)})
			}
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		a, b := all[i], all[j]
		if a.Weight != b.Weight {
			return a.Weight > b.Weight
		}
		if a.Workflow != b.Workflow {
			return a.Workflow < b.Workflow
		}
		if a.Job != b.Job {
			return a.Job < b.Job
		}
		if a.Step != b.Step {
			return a.Step < b.Step
		}
		return a.Occurrence < b.Occurrence
	})
	if len(all) > SlowStepTop {
		all = all[:SlowStepTop]
	}
	return all
}

// stepLabel names a step, with its occurrence when a job repeats the name.
func stepLabel(name string, occurrence int) string {
	if occurrence > 1 {
		return fmt.Sprintf("%s (#%d)", name, occurrence)
	}
	return name
}

func slowStepSignals(ss []Sink) []signal.Signal {
	out := make([]signal.Signal, 0, len(ss))
	for i, s := range ss {
		out = append(out, signal.New("slow-step", signal.SeverityInfo,
			fmt.Sprintf("#%d time sink: %s / %s / %s: p50 %s × %d runs = %s", i+1, s.Workflow, s.Job, stepLabel(s.Step, s.Occurrence), s.P50, s.Runs, s.Weight),
			append([]signal.Evidence{}, s.Evidence...)))
	}
	return out
}
