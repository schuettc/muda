package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/schuettc/muda"
	"github.com/schuettc/muda/internal/cli"
	"github.com/schuettc/muda/internal/content"
	"github.com/schuettc/muda/internal/skills"
	tools "github.com/schuettc/tools-common"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

func TestPluginVersionMatchesVERSION(t *testing.T) {
	b, e := os.ReadFile("../../VERSION")
	if e != nil {
		t.Fatal(e)
	}
	var p struct{ Name, Version, License, Repository string }
	b2, e := os.ReadFile("../../.claude-plugin/plugin.json")
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(b2, &p); e != nil {
		t.Fatal(e)
	}
	if p.Name != "muda" || p.Version != strings.TrimSpace(string(b)) || p.License != "MIT" || p.Repository != "https://github.com/schuettc/muda" {
		t.Fatal(p)
	}
}
func TestEntrySkillContract(t *testing.T) {
	b, e := muda.Content.ReadFile("skills/muda/SKILL.md")
	if e != nil {
		t.Fatal(e)
	}
	text := string(b)
	for _, s := range []string{"Use when", "muda-owned: muda", ">=0.1.0", "muda commands --json", "curl -fsSL https://muda.tools/install.sh | sh", "muda update", "STOP", "muda record check", "muda record show", "muda record init --file muda-settings.json", "approved", "in-pr", "fixed", "reopened", "exempt", "review-by", "history", "muda-measure", "muda-diagnose", "muda-fix", "muda-verify",
		"muda gates --format md", "unavailable", "dev",
		"muda record init --file muda-settings.json muda record check", "Delete muda-settings.json only after `muda record init` and `muda record check` both succeed", "keep it and **STOP**", "unversioned build", "first version token", "logs",
		"| `reopened` finding | Load muda-diagnose", "consent carries over",
		"declined renewal", "load muda-diagnose to renew or reopen",
		"Raise every exemption an exempt finding still relies on whose `review-by` is past today (UTC)",
		"check for an open PR for it", "never pushed directly to the default branch",
		"`gh pr list --json headRefName`", "`muda/record` prefix", "give the Your move merge ask: never route on unmerged state",
		"receipt shows `UNVERIFIED` or `WEAKENED`", "load muda-diagnose for an exact `gate:` exemption, then muda-verify",
		"report the program complete, citing each finding's receipt",
		// Task 6: re-baseline cycles, exact-file exemptions, unfinished baseline sets.
		"then load muda-measure for the next cycle's baseline", "the exact file", "re-attested once",
		// Fix rounds 1-2 (I-1, N-1, N-2): a recorded but undiagnosed next-cycle
		// baseline routes to diagnose, decided only from readable dates.
		"the recorded baseline window's `until` is later than every fixed finding's PR merge time (`gh pr view <pr> --json mergedAt`",
		"not yet diagnosed: load muda-diagnose", "If a merge time can't be read, **STOP** and ask",
		// Final review I-2: full-precision merge times; a same-day exemption
		// routes to diagnose; an explicit re-measure request routes to measure.
		"compared as full timestamps", "no exemption `date` is after `until`'s UTC date (the same day counts as not later)",
		"If the human explicitly asks to re-measure, load muda-measure",
		// Final review m-5: a pre-hash gates record is re-recorded, not exempted.
		"`policy:inventory:raw`", "re-recorded with `record baseline … --gates`, never exempted",
		"`incomplete-baseline-set`", "re-run the exact same `record baseline` command, or do what the reported problem says"} {
		if !strings.Contains(norm(text), norm(s)) {
			t.Error("missing", s)
		}
	}
	for _, s := range []string{"waits on a settings/rebaseline command", "written once", "receipt or exemption date", "(its whole UTC day)"} {
		if strings.Contains(norm(text), s) {
			t.Error("stale", s)
		}
	}
	// Release prep P6 raised 1050 to 1052 for the unmerged-fixed reopen offer.
	// The guided-message and merging rules (feat/guided) raised 1052 to 1171.
	// The measure-to-diagnose record-branch handoff raised 1171 to 1200; naming the
	// "no PR in any state" check for a stale merged branch raised it to 1225.
	if n := len(strings.Fields(text)); n > 1225 {
		t.Errorf("muda is %d words; keep it at most 1225", n)
	}
	if strings.Contains(norm(text), "new baseline through record") || strings.Contains(text, "--search \"muda/record\"") {
		t.Error("stale stopping-rule or record-PR search text")
	}
	if strings.Contains(norm(text), "Raise every exemption whose") {
		t.Error("history exemptions must not be raised")
	}
	if strings.Contains(text, "muda measure --since 30d --no-cache") {
		t.Error("preflight must not list every run as a token probe")
	}
	check, e := cli.CommandChecker(NewApp())
	if e != nil {
		t.Fatal(e)
	}
	// All five skills are shipped: full-set lint against the real CLI is clean.
	var got []string
	for _, p := range skills.Lint(muda.Content, check) {
		got = append(got, p.Path+": "+p.Message)
	}
	if len(got) != 0 {
		t.Errorf("lint = %q, want none", got)
	}
	for _, n := range []string{"muda", "muda-measure", "muda-diagnose", "muda-fix", "muda-verify"} {
		if _, e := muda.Content.ReadFile("skills/" + n + "/SKILL.md"); e != nil {
			t.Error(e)
		}
	}
	for _, args := range [][]string{{"skills", "install", "--agent", "pi", "--scope", "project"}, {"skills", "install", "--agent", "other"}, {"skills", "invented"}, {"skills", "install", "extra"}} {
		want := args[1] == "install" && len(args) == 6
		if check(args) != want {
			t.Errorf("checker %v", args)
		}
	}
}

// T5.3: a fixed finding whose PR has a null mergedAt is an unmerged, pending
// fix, not a merge time that can't be read. It blocks a new cycle, even under
// the explicit re-measure rule; an unreadable merge time still STOPs.
func TestEntryUnmergedFixedPR(t *testing.T) {
	b, e := muda.Content.ReadFile("skills/muda/SKILL.md")
	if e != nil {
		t.Fatal(e)
	}
	text := norm(string(b))
	for _, s := range []string{
		"A fixed finding's PR with null `mergedAt` is unmerged: report its fix as pending. It blocks any new cycle, even an explicit re-measure (STOP and explain why).",
		"If that PR is open, load muda-verify to report its real pipeline, recording nothing; if closed, **STOP** and ask.",
		"If a merge time can't be read, **STOP** and ask",
		"If the human explicitly asks to re-measure, load muda-measure",
	} {
		if !strings.Contains(text, norm(s)) {
			t.Error("missing", s)
		}
	}
	// The pending rule must come before the re-measure rule it overrides.
	if i, j := strings.Index(text, "null `mergedAt` is unmerged"), strings.Index(text, "explicitly asks to re-measure"); i < 0 || j < 0 || i > j {
		t.Error("the null mergedAt rule must precede the explicit re-measure rule")
	}
}

func TestSkillsActualApp(t *testing.T) {
	root := t.TempDir()
	if e := os.Mkdir(filepath.Join(root, ".git"), 0700); e != nil {
		t.Fatal(e)
	}
	sub := filepath.Join(root, "sub")
	if e := os.Mkdir(sub, 0700); e != nil {
		t.Fatal(e)
	}
	t.Chdir(sub)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", "")
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	var out, err bytes.Buffer
	app := NewApp()
	if c := app.Dispatch([]string{"skills", "install", "--agent", "pi", "--scope", "project"}, &out, &err); c != 0 {
		t.Fatalf("%d %s", c, err.String())
	}
	if _, e := os.Stat(filepath.Join(root, ".pi/skills/muda/SKILL.md")); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(filepath.Join(sub, ".pi")); !os.IsNotExist(e) {
		t.Fatal("ambient cwd used")
	}
	for _, a := range [][]string{{"skills", "install", "--agent", "invalid"}, {"skills", "install", "--scope", "invalid"}, {"skills", "install", "extra"}} {
		if c := app.Dispatch(a, &out, &err); c != 2 {
			t.Fatalf("%v code=%d", a, c)
		}
	}
}

func TestFullSkillFixtureUsesRealChecker(t *testing.T) {
	known, e := cli.CommandChecker(newApp(cli.Env{Getenv: func(string) string { t.Fatal("auth/environment called"); return "" }}))
	if e != nil {
		t.Fatal(e)
	}
	b, e := muda.Content.ReadFile("skills/muda/SKILL.md")
	if e != nil {
		t.Fatal(e)
	}
	f := fstest.MapFS{}
	for _, n := range []string{"muda", "muda-measure", "muda-diagnose", "muda-fix", "muda-verify"} {
		text := strings.Replace(string(b), "name: muda\n", "name: "+n+"\n", 1)
		text = strings.Replace(text, "muda-owned: muda\n", "muda-owned: "+n+"\n", 1)
		f["skills/"+n+"/SKILL.md"] = &fstest.MapFile{Data: []byte(text)}
	}
	if ps := skills.Lint(f, known); len(ps) != 0 {
		t.Fatal(ps)
	}
	f["skills/muda-fix/SKILL.md"].Data = append(f["skills/muda-fix/SKILL.md"].Data, []byte("```sh\nmuda skills install --agent bogus\n```\n")...)
	if len(skills.Lint(f, known)) == 0 {
		t.Fatal("real argument checker bypassed")
	}
}

// The plugin and package routes ship the skills/ directory. pi has no manifest
// here: with no package.json it discovers the conventional skills/ directory,
// so a package.json appearing is a deliberate change to that route.
func TestPackageManifests(t *testing.T) {
	var p struct{ Skills string }
	b, e := os.ReadFile("../../.claude-plugin/plugin.json")
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(b, &p); e != nil {
		t.Fatal(e)
	}
	if p.Skills != "./skills/" {
		t.Fatal(p)
	}
	if _, e := os.Stat("../../package.json"); !errors.Is(e, fs.ErrNotExist) {
		t.Fatal("package.json would replace pi's conventional skills/ discovery:", e)
	}
	for _, n := range []string{"muda", "muda-measure", "muda-diagnose", "muda-fix", "muda-verify"} {
		if _, e := os.Stat("../../skills/" + n + "/SKILL.md"); e != nil {
			t.Fatal(e)
		}
	}
	var m struct {
		Plugins []struct{ Name, Source string }
	}
	b, e = os.ReadFile("../../.claude-plugin/marketplace.json")
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(b, &m); e != nil {
		t.Fatal(e)
	}
	if len(m.Plugins) != 1 || m.Plugins[0].Name != "muda" || m.Plugins[0].Source != "./" {
		t.Fatal(m)
	}
}

func TestCommandCheckerHasNoDefaultTrue(t *testing.T) {
	app := NewApp()
	app.Register(tools.Command{Name: "future", Run: func([]string, io.Writer, io.Writer) error { t.Fatal("dispatched"); return nil }})
	known, e := cli.CommandChecker(app)
	if e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		a    []string
		want bool
	}{
		{[]string{"version"}, true}, {[]string{"version", "extra"}, false},
		{[]string{"update"}, true}, {[]string{"update", "extra"}, false},
		{[]string{"commands", "--json"}, true}, {[]string{"commands", "extra"}, false},
		{[]string{"man"}, true}, {[]string{"man", "extra"}, false},
		{[]string{"help"}, true}, {[]string{"help", "record"}, true},
		{[]string{"help", "invented"}, false}, {[]string{"help", "record", "extra"}, false},
		{[]string{"future"}, false},
	} {
		if got := known(tc.a); got != tc.want {
			t.Errorf("%v: %v", tc.a, got)
		}
	}
}

func preflight(t *testing.T, text string) string {
	t.Helper()
	i, j := strings.Index(text, "## Preflight\n"), strings.Index(text, "## Rules\n")
	if i < 0 || j < i {
		t.Fatal("no preflight section")
	}
	return text[i:j]
}

// T6 final review C1: in a replay of a closed window, the preflight's gates
// probe reads the window's last commit, never today's workflows. The clause
// is part of the byte-identical preflight, so every skill carries it.
func TestPreflightReplayClause(t *testing.T) {
	const clause = "Replaying a closed window, add `--ref` with its last commit's full SHA."
	for _, n := range []string{"muda", "muda-measure", "muda-diagnose", "muda-fix", "muda-verify"} {
		b, e := muda.Content.ReadFile("skills/" + n + "/SKILL.md")
		if e != nil {
			t.Fatal(e)
		}
		p := norm(preflight(t, string(b)))
		i, j := strings.Index(p, "Run `muda gates --format md`"), strings.Index(p, norm(clause))
		if j < 0 {
			t.Errorf("%s: preflight missing %q", n, clause)
			continue
		}
		// The words cut to make room keep their rules.
		for _, s := range []string{"tell the user a `dev` binary is an unversioned build", "Run `muda commands --json`", "with a small, bounded read", "Never print credentials.", "note them for muda-measure"} {
			if !strings.Contains(p, norm(s)) {
				t.Errorf("%s: preflight missing %q", n, s)
			}
		}
		// The clause qualifies the gates probe, so it follows it.
		if i < 0 || j < i {
			t.Errorf("%s: the replay clause must follow the gates probe", n)
		}
	}
}
func TestMeasureSkillContract(t *testing.T) {
	entry, e := muda.Content.ReadFile("skills/muda/SKILL.md")
	if e != nil {
		t.Fatal(e)
	}
	b, e := muda.Content.ReadFile("skills/muda-measure/SKILL.md")
	if e != nil {
		t.Fatal(e)
	}
	text := string(b)
	if preflight(t, text) != preflight(t, string(entry)) {
		t.Error("preflight must be the entry skill's text verbatim")
	}
	for _, s := range []string{"name: muda-measure", "muda-owned: muda-measure", "description: Use when", "muda-version: \">=0.1.0\"",
		"muda measure --since 30d > muda-measure.json", "muda scan --since 30d", "muda notices --since 30d", "muda gates > muda-gates.json",
		"1000", "narrow", "all three", "same `--since`", "unavailable", "representative", "**CHECKPOINT:**", "attributed", "roles", "stacks",
		"muda record init --file muda-settings.json", "consent carries over",
		"muda record baseline --file muda-measure.json",
		"muda record show --format json", "keyed by workflow `path`",
		"exemption", "required-check", "attributed to them",
		"absent", "keep the scratch files",
		"`git switch -c muda/record-baseline-YYYY-MM-DD-HHMM`", "commit only `.muda/`", "record-only PR", "`gh pr create`",
		"first run `muda record show --format json`; if it fails, **STOP** and report; do not measure.", "only after `muda record check` passes",
		"Delete the scratch", "never commit", "muda-diagnose",
		// Task 6: one baseline set per cycle, with the scan stored.
		"muda record baseline --file muda-measure.json --settings muda-settings.json --gates muda-gates.json --scan muda-scan.json",
		"muda scan --since 30d > muda-scan.json", "muda-settings.json, muda-gates.json, muda-scan.json and muda-measure.json",
		"always pass `--settings`, `--gates` and `--scan` together",
		"## Next cycle", "when muda-verify reports every finding `fixed` or validly `exempt`: a new window, the same checkpoint and record command",
		"confirm the corrected settings and re-record",
		"`incomplete-baseline-set`", "re-run the exact same `record baseline` command, or do what the reported problem says", "the exact file",
		// Final review m-6: no mid-cycle re-baseline under an in-flight fix.
		"if any finding is `in-pr`, or `approved` with an open PR, **STOP** and ask", "would move its verify window",
		// Stage 1 follow-up T2: a closed window's replay reads gates and scan
		// at its last commit and labels the settings as current.
		"**Replaying a closed window:**", "the window's last commit", "`muda gates --ref COMMIT`", "`muda scan --ref COMMIT`",
		"add `--ref COMMIT` to `record baseline`", "label them current, not historical",
		// Fix round 1, M5: the replay bounds the window's end and names the full SHA.
		"measure, scan and notices take `--until` (the window's end)", "`--ref` takes the full SHA",
		// T6 final review M2: a replay's --since is absolute.
		"`--since` is the window's absolute start, never a relative `30d`"} {
		if !strings.Contains(norm(text), norm(s)) {
			t.Error("missing", s)
		}
	}
	// P3 fix round 1, C1: the allowed stacks are exactly the shipped catalog,
	// so a stack added to recipes/stacks.txt can be confirmed and recorded.
	stacks, err := content.Stacks(muda.Content)
	if err != nil {
		t.Fatal(err)
	}
	quoted := make([]string, len(stacks))
	for i, s := range stacks {
		quoted[i] = "`" + s + "`"
	}
	if allowed := "**Stacks:** only from " + strings.Join(quoted, ", ") + ";"; !strings.Contains(norm(text), norm(allowed)) {
		t.Errorf("measure skill's stack list differs from the catalog; want %q", allowed)
	}
	// T6 final review M1: the replay rule precedes the commands it changes.
	if i, j := strings.Index(text, "**Replaying a closed window:**"), strings.Index(text, "muda measure --since 30d > muda-measure.json"); i < 0 || j < 0 || i > j {
		t.Error("the replay rule must come before the command block")
	}
	// The guided-message and merging rules (feat/guided) raised 1000 to 1117.
	// The record-branch handoff to muda-diagnose raised 1117 to 1146; the PR
	// body scratch-file rule to 1160.
	if n := len(strings.Fields(text)); n > 1160 {
		t.Errorf("muda-measure is %d words; keep it at most 1160", n)
	}
	for _, s := range []string{"provisional", " settings.json", "`settings.json`", " gates.json", " measure.json", " scan.json",
		"write-once", "no settings-update command", "unrecordable", "muda record gates set", "carry over", "relative `--since`"} {
		if strings.Contains(strings.ToLower(norm(text)), s) {
			t.Error("forbidden", s)
		}
	}
}

// The lint's explicit JSON-redirect list must match the real CLI: commands
// with a --format flag defaulting to json, with record narrowed to show.
func TestJSONRedirectCommandsMatchCLI(t *testing.T) {
	var out, errs bytes.Buffer
	if NewApp().Dispatch([]string{"commands", "--json"}, &out, &errs) != 0 {
		t.Fatal(errs.String())
	}
	var cmds []struct {
		Name  string
		Flags []struct{ Name, Default string }
	}
	if e := json.Unmarshal(out.Bytes(), &cmds); e != nil {
		t.Fatal(e)
	}
	derived := 0
	for _, c := range cmds {
		for _, f := range c.Flags {
			if f.Name == "format" && f.Default == "json" {
				derived++
			}
		}
	}
	if len(cmds) == 0 || derived == 0 {
		t.Fatalf("no commands (%d) or no JSON-default commands (%d) decoded from metadata", len(cmds), derived)
	}
	for _, c := range cmds {
		jsonDefault := false
		for _, f := range c.Flags {
			if f.Name == "format" && f.Default == "json" {
				jsonDefault = true
			}
		}
		args := []string{c.Name}
		if c.Name == "record" {
			if !skills.JSONOutputCommand([]string{"record", "show"}) || skills.JSONOutputCommand([]string{"record", "check"}) || skills.JSONOutputCommand([]string{"record"}) {
				t.Error("record: only show may be redirected")
			}
			continue
		}
		if got := skills.JSONOutputCommand(args); got != jsonDefault {
			t.Errorf("%s: redirect allowed=%v, CLI json default=%v", c.Name, got, jsonDefault)
		}
	}
}

func TestDiagnoseSkillContract(t *testing.T) {
	entry, e := muda.Content.ReadFile("skills/muda/SKILL.md")
	if e != nil {
		t.Fatal(e)
	}
	b, e := muda.Content.ReadFile("skills/muda-diagnose/SKILL.md")
	if e != nil {
		t.Fatal(e)
	}
	text := string(b)
	if preflight(t, text) != preflight(t, string(entry)) {
		t.Error("preflight must be the entry skill's text verbatim")
	}
	for _, s := range []string{"name: muda-diagnose", "muda-owned: muda-diagnose", "description: Use when", "muda-version: \">=0.1.0\"",
		"muda record show --format json", "review-by", "expired",
		"muda recipes --signal", "--stack", "muda recipe R", "muda logs ", "--job", "--step", "--grep", "muda scan",
		"`measured`", "`suspected`", "attributed", "does not prove",
		"saving", "risk", "effort", "basis",
		"**CHECKPOINT:**", "order", "reason",
		"muda record finding add --file muda-finding.json",
		"muda record exempt --file muda-exemption.json", "muda record check",
		"\"status\": \"approved\"", "\"exemption-id\"", "agreed-by", "absent", "keep it", "only after `muda record check` passes",
		"`gate:", "`workflow:", "/job:", "/step:", "muda-fix", "one approved finding",
		// Fix round 4: existing findings, exemption flow and minors.
		"## Existing findings", "`open` and `reopened` findings", "refreshed",
		"open/reopened → approved, or → exempt; in-pr and fixed belong to muda-fix and muda-verify",
		"superseded", "history", "{\"status\": \"reopened\"}", "only after its `review-by` has passed",
		"`review-by` (on or after today, UTC)", "`date` (the agreement date)",
		"Before any write, run `muda record check`", "pre-existing problems", "do not write on top of a red record",
		"run/job/step/file/log/annotation", "`file` for code or IaC",
		"`estimate` and `risk` are strings", "`run_id` and `job_id` are integers",
		"`workflow:PATH[/job:JOB[/step:STEP]]`", "%2F", "%25",
		"muda-patch.json", "muda record finding set F-001 --file muda-patch.json", "confirm the status with `muda record show --format json`",
		"never file it under the nearest recipe", "Detect section does not confirm",
		"`--since`",
		// Phase D cleanup (q5, q6).
		"only a pre-existing `expired-exemption` may remain", "whether the renew or decline decision is made now or deferred",
		"re-run the `logs` or `measure` read for its cited run, job and step", "patch the refreshed `evidence`",
		"never gets a second `finding add`",
		"`git switch -c muda/record-findings-YYYY-MM-DD-HHMM`", "Report what was recorded and what remains", "commit only `.muda/`", "record-only PR", "`gh pr create`",
		"starts only once that record PR is merged",
		"gate exemption", "`gate:ID`", "the compare report's `unverified`, `removed` or `loosened` IDs", "not the finding's location", "no `finding set`",
		// Final review I1, Task 6: workflow proof IDs bind the raw file.
		"covers the exact file as it is now; any later edit needs a new exemption", "re-attested once",
		// Task 6: stored scan, carried-over findings, named-test exemptions.
		"static signals from `.muda/scan.json`", "re-run `muda scan --since 28d --format md`", "only to refresh them",
		"re-check each carried-over `open`, `approved` or `reopened` finding's evidence against the new baseline",
		"A stored signal gone from a fresh scan is not a fix: show both", "with no `scan`, use a fresh one and say so",
		"as `Nd`, N the whole days in the baseline window",
		"the named test that catches that class", "reverting that bug's fix", "turn the proposed faster gate red",
		"not granted or renewed", "record both in `reason`",
		"`incomplete-baseline-set`", "re-run the exact same `record baseline` command, or do what the reported problem says",
		// Final review I-1: a deleted or renamed workflow binds its before hash.
		"A removed workflow is exempted by the exact file removed",
		// Final review I-2: a cycle with no agreed findings ends here.
		"If no findings are agreed", "report this cycle complete, citing the baseline", "the human decides when to measure again", "Do not load muda-measure",
		// Final review m-5.
		"`policy:inventory:raw`", "re-recorded with `record baseline … --gates`, never exempted",
		// Validation stage 1: every matched recipe's Detect runs; one sink can
		// carry several wastes.
		"read every recipe it lists", "run each one's Detect section or note why it does not apply",
		"One sink can carry several wastes",
		// Stage 1 follow-up T1: the checkpoint reports every listed recipe's
		// outcome, so a second waste on an explained sink is never dropped.
		"For each sink or signal", "the report lists every recipe step 1 listed", "one outcome each",
		"`measured`, `suspected` or `not shown`, with the evidence read", "or `does not apply`, with the reason",
		"An empty outcome is not allowed", "even once another recipe explains the sink",
		// T1 fix round 1: `not shown` needs a Detect that ran (I-2); the record
		// PR still gates muda-fix (I-1); the scratch sequence runs the check (M-1).
		"`not shown` when its Detect ran and the evidence does not show the waste", "A Detect not run is never `not shown`",
		"The next phase starts only once that record PR is merged; then load muda-fix",
		"run its command and `muda record check`; delete it",
		// T6 final review C2: a historical record's reads stay at its ref and
		// inside its window, including every recipe Detect.
		"If `muda record show` reports `historical`, every `muda gates` and `muda scan` (this refresh, any recipe Detect) adds `--ref` with `historical.ref`",
		"scan, measure and notices add `--since` and `--until` from `settings.window`, not `Nd`",
		// T6 fix ruling: the checkpoint only presents expired exemptions; this
		// rule makes the agent look for them.
		"Raise every exemption an exempt finding relies on whose `review-by` is past today (UTC)."} {
		if !strings.Contains(norm(text), norm(s)) {
			t.Error("missing", s)
		}
	}
	// The refresh-diff rule applies to the bounded fresh scan, so it follows
	// the historical rule.
	if i, j := strings.Index(norm(text), "reports `historical`"), strings.Index(norm(text), "A stored signal gone from a fresh scan"); i < 0 || j < 0 || i > j {
		t.Error("the historical rule must precede the refresh-diff rule")
	}
	if strings.Contains(norm(text), "read one with") {
		t.Error("forbidden: read one with")
	}
	// T1 fix round 1 raised 1250 to 1275 for the per-recipe outcome rule and
	// the `not shown` definition; no further cut was possible without
	// dropping a pinned rule. The T6 fix ruling raised 1275 to 1290 to restore
	// the "Raise every exemption" rule beside the historical-record rule.
	// Release prep P2 raised 1290 to 1294 for the experiment rule; P4 to
	// 1298 for the record-PR rule pointer; P6 to 1305 for the unmerged
	// fixed -> reopened transition; the P2/P4/P6 fix round to 1308 for the
	// mergedAt check, paid for by the 2 words verify's guard gave back.
	// The guided-message and merging rules (feat/guided) raised 1308 to 1427.
	// One record PR for the baseline and findings raised 1427 to 1464; the PR
	// body scratch-file rule to 1475.
	if n := len(strings.Fields(text)); n > 1475 {
		t.Errorf("muda-diagnose is %d words; keep it at most 1475", n)
	}
	for _, s := range []string{"provisional", "<<", "under renewal", "gets never"} {
		if strings.Contains(strings.ToLower(norm(text)), s) {
			t.Error("forbidden", s)
		}
	}
}

// norm collapses whitespace so phrase assertions survive Markdown reflow.
func norm(s string) string { return strings.Join(strings.Fields(s), " ") }

func TestFixSkillContract(t *testing.T) {
	entry, e := muda.Content.ReadFile("skills/muda/SKILL.md")
	if e != nil {
		t.Fatal(e)
	}
	b, e := muda.Content.ReadFile("skills/muda-fix/SKILL.md")
	if e != nil {
		t.Fatal(e)
	}
	text := string(b)
	if preflight(t, text) != preflight(t, string(entry)) {
		t.Error("preflight must be the entry skill's text verbatim")
	}
	for _, s := range []string{"name: muda-fix", "muda-owned: muda-fix", "description: Use when", "muda-version: \">=0.1.0\"",
		"muda record show --format json", "muda record check", "anything but `expired-exemption`",
		"exactly one `approved` finding", "the human picks", "not `approved`",
		"muda recipe R", "--stack", "Fix, Stacks, Traps and Verify",
		"files to touch", "expected effect", "evidence", "risk", "muda gates --format md", "`needs:`",
		"what replaces the guarantee", "out of scope", "**CHECKPOINT:**",
		"One finding, one change, one PR", "two branches and two PRs", "one after the other",
		"\"approve both in one PR\"", "faster together",
		"`git switch -c", "`gh pr create`", "finding id", "recipe id", "gate inventory before", "Never add scratch files to the branch",
		"muda-patch.json", "absent", "{\"status\": \"in-pr\", \"pr\":", "muda record finding set F-001 --file muda-patch.json",
		"keep muda-patch.json", "only after `muda record check` passes", "muda-verify",
		"`git status`", "no uncommitted `.muda/` changes", "`approved` on the up-to-date base",
		"`gh pr list --state open --search \"F-001\"`", "a closed PR or an old branch does not count", "route to muda-verify, not a second fix",
		"`git switch -c muda/F-001-YYYY-MM-DD-HHMM`",
		"on the fix branch", "commit only `.muda/findings.toml`", "record: F-001 in-pr", "push it to the same PR",
		"only the planned edit, plus that one record commit", "In this order: make only the planned edit, commit it, push, measure the after-inventory with `muda gates --ref muda/F-001-YYYY-MM-DD-HHMM --format md`, open the PR with `gh pr create`, then add the record commit", "leaves the base at `approved`",
		"STOP and re-present the plan", "a prerequisite with its own waste is a new finding for muda-diagnose", "still out of scope",
		"same or stronger", "route to muda-diagnose for an exemption",
		// Task 6: the after-inventory is measured at the PR branch.
		"`muda gates --ref muda/F-001-YYYY-MM-DD-HHMM --format md`", "labelled measured", "measured after-inventory",
		"If the measured after-inventory is weaker than the approved plan, STOP before `gh pr create`",
		"a weakening covered by an exemption agreed through muda-diagnose is not a STOP", "the exact file",
		"`incomplete-baseline-set`", "re-run the exact same `record baseline` command, or do what the reported problem says"} {
		if !strings.Contains(norm(text), norm(s)) {
			t.Error("missing", s)
		}
	}
	// The guided-message and merging rules (feat/guided) raised 1000 to 1115;
	// the PR body scratch-file rule to 1130.
	if n := len(strings.Fields(text)); n > 1130 {
		t.Errorf("muda-fix is %d words; keep it at most 1130", n)
	}
	for _, s := range []string{"provisional", "<<", "two independently reviewable commits", "expected, not measured", "reads the default branch"} {
		if strings.Contains(strings.ToLower(norm(text)), s) {
			t.Error("forbidden", s)
		}
	}
}

// oddBacktickLines returns the 1-based lines outside fenced blocks with an odd
// number of backticks: an inline span wrapped across lines escapes the
// per-line inline-command lint.
func oddBacktickLines(text string) []int {
	var odd []int
	fence := ""
	for i, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		if fence != "" {
			if strings.HasPrefix(t, fence) {
				fence = ""
			}
			continue
		}
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			fence = t[:3]
			continue
		}
		if strings.Count(line, "`")%2 == 1 {
			odd = append(odd, i+1)
		}
	}
	return odd
}

func TestNoSplitInlineSpans(t *testing.T) {
	fixture := "---\nname: x\n---\nRun `muda\nrecipe R6` to read it.\n```sh\nmuda version\n```\n"
	if got := oddBacktickLines(fixture); !slices.Equal(got, []int{4, 5}) {
		t.Fatalf("split span not caught: %v", got)
	}
	if got := oddBacktickLines("a `b` c\n   ```sh\n   muda version\n   ```\n"); len(got) != 0 {
		t.Fatalf("fence or balanced span flagged: %v", got)
	}
	entries, e := fs.ReadDir(muda.Content, "skills")
	if e != nil {
		t.Fatal(e)
	}
	for _, d := range entries {
		if !d.IsDir() {
			continue
		}
		b, e := muda.Content.ReadFile("skills/" + d.Name() + "/SKILL.md")
		if e != nil {
			t.Fatal(e)
		}
		if got := oddBacktickLines(string(b)); len(got) != 0 {
			t.Errorf("skills/%s/SKILL.md: odd backticks on lines %v", d.Name(), got)
		}
	}
}

func TestVerifySkillContract(t *testing.T) {
	entry, e := muda.Content.ReadFile("skills/muda/SKILL.md")
	if e != nil {
		t.Fatal(e)
	}
	b, e := muda.Content.ReadFile("skills/muda-verify/SKILL.md")
	if e != nil {
		t.Fatal(e)
	}
	text := string(b)
	if preflight(t, text) != preflight(t, string(entry)) {
		t.Error("preflight must be the entry skill's text verbatim")
	}
	for _, s := range []string{"name: muda-verify", "muda-owned: muda-verify", "description: Use when", "muda-version: \">=0.1.0\"",
		"muda record show --format json", "muda record check", "anything but `expired-exemption`",
		"one `in-pr` finding", "`pr`",
		"muda compare --before", "--after-runs", "--gates --scan --ref muda/F-001-YYYY-MM-DD-HHMM --since 28d > muda-compare-pr.json", "muda compare --before 2026-09-01..2026-09-29 --after-runs 3333,4444 --gates --scan --since 28d > muda-compare-merged.json",
		"check out the fix branch", "return to the up-to-date base before Phase 2",
		"`muda logs 1234`", "`skipped` conclusion on a relevant job is a defect", "--grep \"(?i)skip|trusting\"",
		"Any non-zero exit with an empty or missing report is an error", "exit 2 is usage",
		"`removed`, `loosened`, `unverified` and `exempted` lists", "`UNVERIFIED` outranks `WEAKENED`",
		"`before_unavailable`", "`after_unavailable`", "`presence: \"unavailable\"`", "a missing before side is not an improvement",
		"`gate-status`", "`pr` (the finding's PR URL)", "are strings", "the merged compare", "both the PR and the post-merge run sets",
		"load muda-diagnose to agree an exact `gate:` exemption", "re-run muda-verify after that exemption's record PR is merged",
		"report the program complete, citing each finding's receipt", "--after 2026-10-01..2026-10-15 --gates --format md", "not recorded",
		"after merge", "post-merge runs on the default branch", "Green PR status alone is never fixed",
		"skipped or \"trusting…\" job is a defect, never a pass", "muda logs ",
		"`VERIFIED`", "`EXEMPTED`", "`UNVERIFIED`", "`WEAKENED`", "exits 1",
		"exact active exemption", "acceptance, not proof", "Never mark `fixed` on `UNVERIFIED`",
		// The pre-merge report follows Phase 2's reading rules.
		"the gate and scan results (read as in Phase 2)",
		// Decision 1: a byte-identical workflow file is VERIFIED; only a
		// changed one needs an exemption, whose reason cites its diff.
		"A changed workflow file reports `UNVERIFIED` without an exact accepted exemption",
		"a byte-identical one (equal full SHA-256) is `VERIFIED`",
		"Only changed workflow files need an exemption",
		"read its `workflow_diffs` entry", "its `reason` cites that diff",
		// Final round: VERIFIED covers the workflow files themselves, not
		// what they run.
		"`VERIFIED` covers `.github/workflows` files only, not local composite actions (`uses: ./…`), scripts a step runs or remote actions on moving refs",
		"stays `in-pr` with a receipt", "`reopened`", "**CHECKPOINT:**", "measured effect", "gate result",
		"muda-receipt.json", "muda record receipt F-001 --file muda-receipt.json", "`run-links`", "`gate-result`", "/actions/runs/",
		"{\"status\": \"fixed\"}", "{\"status\": \"reopened\"}", "`pr` is kept",
		"muda record finding set F-001 --file muda-patch.json", "absent", "only after `muda record check` passes",
		"`git switch -c muda/record-verify-F-001-YYYY-MM-DD-HHMM`", "commit only `.muda/`", "record-only PR", "`gh pr create`",
		"every finding is `fixed` or validly `exempt`", "load the entry skill",
		// Final review I1, Task 6: workflow proof IDs bind the raw file.
		"covers the exact file as it is now; any later edit needs a new exemption", "re-attested once",
		// Task 6: pre-merge gates and scan, scan proof in the receipt, next cycle.
		"before the merge ask",
		// Fix round 1 I-2: --since derives from the stored baseline window.
		"pass `--since` as `Nd`, N the whole days in the baseline window (`until` − `since`, from `muda record show --format json`)",
		// Fix round 1 M-3: a workflow edit reads UNVERIFIED before merge.
		"A PR that edits a workflow reads `UNVERIFIED` before merge (the file's hash changed)",
		"agree an exact `gate:` exemption for that hash through muda-diagnose before merging", "Never merge on `WEAKENED`",
		"`removed` and `added` signal lists", "never netted", "reads as removed plus added",
		"signals match by signal and summary, so a change within one file",
		"A removed signal is not proof of a fix unless the finding's cited location changed as the recipe says",
		"a removed `uncached-install` after a job rename is a rename, not a cache fix; ask the human",
		"an `added` signal is grounds to review the finding with the human",
		"scan comparison `unavailable`", "**STOP** and ask the human", "never \"nothing added\" or \"removed\"",
		"signals removed and added", "then load muda-measure for the next cycle's baseline",
		"`incomplete-baseline-set`", "re-run the exact same `record baseline` command, or do what the reported problem says",
		// Final review I-1.
		"A removed workflow is exempted by the exact file removed",
		// Final review m-5.
		"`policy:inventory:raw`", "re-recorded with `record baseline … --gates`, never exempted",
		// Final review m-7: Phase 1 reads the base's exemptions.
		"Run Phase 1's compare from the up-to-date base",
		// Live run: a faster required check must still do the same work.
		"for every required check whose job got faster, compare what it executed before and after",
		"tests run vs replayed, packages built, steps run or skipped",
		"Less work is a weakened gate, even with an unchanged inventory and a faster time",
		"fix it in the same PR, or **STOP**"} {
		if !strings.Contains(norm(text), norm(s)) {
			t.Error("missing", s)
		}
	}
	// Release prep P2 raised 1300 to 1308 for the experiment rule; P4 to
	// 1312 for the record-PR rule pointer; the fix round lowered it to 1310.
	// The guided-message and merging rules (feat/guided) raised 1310 to 1429.
	// The live-run executed-work rule raised 1429 to 1468; the PR body
	// scratch-file rule to 1483.
	if n := len(strings.Fields(text)); n > 1483 {
		t.Errorf("muda-verify is %d words; keep it at most 1483", n)
	}
	for _, s := range []string{"provisional", "<<", "green is green", "skipp|trusting", "waits on a settings/rebaseline command", "gate result waits for merge", "gates read the default branch", "relative `--since`",
		// Decision 1: an unchanged workflow file no longer reads UNVERIFIED.
		"workflow-backed gate policy reports `unverified`"} {
		if strings.Contains(strings.ToLower(norm(text)), s) {
			t.Error("forbidden", s)
		}
	}
	// Fix round 1 M-5: the post-merge compare reads the default branch, never a --ref.
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "muda-compare-merged.json") && strings.Contains(line, "--ref") {
			t.Error("post-merge compare must not pass --ref:", line)
		}
	}
}

// README describes what actually ships: the five skills and every install route.
func TestReadmeSkillsStatus(t *testing.T) {
	b, e := os.ReadFile("../../README.md")
	if e != nil {
		t.Fatal(e)
	}
	text := norm(string(b))
	for _, stale := range []string{"Phase A is partial", "only the entry skill", "not yet clean", "private development", "private and unreleased", "copier", "muda check"} {
		if strings.Contains(text, stale) {
			t.Error("stale README text:", stale)
		}
	}
	for _, want := range []string{"## The five skills", "`muda`", "`muda-measure`", "`muda-diagnose`", "`muda-fix`", "`muda-verify`",
		"curl -fsSL https://muda.tools/install.sh | sh", "muda update", "muda skills install", "/plugin marketplace add schuettc/muda", "pi install git:github.com/schuettc/muda", "not the binary"} {
		if !strings.Contains(text, norm(want)) {
			t.Error("README missing:", want)
		}
	}
	// Final review M2: the skills directory README ships in the binary and
	// the plugin/package route, so it must describe what ships.
	b, e = muda.Content.ReadFile("skills/README.md")
	if e != nil {
		t.Fatal(e)
	}
	text = norm(string(b))
	for _, stale := range []string{"planned for Task 8", "No skills or installer", "shipped yet"} {
		if strings.Contains(text, stale) {
			t.Error("stale skills/README.md text:", stale)
		}
	}
	for _, want := range []string{"five skills", "`muda`", "`muda-measure`", "`muda-diagnose`", "`muda-fix`", "`muda-verify`", "embedded in the muda binary", "muda skills install", "Claude Code plugin", "pi package", "not the binary"} {
		if !strings.Contains(text, norm(want)) {
			t.Error("skills/README.md missing:", want)
		}
	}
}

// Release prep P5 (decision 5): a pipeline change inside the window is shown
// at the checkpoint with three choices, small samples are normal and labelled
// rather than blocked, and a small sample is `measured` only through direct
// evidence, never an unlabelled extrapolation.
func TestWindowChangeAndSmallSampleRules(t *testing.T) {
	read := func(n string) string {
		b, e := muda.Content.ReadFile("skills/" + n + "/SKILL.md")
		if e != nil {
			t.Fatal(e)
		}
		return norm(string(b))
	}
	measure := read("muda-measure")
	for _, s := range []string{
		"`definition_history`", "first run, runs before and after, jobs added or removed",
		"an `unavailable` history cannot rule one out",
		// P5 fix round I2/I1: only default-branch changes are start points;
		// a pull request's version is approximate.
		"`branch_only_versions` are not changes, and `approximate` means read at a pull request's head",
		"Offer: start the window after the change (`--since` its `created_at`) and say the sample is small",
		"keep the full window and say it mixes pipelines", "or wait for more runs",
		"Small samples are normal: label `confidence`, never block on them; muda-diagnose rules what they prove",
		// The pressure test cited numbers without their label.
		"each cited number carries its `confidence` label",
		// P5 fix round I3.
		"never hand-edit `.muda/` or the evidence files",
	} {
		if !strings.Contains(measure, norm(s)) {
			t.Error("muda-measure missing", s)
		}
	}
	// The three choices are part of the window item the checkpoint confirms.
	if i, j, k := strings.Index(measure, "3. **Window:**"), strings.Index(measure, "keep the full window"), strings.Index(measure, "4. **Gate inventory:**"); i < 0 || j < i || k < j {
		t.Error("the window choices belong to the checkpoint's window item")
	}
	if i, j, k := strings.Index(measure, "5. **Where the time goes:**"), strings.Index(measure, "each cited number carries"), strings.Index(measure, "6. **Existing record:**"); i < 0 || j < i || k < j {
		t.Error("the confidence clause belongs to the time sinks item")
	}
	for _, s := range []string{"enough runs", "not a handful"} {
		if strings.Contains(measure, s) {
			t.Error("muda-measure must not block on run count:", s)
		}
	}
	// P5 fix round M2: the direct-evidence rule sits where labels are set.
	diagnose := read("muda-diagnose")
	rule := norm("On a low-`confidence` sample, `measured` needs same-run or paired evidence; label any extrapolated saving.")
	if i, j, k := strings.Index(diagnose, "3. **Label:**"), strings.Index(diagnose, rule), strings.Index(diagnose, "4. **Estimate**"); i < 0 || j < i || k < j {
		t.Error("muda-diagnose step 3 missing", rule)
	}
}

// Release prep P2 (decision 2): a suspected finding is approved only as a
// labelled experiment with pass criteria in its estimate; its basis stays
// suspected and never feeds a saving. muda-verify either upgrades the basis
// to measured with a receipt, or the PR closes and the finding stays
// suspected with the new evidence.
func TestSuspectedExperimentRules(t *testing.T) {
	read := func(n string) string {
		b, e := muda.Content.ReadFile("skills/" + n + "/SKILL.md")
		if e != nil {
			t.Fatal(e)
		}
		return norm(string(b))
	}
	diagnose := read("muda-diagnose")
	rule := norm("A `suspected` finding is approved only as an experiment: its `estimate` starts `Experiment:` and states pass criteria. It stays `suspected` and never feeds a saving.")
	// The rule belongs to the checkpoint, where approval is agreed.
	if i, j, k := strings.Index(diagnose, "**CHECKPOINT:**"), strings.Index(diagnose, rule), strings.Index(diagnose, "## Record (only after the checkpoint)"); i < 0 || j < i || k < j {
		t.Error("muda-diagnose checkpoint missing", rule)
	}
	verify := read("muda-verify")
	for _, s := range []string{
		"An experiment (a `suspected` finding) is judged by its `estimate`'s pass criteria",
		"Met: the `fixed` patch adds `\"basis\": \"measured\"`, citing the receipt",
		"Not met: the human closes the PR; from the base, patch `reopened` and the new `evidence` (still `suspected`) through a record PR",
		"Any other fix PR closed unmerged leaves the base `approved`: record nothing; load the entry skill.",
	} {
		if !strings.Contains(verify, norm(s)) {
			t.Error("muda-verify missing", s)
		}
	}
	// Rules kept by the words cut to make room.
	if !strings.Contains(diagnose, "a finding follows evidence, not a deadline") {
		t.Error("muda-diagnose lost the deadline rule")
	}
	for _, s := range []string{"Verify in the real environment", "decide `fixed`, `reopened`, or that it stays `in-pr` with a receipt saying why"} {
		if !strings.Contains(verify, norm(s)) {
			t.Error("muda-verify missing", s)
		}
	}
	// Fix round: a failed experiment is recorded (reopened), so the
	// closed-unmerged exit covers only other fix PRs, and follows it.
	if i, j, k := strings.Index(verify, "An experiment (a `suspected` finding)"), strings.Index(verify, "Any other fix PR closed unmerged"), strings.Index(verify, "## Phase 2: after merge"); i < 0 || j < i || k < j {
		t.Error("Phase 1 must give the experiment rule, then the other closed-unmerged exit")
	}
	if strings.Contains(verify, "nothing to record here") {
		t.Error("muda-verify: a failed experiment is recorded; stale closed-unmerged text")
	}
}

// Release prep P4 (decision 4): every record PR carries muda.toml's
// record-pr labels and has its checks watched; a convention failure gets a
// proposed PR-carried fix, asked once and recorded; no check is bypassed.
func TestRecordPRConventionRules(t *testing.T) {
	read := func(n string) string {
		b, e := muda.Content.ReadFile("skills/" + n + "/SKILL.md")
		if e != nil {
			t.Fatal(e)
		}
		return norm(string(b))
	}
	entry := read("muda")
	for _, s := range []string{
		"Open every record PR with a `--label` per muda.toml `record-pr` label and watch its checks; never bypass or weaken one.",
		"On a repo-convention failure (missing label, title format), propose the PR-carried fix, ask once, apply it, and record a label on its branch with `muda record baseline --file .muda/baseline.json --settings muda-settings.json` (current settings plus `record-pr`).",
	} {
		if !strings.Contains(entry, norm(s)) {
			t.Error("muda missing", s)
		}
	}
	// Every skill that opens a record PR follows that rule.
	for _, n := range []string{"muda-measure", "muda-diagnose", "muda-verify"} {
		if !strings.Contains(read(n), "open a record-only PR with `gh pr create` (entry skill's record-PR rule).") {
			t.Errorf("%s: record PR does not follow the entry skill's record-PR rule", n)
		}
	}
	// Rules kept by the words cut to make room.
	for n, kept := range map[string][]string{
		"muda":         {"Check any reviewer or agent claim against its cited evidence", "| No baseline, including after init | Load muda-measure. |", "| Approved finding | Load muda-fix. |"},
		"muda-measure": {"Record the gate inventory exactly as derived; never trim, add or \"green\" it", "each `unavailable` entry, with why", "Your move: confirm roles, stacks, gate inventory"},
		"muda-fix":     {"exactly one `approved` finding"},
		"muda-verify":  {"Green PR status alone is never fixed"},
	} {
		for _, s := range kept {
			if !strings.Contains(read(n), norm(s)) {
				t.Errorf("%s missing %q", n, s)
			}
		}
	}
	// --settings replaces muda.toml, so a re-recorded baseline keeps the labels.
	if !strings.Contains(read("muda-measure"), "plus any `record-pr`") {
		t.Error("muda-measure must carry record-pr into muda-settings.json")
	}
}

// Release prep P6 (decision 6): the entry skill's stop-and-ask path for an
// unmerged fixed finding offers fixed -> reopened with a reopen-reason,
// recorded through muda-diagnose; a merged fix needs a new finding.
// muda-verify still marks fixed only after the merged pipeline.
func TestUnmergedFixedReopenRoute(t *testing.T) {
	read := func(n string) string {
		b, e := muda.Content.ReadFile("skills/" + n + "/SKILL.md")
		if e != nil {
			t.Fatal(e)
		}
		return norm(string(b))
	}
	entry := read("muda")
	offer := norm("if closed, **STOP** and ask. If the human confirms it did not merge, offer `reopened` with a `reopen-reason` through muda-diagnose; a merged fix needs a new finding.")
	if i, j := strings.Index(entry, offer), strings.Index(entry, "explicitly asks to re-measure"); i < 0 || j < i {
		t.Error("muda: the stop-and-ask path must offer the reopen, before the re-measure rule:", offer)
	}
	if !strings.Contains(read("muda-diagnose"), norm("in-pr and fixed belong to muda-fix and muda-verify, except `fixed` → `reopened` when `gh pr view <pr> --json mergedAt` is null, cited in `reopen-reason`.")) {
		t.Error("muda-diagnose must record the entry skill's unmerged fixed -> reopened")
	}
	// Rules kept by the words cut to make room.
	for _, s := range []string{"this entry consent carries over", "keep it and **STOP**", "| `in-pr` finding | Load muda-verify. |"} {
		if !strings.Contains(entry, norm(s)) {
			t.Error("muda missing", s)
		}
	}
	verify := read("muda-verify")
	for _, s := range []string{"Never mark `fixed` on `UNVERIFIED` or `WEAKENED`", "post-merge runs on the default branch", "Green PR status alone is never fixed", "## Phase 2: after merge"} {
		if !strings.Contains(verify, norm(s)) {
			t.Error("muda-verify missing", s)
		}
	}
}

// Court's live-run direction: every stop for the human ends with one concrete
// "Your move" ask, and the agent may offer to merge, never bypassing a gate.
// Both rules close every skill's Rules section, byte-identical in all five.
const guidedRules = `**Messages:** recommend, never survey: decide every answer (order, labels,
approval). One line per checkpoint item: what, basis, key number,
recommendation; full evidence and every recipe outcome go to a named,
uncommitted scratch report (e.g. ` + "`.muda-diagnose.md`" + `). At most three status
bullets; no open "go ahead?". End with one ask: "**Your move:** reply 'yes' or
name changes. Next: what follows." Ready PR: "say 'merge' and I'll merge #N
(link) once green, or merge and reply 'merged'".

**Merging:** only on an explicit yes to that PR, every required check green on
the reported head: ` + "`gh pr merge N --squash --match-head-commit SHA`" + `. No admin
bypass, removed check, or gate-passing label unless a ` + "`record-pr`" + ` label or the
human agreed. A fix PR merges only after Phase 1 verify is shown.
`

func rulesSection(t *testing.T, text string) string {
	t.Helper()
	i := strings.Index(text, "## Rules\n")
	if i < 0 {
		t.Fatal("no Rules section")
	}
	j := strings.Index(text[i+1:], "\n## ")
	if j < 0 {
		t.Fatal("no section after Rules")
	}
	return text[i : i+1+j+1]
}

func TestGuidedMessageAndMergeRules(t *testing.T) {
	var first string
	for _, n := range []string{"muda", "muda-measure", "muda-diagnose", "muda-fix", "muda-verify"} {
		b, e := muda.Content.ReadFile("skills/" + n + "/SKILL.md")
		if e != nil {
			t.Fatal(e)
		}
		text := string(b)
		rules := rulesSection(t, text)
		if !strings.HasSuffix(rules, "\n\n"+guidedRules+"\n") {
			t.Errorf("%s: Rules must end with the shared guided and merging rules, verbatim", n)
		}
		if k := strings.Index(rules, "**Messages:**"); k >= 0 {
			if first == "" {
				first = rules[k:]
			} else if rules[k:] != first {
				t.Errorf("%s: the shared rules differ from the entry skill's copy", n)
			}
		}
		if strings.Count(text, "**Messages:**") != 1 || strings.Count(text, "**Merging:**") != 1 {
			t.Errorf("%s: the shared rules must appear exactly once", n)
		}
		if strings.Contains(preflight(t, text), "Your move") {
			t.Errorf("%s: the guided rule must not be in the preflight", n)
		}
		// Every human stop names a concrete Your move ask.
		low := norm(text)
		for _, stale := range []string{"never merge;", "never merge.", "Never merge;", "Never merge,", "the human merges", "wait for the human to merge"} {
			if strings.Contains(low, stale) {
				t.Errorf("%s: stale merge text %q", n, stale)
			}
		}
		i := strings.Index(text, "**CHECKPOINT:**")
		if i < 0 {
			t.Fatalf("%s: no checkpoint", n)
		}
		end := strings.Index(text[i:], "\n\n")
		if end < 0 || !strings.Contains(text[i:i+end], "Your move") {
			t.Errorf("%s: the CHECKPOINT must be a Your move ask", n)
		}
		// muda-fix hands straight to muda-verify; it does not stop there.
		if k := strings.Index(text, "\n## Next\n"); k >= 0 && n != "muda-fix" {
			next := text[k:]
			if m := strings.Index(next[1:], "\n## "); m >= 0 {
				next = next[:m+1]
			}
			if !strings.Contains(next, "Your move") {
				t.Errorf("%s: Next must end on a Your move ask", n)
			}
		}
	}
	// The pinned rule keeps each merge safeguard.
	for _, s := range []string{"explicit yes to that PR", "every required check green on the reported head", "--match-head-commit", "No admin bypass", "unless a `record-pr` label or the human agreed", "only after Phase 1 verify is shown", "At most three status bullets", "no open \"go ahead?\"", "reply 'yes' or name changes",
		// Live run 2: recommend, don't survey; a short checkpoint, with the
		// full evidence and every recipe outcome in a scratch report.
		"recommend, never survey: decide every answer (order, labels, approval)",
		"One line per checkpoint item: what, basis, key number, recommendation",
		"full evidence and every recipe outcome go to a named, uncommitted scratch report"} {
		if !strings.Contains(norm(guidedRules), norm(s)) {
			t.Error("shared rules missing", s)
		}
	}
	// The entry skill still never routes on an unmerged record PR.
	b, _ := muda.Content.ReadFile("skills/muda/SKILL.md")
	if !strings.Contains(norm(string(b)), "never route on unmerged state") {
		t.Error("muda lost: never route on unmerged state")
	}
}

// Live run: measure and diagnose share one record PR. Measure commits the
// baseline on a record branch without a PR; diagnose continues on it and opens
// the one PR with the baseline and findings; the fix phase waits for its
// merge. A measure-only run, or a next-cycle baseline not diagnosed now, is
// the exception that still opens its PR at the end of measure.
func TestOneRecordPRForBaselineAndFindings(t *testing.T) {
	read := func(n string) string {
		b, e := muda.Content.ReadFile("skills/" + n + "/SKILL.md")
		if e != nil {
			t.Fatal(e)
		}
		return norm(string(b))
	}
	measure, diagnose, entry := read("muda-measure"), read("muda-diagnose"), read("muda")
	for n, want := range map[string][]string{
		"muda-measure": {
			"`git switch -c muda/record-baseline-YYYY-MM-DD-HHMM`), commit only `.muda/` and push; open no PR:",
			"muda-diagnose adds findings and opens one record PR for both.",
			"Exception (a measure-only run, or a next-cycle baseline diagnosed later): open a record-only PR with `gh pr create` (entry skill's record-PR rule).",
			"then load muda-diagnose on this branch.",
			"In the exception, give the Your move merge ask.",
		},
		"muda-diagnose": {
			"Handed muda-measure's record branch, stay on it: read the record there.",
			"On a handed branch, commit only `.muda/` there; else on a record-only branch from the up-to-date base (`git switch -c muda/record-findings-YYYY-MM-DD-HHMM`).",
			"On a handed branch it is the one PR for baseline and findings, even with none agreed.",
			"The next phase starts only once that record PR is merged; then load muda-fix",
		},
		"muda": {
			"First, a pushed `muda/record-baseline-…` branch for which `gh pr list --state all --head <branch>` returns nothing (no PR, open, closed or merged) is muda-measure's handoff: switch to it and load muda-diagnose, which reads the record there. More than one such branch: STOP and ask. It is the only unmerged state routed.",
			"give the Your move merge ask: never route on unmerged state",
		},
	} {
		for _, s := range want {
			if !strings.Contains(read(n), norm(s)) {
				t.Errorf("%s missing %q", n, s)
			}
		}
	}
	// The old flow: a baseline PR to merge before diagnose could start.
	if strings.Contains(measure, "merge ask; once merged, load muda-diagnose") {
		t.Error("muda-measure still waits on a baseline PR before diagnose")
	}
	// The exception follows the normal path it qualifies.
	if i, j := strings.Index(measure, "open no PR"), strings.Index(measure, "Exception (a measure-only run"); i < 0 || j < i {
		t.Error("muda-measure: the no-PR handoff must precede its exception")
	}
	// The handoff is checked before the init checkpoint can fire.
	if i, j := strings.Index(entry, "muda-measure's handoff"), strings.Index(entry, "If no `.muda/` exists:"); i < 0 || j < i {
		t.Error("muda: the handoff check must precede the init checkpoint")
	}
	// The fix phase still waits for the merged record.
	if !strings.Contains(diagnose, "starts only once that record PR is merged") {
		t.Error("muda-diagnose: the fix phase must wait for the record PR")
	}
}

// Live run: PR bodies go through a scratch file inside the repository, never
// /tmp, passed with --body-file and deleted after. Every skill that opens a
// PR says so beside its `gh pr create`.
const prBodyRule = "Pass the body via `--body-file` from an in-repo scratch file (never /tmp); delete it after."

func TestPRBodyViaScratchFile(t *testing.T) {
	for _, n := range []string{"muda-measure", "muda-diagnose", "muda-fix", "muda-verify"} {
		b, e := muda.Content.ReadFile("skills/" + n + "/SKILL.md")
		if e != nil {
			t.Fatal(e)
		}
		text := norm(string(b))
		i, j := strings.Index(text, "`gh pr create`"), strings.Index(text, norm(prBodyRule))
		if j < 0 {
			t.Errorf("%s missing %q", n, prBodyRule)
			continue
		}
		// The rule follows the PR it governs.
		if i < 0 || j < i {
			t.Errorf("%s: the PR body rule must follow `gh pr create`", n)
		}
		for _, stale := range []string{"--body \"", "--body '", "/tmp/"} {
			if strings.Contains(text, stale) {
				t.Errorf("%s: stale PR body form %q", n, stale)
			}
		}
	}
}
