// Package gates inventories candidate delivery gates without changing repository settings.
package gates

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/schuettc/muda/internal/gh"
	"github.com/schuettc/muda/internal/measure"
	"github.com/schuettc/muda/internal/workflow"
	"gopkg.in/yaml.v3"
)

type Gate struct {
	Line     int    `json:"line,omitempty"`
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Branch   string `json:"branch"`
	Name     string `json:"name"`
	Workflow string `json:"workflow"`
	Job      string `json:"job"`
	Protects string `json:"protects"`
	Detail   string `json:"detail"`
	URL      string `json:"url"`
}
type Unavailable struct {
	What string `json:"what"`
	Why  string `json:"why"`
	// SHA256 is set only for a workflow file that was read but could not be
	// parsed: lowercase hex SHA-256 of its raw bytes. A file that could not
	// be read has none.
	SHA256 string `json:"sha256,omitempty"`
}

// WorkflowSource projects literal execution settings, not resolved policy or a
// complete pass-through workflow configuration. Empty settings mean absent;
// no implicit GitHub defaults are invented.
type WorkflowSource struct {
	Defaults string `json:"defaults"`
	Env      string `json:"env"`
	URL      string `json:"url"`
	Path     string `json:"path"`
	Name     string `json:"name"`
	// SHA256 is the lowercase hex SHA-256 of the raw workflow file bytes as
	// read at the inventory's ref, before any parsing.
	SHA256 string      `json:"sha256"`
	Jobs   []JobSource `json:"jobs"`
}
type StepSource struct {
	// ContinueOnError keeps the YAML literal (including expressions). "" is
	// absent, distinct from explicitly declared "false", "true" or "null".
	ContinueOnError  string            `json:"continue_on_error"`
	Shell            string            `json:"shell"`
	WorkingDirectory string            `json:"working_directory"`
	Env              string            `json:"env"`
	With             map[string]string `json:"with"`
	Name             string            `json:"name"`
	If               string            `json:"if"`
	Run              string            `json:"run"`
	Uses             string            `json:"uses"`
	Line             int               `json:"line"`
	URL              string            `json:"url"`
}
type JobSource struct {
	// ContinueOnError follows StepSource's literal/presence convention.
	ContinueOnError string       `json:"continue_on_error"`
	Defaults        string       `json:"defaults"`
	Env             string       `json:"env"`
	If              string       `json:"if"`
	Line            int          `json:"line"`
	URL             string       `json:"url"`
	Steps           []StepSource `json:"steps"`
	ID              string       `json:"id"`
	Name            string       `json:"name"`
	Uses            string       `json:"uses"`
	Environment     string       `json:"environment"`
	Needs           []string     `json:"needs"`
	Matrix          string       `json:"matrix"`
}

// Inventory keeps typed security settings as well as semantic candidate gates.
type Inventory struct {
	Security []gh.SecuritySnapshot `json:"security"`
	Branch   string                `json:"branch"`
	// Ref is the ref workflow files were read at; the default branch name
	// when no ref was requested. Settings always come from Branch.
	Ref          string           `json:"ref"`
	Sources      []WorkflowSource `json:"workflows"`
	Schema       int              `json:"schema"`
	Repo         string           `json:"repo"`
	Gates        []Gate           `json:"gates"`
	Unavailable  []Unavailable    `json:"unavailable"`
	Protection   *gh.Protection   `json:"protection,omitempty"`
	Rulesets     []gh.Ruleset     `json:"rulesets"`
	Environments []gh.Environment `json:"environments"`
	Workflows    []*workflow.File `json:"-"`
}

var testRun = regexp.MustCompile(`(?i)\b(test|lint|check|vet)\b`)

func tests(j workflow.Job) bool {
	for _, s := range j.Steps {
		if testRun.MatchString(s.Run) {
			return true
		}
		action := strings.Split(s.Uses, "@")[0]
		switch action {
		case "golangci/golangci-lint-action", "reviewdog/action-eslint", "reviewdog/action-staticcheck", "cypress-io/github-action":
			return true
		}
	}
	return false
}
func Derive(ctx context.Context, c *gh.Client, repo, ref string) (*Inventory, error) {
	branch, err := c.DefaultBranch(ctx, repo)
	if err != nil {
		return nil, err
	}
	// Settings belong to the branch a PR merges into; only workflow files
	// (and the needs: gates derived from them) are read at ref.
	if ref == "" {
		ref = branch
	}
	inv := &Inventory{Security: []gh.SecuritySnapshot{}, Branch: branch, Ref: ref, Sources: []WorkflowSource{}, Schema: 1, Repo: repo, Gates: []Gate{}, Unavailable: []Unavailable{}, Rulesets: []gh.Ruleset{}, Environments: []gh.Environment{}, Workflows: []*workflow.File{}}
	missing := func(what string, err error) error {
		var api *gh.Error
		if !errors.As(err, &api) {
			return err
		}
		why := err.Error()
		if what == "branch protection" && api.Status == 403 {
			why = "needs admin read for branch protection: " + why
		}
		inv.Unavailable = append(inv.Unavailable, Unavailable{What: what, Why: why})
		if what == "branch protection" || what == "rulesets" || what == "environments" {
			inv.Security = append(inv.Security, gh.SecuritySnapshot{What: what, URL: "https://api.github.com" + api.Endpoint, Why: why})
		}
		return nil
	}
	add := func(kind, name, path, job, protects, detail, url string) {
		inv.Gates = append(inv.Gates, Gate{ID: kind + ":" + branch + ":" + name, Kind: kind, Branch: branch, Name: name, Workflow: path, Job: job, Protects: protects, Detail: detail, URL: url})
	}
	p, err := c.BranchProtection(ctx, repo, branch)
	switch {
	case err != nil:
		if e := missing("branch protection", err); e != nil {
			return nil, e
		}
	case p.NotProtected:
		// GitHub's explicit "Branch not protected": complete, comparable.
		inv.Security = append(inv.Security, gh.SecuritySnapshot{What: "branch protection", URL: p.EvidenceURL, Complete: true, Absent: true})
	default:
		p.RequiredStatusChecks.Contexts = nonnil(p.RequiredStatusChecks.Contexts)
		sort.Strings(p.RequiredStatusChecks.Contexts)
		inv.Protection = p
		inv.Security = append(inv.Security, gh.SecuritySnapshot{What: "branch protection", URL: p.EvidenceURL, Raw: p.RawSecurity, Complete: len(p.RawSecurity) > 0})
		for _, name := range p.RequiredStatusChecks.Contexts {
			add("required-check", name, "", "", branch, "required branch status check", "https://github.com/"+repo+"/settings/branches")
		}
	}
	sets, err := c.Rulesets(ctx, repo, branch)
	if err != nil {
		if e := missing("rulesets", err); e != nil {
			return nil, e
		}
	} else {
		sort.Slice(sets, func(i, j int) bool { return sets[i].ID < sets[j].ID })
		for i := range sets {
			sets[i].Rules = nonnil(sets[i].Rules)
			for j := range sets[i].Rules {
				checks := nonnil(sets[i].Rules[j].Parameters.RequiredStatusChecks)
				sort.Slice(checks, func(a, b int) bool { return checks[a].Context < checks[b].Context })
				sets[i].Rules[j].Parameters.RequiredStatusChecks = checks
			}
		}
		inv.Rulesets = append(inv.Rulesets, sets...)
		for _, set := range sets {
			raw, err := gh.CanonicalSecurity(mustSecurityJSON(struct {
				Effective json.RawMessage `json:"effective_rules"`
				Metadata  json.RawMessage `json:"metadata,omitempty"`
			}{set.EffectiveRules, set.RawSecurity}))
			if err != nil {
				return nil, err
			}
			inv.Security = append(inv.Security, gh.SecuritySnapshot{What: fmt.Sprintf("ruleset:%d", set.ID), URL: set.EvidenceURL, Raw: raw, Complete: set.DetailUnavailable == "" && len(set.RawSecurity) > 0, Why: set.DetailUnavailable})
			if set.DetailUnavailable != "" {
				inv.Unavailable = append(inv.Unavailable, Unavailable{What: fmt.Sprintf("ruleset %d complete security", set.ID), Why: set.DetailUnavailable})
			}
			for _, rule := range set.Rules {
				for _, check := range rule.Parameters.RequiredStatusChecks {
					add("ruleset-check", set.Name+"/"+check.Context, "", "", branch, "candidate effective branch ruleset check; see complete security availability", fmt.Sprintf("https://github.com/%s/rules/%d", repo, set.ID))
				}
			}
		}
	}
	envs, err := c.Environments(ctx, repo)
	if err != nil {
		if e := missing("environments", err); e != nil {
			return nil, e
		}
	} else {
		sort.Slice(envs, func(i, j int) bool { return envs[i].Name < envs[j].Name })
		for i := range envs {
			envs[i].ProtectionRules = nonnil(envs[i].ProtectionRules)
			sort.Slice(envs[i].ProtectionRules, func(a, b int) bool { return envs[i].ProtectionRules[a].Type < envs[i].ProtectionRules[b].Type })
		}
		inv.Environments = append(inv.Environments, envs...)
		for _, env := range envs {
			inv.Security = append(inv.Security, gh.SecuritySnapshot{What: "environment:" + env.Name, URL: env.EvidenceURL, Raw: env.RawSecurity, Complete: len(env.RawSecurity) > 0})
			parts := []string{}
			for _, r := range env.ProtectionRules {
				parts = append(parts, r.Type)
			}
			sort.Strings(parts)
			add("environment", env.Name, "", "", env.Name, "candidate environment; protection rules: "+strings.Join(parts, ", "), "https://github.com/"+repo+"/settings/environments")
		}
	}
	entries, err := c.Entries(ctx, repo, ref, ".github/workflows")
	var dirErr *gh.Error
	switch {
	case err != nil && errors.As(err, &dirErr):
		// An unknown ref, or a ref without .github/workflows, is a named gap,
		// never an empty inventory that compares as "nothing changed".
		inv.Unavailable = append(inv.Unavailable, Unavailable{What: ".github/workflows", Why: fmt.Sprintf("workflow directory unreadable at ref %q: %s", ref, err.Error())})
	case err != nil:
		return nil, err
	default:
		sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
		for _, entry := range entries {
			path := entry.Path
			if !strings.HasSuffix(path, ".yml") && !strings.HasSuffix(path, ".yaml") {
				continue
			}
			// A symlink or submodule at a workflow path is named, with no
			// hash: dropping it would read as a removal that binds the old
			// file's hash, and reading through a symlink would hash its target.
			if entry.Type != "file" {
				inv.Unavailable = append(inv.Unavailable, Unavailable{What: path, Why: (&gh.NotFileError{Path: path, Type: entry.Type}).Reason()})
				continue
			}
			data, ok, err := c.File(ctx, repo, ref, path)
			var notFile *gh.NotFileError
			if errors.As(err, &notFile) {
				inv.Unavailable = append(inv.Unavailable, Unavailable{What: path, Why: notFile.Reason()})
				continue
			}
			if err != nil {
				if e := missing(path, err); e != nil {
					return nil, e
				}
				continue
			}
			if !ok {
				inv.Unavailable = append(inv.Unavailable, Unavailable{What: path, Why: "workflow file missing"})
				continue
			}
			// Hash the exact bytes read, before any parsing.
			sum := sha256.Sum256(data)
			digest := hex.EncodeToString(sum[:])
			f, err := workflow.Parse(path, data)
			if err != nil {
				// One unparseable file (including a refused merge key) is a
				// named gap, not an aborted inventory; compare treats it as
				// UNVERIFIED.
				inv.Unavailable = append(inv.Unavailable, Unavailable{What: path, Why: "unparseable workflow: " + err.Error(), SHA256: digest})
				continue
			}
			fileURL := gh.BlobURL(repo, ref, path)
			source := WorkflowSource{Defaults: f.Defaults, URL: fileURL, Path: path, Name: f.Name, SHA256: digest, Jobs: []JobSource{}}
			var literal struct {
				Env  yaml.Node `yaml:"env"`
				Jobs map[string]struct {
					Env             yaml.Node `yaml:"env"`
					ContinueOnError yaml.Node `yaml:"continue-on-error"`
					Steps           []struct {
						Env             yaml.Node `yaml:"env"`
						ContinueOnError yaml.Node `yaml:"continue-on-error"`
					} `yaml:"steps"`
					Strategy struct {
						Matrix yaml.Node `yaml:"matrix"`
					} `yaml:"strategy"`
				} `yaml:"jobs"`
			}
			if err := yaml.Unmarshal(data, &literal); err != nil {
				inv.Unavailable = append(inv.Unavailable, Unavailable{What: path, Why: "unparseable workflow: " + err.Error(), SHA256: digest})
				continue
			}
			inv.Workflows = append(inv.Workflows, f)
			source.Env = yamlLiteral(literal.Env)
			for _, id := range f.JobOrder {
				j := f.Jobs[id]
				matrix := ""
				node := literal.Jobs[id].Strategy.Matrix
				if node.Kind != 0 {
					encoded, err := yaml.Marshal(&node)
					if err != nil {
						return nil, err
					}
					matrix = strings.TrimSpace(string(encoded))
				}
				steps := []StepSource{}
				for index, step := range j.Steps {
					stepEnv, stepContinueOnError := "", ""
					if index < len(literal.Jobs[id].Steps) {
						stepEnv = yamlLiteral(literal.Jobs[id].Steps[index].Env)
						stepContinueOnError = yamlLiteral(literal.Jobs[id].Steps[index].ContinueOnError)
					}
					steps = append(steps, StepSource{ContinueOnError: stepContinueOnError, Shell: step.Shell, WorkingDirectory: step.WorkingDirectory, Env: stepEnv, With: step.With, Name: step.Name, If: step.If, Run: step.Run, Uses: step.Uses, Line: step.Line, URL: fmt.Sprintf("%s#L%d", fileURL, step.Line)})
				}
				source.Jobs = append(source.Jobs, JobSource{ContinueOnError: yamlLiteral(literal.Jobs[id].ContinueOnError), Defaults: j.Defaults, Env: yamlLiteral(literal.Jobs[id].Env), If: j.If, Line: j.Line, URL: fmt.Sprintf("%s#L%d", fileURL, j.Line), Steps: steps, ID: id, Name: j.Name, Uses: j.Uses, Environment: j.Environment, Needs: append([]string{}, j.Needs...), Matrix: matrix})
			}
			inv.Sources = append(inv.Sources, source)
			role, _ := measure.InferRole(f.Name, path, f, nil)
			if role != measure.RoleDeploy {
				continue
			}
			for _, id := range f.JobOrder {
				j := f.Jobs[id]
				for _, need := range j.Needs {
					if tests(f.Jobs[need]) {
						add("needs", path+"/"+id+"/"+need, path, id, id, "candidate deploy dependency on test job "+need+"; not proof of enforcement: job/step guards, continue-on-error, shell/defaults and always() may bypass checks", fmt.Sprintf("%s#L%d", fileURL, j.Line))
						inv.Gates[len(inv.Gates)-1].Line = j.Line
					}
				}
			}
		}
	}
	sort.Slice(inv.Security, func(i, j int) bool { return inv.Security[i].What < inv.Security[j].What })
	sort.Slice(inv.Gates, func(i, j int) bool { return inv.Gates[i].ID < inv.Gates[j].ID })
	sort.Slice(inv.Unavailable, func(i, j int) bool { return inv.Unavailable[i].What < inv.Unavailable[j].What })
	return inv, nil
}

func nonnil[T any](values []T) []T { return append([]T{}, values...) }

func mustSecurityJSON(v any) []byte { b, _ := json.Marshal(v); return b }

func yamlLiteral(node yaml.Node) string {
	if node.Kind == 0 {
		return ""
	}
	raw, _ := yaml.Marshal(&node)
	return strings.TrimSpace(string(raw))
}
