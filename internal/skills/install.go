package skills

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Target validates only the active scope's arguments; it never consults cwd or
// the process home. Project callers must supply an existing Git root.
func Target(agent, scope, home, projectRoot string) (string, string, error) {
	var rel string
	switch agent {
	case "claude":
		rel = ".claude/skills"
	case "pi":
		rel = ".pi/agent/skills"
	case "codex":
		rel = ".codex/skills"
	default:
		return "", "", fmt.Errorf("unsupported agent: %s", agent)
	}
	base := home
	switch scope {
	case "user":
	case "project":
		base = projectRoot
		if agent == "pi" {
			rel = ".pi/skills"
		}
		if agent == "codex" {
			rel = ".agents/skills"
		}
	default:
		return "", "", fmt.Errorf("unsupported scope: %s", scope)
	}
	if base == "" || !filepath.IsAbs(base) {
		return "", "", fmt.Errorf("active %s root must be absolute", scope)
	}
	st, e := os.Lstat(base)
	if e != nil {
		return "", "", e
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return "", "", fmt.Errorf("unsafe root: %s", base)
	}
	if scope == "project" {
		st, e = os.Lstat(filepath.Join(base, ".git"))
		if e != nil || st.Mode()&os.ModeSymlink != 0 || (!st.IsDir() && !st.Mode().IsRegular()) {
			return "", "", fmt.Errorf("not a Git root: %s", base)
		}
	}
	return filepath.Clean(base), rel, nil
}

type source struct {
	rel, name string
	data      []byte
}

func sources(fsys fs.FS) ([]source, error) {
	var files []source
	entries, e := fs.ReadDir(fsys, "skills")
	if e != nil {
		return nil, e
	}
	for _, d := range entries {
		if d.Name() == "README.md" && !d.IsDir() {
			continue
		}
		if !d.IsDir() || !knownName(d.Name()) || d.Type()&fs.ModeSymlink != 0 {
			return nil, fmt.Errorf("invalid source skill: %s", d.Name())
		}
		n := d.Name()
		dir := "skills/" + n
		haveSkill := false
		e = fs.WalkDir(fsys, dir, func(p string, d fs.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if !fs.ValidPath(p) || d.Type()&fs.ModeSymlink != 0 {
				return fmt.Errorf("unsafe source: %s", p)
			}
			if d.IsDir() {
				return nil
			}
			info, e := d.Info()
			if e != nil {
				return e
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("non-regular source: %s", p)
			}
			b, e := fs.ReadFile(fsys, p)
			if e != nil {
				return e
			}
			if !owned(b, n) {
				return fmt.Errorf("unowned source: %s", p)
			}
			if p == dir+"/SKILL.md" {
				haveSkill = true
			}
			files = append(files, source{strings.TrimPrefix(p, "skills/"), n, b})
			return nil
		})
		if e != nil {
			return nil, e
		}
		if !haveSkill {
			return nil, fmt.Errorf("missing source: %s/SKILL.md", dir)
		}
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no bundled skills")
	}
	// Deterministic order with each skill's SKILL.md first: supporting files
	// are only written beneath an already-owned SKILL.md.
	sort.Slice(files, func(i, j int) bool {
		a, b := files[i], files[j]
		if a.name != b.name {
			return a.name+"/" < b.name+"/" // same order as the paths
		}
		if am, bm := a.rel == a.name+"/SKILL.md", b.rel == b.name+"/SKILL.md"; am != bm {
			return am
		}
		return a.rel < b.rel
	})
	return files, nil
}

// safePath rejects symlinks even when their target stays inside the root.
func safePath(r *os.Root, p string) error {
	parts := strings.Split(p, "/")
	for i := range parts {
		q := strings.Join(parts[:i+1], "/")
		st, e := r.Lstat(q)
		if os.IsNotExist(e) {
			return nil
		}
		if e != nil {
			return e
		}
		if st.Mode()&os.ModeSymlink != 0 || (i < len(parts)-1 && !st.IsDir()) {
			return fmt.Errorf("unsafe destination: %s", q)
		}
	}
	return nil
}
func destination(r *os.Root, p, n string) error {
	if e := safePath(r, p); e != nil {
		return e
	}
	st, e := r.Lstat(p)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("foreign destination: %s", p)
	}
	b, e := r.ReadFile(p)
	if e != nil {
		return e
	}
	if !owned(b, n) {
		return fmt.Errorf("foreign destination: %s", p)
	}
	return nil
}

// skillsRoot resolves the agent's skills root once: the root itself may be a
// symlink (e.g. into a dotfiles folder), but must resolve to a
// directory. A missing root is created beneath base without following any
// symlink. Symlinks beneath the resolved root stay refused by safePath.
func skillsRoot(base, rel string) (string, error) {
	p := filepath.Join(base, filepath.FromSlash(rel))
	if _, e := os.Lstat(p); os.IsNotExist(e) {
		r, e := os.OpenRoot(base)
		if e != nil {
			return "", e
		}
		e = safePath(r, rel)
		if e == nil {
			e = r.MkdirAll(rel, 0755)
		}
		if ce := r.Close(); e == nil {
			e = ce
		}
		if e != nil {
			return "", fmt.Errorf("%s: %w", p, e)
		}
	} else if e != nil {
		return "", e
	}
	resolved, e := filepath.EvalSymlinks(p)
	if e != nil {
		return "", e
	}
	st, e := os.Lstat(resolved)
	if e != nil {
		return "", e
	}
	if !st.IsDir() {
		return "", fmt.Errorf("skills root is not a directory: %s", p)
	}
	return resolved, nil
}

// Install is an intentional write-boundary exception to the normal read-only
// CLI. It installs only owned files, preserving unrelated supporting files.
// All static destinations are validated before mkdir/write; each file is then
// atomically renamed through a pinned os.Root. It is not a batch transaction.
func Install(fsys fs.FS, agent, scope, home, projectRoot string) ([]string, error) {
	base, rel, e := Target(agent, scope, home, projectRoot)
	if e != nil {
		return nil, e
	}
	files, e := sources(fsys)
	if e != nil {
		return nil, e
	}
	shown := filepath.Join(base, filepath.FromSlash(rel))
	resolved, e := skillsRoot(base, rel)
	if e != nil {
		return nil, e
	}
	r, e := os.OpenRoot(resolved)
	if e != nil {
		return nil, e
	}
	defer func() { _ = r.Close() }()
	opened, e := r.Stat(".")
	current, ce := os.Lstat(resolved)
	named, ne := os.Stat(shown)
	if e != nil || ce != nil || ne != nil || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(opened, current) || !os.SameFile(opened, named) {
		return nil, fmt.Errorf("root boundary changed: %s", shown)
	}
	fail := func(p string, e error) error { return fmt.Errorf("%s: %w", filepath.Join(shown, p), e) }
	for _, f := range files {
		if e = destination(r, f.rel, f.name); e != nil {
			return nil, fail(f.rel, e)
		}
		if e = safePath(r, f.name); e != nil {
			return nil, fail(f.name, e)
		}
		if _, e = r.Lstat(f.name); e == nil {
			b, err := r.ReadFile(path.Join(f.name, "SKILL.md"))
			if err != nil || !owned(b, f.name) {
				return nil, fail(f.name, fmt.Errorf("foreign skill directory"))
			}
		} else if !os.IsNotExist(e) {
			return nil, fail(f.name, e)
		}
	}
	var written []string
	for _, f := range files {
		if e = safePath(r, f.rel); e != nil {
			return written, e
		}
		if e = r.MkdirAll(path.Dir(f.rel), 0755); e != nil {
			return written, e
		}
		// Pin the own-directory boundary too; root confinement alone would permit
		// an internal symlink redirecting one skill into another.
		own := f.name
		before, e := r.Lstat(own)
		if e != nil {
			return written, e
		}
		ownRoot, e := r.OpenRoot(own)
		if e != nil {
			return written, e
		}
		sub := strings.TrimPrefix(f.rel, f.name+"/")
		e = writeOwned(r, ownRoot, own, sub, f.name, f.data, before)
		closeErr := ownRoot.Close()
		if e != nil {
			return written, e
		}
		if closeErr != nil {
			return written, closeErr
		}
		written = append(written, filepath.Join(shown, filepath.FromSlash(f.rel)))
	}
	return written, nil
}
func writeOwned(parent, r *os.Root, own, sub, n string, b []byte, before os.FileInfo) error {
	boundary := func() error {
		now, e := parent.Lstat(own)
		opened, oe := r.Stat(".")
		if e != nil || oe != nil || now.Mode()&os.ModeSymlink != 0 || !os.SameFile(before, now) || !os.SameFile(before, opened) {
			return fmt.Errorf("skill boundary changed: %s", own)
		}
		if sub != "SKILL.md" {
			if e := safePath(r, "SKILL.md"); e != nil {
				return e
			}
			b, e := r.ReadFile("SKILL.md")
			if e != nil || !owned(b, n) {
				return fmt.Errorf("skill ownership changed: %s", own)
			}
		}
		return nil
	}
	if e := boundary(); e != nil {
		return e
	}
	if e := destination(r, sub, n); e != nil {
		return e
	}
	// Exclusive creation, confined under the own skill root.
	var tmp string
	var f *os.File
	var e error
	for i := 0; i < 100; i++ {
		tmp = path.Join(path.Dir(sub), fmt.Sprintf(".muda-install-%d-%d", os.Getpid(), i))
		f, e = r.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if !os.IsExist(e) {
			break
		}
	}
	if e != nil {
		return e
	}
	defer func() { _ = r.Remove(tmp) }()
	_, e = f.Write(b)
	if e == nil {
		// Created 0600 for exclusivity; installed files are ordinary 0644 files.
		e = f.Chmod(0o644)
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	if e = boundary(); e != nil {
		return e
	}
	if e = destination(r, sub, n); e != nil {
		return e
	}
	return r.Rename(tmp, sub)
}
