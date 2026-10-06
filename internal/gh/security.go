package gh

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"sort"
)

// SecuritySnapshot is a fail-closed comparison seam: Raw retains unknown fields,
// explicit nulls and field presence. Complete=false must never pass comparison.
// Any raw difference (including an unknown field) requires verification, not an
// assumption that the gate was unchanged. URL identifies the API evidence.
type SecuritySnapshot struct {
	What     string          `json:"what"`
	URL      string          `json:"url"`
	Raw      json.RawMessage `json:"raw,omitempty"`
	Complete bool            `json:"complete"`
	Why      string          `json:"why,omitempty"`
	// Absent is an explicit, complete "not configured" state (GitHub reports
	// the branch is not protected). It carries no Raw payload.
	Absent bool `json:"absent,omitempty"`
}

// Equal only certifies byte-equivalent complete canonical security payloads,
// or two explicit Absent states for the same identity. An Absent state against
// a complete payload is a valid, unequal comparison.
func (s SecuritySnapshot) Equal(other SecuritySnapshot) (bool, error) {
	if s.Absent || other.Absent {
		for _, x := range []SecuritySnapshot{s, other} {
			if !x.Complete || x.What == "" || x.URL == "" {
				return false, fmt.Errorf("security snapshot unavailable: %s / %s", s.What, other.What)
			}
			if x.Absent {
				if len(bytes.TrimSpace(x.Raw)) != 0 || x.Why != "" {
					return false, fmt.Errorf("security snapshot unavailable: absent %s carries a payload", x.What)
				}
				continue
			}
			if raw := bytes.TrimSpace(x.Raw); len(raw) == 0 || raw[0] != '{' {
				return false, fmt.Errorf("security snapshot unavailable: expected complete objects")
			}
			if _, err := CanonicalSecurity(x.Raw); err != nil {
				return false, err
			}
		}
		return s.Absent && other.Absent && s.What == other.What && s.URL == other.URL, nil
	}
	if !s.Complete || !other.Complete || s.What == "" || other.What == "" || s.URL == "" || other.URL == "" || len(bytes.TrimSpace(s.Raw)) == 0 || len(bytes.TrimSpace(other.Raw)) == 0 {
		return false, fmt.Errorf("security snapshot unavailable: %s / %s", s.What, other.What)
	}
	if bytes.TrimSpace(s.Raw)[0] != '{' || bytes.TrimSpace(other.Raw)[0] != '{' {
		return false, fmt.Errorf("security snapshot unavailable: expected complete objects")
	}
	a, err := CanonicalSecurity(s.Raw)
	if err != nil {
		return false, err
	}
	b, err := CanonicalSecurity(other.Raw)
	if err != nil {
		return false, err
	}
	return s.What == other.What && s.URL == other.URL && bytes.Equal(a, b), nil
}

// CanonicalSecurity preserves arbitrary JSON without inventing defaults or
// rounding numbers. Object keys and documented set-valued security arrays are
// sorted. Unknown array order remains significant. Null and absent differ.
func CanonicalSecurity(raw []byte) (json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("security payload has trailing JSON")
	}
	canonicalize(v, nil, "")
	return json.Marshal(v)
}

// Paths carry distinct key and array tokens, not dotted strings: an unknown
// key containing dots or '*' cannot accidentally inherit known set semantics.
func canonicalize(v any, path []string, ruleType string) {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			canonicalize(val, append(slices.Clone(path), "k:"+k), ruleType)
		}
	case []any:
		ruleCollection := len(path) == 0 || slices.Equal(path, []string{"k:rules"}) || slices.Equal(path, []string{"k:protection_rules"})
		for _, val := range x {
			childType := ruleType
			if ruleCollection {
				childType = ""
				if rule, ok := val.(map[string]any); ok {
					childType, _ = rule["type"].(string)
				}
			}
			canonicalize(val, append(slices.Clone(path), "a"), childType)
		}
		if unorderedSecurityPath(path, ruleType) {
			sort.SliceStable(x, func(i, j int) bool {
				a, _ := json.Marshal(x[i])
				b, _ := json.Marshal(x[j])
				return bytes.Compare(a, b) < 0
			})
		}
	}
}
func unorderedSecurityPath(path []string, ruleType string) bool {
	matches := func(tokens ...string) bool { return slices.Equal(path, tokens) }
	switch {
	case matches("k:rules"), matches("k:protection_rules"), matches("k:bypass_actors"), matches("k:required_status_checks", "k:contexts"), matches("k:required_status_checks", "k:checks"):
		return true
	case matches("k:rules", "a", "k:parameters", "k:required_status_checks"), matches("a", "k:parameters", "k:required_status_checks"):
		return ruleType == "required_status_checks"
	case matches("k:protection_rules", "a", "k:reviewers"):
		return ruleType == "required_reviewers"
	default:
		return false
	}
}
func rawSecurityObject(raw, existing json.RawMessage) (json.RawMessage, error) {
	if len(bytes.TrimSpace(existing)) > 0 && bytes.TrimSpace(existing)[0] == '{' {
		raw = existing
	}
	if len(bytes.TrimSpace(raw)) == 0 || bytes.TrimSpace(raw)[0] != '{' {
		return nil, fmt.Errorf("security object unavailable: expected JSON object")
	}
	return CanonicalSecurity(raw)
}
func (p *Protection) UnmarshalJSON(raw []byte) error {
	type plain Protection
	var v plain
	if err := json.Unmarshal(raw, &v); err != nil {
		return err
	}
	canon, err := rawSecurityObject(raw, v.RawSecurity)
	if err != nil {
		return err
	}
	*p = Protection(v)
	p.RawSecurity = canon
	return nil
}
func (p *Environment) UnmarshalJSON(raw []byte) error {
	type plain Environment
	var v plain
	if err := json.Unmarshal(raw, &v); err != nil {
		return err
	}
	canon, err := rawSecurityObject(raw, v.RawSecurity)
	if err != nil {
		return err
	}
	*p = Environment(v)
	p.RawSecurity = canon
	return nil
}
func (p *Ruleset) UnmarshalJSON(raw []byte) error {
	type plain Ruleset
	var v plain
	if err := json.Unmarshal(raw, &v); err != nil {
		return err
	}
	canon, err := rawSecurityObject(raw, v.RawSecurity)
	if err != nil {
		return err
	}
	*p = Ruleset(v)
	p.RawSecurity = canon
	return nil
}
