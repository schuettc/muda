package compare

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/schuettc/muda/internal/gates"
	"github.com/schuettc/muda/internal/record"
)

// WorkflowDiff explains one UNVERIFIED workflow file: how its raw file
// changed and, where both sides were parsed, which parsed keys changed. It
// never claims more than the parsed projection (gates.WorkflowSource) shows.
type WorkflowDiff struct {
	Path string `json:"path"`
	// ProofID is the Unverified gate ID for this file; an exemption's
	// location is "gate:" + ProofID. A digest-less ID cannot be exempted.
	ProofID string `json:"proof_id"`
	// Change is WorkflowChanged, WorkflowAdded, WorkflowRemoved or
	// WorkflowUnproven (a side lacks a recorded full SHA-256) or
	// WorkflowSameHash (equal full hashes, but a side was not parsed).
	Change string `json:"change"`
	// Structure is StructureCompared, StructureRawOnly, StructureUnparsed or
	// StructureNone (added or removed: one side only).
	Structure string        `json:"structure"`
	Note      string        `json:"note"`
	Fields    []FieldChange `json:"fields"`
}

// FieldChange is one parsed key that differs. Where is a key path such as
// "jobs.test.steps[0].run"; a nil side means the key is absent there.
type FieldChange struct {
	Where string  `json:"where"`
	Old   *string `json:"old"`
	New   *string `json:"new"`
}

const (
	WorkflowChanged   = "changed"
	WorkflowAdded     = "added"
	WorkflowRemoved   = "removed"
	WorkflowUnproven  = "unproven"
	WorkflowSameHash  = "same-hash"
	StructureCompared = "compared"
	StructureRawOnly  = "raw-only"
	StructureUnparsed = "unparsed"
	StructureNone     = "none"
)

const unparsedKeys = "keys muda does not parse (for example triggers, permissions and runs-on), comments and formatting are not compared"

// workflowSide is what one inventory shows for one workflow path.
type workflowSide struct {
	sources int                  // parsed sources at the path
	source  gates.WorkflowSource // the source when sources == 1
	hash    string               // full recorded SHA-256, or ""
	named   bool                 // listed as a source or unavailable
	unread  bool                 // the workflow directory was unreadable
	broken  *gates.Unavailable   // an unavailable entry at the path
}

func sideOf(inv *gates.Inventory, path string) workflowSide {
	var s workflowSide
	for _, w := range inv.Sources {
		if w.Path == path {
			s.sources++
			s.source = w
			s.named = true
		}
	}
	for i, u := range inv.Unavailable {
		if u.What == strings.TrimSuffix(record.WorkflowDir, "/") {
			s.unread = true
		}
		if u.What == path {
			s.named = true
			s.broken = &inv.Unavailable[i]
		}
	}
	switch {
	case s.sources == 1 && s.broken == nil:
		s.hash = fileDigest(s.source.SHA256)
	case s.sources == 0 && s.broken != nil:
		s.hash = fileDigest(s.broken.SHA256)
	}
	return s
}

// workflowDiff classifies one workflow path present as a source on either
// side. It returns identical=true only when both sides carry exactly one
// parsed source with an equal, full recorded SHA-256.
func workflowDiff(a, b *gates.Inventory, path, proofID string, bound bool) (WorkflowDiff, bool) {
	x, y := sideOf(a, path), sideOf(b, path)
	d := WorkflowDiff{Path: path, ProofID: proofID, Fields: []FieldChange{}}
	parsed := x.sources == 1 && x.broken == nil && y.sources == 1 && y.broken == nil
	switch {
	case parsed && x.hash != "" && x.hash == y.hash:
		return d, true
	case x.named && !y.named && !y.unread:
		d.Change, d.Structure = WorkflowRemoved, StructureNone
		d.Note = "file removed or renamed away; the proof binds the before-side hash"
		if !bound {
			d.Note = "file removed or renamed away; no full recorded SHA-256 on the before side, so the proof binds no hash and cannot be exempted"
		}
		return d, false
	case y.named && !x.named && !x.unread:
		d.Change, d.Structure = WorkflowAdded, StructureNone
		d.Note = "file added; the proof binds the after-side hash"
		if !bound {
			d.Note = "file added; no full recorded SHA-256 on the after side, so the proof binds no hash and cannot be exempted"
		}
		return d, false
	}
	var notes []string
	if !parsed && x.hash != "" && x.hash == y.hash {
		// Equal bytes are not a change; only the structure is unknown on
		// the side that was not parsed, so it stays UNVERIFIED.
		d.Change, d.Structure = WorkflowSameHash, StructureUnparsed
		d.Note = "recorded SHA-256 is equal on both sides; structure could not be compared: the " + strings.Join(unparsedSides(x, y), " and ") + " side was not parsed"
		return d, false
	}
	if x.hash != "" && y.hash != "" {
		d.Change = WorkflowChanged
		notes = append(notes, "raw file changed")
	} else {
		d.Change = WorkflowUnproven
		var why []string
		for _, side := range []struct {
			name string
			s    workflowSide
		}{{"before", x}, {"after", y}} {
			switch {
			case side.s.hash != "":
			case !side.s.named:
				why = append(why, "the "+side.name+" side's workflow directory was unreadable")
			case side.s.sources > 1:
				why = append(why, "the "+side.name+" side lists the path more than once")
			default:
				why = append(why, "the "+side.name+" side has no full recorded SHA-256")
			}
		}
		notes = append(notes, "identity cannot be proven: "+strings.Join(why, " and "))
	}
	if !parsed {
		d.Structure = StructureUnparsed
		notes = append(notes, "structure not compared: the "+strings.Join(unparsedSides(x, y), " and ")+" side was not parsed")
		d.Note = strings.Join(notes, "; ")
		return d, false
	}
	d.Fields = sourceFields(x.source, y.source)
	if len(d.Fields) == 0 {
		d.Structure = StructureRawOnly
		notes = append(notes, "no parsed key changed; "+unparsedKeys)
	} else {
		d.Structure = StructureCompared
		notes = append(notes, "parsed keys that changed are listed; "+unparsedKeys)
	}
	d.Note = strings.Join(notes, "; ")
	return d, false
}

func value(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// sourceFields compares two parsed sources key by key. Line numbers and URLs
// are positions, not content, and are never compared.
func sourceFields(a, b gates.WorkflowSource) []FieldChange {
	out := []FieldChange{}
	field := func(where, x, y string) {
		if x != y {
			out = append(out, FieldChange{Where: where, Old: value(x), New: value(y)})
		}
	}
	field("name", a.Name, b.Name)
	field("defaults", a.Defaults, b.Defaults)
	field("env", a.Env, b.Env)
	jobs := map[string][2]*gates.JobSource{}
	for i := range a.Jobs {
		p := jobs[a.Jobs[i].ID]
		p[0] = &a.Jobs[i]
		jobs[a.Jobs[i].ID] = p
	}
	for i := range b.Jobs {
		p := jobs[b.Jobs[i].ID]
		p[1] = &b.Jobs[i]
		jobs[b.Jobs[i].ID] = p
	}
	for _, id := range sortedKeys(jobs) {
		x, y := jobs[id][0], jobs[id][1]
		where := "jobs." + id
		if x == nil || y == nil {
			label := func(j *gates.JobSource) *string {
				if j == nil {
					return nil
				}
				if j.Name != "" {
					return value(j.Name)
				}
				return value(j.ID)
			}
			out = append(out, FieldChange{Where: where, Old: label(x), New: label(y)})
			continue
		}
		field(where+".name", x.Name, y.Name)
		field(where+".uses", x.Uses, y.Uses)
		field(where+".if", x.If, y.If)
		field(where+".environment", x.Environment, y.Environment)
		field(where+".needs", strings.Join(x.Needs, ", "), strings.Join(y.Needs, ", "))
		field(where+".strategy.matrix", x.Matrix, y.Matrix)
		field(where+".continue-on-error", x.ContinueOnError, y.ContinueOnError)
		field(where+".defaults", x.Defaults, y.Defaults)
		field(where+".env", x.Env, y.Env)
		for i := 0; i < len(x.Steps) || i < len(y.Steps); i++ {
			at := fmt.Sprintf("%s.steps[%d]", where, i)
			if i >= len(x.Steps) || i >= len(y.Steps) {
				var oldV, newV *string
				if i < len(x.Steps) {
					oldV = value(stepLabel(x.Steps[i]))
				} else {
					newV = value(stepLabel(y.Steps[i]))
				}
				out = append(out, FieldChange{Where: at, Old: oldV, New: newV})
				continue
			}
			s, t := x.Steps[i], y.Steps[i]
			field(at+".name", s.Name, t.Name)
			field(at+".if", s.If, t.If)
			field(at+".uses", s.Uses, t.Uses)
			field(at+".run", s.Run, t.Run)
			field(at+".shell", s.Shell, t.Shell)
			field(at+".working-directory", s.WorkingDirectory, t.WorkingDirectory)
			field(at+".continue-on-error", s.ContinueOnError, t.ContinueOnError)
			field(at+".env", s.Env, t.Env)
			keys := map[string]bool{}
			for k := range s.With {
				keys[k] = true
			}
			for k := range t.With {
				keys[k] = true
			}
			for _, k := range sortedKeys(keys) {
				v, ok := s.With[k]
				w, ok2 := t.With[k]
				if ok != ok2 || v != w {
					var oldV, newV *string
					if ok {
						oldV = &v
					}
					if ok2 {
						newV = &w
					}
					out = append(out, FieldChange{Where: at + ".with." + k, Old: oldV, New: newV})
				}
			}
		}
	}
	return out
}

// stepLabel names an added or removed step: its name, else its action, else
// its command's first line.
func stepLabel(s gates.StepSource) string {
	switch {
	case s.Name != "":
		return s.Name
	case s.Uses != "":
		return s.Uses
	default:
		line, _, _ := strings.Cut(s.Run, "\n")
		return line
	}
}

// renderValue quotes a value for Markdown; an absent key reads "(absent)".
func renderValue(v *string) string {
	if v == nil {
		return "(absent)"
	}
	return strconv.Quote(*v)
}

// unparsedSides names the sides not parsed as a single source.
func unparsedSides(x, y workflowSide) []string {
	var sides []string
	if x.sources != 1 || x.broken != nil {
		sides = append(sides, "before")
	}
	if y.sources != 1 || y.broken != nil {
		sides = append(sides, "after")
	}
	return sides
}
