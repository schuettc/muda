package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/schuettc/muda/internal/gh"
	"github.com/schuettc/muda/internal/logs"
	tools "github.com/schuettc/tools-common"
	"io"
	"os"
	ossignal "os/signal"
	"sort"
	"strconv"
	"strings"
	"time"
)

const logsHelp = "Lists final-attempt jobs for a known run ID, or reads a selected job's log. Splits timestamped groups, strips ANSI; --step matches a group name exactly and --grep filters lines by regular expression. --since is ignored (the run ID selects the run); it is still validated. --until is ignored and validated the same way: a future --until is rejected."

type logsOptions struct {
	job        int64
	step, grep string
}

func logsFlags() (*flag.FlagSet, *Common, *logsOptions) {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	c := AddCommon(fs)
	o := &logsOptions{}
	fs.Int64Var(&o.job, "job", 0, "job ID in the run's final attempt")
	fs.StringVar(&o.step, "step", "", "exact log group name")
	fs.StringVar(&o.grep, "grep", "", "regular expression line filter")
	tools.SetUsage(fs, "muda logs <run-id> [--job ID] [--step NAME] [--grep RE] [--repo OWNER/NAME] [--since 30d] [--until DATE] [--format json|md] [--no-cache]", logsHelp)
	return fs, c, o
}

type logsReport struct {
	Schema int         `json:"schema"`
	Repo   string      `json:"repo"`
	RunID  int64       `json:"run_id"`
	JobID  int64       `json:"job_id"`
	Jobs   []gh.Job    `json:"jobs"`
	Steps  []logs.Step `json:"steps"`
	URLs   []string    `json:"evidence_urls"`
}

func Logs(env Env) tools.Command {
	return tools.Command{Name: "logs", Summary: "final-attempt jobs or filtered job logs", Synopsis: "<run-id> [--job ID] [--step NAME] [--grep RE] [--repo OWNER/NAME] [--since 30d] [--until DATE] [--format json|md] [--no-cache]", Help: logsHelp, NewFlags: func() *flag.FlagSet { fs, _, _ := logsFlags(); return fs }, Run: func(args []string, out, _ io.Writer) error {
		fs, common, o := logsFlags()
		// flag stops at the first positional argument; the documented run ID
		// precedes flags, so move that leading positional to the end.
		if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
			args = append(append([]string{}, args[1:]...), args[0])
		}
		if err := tools.ParseFlags(fs, args, out); err != nil {
			return err
		}
		if fs.NArg() != 1 {
			return tools.UsageError{Msg: "expected one run ID"}
		}
		runID, err := strconv.ParseInt(fs.Arg(0), 10, 64)
		if err != nil || runID <= 0 {
			return tools.UsageError{Msg: "run ID must be a positive integer"}
		}
		jobSet := false
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "job" {
				jobSet = true
			}
		})
		if o.job < 0 || (jobSet && o.job == 0) {
			return tools.UsageError{Msg: "job ID must be a positive integer"}
		}
		if _, err := logs.Filter(nil, o.step, o.grep); err != nil {
			return tools.UsageError{Msg: err.Error()}
		}
		if o.job == 0 && (o.step != "" || o.grep != "") {
			return tools.UsageError{Msg: "--step and --grep require --job"}
		}
		if err := common.Resolve(env.GitRemote, env.Now); err != nil {
			return tools.UsageError{Msg: err.Error()}
		}
		c, err := env.client(common.NoCache)
		if err != nil {
			return err
		}
		ctx, stop := ossignal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		jobs, err := logs.Jobs(ctx, c, common.Repo, runID, common.Since)
		if err != nil {
			return err
		}
		sort.Slice(jobs, func(i, j int) bool { return jobs[i].ID < jobs[j].ID })
		r := logsReport{Schema: 1, Repo: common.Repo, RunID: runID, JobID: o.job, Jobs: []gh.Job{}, Steps: []logs.Step{}, URLs: []string{}}
		if o.job == 0 {
			r.Jobs = jobs
			for i, j := range r.Jobs {
				if j.Steps == nil {
					r.Jobs[i].Steps = []gh.Step{}
				}
				if j.Labels == nil {
					r.Jobs[i].Labels = []string{}
				}
				u := j.HTMLURL
				if u == "" {
					u = fmt.Sprintf("https://github.com/%s/actions/runs/%d/job/%d", common.Repo, runID, j.ID)
				}
				r.URLs = append(r.URLs, u)
			}
		} else {
			r.Steps, err = logs.FetchJob(ctx, c, common.Repo, runID, o.job, jobs, o.step, o.grep)
			if err != nil {
				return err
			}
			for _, j := range jobs {
				if j.ID == o.job {
					u := j.HTMLURL
					if u == "" {
						u = fmt.Sprintf("https://github.com/%s/actions/runs/%d/job/%d", common.Repo, runID, j.ID)
					}
					r.URLs = append(r.URLs, u)
				}
			}
		}
		if common.Format == "md" {
			var b strings.Builder
			fmt.Fprintf(&b, "# muda logs: %s run %d\n\n", r.Repo, runID)
			if o.job == 0 {
				if len(jobs) == 0 {
					b.WriteString("No jobs observed in the final attempt.\n")
				}
				for _, j := range jobs {
					fmt.Fprintf(&b, "- %d: %s (%s)\n", j.ID, j.Name, j.Conclusion)
				}
			} else {
				fmt.Fprintf(&b, "Job %d: %d matching log groups.\n", o.job, len(r.Steps))
				for _, s := range r.Steps {
					fmt.Fprintf(&b, "\n## %s\n\n", s.Name)
					start, end := "unavailable", "unavailable"
					if !s.Start.IsZero() {
						start = s.Start.Format(time.RFC3339Nano)
					}
					if !s.End.IsZero() {
						end = s.End.Format(time.RFC3339Nano)
					}
					fmt.Fprintf(&b, "Start: %s; end: %s.\n\n", start, end)
					if len(s.Lines) == 0 {
						b.WriteString("No lines.\n")
					} else {
						for _, line := range s.Lines {
							fmt.Fprintf(&b, "    %s\n", line)
						}
					}
				}
			}
			if len(r.URLs) > 0 {
				b.WriteString("\n## Evidence\n\n")
				for _, u := range r.URLs {
					fmt.Fprintf(&b, "- %s\n", u)
				}
			}
			_, err = io.WriteString(out, b.String())
			return err
		}
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(r)
	}}
}
