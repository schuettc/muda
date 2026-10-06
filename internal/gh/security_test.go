package gh

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRawSecurityPairedWeakening(t *testing.T) {
	pairs := [][2]string{
		{`{"required_pull_request_reviews":{"dismiss_stale_reviews":true,"require_code_owner_reviews":true,"require_last_push_approval":true}}`, `{"required_pull_request_reviews":{"dismiss_stale_reviews":false,"require_code_owner_reviews":false,"require_last_push_approval":false}}`},
		{`{"name":"prod","protection_rules":[{"type":"required_reviewers","reviewers":[{"id":1}]},{"type":"wait_timer","wait_timer":20}],"can_admins_bypass":false}`, `{"name":"prod","protection_rules":[{"type":"required_reviewers","reviewers":[]},{"type":"wait_timer","wait_timer":0}],"can_admins_bypass":true}`},
		{`{"rules":[{"type":"pull_request","parameters":{"required_approving_review_count":2}},{"type":"required_status_checks","parameters":{"strict_required_status_checks_policy":true,"required_status_checks":[{"context":"test","integration_id":1}]}}]}`, `{"rules":[{"type":"pull_request","parameters":{"required_approving_review_count":0}},{"type":"required_status_checks","parameters":{"strict_required_status_checks_policy":false,"required_status_checks":[{"context":"test","integration_id":2}]}}]}`},
		{`{"unknown":{"future":null,"number":9007199254740993}}`, `{"unknown":{"number":9007199254740992}}`},
	}
	for _, pair := range pairs {
		for _, kind := range []string{"protection", "environment", "ruleset"} {
			var raw [2]json.RawMessage
			for i, s := range pair {
				switch kind {
				case "protection":
					var v Protection
					if err := json.Unmarshal([]byte(s), &v); err != nil {
						t.Fatal(err)
					}
					raw[i] = v.RawSecurity
				case "environment":
					var v Environment
					if err := json.Unmarshal([]byte(s), &v); err != nil {
						t.Fatal(err)
					}
					raw[i] = v.RawSecurity
				case "ruleset":
					var v Ruleset
					if err := json.Unmarshal([]byte(s), &v); err != nil {
						t.Fatal(err)
					}
					raw[i] = v.RawSecurity
				}
			}
			if string(raw[0]) == string(raw[1]) || len(raw[0]) == 0 {
				t.Fatalf("%s lost security: %s", kind, raw)
			}
		}
	}
	a, err := CanonicalSecurity([]byte(`{"protection_rules":[{"type":"wait_timer","wait_timer":1},{"type":"required_reviewers","reviewers":[{"id":2},{"id":1}]}],"unknown":[2,1]}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := CanonicalSecurity([]byte(`{"unknown":[2,1],"protection_rules":[{"reviewers":[{"id":1},{"id":2}],"type":"required_reviewers"},{"wait_timer":1,"type":"wait_timer"}]}`))
	if err != nil || string(a) != string(b) {
		t.Fatalf("unstable canonical: %s %s %v", a, b, err)
	}
	if !strings.Contains(string(a), `"unknown":[2,1]`) {
		t.Fatal("unknown order erased")
	}
}
func TestRulesetFullAndEffectiveSecurity(t *testing.T) {
	for _, forbidden := range []bool{false, true} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/repos/o/r/rules/branches/main":
				_, _ = fmt.Fprint(w, `[{"ruleset_id":7,"ruleset_name":"policy","type":"pull_request","source_type":"Organization","parameters":{"require_last_push_approval":true,"future":null}}]`)
			case "/repos/o/r/rulesets/7":
				if forbidden {
					w.WriteHeader(403)
					_, _ = fmt.Fprint(w, `{}`)
				} else {
					_, _ = fmt.Fprint(w, `{"id":7,"name":"policy","enforcement":"active","bypass_actors":[{"actor_id":1}],"rules":[{"type":"pull_request","parameters":{"required_approving_review_count":2}}],"future":"retained"}`)
				}
			default:
				t.Errorf("unexpected %s", r.URL)
				w.WriteHeader(404)
			}
		}))
		c := New(Options{BaseURL: s.URL, NoCache: true})
		sets, err := c.Rulesets(context.Background(), "o/r", "main")
		if err != nil || len(sets) != 1 {
			t.Fatalf("%+v %v", sets, err)
		}
		set := sets[0]
		if !strings.Contains(string(set.EffectiveRules), `"future":null`) {
			t.Fatal("effective source/parameters lost", set)
		}
		if forbidden {
			if set.DetailUnavailable == "" {
				t.Fatal("missing metadata green")
			}
		} else if !strings.Contains(string(set.RawSecurity), `"bypass_actors"`) {
			t.Fatal("details lost")
		}
		s.Close()
	}
}

func TestSecurityUnknownOrderNullAndRoundTrip(t *testing.T) {
	a, err := CanonicalSecurity([]byte(`{"future":{"rules":[1,2]},"optional":null}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := CanonicalSecurity([]byte(`{"future":{"rules":[2,1]},"optional":null}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(a) == string(b) {
		t.Fatal("unknown security order change erased")
	}
	p := Protection{}
	if err := json.Unmarshal([]byte(`{"required_pull_request_reviews":null,"future":42}`), &p); err != nil {
		t.Fatal(err)
	}
	serialized, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var again Protection
	if err := json.Unmarshal(serialized, &again); err != nil {
		t.Fatal(err)
	}
	if string(p.RawSecurity) != string(again.RawSecurity) {
		t.Fatal("roundtrip replaced authoritative raw payload")
	}
	for _, v := range []any{&Protection{}, &Environment{}, &Ruleset{}} {
		if err := json.Unmarshal([]byte(`null`), v); err == nil {
			t.Fatal("missing security object accepted as complete")
		}
	}
	s := SecuritySnapshot{What: "branch protection", URL: "https://api.github.com/repos/o/r/branches/main/protection", Raw: a, Complete: true}
	equal, err := s.Equal(SecuritySnapshot{What: s.What, URL: s.URL, Raw: b, Complete: true})
	if err != nil || equal {
		t.Fatalf("unknown change passed %t %v", equal, err)
	}
	if equal, err := s.Equal(SecuritySnapshot{What: s.What, Raw: a, Complete: false}); err == nil || equal {
		t.Fatal("incomplete snapshot passed")
	}
}

func TestMissingSecurityListsAreNotEmptySuccess(t *testing.T) {
	for _, endpoint := range []string{"rulesets", "environments"} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if endpoint == "rulesets" {
				_, _ = fmt.Fprint(w, `null`)
			} else {
				_, _ = fmt.Fprint(w, `{"environments":null}`)
			}
		}))
		c := New(Options{BaseURL: s.URL, NoCache: true})
		var err error
		if endpoint == "rulesets" {
			_, err = c.Rulesets(context.Background(), "o/r", "main")
		} else {
			_, err = c.Environments(context.Background(), "o/r")
		}
		if err == nil {
			t.Error("missing security list became empty success", endpoint)
		}
		s.Close()
	}
}

func TestSnapshotEqualityFailsClosedWithoutEvidence(t *testing.T) {
	for _, s := range []SecuritySnapshot{{What: "gate", URL: "https://api.github.com/repos/o/r", Raw: json.RawMessage(`null`), Complete: true}, {What: "gate", Raw: json.RawMessage(`{}`), Complete: true}, {URL: "https://api.github.com/repos/o/r", Raw: json.RawMessage(`{}`), Complete: true}} {
		peer := s
		if equal, err := s.Equal(peer); err == nil || equal {
			t.Fatal("invalid security proof accepted", s)
		}
	}
	a := SecuritySnapshot{What: "gate", URL: "https://api.github.com/repos/o/r", Raw: json.RawMessage(`{}`), Complete: true}
	b := a
	b.URL = "https://api.github.com/repos/o/other"
	if equal, err := a.Equal(b); err != nil || equal {
		t.Fatal("different source identities treated as same gate")
	}
}

func TestMissingSecurityIdentityFailsClosed(t *testing.T) {
	for _, endpoint := range []string{"rulesets", "environments"} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case endpoint == "environments":
				_, _ = fmt.Fprint(w, `{"environments":[{"can_admins_bypass":false}]}`)
			case strings.Contains(r.URL.Path, "/rulesets/"):
				_, _ = fmt.Fprint(w, `{"id":0,"name":"invented"}`)
			default:
				_, _ = fmt.Fprint(w, `[{"type":"pull_request","parameters":{"require_last_push_approval":true}}]`)
			}
		}))
		c := New(Options{BaseURL: s.URL, NoCache: true})
		var err error
		if endpoint == "rulesets" {
			_, err = c.Rulesets(context.Background(), "o/r", "main")
		} else {
			_, err = c.Environments(context.Background(), "o/r")
		}
		if err == nil {
			t.Error("unknown identity became complete security", endpoint)
		}
		s.Close()
	}
}

func TestUnknownDottedSecurityKeysRemainSignificant(t *testing.T) {
	a, err := CanonicalSecurity([]byte(`{"required_status_checks.contexts":[1,2],"rules.*.parameters.required_status_checks":[1,2]}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := CanonicalSecurity([]byte(`{"required_status_checks.contexts":[2,1],"rules.*.parameters.required_status_checks":[2,1]}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(a) == string(b) {
		t.Fatal("unknown dotted key borrowed known path sorting semantics")
	}
}

func TestUnknownRuleParametersKeepOrder(t *testing.T) {
	for _, pair := range [][2]string{
		{`{"rules":[{"type":"future","parameters":{"required_status_checks":[{"context":"a"},{"context":"b"}]}}]}`, `{"rules":[{"type":"future","parameters":{"required_status_checks":[{"context":"b"},{"context":"a"}]}}]}`},
		{`{"protection_rules":[{"type":"future","reviewers":[{"id":1},{"id":2}]}]}`, `{"protection_rules":[{"type":"future","reviewers":[{"id":2},{"id":1}]}]}`},
	} {
		a, err := CanonicalSecurity([]byte(pair[0]))
		if err != nil {
			t.Fatal(err)
		}
		b, err := CanonicalSecurity([]byte(pair[1]))
		if err != nil {
			t.Fatal(err)
		}
		if string(a) == string(b) {
			t.Fatal("unknown rule borrowed known parameter sorting semantics")
		}
	}
}
