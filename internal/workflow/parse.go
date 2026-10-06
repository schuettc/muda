// Package workflow parses GitHub Actions workflow YAML into the small model
// measure, scan and gates share. Values are kept literally: an expression in
// `runs-on` or a matrix built by `fromJSON` is reported as written, never
// evaluated or guessed.
package workflow

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// File is one workflow file.
type File struct {
	Path string
	Name string
	// On maps each trigger event to its configuration: nil, a map[string]any,
	// a []any or a string. The string and list forms of `on` become keys with
	// nil values.
	On          map[string]any
	OnLine      int
	Concurrency any
	Env         map[string]string // workflow env, values as written
	Defaults    string            // workflow defaults, in flow form
	Jobs        map[string]Job
	JobOrder    []string // job ids in document order
}

// Job is one entry under `jobs:`.
type Job struct {
	ID     string
	Name   string // the `name:` as written (may hold expressions); "" if absent
	RunsOn string // literal: a scalar as written, a list or map in flow form
	Needs  []string
	// Uses is the called workflow of a reusable-workflow job, which has no steps.
	Uses        string
	Environment string // the environment name, as written
	Matrix      bool   // strategy.matrix is present (a map or an expression)
	If          string
	Steps       []Step
	Concurrency any
	// What the job executes with, as written: env values, and container,
	// services and defaults in flow form.
	Env                           map[string]string
	Container, Services, Defaults string
	Line                          int
}

// Step is one entry under a job's `steps:`.
type Step struct {
	ID, Name, Uses, Run, If string
	With                    map[string]string // scalars as written, others in flow form
	Env                     map[string]string
	Shell, WorkingDirectory string
	Line                    int
}

// Triggers returns the trigger event names, sorted.
func (f *File) Triggers() []string {
	out := make([]string, 0, len(f.On))
	for k := range f.On {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Parse reads one workflow document. Errors name the path.
func Parse(path string, data []byte) (*File, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("%s: empty workflow file", path)
		}
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var more yaml.Node
	if err := dec.Decode(&more); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: expected a single YAML document", path)
	}
	root := deref(&doc)
	if root.Kind == yaml.DocumentNode && len(root.Content) == 1 {
		root = deref(root.Content[0])
	}
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s is not a YAML mapping", path)
	}
	if line := mergeKey(root); line > 0 {
		return nil, fmt.Errorf("%s: line %d: YAML merge key (<<) is not supported; expand it so no key is silently lost", path, line)
	}
	f := &File{Path: path, On: map[string]any{}, Env: map[string]string{}, Jobs: map[string]Job{}, JobOrder: []string{}}
	for i := 0; i+1 < len(root.Content); i += 2 {
		key, val := root.Content[i], deref(root.Content[i+1])
		switch key.Value {
		case "name":
			f.Name = scalar(val)
		case "on":
			f.OnLine = key.Line
			on, err := triggers(val)
			if err != nil {
				return nil, fmt.Errorf("%s: on: %w", path, err)
			}
			f.On = on
		case "concurrency":
			f.Concurrency = value(val)
		case "env":
			f.Env = stringMap(val)
		case "defaults":
			f.Defaults = literal(val)
		case "jobs":
			if val.Kind != yaml.MappingNode {
				return nil, fmt.Errorf("%s: jobs is not a mapping", path)
			}
			for j := 0; j+1 < len(val.Content); j += 2 {
				id := val.Content[j].Value
				if _, dup := f.Jobs[id]; dup {
					return nil, fmt.Errorf("%s: duplicate job %s", path, id)
				}
				job, err := parseJob(id, val.Content[j].Line, deref(val.Content[j+1]))
				if err != nil {
					return nil, fmt.Errorf("%s: %w", path, err)
				}
				f.Jobs[id] = job
				f.JobOrder = append(f.JobOrder, id)
			}
		}
	}
	return f, nil
}

func triggers(n *yaml.Node) (map[string]any, error) {
	on := map[string]any{}
	switch n.Kind {
	case yaml.ScalarNode:
		if n.Value != "" {
			on[n.Value] = nil
		}
	case yaml.SequenceNode:
		for _, e := range n.Content {
			e = deref(e)
			if e.Kind != yaml.ScalarNode {
				return nil, fmt.Errorf("list entries must be event names")
			}
			on[e.Value] = nil
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			on[n.Content[i].Value] = value(deref(n.Content[i+1]))
		}
	default:
		return nil, fmt.Errorf("unsupported shape")
	}
	return on, nil
}

func parseJob(id string, line int, n *yaml.Node) (Job, error) {
	job := Job{ID: id, Line: line, Needs: []string{}, Steps: []Step{}, Env: map[string]string{}}
	if n.Kind != yaml.MappingNode {
		return job, fmt.Errorf("job %s is not a mapping", id)
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		key, val := n.Content[i].Value, deref(n.Content[i+1])
		switch key {
		case "name":
			job.Name = scalar(val)
		case "runs-on":
			job.RunsOn = literal(val)
		case "needs":
			if val.Kind == yaml.SequenceNode {
				for _, e := range val.Content {
					job.Needs = append(job.Needs, scalar(deref(e)))
				}
			} else if s := scalar(val); s != "" {
				job.Needs = append(job.Needs, s)
			}
		case "uses":
			job.Uses = scalar(val)
		case "environment":
			if val.Kind == yaml.MappingNode {
				job.Environment = scalar(mapGet(val, "name"))
			} else {
				job.Environment = scalar(val)
			}
		case "strategy":
			if val.Kind == yaml.MappingNode && mapGet(val, "matrix") != nil {
				job.Matrix = true
			}
		case "if":
			job.If = literal(val)
		case "concurrency":
			job.Concurrency = value(val)
		case "env":
			job.Env = stringMap(val)
		case "container":
			job.Container = literal(val)
		case "services":
			job.Services = literal(val)
		case "defaults":
			job.Defaults = literal(val)
		case "steps":
			if val.Kind != yaml.SequenceNode {
				return job, fmt.Errorf("job %s: steps is not a list", id)
			}
			for _, s := range val.Content {
				s = deref(s)
				if s.Kind != yaml.MappingNode {
					return job, fmt.Errorf("job %s: step at line %d is not a mapping", id, s.Line)
				}
				job.Steps = append(job.Steps, parseStep(s))
			}
		}
	}
	return job, nil
}

func parseStep(n *yaml.Node) Step {
	st := Step{Line: n.Line, With: map[string]string{}, Env: map[string]string{}}
	for i := 0; i+1 < len(n.Content); i += 2 {
		key, val := n.Content[i].Value, deref(n.Content[i+1])
		switch key {
		case "id":
			st.ID = scalar(val)
		case "name":
			st.Name = literal(val)
		case "uses":
			st.Uses = scalar(val)
		case "run":
			st.Run = scalar(val)
		case "if":
			st.If = literal(val)
		case "with":
			st.With = stringMap(val)
		case "env":
			st.Env = stringMap(val)
		case "shell":
			st.Shell = literal(val)
		case "working-directory":
			st.WorkingDirectory = literal(val)
		}
	}
	return st
}

// stringMap renders a mapping's values literally; anything else (an
// expression such as `env: ${{ fromJSON(x) }}`) is kept under the key "".
func stringMap(n *yaml.Node) map[string]string {
	out := map[string]string{}
	if n == nil {
		return out
	}
	if n.Kind != yaml.MappingNode {
		if s := literal(n); s != "" {
			out[""] = s
		}
		return out
	}
	for j := 0; j+1 < len(n.Content); j += 2 {
		out[n.Content[j].Value] = literal(deref(n.Content[j+1]))
	}
	return out
}

// mergeKey returns the line of the first `<<` merge key, or 0.
func mergeKey(n *yaml.Node) int {
	seen := map[*yaml.Node]bool{}
	var walk func(n *yaml.Node) int
	walk = func(n *yaml.Node) int {
		n = deref(n)
		if n == nil || seen[n] {
			return 0
		}
		seen[n] = true
		if n.Kind == yaml.MappingNode {
			for i := 0; i+1 < len(n.Content); i += 2 {
				if k := n.Content[i]; k.Value == "<<" && k.Style == 0 && k.ShortTag() == "!!merge" {
					return k.Line
				}
			}
		}
		for _, c := range n.Content {
			if l := walk(c); l > 0 {
				return l
			}
		}
		return 0
	}
	return walk(n)
}

func deref(n *yaml.Node) *yaml.Node {
	for n != nil && n.Kind == yaml.AliasNode && n.Alias != nil {
		n = n.Alias
	}
	return n
}

func mapGet(n *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return deref(n.Content[i+1])
		}
	}
	return nil
}

// scalar is a scalar's text, or "" for anything else.
func scalar(n *yaml.Node) string {
	if n == nil || n.Kind != yaml.ScalarNode || n.ShortTag() == "!!null" {
		return ""
	}
	return n.Value
}

// literal renders any node as written: a scalar's text, a list or map in
// YAML flow form.
func literal(n *yaml.Node) string {
	n = deref(n)
	if n == nil {
		return ""
	}
	switch n.Kind {
	case yaml.ScalarNode:
		return scalar(n)
	case yaml.SequenceNode:
		parts := make([]string, 0, len(n.Content))
		for _, e := range n.Content {
			parts = append(parts, literal(e))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case yaml.MappingNode:
		parts := make([]string, 0, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			parts = append(parts, n.Content[i].Value+": "+literal(n.Content[i+1]))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	}
	return ""
}

// value converts a node to plain Go values: map[string]any, []any, string or
// nil. Scalars stay strings so expressions and versions are never retyped.
func value(n *yaml.Node) any {
	n = deref(n)
	if n == nil {
		return nil
	}
	switch n.Kind {
	case yaml.ScalarNode:
		if n.ShortTag() == "!!null" {
			return nil
		}
		return n.Value
	case yaml.SequenceNode:
		out := make([]any, 0, len(n.Content))
		for _, e := range n.Content {
			out = append(out, value(e))
		}
		return out
	case yaml.MappingNode:
		out := map[string]any{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			out[n.Content[i].Value] = value(n.Content[i+1])
		}
		return out
	}
	return nil
}
