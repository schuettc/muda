package measure

import (
	"fmt"
	"strings"
	"time"

	"github.com/schuettc/muda/internal/signal"
)

// RenderMarkdown renders the report for people. It carries the same numbers
// as the JSON and links every piece of evidence.
func RenderMarkdown(r *Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# muda measure: %s\n\n", r.Repo)
	fmt.Fprintf(&b, "Window: %s to %s (UTC). Each run's final attempt is timed; re-runs are counted and cited. Roles are candidates to confirm. Confidence is by sample size (low below %d runs, medium below %d, high from %d): it labels a number, never hides one.\n\n",
		r.Since.UTC().Format(time.RFC3339), r.Until.UTC().Format(time.RFC3339), MediumConfidenceRuns, HighConfidenceRuns, HighConfidenceRuns)

	if len(r.Workflows) > 0 {
		renderDefinitions(&b, r.Workflows)
	}

	b.WriteString("## Workflows\n\n")
	if len(r.Workflows) == 0 {
		b.WriteString("No completed runs in this window.\n\n")
	} else {
		b.WriteString("| Workflow | Role | Runs | Confidence | Re-runs | Success | p50 | p90 | Queue p50 | Queue p90 | Critical path |\n")
		b.WriteString("|---|---|---:|---|---:|---:|---:|---:|---:|---:|---|\n")
		for _, w := range r.Workflows {
			path := "unavailable"
			if len(w.CriticalPath) > 0 {
				path = strings.Join(w.CriticalPath, " → ")
			}
			fmt.Fprintf(&b, "| %s | %s | %d | %s | %d | %.0f%% | %s | %s | %s | %s | %s |\n", cell(w.Path), w.Role, w.Runs, confidence(w.Confidence), w.Reruns,
				100*w.SuccessRate, w.P50, w.P90, w.QueueP50, w.QueueP90, cell(path))
		}
		b.WriteString("\n")
		for _, w := range r.Workflows {
			renderWorkflow(&b, w)
		}
	}

	if len(r.Sinks) > 0 {
		b.WriteString("## Time sinks\n\nSteps ranked by p50 × runs.\n\n")
		for i, s := range r.Sinks {
			fmt.Fprintf(&b, "%d. %s / %s / `%s`: p50 %s, p90 %s × %d runs (%s confidence) = %s\n", i+1, s.Workflow, s.Job, code(stepLabel(s.Step, s.Occurrence)), s.P50, s.P90, s.Runs, confidence(s.Confidence), s.Weight)
			evidenceList(&b, "   ", s.Evidence)
		}
		b.WriteString("\n")
	}

	b.WriteString("## Signals\n\n")
	if len(r.Signals) == 0 {
		b.WriteString("No signals in this window.\n\n")
	} else {
		for _, s := range r.Signals {
			waste := "no waste class"
			if len(s.Waste) > 0 {
				waste = strings.Join(s.Waste, ", ")
			}
			fmt.Fprintf(&b, "- **%s** (%s; %s): %s\n", s.ID, s.Severity, waste, text(s.Summary))
			evidenceList(&b, "  ", s.Evidence)
		}
		b.WriteString("\n")
	}

	if len(r.Unavailable) > 0 {
		b.WriteString("## Unavailable\n\n")
		for _, u := range r.Unavailable {
			fmt.Fprintf(&b, "- **%s**: %s\n", text(u.What), text(u.Why))
			evidenceList(&b, "  ", u.Evidence)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// renderDefinitions says, per workflow, whether its workflow file changed
// inside the window, and where. A workflow whose history is missing (a report
// written before it existed) or partly unread is never shown as unchanged.
func renderDefinitions(b *strings.Builder, ws []WorkflowStats) {
	b.WriteString("## Workflow file changes\n\n")
	b.WriteString("Changes are placed on the default branch's push, schedule and workflow\\_dispatch runs only: each run's file is read at its head commit and compared by content (SHA-256) in run order. " +
		"Runs before and from a change count every run of the workflow. A version only other branches, tags or pull requests ran is a branch-only version, never a window change; " +
		"a pull request's is approximate (read at its head commit, but GitHub ran its merge ref).\n\n")
	for _, w := range ws {
		h := w.Definitions
		fmt.Fprintf(b, "- `%s`: ", code(w.Path))
		switch {
		case h == nil:
			b.WriteString("not recorded in this report\n")
			continue
		case h.RunsRead == 0:
			fmt.Fprintf(b, "unavailable: %s (cited under Unavailable)\n", text(h.Unavailable))
		case h.Unavailable == "":
			fmt.Fprintf(b, "%s across all %d default-branch runs; %s\n", plural(h.Versions, "version"), h.RunsRead, changes(len(h.Changes)))
		default:
			fmt.Fprintf(b, "%s across the %d default-branch runs read; %s among the runs read; %s (cited under Unavailable)\n", plural(h.Versions, "version"), h.RunsRead,
				changes(len(h.Changes)), text(h.Unavailable))
		}
		for _, c := range h.Changes {
			fmt.Fprintf(b, "  - run %d (created %s, head %s, %s on %s) first used a new version: %d runs before it, %d from it on",
				c.RunID, c.CreatedAt.UTC().Format(time.RFC3339), text(c.HeadSHA), text(c.Event), text(c.Branch), c.RunsBefore, c.RunsAfter)
			if c.JobsUnavailable != "" {
				fmt.Fprintf(b, "; %s", text(c.JobsUnavailable))
			} else {
				fmt.Fprintf(b, "; jobs added: %s; jobs removed: %s", list(c.JobsAdded), list(c.JobsRemoved))
			}
			if c.UnreadBetween > 0 {
				fmt.Fprintf(b, "; %d unread runs before it: the change may have come with one of them", c.UnreadBetween)
			}
			b.WriteString("\n")
			evidenceList(b, "    ", c.Evidence)
		}
		for _, v := range h.BranchOnly {
			short := v.SHA256
			if len(short) > 12 {
				short = short[:12]
			}
			fmt.Fprintf(b, "  - branch-only version %s on %s (%s): %d runs, first run %d (created %s, head %s); not a window change",
				short, text(strings.Join(v.Branches, ", ")), text(strings.Join(v.Events, ", ")), v.Runs, v.FirstRunID,
				v.FirstCreatedAt.UTC().Format(time.RFC3339), text(v.FirstHeadSHA))
			if v.Approximate {
				fmt.Fprintf(b, "; approximate: %s", text(v.ApproximateReason))
			}
			b.WriteString("\n")
			evidenceList(b, "    ", v.Evidence)
		}
		if n := len(h.OtherUnread); n > 0 {
			if h.BranchRuns == 0 {
				fmt.Fprintf(b, "  - %d other runs not read (cited under Unavailable)\n", n)
			} else {
				fmt.Fprintf(b, "  - %d other runs not read:\n", n)
				evidenceList(b, "    ", h.OtherUnread)
			}
		}
	}
	b.WriteString("\n")
}

func changes(n int) string {
	if n == 0 {
		return "no change"
	}
	return plural(n, "change")
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func list(s []string) string {
	if len(s) == 0 {
		return "none"
	}
	return text(strings.Join(s, ", "))
}

func renderWorkflow(b *strings.Builder, w WorkflowStats) {
	fmt.Fprintf(b, "### %s (%s)\n\n", text(w.Path), text(w.Name))
	fmt.Fprintf(b, "- Candidate role: %s (%s)\n", w.Role, text(w.RoleReason))
	fmt.Fprintf(b, "- Events: %s\n", strings.Join(w.Events, ", "))
	fmt.Fprintf(b, "- Runs: %d (%s confidence: n=%d), re-runs: %d, failures: %d, success rate %.0f%%\n", w.Runs, confidence(w.Confidence), w.Runs, w.Reruns, w.Failures, 100*w.SuccessRate)
	fmt.Fprintf(b, "- Duration p50 %s, p90 %s (start to last job completed; %d run ends estimated from updated\\_at); job queue p50 %s, p90 %s\n",
		w.P50, w.P90, w.EstimatedEnds, w.QueueP50, w.QueueP90)
	if w.CarriedOverJobs > 0 {
		fmt.Fprintf(b, "- %d jobs carried over by partial re-runs are cited below and not timed\n", w.CarriedOverJobs)
	}
	c := w.Critical
	if c.Unavailable != "" {
		fmt.Fprintf(b, "- Critical path: unavailable: %s\n", text(c.Unavailable))
	} else {
		fmt.Fprintf(b, "- Critical path: %s, p50 %s, from the definition at %s (run %d, %s on %s; [file](%s)), over %d runs whose job lists match it and whose timings do not contradict its needs edges (%d did not list matching jobs, %d contradicted its needs edges)\n",
			strings.Join(w.CriticalPath, " → "), c.P50, c.Ref, c.SourceRunID, c.Event, text(c.Branch), c.Definition, c.BasisRuns, c.UnmatchedRuns, len(c.Rejected))
		for _, n := range c.Nodes {
			fmt.Fprintf(b, "  - `%s` (%s): p50 %s over %d runs\n", code(n.Job), n.Kind, n.P50, n.Runs)
			evidenceList(b, "    ", n.Evidence)
		}
	}
	if len(c.Rejected) > 0 {
		b.WriteString("  - Runs excluded because their timings contradict the definition's needs edges:\n")
		evidenceList(b, "    ", c.Rejected)
	}
	b.WriteString("\n")
	if len(w.Jobs) > 0 {
		b.WriteString("#### Jobs\n\n")
		for _, j := range w.Jobs {
			fmt.Fprintf(b, "- `%s`: %d runs (%s confidence), p50 %s, p90 %s, queue p50 %s, p90 %s\n", code(j.Name), j.Runs, confidence(j.Confidence), j.P50, j.P90, j.QueueP50, j.QueueP90)
			evidenceList(b, "  ", j.Evidence)
			evidenceList(b, "  ", j.QueueEvidence)
			for _, s := range j.Steps {
				fmt.Fprintf(b, "  - step `%s`: %d runs (%s confidence), p50 %s, p90 %s\n", code(stepLabel(s.Name, s.Occurrence)), s.Runs, confidence(s.Confidence), s.P50, s.P90)
				evidenceList(b, "    ", s.Evidence)
			}
		}
		b.WriteString("\n")
	}
	b.WriteString("#### Runs\n\n")
	evidenceList(b, "", w.Evidence)
	b.WriteString("\n")
}

func evidenceList(b *strings.Builder, indent string, ev []signal.Evidence) {
	for _, e := range ev {
		fmt.Fprintf(b, "%s- [%s](%s)", indent, text(label(e)), e.URL)
		if e.Note != "" {
			fmt.Fprintf(b, " %s", text(e.Note))
		}
		b.WriteString("\n")
	}
}

func label(e signal.Evidence) string {
	switch {
	case e.Kind == signal.KindStep:
		return fmt.Sprintf("step %s of job %d", e.Step, e.JobID)
	case e.JobID != 0:
		return fmt.Sprintf("%s %d", e.Kind, e.JobID)
	case e.Kind == signal.KindFile && e.Line > 0:
		return fmt.Sprintf("%s:%d", e.Path, e.Line)
	case e.Kind == signal.KindFile:
		return e.Path
	case e.RunID != 0:
		return fmt.Sprintf("%s %d", e.Kind, e.RunID)
	}
	return e.Kind
}

// text escapes characters that would change Markdown structure.
func text(s string) string {
	return strings.NewReplacer("\n", " ", "[", `\[`, "]", `\]`, "|", `\|`, "<", "&lt;", "*", `\*`, "_", `\_`, "`", "'").Replace(s)
}

// confidence renders a label; a report written before labels existed has
// none, and says so rather than guessing one.
func confidence(label string) string {
	if label == "" {
		return "unlabelled"
	}
	return label
}

func cell(s string) string { return strings.NewReplacer("|", `\|`, "\n", " ").Replace(s) }

func code(s string) string { return strings.NewReplacer("`", "'", "\n", " ").Replace(s) }
