// Package record owns the versioned, local-only .muda record.
package record

import (
	"fmt"
	"github.com/schuettc/muda/internal/signal"
)

const Schema = 1

type Evidence = signal.Evidence
type Window struct {
	Since string `json:"since" toml:"since"`
	Until string `json:"until" toml:"until"`
}
type Settings struct {
	Schema      int               `json:"schema" toml:"schema"`
	Roles       map[string]string `json:"roles" toml:"roles"`
	Stacks      []string          `json:"stacks" toml:"stacks"`
	Window      Window            `json:"window" toml:"window"`
	MudaVersion string            `json:"muda-version" toml:"muda-version"`
	// RecordPR is optional: records from before it existed have none.
	RecordPR *RecordPR `json:"record-pr,omitempty" toml:"record-pr,omitempty"`
}

// RecordPR holds the repository conventions every record PR carries.
type RecordPR struct {
	Labels []string `json:"labels" toml:"labels"`
}
type Status string

const (
	OpenStatus   Status = "open"
	Approved     Status = "approved"
	InPR         Status = "in-pr"
	Fixed        Status = "fixed"
	Reopened     Status = "reopened"
	ExemptStatus Status = "exempt"
)

type Finding struct {
	ID          string     `json:"id" toml:"id"`
	Waste       string     `json:"waste" toml:"waste"`
	Location    string     `json:"location" toml:"location"`
	Recipe      string     `json:"recipe" toml:"recipe"`
	Evidence    []Evidence `json:"evidence" toml:"evidence"`
	Basis       string     `json:"basis" toml:"basis"`
	Estimate    string     `json:"estimate" toml:"estimate"`
	Risk        string     `json:"risk" toml:"risk"`
	Status      Status     `json:"status" toml:"status"`
	PR          string     `json:"pr" toml:"pr"`
	ExemptionID string     `json:"exemption-id" toml:"exemption-id"`
	// ReopenReason is set only by fixed -> reopened: the human's word that
	// the finding's PR did not merge. Older findings have none.
	ReopenReason string `json:"reopen-reason,omitempty" toml:"reopen-reason,omitempty"`
}

// Pointer fields distinguish an omitted patch from an explicit empty value.
type FindingPatch struct {
	Waste       *string     `json:"waste"`
	Location    *string     `json:"location"`
	Recipe      *string     `json:"recipe"`
	Evidence    *[]Evidence `json:"evidence"`
	Basis       *string     `json:"basis"`
	Estimate    *string     `json:"estimate"`
	Risk        *string     `json:"risk"`
	Status      *Status     `json:"status"`
	PR          *string     `json:"pr"`
	ExemptionID *string     `json:"exemption-id"`
	// ReopenReason is required by, and only accepted with, fixed -> reopened.
	ReopenReason *string `json:"reopen-reason"`
}
type Exemption struct {
	ID       string `json:"id" toml:"id"`
	Waste    string `json:"waste" toml:"waste"`
	Location string `json:"location" toml:"location"`
	Reason   string `json:"reason" toml:"reason"`
	AgreedBy string `json:"agreed-by" toml:"agreed-by"`
	Date     string `json:"date" toml:"date"`
	ReviewBy string `json:"review-by" toml:"review-by"`
}
type Receipt struct {
	Schema     int      `json:"schema" toml:"schema"`
	FindingID  string   `json:"finding-id" toml:"finding-id"`
	PR         string   `json:"pr" toml:"pr"`
	Before     string   `json:"before" toml:"before"`
	After      string   `json:"after" toml:"after"`
	RunLinks   []string `json:"run-links" toml:"run-links"`
	GateStatus string   `json:"gate-status" toml:"gate-status"`
	GateResult string   `json:"gate-result" toml:"gate-result"`
}

// Gate statuses reported by muda compare --gates. VERIFIED and EXEMPTED pass
// (EXEMPTED is explicit acceptance, not proof); UNVERIFIED and WEAKENED fail.
const (
	GateVerified   = "VERIFIED"
	GateExempted   = "EXEMPTED"
	GateUnverified = "UNVERIFIED"
	GateWeakened   = "WEAKENED"
)

// GatePassed reports whether a gate status allows a finding to be fixed.
func GatePassed(status string) bool { return status == GateVerified || status == GateExempted }

type Problem struct {
	Code   string `json:"code"`
	File   string `json:"file"`
	Detail string `json:"detail"`
}

func (p Problem) Error() string               { return fmt.Sprintf("%s: %s: %s", p.Code, p.File, p.Detail) }
func problem(code, file, detail string) error { return Problem{code, file, detail} }

type findingsFile struct {
	Schema   int       `toml:"schema"`
	Findings []Finding `toml:"findings"`
}
type exemptionsFile struct {
	Schema     int         `toml:"schema"`
	Exemptions []Exemption `toml:"exemptions"`
}

// InventoryJSON stores the ENTIRE producer JSON, including unknown additive
// policy fields, rather than projecting a potentially lossy list of gates.
// HistoricalRef and SettingsRead are set together, only for a historical
// baseline (see Historical); older records have neither.
type gatesFile struct {
	Schema        int    `toml:"schema"`
	InventoryJSON string `toml:"inventory-json"`
	HistoricalRef string `toml:"historical-ref,omitempty"`
	SettingsRead  string `toml:"settings-read,omitempty"`
}
