package measure

import (
	"strings"
	"testing"

	"github.com/schuettc/muda/internal/workflow"
)

func TestRoles(t *testing.T) {
	parse := func(src string) *workflow.File {
		f, err := workflow.Parse("w.yml", []byte(src))
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	job := "\njobs:\n  a:\n    runs-on: x\n    steps: [{run: make}]\n"
	for _, tc := range []struct {
		name, wfName, path string
		file               *workflow.File
		events             []string
		want, reason       string
	}{
		{"gate by pull_request", "ci", ".github/workflows/ci.yml", parse("on: [push, pull_request]" + job), nil, RoleGate, "pull_request"},
		{"gate by merge_group", "ci", ".github/workflows/ci.yml", parse("on: merge_group" + job), nil, RoleGate, "merge_group"},
		{"gate by observed event without a file", "ci", "dynamic/x", nil, []string{"pull_request"}, RoleGate, "pull_request"},
		{"deploy by name", "Deploy app", ".github/workflows/a.yml", parse("on: push" + job), nil, RoleDeploy, "name"},
		{"deploy by path", "x", ".github/workflows/release-to-prod.yml", parse("on: push" + job), nil, RoleDeploy, "path"},
		{"deploy by job name", "x", ".github/workflows/a.yml", parse("on: push\njobs:\n  promote:\n    runs-on: x\n"), nil, RoleDeploy, "job promote"},
		{"deploy by environment", "x", ".github/workflows/a.yml", parse("on: push\njobs:\n  a:\n    runs-on: x\n    environment: prod\n"), nil, RoleDeploy, "environment"},
		{"release by release trigger", "publish", ".github/workflows/p.yml", parse("on: release" + job), nil, RoleRelease, "release"},
		{"release by push tags", "publish", ".github/workflows/p.yml", parse("on:\n  push:\n    tags: ['v*']" + job), nil, RoleRelease, "push: tags"},
		{"bump by repository_dispatch", "sync", ".github/workflows/s.yml", parse("on: repository_dispatch" + job), nil, RoleBump, "repository_dispatch"},
		{"bump by name", "Bump pins", ".github/workflows/s.yml", parse("on: schedule" + job), nil, RoleBump, "name"},
		{"other", "nightly", ".github/workflows/n.yml", parse("on: schedule" + job), nil, RoleOther, "no role rule matched"},
		{"release-tools is not release-to (M3)", "tools", ".github/workflows/release-tools.yml", parse("on: schedule" + job), nil, RoleOther, "no role rule matched"},
		{"push branches is not a release", "build", ".github/workflows/b.yml", parse("on:\n  push:\n    branches: [main]" + job), nil, RoleOther, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			role, reason := InferRole(tc.wfName, tc.path, tc.file, tc.events)
			if role != tc.want || !strings.Contains(reason, tc.reason) {
				t.Fatalf("role %q (%q), want %q containing %q", role, reason, tc.want, tc.reason)
			}
			if reason == "" {
				t.Fatal("a candidate role must say why")
			}
		})
	}
}
