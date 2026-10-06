package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schuettc/muda/internal/record"
)

func TestRecordDispatch(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: elsewhere"), 0600); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)
	settings := `{"schema":1,"roles":{"ci.yml":"verify"},"stacks":["go"],"window":{"since":"2026-09-01","until":"2026-09-15"},"muda-version":"dev"}`
	f := `{"waste":"waiting","location":"workflow:ci.yml/job:test","recipe":"slow-step","evidence":[{"kind":"file","path":"ci.yml"}],"basis":"suspected","estimate":"2m","risk":"low"}`
	dispatch := func(args []string, input string, want int) string {
		t.Helper()
		file, err := os.CreateTemp(t.TempDir(), "stdin")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = file.WriteString(input); err != nil {
			t.Fatal(err)
		}
		if _, err = file.Seek(0, 0); err != nil {
			t.Fatal(err)
		}
		old := os.Stdin
		os.Stdin = file
		defer func() {
			os.Stdin = old
			if err := file.Close(); err != nil {
				t.Error(err)
			}
		}()
		var out, errs bytes.Buffer
		env := fixtureEnv(t)
		env.GHToken = func() (string, error) { t.Fatal("local command requested auth"); return "", nil }
		env.Getenv = func(string) string { t.Fatal("local command requested environment token"); return "" }
		code := newApp(env).Dispatch(args, &out, &errs)
		if code != want {
			t.Fatalf("%v exit %d want %d: %s", args, code, want, errs.String())
		}
		return out.String()
	}
	dispatch([]string{"record", "init"}, settings, 0)
	dispatch([]string{"record", "baseline"}, `{"schema":1,"repo":"o/r","since":"2026-09-01T00:00:00Z","until":"2026-09-15T00:00:00Z","workflows":[],"scan":{"schema":1,"extra":"kept"}}`, 0)
	gateFile := filepath.Join(t.TempDir(), "gates.json")
	raw := `{"schema":1,"gates":[{"id":"check:main:test"}],"raw-policy":{"unknown":{"keep":true}},"workflow-conditions":"preserved"}`
	if err := os.WriteFile(gateFile, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	dispatch([]string{"record", "gates", "set", "--file", gateFile}, "", 0)
	got := dispatch([]string{"record", "finding", "add"}, f, 0)
	if !strings.Contains(got, "F-001") {
		t.Fatal(got)
	}
	dispatch([]string{"record", "finding", "set", "F-001"}, `{"status":"approved","pr":"https://github.com/o/r/pull/1"}`, 0)
	dispatch([]string{"record", "finding", "set", "F-001"}, `{"status":"fixed"}`, 1)
	dispatch([]string{"record", "exempt"}, `{"id":"E-001","waste":"waiting","location":"workflow:ci.yml/job:test","reason":"deliberate","agreed-by":"owner","date":"2026-09-01","review-by":"2099-01-01"}`, 0)
	dispatch([]string{"record", "finding", "set", "F-001"}, `{"status":"exempt","exemption-id":"E-001"}`, 0)
	dispatch([]string{"record", "receipt", "F-001"}, `{"schema":1,"pr":"https://github.com/o/r/pull/1","before":"2m","after":"1m","run-links":["https://github.com/o/r/actions/runs/1"],"gate-status":"VERIFIED","gate-result":"unchanged"}`, 0)
	show := dispatch([]string{"record", "show"}, "", 0)
	var doc map[string]any
	if err := json.Unmarshal([]byte(show), &doc); err != nil {
		t.Fatal(err)
	}
	if doc["schema"] != float64(1) || !strings.Contains(show, "raw-policy") || !strings.Contains(show, "workflow-conditions") {
		t.Fatal(show)
	}
	if !strings.HasPrefix(dispatch([]string{"record", "show", "--format", "md"}, "", 0), "# muda record") {
		t.Fatal("markdown")
	}
	dispatch([]string{"record", "check"}, "", 0)
	if err := os.WriteFile(filepath.Join(root, ".muda", "baseline.json"), []byte(`{"schema":2}`), 0600); err != nil {
		t.Fatal(err)
	}
	got = dispatch([]string{"record", "check"}, "", 1)
	if !strings.Contains(got, "newer-schema") {
		t.Fatal(got)
	}
	for _, args := range [][]string{{"record"}, {"record", "unknown"}, {"record", "finding", "set"}, {"record", "show", "--format", "xml"}, {"record", "receipt", "../outside"}, {"record", "show", "--file", "x"}, {"record", "gates"}} {
		dispatch(args, "", 2)
	}
	dispatch([]string{"record", "init"}, settings, 1)
}
func TestRecordAbsentRuntime(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	if err := os.Mkdir(".git", 0700); err != nil {
		t.Fatal(err)
	}
	var out, errs bytes.Buffer
	if code := NewApp().Dispatch([]string{"record", "show"}, &out, &errs); code != 1 {
		t.Fatalf("%d %s", code, errs.String())
	}
}

func TestRecordFileInputsAndMarkdown(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	if err := os.Mkdir(".git", 0700); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		args  []string
		input string
	}{
		{[]string{"init"}, `{"roles":{"ci.yml":"verify"},"stacks":["go"],"window":{"since":"2026-09-01","until":"2026-09-15"},"muda-version":"dev"}`},
		{[]string{"baseline"}, `{"schema":1,"repo":"o/r","since":"2026-09-01T00:00:00Z","until":"2026-09-15T00:00:00Z","workflows":[]}`},
		{[]string{"finding", "add"}, `{"waste":"waiting","location":"workflow:ci.yml/job:test","recipe":"slow-step","evidence":[{"kind":"file","path":"ci.yml"}],"basis":"measured","estimate":"2m","risk":"low"}`},
		{[]string{"finding", "set", "F-001"}, `{"status":"approved"}`},
		{[]string{"exempt"}, `{"id":"E-local","waste":"waiting","location":"workflow:ci.yml/job:test","reason":"deliberate","agreed-by":"owner","date":"2026-09-01","review-by":"2099-01-01"}`},
		{[]string{"receipt", "F-001"}, `{"pr":"https://github.com/o/r/pull/1","before":"2m","after":"1m","run-links":["https://github.com/o/r/actions/runs/1"],"gate-status":"VERIFIED","gate-result":"unchanged"}`},
	} {
		file := filepath.Join(t.TempDir(), "input.json")
		if err := os.WriteFile(file, []byte(tc.input), 0600); err != nil {
			t.Fatal(err)
		}
		args := append([]string{"record"}, tc.args...)
		args = append(args, "--file", file, "--format", "md")
		var out, errs bytes.Buffer
		if code := NewApp().Dispatch(args, &out, &errs); code != 0 || !strings.HasPrefix(out.String(), "# muda record") {
			t.Fatalf("%v %d %s %s", args, code, out.String(), errs.String())
		}
	}
	var out, errs bytes.Buffer
	if code := NewApp().Dispatch([]string{"record", "check", "--format", "md"}, &out, &errs); code != 0 || !strings.HasPrefix(out.String(), "# muda record") {
		t.Fatalf("%d %s %s", code, out.String(), errs.String())
	}
	if code := NewApp().Dispatch([]string{"record", "baseline", "--file", "missing.json"}, &out, &errs); code != 1 {
		t.Fatalf("missing file exit %d", code)
	}
	if code := NewApp().Dispatch([]string{"help", "record"}, &out, &errs); code != 0 || !strings.Contains(out.String(), "review-by") {
		t.Fatalf("help %d %s", code, errs.String())
	}
}

func TestTypedRecordJSONMustBeObject(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	if err := os.Mkdir(".git", 0700); err != nil {
		t.Fatal(err)
	}
	s := &record.Store{Root: root}
	if err := s.Init(record.Settings{Roles: map[string]string{"ci.yml": "verify"}, Stacks: []string{"go"}, Window: record.Window{Since: "2026-09-01", Until: "2026-09-15"}, MudaVersion: "dev"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddFinding(record.Finding{Waste: "waiting", Location: "workflow:ci.yml/job:test", Recipe: "slow-step", Evidence: []record.Evidence{{Kind: "file", Path: "ci.yml"}}, Basis: "suspected", Estimate: "2m", Risk: "low"}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(root, ".muda", "findings.toml"))
	if err != nil {
		t.Fatal(err)
	}
	beforeInfo, err := os.Stat(filepath.Join(root, ".muda", "findings.toml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init"}, {"finding", "add"}, {"finding", "set", "F-001"}, {"exempt"}, {"receipt", "F-001"}} {
		for _, input := range []string{"null", "[]", "42", "true", `"value"`, "{} {}"} {
			for _, mode := range []string{"stdin", "file"} {
				t.Run(strings.Join(args, "-")+"-"+input+"-"+mode, func(t *testing.T) {
					f, err := os.CreateTemp(t.TempDir(), "input")
					if err != nil {
						t.Fatal(err)
					}
					defer func() {
						if err := f.Close(); err != nil {
							t.Error(err)
						}
					}()
					if _, err = f.WriteString(input); err != nil {
						t.Fatal(err)
					}
					if _, err = f.Seek(0, 0); err != nil {
						t.Fatal(err)
					}
					a := append([]string{"record"}, args...)
					old := os.Stdin
					defer func() { os.Stdin = old }()
					if mode == "file" {
						a = append(a, "--file", f.Name())
					} else {
						os.Stdin = f
					}
					var out, errs bytes.Buffer
					if code := NewApp().Dispatch(a, &out, &errs); code != 2 {
						t.Errorf("exit %d want 2: %s", code, errs.String())
					}
					after, err := os.ReadFile(filepath.Join(root, ".muda", "findings.toml"))
					if err != nil || !bytes.Equal(before, after) {
						t.Fatal("findings changed", err)
					}
					afterInfo, err := os.Stat(filepath.Join(root, ".muda", "findings.toml"))
					if err != nil || !os.SameFile(beforeInfo, afterInfo) || !beforeInfo.ModTime().Equal(afterInfo.ModTime()) {
						t.Fatal("findings rewritten", err)
					}
				})
			}
		}
	}
}

func recordRepo(t *testing.T, window string) string {
	t.Helper()
	root := t.TempDir()
	t.Chdir(root)
	if err := os.Mkdir(".git", 0700); err != nil {
		t.Fatal(err)
	}
	settings := `{"schema":1,"roles":{"ci.yml":"verify"},"stacks":["go"],"window":` + window + `,"muda-version":"dev"}`
	var out, errs bytes.Buffer
	if code := NewApp().Dispatch([]string{"record", "init", "--file", writeInput(t, settings)}, &out, &errs); code != 0 {
		t.Fatalf("init %d %s", code, errs.String())
	}
	return root
}
func writeInput(t *testing.T, body string) string {
	t.Helper()
	f := filepath.Join(t.TempDir(), "input.json")
	if err := os.WriteFile(f, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestRecordBaselineFlags(t *testing.T) {
	root := recordRepo(t, `{"since":"2026-09-01","until":"2026-09-15"}`)
	since, until := "2026-09-16T00:00:00Z", "2026-09-30T00:00:00Z"
	measure := `{"schema":1,"repo":"o/r","since":"` + since + `","until":"` + until + `","workflows":[],"signals":[]}`
	settings := `{"schema":1,"roles":{"ci.yml":"verify"},"stacks":["go"],"window":{"since":"` + since + `","until":"` + until + `"},"muda-version":"dev"}`
	gates := `{"schema":1,"repo":"o/r","security":[],"workflows":[],"gates":[]}`
	scan := `{"schema":1,"repo":"o/r","ref":"main","files":["ci.yml"],"signals":[]}`
	args := []string{"record", "baseline", "--file", writeInput(t, measure), "--settings", writeInput(t, settings), "--gates", writeInput(t, gates), "--scan", writeInput(t, scan)}
	var out, errs bytes.Buffer
	if code := NewApp().Dispatch(args, &out, &errs); code != 0 {
		t.Fatalf("exit %d: %s", code, errs.String())
	}
	for name, want := range map[string]string{"baseline.json": measure, "scan.json": scan, "gates.toml": "security", "muda.toml": since} {
		b, err := os.ReadFile(filepath.Join(root, ".muda", name))
		if err != nil || !strings.Contains(string(b), want) {
			t.Fatalf("%s: %v %s", name, err, b)
		}
	}
	out.Reset()
	if code := NewApp().Dispatch([]string{"record", "check"}, &out, &errs); code != 0 {
		t.Fatalf("check %d %s", code, out.String())
	}
	out.Reset()
	if code := NewApp().Dispatch([]string{"record", "show"}, &out, &errs); code != 0 || !strings.Contains(out.String(), `"scan"`) {
		t.Fatalf("show %d %s", code, out.String())
	}
	// The set flags belong to baseline alone.
	for _, flag := range []string{"--settings", "--gates", "--scan"} {
		errs.Reset()
		if code := NewApp().Dispatch([]string{"record", "gates", "set", "--file", writeInput(t, gates), flag, writeInput(t, scan)}, &out, &errs); code != 2 {
			t.Fatalf("%s on gates set: exit %d", flag, code)
		}
	}
	if code := NewApp().Dispatch([]string{"record", "baseline", "--file", writeInput(t, measure), "--scan", "missing.json"}, &out, &errs); code != 1 {
		t.Fatalf("missing --scan exit %d", code)
	}
}

func TestRecordBaselineRerecordUsage(t *testing.T) {
	root := recordRepo(t, `{"since":"2026-09-01","until":"2026-09-30"}`)
	before, err := os.ReadFile(filepath.Join(root, ".muda", "baseline.json"))
	if err != nil {
		t.Fatal(err)
	}
	measure := `{"schema":1,"repo":"o/r","since":"2026-09-05T00:00:00Z","until":"2026-10-05T00:00:00Z","workflows":[]}`
	var out, errs bytes.Buffer
	if code := NewApp().Dispatch([]string{"record", "baseline", "--file", writeInput(t, measure)}, &out, &errs); code != 1 || !strings.Contains(errs.String(), "--settings") || !strings.Contains(errs.String(), "window-mismatch") {
		t.Fatalf("exit %d: %s", code, errs.String())
	}
	after, err := os.ReadFile(filepath.Join(root, ".muda", "baseline.json"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("baseline changed", err)
	}
}

func TestRecordHelpDocumentsBaselineRecovery(t *testing.T) {
	var out, errs bytes.Buffer
	if code := NewApp().Dispatch([]string{"help", "record"}, &out, &errs); code != 0 {
		t.Fatalf("help %d %s", code, errs.String())
	}
	for _, want := range []string{".baseline-set-pending", "incomplete-baseline-set", "re-run the same record baseline command", "corrupt muda.toml must be fixed by hand", "show is refused", "every flag the unfinished run had", "An invalid marker"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("help lacks %q", want)
		}
	}
}

func TestRecordShowRefusesPendingSet(t *testing.T) {
	root := recordRepo(t, `{"since":"2026-09-01","until":"2026-09-15"}`)
	marker := `{"schema":1,"files":{"baseline.json":"` + strings.Repeat("ab", 32) + `"}}`
	if err := os.WriteFile(filepath.Join(root, ".muda", ".baseline-set-pending"), []byte(marker), 0600); err != nil {
		t.Fatal(err)
	}
	var out, errs bytes.Buffer
	if code := NewApp().Dispatch([]string{"record", "show"}, &out, &errs); code != 1 || !strings.Contains(errs.String(), "incomplete-baseline-set") {
		t.Fatalf("show exit %d: %s", code, errs.String())
	}
	out.Reset()
	if code := NewApp().Dispatch([]string{"record", "check"}, &out, &errs); code != 1 || !strings.Contains(out.String(), "incomplete-baseline-set") {
		t.Fatalf("check exit %d: %s", code, out.String())
	}
}

// A closed window's baseline records gates and scan read at its last commit
// with --ref; the record names the ref as historical and the settings as
// current.
func TestRecordBaselineHistoricalRef(t *testing.T) {
	root := recordRepo(t, `{"since":"2020-01-01","until":"2020-01-15"}`)
	sha := "0123456789abcdef0123456789abcdef01234567"
	measure := `{"schema":1,"repo":"o/r","since":"2020-01-01T00:00:00Z","until":"2020-01-15T00:00:00Z","workflows":[],"signals":[]}`
	gates := `{"schema":1,"repo":"o/r","branch":"main","ref":"` + sha + `","security":[],"workflows":[],"gates":[]}`
	scan := `{"schema":1,"repo":"o/r","ref":"` + sha + `","files":["ci.yml"],"signals":[]}`
	args := []string{"record", "baseline", "--file", writeInput(t, measure), "--gates", writeInput(t, gates), "--scan", writeInput(t, scan)}
	var out, errs bytes.Buffer
	if code := NewApp().Dispatch(args, &out, &errs); code != 1 || !strings.Contains(errs.String(), "ref-mismatch") {
		t.Fatalf("without --ref: exit %d %s", code, errs.String())
	}
	errs.Reset()
	if code := NewApp().Dispatch(append(args, "--ref", sha), &out, &errs); code != 0 {
		t.Fatalf("exit %d: %s", code, errs.String())
	}
	b, err := os.ReadFile(filepath.Join(root, ".muda", "gates.toml"))
	if err != nil || !strings.Contains(string(b), `historical-ref = "`+sha+`"`) {
		t.Fatalf("gates.toml %v %s", err, b)
	}
	out.Reset()
	if code := NewApp().Dispatch([]string{"record", "check"}, &out, &errs); code != 0 {
		t.Fatalf("check %d %s", code, out.String())
	}
	out.Reset()
	if code := NewApp().Dispatch([]string{"record", "show"}, &out, &errs); code != 0 || !strings.Contains(out.String(), `"historical"`) || !strings.Contains(out.String(), ", not historical") {
		t.Fatalf("show %d %s", code, out.String())
	}
	// --ref belongs to baseline alone, and must name something.
	for _, tc := range [][]string{
		{"record", "gates", "set", "--file", writeInput(t, gates), "--ref", sha},
		append(args, "--ref", " "),
	} {
		if code := NewApp().Dispatch(tc, &out, &errs); code != 2 {
			t.Fatalf("%v: exit %d", tc, code)
		}
	}
	out.Reset()
	if code := NewApp().Dispatch([]string{"help", "record"}, &out, &errs); code != 0 {
		t.Fatal(errs.String())
	}
	for _, want := range []string{"--ref COMMIT", "window has closed", "historical-ref", "before side",
		"at or before the start of the current UTC day", "full 40- or 64-hex commit SHA", "git rev-parse", "gates set refuses a historical gates.toml", "settings read DATE, not historical"} {
		if !strings.Contains(norm(out.String()), want) {
			t.Errorf("help lacks %q", want)
		}
	}
}

// Release prep P4: the skills' route for recording a record-PR label
// re-records the stored baseline with settings carrying record-pr, through
// the real dispatch; help documents the setting.
func TestRecordPRLabelsThroughStoredBaseline(t *testing.T) {
	root := recordRepo(t, `{"since":"2026-09-01","until":"2026-09-15"}`)
	measure := `{"schema":1,"repo":"o/r","since":"2026-09-01T00:00:00Z","until":"2026-09-15T00:00:00Z","workflows":[]}`
	var out, errs bytes.Buffer
	if code := NewApp().Dispatch([]string{"record", "baseline", "--file", writeInput(t, measure)}, &out, &errs); code != 0 {
		t.Fatalf("baseline %d %s", code, errs.String())
	}
	settings := `{"schema":1,"roles":{"ci.yml":"verify"},"stacks":["go"],"window":{"since":"2026-09-01","until":"2026-09-15"},"muda-version":"dev","record-pr":{"labels":["skip-changelog"]}}`
	if code := NewApp().Dispatch([]string{"record", "baseline", "--file", ".muda/baseline.json", "--settings", writeInput(t, settings)}, &out, &errs); code != 0 {
		t.Fatalf("re-record %d %s", code, errs.String())
	}
	b, err := os.ReadFile(filepath.Join(root, ".muda", "baseline.json"))
	if err != nil || string(b) != measure {
		t.Fatalf("stored baseline changed: %v %s", err, b)
	}
	out.Reset()
	if code := NewApp().Dispatch([]string{"record", "show"}, &out, &errs); code != 0 || !strings.Contains(out.String(), `"record-pr"`) || !strings.Contains(out.String(), `"skip-changelog"`) {
		t.Fatalf("show %d %s", code, out.String())
	}
	if code := NewApp().Dispatch([]string{"record", "check"}, &out, &errs); code != 0 {
		t.Fatalf("check %d", code)
	}
	out.Reset()
	if code := NewApp().Dispatch([]string{"help", "record"}, &out, &errs); code != 0 || !strings.Contains(out.String(), "record-pr={labels:[label]}") {
		t.Fatalf("help lacks record-pr: %s", out.String())
	}
}

// Release prep P6: through the real dispatch, fixed -> reopened needs a
// reopen-reason (the human's word that the PR did not merge); help says so.
func TestRecordFixedReopenThroughDispatch(t *testing.T) {
	recordRepo(t, `{"since":"2026-09-01","until":"2026-09-15"}`)
	run := func(want int, args ...string) string {
		t.Helper()
		var out, errs bytes.Buffer
		if code := NewApp().Dispatch(args, &out, &errs); code != want {
			t.Fatalf("%v exit %d want %d: %s", args, code, want, errs.String())
		}
		return out.String() + errs.String()
	}
	pr := "https://github.com/o/r/pull/1"
	run(0, "record", "finding", "add", "--file", writeInput(t, `{"waste":"waiting","location":"workflow:ci.yml/job:test","recipe":"slow-step","evidence":[{"kind":"file","path":"ci.yml"}],"basis":"suspected","estimate":"2m","risk":"low"}`))
	run(0, "record", "finding", "set", "F-001", "--file", writeInput(t, `{"status":"approved"}`))
	run(0, "record", "finding", "set", "F-001", "--file", writeInput(t, `{"status":"in-pr","pr":"`+pr+`"}`))
	run(0, "record", "receipt", "F-001", "--file", writeInput(t, `{"pr":"`+pr+`","before":"b","after":"a","run-links":["https://github.com/o/r/actions/runs/1"],"gate-status":"VERIFIED","gate-result":"none"}`))
	run(0, "record", "finding", "set", "F-001", "--file", writeInput(t, `{"status":"fixed"}`))
	if got := run(1, "record", "finding", "set", "F-001", "--file", writeInput(t, `{"status":"reopened"}`)); !strings.Contains(got, "missing-reopen-reason") {
		t.Fatalf("no reason: %s", got)
	}
	run(0, "record", "finding", "set", "F-001", "--file", writeInput(t, `{"status":"reopened","reopen-reason":"PR closed unmerged"}`))
	if got := run(0, "record", "show"); !strings.Contains(got, `"reopen-reason": "PR closed unmerged"`) {
		t.Fatalf("show: %s", got)
	}
	run(0, "record", "check")
	if got := run(0, "help", "record"); !strings.Contains(got, "fixed -> reopened only with reopen-reason") || !strings.Contains(got, "approved -> reopened (a failed experiment)") {
		t.Fatalf("help lacks fixed -> reopened: %s", got)
	}
}
