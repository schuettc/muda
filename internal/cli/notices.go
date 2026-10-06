package cli

import (
	"context"
	"flag"
	"fmt"
	"github.com/schuettc/muda/internal/notices"
	tools "github.com/schuettc/tools-common"
	"io"
	"os"
	ossignal "os/signal"
)

const noticesHelp = "Groups warning and notice annotations by normalized signature across run attempts. Every observation cites its source. Emits advance-notice (W10); signals are data, exit code 0. Unreadable sources are unavailable. Only runs created in the window [since, until) are read; --until takes the same forms as --since, defaults to now, must follow --since and may not be in the future."

func noticesFlags() (*flag.FlagSet, *Common) {
	fs := flag.NewFlagSet("notices", flag.ContinueOnError)
	c := AddCommon(fs)
	tools.SetUsage(fs, "muda notices [--repo OWNER/NAME] [--since 30d] [--until DATE] [--format json|md] [--no-cache]", noticesHelp)
	return fs, c
}
func Notices(env Env) tools.Command {
	return tools.Command{Name: "notices", Summary: "advance notices grouped with source evidence", Synopsis: "[--repo OWNER/NAME] [--since 30d] [--until DATE] [--format json|md] [--no-cache]", Help: noticesHelp, NewFlags: func() *flag.FlagSet { fs, _ := noticesFlags(); return fs }, Run: func(args []string, out, _ io.Writer) error {
		fs, common := noticesFlags()
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
		groups, sigs, err := notices.Run(ctx, c, common.Repo, common.Since, common.Until)
		if err != nil {
			return err
		}
		r := notices.NewReport(common.Repo, common.Since, common.Until, groups, sigs)
		if common.Format == "md" {
			_, err = io.WriteString(out, notices.RenderMarkdown(r))
			return err
		}
		return notices.WriteJSON(out, r)
	}}
}
