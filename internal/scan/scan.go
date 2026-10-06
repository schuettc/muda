// Package scan runs static workflow analysis and emits the scan-family signals.
package scan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/schuettc/muda/internal/gh"
	"github.com/schuettc/muda/internal/signal"
	"github.com/schuettc/muda/internal/workflow"
)

// Schema is the JSON contract version for Report.
const Schema = 1

// minPathFilterDuration is the p50 threshold for the no-path-filter rule.
const minPathFilterDuration = 5 * time.Minute

// Unavailable names a piece of evidence that scan could not obtain.
type Unavailable struct {
	What string `json:"what"`
	Why  string `json:"why"`
}

// Report is `muda scan`'s output. Schema 1; signals are data, exit code is 0.
type Report struct {
	Schema      int             `json:"schema"`
	Repo        string          `json:"repo"`
	Ref         string          `json:"ref"`
	Since       time.Time       `json:"since"` // run-history window [since, until)
	Until       time.Time       `json:"until"`
	Files       []string        `json:"files"`
	Skipped     []string        `json:"skipped,omitempty"`
	Unavailable []Unavailable   `json:"unavailable,omitempty"`
	Signals     []signal.Signal `json:"signals"`
}

// Files runs all static rules over the parsed workflow files. durations maps
// workflow path to its p50 run duration (for the no-path-filter rule); a
// workflow absent from the map gets no no-path-filter signal and is listed in
// the skipped report (see Skipped). durations may be nil.
func Files(files []*workflow.File, durations map[string]time.Duration) []signal.Signal {
	sigs := []signal.Signal{}
	sigs = append(sigs, ruleFloatingRef(files)...)
	sigs = append(sigs, ruleFloatingRunner(files)...)
	sigs = append(sigs, ruleFloatingToolchain(files)...)
	sigs = append(sigs, ruleToolchainDrift(files)...)
	sigs = append(sigs, ruleEmulation(files)...)
	sigs = append(sigs, ruleUncachedInstall(files)...)
	sigs = append(sigs, ruleSerialIndependentJobs(files)...)
	sigs = append(sigs, ruleDuplicateCheckSet(files)...)
	sigs = append(sigs, ruleNoConcurrency(files)...)
	sigs = append(sigs, ruleNoPathFilter(files, durations)...)
	return sigs
}

// Skipped returns the sorted paths of workflows that trigger on push or
// pull_request without paths/paths-ignore filters but are absent from the
// durations map, so no no-path-filter signal could be produced. An absent
// workflow is not a green result — the evidence is unavailable.
func Skipped(files []*workflow.File, durations map[string]time.Duration) []string {
	var out []string
	for _, f := range files {
		if !hasPushOrPR(f) {
			continue
		}
		if hasPathFilter(f) {
			continue
		}
		if durations != nil {
			if _, ok := durations[f.Path]; ok {
				continue
			}
		}
		out = append(out, f.Path)
	}
	sort.Strings(out)
	return out
}

// Run fetches the repo's workflows at ref (default branch when ""), computes
// per-workflow p50 durations from the last 20 completed runs created in
// [since, until), and calls Files. until bounds run history only; it never
// selects the ref. Parse errors produce Unavailable entries, never silent skips.
func Run(ctx context.Context, c *gh.Client, repo, ref string, since, until time.Time) (*Report, error) {
	if ref == "" {
		def, err := c.DefaultBranch(ctx, repo)
		if err != nil {
			return nil, fmt.Errorf("resolve default branch: %w", err)
		}
		ref = def
	}

	entries, err := c.Entries(ctx, repo, ref, ".github/workflows")
	if err != nil {
		return nil, fmt.Errorf("list .github/workflows: %w", err)
	}

	var files []*workflow.File
	filePaths := []string{}
	var unavail []Unavailable

	for _, entry := range entries {
		p := entry.Path
		if !isWorkflowFile(p) {
			continue
		}
		// A symlink or submodule at a workflow path is named: dropped, its
		// recorded signals would read as removed.
		if entry.Type != "file" {
			unavail = append(unavail, Unavailable{What: p, Why: (&gh.NotFileError{Path: p, Type: entry.Type}).Reason()})
			continue
		}
		data, ok, err := c.File(ctx, repo, ref, p)
		var notFile *gh.NotFileError
		if errors.As(err, &notFile) {
			unavail = append(unavail, Unavailable{What: p, Why: notFile.Reason()})
			continue
		}
		if err != nil {
			unavail = append(unavail, Unavailable{
				What: p,
				Why:  fmt.Sprintf("fetch: %v", err),
			})
			continue
		}
		if !ok {
			unavail = append(unavail, Unavailable{
				What: p,
				Why:  "file disappeared between directory listing and fetch",
			})
			continue
		}
		f, err := workflow.Parse(p, data)
		if err != nil {
			// Explicit: parse errors are never silently dropped.
			unavail = append(unavail, Unavailable{
				What: p,
				Why:  fmt.Sprintf("parse: %v", err),
			})
			continue
		}
		files = append(files, f)
		filePaths = append(filePaths, p)
	}

	durations, timingNotes, dErr := fetchDurations(ctx, c, repo, since, until, files)
	if dErr != nil {
		unavail = append(unavail, Unavailable{
			What: "run history",
			Why:  fmt.Sprintf("fetch runs since %s until %s: %v", since.Format(time.RFC3339), until.Format(time.RFC3339), dErr),
		})
	}

	unavail = append(unavail, timingNotes...)
	unavail = append(unavail, cacheUnavailable(files)...)
	unavail = append(unavail, toolchainUnavailable(files)...)
	sigs := Files(files, durations)
	for i := range sigs {
		for j := range sigs[i].Evidence {
			e := &sigs[i].Evidence[j]
			if e.Path != "" {
				e.URL = gh.BlobURL(repo, ref, e.Path)
				if e.Line > 0 {
					e.URL += fmt.Sprintf("#L%d", e.Line)
				}
			}
		}
	}
	skipped := Skipped(files, durations)

	return &Report{
		Schema:      Schema,
		Repo:        repo,
		Ref:         ref,
		Since:       since.UTC(),
		Until:       until.UTC(),
		Files:       filePaths,
		Skipped:     skipped,
		Unavailable: unavail,
		Signals:     sigs,
	}, nil
}

// fetchDurations returns a per-workflow-path p50 duration from the last 20
// completed runs created in [since, until). Duration is the final attempt's wall
// time from final-attempt start through job completion (or a named UpdatedAt estimate).
func fetchDurations(ctx context.Context, c *gh.Client, repo string, since, until time.Time, files []*workflow.File) (map[string]time.Duration, []Unavailable, error) {
	if len(files) == 0 {
		return nil, nil, nil
	}

	// Build set of workflow paths we care about.
	want := map[string]bool{}
	for _, f := range files {
		want[f.Path] = true
	}

	runs, err := c.RunsWindow(ctx, repo, since, until)
	if err != nil {
		return nil, nil, err
	}

	// Group completed runs by workflow path.
	byPath := map[string][]gh.Run{}
	for _, r := range runs {
		if r.Status != "completed" || !want[r.Path] {
			continue
		}
		byPath[r.Path] = append(byPath[r.Path], r)
	}

	result := map[string]time.Duration{}
	var notes []Unavailable
	paths := make([]string, 0, len(byPath))
	for path := range byPath {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		rs := byPath[path]
		// Sort descending by UpdatedAt, take last 20.
		sort.Slice(rs, func(i, j int) bool {
			return rs[i].UpdatedAt.After(rs[j].UpdatedAt)
		})
		if len(rs) > 20 {
			rs = rs[:20]
		}
		ds := make([]time.Duration, 0, len(rs))
		for _, r := range rs {
			d, note, err := finalDuration(ctx, c, repo, r)
			if note != "" {
				notes = append(notes, Unavailable{What: fmt.Sprintf("%s run %d duration", path, r.ID), Why: note})
			}
			if err != nil {
				notes = append(notes, Unavailable{What: fmt.Sprintf("%s run %d duration", path, r.ID), Why: err.Error()})
				ds = nil
				break
			}
			if d > 0 {
				ds = append(ds, d)
			}
		}
		if p := p50(ds); p > 0 {
			result[path] = p
		}
	}
	return result, notes, nil
}

// p50 returns the lower-median (nearest-rank p50) of a slice of durations.
func p50(ds []time.Duration) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	sorted := append([]time.Duration(nil), ds...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	idx := (len(sorted) - 1) / 2
	return sorted[idx]
}

// isWorkflowFile returns true for *.yml and *.yaml files.
func isWorkflowFile(p string) bool {
	return strings.HasSuffix(p, ".yml") || strings.HasSuffix(p, ".yaml")
}

// hasPushOrPR returns true if the workflow triggers on push or pull_request.
func hasPushOrPR(f *workflow.File) bool {
	_, hasPush := f.On["push"]
	_, hasPR := f.On["pull_request"]
	return hasPush || hasPR
}

// hasPathFilter requires every present push/PR trigger to be filtered.
func hasPathFilter(f *workflow.File) bool {
	for _, event := range []string{"push", "pull_request"} {
		v, present := f.On[event]
		if !present {
			continue
		}
		m, ok := v.(map[string]any)
		if !ok {
			return false
		}
		_, paths := m["paths"]
		_, ignore := m["paths-ignore"]
		if !paths && !ignore {
			return false
		}
	}
	return true
}

func finalDuration(ctx context.Context, c *gh.Client, repo string, r gh.Run) (time.Duration, string, error) {
	start := r.RunStartedAt
	if r.RunAttempt < 1 {
		return 0, "", fmt.Errorf("final attempt number unavailable")
	}
	if r.RunAttempt > 1 {
		attempts, err := c.RunAttempts(ctx, repo, r)
		if err != nil {
			return 0, "", fmt.Errorf("final attempt: %w", err)
		}
		final := attempts[len(attempts)-1]
		if final.RunAttempt != r.RunAttempt || final.Status != "completed" {
			return 0, "", fmt.Errorf("final attempt incomplete or mismatched")
		}
		start = final.RunStartedAt
	}
	if start.IsZero() {
		return 0, "", fmt.Errorf("final attempt start unavailable")
	}
	jobs, err := c.Jobs(ctx, repo, r.ID, r.RunAttempt)
	if err != nil {
		return 0, "", fmt.Errorf("final attempt jobs: %w", err)
	}
	var end time.Time
	for _, j := range jobs {
		// Rerun listings can carry over jobs from earlier attempts.
		if j.RunAttempt != r.RunAttempt || j.StartedAt.Before(start) {
			continue
		}
		if j.Status != "completed" || j.CompletedAt.IsZero() {
			return 0, "", fmt.Errorf("final attempt job completion unavailable")
		}
		if j.CompletedAt.After(end) {
			end = j.CompletedAt
		}
	}
	if !end.IsZero() && end.After(start) {
		return end.Sub(start), "", nil
	}
	if r.UpdatedAt.After(start) {
		return r.UpdatedAt.Sub(start), "UpdatedAt estimate: final-attempt job completion unavailable", nil
	}
	return 0, "", fmt.Errorf("final attempt duration unavailable")
}

// WriteJSON encodes the report as indented JSON.
func WriteJSON(w io.Writer, r *Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	copyReport := *r
	if copyReport.Files == nil {
		copyReport.Files = []string{}
	}
	if copyReport.Signals == nil {
		copyReport.Signals = []signal.Signal{}
	}
	return enc.Encode(&copyReport)
}

// RenderMarkdown returns a brief Markdown summary of the report.
func RenderMarkdown(r *Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# muda scan: %s\n\n", r.Repo)
	fmt.Fprintf(&b, "**Ref:** %s  \n", r.Ref)
	fmt.Fprintf(&b, "**Run history window:** [%s, %s)  \n", r.Since.Format(time.RFC3339), r.Until.Format(time.RFC3339))
	fmt.Fprintf(&b, "**Workflows scanned:** %d  \n", len(r.Files))
	if len(r.Skipped) > 0 {
		fmt.Fprintf(&b, "**Skipped (no duration data):** %d  \n", len(r.Skipped))
		for _, s := range r.Skipped {
			fmt.Fprintf(&b, "  - `%s` (no-path-filter check skipped: run history unavailable)\n", s)
		}
	}
	if len(r.Unavailable) > 0 {
		fmt.Fprintf(&b, "\n## Unavailable\n\n")
		for _, u := range r.Unavailable {
			fmt.Fprintf(&b, "- **%s**: %s\n", u.What, u.Why)
		}
	}
	if len(r.Signals) == 0 {
		fmt.Fprintf(&b, "\nNo signals found.\n")
		return b.String()
	}
	fmt.Fprintf(&b, "\n## Signals (%d)\n\n", len(r.Signals))
	for _, s := range r.Signals {
		fmt.Fprintf(&b, "### %s (%s)\n\n", s.ID, s.Severity)
		fmt.Fprintf(&b, "%s\n\n", s.Summary)
		for _, e := range s.Evidence {
			link := e.URL
			if link == "" && e.Path != "" {
				link = e.Path
				if e.Line > 0 {
					link = fmt.Sprintf("%s#L%d", e.Path, e.Line)
				}
			}
			if link != "" {
				fmt.Fprintf(&b, "- [%s](%s)", e.Note, link)
			} else {
				fmt.Fprintf(&b, "- %s", e.Note)
			}
			fmt.Fprintln(&b)
		}
		fmt.Fprintln(&b)
	}
	return b.String()
}
