// Package signal defines muda's findings: a stable signal id, the waste
// classes it points at, a severity and the evidence behind every number.
package signal

import "fmt"

// Severities, lowest first.
const (
	SeverityInfo = "info"
	SeverityWarn = "warn"
	SeverityHigh = "high"
)

// Evidence kinds.
const (
	KindRun        = "run"
	KindJob        = "job"
	KindStep       = "step"
	KindFile       = "file"
	KindLog        = "log"
	KindAnnotation = "annotation"
)

// Signal is one finding. Summary states the numbers; Evidence names where
// each of them came from.
type Signal struct {
	ID       string     `json:"id"`
	Waste    []string   `json:"waste"`
	Severity string     `json:"severity"`
	Summary  string     `json:"summary"`
	Evidence []Evidence `json:"evidence"`
}

// Evidence is one source a number can be cited from. Note says what the
// source contributes (for example "p50 duration sample (2m3s)").
//
// TOML keys match the JSON keys and omit the same zero values (omitzero is
// TOML's omitempty for numbers): .muda/findings.toml is schema 1 on disk.
type Evidence struct {
	Kind  string `json:"kind" toml:"kind"`
	URL   string `json:"url" toml:"url"`
	RunID int64  `json:"run_id,omitempty" toml:"run_id,omitzero"`
	JobID int64  `json:"job_id,omitempty" toml:"job_id,omitzero"`
	Step  string `json:"step,omitempty" toml:"step,omitempty"`
	Path  string `json:"path,omitempty" toml:"path,omitempty"`
	Line  int    `json:"line,omitempty" toml:"line,omitzero"`
	Note  string `json:"note,omitempty" toml:"note,omitempty"`
}

// New builds a signal with the registry's waste classes. An unknown id is a
// programming error: signal ids are a contract, declared once in Registry.
func New(id, severity, summary string, evidence []Evidence) Signal {
	def, ok := Registry[id]
	if !ok {
		panic(fmt.Sprintf("signal %q is not in the registry", id))
	}
	if evidence == nil {
		evidence = []Evidence{}
	}
	return Signal{ID: id, Waste: append([]string{}, def.Waste...), Severity: severity, Summary: summary, Evidence: evidence}
}
