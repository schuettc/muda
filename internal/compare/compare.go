// Package compare compares measured delivery windows without writing records.
package compare

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/schuettc/muda/internal/gates"
	"github.com/schuettc/muda/internal/gh"
	"github.com/schuettc/muda/internal/measure"
	"github.com/schuettc/muda/internal/record"
	"github.com/schuettc/muda/internal/signal"
)

type Window struct {
	Since  time.Time `json:"since,omitempty"`
	Until  time.Time `json:"until,omitempty"`
	RunIDs []int64   `json:"run_ids,omitempty"`
}

func (w Window) Validate() error {
	if len(w.RunIDs) > 0 {
		if !w.Since.IsZero() || !w.Until.IsZero() {
			return fmt.Errorf("window and run IDs are mutually exclusive")
		}
		seen := map[int64]bool{}
		for _, id := range w.RunIDs {
			if id <= 0 || seen[id] {
				return fmt.Errorf("run IDs must be positive and unique")
			}
			seen[id] = true
		}
		return nil
	}
	if w.Since.IsZero() || w.Until.IsZero() || !w.Since.Before(w.Until) {
		return fmt.Errorf("window requires ordered nonzero [since, until) bounds")
	}
	return nil
}

type Timing struct {
	Runs     int               `json:"runs"`
	P50      time.Duration     `json:"p50_ns"`
	P90      time.Duration     `json:"p90_ns"`
	Evidence []signal.Evidence `json:"evidence"`
}
type Delta struct {
	Workflow        string         `json:"workflow"`
	Job             string         `json:"job,omitempty"`
	Name            string         `json:"name"`
	Occurrence      int            `json:"occurrence,omitempty"`
	Presence        string         `json:"presence"`
	Before          *Timing        `json:"before"`
	After           *Timing        `json:"after"`
	ChangeNS        *time.Duration `json:"change_ns"`
	RelativePercent *float64       `json:"relative_percent"`
}
type Result struct {
	Schema            int                   `json:"schema"`
	Before            Window                `json:"before"`
	After             Window                `json:"after"`
	Workflows         []Delta               `json:"workflows"`
	Steps             []Delta               `json:"steps"`
	Gates             *GateDiff             `json:"gates"`
	Scan              *ScanDiff             `json:"scan"`
	BeforeUnavailable []measure.Unavailable `json:"before_unavailable"`
	AfterUnavailable  []measure.Unavailable `json:"after_unavailable"`
}
type GateDiff struct {
	Status       string            `json:"status"`
	Removed      []gates.Gate      `json:"removed"`
	Loosened     []gates.Gate      `json:"loosened"`
	Added        []gates.Gate      `json:"added"`
	Strengthened []gates.Gate      `json:"strengthened"`
	Unverified   []gates.Gate      `json:"unverified"`
	Exempted     []string          `json:"exempted"`
	Evidence     []signal.Evidence `json:"evidence"`
	// WorkflowDiffs explains each UNVERIFIED workflow file; a byte-identical
	// file is VERIFIED and absent here.
	WorkflowDiffs []WorkflowDiff `json:"workflow_diffs"`
	// BeforeRef and BeforeSettings are set for a historical baseline: the
	// recorded inventory read workflows at BeforeRef, while its repository
	// settings were read later and are current (record.SettingsLabel).
	BeforeRef string `json:"before_ref,omitempty"`
	// BeforeRepo is the name the baseline was recorded under, when GitHub has
	// since renamed or transferred the repository to the one compared.
	BeforeRepo     string `json:"before_repo,omitempty"`
	BeforeSettings string `json:"before_settings,omitempty"`
}

func newGateDiff() *GateDiff {
	return &GateDiff{Removed: []gates.Gate{}, Loosened: []gates.Gate{}, Added: []gates.Gate{}, Strengthened: []gates.Gate{}, Unverified: []gates.Gate{}, Exempted: []string{}, Evidence: []signal.Evidence{}, WorkflowDiffs: []WorkflowDiff{}}
}

// UnavailableGates names a missing local/API proof instead of an empty pass.
func UnavailableGates(why string) *GateDiff {
	d := newGateDiff()
	d.Unverified = append(d.Unverified, gates.Gate{ID: record.PolicyInventory, Kind: "policy-proof", Detail: why})
	d.status()
	return d
}
func (d *GateDiff) Passed() bool {
	ex := map[string]bool{}
	for _, id := range d.Exempted {
		ex[id] = true
	}
	for _, list := range [][]gates.Gate{d.Removed, d.Loosened, d.Unverified} {
		for _, g := range list {
			if !ex[g.ID] {
				return false
			}
		}
	}
	return true
}
func (d *GateDiff) status() {
	d.Status = record.GateVerified
	if len(d.Unverified) > 0 {
		d.Status = record.GateUnverified
	} else if len(d.Removed)+len(d.Loosened) > 0 {
		d.Status = record.GateWeakened
	}
	if d.Passed() && len(d.Exempted) > 0 {
		d.Status = record.GateExempted
	}
}

// Options selects the checks compare runs beside the timing comparison.
type Options struct {
	// Gates verifies GatesFile; a GatesFile also enables it.
	Gates     bool
	GatesFile string
	// Scan compares the record's scan.json with a fresh scan.
	Scan bool
	// Ref is the after side of --gates and --scan: workflows are read at it.
	// "" means the default branch.
	Ref string
	// Record is the repository root holding the .muda record whose scan.json
	// --scan reads (as found by record.Discover); "" means none was found.
	Record string
	// Since and Until bound the fresh scan's run history to [Since, Until), as
	// muda scan --since and --until do. Timing phases use their own windows.
	Since, Until time.Time
}

func Run(ctx context.Context, c *gh.Client, repo string, before, after Window, o Options) (*Result, error) {
	if err := before.Validate(); err != nil {
		return nil, fmt.Errorf("before: %w", err)
	}
	if err := after.Validate(); err != nil {
		return nil, fmt.Errorf("after: %w", err)
	}
	if o.Scan && (o.Since.IsZero() || o.Until.IsZero() || !o.Since.Before(o.Until)) {
		return nil, fmt.Errorf("scan: run history requires ordered nonzero [since, until) bounds")
	}
	fetch := func(w Window) (*measure.Report, error) {
		var runs []gh.Run
		var err error
		if len(w.RunIDs) > 0 {
			ids := append([]int64(nil), w.RunIDs...)
			sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
			for _, id := range ids {
				r, e := c.Run(ctx, repo, id)
				if e != nil {
					return nil, e
				}
				if r.ID != id {
					return nil, fmt.Errorf("run %d returned identity %d", id, r.ID)
				}
				runs = append(runs, *r)
			}
		} else {
			runs, err = c.RunsWindow(ctx, repo, w.Since, w.Until)
			if err != nil {
				return nil, err
			}
		}
		return measure.RunSelected(ctx, c, repo, runs, w.Since, w.Until)
	}
	a, err := fetch(before)
	if err != nil {
		return nil, err
	}
	b, err := fetch(after)
	if err != nil {
		return nil, err
	}
	r := Deltas(a, b, before, after)
	if o.Gates || o.GatesFile != "" {
		if o.GatesFile == "" {
			r.Gates = UnavailableGates("no recorded gates inventory selected")
		} else {
			r.Gates = verifyGates(ctx, c, repo, o.GatesFile, o.Ref)
		}
	}
	if o.Scan {
		r.Scan = verifyScan(ctx, c, repo, o)
	}
	return r, nil
}
func Deltas(a, b *measure.Report, before, after Window) *Result {
	r := &Result{Schema: 1, Before: before, After: after, Workflows: []Delta{}, Steps: []Delta{}, BeforeUnavailable: a.Unavailable, AfterUnavailable: b.Unavailable}
	if r.BeforeUnavailable == nil {
		r.BeforeUnavailable = []measure.Unavailable{}
	}
	if r.AfterUnavailable == nil {
		r.AfterUnavailable = []measure.Unavailable{}
	}
	type key struct {
		path, job, name string
		occ             int
	}
	type pair struct{ a, b *Timing }
	ws := map[key]*pair{}
	ss := map[key]*pair{}
	put := func(m map[key]*pair, k key, t Timing, after bool) {
		if t.Evidence == nil {
			t.Evidence = []signal.Evidence{}
		}
		p := m[k]
		if p == nil {
			p = &pair{}
			m[k] = p
		}
		if after {
			p.b = &t
		} else {
			p.a = &t
		}
	}
	for n, rep := range []*measure.Report{a, b} {
		for _, w := range rep.Workflows {
			put(ws, key{path: w.Path, name: w.Name}, Timing{w.Runs, w.P50, w.P90, w.Evidence}, n == 1)
			for _, j := range w.Jobs {
				for _, s := range j.Steps {
					put(ss, key{w.Path, j.Name, s.Name, s.Occurrence}, Timing{s.Runs, s.P50, s.P90, s.Evidence}, n == 1)
				}
			}
		}
	}
	// Workflow identity is its path, never its mutable display name.
	merged := map[key]*pair{}
	for k, p := range ws {
		q := key{path: k.path}
		v := merged[q]
		if v == nil {
			v = &pair{}
			merged[q] = v
		}
		if p.a != nil {
			v.a = p.a
		}
		if p.b != nil {
			v.b = p.b
		}
	}
	build := func(m map[key]*pair) []Delta {
		keys := make([]key, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			x, y := keys[i], keys[j]
			if x.path != y.path {
				return x.path < y.path
			}
			if x.job != y.job {
				return x.job < y.job
			}
			if x.name != y.name {
				return x.name < y.name
			}
			return x.occ < y.occ
		})
		out := []Delta{}
		for _, k := range keys {
			p := m[k]
			d := Delta{Workflow: k.path, Job: k.job, Name: k.name, Occurrence: k.occ, Before: p.a, After: p.b, Presence: "both"}
			switch {
			case p.a == nil:
				d.Presence = "after-only"
			case p.b == nil:
				d.Presence = "before-only"
			case p.a.Runs == 0 || p.b.Runs == 0:
				d.Presence = "unavailable"
			default:
				change := p.b.P50 - p.a.P50
				d.ChangeNS = &change
				if p.a.P50 != 0 {
					percent := (float64(p.b.P50)/float64(p.a.P50) - 1) * 100
					d.RelativePercent = &percent
				}
			}
			out = append(out, d)
		}
		return out
	}
	r.Workflows = build(merged)
	r.Steps = build(ss)
	return r
}

// GateChanges compares complete security snapshots, never the lossy projections.
func GateChanges(a, b *gates.Inventory) *GateDiff {
	d := newGateDiff()
	unknown := func(id, why, url string) {
		g := gates.Gate{ID: id, Kind: "policy-proof", Detail: why, URL: url}
		// The same gap seen on both sides (an identical unparseable file,
		// say) is one entry, not one per side.
		if !slices.Contains(d.Unverified, g) {
			d.Unverified = append(d.Unverified, g)
		}
	}
	if a.Schema != 1 || b.Schema != 1 || a.Repo != b.Repo {
		unknown(record.PolicyInventory, "schema/repository mismatch", "")
	}
	for _, inv := range []*gates.Inventory{a, b} {
		for _, u := range inv.Unavailable {
			// An unparseable workflow binds its raw file hash, so an
			// exemption covers only that exact broken file. A file that
			// could not be read has no hash and stays unexemptable.
			id := record.PolicyAvailabilityPrefix + u.What
			if digest := fileDigest(u.SHA256); digest != "" {
				id += "@" + digest
			}
			unknown(id, u.Why, "")
		}
	}
	if a.Branch != b.Branch || !protectionProjectionEqual(a, b) || !reflect.DeepEqual(a.Rulesets, b.Rulesets) || !reflect.DeepEqual(a.Environments, b.Environments) {
		unknown(record.PolicyInventoryProjection, "inventory identity or security projection changed; cannot certify", "")
	}
	am, bm := map[string]gates.Gate{}, map[string]gates.Gate{}
	for _, g := range a.Gates {
		if _, ok := am[g.ID]; ok || g.ID == "" {
			unknown(record.PolicyGatePrefix+g.ID, "missing or duplicate gate identity", g.URL)
		}
		am[g.ID] = g
	}
	for _, g := range b.Gates {
		if _, ok := bm[g.ID]; ok || g.ID == "" {
			unknown(record.PolicyGatePrefix+g.ID, "missing or duplicate gate identity", g.URL)
		}
		bm[g.ID] = g
	}
	for _, id := range sortedKeys(am) {
		g := am[id]
		h, ok := bm[id]
		if !ok {
			d.Removed = append(d.Removed, g)
			continue
		}
		g.Line, h.Line = 0, 0
		g.URL, h.URL = "", ""
		if !reflect.DeepEqual(g, h) {
			unknown(id, "gate semantics changed; cannot classify execution weakening", bm[id].URL)
		}
	}
	for _, id := range sortedKeys(bm) {
		if _, ok := am[id]; !ok {
			d.Added = append(d.Added, bm[id])
		}
	}
	as, bs := map[string]gh.SecuritySnapshot{}, map[string]gh.SecuritySnapshot{}
	for _, s := range a.Security {
		if _, ok := as[s.What]; ok {
			unknown(record.PolicySecurityPrefix+s.What, "duplicate security identity", s.URL)
		}
		as[s.What] = s
	}
	for _, s := range b.Security {
		if _, ok := bs[s.What]; ok {
			unknown(record.PolicySecurityPrefix+s.What, "duplicate security identity", s.URL)
		}
		bs[s.What] = s
	}
	if len(as) == 0 || len(bs) == 0 {
		unknown(record.PolicySecurity, "complete security inventory missing", "")
	}
	all := map[string]bool{}
	for k := range as {
		all[k] = true
	}
	for k := range bs {
		all[k] = true
	}
	for _, id := range sortedKeys(all) {
		x, xok := as[id]
		y, yok := bs[id]
		g := gates.Gate{ID: record.PolicySecurityPrefix + id, Kind: "policy-proof", Name: id, URL: y.URL}
		for _, s := range []gh.SecuritySnapshot{x, y} {
			if s.URL != "" {
				d.Evidence = append(d.Evidence, signal.Evidence{Kind: signal.KindFile, URL: s.URL, Note: s.What})
			}
		}
		if !xok || !yok {
			unknown(g.ID, "security snapshot missing", g.URL)
			continue
		}
		equal, err := x.Equal(y)
		if err != nil {
			unknown(g.ID, err.Error(), g.URL)
			continue
		}
		if equal {
			continue
		}
		if x.Absent || y.Absent {
			// Only branch protection has an explicit no-protection state.
			switch {
			case id != "branch protection" || x.URL != y.URL:
				unknown(g.ID, "unclassified security state change", g.URL)
			case x.Absent:
				g.Detail = "branch protection added where none was configured"
				d.Strengthened = append(d.Strengthened, g)
			default:
				g.Detail = "branch protection removed"
				d.Loosened = append(d.Loosened, g)
			}
			continue
		}
		weak, strong, unverified := securityDirection(x.Raw, y.Raw)
		if x.URL != y.URL {
			unverified = true
		}
		if unverified {
			unknown(g.ID, "unclassified raw security change", g.URL)
		}
		if weak {
			g.Detail = "known weaker security policy"
			d.Loosened = append(d.Loosened, g)
		}
		if strong {
			g.Detail = "known stronger security policy"
			d.Strengthened = append(d.Strengthened, g)
		}
	}
	// A workflow file whose recorded full SHA-256 is equal on both sides, and
	// parsed on both, is byte-identical: its own text is unchanged, so it
	// needs no attestation. VERIFIED covers .github/workflows files only. What
	// the file runs but does not contain can still change between the sides:
	// local composite actions (uses: ./...), scripts a step runs, and remote
	// actions on moving refs. None of those is compared here. Any other file
	// lacks a completeness attestation in the current producer: retain its
	// evidence, refuse to certify omitted execution policy, and explain the
	// change in WorkflowDiffs.
	// The proof ID binds the after-side raw file hash, so an exemption covers
	// exactly the attested file; any edit needs a fresh decision. A path the
	// after side shows is gone (deleted or renamed away) binds the before-side
	// hash instead: the exemption then covers exactly the file removed.
	// Each path is one entry, never one per side.
	after := workflowDigests(b.Sources)
	removed := removedWorkflowDigests(a, b)
	paths := map[string]string{}
	for n, inv := range []*gates.Inventory{a, b} {
		for _, s := range inv.Sources {
			if s.URL != "" {
				d.Evidence = append(d.Evidence, signal.Evidence{Kind: signal.KindFile, URL: s.URL, Path: s.Path, Note: [...]string{"before workflow file", "after workflow file"}[n]})
			}
			// The after side's URL names the file as it is now.
			if _, ok := paths[s.Path]; !ok || n == 1 {
				paths[s.Path] = s.URL
			}
		}
	}
	for _, path := range sortedKeys(paths) {
		id := record.PolicyWorkflowPrefix + path
		digest := after[path]
		if digest == "" {
			digest = removed[path]
		}
		if digest != "" {
			id += "@" + digest
		}
		diff, identical := workflowDiff(a, b, path, id, digest != "")
		if identical {
			continue
		}
		d.WorkflowDiffs = append(d.WorkflowDiffs, diff)
		what := "workflow file " + diff.Change
		if diff.Change == WorkflowSameHash {
			what = "workflow file hash equal but a side not parsed"
		}
		unknown(id, what+" (see workflow_diffs); execution proof lacks completeness attestation", paths[path])
	}
	// Both sides citing the same evidence (an unchanged security URL) is
	// one citation.
	evidence := []signal.Evidence{}
	for _, e := range d.Evidence {
		if !slices.Contains(evidence, e) {
			evidence = append(evidence, e)
		}
	}
	d.Evidence = evidence
	d.status()
	return d
}

// protectionProjectionEqual compares the typed branch protection projection.
// A side whose snapshot records the explicit no-protection state must carry no
// projection; the absent→present change is then classified from snapshots.
func protectionProjectionEqual(a, b *gates.Inventory) bool {
	absent := func(inv *gates.Inventory) bool {
		for _, s := range inv.Security {
			if s.What == "branch protection" && s.Absent {
				return true
			}
		}
		return false
	}
	xa, ya := absent(a), absent(b)
	if !xa && !ya {
		return reflect.DeepEqual(a.Protection, b.Protection)
	}
	return (!xa || a.Protection == nil) && (!ya || b.Protection == nil)
}

// workflowDigests maps each after-side workflow path to the digest of its
// recorded raw file hash (WorkflowSource.SHA256). A path that is missing,
// duplicated, or has an empty or malformed hash (a record from before hashes
// were recorded) gets no digest, so its proof ID stays digest-less and no
// exemption can match it.
func workflowDigests(sources []gates.WorkflowSource) map[string]string {
	out, seen := map[string]string{}, map[string]int{}
	for _, s := range sources {
		seen[s.Path]++
		if digest := fileDigest(s.SHA256); digest != "" {
			out[s.Path] = digest
		}
	}
	for path, n := range seen {
		if n != 1 {
			delete(out, path)
		}
	}
	return out
}

// removedWorkflowDigests maps each workflow path present only on the before
// side to the digest of its before-side raw file hash. The after side must
// positively show the file is gone: a path it names at all (as a source or an
// unavailable file) or an unreadable workflow directory binds nothing, and a
// before side without a valid, unique hash (a legacy record) gets no digest,
// so those IDs stay digest-less and unexemptable.
func removedWorkflowDigests(a, b *gates.Inventory) map[string]string {
	out := map[string]string{}
	afterNames := map[string]bool{}
	for _, s := range b.Sources {
		afterNames[s.Path] = true
	}
	for _, u := range b.Unavailable {
		if u.What == strings.TrimSuffix(record.WorkflowDir, "/") {
			return out
		}
		afterNames[u.What] = true
	}
	for path, digest := range workflowDigests(a.Sources) {
		if !afterNames[path] {
			out[path] = digest
		}
	}
	return out
}

// fileDigest is a recorded raw file SHA-256 (record.WorkflowDigestLen hex
// digits), or "" unless it is a full 64-digit lowercase hex hash.
func fileDigest(sha string) string {
	if len(sha) != 2*sha256.Size {
		return ""
	}
	for _, r := range sha {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return ""
		}
	}
	return sha
}

// unverifiedRaw records that the raw inventory cannot be certified.
func unverifiedRaw(d *GateDiff) {
	d.Unverified = append(d.Unverified, gates.Gate{ID: record.PolicyInventoryRaw, Kind: "policy-proof", Detail: "raw inventory contains unknown or omitted fields; execution proof cannot be certified"})
}
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func securityDirection(a, b json.RawMessage) (weak, strong, unknown bool) {
	decode := func(raw json.RawMessage) any {
		var v any
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		_ = dec.Decode(&v)
		return v
	}
	var walk func(any, any, []string)
	walk = func(x, y any, path []string) {
		if reflect.DeepEqual(x, y) {
			return
		}
		xm, xok := x.(map[string]any)
		ym, yok := y.(map[string]any)
		if xok && yok {
			keys := map[string]bool{}
			for k := range xm {
				keys[k] = true
			}
			for k := range ym {
				keys[k] = true
			}
			for k := range keys {
				xx, p := xm[k]
				yy, q := ym[k]
				if !p || !q {
					unknown = true
					continue
				}
				walk(xx, yy, append(append([]string(nil), path...), k))
			}
			return
		}
		// Match complete JSON key tokens, never a delimiter encoding: unknown
		// slash-containing keys must not impersonate a known nested policy.
		matches := func(keys ...string) bool { return slices.Equal(path, keys) }
		direction := 0
		switch {
		case matches("required_pull_request_reviews", "required_approving_review_count"), matches("wait_timer"):
			xn, xok := x.(json.Number)
			yn, yok := y.(json.Number)
			if xok && yok {
				xi, e := xn.Int64()
				yi, f := yn.Int64()
				if e == nil && f == nil && xi >= 0 && yi >= 0 {
					if yi > xi {
						direction = 1
					} else {
						direction = -1
					}
				}
			}
		case matches("required_status_checks", "strict"), matches("required_pull_request_reviews", "dismiss_stale_reviews"), matches("required_pull_request_reviews", "require_code_owner_reviews"), matches("required_pull_request_reviews", "require_last_push_approval"), matches("enforce_admins", "enabled"), matches("prevent_self_review"), matches("can_admins_bypass"):
			_, xok := x.(bool)
			yb, yok := y.(bool)
			if xok && yok {
				if yb {
					direction = 1
				} else {
					direction = -1
				}
				if matches("can_admins_bypass") {
					direction = -direction
				}
			}
		case matches("required_status_checks", "contexts"):
			xa, xok := x.([]any)
			ya, yok := y.([]any)
			if xok && yok {
				xs, ys := map[string]bool{}, map[string]bool{}
				valid := true
				for _, v := range xa {
					s, ok := v.(string)
					if !ok {
						valid = false
					}
					xs[s] = true
				}
				for _, v := range ya {
					s, ok := v.(string)
					if !ok {
						valid = false
					}
					ys[s] = true
				}
				if valid {
					for s := range xs {
						if !ys[s] {
							weak = true
						}
					}
					for s := range ys {
						if !xs[s] {
							strong = true
						}
					}
					return
				}
			}
		}
		switch direction {
		case 1:
			strong = true
		case -1:
			weak = true
		default:
			unknown = true
		}
	}
	ac, e := gh.CanonicalSecurity(a)
	bc, f := gh.CanonicalSecurity(b)
	if e != nil || f != nil {
		return false, false, true
	}
	walk(decode(ac), decode(bc), nil)
	return
}
func ApplyExemptions(d *GateDiff, es []record.Exemption, now time.Time) {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	var ids []string
	for _, list := range [][]gates.Gate{d.Removed, d.Loosened, d.Unverified} {
		for _, g := range list {
			ids = append(ids, g.ID)
		}
	}
	for _, e := range es {
		if strings.TrimSpace(e.ID) == "" || strings.TrimSpace(e.Waste) == "" || strings.TrimSpace(e.Reason) == "" || strings.TrimSpace(e.AgreedBy) == "" {
			continue
		}
		date, err := time.Parse("2006-01-02", e.Date)
		review, err2 := time.Parse("2006-01-02", e.ReviewBy)
		if err != nil || err2 != nil || date.After(today) || review.Before(today) || review.Before(date) {
			continue
		}
		id, ok := strings.CutPrefix(e.Location, "gate:")
		// A synthetic proof ID is exemptable only in its exact recognized
		// form: a digest-less workflow proof names unknown content.
		if ok && strings.HasPrefix(id, "policy:") && !record.IsPolicyProofID(id) {
			continue
		}
		if !ok {
			continue
		}
		// An older record's 12-hex exemption covers the emitted full-digest
		// ID it prefixes; Exempted names the emitted ID so Passed matches it.
		for _, g := range ids {
			if record.ProofCovers(id, g) {
				d.Exempted = append(d.Exempted, g)
			}
		}
	}
	sort.Strings(d.Exempted)
	d.Exempted = compact(d.Exempted)
	d.status()
}
func compact(xs []string) []string {
	out := []string{}
	for _, x := range xs {
		if len(out) == 0 || out[len(out)-1] != x {
			out = append(out, x)
		}
	}
	return out
}

func verifyGates(ctx context.Context, c *gh.Client, repo, path, ref string) *GateDiff {
	failure := func(err error) *GateDiff {
		return UnavailableGates(err.Error())
	}
	var raw []byte
	var es []record.Exemption
	var hist *record.Historical
	// Standard record reads use Store's guarded read boundary, including its
	// exact raw inventory preservation and schema validation.
	if filepath.Base(path) == "gates.toml" && filepath.Base(filepath.Dir(path)) == ".muda" {
		s, err := record.Open(filepath.Dir(filepath.Dir(path)))
		if err != nil {
			return failure(err)
		}
		raw, err = s.GatesJSON()
		if err != nil {
			return failure(err)
		}
		snap, err := s.Snapshot()
		if err != nil {
			return failure(err)
		}
		es, _ = snap["exemptions"].([]record.Exemption)
		if h, ok := snap["historical"].(record.Historical); ok {
			hist = &h
		}
	} else {
		var doc struct {
			Schema        int    `toml:"schema"`
			InventoryJSON string `toml:"inventory-json"`
			HistoricalRef string `toml:"historical-ref"`
			SettingsRead  string `toml:"settings-read"`
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return failure(err)
		}
		if _, err = toml.Decode(string(b), &doc); err != nil {
			return failure(err)
		}
		if doc.Schema != 1 {
			return failure(fmt.Errorf("unsupported gates file schema %d", doc.Schema))
		}
		raw = []byte(doc.InventoryJSON)
		if hist, err = record.HistoricalOf(doc.HistoricalRef, doc.SettingsRead); err != nil {
			return failure(err)
		}
	}
	var a gates.Inventory
	if err := json.Unmarshal(raw, &a); err != nil {
		return failure(err)
	}
	from, err := renamedFrom(ctx, c, a.Repo, repo)
	if err != nil {
		return failure(err)
	}
	if from != "" {
		if raw, err = renameRepo(raw, from, repo); err != nil {
			return failure(err)
		}
		a = gates.Inventory{}
		if err := json.Unmarshal(raw, &a); err != nil {
			return failure(err)
		}
	}
	if a.Schema != 1 || a.Repo != repo {
		return failure(fmt.Errorf("baseline must have schema 1 and repo %s", repo))
	}
	// A historical baseline's before side is its recorded commit; an
	// inventory read anywhere else is not that baseline.
	if hist != nil && a.Ref != hist.Ref {
		return failure(fmt.Errorf("the recorded inventory reads workflows at %q, not the baseline's historical ref %q", a.Ref, hist.Ref))
	}
	b, err := gates.Derive(ctx, c, repo, ref)
	if err != nil {
		return failure(err)
	}
	d := GateChanges(&a, b)
	// The producer's typed round-trip is only a field-presence check, not a
	// policy attestation. Compare the entire raw baseline against it, without
	// a whitelist: missing literal fields and unknown fields at any depth
	// cannot disappear silently. Known changes are assessed by GateChanges.
	projected, err := json.Marshal(&a)
	if err != nil {
		return failure(err)
	}
	if !rawInventoryEqual(raw, projected) {
		unverifiedRaw(d)
	}
	ApplyExemptions(d, es, time.Now().UTC())
	if hist != nil {
		d.BeforeRef, d.BeforeSettings = hist.Ref, hist.Settings
	}
	d.BeforeRepo = from
	return d
}

// rawInventoryEqual retains every field, including nested future fields and
// explicit empty literals. Numeric decoding never rounds policy values.
// Object order is insignificant; array order and field presence are not.
func rawInventoryEqual(a, b []byte) bool {
	decode := func(raw []byte) (any, error) {
		var v any
		d := json.NewDecoder(bytes.NewReader(raw))
		d.UseNumber()
		if err := d.Decode(&v); err != nil {
			return nil, err
		}
		return v, nil
	}
	x, e := decode(a)
	y, f := decode(b)
	return e == nil && f == nil && reflect.DeepEqual(x, y)
}
