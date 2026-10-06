package content_test

import (
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/schuettc/muda"
	"github.com/schuettc/muda/internal/content"
)

func libraryFixture(t *testing.T) fstest.MapFS {
	t.Helper()
	f := fstest.MapFS{}
	for _, p := range []string{"standard/standard.md", "recipes/stacks.txt", "recipes/R1.md"} {
		b, err := fs.ReadFile(muda.Content, p)
		if err != nil {
			t.Fatal(err)
		}
		f[p] = &fstest.MapFile{Data: b}
	}
	return f
}
func wantProblem(t *testing.T, ps []content.Problem, path, message string) {
	t.Helper()
	for _, p := range ps {
		if p.Path == path && strings.Contains(p.Message, message) {
			return
		}
	}
	t.Fatalf("missing %s: %s in %+v", path, message, ps)
}

type unreadableFS struct {
	fs.FS
	operation string
}

func (f unreadableFS) ReadDir(p string) ([]fs.DirEntry, error) {
	if p == "recipes" && f.operation == "readdir" {
		return nil, &fs.PathError{Op: "readdir", Path: p, Err: fs.ErrPermission}
	}
	return fs.ReadDir(f.FS, p)
}
func (f unreadableFS) Stat(p string) (fs.FileInfo, error) {
	if p == "recipes" && f.operation == "stat" {
		return nil, &fs.PathError{Op: "stat", Path: p, Err: fs.ErrPermission}
	}
	return fs.Stat(f.FS, p)
}
func TestCheckRequiredContent(t *testing.T) {
	t.Run("missing standard", func(t *testing.T) {
		f := libraryFixture(t)
		delete(f, "standard/standard.md")
		wantProblem(t, content.Check(f, func([]string) bool { return true }), "standard/standard.md", "read required standard")
	})
	t.Run("stacks only", func(t *testing.T) {
		f := libraryFixture(t)
		delete(f, "recipes/R1.md")
		wantProblem(t, content.Check(f, func([]string) bool { return true }), "recipes", "empty recipe inventory")
	})
	t.Run("missing recipes", func(t *testing.T) {
		f := libraryFixture(t)
		delete(f, "recipes/R1.md")
		delete(f, "recipes/stacks.txt")
		ps := content.Check(f, func([]string) bool { return true })
		wantProblem(t, ps, "recipes", "read content")
		wantProblem(t, ps, "recipes/stacks.txt", "read")
	})
	for _, op := range []string{"stat", "readdir"} {
		t.Run(op, func(t *testing.T) {
			wantProblem(t, content.Check(unreadableFS{libraryFixture(t), op}, func([]string) bool { return true }), "recipes", "permission denied")
		})
	}
}
func TestCheckDetectVerifyExamples(t *testing.T) {
	for _, section := range []string{"Detect", "Verify"} {
		for _, language := range []string{"bash", "", "prose"} {
			t.Run(section+"/"+language, func(t *testing.T) {
				f := libraryFixture(t)
				text := string(f["recipes/R1.md"].Data)
				start := strings.Index(text, "## "+section+"\n")
				end := strings.Index(text[start+4:], "\n## ") + start + 4
				body := text[start:end]
				if language == "prose" {
					body = "## " + section + "\n\nRun diagnostics.\n"
				} else {
					body = strings.ReplaceAll(body, "```sh", "```"+language)
					body = strings.ReplaceAll(body, "muda scan", "muda nonexistent")
					body = strings.ReplaceAll(body, "muda compare", "muda nonexistent")
				}
				f["recipes/R1.md"].Data = []byte(text[:start] + body + text[end:])
				ps := content.Check(f, func(a []string) bool { return a[0] != "nonexistent" })
				wantProblem(t, ps, "recipes/R1.md", "missing sh command example: "+section)
				if language != "prose" {
					wantProblem(t, ps, "recipes/R1.md", "unsupported example fence language")
					wantProblem(t, ps, "recipes/R1.md", "unknown command: nonexistent")
				}
			})
		}
	}
}
func TestCheckMalformedRecipeStillChecksRaw(t *testing.T) {
	f := libraryFixture(t)
	text := strings.Replace(string(f["recipes/R1.md"].Data), "id: R1", "unexpected: value", 1)
	text = strings.Replace(text, "muda scan", "muda nonexistent", 1) + "\nwidgetco\n"
	f["recipes/R1.md"].Data = []byte(text)
	fakeFacts(t)
	ps := content.Check(f, func(a []string) bool { return a[0] != "nonexistent" })
	wantProblem(t, ps, "recipes/R1.md", "unknown command: nonexistent")
	wantProblem(t, ps, "recipes/R1.md", "project facts are forbidden")
}
func TestCheckAllowsExternalBootstrapOutsideRecipeExamples(t *testing.T) {
	f := libraryFixture(t)
	for _, p := range []string{"skills/README.md", "standard/bootstrap.md"} {
		f[p] = &fstest.MapFile{Data: []byte("```sh\ncurl https://example.com/install.sh\n```\n")}
	}
	if ps := content.Check(f, func([]string) bool { return true }); len(ps) != 0 {
		t.Fatal(ps)
	}
}
