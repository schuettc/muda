package record

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/schuettc/muda/internal/scan"
)

// BaselineSet is one `record baseline` run. A nil field was not supplied;
// Measure is required. Everything is validated before the first write.
// Ref, when set, declares a historical baseline: the gate inventory and scan
// were read at Ref, the closed window's last commit (see Historical).
type BaselineSet struct {
	Measure, Settings, Gates, Scan []byte
	Ref                            string
}

// Historical marks a baseline whose gate inventory and scan were read at a
// closed window's last commit rather than at the default branch. Repository
// settings (branch protection, rulesets, environments) have no history on
// GitHub: they were read on SettingsRead and are current, which Settings
// says in words.
type Historical struct {
	Ref          string `json:"ref"`
	SettingsRead string `json:"settings-read"`
	Settings     string `json:"settings"`
}

// SettingsLabel is how a historical baseline's repository settings are named.
func SettingsLabel(date string) string { return "settings read " + date + ", not historical" }

// commitRef is a full commit SHA (SHA-1 or SHA-256): a historical ref must not
// move as a branch does, and muda gates and scan echo --ref verbatim, so an
// abbreviation would be recorded as given.
var commitRef = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

// HistoricalOf validates gates.toml's historical-ref and settings-read pair:
// both absent (a current or older record) gives nil; both present gives the
// marker; anything else is invalid-historical.
func HistoricalOf(ref, settingsRead string) (*Historical, error) {
	if ref == "" && settingsRead == "" {
		return nil, nil
	}
	if _, err := time.Parse("2006-01-02", settingsRead); err != nil || !commitRef.MatchString(ref) {
		return nil, problem("invalid-historical", gatesKind.file, "historical-ref (a full commit SHA) and settings-read (YYYY-MM-DD) must be set together")
	}
	return &Historical{Ref: ref, SettingsRead: settingsRead, Settings: SettingsLabel(settingsRead)}, nil
}

// windowClosed reports whether muda.toml's window ended at or before the start
// of the current UTC day. A report's until is never after the moment measure
// ran, so "before now" would hold for every window, including today's. As in
// windowMatch, a date-only until is that day's 00:00Z and RFC3339 is exact.
func windowClosed(w Window) bool {
	until, err := dateTime(w.Until)
	return err == nil && !until.After(today())
}

// openWindowProblem refuses a historical ref while the window is still open:
// its last commit is not yet known, so workflows are read at the branch.
func openWindowProblem(w Window, hist string) error {
	if hist == "" || windowClosed(w) {
		return nil
	}
	return problem("ref-mismatch", gatesKind.file, fmt.Sprintf("records workflows read at %q as historical, but muda.toml's window is still open (until %s is after the start of today, %s); a baseline of an open window records muda gates and muda scan without --ref", hist, w.Until, today().Format(time.RFC3339)))
}

// reportKind decides a report's type by its top-level keys.
type reportKind struct {
	name, file, flag string
	keys             [2]string
}

var (
	measureKind  = reportKind{"measure", "baseline.json", "--file", [2]string{"workflows", "since"}}
	gatesKind    = reportKind{"gates", "gates.toml", "--gates", [2]string{"gates", "security"}}
	scanKind     = reportKind{"scan", "scan.json", "--scan", [2]string{"signals", "files"}}
	settingsKind = reportKind{"settings", "muda.toml", "--settings", [2]string{"roles", "window"}}
)

func (k reportKind) has(keys map[string]json.RawMessage) bool {
	_, a := keys[k.keys[0]]
	_, b := keys[k.keys[1]]
	return a && b
}

func (k reportKind) wrong() error {
	return problem("wrong-report-type", k.file, fmt.Sprintf("%s is not a muda %s report: it needs top-level %q and %q", k.flag, k.name, k.keys[0], k.keys[1]))
}

func topKeys(b []byte, file string) (map[string]json.RawMessage, error) {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(b, &keys); err != nil {
		return nil, problem("invalid-json", file, err.Error())
	}
	return keys, nil
}

// typedReport validates a schema-1 report and requires its kind's shape.
func typedReport(b []byte, k reportKind) (map[string]json.RawMessage, error) {
	if err := validateJSON(b, k.file); err != nil {
		return nil, err
	}
	keys, err := topKeys(b, k.file)
	if err != nil {
		return nil, err
	}
	if !k.has(keys) {
		return nil, k.wrong()
	}
	return keys, nil
}

// strictJSON decodes exactly one object, rejecting unknown fields.
func strictJSON(b []byte, file, flag string, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return problem("invalid-json", file, flag+err.Error())
	}
	var extra any
	if err := d.Decode(&extra); !errors.Is(err, io.EOF) {
		return problem("invalid-json", file, flag+"expected one JSON object")
	}
	return nil
}

// validScan applies the scan shape and strict decoding to scan.json bytes.
func validScan(b []byte, flag string) error {
	if _, err := typedReport(b, reportKind{scanKind.name, scanKind.file, flag, scanKind.keys}); err != nil {
		return err
	}
	prefix := ""
	if flag != scanKind.file {
		prefix = flag + ": "
	}
	var r scan.Report
	return strictJSON(b, scanKind.file, prefix, &r)
}

func stringKey(keys map[string]json.RawMessage, key string) (string, bool) {
	raw, ok := keys[key]
	if !ok {
		return "", false
	}
	var v string
	if json.Unmarshal(raw, &v) != nil {
		return "", false
	}
	return v, true
}

func repoProblem(file, got, want string) error {
	if got == want {
		return nil
	}
	return problem("repo-mismatch", file, fmt.Sprintf("names repo %q; the baseline names %q", got, want))
}

// refProblems reports a baseline gate inventory that read workflows at a ref
// other than its own default branch, and a scan that read another ref than
// that branch. Either would make every later compare diff against branch
// workflows. A record without ref or branch (older records) has nothing to
// compare. A historical baseline (hist set) instead requires both reports to
// name hist exactly.
func refProblems(gates, scanKeys map[string]json.RawMessage, hist string) (gatesErr, scanErr error) {
	if hist != "" {
		if ref, _ := stringKey(gates, "ref"); gates != nil && ref != hist {
			gatesErr = problem("ref-mismatch", gatesKind.file, fmt.Sprintf("reads workflows at ref %q, not the baseline's historical ref %q; record muda gates --ref %s", ref, hist, hist))
		}
		if ref, _ := stringKey(scanKeys, "ref"); scanKeys != nil && ref != hist {
			scanErr = problem("ref-mismatch", scanKind.file, fmt.Sprintf("scans ref %q, not the baseline's historical ref %q; record muda scan --ref %s", ref, hist, hist))
		}
		return gatesErr, scanErr
	}
	branch, ok := stringKey(gates, "branch")
	if !ok || branch == "" {
		return nil, nil
	}
	if ref, ok := stringKey(gates, "ref"); ok && ref != "" && ref != branch {
		gatesErr = problem("ref-mismatch", gatesKind.file, fmt.Sprintf("reads workflows at ref %q, not its default branch %q; a baseline records muda gates without --ref", ref, branch))
	}
	if ref, ok := stringKey(scanKeys, "ref"); ok && ref != "" && ref != branch {
		scanErr = problem("ref-mismatch", scanKind.file, fmt.Sprintf("scans ref %q, not the gate inventory's default branch %q; a baseline records muda scan without --ref", ref, branch))
	}
	return gatesErr, scanErr
}

// windowMatch compares a muda.toml window value with a report's: a date-only
// setting matches the report's UTC date; an RFC3339 setting is an exact instant.
func windowMatch(setting, report string) bool {
	rt, err := dateTime(report)
	if err != nil {
		return false
	}
	if _, err := time.Parse("2006-01-02", setting); err == nil {
		return rt.UTC().Format("2006-01-02") == setting
	}
	st, err := time.Parse(time.RFC3339, setting)
	return err == nil && st.Equal(rt)
}

// windowProblem compares the baseline's since/until with muda.toml's window.
// A baseline without both (the init placeholder) has no window to compare.
func windowProblem(settings Settings, keys map[string]json.RawMessage, hint string) error {
	since, ok1 := stringKey(keys, "since")
	until, ok2 := stringKey(keys, "until")
	if !ok1 || !ok2 {
		return nil
	}
	if windowMatch(settings.Window.Since, since) && windowMatch(settings.Window.Until, until) {
		return nil
	}
	return problem("window-mismatch", "baseline.json", fmt.Sprintf("baseline window %s..%s differs from muda.toml window %s..%s%s", since, until, settings.Window.Since, settings.Window.Until, hint))
}

// WriteBaselineSet records a measure report and, optionally, new settings, a
// gate inventory and a scan report together. Every input and the cross-file
// rules are validated first; then, under one lock, scan.json, gates.toml,
// baseline.json and muda.toml are written in that order. muda.toml goes last
// so a crash part-way leaves a window-mismatch for record check to flag.
func (s *Store) WriteBaselineSet(in BaselineSet) (err error) {
	if in.Measure == nil {
		return problem("missing-baseline", "baseline.json", "a measure report is required")
	}
	s, release, err := s.mutation()
	if err != nil {
		return err
	}
	defer func() {
		if releaseErr := release(); err == nil {
			err = releaseErr
		}
	}()
	measureKeys, err := typedReport(in.Measure, measureKind)
	if err != nil {
		return err
	}
	for _, key := range []string{"repo", "since", "until"} {
		if v, ok := stringKey(measureKeys, key); !ok || v == "" {
			return problem("invalid-json", measureKind.file, "--file: a muda measure report needs a non-empty string "+key)
		}
	}
	repo, _ := stringKey(measureKeys, "repo")
	var stored Settings
	if err = s.load("muda.toml", &stored); err != nil {
		return err
	}
	old, err := s.read("baseline.json")
	if err != nil {
		return err
	}
	if err = validateJSON(old, "baseline.json"); err != nil {
		return err
	}
	var writes []setFile
	settings := stored
	if in.Settings != nil {
		if settings, err = decodeSettings(in.Settings); err != nil {
			return err
		}
	}
	if err = windowProblem(settings, measureKeys, windowHint); err != nil {
		return err
	}
	if in.Scan != nil {
		if err = validScan(in.Scan, scanKind.flag); err != nil {
			return err
		}
		keys, _ := topKeys(in.Scan, scanKind.file)
		got, _ := stringKey(keys, "repo")
		if err = repoProblem(scanKind.file, got, repo); err != nil {
			return err
		}
		if err = s.existingScan(); err != nil {
			return err
		}
		writes = append(writes, setFile{scanKind.file, in.Scan})
	}
	if in.Gates != nil {
		keys, err := typedReport(in.Gates, gatesKind)
		if err != nil {
			return err
		}
		got, _ := stringKey(keys, "repo")
		if err = repoProblem(gatesKind.file, got, repo); err != nil {
			return err
		}
		if _, err = s.GatesJSON(); err != nil {
			return err
		}
		doc := gatesFile{Schema: Schema, InventoryJSON: string(in.Gates)}
		if in.Ref != "" {
			doc.HistoricalRef, doc.SettingsRead = in.Ref, today().Format("2006-01-02")
		}
		b, err := encode(doc)
		if err != nil {
			return err
		}
		writes = append(writes, setFile{gatesKind.file, b})
	}
	if in.Ref != "" && !commitRef.MatchString(in.Ref) {
		return problem("invalid-ref", gatesKind.file, fmt.Sprintf("--ref %q is not a full 40- or 64-hex commit SHA (git rev-parse prints one); a historical baseline reads gates and scan at the window's last commit", in.Ref))
	}
	// hist is the record's historical ref once this run is written: --ref
	// with new gates, else what the stored gate inventory records.
	var gk, sk map[string]json.RawMessage
	hist := in.Ref
	if in.Gates != nil {
		gk, _ = topKeys(in.Gates, gatesKind.file)
	} else if stored, err := s.loadGates(); err == nil {
		gk, _ = topKeys([]byte(stored.InventoryJSON), gatesKind.file)
		if in.Ref != "" && in.Ref != stored.HistoricalRef {
			return problem("ref-mismatch", gatesKind.file, fmt.Sprintf("--ref %q, but the recorded gate inventory is not historical at that ref; pass --gates read with muda gates --ref %s", in.Ref, in.Ref))
		}
		hist = stored.HistoricalRef
	} else if in.Ref != "" {
		return err
	}
	if in.Gates != nil || in.Scan != nil {
		if in.Scan != nil {
			sk, _ = topKeys(in.Scan, scanKind.file)
		}
		gatesErr, scanErr := refProblems(gk, sk, hist)
		if in.Gates != nil && gatesErr != nil {
			return gatesErr
		}
		if scanErr != nil {
			return scanErr
		}
	}
	if err = openWindowProblem(settings.Window, hist); err != nil {
		return err
	}
	writes = append(writes, setFile{measureKind.file, in.Measure})
	if in.Settings != nil {
		b, err := encode(settings)
		if err != nil {
			return err
		}
		writes = append(writes, setFile{settingsKind.file, b})
	}
	for _, f := range writes {
		if err = noSecrets(f.data); err != nil {
			return err
		}
	}
	// An unfinished set is only superseded by a run covering all its files.
	if err = s.supersedesPending(writes); err != nil {
		return err
	}
	// The marker records what the set intends before the first file changes.
	// It is removed only after every write succeeds, so a crash or a returned
	// error part-way leaves it for record check (incomplete-baseline-set).
	marker := pendingSet{Schema: Schema, Files: map[string]string{}}
	for _, f := range writes {
		marker.Files[f.name] = digest(f.data)
	}
	b, err := json.Marshal(marker)
	if err != nil {
		return err
	}
	if err = s.write(pendingName, append(b, '\n')); err != nil {
		return err
	}
	for _, f := range writes {
		if err = s.write(f.name, f.data); err != nil {
			return err
		}
		if writeHook != nil {
			if err = writeHook(f.name); err != nil {
				return err
			}
		}
	}
	return s.remove(pendingName)
}

const windowHint = "; muda.toml's window must match the report; pass --settings carrying the report's window"

// pendingName marks a record baseline run that has not finished writing.
const pendingName = ".baseline-set-pending"

// writeHook is a test-only fault-injection seam, consulted after each write.
var writeHook func(name string) error

type pendingSet struct {
	Schema int               `json:"schema"`
	Files  map[string]string `json:"files"`
}

func digest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (s *Store) remove(name string) error {
	r, err := s.root()
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	if err = safe(r, name); err != nil {
		return err
	}
	return r.Remove(name)
}

const rerun = "re-run the same record baseline command"

// refusePending stops single-file baseline and gates writes, and show, from
// building on a partly written set. An invalid or newer-schema marker gets the
// same remove-by-hand recovery as the set writer gives, since a re-run would
// itself be refused.
func (s *Store) refusePending() error {
	_, found, err := s.loadPending()
	if !found {
		return nil
	}
	if err != nil {
		return unusableMarker(err)
	}
	return problem("incomplete-baseline-set", pendingName, "a record baseline run did not finish; "+rerun+" first")
}

// unusableMarker is the refusal for a marker loadPending rejected.
func unusableMarker(err error) error {
	detail := "a record baseline run did not finish, and its marker is an invalid marker"
	var p Problem
	if errors.As(err, &p) && p.Code == "newer-schema" {
		detail = "a record baseline run did not finish, and its marker has a newer schema"
	}
	return problem("incomplete-baseline-set", pendingName, detail+"; "+removeByHand)
}

// pendingProblem reports an unfinished set, naming the files whose current
// hash differs from the intended one. Names and hashes only, never content.
func (s *Store) pendingProblem() error {
	marker, found, err := s.loadPending()
	if !found {
		return nil
	}
	if err != nil {
		return err
	}
	names := make([]string, 0, len(marker.Files))
	for name := range marker.Files {
		names = append(names, name)
	}
	sort.Strings(names)
	var unapplied []string
	for _, name := range names {
		current := "absent"
		if data, err := s.read(name); err == nil {
			current = "sha256:" + digest(data)
		} else if !errors.Is(err, os.ErrNotExist) {
			current = "unreadable"
		}
		if current != "sha256:"+marker.Files[name] {
			unapplied = append(unapplied, fmt.Sprintf("%s (intended sha256:%s, current %s)", name, marker.Files[name], current))
		}
	}
	if len(unapplied) == 0 {
		return problem("incomplete-baseline-set", pendingName, "a record baseline run wrote every file but did not finish; "+rerun)
	}
	return problem("incomplete-baseline-set", pendingName, "a record baseline run did not finish; not yet applied: "+strings.Join(unapplied, ", ")+"; "+rerun)
}

type setFile struct {
	name string
	data []byte
}

// setFlag names the record baseline input that writes each set file.
var setFlag = map[string]string{"scan.json": "--scan", "gates.toml": "--gates", "baseline.json": "--file", "muda.toml": "--settings"}

var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

const removeByHand = "remove .muda/" + pendingName + " by hand through a reviewed record PR"

// loadPending reads and validates the marker. found reports whether one
// exists. A newer schema is newer-schema; any other invalid marker is
// "invalid marker" and nothing from it is echoed.
func (s *Store) loadPending() (pendingSet, bool, error) {
	var marker pendingSet
	b, err := s.read(pendingName)
	if errors.Is(err, os.ErrNotExist) {
		return marker, false, nil
	}
	invalid := problem("incomplete-baseline-set", pendingName, "a record baseline run did not finish, and its marker is an invalid marker; "+removeByHand)
	if err != nil || noSecrets(b) != nil {
		return marker, true, invalid
	}
	var header struct {
		Schema int `json:"schema"`
	}
	if json.Unmarshal(b, &header) != nil {
		return marker, true, invalid
	}
	if err := schemaCheck(header.Schema, pendingName); err != nil {
		var p Problem
		if errors.As(err, &p) && p.Code == "newer-schema" {
			return marker, true, err
		}
		return marker, true, invalid
	}
	if strictJSON(b, pendingName, "", &marker) != nil || len(marker.Files) == 0 {
		return marker, true, invalid
	}
	for name, hash := range marker.Files {
		if _, ok := setFlag[name]; !ok || !sha256Hex.MatchString(hash) {
			return marker, true, invalid
		}
	}
	return marker, true, nil
}

// supersedesPending lets a run replace an unfinished set's marker only when
// it writes every file that set intended; otherwise a narrower run would
// clear the evidence of a mixed record.
func (s *Store) supersedesPending(writes []setFile) error {
	marker, found, err := s.loadPending()
	if !found {
		return nil
	}
	if err != nil {
		return unusableMarker(err)
	}
	covered := map[string]bool{}
	for _, f := range writes {
		covered[f.name] = true
	}
	var missing []string
	for name := range marker.Files {
		if !covered[name] {
			missing = append(missing, setFlag[name])
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return problem("incomplete-baseline-set", pendingName, "a record baseline run did not finish; this run lacks "+strings.Join(missing, ", ")+" that it included; "+rerun)
}

// decodeSettings validates --settings exactly as init does.
func decodeSettings(b []byte) (Settings, error) {
	var v Settings
	keys, err := topKeys(b, settingsKind.file)
	if err != nil {
		return v, err
	}
	if !settingsKind.has(keys) {
		return v, settingsKind.wrong()
	}
	if err = strictJSON(b, settingsKind.file, "--settings: ", &v); err != nil {
		return v, err
	}
	if v.Schema == 0 {
		v.Schema = Schema
	}
	return v, validateSettings(v)
}

// existingScan refuses to replace an unreadable or newer-schema scan.json.
func (s *Store) existingScan() error {
	b, err := s.read(scanKind.file)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return validateJSON(b, scanKind.file)
}

// readScan returns stored scan.json, or nil when the record has none.
func (s *Store) readScan() ([]byte, error) {
	b, err := s.read(scanKind.file)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return b, validScan(b, scanKind.file)
}

// checkBaselineSet applies the write-time rules to what is on disk. Only real
// inconsistencies are problems: the init placeholders and older gate records
// without a security key or repo stay clean; a stored report is the wrong type
// only when it has another report's shape.
func checkBaselineSet(add func(string, error), settings *Settings, baseline, gatesJSON, scanJSON []byte, hist string) {
	var bk, gk, sk map[string]json.RawMessage
	if baseline != nil {
		bk, _ = topKeys(baseline, measureKind.file)
	}
	if gatesJSON != nil {
		gk, _ = topKeys(gatesJSON, gatesKind.file)
	}
	if scanJSON != nil {
		sk, _ = topKeys(scanJSON, scanKind.file)
	}
	crossType := func(keys map[string]json.RawMessage, own reportKind, file string) bool {
		if own.has(keys) {
			return false
		}
		for _, k := range []reportKind{measureKind, gatesKind, scanKind} {
			if k != own && k.has(keys) {
				add(file, problem("wrong-report-type", file, fmt.Sprintf("holds a muda %s report, not %s", k.name, own.name)))
				return true
			}
		}
		return false
	}
	gatesWrong := gk != nil && crossType(gk, gatesKind, gatesKind.file)
	if gk != nil && !gatesWrong {
		gatesErr, scanErr := refProblems(gk, sk, hist)
		add(gatesKind.file, gatesErr)
		add(scanKind.file, scanErr)
	}
	if settings != nil {
		add(gatesKind.file, openWindowProblem(settings.Window, hist))
	}
	if bk == nil || crossType(bk, measureKind, measureKind.file) || !measureKind.has(bk) {
		return
	}
	if settings != nil {
		add(measureKind.file, windowProblem(*settings, bk, ""))
	}
	repo, ok := stringKey(bk, "repo")
	if !ok {
		return
	}
	if gk != nil && !gatesWrong {
		if got, ok := stringKey(gk, "repo"); ok {
			add(gatesKind.file, repoProblem(gatesKind.file, got, repo))
		}
	}
	if sk != nil {
		got, _ := stringKey(sk, "repo")
		add(scanKind.file, repoProblem(scanKind.file, got, repo))
	}
}
