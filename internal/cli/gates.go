package cli

import (
	"context"
	"flag"
	"fmt"
	"github.com/schuettc/muda/internal/gates"
	tools "github.com/schuettc/tools-common"
	"io"
	"os"
	ossignal "os/signal"
	"strings"
)

const gatesHelp = "Inventories candidate required checks, rulesets, environments and deploy/test dependencies. Retains typed security settings. Missing access is unavailable, not a no-gates pass. Repository settings come from the default branch; workflows are read at --ref (the default branch if omitted), and each records the SHA-256 of its raw file. An unreadable ref is unavailable. --since is ignored (gates read current settings and workflows); it is still validated like every command's common flags. --until is ignored and validated the same way: a future --until is rejected."

func gatesFlags() (*flag.FlagSet, *Common, *string) {
	fs := flag.NewFlagSet("gates", flag.ContinueOnError)
	c := AddCommon(fs)
	ref := fs.String("ref", "", "read workflow files at this branch, tag or SHA (default: the default branch)")
	tools.SetUsage(fs, "muda gates [--ref REF] [--repo OWNER/NAME] [--since 30d] [--until DATE] [--format json|md] [--no-cache]", gatesHelp)
	return fs, c, ref
}
func Gates(env Env) tools.Command {
	return tools.Command{Name: "gates", Summary: "candidate delivery gates and security settings", Synopsis: "[--ref REF] [--repo OWNER/NAME] [--since 30d] [--until DATE] [--format json|md] [--no-cache]", Help: gatesHelp, NewFlags: func() *flag.FlagSet { fs, _, _ := gatesFlags(); return fs }, Run: func(args []string, out, _ io.Writer) error {
		fs, common, ref := gatesFlags()
		if err := tools.ParseFlags(fs, args, out); err != nil {
			return err
		}
		if fs.NArg() > 0 {
			return tools.UsageError{Msg: fmt.Sprintf("unexpected argument %q", fs.Arg(0))}
		}
		if err := common.Resolve(env.GitRemote, env.Now); err != nil {
			return tools.UsageError{Msg: err.Error()}
		}
		refSet := false
		fs.Visit(func(f *flag.Flag) { refSet = refSet || f.Name == "ref" })
		if refSet && strings.TrimSpace(*ref) == "" {
			// An explicit empty ref would silently fall back to the default branch.
			return tools.UsageError{Msg: "--ref must name a branch, tag or SHA"}
		}
		c, err := env.client(common.NoCache)
		if err != nil {
			return err
		}
		ctx, stop := ossignal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		r, err := gates.Derive(ctx, c, common.Repo, *ref)
		if err != nil {
			return err
		}
		if common.Format == "md" {
			_, err = io.WriteString(out, gates.RenderMarkdown(r))
			return err
		}
		return gates.WriteJSON(out, r)
	}}
}
