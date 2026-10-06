package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"time"

	"github.com/schuettc/muda/internal/gh"
	"github.com/schuettc/muda/internal/measure"
	tools "github.com/schuettc/tools-common"
)

// Env is what a GitHub-reading command takes from its surroundings. Tests
// inject it; DefaultEnv is the process's own.
type Env struct {
	Getenv    func(string) string
	GHToken   func() (string, error)
	GitRemote func() (string, error)
	Now       func() time.Time
	HTTP      *http.Client
	BaseURL   string // "" means the public GitHub API
	CacheDir  string // "" means the family user cache dir
}

// DefaultEnv reads the real environment, `gh auth token` and the origin remote.
func DefaultEnv() Env {
	return Env{
		Getenv: os.Getenv,
		GHToken: func() (string, error) {
			out, err := exec.Command("gh", "auth", "token").Output()
			return strings.TrimSpace(string(out)), err
		},
		GitRemote: func() (string, error) {
			out, err := exec.Command("git", "remote", "get-url", "origin").Output()
			return strings.TrimSpace(string(out)), err
		},
		Now: time.Now,
	}
}

// client resolves the token and builds the API client. The token is never
// printed or put in an error.
func (e Env) client(noCache bool) (*gh.Client, error) {
	token, err := gh.ResolveToken(e.Getenv, e.GHToken)
	if err != nil {
		return nil, err
	}
	return gh.New(gh.Options{Token: token, BaseURL: e.BaseURL, CacheDir: e.CacheDir, NoCache: noCache, HTTP: e.HTTP}), nil
}

const measureHelp = `Reports where the delivery time goes over the window: for each workflow its
run count, p50 and p90 duration, queue time, success rate, re-runs, per-job
and per-step durations and critical path; the top time sinks; and the
signals only run data shows (slow-step, queue-time, rerun, tree-rechecked,
handoff-failure). Each workflow gets a candidate role to confirm. Every
number cites its runs, jobs or steps. Signals are data: the exit code is 0.

Each run's workflow file is read at its head commit. Changes are placed on the
default branch's push, schedule and workflow_dispatch runs: each is reported
with its first run, head commit and the runs before and after it (an
unreadable file is unavailable, never "no change"). Versions only other
branches, tags or pull requests ran are branch-only, never window changes; a
pull request's is approximate, since GitHub runs its merge ref, not its head.
Workflow, job, step and sink stats carry a confidence label by run count:
low below 10, medium below 30, high from 30. Labels never hide a number.

The window is [since, until): runs created at or after --since and before
--until. --until takes the same forms as --since and defaults to now; it must
follow --since and may not be in the future. The run listing is live and must
be complete, or the command fails.`

func measureFlags() (*flag.FlagSet, *Common) {
	fs := flag.NewFlagSet("measure", flag.ContinueOnError)
	c := AddCommon(fs)
	tools.SetUsage(fs, "muda measure [--repo OWNER/NAME] [--since 30d] [--until DATE] [--format json|md] [--no-cache]", measureHelp)
	return fs, c
}

// Measure is the `muda measure` command.
func Measure(env Env) tools.Command {
	return tools.Command{
		Name:     "measure",
		Summary:  "where the delivery time goes, with evidence",
		Synopsis: "[--repo OWNER/NAME] [--since 30d] [--until DATE] [--format json|md] [--no-cache]",
		Help:     measureHelp,
		NewFlags: func() *flag.FlagSet { fs, _ := measureFlags(); return fs },
		Run: func(args []string, out, _ io.Writer) error {
			fs, common := measureFlags()
			if err := tools.ParseFlags(fs, args, out); err != nil {
				return err
			}
			if fs.NArg() > 0 {
				return tools.UsageError{Msg: fmt.Sprintf("unexpected argument %q", fs.Arg(0))}
			}
			if err := common.Resolve(env.GitRemote, env.Now); err != nil {
				return tools.UsageError{Msg: err.Error()}
			}
			c, err := env.client(common.NoCache)
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
			defer stop()
			report, err := measure.Run(ctx, c, common.Repo, common.Since, common.Until.UTC())
			if err != nil {
				return err
			}
			if common.Format == "md" {
				_, err = io.WriteString(out, measure.RenderMarkdown(report))
				return err
			}
			return measure.WriteJSON(out, report)
		},
	}
}
