package compare

import (
	"encoding/json"
	"fmt"
	"github.com/schuettc/muda/internal/gates"
	"github.com/schuettc/muda/internal/signal"
	"io"
	"strings"
	"time"
)

func WriteJSON(w io.Writer, r *Result) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}
func RenderMarkdown(r *Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Delivery comparison\n\nSchema: %d\n\nBefore: `%s`\n\nAfter: `%s`\n\n", r.Schema, windowText(r.Before), windowText(r.After))
	for _, section := range []struct {
		name string
		ds   []Delta
	}{{"Workflows", r.Workflows}, {"Steps", r.Steps}} {
		fmt.Fprintf(&b, "## %s\n\n", section.name)
		if len(section.ds) == 0 {
			b.WriteString("Unavailable: no completed samples.\n\n")
		}
		for _, d := range section.ds {
			fmt.Fprintf(&b, "### %s / %s / %s (%d)\n\nPresence: %s\n\n", d.Workflow, d.Job, d.Name, d.Occurrence, d.Presence)
			for _, side := range []struct {
				name string
				t    *Timing
			}{{"Before", d.Before}, {"After", d.After}} {
				if side.t == nil {
					fmt.Fprintf(&b, "%s: unavailable\n\n", side.name)
					continue
				}
				fmt.Fprintf(&b, "%s: %d samples; p50 %s; p90 %s\n\n", side.name, side.t.Runs, side.t.P50, side.t.P90)
				for _, e := range side.t.Evidence {
					fmt.Fprintf(&b, "- %s — %s\n", e.URL, e.Note)
				}
			}
			if d.ChangeNS != nil {
				fmt.Fprintf(&b, "\nChange: %s; relative: ", *d.ChangeNS)
				if d.RelativePercent == nil {
					b.WriteString("undefined (zero baseline)\n\n")
				} else {
					fmt.Fprintf(&b, "%.2f%%\n\n", *d.RelativePercent)
				}
			}
		}
	}
	for _, u := range r.BeforeUnavailable {
		fmt.Fprintf(&b, "Before unavailable: %s — %s\n", u.What, u.Why)
		for _, e := range u.Evidence {
			fmt.Fprintf(&b, "- %s — %s\n", e.URL, e.Note)
		}
	}
	for _, u := range r.AfterUnavailable {
		fmt.Fprintf(&b, "After unavailable: %s — %s\n", u.What, u.Why)
		for _, e := range u.Evidence {
			fmt.Fprintf(&b, "- %s — %s\n", e.URL, e.Note)
		}
	}
	if d := r.Gates; d != nil {
		fmt.Fprintf(&b, "\n## Gates: %s\n\n", d.Status)
		if d.BeforeRef != "" {
			fmt.Fprintf(&b, "Before side: workflows at `%s`; %s.\n\n", d.BeforeRef, d.BeforeSettings)
		}
		if d.BeforeRepo != "" {
			fmt.Fprintf(&b, "Before side: recorded under `%s`, which GitHub redirects to this repository.\n\n", d.BeforeRepo)
		}
		write := func(name string, gs []gates.Gate) {
			if len(gs) == 0 {
				return
			}
			fmt.Fprintf(&b, "### %s\n\n", name)
			data, _ := json.MarshalIndent(gs, "", "  ")
			fmt.Fprintf(&b, "```json\n%s\n```\n\n", data)
		}
		write("Removed", d.Removed)
		write("Loosened", d.Loosened)
		write("Added", d.Added)
		write("Strengthened", d.Strengthened)
		write("Unverified", d.Unverified)
		if len(d.WorkflowDiffs) > 0 {
			b.WriteString("### Workflow changes\n\n")
		}
		for _, w := range d.WorkflowDiffs {
			fmt.Fprintf(&b, "#### `%s` (%s)\n\nProof: `gate:%s`\n\nNote: %s.\n\n", w.Path, w.Change, w.ProofID, w.Note)
			for _, f := range w.Fields {
				fmt.Fprintf(&b, "- `%s`: %s → %s\n", f.Where, renderValue(f.Old), renderValue(f.New))
			}
			if len(w.Fields) > 0 {
				b.WriteString("\n")
			}
		}
		for _, id := range d.Exempted {
			fmt.Fprintf(&b, "Exempted: `gate:%s`\n", id)
		}
		for _, e := range d.Evidence {
			fmt.Fprintf(&b, "- %s — %s\n", e.URL, e.Note)
		}
	}
	if d := r.Scan; d != nil {
		fmt.Fprintf(&b, "\n## Scan: %s\n\n", d.Status)
		if d.BeforeRef != "" {
			fmt.Fprintf(&b, "Before side: scan at `%s`.\n\n", d.BeforeRef)
		}
		if d.BeforeRepo != "" {
			fmt.Fprintf(&b, "Before side: recorded under `%s`, which GitHub redirects to this repository.\n\n", d.BeforeRepo)
		}
		if d.Status == ScanUnavailable {
			fmt.Fprintf(&b, "Unavailable: %s\n", d.Unavailable)
		} else {
			for _, section := range []struct {
				name string
				ss   []signal.Signal
			}{{"Removed", d.Removed}, {"Added", d.Added}, {"Unchanged", d.Unchanged}} {
				fmt.Fprintf(&b, "### %s (%d)\n\n", section.name, len(section.ss))
				for _, s := range section.ss {
					fmt.Fprintf(&b, "- `%s` — %s\n", s.ID, s.Summary)
					for _, e := range s.Evidence {
						where := e.Path
						if e.Line > 0 {
							where = fmt.Sprintf("%s:%d", e.Path, e.Line)
						}
						fmt.Fprintf(&b, "  - %s %s\n", where, e.URL)
					}
				}
				b.WriteString("\n")
			}
		}
	}
	return b.String()
}
func windowText(w Window) string {
	if len(w.RunIDs) > 0 {
		return fmt.Sprintf("run IDs %v", w.RunIDs)
	}
	return w.Since.UTC().Format(time.RFC3339Nano) + ".." + w.Until.UTC().Format(time.RFC3339Nano)
}
