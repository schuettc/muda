package notices

import (
	"encoding/json"
	"fmt"
	"github.com/schuettc/muda/internal/signal"
	"io"
	"strings"
	"time"
)

type Report struct {
	Schema      int             `json:"schema"`
	Repo        string          `json:"repo"`
	Since       time.Time       `json:"since"`
	Until       time.Time       `json:"until"` // exclusive: the window is [since, until)
	Groups      []Group         `json:"groups"`
	Signals     []signal.Signal `json:"signals"`
	Unavailable []Group         `json:"unavailable"`
}

func NewReport(repo string, since, until time.Time, groups []Group, sigs []signal.Signal) *Report {
	r := &Report{Schema: 1, Repo: repo, Since: since, Until: until, Groups: []Group{}, Signals: append([]signal.Signal{}, sigs...), Unavailable: []Group{}}
	for _, g := range groups {
		if g.Level == LevelUnavailable {
			r.Unavailable = append(r.Unavailable, g)
		} else {
			r.Groups = append(r.Groups, g)
		}
	}
	return r
}
func WriteJSON(w io.Writer, r *Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}
func RenderMarkdown(r *Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# muda notices: %s\n\nWindow: [%s, %s).\n", r.Repo, r.Since.Format(time.RFC3339), r.Until.Format(time.RFC3339))
	for _, section := range []struct {
		name   string
		groups []Group
	}{{"Notices", r.Groups}, {"Unavailable", r.Unavailable}} {
		if len(section.groups) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n## %s\n", section.name)
		for _, g := range section.groups {
			fmt.Fprintf(&b, "\n- %s ×%d: %s\n", g.Level, g.Count, g.Example)
			first, last := "unavailable", "unavailable"
			if !g.FirstSeen.IsZero() {
				first = g.FirstSeen.Format(time.RFC3339Nano)
			}
			if !g.LastSeen.IsZero() {
				last = g.LastSeen.Format(time.RFC3339Nano)
			}
			fmt.Fprintf(&b, "  - First seen: %s; last seen: %s.\n", first, last)
			for _, e := range g.Sources {
				fmt.Fprintf(&b, "  - %s (%s:%d): %s\n", e.URL, e.Path, e.Line, e.Note)
			}
		}
	}
	if len(r.Groups) == 0 && len(r.Unavailable) == 0 {
		b.WriteString("\nNo warning or notice annotations observed in the readable window.\n")
	}
	return b.String()
}
