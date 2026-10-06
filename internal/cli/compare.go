package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/schuettc/muda/internal/compare"
	"github.com/schuettc/muda/internal/record"
	tools "github.com/schuettc/tools-common"
)

const compareHelp = `Compares completed final-attempt workflow and step samples with evidence.
Both phases are required: --before/--after YYYY-MM-DD..YYYY-MM-DD (or RFC3339)
or --before-runs/--after-runs comma-separated positive IDs, exclusive per phase.
Windows are [since, until); run IDs are fetched directly regardless of age.
--gates verifies the recorded .muda/gates.toml without updating it. Missing proof
is UNVERIFIED. Unexempted removed, weakened or unverified policy exits 1 after
printing the report. --gates-file selects another schema-1 recorded TOML inventory
and enables verification. Only a canonical .muda/gates.toml path uses
that record's confined exemptions, including when selected via --gates-file;
it never borrows exemptions from a different or walk-up record.
Arbitrary overrides disable exemptions, even if copied from a standard record.
A workflow file whose recorded full SHA-256 is equal before and after, and
parsed on both sides, is byte-identical and VERIFIED. Acceptance limitation:
VERIFIED covers .github/workflows files only. What a workflow runs but does
not contain is not compared: local composite actions (uses: ./…),
scripts a step runs, and remote actions on moving refs can change while the
workflow file stays byte-identical. A changed, added or removed workflow file, or one
without a full recorded hash on both sides, is UNVERIFIED because the
producer lacks full execution-policy attestation. workflow_diffs explains
each: the parsed job, step and key changes (old → new), or "raw file changed"
when no parsed key changed or a side was not parsed. Equal hashes with a side
not parsed are same-hash, not a change, and stay UNVERIFIED.
Exact active exemptions represent explicit acceptance, not proof. A workflow
proof ID (policy:workflow:PATH@DIGEST) carries the full SHA-256 (64 hex
digits) of that workflow's raw file bytes, so an exemption covers the file
exactly as it is; any later edit, even a comment or trigger, needs a new
exemption. An older record's exemption with a 12-hex DIGEST prefix still
covers the file whose SHA-256 starts with it; record exempt writes the full one. A workflow deleted or renamed away binds its before-side raw
file the same way, so its exemption covers exactly the file removed.
An unparseable workflow's ID (policy:availability:PATH@DIGEST)
binds its raw file the same way; a workflow file that could not be read, or
an unreadable .github/workflows directory, has no digest and cannot be
exempted. A recorded inventory without raw file hashes is UNVERIFIED
(policy:inventory:raw).
--scan compares the recorded .muda/scan.json with a fresh muda scan and lists
signals removed, added and unchanged. Signals match by ID, summary (the
action ref, job, command or value it names) and each evidence file and step,
never by line number; lines are still shown. no-path-filter matches by ID and
file only, since its summary carries a rolling p50. A record without
scan.json, an unfinished record baseline run, or a fresh scan missing the
evidence behind a removed signal makes the scan comparison unavailable with a
reason, never "no changes". Signal changes never change the exit code.
--ref REF reads workflows at REF for --gates and --scan (the after side;
repository settings still come from the default branch); the default is the
default branch. It requires --gates, --gates-file or --scan. Timing phases
are unaffected.
A historical baseline (record baseline --ref) is compared from its recorded
commit: before_ref names it for --gates and --scan, and a recorded inventory or
scan read at another ref is unavailable. before_settings labels the recorded
repository settings as current when read, not historical.
--since is ignored by the timing phases (they set their own windows); it is
still validated, and with --scan it bounds the fresh scan's run history, as
for muda scan. --until is likewise ignored by the timing phases and validated
(it must follow --since and may not be in the future); with --scan the fresh
scan reads run history in [since, until), so a past window can be diffed.`

type compareOptions struct {
	common                                               *Common
	before, after, beforeRuns, afterRuns, gatesFile, ref string
	gates, scan                                          bool
}

func compareFlags() (*flag.FlagSet, *compareOptions) {
	fs := flag.NewFlagSet("compare", flag.ContinueOnError)
	o := &compareOptions{common: AddCommon(fs)}
	fs.StringVar(&o.before, "before", "", "before [since,until) date or RFC3339 range")
	fs.StringVar(&o.after, "after", "", "after [since,until) date or RFC3339 range")
	fs.StringVar(&o.beforeRuns, "before-runs", "", "before positive run IDs (CSV)")
	fs.StringVar(&o.afterRuns, "after-runs", "", "after positive run IDs (CSV)")
	fs.BoolVar(&o.gates, "gates", false, "verify recorded .muda/gates.toml")
	fs.StringVar(&o.gatesFile, "gates-file", "", "verify a schema-1 recorded TOML gates inventory (enables --gates)")
	fs.BoolVar(&o.scan, "scan", false, "compare recorded .muda/scan.json with a fresh scan")
	fs.StringVar(&o.ref, "ref", "", "read workflows at this branch, tag or SHA for --gates and --scan (default: the default branch)")
	tools.SetUsage(fs, "muda compare --before WINDOW --after WINDOW [--gates] [--gates-file F] [--scan] [--ref REF] [--repo OWNER/NAME] [--format json|md]", compareHelp)
	return fs, o
}
func parsePhase(window, ids string) (compare.Window, error) {
	w := compare.Window{}
	if window != "" && ids != "" {
		return w, fmt.Errorf("window and run IDs are mutually exclusive")
	}
	if ids != "" {
		for _, s := range strings.Split(ids, ",") {
			id, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
			if err != nil {
				return w, fmt.Errorf("invalid positive run ID %q", s)
			}
			w.RunIDs = append(w.RunIDs, id)
		}
	} else {
		a, b, ok := strings.Cut(window, "..")
		if !ok {
			return w, fmt.Errorf("required window: YYYY-MM-DD..YYYY-MM-DD or RFC3339 range")
		}
		parse := func(s string) (time.Time, error) {
			t, err := time.Parse("2006-01-02", s)
			if err != nil {
				return time.Parse(time.RFC3339, s)
			}
			return t, nil
		}
		var err error
		w.Since, err = parse(a)
		if err != nil {
			return w, err
		}
		w.Until, err = parse(b)
		if err != nil {
			return w, err
		}
	}
	return w, w.Validate()
}
func Compare(env Env) tools.Command {
	return tools.Command{Name: "compare", Summary: "before/after timings and fail-closed gate proof", Synopsis: "--before WINDOW --after WINDOW [--gates] [--gates-file F] [--scan] [--ref REF]", Help: compareHelp, NewFlags: func() *flag.FlagSet { fs, _ := compareFlags(); return fs }, Run: func(args []string, out, _ io.Writer) error {
		fs, o := compareFlags()
		if err := tools.ParseFlags(fs, args, out); err != nil {
			return err
		}
		seen := map[string]bool{}
		fs.Visit(func(f *flag.Flag) { seen[f.Name] = true })
		if (seen["before"] && seen["before-runs"]) || (seen["after"] && seen["after-runs"]) {
			return tools.UsageError{Msg: "window and run ID flags are mutually exclusive per phase"}
		}
		if fs.NArg() != 0 {
			return tools.UsageError{Msg: "compare takes no positional arguments"}
		}
		before, err := parsePhase(o.before, o.beforeRuns)
		if err != nil {
			return tools.UsageError{Msg: "before: " + err.Error()}
		}
		after, err := parsePhase(o.after, o.afterRuns)
		if err != nil {
			return tools.UsageError{Msg: "after: " + err.Error()}
		}
		if seen["ref"] {
			if strings.TrimSpace(o.ref) == "" {
				// An explicit empty ref would silently mean the default branch.
				return tools.UsageError{Msg: "--ref must name a branch, tag or SHA"}
			}
			if !o.gates && o.gatesFile == "" && !o.scan {
				return tools.UsageError{Msg: "--ref applies only to --gates, --gates-file or --scan"}
			}
		}
		if err = o.common.Resolve(env.GitRemote, env.Now); err != nil {
			return tools.UsageError{Msg: err.Error()}
		}
		var proofError, recordError error
		path := o.gatesFile
		root := ""
		if (o.gates && path == "") || o.scan {
			cwd, e := os.Getwd()
			if e != nil {
				return e
			}
			root, recordError = record.Discover(cwd)
			if recordError != nil {
				root = ""
			}
			if o.gates && path == "" {
				if recordError != nil {
					proofError = recordError
				} else {
					path = filepath.Join(root, ".muda", "gates.toml")
				}
			}
		}
		c, err := env.client(o.common.NoCache)
		if err != nil {
			return err
		}
		opts := compare.Options{Gates: o.gates || o.gatesFile != "", GatesFile: path, Scan: o.scan, Ref: o.ref, Record: root, Since: o.common.Since, Until: o.common.Until}
		r, err := compare.Run(context.Background(), c, o.common.Repo, before, after, opts)
		if err != nil {
			return err
		}
		if proofError != nil {
			r.Gates = compare.UnavailableGates(proofError.Error())
		}
		if o.scan && recordError != nil {
			r.Scan = compare.UnavailableScan(recordError.Error())
		}
		if o.common.Format == "md" {
			_, err = io.WriteString(out, compare.RenderMarkdown(r))
		} else {
			err = compare.WriteJSON(out, r)
		}
		if err != nil {
			return err
		}
		// Only an unpassed gate check fails; scan signal changes are data.
		if r.Gates != nil && !r.Gates.Passed() {
			return fmt.Errorf("gate verification failed: %s (see report)", r.Gates.Status)
		}
		return nil
	}}
}
