// Package notices groups warning and notice annotations with their source evidence.
package notices

import (
	"context"
	"errors"
	"fmt"
	"github.com/schuettc/muda/internal/gh"
	"github.com/schuettc/muda/internal/signal"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const LevelUnavailable = "unavailable"

type Group struct {
	Signature string            `json:"signature"`
	Example   string            `json:"example"`
	Level     string            `json:"level"`
	Count     int               `json:"count"`
	FirstSeen time.Time         `json:"first_seen,omitzero"`
	LastSeen  time.Time         `json:"last_seen,omitzero"`
	Sources   []signal.Evidence `json:"sources"`
}

// Run groups the annotations of runs created in [since, until), listed by
// gh.RunsWindow: live, bounded and complete or an error.
func Run(ctx context.Context, c *gh.Client, repo string, since, until time.Time) ([]Group, []signal.Signal, error) {
	runs, err := c.RunsWindow(ctx, repo, since, until)
	if err != nil {
		return nil, nil, err
	}
	sort.Slice(runs, func(i, j int) bool {
		if runs[i].CreatedAt.Equal(runs[j].CreatedAt) {
			return runs[i].ID < runs[j].ID
		}
		return runs[i].CreatedAt.Before(runs[j].CreatedAt)
	})
	groups := map[string]*Group{}
	// Each check run is one immutable annotation source. Iterate every returned
	// position once: identical annotations at different positions are distinct.
	observedChecks := map[int64]bool{}
	add := func(sig, msg, level string, seen time.Time, e signal.Evidence) {
		key := sig
		if level == LevelUnavailable {
			key = "unavailable:" + sig
		}
		g := groups[key]
		if g == nil {
			g = &Group{Signature: sig, Example: msg, Level: level, FirstSeen: seen, LastSeen: seen, Sources: []signal.Evidence{}}
			groups[key] = g
		}
		if level == "warning" {
			g.Level = "warning"
		}
		g.Count++
		g.Sources = append(g.Sources, e)
		if seen.Before(g.FirstSeen) {
			g.FirstSeen = seen
			g.Example = msg
		}
		if seen.After(g.LastSeen) {
			g.LastSeen = seen
		}
	}
	missing := func(what string, err error, seen time.Time, e signal.Evidence) error {
		var api *gh.Error
		if !errors.As(err, &api) {
			return err
		}
		reason := what + " unavailable: " + err.Error()
		add(reason, reason, LevelUnavailable, seen, e)
		return nil
	}
	for _, run := range runs {
		if run.ID <= 0 || run.RunAttempt < 1 {
			return nil, nil, fmt.Errorf("run %d unavailable: missing run identity or attempt", run.ID)
		}
		if run.CreatedAt.Before(since) || !run.CreatedAt.Before(until) {
			continue
		}
		runURL := run.HTMLURL
		if runURL == "" {
			runURL = fmt.Sprintf("https://github.com/%s/actions/runs/%d", repo, run.ID)
		}
		for attempt := 1; attempt <= run.RunAttempt; attempt++ {
			jobs, err := c.Jobs(ctx, repo, run.ID, attempt)
			if err != nil {
				if err := missing("jobs", err, time.Time{}, signal.Evidence{Kind: signal.KindRun, URL: runURL, RunID: run.ID, Note: fmt.Sprintf("attempt %d", attempt)}); err != nil {
					return nil, nil, err
				}
				continue
			}
			sort.Slice(jobs, func(i, j int) bool { return jobs[i].ID < jobs[j].ID })
			for _, job := range jobs {
				if job.RunID != run.ID {
					return nil, nil, fmt.Errorf("job %d does not belong to run %d", job.ID, run.ID)
				}
				jobURL := job.HTMLURL
				if jobURL == "" {
					jobURL = fmt.Sprintf("%s/job/%d", runURL, job.ID)
				}
				seen := job.CompletedAt
				if seen.IsZero() {
					seen = job.StartedAt
				}
				if seen.IsZero() {
					seen = job.CreatedAt
				}
				ev := signal.Evidence{Kind: signal.KindAnnotation, URL: jobURL, RunID: run.ID, JobID: job.ID}
				if job.RunAttempt != 0 && job.RunAttempt != attempt {
					reason := fmt.Sprintf("annotations unavailable: job %d attempt %d does not match requested attempt %d", job.ID, job.RunAttempt, attempt)
					add(reason, reason, LevelUnavailable, time.Time{}, ev)
					continue
				}
				if seen.IsZero() {
					reason := fmt.Sprintf("annotations unavailable: job %d has no observation timestamp", job.ID)
					add(reason, reason, LevelUnavailable, time.Time{}, ev)
					continue
				}
				u, parseErr := url.Parse(job.CheckRunURL)
				expected := "/repos/" + repo + "/check-runs/"
				id := int64(0)
				if parseErr == nil && u != nil && u.Scheme == "https" && u.Host == "api.github.com" && strings.HasPrefix(u.Path, expected) {
					id, _ = strconv.ParseInt(strings.TrimPrefix(u.Path, expected), 10, 64)
				}
				if id <= 0 {
					reason := "annotations unavailable: missing or invalid check run URL"
					add(reason, reason, LevelUnavailable, seen, ev)
					continue
				}
				if observedChecks[id] {
					continue
				}
				observedChecks[id] = true
				anns, err := c.Annotations(ctx, repo, id)
				if err != nil {
					if err := missing("annotations", err, seen, ev); err != nil {
						return nil, nil, err
					}
					continue
				}
				for position, a := range anns {
					if a.AnnotationLevel != "warning" && a.AnnotationLevel != "notice" {
						continue
					}
					source := ev
					source.Path = a.Path
					source.Line = a.StartLine
					source.Note = fmt.Sprintf("check run %d annotation %d: %s", id, position+1, a.Message)
					add(Signature(a.Message), a.Message, a.AnnotationLevel, seen, source)
				}
			}
		}
	}
	out := []Group{}
	for _, g := range groups {
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i].Level == LevelUnavailable) != (out[j].Level == LevelUnavailable) {
			return out[j].Level == LevelUnavailable
		}
		if out[i].Signature == out[j].Signature {
			return out[i].Level < out[j].Level
		}
		return out[i].Signature < out[j].Signature
	})
	sigs := []signal.Signal{}
	for _, g := range out {
		if g.Level == LevelUnavailable {
			continue
		}
		severity := signal.SeverityInfo
		if g.Level == "warning" {
			severity = signal.SeverityWarn
		}
		sigs = append(sigs, signal.New("advance-notice", severity, fmt.Sprintf("%s seen ×%d since %s: %s", g.Level, g.Count, g.FirstSeen.Format("2006-01-02"), g.Example), g.Sources))
	}
	return out, sigs, nil
}
