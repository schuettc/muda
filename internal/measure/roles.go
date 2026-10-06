package measure

import (
	"fmt"
	"regexp"

	"github.com/schuettc/muda/internal/workflow"
)

// Candidate workflow roles, for the user to confirm.
const (
	RoleGate    = "gate"
	RoleDeploy  = "deploy"
	RoleRelease = "release"
	RoleBump    = "bump"
	RoleOther   = "other"
)

var (
	deployName = regexp.MustCompile(`(?i)deploy|release-to\b|promote`)
	bumpName   = regexp.MustCompile(`(?i)bump`)
)

// InferRole applies the role rules in order; the first match wins. Triggers
// come from the workflow file when it could be read, plus the events its runs
// were observed with (a GitHub-managed workflow has no file). reason says
// which rule matched so the user can judge the candidate.
func InferRole(name, path string, f *workflow.File, events []string) (role, reason string) {
	triggers := map[string]any{}
	for _, e := range events {
		triggers[e] = nil
	}
	if f != nil {
		for k, v := range f.On {
			triggers[k] = v
		}
	}
	for _, e := range []string{"pull_request", "merge_group"} {
		if _, ok := triggers[e]; ok {
			return RoleGate, "triggered by " + e
		}
	}
	switch {
	case deployName.MatchString(name):
		return RoleDeploy, fmt.Sprintf("name %q matches deploy|release-to|promote", name)
	case deployName.MatchString(path):
		return RoleDeploy, fmt.Sprintf("path %q matches deploy|release-to|promote", path)
	}
	if f != nil {
		for _, id := range f.JobOrder {
			j := f.Jobs[id]
			if deployName.MatchString(id) || deployName.MatchString(j.Name) {
				return RoleDeploy, fmt.Sprintf("job %s matches deploy|release-to|promote", id)
			}
		}
		for _, id := range f.JobOrder {
			if env := f.Jobs[id].Environment; env != "" {
				return RoleDeploy, fmt.Sprintf("job %s uses environment: %s", id, env)
			}
		}
	}
	if _, ok := triggers["release"]; ok {
		return RoleRelease, "triggered by release"
	}
	if push, ok := triggers["push"].(map[string]any); ok {
		if _, tags := push["tags"]; tags {
			return RoleRelease, "triggered by push: tags"
		}
	}
	if _, ok := triggers["repository_dispatch"]; ok {
		return RoleBump, "triggered by repository_dispatch"
	}
	if bumpName.MatchString(name) {
		return RoleBump, fmt.Sprintf("name %q matches bump", name)
	}
	return RoleOther, "no role rule matched"
}
