package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	ossignal "os/signal"

	"github.com/schuettc/muda/internal/scan"
	tools "github.com/schuettc/tools-common"
)

const scanHelp = `Static analysis of .github/workflows/*.yml at the given ref (default: repo
default branch). For each workflow, reports signals in the scan family:
floating-ref, floating-runner, floating-toolchain, emulation, uncached-install,
serial-independent-jobs, duplicate-check-set, no-concurrency, no-path-filter,
and toolchain-drift (one setup action pinned to different versions in
different workflow files).

no-path-filter requires p50 run duration ≥5 min; durations come from the last
20 completed runs per workflow created in the window [since, until). --until
takes the same forms as --since, defaults to now, must follow --since and may
not be in the future; it bounds run history only and never selects the ref.
Workflows with no run history in the window are listed as skipped (not a
green result).

Signals are data: exit code is 0. A missing API result, permission or evidence
is unavailable with a reason, never an empty success.`

func scanFlags() (*flag.FlagSet, *Common, *string) {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	c := AddCommon(fs)
	ref := fs.String("ref", "", "branch, tag or SHA to scan (default: repo default branch)")
	tools.SetUsage(fs, "muda scan [--repo OWNER/NAME] [--ref REF] [--since 30d] [--until DATE] [--format json|md] [--no-cache]", scanHelp)
	return fs, c, ref
}

// Scan is the `muda scan` command.
func Scan(env Env) tools.Command {
	return tools.Command{
		Name:     "scan",
		Summary:  "static workflow signals: floating refs, uncached installs and more",
		Synopsis: "[--repo OWNER/NAME] [--ref REF] [--since 30d] [--until DATE] [--format json|md] [--no-cache]",
		Help:     scanHelp,
		NewFlags: func() *flag.FlagSet { fs, _, _ := scanFlags(); return fs },
		Run: func(args []string, out, _ io.Writer) error {
			fs, common, ref := scanFlags()
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
			ctx, stop := ossignal.NotifyContext(context.Background(), os.Interrupt)
			defer stop()
			report, err := scan.Run(ctx, c, common.Repo, *ref, common.Since, common.Until)
			if err != nil {
				return err
			}
			if common.Format == "md" {
				_, err = io.WriteString(out, scan.RenderMarkdown(report))
				return err
			}
			return scan.WriteJSON(out, report)
		},
	}
}
