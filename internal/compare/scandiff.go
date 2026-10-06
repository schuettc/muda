package compare

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/schuettc/muda/internal/gh"
	"github.com/schuettc/muda/internal/record"
	"github.com/schuettc/muda/internal/scan"
	"github.com/schuettc/muda/internal/signal"
)

// ScanDiff statuses.
const (
	ScanCompared    = "compared"
	ScanUnavailable = "unavailable"
)

// missingScan is why a record without scan.json cannot be compared.
const missingScan = "no recorded .muda/scan.json (the record predates stored scans); re-record the baseline with --scan to compare signals"

// ScanDiff compares the recorded scan's signals with a fresh scan's. Signal
// changes are data: they never change compare's exit code.
type ScanDiff struct {
	Status      string          `json:"status"`
	Removed     []signal.Signal `json:"removed"`
	Added       []signal.Signal `json:"added"`
	Unchanged   []signal.Signal `json:"unchanged"`
	Unavailable string          `json:"unavailable,omitempty"`
	// BeforeRef is the recorded commit a historical baseline's scan read.
	BeforeRef string `json:"before_ref,omitempty"`
}

// UnavailableScan names why no scan comparison was possible; it never reads
// as "no changes".
func UnavailableScan(why string) *ScanDiff {
	return &ScanDiff{Status: ScanUnavailable, Removed: []signal.Signal{}, Added: []signal.Signal{}, Unchanged: []signal.Signal{}, Unavailable: why}
}

// scanKey is a signal's identity across scans: its ID, its Summary, and in
// evidence order each evidence item's file and step. Summaries name the
// action ref, job, command or value a signal is about and never a line, so a
// fixed ref and a new floating one in the same file are different signals.
// no-path-filter's summary carries a rolling p50, so its identity is its ID
// and files only. Line numbers shift with edits above a signal, and URL and
// Note follow them, so none of those take part.
func scanKey(s signal.Signal) string {
	parts := [][2]string{}
	for _, e := range s.Evidence {
		parts = append(parts, [2]string{e.Path, e.Step})
	}
	summary := s.Summary
	if s.ID == "no-path-filter" {
		summary = ""
	}
	b, _ := json.Marshal(struct {
		ID       string
		Summary  string
		Evidence [][2]string
	}{s.ID, summary, parts})
	return string(b)
}

// ScanChanges matches stored and fresh signals as a multiset keyed by
// scanKey: each fresh signal consumes one equal stored signal (unchanged,
// shown with its current lines) or is added; stored signals left over are
// removed. A nil stored report is unavailable, never "no changes".
func ScanChanges(stored, fresh *scan.Report) *ScanDiff {
	if stored == nil {
		return UnavailableScan(missingScan)
	}
	if stored.Schema != scan.Schema {
		return UnavailableScan(fmt.Sprintf(".muda/scan.json has schema %d; want %d", stored.Schema, scan.Schema))
	}
	if stored.Repo != fresh.Repo {
		return UnavailableScan(fmt.Sprintf(".muda/scan.json is for repository %q, not %q", stored.Repo, fresh.Repo))
	}
	// A stored ID that is no longer a registered scan signal (renamed or
	// removed in a later muda version) can never match a fresh signal, so
	// its absence proves nothing.
	unknown := []string{}
	for _, s := range stored.Signals {
		if def, ok := signal.Registry[s.ID]; !ok || def.From != "scan" {
			unknown = append(unknown, s.ID)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return UnavailableScan(".muda/scan.json has signals that are not registered scan signals (re-record the baseline with --scan): " + strings.Join(compact(unknown), ", "))
	}
	d := &ScanDiff{Status: ScanCompared, Removed: []signal.Signal{}, Added: []signal.Signal{}, Unchanged: []signal.Signal{}}
	pool := map[string][]int{}
	for i, s := range stored.Signals {
		k := scanKey(s)
		pool[k] = append(pool[k], i)
	}
	for _, s := range fresh.Signals {
		k := scanKey(s)
		if len(pool[k]) == 0 {
			d.Added = append(d.Added, s)
			continue
		}
		pool[k] = pool[k][1:]
		d.Unchanged = append(d.Unchanged, s)
	}
	left := []int{}
	for _, idx := range pool {
		left = append(left, idx...)
	}
	sort.Ints(left)
	for _, i := range left {
		d.Removed = append(d.Removed, stored.Signals[i])
	}
	if why := unprovableRemovals(d.Removed, stored, fresh); why != "" {
		return UnavailableScan(why)
	}
	return d
}

// unprovableRemovals names removed signals that the fresh scan could not
// have produced because its evidence was missing: a workflow file it could
// not fetch or parse, a workflow skipped for lack of run timings
// (no-path-filter), the same file, job and package manager whose cache
// coverage is unknown (uncached-install), or a tool with an
// expression-valued pin that the recorded scan did not have byte for byte
// (toolchain-drift). Reporting those as removed would claim a fix.
//
// An expression pin note names every expression pin of one tool in one job,
// in step order. A note equal in path, job, tool and every expression on
// both sides names pins that both drift comparisons excluded, so it cannot
// be what cleared the drift. Any other fresh note (a new site, a changed
// expression, or a literal pin turned into an expression) can be, so it
// blocks; a stored scan without notes blocks every fresh one.
//
// Equality covers each expression's input and text and their order among
// the job's expression pins. It does not cover a step's position among the
// job's literal pins: those are compared for drift on both sides anyway, so
// moving an expression step past a literal one hides nothing.
func unprovableRemovals(removed []signal.Signal, stored, fresh *scan.Report) string {
	unreadable := map[string]string{}
	cacheUnknown := map[scan.CacheSite]bool{}
	for _, u := range fresh.Unavailable {
		unreadable[u.What] = u.Why
		if site, ok := scan.UnknownCacheSite(u); ok {
			cacheUnknown[site] = true
		}
	}
	recordedExpr := map[scan.Unavailable]bool{}
	for _, u := range stored.Unavailable {
		if _, ok := scan.ExpressionPinTool(u); ok {
			recordedExpr[u] = true
		}
	}
	exprPins := map[string][]string{}
	for _, u := range fresh.Unavailable {
		if tool, ok := scan.ExpressionPinTool(u); ok && !recordedExpr[u] {
			exprPins[tool] = append(exprPins[tool], u.What)
		}
	}
	skipped := map[string]bool{}
	for _, p := range fresh.Skipped {
		skipped[p] = true
	}
	reasons := []string{}
	for _, s := range removed {
		if site, ok := scan.UncachedInstallSite(s); ok && cacheUnknown[site] {
			reasons = append(reasons, fmt.Sprintf("%s in %s job %s (fresh scan cannot tell its %s cache coverage)", s.ID, site.Path, site.Job, site.Manager))
		}
		if tool, ok := scan.DriftTool(s); ok && len(exprPins[tool]) > 0 {
			reasons = append(reasons, fmt.Sprintf("%s for %s (fresh scan cannot compare %s)", s.ID, tool, strings.Join(exprPins[tool], ", ")))
		}
		for _, e := range s.Evidence {
			switch {
			case e.Path == "":
				continue
			case unreadable[e.Path] != "":
				reasons = append(reasons, fmt.Sprintf("%s in %s (fresh scan: %s)", s.ID, e.Path, unreadable[e.Path]))
			case s.ID == "no-path-filter" && skipped[e.Path]:
				reasons = append(reasons, fmt.Sprintf("%s in %s (fresh scan has no run timings for it)", s.ID, e.Path))
			}
		}
	}
	if len(reasons) == 0 {
		return ""
	}
	sort.Strings(reasons)
	return "fresh scan lacks the evidence to show these recorded signals were removed: " + strings.Join(compact(reasons), "; ")
}

// verifyScan reads the record's scan.json through the store's confined
// reader (Snapshot, which also refuses an unfinished baseline set) and
// compares it with a fresh scan at o.Ref. A historical baseline's stored scan
// must have read its recorded commit, which is then the before side.
func verifyScan(ctx context.Context, c *gh.Client, repo string, o Options) *ScanDiff {
	if o.Record == "" {
		return UnavailableScan("no .muda record found; " + missingScan)
	}
	s, err := record.Open(o.Record)
	if err != nil {
		return UnavailableScan(err.Error())
	}
	snap, err := s.Snapshot()
	if err != nil {
		return UnavailableScan(err.Error())
	}
	raw, ok := snap["scan"].(json.RawMessage)
	if !ok {
		return UnavailableScan(missingScan)
	}
	var stored scan.Report
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&stored); err != nil {
		return UnavailableScan(".muda/scan.json: " + err.Error())
	}
	hist, historical := snap["historical"].(record.Historical)
	if historical && stored.Ref != hist.Ref {
		return UnavailableScan(fmt.Sprintf(".muda/scan.json scans ref %q, not the baseline's historical ref %q", stored.Ref, hist.Ref))
	}
	fresh, err := scan.Run(ctx, c, repo, o.Ref, o.Since, o.Until)
	if err != nil {
		return UnavailableScan("fresh scan: " + err.Error())
	}
	d := ScanChanges(&stored, fresh)
	if historical {
		d.BeforeRef = hist.Ref
	}
	return d
}
