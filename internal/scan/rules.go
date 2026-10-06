package scan

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/schuettc/muda/internal/signal"
	"github.com/schuettc/muda/internal/workflow"
)

// ── helpers ──────────────────────────────────────────────────────────────────

var (
	// sha40 matches exactly 40 lowercase hex characters.
	sha40 = regexp.MustCompile(`^[0-9a-f]{40}$`)

	// exactSemver matches vX.Y.Z exactly (no pre-release, no build metadata).
	exactSemver = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)

	// exactVersion matches X.Y.Z (toolchain version without v prefix).
	exactVersion = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
)

// actionBase returns the owner/repo part of a uses: value, stripping the @ref.
// e.g. "actions/checkout@v4" → "actions/checkout"
func actionBase(uses string) string {
	if idx := strings.IndexByte(uses, '@'); idx >= 0 {
		return uses[:idx]
	}
	return uses
}

// actionRef returns the @ref part of a uses: value, or "" if none.
func actionRef(uses string) string {
	if idx := strings.IndexByte(uses, '@'); idx >= 0 {
		return uses[idx+1:]
	}
	return ""
}

// fileEv builds a file+step evidence for a signal.
func fileEv(path string, line int, note string) signal.Evidence {
	return signal.Evidence{Kind: signal.KindFile, Path: path, Line: line, Note: note}
}

// jobEv builds a job-level file evidence.
func jobEv(path string, line int, note string) signal.Evidence {
	return signal.Evidence{Kind: signal.KindJob, Path: path, Line: line, Note: note}
}

// isFalsy returns true for explicit-false YAML scalars ("false").
func isFalsy(v string) bool {
	return strings.EqualFold(v, "false")
}

// ── floating-ref ─────────────────────────────────────────────────────────────

// ruleFloatingRef checks every uses: value (step and reusable-job) for refs
// that are not a 40-hex SHA, not an exact vX.Y.Z, not a local action and not
// a Docker sha256 digest. Expression refs are unknown — they do not prove a
// float, so they do not fire.
func ruleFloatingRef(files []*workflow.File) []signal.Signal {
	var sigs []signal.Signal
	for _, f := range files {
		for _, jid := range f.JobOrder {
			job := f.Jobs[jid]
			// Reusable-workflow jobs have a uses: at job level.
			if job.Uses != "" {
				if s, ok := floatingRefSignal(f.Path, job.Uses, job.Line); ok {
					sigs = append(sigs, s)
				}
			}
			for _, step := range job.Steps {
				if step.Uses == "" {
					continue
				}
				if s, ok := floatingRefSignal(f.Path, step.Uses, step.Line); ok {
					sigs = append(sigs, s)
				}
			}
		}
	}
	return sigs
}

func floatingRefSignal(path, uses string, line int) (signal.Signal, bool) {
	// Local action: not floating.
	if strings.HasPrefix(uses, "./") {
		return signal.Signal{}, false
	}

	// Docker image: only a sha256 digest is pinned.
	if strings.HasPrefix(uses, "docker://") {
		if strings.Contains(uses, "@sha256:") {
			return signal.Signal{}, false
		}
		return signal.New("floating-ref", signal.SeverityWarn,
			fmt.Sprintf("docker image ref without digest: %s", uses),
			[]signal.Evidence{fileEv(path, line, uses)}), true
	}

	ref := actionRef(uses)
	// No @ref at all: not a standard action reference, skip.
	if ref == "" {
		return signal.Signal{}, false
	}
	// Expression ref: unknown, not proof of float.
	if strings.Contains(ref, "${{") {
		return signal.Signal{}, false
	}
	// 40-hex SHA: pinned.
	if sha40.MatchString(ref) {
		return signal.Signal{}, false
	}
	// Exact vX.Y.Z: pinned.
	if exactSemver.MatchString(ref) {
		return signal.Signal{}, false
	}
	// Everything else is floating.
	return signal.New("floating-ref", signal.SeverityWarn,
		fmt.Sprintf("action ref is not a SHA or exact vX.Y.Z: %s", uses),
		[]signal.Evidence{fileEv(path, line, uses)}), true
}

// ── floating-runner ───────────────────────────────────────────────────────────

// ruleFloatingRunner checks runs-on values for labels ending in -latest.
// Expression values are unknown and do not fire.
func ruleFloatingRunner(files []*workflow.File) []signal.Signal {
	var sigs []signal.Signal
	for _, f := range files {
		for _, jid := range f.JobOrder {
			job := f.Jobs[jid]
			if job.RunsOn == "" {
				continue
			}
			for _, label := range parseRunsOn(job.RunsOn) {
				if strings.HasSuffix(label, "-latest") {
					sigs = append(sigs, signal.New("floating-runner", signal.SeverityWarn,
						fmt.Sprintf("runner image floats on %s in job %s", label, job.ID),
						[]signal.Evidence{jobEv(f.Path, job.Line, label)}))
				}
			}
		}
	}
	return sigs
}

// parseRunsOn splits a runs-on value (scalar or [a, b, c] list literal) into
// individual labels, skipping expressions.
func parseRunsOn(runsOn string) []string {
	runsOn = strings.TrimSpace(runsOn)
	// Expression: unknown.
	if strings.Contains(runsOn, "${{") {
		return nil
	}
	// List in flow form: [a, b, c]
	if strings.HasPrefix(runsOn, "[") && strings.HasSuffix(runsOn, "]") {
		inner := runsOn[1 : len(runsOn)-1]
		parts := strings.Split(inner, ",")
		var out []string
		for _, p := range parts {
			label := strings.TrimSpace(p)
			if label != "" && !strings.Contains(label, "${{") {
				out = append(out, label)
			}
		}
		return out
	}
	return []string{runsOn}
}

// ── floating-toolchain ────────────────────────────────────────────────────────

// setupInputs names a setup action's toolchain and its version inputs.
type setupInputs struct {
	tool        string // grouping key: both setup-uv spellings are one tool
	versionKey  string
	versionFile string
}

var setupActions = map[string]setupInputs{
	"actions/setup-node":   {"node", "node-version", "node-version-file"},
	"actions/setup-python": {"python", "python-version", "python-version-file"},
	"actions/setup-go":     {"go", "go-version", "go-version-file"},
	"actions/setup-java":   {"java", "java-version", "java-version-file"},
	"astral-sh/setup-uv":   {"uv", "version", "version-file"},
	"actions/setup-uv":     {"uv", "version", "version-file"},
}

// ruleFloatingToolchain checks setup-{node,python,go,java,uv} steps for
// unpinned toolchain versions. A version-file input or an exact X.Y.Z version
// is OK.
func ruleFloatingToolchain(files []*workflow.File) []signal.Signal {
	var sigs []signal.Signal
	for _, f := range files {
		for _, jid := range f.JobOrder {
			job := f.Jobs[jid]
			for _, step := range job.Steps {
				if step.Uses == "" {
					continue
				}
				in, ok := setupActions[actionBase(step.Uses)]
				if !ok {
					continue
				}
				versionFile := step.With[in.versionFile]
				if versionFile != "" {
					continue // pinned via file
				}
				ver := step.With[in.versionKey]
				// Strip optional leading v for comparison.
				bare := strings.TrimPrefix(ver, "v")
				if exactVersion.MatchString(bare) {
					continue // exact X.Y.Z
				}
				shown := ver
				if shown == "" {
					shown = "(no version specified)"
				}
				sigs = append(sigs, signal.New("floating-toolchain", signal.SeverityWarn,
					fmt.Sprintf("%s version not pinned to exact X.Y.Z: %s", actionBase(step.Uses), shown),
					[]signal.Evidence{fileEv(f.Path, step.Line, fmt.Sprintf("%s %s=%s", actionBase(step.Uses), in.versionKey, shown))}))
			}
		}
	}
	return sigs
}

// ── toolchain-drift ───────────────────────────────────────────────────────────

// toolchainPin is one setup step's declared version: a literal version or a
// version file.
type toolchainPin struct {
	path, action, input, value string
	line                       int
	isFile                     bool
}

// key is what drift compares: two pins agree only if they are the same kind
// and the same value. A leading v on a literal version is not a difference.
func (p toolchainPin) key() string {
	if p.isFile {
		return "file:" + p.value
	}
	return "version:" + strings.TrimPrefix(p.value, "v")
}

// toolchainPins collects every setup step's pin by tool. A literal version
// input takes precedence over a version-file input, as it does in the setup
// actions. A value containing an expression (`${{ matrix.node }}`) is not a
// pin: its value is unknown, so it is returned as a named unavailable entry
// instead, one per job and tool whose Why lists every such input=value in
// step order (compare treats a note as unchanged only when it is equal, so
// it must name every excluded pin). A step with neither input is excluded
// (floating-toolchain reports it).
func toolchainPins(files []*workflow.File) (map[string][]toolchainPin, []Unavailable) {
	byTool := map[string][]toolchainPin{}
	var unknown []Unavailable
	for _, f := range files {
		for _, jid := range f.JobOrder {
			var exprTools []string
			exprs := map[string][]toolchainPin{}
			for _, step := range f.Jobs[jid].Steps {
				in, ok := setupActions[actionBase(step.Uses)]
				if !ok {
					continue
				}
				pin := toolchainPin{path: f.Path, action: actionBase(step.Uses), line: step.Line}
				if v := strings.TrimSpace(step.With[in.versionKey]); v != "" {
					pin.input, pin.value = in.versionKey, v
				} else if v := strings.TrimSpace(step.With[in.versionFile]); v != "" {
					pin.input, pin.value, pin.isFile = in.versionFile, v, true
				} else {
					continue
				}
				if strings.Contains(pin.value, "${{") {
					if exprs[in.tool] == nil {
						exprTools = append(exprTools, in.tool)
					}
					exprs[in.tool] = append(exprs[in.tool], pin)
					continue
				}
				byTool[in.tool] = append(byTool[in.tool], pin)
			}
			for _, tool := range exprTools {
				unknown = append(unknown, Unavailable{What: expressionPinWhat(f.Path, jid, tool), Why: expressionPinWhy(exprs[tool])})
			}
		}
	}
	return byTool, unknown
}

// expressionPinWhy lists a job's expression-valued pins for one tool. A
// single pin keeps the wording notes had before every pin was listed, so a
// recorded note for a one-pin job still equals a fresh one. Two or more pins
// quote each value, so a value containing the ", " separator cannot make a
// different list of pins read the same.
func expressionPinWhy(pins []toolchainPin) string {
	if len(pins) == 1 {
		return pins[0].input + "=" + pins[0].value + " is an expression; its value is unknown, so it is not compared for toolchain drift"
	}
	listed := make([]string, len(pins))
	for i, p := range pins {
		listed[i] = fmt.Sprintf("%s=%q", p.input, p.value)
	}
	return strings.Join(listed, ", ") + " are expressions; their values are unknown, so they are not compared for toolchain drift"
}

// toolchainUnavailable names each job's expression-valued toolchain pins.
func toolchainUnavailable(files []*workflow.File) []Unavailable {
	_, unknown := toolchainPins(files)
	return unknown
}

// ruleToolchainDrift groups setup steps by tool across all workflow files
// (release-only workflows included) and fires once per tool when two files
// declare different versions. Evidence cites every step whose pin differs
// from a pin in another file. The summary shows normalised values (no
// leading v), so it never depends on workflow order.
//
// Expression-valued pins are excluded from the comparison and named by
// toolchainUnavailable. Different versions inside a single file are not
// drift.
func ruleToolchainDrift(files []*workflow.File) []signal.Signal {
	byTool, _ := toolchainPins(files)

	tools := make([]string, 0, len(byTool))
	for tool := range byTool {
		tools = append(tools, tool)
	}
	sort.Strings(tools)

	var sigs []signal.Signal
	for _, tool := range tools {
		pins := byTool[tool]
		conflicting := make([]bool, len(pins))
		for i := range pins {
			for j := i + 1; j < len(pins); j++ {
				if pins[i].path != pins[j].path && pins[i].key() != pins[j].key() {
					conflicting[i], conflicting[j] = true, true
				}
			}
		}
		var ev []signal.Evidence
		var values []string
		seenValue := map[string]bool{}
		hasFile := false
		for i, p := range pins {
			if !conflicting[i] {
				continue
			}
			ev = append(ev, fileEv(p.path, p.line, fmt.Sprintf("%s %s=%s", p.action, p.input, p.value)))
			if !seenValue[p.key()] {
				seenValue[p.key()] = true
				shown := strings.TrimPrefix(p.value, "v")
				if p.isFile {
					shown = p.input + " " + p.value
				}
				values = append(values, shown)
			}
			hasFile = hasFile || p.isFile
		}
		if len(ev) == 0 {
			continue
		}
		sort.SliceStable(ev, func(i, j int) bool {
			if ev[i].Path != ev[j].Path {
				return ev[i].Path < ev[j].Path
			}
			return ev[i].Line < ev[j].Line
		})
		sort.Strings(values)
		summary := driftSummaryPrefix(tool) + strings.Join(values, ", ")
		if hasFile {
			summary += "; a version file is involved, so its contents may disagree with the other pins"
		}
		sigs = append(sigs, signal.New("toolchain-drift", signal.SeverityWarn, summary, ev))
	}
	return sigs
}

// ── emulation ─────────────────────────────────────────────────────────────────

// knownX86Runner returns true for runners that are definitively x86_64 (not arm).
func knownX86Runner(label string) bool {
	if strings.Contains(strings.ToLower(label), "arm") {
		return false
	}
	return strings.HasPrefix(label, "ubuntu-") || strings.HasPrefix(label, "windows-")
}

// nonAMD64Platform returns true for --platform values that are not amd64/x86_64.
var nonAMD64 = regexp.MustCompile(`(?i)linux/(arm|386|riscv|ppc|s390|mips)`)

// ruleEmulation checks for docker/setup-qemu-action and for --platform flags
// on known x86_64 runners that include non-native target platforms.
// QEMU must not invent an arm64 runner mapping: only known-x86 runners are
// tested against the --platform flag.
func ruleEmulation(files []*workflow.File) []signal.Signal {
	var sigs []signal.Signal
	for _, f := range files {
		for _, jid := range f.JobOrder {
			job := f.Jobs[jid]
			labels := parseRunsOn(job.RunsOn)
			isX86 := false
			for _, l := range labels {
				if knownX86Runner(l) {
					isX86 = true
					break
				}
			}
			for _, step := range job.Steps {
				if step.Uses != "" {
					base := actionBase(step.Uses)
					if base == "docker/setup-qemu-action" {
						sigs = append(sigs, signal.New("emulation", signal.SeverityWarn,
							fmt.Sprintf("QEMU emulation set up in job %s", job.ID),
							[]signal.Evidence{fileEv(f.Path, step.Line, step.Uses)}))
					}
				}
				if step.Run != "" && isX86 {
					for _, line := range strings.Split(step.Run, "\n") {
						if m := platformFlag(line); m != "" && nonAMD64.MatchString(m) {
							sigs = append(sigs, signal.New("emulation", signal.SeverityWarn,
								fmt.Sprintf("--platform %s on x86_64 runner requires emulation in job %s", m, job.ID),
								[]signal.Evidence{fileEv(f.Path, step.Line, fmt.Sprintf("--platform %s", m))}))
							break
						}
					}
				}
			}
		}
	}
	return sigs
}

// platformFlag extracts the value after --platform or --platform= from a shell
// line. Returns "" if none.
var platformRE = regexp.MustCompile(`--platform(?:=|\s+)(\S+)`)

func platformFlag(line string) string {
	m := platformRE.FindStringSubmatch(line)
	if len(m) > 1 {
		return m[1]
	}
	return ""
}

// ── uncached-install ──────────────────────────────────────────────────────────

// installPatterns maps a detector key to a pattern that finds the install
// command in a run script.
type installDetector struct {
	pattern *regexp.Regexp
	manager string
}

var installDetectors = []installDetector{
	{regexp.MustCompile(`\bnpm\s+(?:ci|install)\b`), "npm"},
	{regexp.MustCompile(`\bpip\s+install\b`), "pip"},
	{regexp.MustCompile(`\buv\s+sync\b`), "uv"},
	{regexp.MustCompile(`\bgo\s+mod\s+download\b`), "go"},
	{regexp.MustCompile(`\bpoetry\s+install\b`), "poetry"},
}

// ruleUncachedInstall checks jobs for package-manager install commands that
// have no corresponding cache mechanism.
func ruleUncachedInstall(files []*workflow.File) []signal.Signal {
	var sigs []signal.Signal
	for _, f := range files {
		for _, jid := range f.JobOrder {
			job := f.Jobs[jid]
			if len(job.Steps) == 0 {
				continue
			}
			ci := jobCacheInfo(job)
			for _, step := range job.Steps {
				if step.Run == "" {
					continue
				}
				for _, det := range installDetectors {
					if !det.pattern.MatchString(step.Run) {
						continue
					}
					if ci.coversManager(det.manager) {
						continue
					}
					cmd := det.pattern.FindString(step.Run)
					sigs = append(sigs, signal.New("uncached-install", signal.SeverityWarn,
						uncachedSummary(job.ID, cmd),
						[]signal.Evidence{fileEv(f.Path, step.Line, cmd)}))
				}
			}
		}
	}
	return sigs
}

// cacheInfo records proven manager coverage and explicitly unknown coverage.
type cacheInfo struct {
	covered map[string]bool
	unknown map[string]bool
}

func jobCacheInfo(job workflow.Job) cacheInfo {
	ci := cacheInfo{covered: map[string]bool{}, unknown: map[string]bool{}}
	for _, step := range job.Steps {
		switch actionBase(step.Uses) {
		case "actions/cache", "actions/cache/restore":
			paths := step.With["path"]
			unresolved := false
			for _, path := range strings.Split(paths, "\n") {
				path = strings.TrimSpace(path)
				// A trailing newline is not an additional cache path.
				if path == "" && strings.TrimSpace(paths) != "" {
					continue
				}
				known := false
				if !strings.Contains(path, "${{") {
					for mgr, suffix := range map[string]string{"npm": "/.npm", "pip": "/.cache/pip", "uv": "/.cache/uv", "poetry": "/.cache/pypoetry", "go": "/go/pkg/mod"} {
						if strings.HasSuffix(path, suffix) {
							ci.covered[mgr] = true
							known = true
						}
					}
				}
				unresolved = unresolved || !known
			}
			if unresolved {
				for _, mgr := range []string{"npm", "pip", "uv", "poetry", "go"} {
					ci.unknown[mgr] = true
				}
			}
		case "actions/setup-node":
			value := step.With["cache"]
			if value == "npm" {
				ci.covered["npm"] = true
			} else if strings.Contains(value, "${{") {
				ci.unknown["npm"] = true
			}
		case "actions/setup-python":
			value := step.With["cache"]
			if value == "pip" || value == "poetry" {
				ci.covered[value] = true
			} else if strings.Contains(value, "${{") {
				ci.unknown["pip"] = true
				ci.unknown["poetry"] = true
			}
		case "actions/setup-go":
			value := step.With["cache"]
			if value == "" || value == "true" {
				ci.covered["go"] = true
			} else if !isFalsy(value) {
				ci.unknown["go"] = true
			}
		case "actions/setup-uv", "astral-sh/setup-uv":
			value := step.With["enable-cache"]
			switch value {
			case "true":
				ci.covered["uv"] = true
			case "false":
			case "", "auto":
				// setup-uv v5 auto tests RUNNER_ENVIRONMENT, not lockfile presence.
				switch job.RunsOn {
				case "ubuntu-latest", "ubuntu-24.04", "ubuntu-22.04", "ubuntu-24.04-arm", "windows-latest", "windows-2022", "windows-2025", "macos-latest", "macos-14", "macos-15", "macos-13":
					ci.covered["uv"] = true
				default:
					ci.unknown["uv"] = true
				}
			default:
				ci.unknown["uv"] = true
			}
		}
	}
	return ci
}
func (ci cacheInfo) coversManager(mgr string) bool { return ci.covered[mgr] || ci.unknown[mgr] }

func cacheUnavailable(files []*workflow.File) []Unavailable {
	var out []Unavailable
	for _, f := range files {
		for _, jid := range f.JobOrder {
			job := f.Jobs[jid]
			ci := jobCacheInfo(job)
			seen := map[string]bool{}
			for _, step := range job.Steps {
				for _, det := range installDetectors {
					if det.pattern.MatchString(step.Run) && ci.unknown[det.manager] && !ci.covered[det.manager] && !seen[det.manager] {
						seen[det.manager] = true
						out = append(out, Unavailable{What: unknownCacheWhat(CacheSite{f.Path, jid, det.manager}), Why: "cache path/input or runner environment is unknown; no uncached-install claim"})
					}
				}
			}
		}
	}
	return out
}

// ── serial-independent-jobs ───────────────────────────────────────────────────

// ruleSerialIndependentJobs finds jobs that need another job but do not
// reference its outputs anywhere (run, if, environment, with values).
// if: and environment: references are explicitly searched, not discarded.
func ruleSerialIndependentJobs(files []*workflow.File) []signal.Signal {
	var sigs []signal.Signal
	for _, f := range files {
		for _, downID := range f.JobOrder {
			down := f.Jobs[downID]
			if len(down.Steps) == 0 {
				continue
			}
			for _, upID := range down.Needs {
				up, ok := f.Jobs[upID]
				if !ok || len(up.Steps) == 0 {
					continue
				}
				if !jobReferencesOutputs(down, upID) {
					sigs = append(sigs, signal.New("serial-independent-jobs", signal.SeverityInfo,
						fmt.Sprintf("job %s needs %s but does not reference its outputs (candidate for parallelism — review before removing security gates)", downID, upID),
						[]signal.Evidence{jobEv(f.Path, down.Line, fmt.Sprintf("%s needs %s", downID, upID))}))
				}
			}
		}
	}
	return sigs
}

// jobReferencesOutputs returns true if job j references needs.upID.outputs.
// anywhere in its if condition, environment, steps' run/if/with, or any
// string field. Expression references in if: and environment: are included.
func jobReferencesOutputs(j workflow.Job, upID string) bool {
	needle := "needs." + upID + ".outputs."
	// Job-level fields.
	if strings.Contains(j.If, needle) {
		return true
	}
	if strings.Contains(j.Environment, needle) {
		return true
	}
	for _, step := range j.Steps {
		if strings.Contains(step.Run, needle) {
			return true
		}
		if strings.Contains(step.If, needle) {
			return true
		}
		if strings.Contains(step.Uses, needle) {
			return true
		}
		for _, v := range step.With {
			if strings.Contains(v, needle) {
				return true
			}
		}
	}
	return false
}

// ── duplicate-check-set ───────────────────────────────────────────────────────

// ruleduplicateCheckSet compares jobs across workflows that share a branch
// trigger (push or pull_request). Jobs with identical ordered run-command
// lists (trimmed) are a duplicate check set. Conservative glob handling:
// only literal branch names are used for overlap detection.
func ruleDuplicateCheckSet(files []*workflow.File) []signal.Signal {
	if len(files) < 2 {
		return nil
	}

	type jobSig struct {
		wfPath  string
		jobID   string
		wfLine  int
		jobLine int
		cmds    string // canonical run-command signature
	}

	var candidates []jobSig
	for _, f := range files {
		for _, jid := range f.JobOrder {
			job := f.Jobs[jid]
			cmds := runSignature(job)
			if cmds == "" {
				continue
			}
			// Only include if the workflow has push or pull_request trigger.
			if !hasPushOrPR(f) {
				continue
			}
			candidates = append(candidates, jobSig{
				wfPath:  f.Path,
				jobID:   jid,
				wfLine:  f.OnLine,
				jobLine: job.Line,
				cmds:    cmds,
			})
		}
	}

	var sigs []signal.Signal
	seen := map[string]bool{} // avoid duplicate pair signals

	for i := 0; i < len(candidates); i++ {
		for j := i + 1; j < len(candidates); j++ {
			a, b := candidates[i], candidates[j]
			if a.wfPath == b.wfPath {
				continue
			}
			if a.cmds != b.cmds {
				continue
			}
			aFile := findFile(files, a.wfPath)
			bFile := findFile(files, b.wfPath)
			if aFile == nil || bFile == nil {
				continue
			}
			if !workflowsShareBranch(aFile, bFile) {
				continue
			}
			pairKey := a.cmds + "|" + a.wfPath + "|" + a.jobID + "|" + b.wfPath + "|" + b.jobID
			if seen[pairKey] {
				continue
			}
			seen[pairKey] = true
			sigs = append(sigs, signal.New("duplicate-check-set", signal.SeverityInfo,
				fmt.Sprintf("jobs %s(%s) and %s(%s) run identical check commands on the same branch",
					a.jobID, a.wfPath, b.jobID, b.wfPath),
				[]signal.Evidence{
					fileEv(a.wfPath, a.jobLine, fmt.Sprintf("%s: duplicate run set", a.jobID)),
					fileEv(b.wfPath, b.jobLine, fmt.Sprintf("%s: duplicate run set", b.jobID)),
				}))
		}
	}
	return sigs
}

// runSignature returns a canonical string representing a job's ordered,
// trimmed run: commands. Empty if no run steps.
func runSignature(job workflow.Job) string {
	var cmds []string
	for _, step := range job.Steps {
		if step.Run != "" {
			cmds = append(cmds, strings.TrimSpace(step.Run))
		}
	}
	if len(cmds) == 0 {
		return ""
	}
	return strings.Join(cmds, "\x00")
}

func findFile(files []*workflow.File, path string) *workflow.File {
	for _, f := range files {
		if f.Path == path {
			return f
		}
	}
	return nil
}

// workflowsShareBranch returns true if the two workflows both trigger on push
// or pull_request on at least one common branch. Conservative: only literal
// branch names are compared (no glob expansion). If either workflow has no
// branch filter for a trigger, it matches all branches.
func workflowsShareBranch(a, b *workflow.File) bool {
	for _, event := range []string{"push", "pull_request"} {
		aV, aOK := a.On[event]
		bV, bOK := b.On[event]
		if !aOK || !bOK {
			continue
		}
		aAll, aLits := triggerBranches(aV)
		bAll, bLits := triggerBranches(bV)
		if branchesOverlap(aAll, aLits, bAll, bLits) {
			return true
		}
	}
	return false
}

// triggerBranches extracts literal branch names from a trigger configuration.
// allBranches is true when there is no branches filter at all (matches every
// branch). Glob-only entries are dropped from literals; their coverage is
// unknown so they are treated conservatively (not counted as overlap).
func triggerBranches(v any) (allBranches bool, literals []string) {
	m, ok := v.(map[string]any)
	if !ok {
		// Scalar nil or unknown config → all branches.
		return true, nil
	}
	if _, ignored := m["branches-ignore"]; ignored {
		return false, nil // complement coverage is unproven
	}
	raw, ok := m["branches"]
	if !ok {
		return true, nil // no branches key → all branches
	}
	list, ok := raw.([]any)
	if !ok {
		return false, nil
	}
	for _, item := range list {
		s, ok := item.(string)
		if !ok {
			continue
		}
		// Conservative: skip glob patterns; their coverage is unknown.
		if strings.ContainsAny(s, "*?[") {
			continue
		}
		literals = append(literals, s)
	}
	return false, literals
}

// branchesOverlap returns true when two trigger branch sets share at least one
// common reachable branch.
//
//   - allBranches=true means no filter (any branch triggers).
//   - literals holds the literal (non-glob) branch names.
//   - Conservative: a side with only globs (allBranches=false, len(literals)==0)
//     has unknown coverage and is treated as non-overlapping.
func branchesOverlap(aAll bool, aLits []string, bAll bool, bLits []string) bool {
	switch {
	case aAll && bAll:
		return true // both match all branches
	case aAll:
		// a matches all; overlap exists only if b has known literal branches.
		return len(bLits) > 0
	case bAll:
		return len(aLits) > 0
	default:
		// Neither matches all; need literal intersection.
		if len(aLits) == 0 || len(bLits) == 0 {
			// One side has only globs — conservative: no overlap.
			return false
		}
		set := make(map[string]bool, len(aLits))
		for _, s := range aLits {
			set[s] = true
		}
		for _, s := range bLits {
			if set[s] {
				return true
			}
		}
		return false
	}
}

// ── no-concurrency ────────────────────────────────────────────────────────────

// ruleNoConcurrency flags workflows triggered by push or pull_request that
// have no top-level concurrency and no job-level concurrency on any job.
func ruleNoConcurrency(files []*workflow.File) []signal.Signal {
	var sigs []signal.Signal
	for _, f := range files {
		if !hasPushOrPR(f) {
			continue
		}
		if f.Concurrency != nil {
			continue
		}
		// Check if any job has concurrency.
		anyJobConcurrency := false
		for _, jid := range f.JobOrder {
			if f.Jobs[jid].Concurrency != nil {
				anyJobConcurrency = true
				break
			}
		}
		if anyJobConcurrency {
			continue
		}
		sigs = append(sigs, signal.New("no-concurrency", signal.SeverityWarn,
			fmt.Sprintf("%s triggers on push/pull_request but has no concurrency control", f.Path),
			[]signal.Evidence{fileEv(f.Path, f.OnLine, "push/pull_request trigger without concurrency")}))
	}
	return sigs
}

// ── no-path-filter ────────────────────────────────────────────────────────────

// ruleNoPathFilter flags workflows with push/pull_request triggers that lack
// paths/paths-ignore filters and whose p50 duration is at least 5 min.
// Workflows absent from durations receive no signal (see Skipped).
func ruleNoPathFilter(files []*workflow.File, durations map[string]time.Duration) []signal.Signal {
	var sigs []signal.Signal
	for _, f := range files {
		if !hasPushOrPR(f) {
			continue
		}
		if hasPathFilter(f) {
			continue
		}
		d, ok := durations[f.Path]
		if !ok {
			// Absent → skipped, not green.
			continue
		}
		if d < minPathFilterDuration {
			continue
		}
		sigs = append(sigs, signal.New("no-path-filter", signal.SeverityWarn,
			fmt.Sprintf("%s triggers on push/pull_request without paths filter; p50=%s (≥5 min)", f.Path, d.Round(time.Second)),
			[]signal.Evidence{fileEv(f.Path, f.OnLine, fmt.Sprintf("push/pull_request without paths filter; p50=%s", d.Round(time.Second)))}))
	}
	return sigs
}
