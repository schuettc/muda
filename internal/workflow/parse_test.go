package workflow

import (
	"reflect"
	"strings"
	"testing"
)

// TestScanWorkflowShapes pins the YAML shapes scan, gates and measure all
// depend on (Review Focus 4): every `on` form, quoted "on", reusable and
// matrix jobs, and expression-valued `runs-on` kept literally.
func TestScanWorkflowShapes(t *testing.T) {
	t.Run("on forms", func(t *testing.T) {
		for _, tc := range []struct {
			name, src string
			want      []string
		}{
			{"string", "on: push\njobs: {}\n", []string{"push"}},
			{"list", "on: [push, pull_request]\njobs: {}\n", []string{"pull_request", "push"}},
			{"block list", "on:\n  - push\n  - merge_group\njobs: {}\n", []string{"merge_group", "push"}},
			{"map", "on:\n  push:\n    branches: [main]\n  workflow_dispatch:\njobs: {}\n", []string{"push", "workflow_dispatch"}},
			{"quoted", "\"on\":\n  pull_request:\njobs: {}\n", []string{"pull_request"}},
			{"single quoted", "'on': repository_dispatch\njobs: {}\n", []string{"repository_dispatch"}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				f, err := Parse("w.yml", []byte(tc.src))
				if err != nil {
					t.Fatal(err)
				}
				if got := f.Triggers(); !reflect.DeepEqual(got, tc.want) {
					t.Fatalf("triggers %v, want %v", got, tc.want)
				}
				if f.OnLine != 1 {
					t.Fatalf("on line %d", f.OnLine)
				}
			})
		}
	})

	src := `name: ci
on:
  push:
    branches: [main]
    tags: ["v*"]
    paths-ignore: [docs/**]
concurrency:
  group: ci-${{ github.ref }}
  cancel-in-progress: true
jobs:
  build:
    name: Build it
    runs-on: ${{ matrix.os }}
    strategy:
      matrix:
        os: [ubuntu-26.04, macos-26]
    steps:
      - uses: actions/checkout@v7.0.1
      - name: test
        id: t
        if: github.event_name == 'push'
        run: |
          go test ./...
        with:
          flag: true
          list: [a, b]
  call:
    needs: build
    uses: ./.github/workflows/reusable.yml
    secrets: inherit
  deploy:
    needs: [build, call]
    runs-on: [self-hosted, linux]
    environment:
      name: production
      url: https://example.invalid
    concurrency: deploy
    if: ${{ success() }}
    steps:
      - run: ./deploy
  expr-matrix:
    runs-on: ubuntu-26.04
    environment: staging
    strategy:
      matrix: ${{ fromJSON(needs.plan.outputs.matrix) }}
`
	f, err := Parse(".github/workflows/ci.yml", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if f.Path != ".github/workflows/ci.yml" || f.Name != "ci" {
		t.Fatalf("file: %+v", f)
	}
	if got := f.JobOrder; !reflect.DeepEqual(got, []string{"build", "call", "deploy", "expr-matrix"}) {
		t.Fatalf("job order %v", got)
	}
	push, ok := f.On["push"].(map[string]any)
	if !ok || !reflect.DeepEqual(push["tags"], []any{"v*"}) || !reflect.DeepEqual(push["paths-ignore"], []any{"docs/**"}) {
		t.Fatalf("push trigger %#v", f.On["push"])
	}
	if c, ok := f.Concurrency.(map[string]any); !ok || c["group"] != "ci-${{ github.ref }}" {
		t.Fatalf("concurrency %#v", f.Concurrency)
	}

	build := f.Jobs["build"]
	if build.ID != "build" || build.Name != "Build it" || build.RunsOn != "${{ matrix.os }}" || !build.Matrix || build.Line != 11 {
		t.Fatalf("build: %+v", build)
	}
	if len(build.Needs) != 0 || build.Needs == nil {
		t.Fatalf("build needs must be an empty, non-nil list: %#v", build.Needs)
	}
	if len(build.Steps) != 2 || build.Steps[0].Uses != "actions/checkout@v7.0.1" || build.Steps[0].Line != 18 {
		t.Fatalf("build steps: %+v", build.Steps)
	}
	step := build.Steps[1]
	if step.Name != "test" || step.ID != "t" || step.Run != "go test ./...\n" || step.If != "github.event_name == 'push'" || step.Line != 19 {
		t.Fatalf("step: %+v", step)
	}
	if !reflect.DeepEqual(step.With, map[string]string{"flag": "true", "list": "[a, b]"}) {
		t.Fatalf("with: %#v", step.With)
	}

	call := f.Jobs["call"]
	if call.Uses != "./.github/workflows/reusable.yml" || len(call.Steps) != 0 || call.Steps == nil || !reflect.DeepEqual(call.Needs, []string{"build"}) || call.RunsOn != "" {
		t.Fatalf("reusable job: %+v", call)
	}

	deploy := f.Jobs["deploy"]
	if !reflect.DeepEqual(deploy.Needs, []string{"build", "call"}) || deploy.RunsOn != "[self-hosted, linux]" ||
		deploy.Environment != "production" || deploy.Concurrency != "deploy" || deploy.If != "${{ success() }}" || deploy.Matrix {
		t.Fatalf("deploy: %+v", deploy)
	}

	em := f.Jobs["expr-matrix"]
	if !em.Matrix || em.Environment != "staging" {
		t.Fatalf("expression matrix: %+v", em)
	}
	if got := f.Triggers(); !reflect.DeepEqual(got, []string{"push"}) {
		t.Fatalf("triggers %v", got)
	}
}

func TestParseRejectsBadInput(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"not yaml", "on: [push\n", "bad.yml"},
		{"not a mapping", "- a\n- b\n", "not a YAML mapping"},
		{"jobs not a mapping", "on: push\njobs: [a]\n", "jobs"},
		{"job not a mapping", "on: push\njobs:\n  a: 1\n", "job a"},
		{"two documents", "on: push\n---\non: push\n", "single"},
		{"steps not a list", "on: push\njobs:\n  a:\n    steps: 1\n", "steps"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse("bad.yml", []byte(tc.src))
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "bad.yml") {
				t.Fatalf("err %v, want mention of %q and the path", err, tc.want)
			}
		})
	}
}

func TestParseAnchorsAndMissingOn(t *testing.T) {
	src := "defaults: &steps\n  - run: make\njobs:\n  a:\n    runs-on: ubuntu-26.04\n    steps: *steps\n"
	f, err := Parse("w.yml", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Triggers()) != 0 || f.On == nil {
		t.Fatalf("missing on must be an empty map: %#v", f.On)
	}
	if len(f.Jobs["a"].Steps) != 1 || f.Jobs["a"].Steps[0].Run != "make" {
		t.Fatalf("alias steps: %+v", f.Jobs["a"])
	}
}

// TestParseMergeKeysRejected: a merge key would silently drop needs and
// runs-on from a job, so it is an explicit error naming the path.
func TestParseMergeKeysRejected(t *testing.T) {
	src := "on: push\njobs:\n  a: &d\n    runs-on: x\n  b:\n    <<: *d\n    needs: a\n"
	_, err := Parse("m.yml", []byte(src))
	if err == nil || !strings.Contains(err.Error(), "m.yml") || !strings.Contains(err.Error(), "merge key") {
		t.Fatalf("want explicit merge-key error, got %v", err)
	}
}

// TestParseIdentityAttributes: what a job executes, beyond its step names,
// is kept for identity comparison (measure's tree-rechecked, scan).
func TestParseIdentityAttributes(t *testing.T) {
	src := `on: push
env:
  GLOBAL: "1"
defaults:
  run:
    shell: bash
jobs:
  a:
    runs-on: ubuntu-26.04
    container: node:22
    services:
      db: {image: postgres}
    env:
      MODE: ci
    defaults:
      run:
        working-directory: app
    steps:
      - run: make
        shell: sh
        working-directory: sub
        env:
          X: y
`
	f, err := Parse("w.yml", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.Env, map[string]string{"GLOBAL": "1"}) || f.Defaults != "{run: {shell: bash}}" {
		t.Fatalf("file env/defaults: %#v %q", f.Env, f.Defaults)
	}
	a := f.Jobs["a"]
	if a.Container != "node:22" || a.Services != "{db: {image: postgres}}" || !reflect.DeepEqual(a.Env, map[string]string{"MODE": "ci"}) || a.Defaults != "{run: {working-directory: app}}" {
		t.Fatalf("job identity: %+v", a)
	}
	s := a.Steps[0]
	if s.Shell != "sh" || s.WorkingDirectory != "sub" || !reflect.DeepEqual(s.Env, map[string]string{"X": "y"}) {
		t.Fatalf("step identity: %+v", s)
	}
}
