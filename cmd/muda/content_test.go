package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/schuettc/muda"
	"github.com/schuettc/muda/internal/cli"
	"github.com/schuettc/muda/internal/content"
)

func TestShippedContentIsClean(t *testing.T) {
	known, err := cli.CommandChecker(NewApp())
	if err != nil {
		t.Fatal(err)
	}
	problems := content.Check(muda.Content, known)
	if len(problems) != 0 {
		t.Fatalf("%+v", problems)
	}
}
func TestRecipeCLIFilters(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
		code int
	}{
		{[]string{"recipes"}, "R10", 0}, {[]string{"recipes", "--signal", "slow-step", "--stack", "github-actions"}, "R11: A cache that never warms", 0}, {[]string{"recipes", "--waste", "W8"}, "R8", 0},
		{[]string{"recipes", "--signal", "emulation"}, "R6", 0}, {[]string{"recipes", "--stack", "node"}, "R1", 0},
		{[]string{"recipe", "R1", "--stack", "docker"}, "### docker", 0},
		{[]string{"recipe", "R11", "--stack", "go"}, "### go", 0}, {[]string{"recipe", "R2", "--stack", "go"}, "", 2},
		{[]string{"recipe", "R1"}, "### node", 0}, {[]string{"standard", "W10"}, "Ignored advance notice", 0},
		{[]string{"standard"}, "## Principles", 0}, {[]string{"recipes", "--waste", "W99"}, "", 2},
		{[]string{"recipe", "R1", "--stack", "cdk"}, "", 2}, {[]string{"recipe", "R99"}, "", 2},
		{[]string{"standard", "W99"}, "", 2},
	} {
		var out, errs bytes.Buffer
		code := NewApp().Dispatch(tc.args, &out, &errs)
		if code != tc.code || !strings.Contains(out.String(), tc.want) {
			t.Errorf("%v: %d %s %s", tc.args, code, out.String(), errs.String())
		}
		if len(tc.args) > 3 && tc.args[0] == "recipe" && strings.Contains(out.String(), "### node") {
			t.Error("unselected stack printed")
		}
	}
}

// Release prep P3: the go stack selects exactly R1, R5, R6 and R11.
func TestRecipesStackGo(t *testing.T) {
	var out, errs bytes.Buffer
	if code := NewApp().Dispatch([]string{"recipes", "--stack", "go"}, &out, &errs); code != 0 {
		t.Fatalf("exit %d: %s", code, errs.String())
	}
	var ids []string
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		ids = append(ids, strings.SplitN(line, ":", 2)[0])
	}
	if strings.Join(ids, ",") != "R1,R5,R6,R11" {
		t.Fatalf("recipes --stack go: %q", out.String())
	}
}
func TestCommandCheckerPure(t *testing.T) {
	env := cli.Env{Getenv: func(string) string { t.Fatal("environment access"); return "" }, GHToken: func() (string, error) { t.Fatal("auth"); return "", nil }, GitRemote: func() (string, error) { t.Fatal("git"); return "", nil }}
	known, err := cli.CommandChecker(newApp(env))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		a    []string
		want bool
	}{
		{[]string{"scan", "--since", "2026-09-01"}, true}, {[]string{"scan", "--imaginary"}, false},
		{[]string{"record", "check"}, true}, {[]string{"not-installed"}, false},
		{[]string{"scan", "--no-cache=bad"}, false}, {[]string{"scan", "--since"}, false},
		{[]string{"logs", "123", "--step", "a quoted step"}, true},
		{[]string{"logs"}, false}, {[]string{"logs", "abc"}, false},
		{[]string{"logs", "123", "extra"}, false},
		{[]string{"scan", "unexpected", "--imaginary"}, false},
		{[]string{"scan", "--", "extra"}, false},
		{[]string{"record", "unknown"}, false},
		{[]string{"record", "check", "extra"}, false},
		{[]string{"record", "finding", "set"}, false},
		{[]string{"recipe"}, false}, {[]string{"recipe", "R1", "extra"}, false},
		{[]string{"standard", "W1", "W2"}, false},
		{[]string{"recipes", "extra"}, false},
		{[]string{"compare"}, false},
		{[]string{"compare", "--before", "2026-09-01..2026-09-07", "--after", "2026-09-08..2026-09-14", "--gates"}, true},
	} {
		if got := known(tc.a); got != tc.want {
			t.Errorf("%v: %v", tc.a, got)
		}
	}
}

func TestContentCLISelectionAndDeterminism(t *testing.T) {
	known, err := cli.CommandChecker(NewApp())
	if err != nil {
		t.Fatal(err)
	}
	if problems := content.Check(muda.Content, known); len(problems) != 0 {
		t.Fatal(problems)
	}
	for _, args := range [][]string{{"recipes"}, {"recipes", "--waste", "W2", "--stack", "node", "--signal", "floating-ref"}, {"recipe", "R1", "--stack", "docker"}, {"standard", "W1"}} {
		var first, second, errs bytes.Buffer
		if NewApp().Dispatch(args, &first, &errs) != 0 || NewApp().Dispatch(args, &second, &errs) != 0 {
			t.Fatal(errs.String())
		}
		if first.String() != second.String() {
			t.Errorf("nondeterministic %v", args)
		}
		if args[0] == "recipes" && len(args) > 1 && first.String() != "R1: Pin the toolchain through version files\n" {
			t.Errorf("filters: %s", first.String())
		}
		if args[0] == "recipe" {
			for _, heading := range content.SectionNames {
				if !strings.Contains(first.String(), "## "+heading+"\n") {
					t.Errorf("missing core %s", heading)
				}
			}
			for _, stack := range []string{"node", "python", "github-actions"} {
				if strings.Contains(first.String(), "### "+stack) {
					t.Errorf("unselected %s", stack)
				}
			}
		}
		if args[0] == "standard" && (strings.Contains(first.String(), "| W2 |") || !strings.Contains(first.String(), "| W1 |")) {
			t.Error("incorrect catalog selection")
		}
	}
}
