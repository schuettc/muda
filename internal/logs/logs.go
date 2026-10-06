// Package logs reads final-attempt job logs and splits them into timestamped groups.
package logs

import (
	"context"
	"fmt"
	"github.com/schuettc/muda/internal/gh"
	"regexp"
	"strings"
	"time"
)

type Step struct {
	Name  string    `json:"name"`
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	Lines []string  `json:"lines"`
}

var ansi = regexp.MustCompile("\x1b(?:\\[[0-?]*[ -/]*[@-~]|\\][^\x07]*(?:\x07|\x1b\\\\))")

func Split(raw string) []Step {
	out := []Step{}
	current := Step{Name: "Log", Lines: []string{}}
	flush := func() {
		if len(current.Lines) > 0 || current.Name != "Log" {
			out = append(out, current)
		}
		current = Step{Name: "Log", Lines: []string{}}
	}
	for _, line := range strings.Split(strings.TrimPrefix(raw, "\ufeff"), "\n") {
		line = strings.TrimSuffix(ansi.ReplaceAllString(line, ""), "\r")
		if line == "" {
			continue
		}
		var stamp time.Time
		if prefix, rest, ok := strings.Cut(line, " "); ok {
			if t, err := time.Parse(time.RFC3339Nano, prefix); err == nil {
				stamp = t
				line = rest
			}
		}
		if strings.HasPrefix(line, "##[group]") {
			flush()
			current.Name = strings.TrimPrefix(line, "##[group]")
			current.Start = stamp
			current.End = stamp
			continue
		}
		if !stamp.IsZero() {
			if current.Start.IsZero() {
				current.Start = stamp
			}
			current.End = stamp
		}
		if strings.HasPrefix(line, "##[endgroup]") {
			flush()
			continue
		}
		current.Lines = append(current.Lines, line)
	}
	flush()
	return out
}
func Filter(steps []Step, step, grep string) ([]Step, error) {
	var re *regexp.Regexp
	var err error
	if grep != "" {
		re, err = regexp.Compile(grep)
		if err != nil {
			return nil, fmt.Errorf("invalid --grep: %w", err)
		}
	}
	out := []Step{}
	for _, s := range steps {
		if step != "" && s.Name != step {
			continue
		}
		lines := []string{}
		for _, l := range s.Lines {
			if re == nil || re.MatchString(l) {
				lines = append(lines, l)
			}
		}
		if re != nil && len(lines) == 0 {
			continue
		}
		s.Lines = lines
		out = append(out, s)
	}
	return out, nil
}

// Jobs reads live run metadata and only the final attempt. since is retained
// for API compatibility; a known run ID is independent of discovery windows.
func Jobs(ctx context.Context, c *gh.Client, repo string, runID int64, _ time.Time) ([]gh.Job, error) {
	r, err := c.Run(ctx, repo, runID)
	if err != nil {
		return nil, err
	}
	jobs, err := c.Jobs(ctx, repo, runID, r.RunAttempt)
	if err != nil {
		return nil, err
	}
	out := []gh.Job{}
	for _, j := range jobs {
		if j.RunID != runID {
			return nil, fmt.Errorf("job %d does not belong to run %d", j.ID, runID)
		}
		if j.RunAttempt != 0 && j.RunAttempt != r.RunAttempt {
			return nil, fmt.Errorf("job %d is not in final attempt", j.ID)
		}
		out = append(out, j)
	}
	return out, nil
}
func Fetch(ctx context.Context, c *gh.Client, repo string, runID, jobID int64, step, grep string) ([]Step, error) {
	if _, err := Filter(nil, step, grep); err != nil {
		return nil, err
	}
	jobs, err := Jobs(ctx, c, repo, runID, time.Time{})
	if err != nil {
		return nil, err
	}
	return FetchJob(ctx, c, repo, runID, jobID, jobs, step, grep)
}

// FetchJob accepts the already verified final-attempt listing used by the CLI.
func FetchJob(ctx context.Context, c *gh.Client, repo string, runID, jobID int64, jobs []gh.Job, step, grep string) ([]Step, error) {
	for _, j := range jobs {
		if j.ID == jobID && j.RunID == runID {
			raw, err := c.JobLog(ctx, repo, jobID)
			if err != nil {
				return nil, err
			}
			return Filter(Split(raw), step, grep)
		}
	}
	return nil, fmt.Errorf("job %d unavailable: not in final attempt of run %d", jobID, runID)
}
