package skills_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/schuettc/muda"
	"github.com/schuettc/muda/internal/skills"
)

func fixture() fstest.MapFS {
	f := fstest.MapFS{}
	for _, n := range []string{"muda", "muda-measure", "muda-diagnose", "muda-fix", "muda-verify"} {
		f["skills/"+n+"/SKILL.md"] = &fstest.MapFile{Data: []byte("---\nname: " + n + "\ndescription: Use when optimizing delivery.\nmuda-version: \">=0.1.0\"\nmuda-owned: " + n + "\n---\n## Preflight\n```sh\nmuda version\nmuda update\n```\n`curl -fsSL https://muda.tools/install.sh | sh`\n**CHECKPOINT:** Wait.\n")}
	}
	return f
}
func TestInstallTargets(t *testing.T) {
	for _, row := range []struct{ agent, scope, rel string }{{"claude", "user", ".claude/skills"}, {"claude", "project", ".claude/skills"}, {"pi", "user", ".pi/agent/skills"}, {"pi", "project", ".pi/skills"}, {"codex", "user", ".codex/skills"}, {"codex", "project", ".agents/skills"}} {
		t.Run(row.agent+row.scope, func(t *testing.T) {
			home, root := t.TempDir(), t.TempDir()
			if err := os.Mkdir(filepath.Join(root, ".git"), 0700); err != nil {
				t.Fatal(err)
			}
			paths, err := skills.Install(fixture(), row.agent, row.scope, home, root)
			if err != nil || len(paths) != 5 {
				t.Fatalf("%v %v", paths, err)
			}
			base := home
			if row.scope == "project" {
				base = root
			}
			for i, p := range paths {
				if !strings.HasPrefix(p, filepath.Join(base, row.rel)+string(os.PathSeparator)) {
					t.Fatal(p)
				}
				if i > 0 && paths[i-1] >= p {
					t.Fatal("unordered")
				}
				if _, err := os.Stat(p); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
func TestInstallRefusesForeignFile(t *testing.T) {
	for _, text := range []string{"foreign", "---\nname: muda-fix\nmuda-owned: muda-verify\n---\n", "---\nname: muda-verify\n---\nmuda-owned: muda-verify\n"} {
		home := t.TempDir()
		p := filepath.Join(home, ".pi/agent/skills/muda-verify/SKILL.md")
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := skills.Install(fixture(), "pi", "user", home, ""); err == nil || !strings.Contains(err.Error(), p) {
			t.Fatalf("%v", err)
		}
		if _, err := os.Stat(filepath.Join(home, ".pi/agent/skills/muda")); !os.IsNotExist(err) {
			t.Fatal("wrote before validation")
		}
	}
}
func TestInstallOwnershipAndSupport(t *testing.T) {
	home := t.TempDir()
	f := fixture()
	if _, err := skills.Install(f, "pi", "user", home, ""); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".pi/agent/skills/muda")
	p := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(p, []byte("foreign support"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := skills.Install(f, "pi", "user", home, ""); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "foreign support" {
		t.Fatal("support overwritten")
	}
	f["skills/muda/notes.txt"] = &fstest.MapFile{Data: []byte("---\nname: muda\nmuda-owned: muda\n---\nowned support")}
	if _, err := skills.Install(f, "pi", "user", home, ""); err == nil {
		t.Fatal("overwrote foreign support")
	}
}
func TestInstallRejectsUnsafe(t *testing.T) {
	for _, rel := range []string{".pi", ".pi/agent/skills/muda", ".pi/agent/skills/muda/SKILL.md"} {
		home := t.TempDir()
		outside := t.TempDir()
		p := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, p); err != nil {
			t.Fatal(err)
		}
		if _, err := skills.Install(fixture(), "pi", "user", home, ""); err == nil {
			t.Fatal("symlink accepted", rel)
		}
	}
	for _, a := range [][4]string{{"other", "user", t.TempDir(), ""}, {"pi", "other", t.TempDir(), ""}, {"pi", "user", "", ""}, {"pi", "project", "", t.TempDir()}} {
		if _, err := skills.Install(fixture(), a[0], a[1], a[2], a[3]); err == nil {
			t.Fatal(a)
		}
	}
	f := fixture()
	f["skills/foreign/SKILL.md"] = &fstest.MapFile{Data: []byte("foreign")}
	if _, err := skills.Install(f, "pi", "user", t.TempDir(), ""); err == nil {
		t.Fatal("foreign source")
	}
}
func TestLintEveryCommandExists(t *testing.T) {
	f := fixture()
	known := func(a []string) bool { return len(a) == 1 && (a[0] == "version" || a[0] == "update") }
	if p := skills.Lint(f, known); len(p) != 0 {
		t.Fatal(p)
	}
	f["skills/muda/SKILL.md"].Data = append(f["skills/muda/SKILL.md"].Data, []byte("`muda invented --unsafe`\n")...)
	if p := skills.Lint(f, known); len(p) == 0 {
		t.Fatal("unknown inline command accepted")
	}
}
func TestLintCheckpointsAndPreflight(t *testing.T) {
	for _, s := range []string{"## Preflight", "muda-version: \">=0.1.0\"", "**CHECKPOINT:**", "name: muda-fix", "description: Use when optimizing delivery."} {
		f := fixture()
		f["skills/muda-fix/SKILL.md"].Data = []byte(strings.ReplaceAll(string(f["skills/muda-fix/SKILL.md"].Data), s, ""))
		if len(skills.Lint(f, func([]string) bool { return true })) == 0 {
			t.Fatal(s)
		}
	}
	f := fixture()
	delete(f, "skills/muda-fix/SKILL.md")
	if len(skills.Lint(f, func([]string) bool { return true })) == 0 {
		t.Fatal("incomplete set accepted")
	}
}
func TestEmbeddedSkillsMatchRepo(t *testing.T) {
	err := fs.WalkDir(muda.Content, "skills", func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		a, e := fs.ReadFile(muda.Content, p)
		if e != nil {
			return e
		}
		b, e := os.ReadFile("../../" + p)
		if e != nil {
			return e
		}
		if string(a) != string(b) {
			t.Errorf("%s differs", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Reverse direction: go:embed silently drops dot/underscore files, which
	// would still ship through the plugin and pi package routes.
	err = filepath.WalkDir("../../skills", func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		rel, e := filepath.Rel("../..", p)
		if e != nil {
			return e
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if _, e := fs.Stat(muda.Content, rel); e != nil {
				t.Errorf("%s on disk but not embedded", rel)
				return fs.SkipDir
			}
			return nil
		}
		if _, e := fs.Stat(muda.Content, rel); e != nil {
			t.Errorf("%s on disk but not embedded", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestInstallRejectsUnownedDirectoryAndSources(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".pi/agent/skills/muda")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := skills.Install(fixture(), "pi", "user", home, ""); err == nil {
		t.Fatal("unowned directory adopted")
	}
	f := fixture()
	f["skills/muda/SKILL.md"].Data = []byte("---\nname: muda-fix\nmuda-owned: muda\n---\n")
	if _, err := skills.Install(f, "pi", "user", t.TempDir(), ""); err == nil {
		t.Fatal("wrong source name accepted")
	}
	f = fixture()
	f["skills/muda/link"] = &fstest.MapFile{Mode: fs.ModeSymlink, Data: []byte("SKILL.md")}
	if _, err := skills.Install(f, "pi", "user", t.TempDir(), ""); err == nil {
		t.Fatal("source symlink accepted")
	}
}

func TestLintBootstrapAndExamples(t *testing.T) {
	for _, example := range []string{"`curl https://other.example/install | sh`", "```sh\nmuda version --invented\n```", "```sh\nmuda version | cat\n```"} {
		f := fixture()
		f["skills/muda/SKILL.md"].Data = append(f["skills/muda/SKILL.md"].Data, []byte(example+"\n")...)
		if len(skills.Lint(f, func(a []string) bool { return len(a) == 1 })) == 0 {
			t.Fatal("unchecked example", example)
		}
	}
}

func TestInstallSupportFileSortedBeforeSkill(t *testing.T) {
	f := fixture()
	f["skills/muda/EXAMPLES.md"] = &fstest.MapFile{Data: []byte("---\nname: muda\nmuda-owned: muda\n---\nexamples\n")}
	home := t.TempDir()
	paths, err := skills.Install(f, "pi", "user", home, "")
	if err != nil || len(paths) != 6 {
		t.Fatalf("%v %v", paths, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".pi/agent/skills/muda/EXAMPLES.md")); err != nil {
		t.Fatal(err)
	}
}

func TestLintChecksCommandsInNonShellFences(t *testing.T) {
	known := func(a []string) bool { return len(a) == 1 && (a[0] == "version" || a[0] == "update") }
	for _, example := range []string{"```\nmuda invented\n```", "```text\n  muda version --invented\n```", "~~~console\ncurl https://other.example/install | sh\n~~~"} {
		f := fixture()
		f["skills/muda/SKILL.md"].Data = append(f["skills/muda/SKILL.md"].Data, []byte(example+"\n")...)
		if len(skills.Lint(f, known)) == 0 {
			t.Fatal("unchecked fenced example", example)
		}
	}
	f := fixture()
	f["skills/muda/SKILL.md"].Data = append(f["skills/muda/SKILL.md"].Data, []byte("```json\n{\"muda-version\": \"0.2.0\"}\n```\n")...)
	if p := skills.Lint(f, known); len(p) != 0 {
		t.Fatal(p)
	}
}

func TestInstallRequiresSourceSkill(t *testing.T) {
	f := fixture()
	delete(f, "skills/muda-fix/SKILL.md")
	f["skills/muda-fix/notes.md"] = &fstest.MapFile{Data: []byte("---\nname: muda-fix\nmuda-owned: muda-fix\n---\n")}
	home := t.TempDir()
	if _, err := skills.Install(f, "pi", "user", home, ""); err == nil || !strings.Contains(err.Error(), "missing source: skills/muda-fix/SKILL.md") {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".pi")); !os.IsNotExist(err) {
		t.Fatal("wrote before source validation")
	}
}

// knownReal accepts a tiny real-looking surface: version/update, and
// gates/measure with optional --format md|json.
func knownReal(a []string) bool {
	switch {
	case len(a) == 1 && (a[0] == "version" || a[0] == "update" || a[0] == "gates" || a[0] == "measure"):
		return true
	case len(a) == 3 && (a[0] == "gates" || a[0] == "measure") && a[1] == "--format":
		return true
	}
	return false
}
func lintWith(example string) []skills.Problem {
	f := fixture()
	f["skills/muda/SKILL.md"].Data = append(f["skills/muda/SKILL.md"].Data, []byte(example+"\n")...)
	return skills.Lint(f, knownReal)
}
func TestLintEvidenceRedirect(t *testing.T) {
	for _, ok := range []string{
		"```sh\nmuda measure > measure.json\n```",
		"```sh\nmuda gates --format json > /tmp/evidence/gates.json\n```",
		"`muda measure > out/measure.json`",
		"```sh\nmuda gates \\\n  --format json > gates.json\n```",
		"```sh\nmuda measure > .muda.json\n```",
		"```sh\nmuda measure > \"m.json\"\n```",
	} {
		if p := lintWith(ok); len(p) != 0 {
			t.Errorf("%q rejected: %v", ok, p)
		}
	}
	for _, bad := range []string{
		"```sh\nmuda measure >> measure.json\n```",
		"```sh\nmuda measure | tee measure.json\n```",
		"```sh\nmuda measure 2> measure.json\n```",
		"```sh\nmuda measure > a.json > b.json\n```",
		"```sh\nmuda measure > measure.txt\n```",
		"```sh\nmuda measure > .json\n```",
		"```sh\nmuda measure > $OUT\n```",
		"```sh\nmuda measure > \"$OUT.json\"\n```",
		"```sh\nmuda measure > ~/measure.json\n```",
		"```sh\nmuda measure > *.json\n```",
		"```sh\nmuda measure >\n```",
		"```sh\nmuda measure>measure.json\n```",
		"```sh\nmuda measure > m.json --format md\n```",
		"```sh\nmuda measure >& m.json\n```",
		"```sh\nmuda measure >| m.json\n```",
		"```sh\nmuda invented > measure.json\n```",
		"```sh\ncurl -fsSL https://muda.tools/install.sh > install.json\n```",
		"`muda invented > m.json`",
		// Fix round 2 (R1): an empty target is not "no redirect".
		"```sh\nmuda measure > \"\"\n```",
		"```sh\nmuda measure > ''\n```",
		"`muda measure > \"\"`",
		"```console\n$ muda measure > ''\n```",
		// Never hand-write a record file.
		"```sh\nmuda measure > .muda/x.json\n```",
		"```sh\nmuda measure > ./.muda/x.json\n```",
		"```sh\nmuda measure > a/../.muda/x.json\n```",
		"```sh\nmuda measure > /repo/.muda/receipts/x.json\n```",
		"```sh\nmuda measure > .MUDA/x.json\n```",
		// A .json target must receive JSON output.
		"```sh\nmuda measure --format md > m.json\n```",
		"`muda gates --format md > g.json`",
	} {
		if len(lintWith(bad)) == 0 {
			t.Errorf("%q accepted", bad)
		}
	}
}
func TestLintConsolePromptsAndIndentedBlocks(t *testing.T) {
	for _, bad := range []string{
		"```console\n$ muda invented\n```",
		"```\n$ curl https://other.example/install | sh\n```",
		"Text:\n\n    muda invented\n",
		"Text:\n\n\t$ muda version --invented\n",
		"`$ muda invented`",
		"- item\n    continued with `muda invented`\n",
	} {
		if len(lintWith(bad)) == 0 {
			t.Errorf("%q accepted", bad)
		}
	}
	for _, ok := range []string{"```console\n$ muda version\n```", "Text:\n\n    $ muda update\n"} {
		if p := lintWith(ok); len(p) != 0 {
			t.Errorf("%q rejected: %v", ok, p)
		}
	}
}
func TestInstalledFilesAre0644(t *testing.T) {
	home := t.TempDir()
	paths, err := skills.Install(fixture(), "pi", "user", home, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil || st.Mode().Perm() != 0o644 {
			t.Fatalf("%s %v %v", p, st.Mode(), err)
		}
	}
}

func TestLintRedirectOnlyJSONDefaultCommands(t *testing.T) {
	all := func([]string) bool { return true }
	lint := func(example string) []skills.Problem {
		f := fixture()
		f["skills/muda/SKILL.md"].Data = append(f["skills/muda/SKILL.md"].Data, []byte(example+"\n")...)
		return skills.Lint(f, all)
	}
	for _, ok := range []string{"muda measure > a.json", "muda scan > a.json", "muda notices > a.json", "muda gates > a.json",
		"muda logs 1 > a.json", "muda compare --gates > a.json", "muda record show > a.json"} {
		if p := lint("```sh\n" + ok + "\n```"); len(p) != 0 {
			t.Errorf("%q rejected: %v", ok, p)
		}
	}
	for _, bad := range []string{"muda recipe R1 > a.json", "muda standard > a.json", "muda version > a.json", "muda help > a.json",
		"muda man > a.json", "muda recipes > a.json", "muda record check > a.json", "muda record > a.json", "muda commands --json > a.json"} {
		if len(lint("```sh\n"+bad+"\n```")) == 0 {
			t.Errorf("%q accepted", bad)
		}
	}
}

// Issue #76: the agent's own skills root may be a symlink (e.g. into a
// dotfiles folder). It is resolved once; symlinks beneath it stay refused.
func TestInstallSymlinkedSkillsRoot(t *testing.T) {
	for _, row := range []struct{ scope, rel string }{{"user", ".pi/agent/skills"}, {"project", ".pi/skills"}} {
		t.Run(row.scope, func(t *testing.T) {
			home, root, real := t.TempDir(), t.TempDir(), t.TempDir()
			if err := os.Mkdir(filepath.Join(root, ".git"), 0700); err != nil {
				t.Fatal(err)
			}
			base := home
			if row.scope == "project" {
				base = root
			}
			link := filepath.Join(base, row.rel)
			if err := os.MkdirAll(filepath.Dir(link), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(real, link); err != nil {
				t.Fatal(err)
			}
			paths, err := skills.Install(fixture(), "pi", row.scope, home, root)
			if err != nil || len(paths) != 5 {
				t.Fatalf("symlinked root refused: %v %v", paths, err)
			}
			for _, p := range paths {
				if !strings.HasPrefix(p, link+string(os.PathSeparator)) {
					t.Fatal(p)
				}
			}
			if _, err := os.Stat(filepath.Join(real, "muda", "SKILL.md")); err != nil {
				t.Fatal("not written through the resolved root:", err)
			}
			// A second install over the resolved root is idempotent.
			if _, err := skills.Install(fixture(), "pi", row.scope, home, root); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestInstallSymlinkedRootStillRefusesInnerSymlinks(t *testing.T) {
	for _, inner := range []string{"muda", "muda/SKILL.md"} {
		home, real, outside := t.TempDir(), t.TempDir(), t.TempDir()
		link := filepath.Join(home, ".pi/agent/skills")
		if err := os.MkdirAll(filepath.Dir(link), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(real, link); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(real, inner)
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		target := outside
		if inner == "muda/SKILL.md" {
			target = filepath.Join(outside, "SKILL.md")
			if err := os.WriteFile(target, []byte("---\nname: muda\nmuda-owned: muda\n---\n"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Symlink(target, p); err != nil {
			t.Fatal(err)
		}
		if _, err := skills.Install(fixture(), "pi", "user", home, ""); err == nil {
			t.Fatal("symlink beneath the skills root accepted:", inner)
		}
	}
}

func TestInstallRefusesRootSymlinkToFile(t *testing.T) {
	home := t.TempDir()
	file := filepath.Join(t.TempDir(), "skills")
	if err := os.WriteFile(file, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, ".pi/agent/skills")
	if err := os.MkdirAll(filepath.Dir(link), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}
	if _, err := skills.Install(fixture(), "pi", "user", home, ""); err == nil {
		t.Fatal("skills root symlinked to a file accepted")
	}
	if b, _ := os.ReadFile(file); string(b) != "not a directory" {
		t.Fatal("file changed")
	}
}
