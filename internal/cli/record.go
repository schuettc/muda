package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/schuettc/muda/internal/record"
	tools "github.com/schuettc/tools-common"
)

const recordHelp = `Local, reviewed pipeline facts; no GitHub authentication or network access.
Walks up to the nearest .git directory or worktree .git file. Writes only .muda/.

Usage (flags follow the subcommand and any ID):
  muda record init [--file settings.json]
  muda record baseline [--file measure.json] [--settings settings.json]
                       [--gates gates.json] [--scan scan.json] [--ref COMMIT]
  muda record finding add [--file finding.json]
  muda record finding set F-NNN [--file patch.json]
  muda record exempt [--file exemption.json]
  muda record gates set [--file gates.json]
  muda record receipt F-NNN [--file receipt.json]
  muda record show|check [--format json|md]
All commands accept --format json|md (default json). Input is JSON from stdin
unless --file is provided. show/check accept no input. Usage errors exit 2;
runtime/validation errors and invalid check exit 1. No destructive commands.

Settings: schema=1, roles={workflow:confirmed-role}, stacks=[confirmed-stack],
window={since,until} (dates or RFC3339), muda-version; optional
record-pr={labels:[label]}, the labels every record PR carries (non-empty,
unpadded, comma-free, unique; invalid-record-pr). Older records have none.
Finding add: waste, location, recipe, evidence[] (signal.Evidence),
basis=measured|suspected, estimate, risk; pr (required once in-pr or fixed).
IDs are generated and status starts open. set accepts any of these fields plus status, exemption-id.
Transitions: open -> approved -> in-pr -> fixed|reopened; reopened -> approved;
approved -> reopened (a failed experiment);
fixed -> reopened only with reopen-reason (set takes it with that transition
alone), when the finding's PR did not merge: record cannot check the PR, and a
merged fix needs a new finding (missing-reopen-reason);
any non-fixed status -> exempt, with a matching exemption-id; exempt -> exempt
with a new exemption-id renews; exempt -> reopened only after the current
exemption's review-by has passed (UTC), clearing exemption-id.
Exemption: id (E-...), waste, location, reason, agreed-by, date, review-by
(YYYY-MM-DD; review-by not before date or today, UTC). check reports an
expired exemption only while an exempt finding references it.
Location is gate:<gate-id> or workflow:<path>/job:<job-id>, optionally
/step:<stable-name>, never a numeric step index.
Receipt: schema=1, pr (the finding's PR URL), before, after (Markdown),
run-links[] (GitHub actions run URLs), gate-status (VERIFIED|EXEMPTED|
UNVERIFIED|WEAKENED from compare --gates), gate-result (prose); finding-id
defaults to the supplied F-NNN. in-pr -> fixed requires this finding's receipt
with a VERIFIED or EXEMPTED gate-status and the finding's pr (unverified-fix).
Baseline: --file (or stdin) is the muda measure report (top-level workflows
and since). Optional --settings (as init; replaces muda.toml, the only way it
changes after init), --gates (a muda gates inventory: gates and security;
replaces gates.toml) and --scan (a muda scan report: signals and files; stored
as scan.json) are validated together with it before anything is written; if
any is invalid nothing changes (wrong-report-type names the flag). Gates and
scan must name the measure report's repo (repo-mismatch). The gates inventory
must read workflows at its default branch, and the scan must read that branch
(ref-mismatch): record muda gates and muda scan without --ref. Historical
baseline: once muda.toml's window has closed (its until is at or before the
start of the current UTC day; a date-only until is that day's 00:00Z, RFC3339
is exact), --ref COMMIT (the window's last commit, as a full 40- or 64-hex
commit SHA, e.g. from git rev-parse) accepts gates and scan read with
muda gates --ref COMMIT and muda scan --ref COMMIT; each report must name
COMMIT (ref-mismatch), and --ref needs --gates unless the recorded inventory
is already historical at COMMIT. gates.toml then records historical-ref and
settings-read (today, UTC), and compare --gates and --scan take COMMIT as the
before side. Repository settings (branch protection, rulesets, environments)
have no history: they stay current, labelled settings read DATE, not historical.
While the window is open, --ref is refused (ref-mismatch). gates set refuses a
historical gates.toml (historical-gates): re-record with baseline --gates. muda.toml's window
must match the report's since/until: a date matches that UTC date, RFC3339 is
exact (window-mismatch). Re-recording a later baseline therefore requires
--settings carrying the new window. check applies the same rules on disk.
The measure report must carry non-empty repo, since and until.
While writing, baseline leaves .muda/.baseline-set-pending with each file's
intended sha256 and removes it only when every file is written. If it stops
part-way, check reports incomplete-baseline-set naming the files not yet
applied; show is refused and so is gates set. To finish,
re-run the same record baseline command; it clears the marker on success.
A narrower run is refused: it must pass every flag the unfinished run had.
An invalid marker (not muda's own) must be removed by hand through a
reviewed record PR.
A corrupt muda.toml must be fixed by hand, through a reviewed record PR;
baseline --settings refuses to replace a muda.toml it cannot read.
Baseline and gates input must have schema=1; entire JSON snapshots, including
unknown additive policy/scan fields, are preserved. gates.toml stores the
snapshot as inventory-json. Every record file and receipt has schema=1.
Locations parse completely: step requires job; no empty, duplicate or
out-of-order components or traversal. Job/step names may contain spaces and
colons; encode a literal slash as %2F and percent as %25. Workflow paths are
relative slash-separated components (encode literal colons as %3A). Gate IDs
are opaque producer identities: colons and workflow-path slashes stay literal.
Typed inputs must be one JSON object, never null, an array or a scalar.
Overlapping mutations fail with record-busy (exit 1): .muda/.write-lock uses
exclusive creation for the full read/validate/write. Retry after the owner
finishes; stale locks require manual review and are never auto-cleared.
Do not supply secrets. init refuses an existing .muda directory, including
an incomplete record left by a failed init; inspect it with record check.`

func recordFlags() *flag.FlagSet {
	fs := flag.NewFlagSet("record", flag.ContinueOnError)
	fs.String("file", "", "read JSON from file instead of stdin (mutation commands only)")
	fs.String("format", "json", "output format: json or md")
	for _, name := range setFlags {
		fs.String(name, "", "baseline only: "+name+" JSON file recorded with the measure report")
	}
	fs.String("ref", "", "baseline only: the closed window's last commit that --gates and --scan were read at (historical baseline)")
	tools.SetUsage(fs, "muda record <init|baseline|finding add|finding set F-NNN|exempt|gates set|receipt F-NNN|show|check> [--file JSON] [--settings|--gates|--scan JSON] [--ref COMMIT] [--format json|md]", recordHelp)
	return fs
}

// setFlags are the optional inputs `record baseline` records with the measure report.
var setFlags = []string{"settings", "gates", "scan"}

// baselineSet reads the measure report and every supplied set flag's file.
func baselineSet(fs *flag.FlagSet, measure []byte) (record.BaselineSet, error) {
	in := record.BaselineSet{Measure: measure, Ref: fs.Lookup("ref").Value.String()}
	targets := map[string]*[]byte{"settings": &in.Settings, "gates": &in.Gates, "scan": &in.Scan}
	for _, name := range setFlags {
		path := fs.Lookup(name).Value.String()
		if path == "" {
			continue
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return in, fmt.Errorf("--%s: %w", name, err)
		}
		*targets[name] = b
	}
	return in, nil
}

func recordAction(args []string) (action, id string, rest []string, err error) {
	bad := func() (string, string, []string, error) {
		return "", "", nil, tools.UsageError{Msg: "expected record subcommand; see muda help record"}
	}
	if len(args) == 0 {
		return bad()
	}
	action = args[0]
	rest = args[1:]
	switch action {
	case "finding":
		if len(rest) == 0 {
			return bad()
		}
		action += " " + rest[0]
		rest = rest[1:]
		if action != "finding add" && action != "finding set" {
			return bad()
		}
	case "gates":
		if len(rest) == 0 || rest[0] != "set" {
			return bad()
		}
		action = "gates set"
		rest = rest[1:]
	case "init", "baseline", "exempt", "receipt", "show", "check":
	default:
		return bad()
	}
	if action == "finding set" || action == "receipt" {
		if len(rest) == 0 || !record.ValidFindingID(rest[0]) {
			return "", "", nil, tools.UsageError{Msg: "expected strict F-NNN finding ID"}
		}
		id = rest[0]
		rest = rest[1:]
	}
	return action, id, rest, nil
}
func inputJSON(file string) ([]byte, error) {
	if file != "" {
		return os.ReadFile(file)
	}
	return io.ReadAll(os.Stdin)
}
func decodeRecord(b []byte, v any) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || b[0] != '{' {
		return tools.UsageError{Msg: "expected one JSON object"}
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return tools.UsageError{Msg: "invalid record JSON: " + err.Error()}
	}
	var extra any
	if err := d.Decode(&extra); !errors.Is(err, io.EOF) {
		return tools.UsageError{Msg: "expected one JSON object"}
	}
	return nil
}
func recordOutput(out io.Writer, format string, data any) error {
	if format == "json" {
		e := json.NewEncoder(out)
		e.SetIndent("", "  ")
		return e.Encode(data)
	}
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "# muda record\n\n```json\n%s\n```\n", b)
	return err
}

// Record deliberately takes no GitHub Env: all operations are local.
func Record() tools.Command {
	return tools.Command{Name: "record", Summary: "reviewed local pipeline record under .muda/", Synopsis: "<subcommand> [--file JSON] [--format json|md]", Help: recordHelp, NewFlags: recordFlags, Run: func(args []string, out, _ io.Writer) error {
		if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
			return tools.ParseFlags(recordFlags(), args, out)
		}
		action, id, rest, err := recordAction(args)
		if err != nil {
			return err
		}
		fs := recordFlags()
		if err = tools.ParseFlags(fs, rest, out); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return tools.UsageError{Msg: "unexpected positional arguments"}
		}
		file := fs.Lookup("file").Value.String()
		format := fs.Lookup("format").Value.String()
		if format != "json" && format != "md" {
			return tools.UsageError{Msg: "unsupported format: " + format}
		}
		if (action == "show" || action == "check") && file != "" {
			return tools.UsageError{Msg: "--file is for mutation commands only"}
		}
		refSet := false
		fs.Visit(func(f *flag.Flag) { refSet = refSet || f.Name == "ref" })
		if action != "baseline" {
			for _, name := range append(setFlags, "ref") {
				if fs.Lookup(name).Value.String() != "" || (name == "ref" && refSet) {
					return tools.UsageError{Msg: "--" + name + " is for record baseline only"}
				}
			}
		}
		if refSet && strings.TrimSpace(fs.Lookup("ref").Value.String()) == "" {
			// An explicit empty ref would silently record a current baseline.
			return tools.UsageError{Msg: "--ref must name a commit SHA"}
		}
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		root, err := record.Discover(cwd)
		if err != nil {
			return err
		}
		s := &record.Store{Root: root}
		if action != "init" {
			s, err = record.Open(root)
			if err != nil {
				return err
			}
		}
		var b []byte
		if action != "show" && action != "check" {
			b, err = inputJSON(file)
			if err != nil {
				return err
			}
		}
		switch action {
		case "init":
			var v record.Settings
			if err = decodeRecord(b, &v); err == nil {
				err = s.Init(v)
			}
		case "baseline":
			var in record.BaselineSet
			if in, err = baselineSet(fs, b); err == nil {
				err = s.WriteBaselineSet(in)
			}
		case "gates set":
			err = s.SetGatesJSON(b)
		case "finding add":
			var v record.Finding
			if err = decodeRecord(b, &v); err == nil {
				id, err = s.AddFinding(v)
			}
		case "finding set":
			var v record.FindingPatch
			if err = decodeRecord(b, &v); err == nil {
				err = s.SetFinding(id, v)
			}
		case "exempt":
			var v record.Exemption
			if err = decodeRecord(b, &v); err == nil {
				err = s.Exempt(v)
				id = v.ID
			}
		case "receipt":
			var v record.Receipt
			if err = decodeRecord(b, &v); err == nil {
				err = s.WriteReceipt(id, v)
			}
		case "show":
			var v map[string]any
			v, err = s.Snapshot()
			if err != nil {
				return err
			}
			return recordOutput(out, format, v)
		case "check":
			problems := s.Check()
			if err = recordOutput(out, format, map[string]any{"schema": record.Schema, "problems": problems}); err != nil {
				return err
			}
			if len(problems) > 0 {
				return fmt.Errorf("record check: %d problem(s)", len(problems))
			}
			return nil
		}
		if err != nil {
			return err
		}
		data := map[string]any{"schema": record.Schema, "action": strings.ReplaceAll(action, " ", "-")}
		if id != "" {
			data["id"] = id
		}
		return recordOutput(out, format, data)
	}}
}
