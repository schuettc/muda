package signal

import "sort"

// Def declares one signal id: the waste classes it points at and the command
// that emits it.
type Def struct {
	ID    string   `json:"id"`
	Waste []string `json:"waste"`
	From  string   `json:"from"`
}

// Registry is the design spec's §4 signal table, verbatim. It is the only
// place signal ids are declared; recipes are checked against it.
//
// `slow-step` names no waste class: recipes match it on step name or content.
// `rerun` points at W7 or at a flake.
var Registry = map[string]Def{
	"slow-step":               {ID: "slow-step", From: "measure", Waste: []string{}},
	"queue-time":              {ID: "queue-time", From: "measure", Waste: []string{"W8"}},
	"rerun":                   {ID: "rerun", From: "measure", Waste: []string{"W7", "flake"}},
	"tree-rechecked":          {ID: "tree-rechecked", From: "measure", Waste: []string{"W6"}},
	"handoff-failure":         {ID: "handoff-failure", From: "measure", Waste: []string{"W7"}},
	"floating-ref":            {ID: "floating-ref", From: "scan", Waste: []string{"W2"}},
	"floating-runner":         {ID: "floating-runner", From: "scan", Waste: []string{"W2"}},
	"floating-toolchain":      {ID: "floating-toolchain", From: "scan", Waste: []string{"W2"}},
	"toolchain-drift":         {ID: "toolchain-drift", From: "scan", Waste: []string{"W2"}},
	"emulation":               {ID: "emulation", From: "scan", Waste: []string{"W3"}},
	"uncached-install":        {ID: "uncached-install", From: "scan", Waste: []string{"W4"}},
	"serial-independent-jobs": {ID: "serial-independent-jobs", From: "scan", Waste: []string{"W5"}},
	"duplicate-check-set":     {ID: "duplicate-check-set", From: "scan", Waste: []string{"W6"}},
	"no-concurrency":          {ID: "no-concurrency", From: "scan", Waste: []string{"W6"}},
	"no-path-filter":          {ID: "no-path-filter", From: "scan", Waste: []string{"W6"}},
	"advance-notice":          {ID: "advance-notice", From: "notices", Waste: []string{"W10"}},
}

// Known reports whether id is a registered signal id.
func Known(id string) bool {
	_, ok := Registry[id]
	return ok
}

// IDs returns every registered id, sorted.
func IDs() []string {
	ids := make([]string, 0, len(Registry))
	for id := range Registry {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
