package record

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/schuettc/muda/internal/measure"
)

type Store struct {
	Root string
	// A mutation session pins the validated boundary for its entire RMW.
	dir, parent *os.Root
	info        os.FileInfo
}

// Discover recognizes both a .git directory and a worktree's .git file.
func Discover(start string) (string, error) {
	p, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		if _, e := os.Stat(filepath.Join(p, ".git")); e == nil {
			return p, nil
		}
		next := filepath.Dir(p)
		if next == p {
			return "", problem("no-git-root", p, "no .git ancestor")
		}
		p = next
	}
}
func Open(root string) (*Store, error) {
	s := &Store{Root: root}
	r, err := s.root()
	if err != nil {
		return nil, err
	}
	if err = r.Close(); err != nil {
		return nil, err
	}
	return s, nil
}

// openRepoRoot refuses a symlink repository boundary.
func openRepoRoot(path string) (*os.Root, error) {
	path = filepath.Clean(path)
	st, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return nil, problem("unsafe-path", "root", "repository root must be a real directory")
	}
	r, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	opened, err := r.Stat(".")
	current, currentErr := os.Lstat(path)
	if err != nil || currentErr != nil || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(st, opened) || !os.SameFile(st, current) {
		_ = r.Close()
		return nil, problem("unsafe-path", "root", "repository boundary changed while opening")
	}
	return r, nil
}

// root refuses a symlink record boundary and uses os.Root for descendant
// operations, so escaping symlinks cannot redirect reads or writes.
func (s *Store) root() (*os.Root, error) {
	if s.dir != nil {
		if err := verifyBoundary(s.parent, s.dir, s.info); err != nil {
			return nil, err
		}
		return s.dir.OpenRoot(".")
	}
	r, err := openRepoRoot(s.Root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	st, err := r.Lstat(".muda")
	if err != nil {
		return nil, err
	}
	return openRecordRoot(r, st)
}

// openRecordRoot opens a previously validated .muda boundary directory.
func openRecordRoot(parent *os.Root, info os.FileInfo) (*os.Root, error) {
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, problem("unsafe-path", ".muda", "must be a real directory")
	}
	opened, err := parent.OpenRoot(".muda")
	if err != nil {
		return nil, err
	}
	if err := verifyBoundary(parent, opened, info); err != nil {
		_ = opened.Close()
		return nil, err
	}
	return opened, nil
}

// Identity is checked on BOTH the opened directory and the parent's current
// entry. A post-Lstat symlink/replacement cannot become the record boundary.
func verifyBoundary(parent, opened *os.Root, info os.FileInfo) error {
	actual, err := opened.Stat(".")
	current, currentErr := parent.Lstat(".muda")
	if err != nil || currentErr != nil || !current.IsDir() || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(info, actual) || !os.SameFile(info, current) {
		return problem("unsafe-path", ".muda", "directory boundary changed")
	}
	return nil
}

const lockName = ".write-lock"

// mutation rejects overlapping writers instead of waiting or guessing whether
// an existing owner's lock is stale. The session pins one boundary through the
// whole read/validate/allocate/write sequence, including cross-file references.
func (s *Store) mutation() (*Store, func() error, error) {
	parent, err := openRepoRoot(s.Root)
	if err != nil {
		return nil, nil, err
	}
	info, err := parent.Lstat(".muda")
	if err != nil {
		_ = parent.Close()
		return nil, nil, err
	}
	dir, err := openRecordRoot(parent, info)
	if err != nil {
		_ = parent.Close()
		return nil, nil, err
	}
	session, unlock, err := s.lockBoundary(parent, dir, info)
	if err != nil {
		_ = dir.Close()
		_ = parent.Close()
		return nil, nil, err
	}
	return session, func() error { return errors.Join(unlock(), dir.Close(), parent.Close()) }, nil
}

func (s *Store) lockBoundary(parent, dir *os.Root, info os.FileInfo) (*Store, func() error, error) {
	if err := verifyBoundary(parent, dir, info); err != nil {
		return nil, nil, err
	}
	f, err := dir.OpenFile(lockName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, nil, problem("record-busy", lockName, "writer lock exists; retry after its owner finishes; stale locks require manual review")
		}
		return nil, nil, err
	}
	owned, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	unlock := func() error {
		defer func() { _ = f.Close() }()
		current, err := dir.Lstat(lockName)
		if err != nil || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(owned, current) {
			return problem("record-busy", lockName, "lock ownership changed; refusing to remove it")
		}
		return dir.Remove(lockName)
	}
	return &Store{Root: s.Root, parent: parent, dir: dir, info: info}, unlock, nil
}

func safe(r *os.Root, name string) error {
	parts := strings.Split(name, "/")
	for i := range parts {
		p := strings.Join(parts[:i+1], "/")
		st, err := r.Lstat(p)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return problem("unsafe-path", name, "symlinks are not record files")
		}
	}
	return nil
}
func (s *Store) read(name string) ([]byte, error) {
	r, err := s.root()
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	if err = safe(r, name); err != nil {
		return nil, err
	}
	return r.ReadFile(name)
}
func atomic(r *os.Root, name string, b []byte) error {
	if err := safe(r, name); err != nil {
		return err
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(name), ".tmp-"+hex.EncodeToString(random[:]))
	f, err := r.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer func() { _ = r.Remove(tmp) }()
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return r.Rename(tmp, name)
}
func (s *Store) write(name string, b []byte) error {
	if err := noSecrets(b); err != nil {
		return err
	}
	r, err := s.root()
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	return atomic(r, name, b)
}
func encode(v any) ([]byte, error) {
	var b bytes.Buffer
	err := toml.NewEncoder(&b).Encode(v)
	return b.Bytes(), err
}
func (s *Store) writeTOML(name string, v any) error {
	b, err := encode(v)
	if err != nil {
		return err
	}
	return s.write(name, b)
}
func schemaCheck(n int, file string) error {
	if n > Schema {
		return problem("newer-schema", file, fmt.Sprintf("schema %d exceeds %d", n, Schema))
	}
	if n != Schema {
		return problem("invalid-schema", file, "expected schema 1")
	}
	return nil
}
func decodeTOML(b []byte, file string, v any) error {
	if err := noSecrets(b); err != nil {
		return err
	}
	var header struct {
		Schema int `toml:"schema"`
	}
	if _, err := toml.Decode(string(b), &header); err != nil {
		return problem("invalid-toml", file, err.Error())
	}
	if err := schemaCheck(header.Schema, file); err != nil {
		return err
	}
	meta, err := toml.Decode(string(b), v)
	if err != nil {
		return err
	}
	// Strict like the JSON input path: an unknown or misspelled key is a
	// problem, never silently dropped on the next write.
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		return problem("unknown-key", file, undecoded[0].String())
	}
	return nil
}
func (s *Store) load(name string, v any) error {
	b, err := s.read(name)
	if err != nil {
		return err
	}
	return decodeTOML(b, name, v)
}
func validateSettings(v Settings) error {
	if err := schemaCheck(v.Schema, "muda.toml"); err != nil {
		return err
	}
	if len(v.Roles) == 0 || len(v.Stacks) == 0 || v.MudaVersion == "" {
		return problem("missing-settings", "muda.toml", "roles, stacks and muda-version must be confirmed")
	}
	for k, val := range v.Roles {
		if k == "" || val == "" {
			return problem("missing-settings", "muda.toml", "empty workflow role")
		}
	}
	for _, stack := range v.Stacks {
		if stack == "" {
			return problem("missing-settings", "muda.toml", "empty stack")
		}
	}
	if v.RecordPR != nil {
		seen := map[string]bool{}
		for _, l := range v.RecordPR.Labels {
			// gh pr create --label splits on commas.
			if l == "" || strings.TrimSpace(l) != l || strings.Contains(l, ",") || seen[l] {
				return problem("invalid-record-pr", "muda.toml", fmt.Sprintf("record-pr label %q: labels are non-empty, unpadded, comma-free and unique", l))
			}
			seen[l] = true
		}
	}
	a, err := dateTime(v.Window.Since)
	if err != nil {
		return problem("invalid-window", "muda.toml", "invalid since")
	}
	b, err := dateTime(v.Window.Until)
	if err != nil || !b.After(a) {
		return problem("invalid-window", "muda.toml", "until must follow since")
	}
	return nil
}
func dateTime(v string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02", v)
}
func (s *Store) Init(v Settings) (err error) {
	if v.Schema == 0 {
		v.Schema = Schema
	}
	if err := validateSettings(v); err != nil {
		return err
	}
	b, err := encode(v)
	if err != nil {
		return err
	}
	if err = noSecrets(b); err != nil {
		return err
	}
	r, err := openRepoRoot(s.Root)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	if err = r.Mkdir(".muda", 0700); err != nil {
		if errors.Is(err, os.ErrExist) {
			return problem("record-exists", ".muda", "record already exists (possibly incomplete); refusing overwrite; inspect with record check")
		}
		return err
	}
	info, err := r.Lstat(".muda")
	if err != nil {
		return err
	}
	dot, err := openRecordRoot(r, info)
	if err != nil {
		return err
	}
	defer func() { _ = dot.Close() }()
	session, unlock, err := s.lockBoundary(r, dot, info)
	if err != nil {
		return err
	}
	defer func() {
		if releaseErr := unlock(); err == nil {
			err = releaseErr
		}
	}()
	if err = verifyBoundary(r, dot, info); err != nil {
		return err
	}
	if err = dot.Mkdir("receipts", 0700); err != nil {
		return err
	}
	files := []struct {
		name string
		v    any
	}{{"muda.toml", v}, {"gates.toml", gatesFile{Schema: Schema, InventoryJSON: `{"schema":1,"gates":[]}`}}, {"findings.toml", findingsFile{Schema: Schema, Findings: []Finding{}}}, {"exemptions.toml", exemptionsFile{Schema: Schema, Exemptions: []Exemption{}}}}
	for _, f := range files {
		data, e := encode(f.v)
		if e != nil {
			return e
		}
		if e = session.write(f.name, data); e != nil {
			return e
		}
	}
	return session.write("baseline.json", []byte("{\"schema\":1}\n"))
}
func validateJSON(b []byte, file string) error {
	var v struct {
		Schema int `json:"schema"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return problem("invalid-json", file, err.Error())
	}
	if err := schemaCheck(v.Schema, file); err != nil {
		return err
	}
	return noSecrets(b)
}
func (s *Store) WriteBaseline(r *measure.Report) error {
	if r == nil {
		return problem("missing-baseline", "baseline.json", "nil report")
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return s.WriteBaselineJSON(b)
}

// WriteBaselineJSON preserves additive producer fields (including scan data).
func (s *Store) WriteBaselineJSON(b []byte) (err error) {
	s, release, err := s.mutation()
	if err != nil {
		return err
	}
	defer func() {
		if releaseErr := release(); err == nil {
			err = releaseErr
		}
	}()
	if err := s.refusePending(); err != nil {
		return err
	}
	if err := validateJSON(b, "baseline.json"); err != nil {
		return err
	}
	old, err := s.read("baseline.json")
	if err != nil {
		return err
	}
	if err = validateJSON(old, "baseline.json"); err != nil {
		return err
	}
	return s.write("baseline.json", b)
}

// Credential material is forbidden everywhere, even inside an expression.
var secretPattern = regexp.MustCompile(`(?i)(gh[pousr]_[a-z0-9]{12,}|github_pat_[a-z0-9_]{12,}|-----BEGIN [A-Z ]*PRIVATE KEY-----|https?://[^\s/"']+:[^\s/@"']+@)`)
var credentialAssignment = regexp.MustCompile(`(?i)(?:["']|\b)(token|password|secret|authorization)(?:["']|\b)\s*[:=]\s*(?:"([^"]+)"|'([^']+)')`)

// credentialReference is a single whole, unevaluated context reference. It
// remains the narrowest accepted form; credentialExpression widens it to
// operators and function calls over such references (see expression.go).
var credentialReference = regexp.MustCompile(`^\$\{\{\s*(?:secrets|github|inputs|env|vars|needs|steps|matrix|jobs|runner|strategy)(?:\.[A-Za-z_][A-Za-z0-9_-]*)+\s*\}\}$`)

// noSecrets refuses credential-shaped text and credential-named values that
// are not a whole reference expression. secretPattern runs first and wins.
// The problem names the key and, for JSON, its path; never the value.
func noSecrets(b []byte) error {
	if loc := secretPattern.FindIndex(b); loc != nil {
		return problem("secret-detected", "record", "credentials must not be recorded"+credentialWhere(b, "", func(s string) bool { return secretPattern.MatchString(s) }))
	}
	for _, match := range credentialAssignment.FindAllSubmatch(b, -1) {
		value, quoted := match[2], true
		if len(value) == 0 {
			value, quoted = match[3], false
		}
		text := string(bytes.TrimSpace(value))
		if quoted {
			// JSON and TOML basic strings escape characters such as '&'.
			var unquoted string
			if json.Unmarshal([]byte(`"`+string(value)+`"`), &unquoted) == nil {
				text = strings.TrimSpace(unquoted)
			}
		}
		if !credentialReference.MatchString(text) && !credentialExpression(text) {
			key := strings.ToLower(string(match[1]))
			return problem("secret-detected", "record", "credentials must not be recorded"+credentialWhere(b, key, func(s string) bool {
				s = strings.TrimSpace(s)
				return !credentialReference.MatchString(s) && !credentialExpression(s)
			}))
		}
	}
	return nil
}

// credentialWhere names the offending key and, when b is JSON, the path of the
// first string value that matches; it never includes the value itself.
func credentialWhere(b []byte, key string, match func(string) bool) string {
	var doc any
	if json.Unmarshal(b, &doc) == nil {
		if path, last, ok := findString(doc, "", "", key, match); ok {
			return fmt.Sprintf(": key %q at %s", last, path)
		}
	}
	if key != "" {
		return fmt.Sprintf(": key %q", key)
	}
	return ""
}

func findString(v any, path, last, key string, match func(string) bool) (string, string, bool) {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			p := k
			if path != "" {
				p = path + "." + k
			}
			if key == "" || credentialKey.MatchString(k) || !isString(x[k]) {
				if found, name, ok := findString(x[k], p, k, key, match); ok {
					return found, name, true
				}
			}
		}
	case []any:
		for i, e := range x {
			if found, name, ok := findString(e, fmt.Sprintf("%s[%d]", path, i), last, key, match); ok {
				return found, name, true
			}
		}
	case string:
		if match(x) && path != "" {
			return path, last, true
		}
	}
	return "", "", false
}

// credentialKey matches a key the way credentialAssignment does.
var credentialKey = regexp.MustCompile(`(?i)\b(?:token|password|secret|authorization)\b`)

func isString(v any) bool { _, ok := v.(string); return ok }
