package measure

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/schuettc/muda/internal/gh"
	"github.com/schuettc/muda/internal/signal"
	"github.com/schuettc/muda/internal/workflow"
)

// NeedsOrderTolerance absorbs GitHub's one-second timestamp resolution when
// checking that a dependent job started after its need completed.
const NeedsOrderTolerance = time.Second

// Critical-path node kinds.
const (
	NodeJob      = "job"
	NodeMatrix   = "matrix"
	NodeReusable = "reusable"
)

// criticalPath is the longest chain through `needs` by p50. The edges come
// from one definition; the durations only from runs whose job lists match it
// and whose timings do not contradict its needs edges (no dependent job
// started before a need completed). That does not prove a run had the same
// edges: an edge absent in history whose jobs happened to run serially still
// passes. A run with a changed or renamed job, or a contradicted edge, is
// left out. Anything uncertain is an explicit Unavailable reason.
func criticalPath(runs []RunData, def Definition, hasDef bool) CriticalPathDetail {
	d := CriticalPathDetail{Nodes: []PathNode{}, Rejected: []signal.Evidence{}}
	if !hasDef {
		d.Unavailable = "workflow definition was not read"
		return d
	}
	d.Ref, d.Definition, d.Branch, d.Event, d.SourceRunID = def.Ref, def.URL, def.Branch, def.Event, def.RunID
	if def.File == nil {
		d.Unavailable = def.Unavailable
		return d
	}
	f := def.File
	if len(f.JobOrder) == 0 {
		d.Unavailable = fmt.Sprintf("definition at %s has no jobs", def.Ref)
		return d
	}
	for _, id := range f.JobOrder {
		j := f.Jobs[id]
		if strings.Contains(j.Name, "${{") {
			d.Unavailable = fmt.Sprintf("job %s has an expression in its name (%q); API job names cannot be matched to it", id, j.Name)
			return d
		}
		for _, n := range j.Needs {
			if _, ok := f.Jobs[n]; !ok {
				d.Unavailable = fmt.Sprintf("job %s needs unknown job %s at %s", id, n, def.Ref)
				return d
			}
		}
	}
	order, cyclic := topo(f)
	if cyclic != "" {
		d.Unavailable = fmt.Sprintf("needs edges form a cycle through job %s at %s", cyclic, def.Ref)
		return d
	}

	samples := map[string][]Sample{}
	for _, rd := range runs {
		groups, ok := matchRun(f, rd.Jobs)
		if !ok {
			d.UnmatchedRuns++
			continue
		}
		timed, _ := timedJobs(rd)
		isTimed := map[int64]bool{}
		for _, j := range timed {
			isTimed[j.ID] = true
		}
		spans := map[string]Sample{}
		ends := map[string]time.Time{}
		starts := map[string]time.Time{}
		for id, jobs := range groups {
			var ran []gh.Job
			for _, j := range jobs {
				if isTimed[j.ID] {
					ran = append(ran, j)
				}
			}
			if s, start, end, ok := span(rd.Run, ran); ok {
				spans[id], starts[id], ends[id] = s, start, end
			}
		}
		if why := contradiction(f, starts, ends); why != "" {
			d.Rejected = append(d.Rejected, runEvidence(rd.Run, why))
			continue
		}
		d.BasisRuns++
		for id, s := range spans {
			samples[id] = append(samples[id], s)
		}
	}
	if d.BasisRuns == 0 {
		if len(d.Rejected) > 0 {
			d.Unavailable = fmt.Sprintf("all %d runs whose jobs match the definition at %s contradict its needs edges (a job started before a need completed), so those edges are not the ones they ran with", len(d.Rejected), def.Ref)
		} else {
			d.Unavailable = fmt.Sprintf("no run in the window listed jobs matching the definition at %s", def.Ref)
		}
		return d
	}

	weight := map[string]time.Duration{}
	for _, id := range f.JobOrder {
		if p, ok := Percentile(samples[id], 0.5); ok {
			weight[id] = p.D
		}
	}
	best := map[string]time.Duration{}
	pred := map[string]string{}
	for _, id := range order {
		needs := append([]string(nil), f.Jobs[id].Needs...)
		sort.Strings(needs)
		var top time.Duration
		for _, n := range needs {
			if _, chosen := pred[id]; !chosen || best[n] > top {
				top, pred[id] = best[n], n
			}
		}
		best[id] = weight[id] + top
	}
	end := ""
	for _, id := range f.JobOrder {
		if end == "" || best[id] > best[end] {
			end = id
		}
	}
	var path []string
	for id := end; id != ""; id = pred[id] {
		path = append([]string{id}, path...)
	}
	d.P50 = best[end]
	for _, id := range path {
		j := f.Jobs[id]
		kind := NodeJob
		switch {
		case j.Uses != "":
			kind = NodeReusable
		case j.Matrix:
			kind = NodeMatrix
		}
		n := PathNode{Job: id, Kind: kind, Runs: len(samples[id]), P50: weight[id], Evidence: []signal.Evidence{}}
		if p, ok := Percentile(samples[id], 0.5); ok {
			n.Evidence = append(n.Evidence, sampleEvidence(p, "p50 "+kind+" duration"))
		}
		if def.URL != "" {
			n.Evidence = append(n.Evidence, signal.Evidence{Kind: signal.KindFile, URL: fmt.Sprintf("%s#L%d", def.URL, j.Line),
				Path: def.Path, Line: j.Line, Note: "job definition and needs at " + def.Ref})
		}
		d.Nodes = append(d.Nodes, n)
	}
	return d
}

// contradiction names the first needs edge a run's timings contradict, or "".
func contradiction(f *workflow.File, starts, ends map[string]time.Time) string {
	for _, id := range f.JobOrder {
		start, ok := starts[id]
		if !ok {
			continue
		}
		needs := append([]string(nil), f.Jobs[id].Needs...)
		sort.Strings(needs)
		for _, n := range needs {
			end, ok := ends[n]
			if ok && start.Add(NeedsOrderTolerance).Before(end) {
				return fmt.Sprintf("%s started %s before its need %s completed %s; run excluded from the critical-path basis",
					id, start.UTC().Format(time.RFC3339), n, end.UTC().Format(time.RFC3339))
			}
		}
	}
	return ""
}

// topo orders jobs so every job follows its needs; it returns a job on a
// cycle when there is one.
func topo(f *workflow.File) (order []string, cyclic string) {
	state := map[string]int{} // 1 visiting, 2 done
	var visit func(id string) bool
	visit = func(id string) bool {
		switch state[id] {
		case 1:
			cyclic = id
			return false
		case 2:
			return true
		}
		state[id] = 1
		needs := append([]string(nil), f.Jobs[id].Needs...)
		sort.Strings(needs)
		for _, n := range needs {
			if !visit(n) {
				return false
			}
		}
		state[id] = 2
		order = append(order, id)
		return true
	}
	for _, id := range f.JobOrder {
		if !visit(id) {
			return nil, cyclic
		}
	}
	return order, ""
}

// matchRun assigns each API job of a run to exactly one workflow job. The run
// matches only if every API job has one candidate and every workflow job has
// at least one API job (skipped jobs are listed too). An ambiguous or unknown
// name means the run was not produced by this definition as far as we can
// tell, so it is left out rather than fitted.
func matchRun(f *workflow.File, jobs []gh.Job) (map[string][]gh.Job, bool) {
	if len(jobs) == 0 {
		return nil, false
	}
	groups := map[string][]gh.Job{}
	for _, j := range jobs {
		var hit []string
		for _, id := range f.JobOrder {
			if matches(f.Jobs[id], j.Name) {
				hit = append(hit, id)
			}
		}
		if len(hit) != 1 {
			return nil, false
		}
		groups[hit[0]] = append(groups[hit[0]], j)
	}
	for _, id := range f.JobOrder {
		if len(groups[id]) == 0 {
			return nil, false
		}
	}
	return groups, true
}

func matches(j workflow.Job, apiName string) bool {
	base := j.Name
	if base == "" {
		base = j.ID
	}
	switch {
	case apiName == base:
		return true
	case j.Matrix && strings.HasPrefix(apiName, base+" (") && strings.HasSuffix(apiName, ")"):
		return true
	case j.Uses != "" && strings.HasPrefix(apiName, base+" / "):
		return true
	}
	return false
}

// span times the timed API jobs of one workflow job: first start to last
// completion. A single job cites itself; a matrix or reusable span cites the
// run and lists its jobs.
func span(r gh.Run, ran []gh.Job) (s Sample, start, end time.Time, ok bool) {
	for _, j := range ran {
		if start.IsZero() || j.StartedAt.Before(start) {
			start = j.StartedAt
		}
		if j.CompletedAt.After(end) {
			end = j.CompletedAt
		}
	}
	switch len(ran) {
	case 0:
		return Sample{}, start, end, false
	case 1:
		return Sample{D: end.Sub(start), Ev: jobEvidence(ran[0])}, start, end, true
	}
	ids := make([]string, 0, len(ran))
	for _, j := range ran {
		ids = append(ids, fmt.Sprint(j.ID))
	}
	sort.Strings(ids)
	return Sample{D: end.Sub(start), Ev: runEvidence(r, "span of jobs "+strings.Join(ids, ", "))}, start, end, true
}
