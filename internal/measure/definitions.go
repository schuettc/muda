package measure

import (
	"fmt"
	"strings"

	"github.com/schuettc/muda/internal/gh"
	"github.com/schuettc/muda/internal/signal"
	"github.com/schuettc/muda/internal/workflow"
)

// otherFileEvents run a workflow file from another branch, so the file at the
// run's head commit is not the version that ran: they are cited, never read.
var otherFileEvents = map[string]string{
	"pull_request_target": "base branch's",
	"workflow_run":        "default branch's",
	"repository_dispatch": "default branch's",
}

// historyEvents on the default branch run the file at the run's head commit,
// in commit order: the one line of history a change is placed on.
var historyEvents = map[string]bool{"push": true, "schedule": true, "workflow_dispatch": true}

const approximateReason = "read at the pull request's head commit, but GitHub ran its merge ref's file, which also carries base-branch changes"

// definitionHistory places a workflow's file changes on the default branch's
// push, schedule and workflow_dispatch runs (runs are already in run order),
// and lists every other run's versions as branch-only. A run whose file was
// not read is cited, never assumed unchanged.
func definitionHistory(runs []RunData, refs map[string]Definition, defaultBranch string) *DefinitionHistory {
	h := &DefinitionHistory{Branch: defaultBranch, Changes: []DefinitionChange{}, Unread: []signal.Evidence{},
		BranchOnly: []BranchVersion{}, OtherUnread: []signal.Evidence{}}
	versions := map[string]bool{}
	reasons, otherReasons := map[string]bool{}, map[string]bool{}
	var prev Definition // the last read default-branch run's definition
	var prevRun gh.Run
	havePrev := false
	unreadSince := 0
	var others []int // indexes of other runs whose file was read
	for i, rd := range runs {
		r := rd.Run
		onLine := defaultBranch != "" && r.HeadBranch == defaultBranch && historyEvents[r.Event]
		d, ok := refs[RefKey(r.Path, r.HeadSHA)]
		why := ""
		switch {
		case !strings.HasPrefix(r.Path, ".github/workflows/"):
			why = githubManaged
		case otherFileEvents[r.Event] != "":
			why = fmt.Sprintf("%s runs the %s workflow file, not the one at its head commit", r.Event, otherFileEvents[r.Event])
		case !ok:
			why = "workflow file at the run's head commit was not read"
		case d.SHA256 == "":
			why = d.Unavailable
		}
		if onLine {
			h.BranchRuns++
		}
		if why != "" {
			ev := runEvidence(r, "workflow file not read: "+why)
			if onLine {
				reasons[why] = true
				h.Unread = append(h.Unread, ev)
				unreadSince++
			} else {
				otherReasons[why] = true
				h.OtherUnread = append(h.OtherUnread, ev)
			}
			continue
		}
		if !onLine {
			others = append(others, i)
			continue
		}
		h.RunsRead++
		versions[d.SHA256] = true
		if havePrev && d.SHA256 != prev.SHA256 {
			h.Changes = append(h.Changes, definitionChange(r, d, prev, prevRun, i, len(runs), unreadSince))
		}
		prev, prevRun, havePrev, unreadSince = d, r, true, 0
	}
	h.Versions = len(versions)
	h.BranchOnly = branchOnly(runs, others, refs, versions)
	if len(runs) > 0 && !strings.HasPrefix(runs[0].Run.Path, ".github/workflows/") {
		h.Unavailable = githubManaged + "; whether it changed in the window is unknown"
		return h
	}
	switch n := len(h.Unread); {
	case h.BranchRuns == 0:
		h.Unavailable = fmt.Sprintf("no push, schedule or workflow_dispatch run on %s in the window: no default-branch history to place a change on", orUnknown(defaultBranch))
		reasons = otherReasons
	case n == h.BranchRuns:
		h.Unavailable = fmt.Sprintf("no default-branch run's workflow file could be read (%d runs); whether it changed in the window is unknown", n)
	case n > 0:
		h.Unavailable = fmt.Sprintf("%d of %d default-branch runs' workflow files could not be read; a change among the unread runs cannot be ruled out", n, h.BranchRuns)
	}
	if h.Unavailable != "" && len(reasons) == 1 {
		for why := range reasons {
			h.Unavailable += ": " + why
		}
	}
	return h
}

func orUnknown(branch string) string {
	if branch == "" {
		return "the default branch (unknown)"
	}
	return branch
}

func definitionChange(r gh.Run, d, prev Definition, prevRun gh.Run, i, total, unreadSince int) DefinitionChange {
	c := DefinitionChange{RunID: r.ID, RunURL: r.HTMLURL, CreatedAt: r.CreatedAt.UTC(), HeadSHA: r.HeadSHA,
		Branch: r.HeadBranch, Event: r.Event, From: prev.SHA256, To: d.SHA256, RunsBefore: i, RunsAfter: total - i,
		UnreadBetween: unreadSince, JobsAdded: []string{}, JobsRemoved: []string{}}
	if prev.File == nil || d.File == nil {
		c.JobsUnavailable = jobsUnavailable(prev, d)
	} else {
		c.JobsAdded, c.JobsRemoved = jobDiff(prev.File, d.File)
	}
	c.Evidence = []signal.Evidence{runEvidence(r, "first default-branch run on the new workflow file version")}
	if d.URL != "" {
		c.Evidence = append(c.Evidence, signal.Evidence{Kind: signal.KindFile, URL: d.URL, Path: d.Path, Note: "new version (sha256 " + d.SHA256 + ")"})
	}
	if prev.URL != "" {
		c.Evidence = append(c.Evidence, signal.Evidence{Kind: signal.KindFile, URL: prev.URL, Path: prev.Path,
			Note: fmt.Sprintf("previous version (sha256 %s), last used by run %d", prev.SHA256, prevRun.ID)})
	}
	return c
}

// branchOnly groups the read runs off the default branch's history by
// version, keeping only versions no default-branch run used, in order of
// first use.
func branchOnly(runs []RunData, idx []int, refs map[string]Definition, onLine map[string]bool) []BranchVersion {
	out := []BranchVersion{}
	at := map[string]int{}
	branches, events := map[string]map[string]bool{}, map[string]map[string]bool{}
	for _, i := range idx {
		r := runs[i].Run
		d := refs[RefKey(r.Path, r.HeadSHA)]
		if onLine[d.SHA256] {
			continue
		}
		n, seen := at[d.SHA256]
		if !seen {
			n = len(out)
			at[d.SHA256] = n
			branches[d.SHA256], events[d.SHA256] = map[string]bool{}, map[string]bool{}
			v := BranchVersion{SHA256: d.SHA256, FirstRunID: r.ID, FirstCreatedAt: r.CreatedAt.UTC(), FirstHeadSHA: r.HeadSHA,
				Evidence: []signal.Evidence{runEvidence(r, "first run on this branch-only version")}}
			if d.URL != "" {
				v.Evidence = append(v.Evidence, signal.Evidence{Kind: signal.KindFile, URL: d.URL, Path: d.Path, Note: "branch-only version (sha256 " + d.SHA256 + ")"})
			}
			out = append(out, v)
		}
		v := &out[n]
		v.Runs++
		branches[d.SHA256][r.HeadBranch] = true
		events[d.SHA256][r.Event] = true
		if r.Event == "pull_request" {
			v.Approximate, v.ApproximateReason = true, approximateReason
		}
	}
	for i := range out {
		out[i].Branches = sortedKeys(branches[out[i].SHA256])
		out[i].Events = sortedKeys(events[out[i].SHA256])
	}
	return out
}

func jobsUnavailable(prev, d Definition) string {
	for _, x := range []Definition{prev, d} {
		if x.File == nil {
			return fmt.Sprintf("jobs not compared: the version at %s: %s", x.Ref, x.Unavailable)
		}
	}
	return ""
}

// jobDiff returns the job ids in b and not a (b's document order), and in a
// and not b (a's order).
func jobDiff(a, b *workflow.File) (added, removed []string) {
	added, removed = []string{}, []string{}
	for _, id := range b.JobOrder {
		if _, ok := a.Jobs[id]; !ok {
			added = append(added, id)
		}
	}
	for _, id := range a.JobOrder {
		if _, ok := b.Jobs[id]; !ok {
			removed = append(removed, id)
		}
	}
	return added, removed
}
