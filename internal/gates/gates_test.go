package gates

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/schuettc/muda/internal/ghtest"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/schuettc/muda/internal/gh"
	"github.com/schuettc/muda/internal/workflow"
)

func derive(t *testing.T, forbidden bool, yaml string) *Inventory {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/o/r":
			_, _ = fmt.Fprint(w, `{"default_branch":"main"}`)
		case "/repos/o/r/branches/main/protection":
			if forbidden {
				w.WriteHeader(403)
				_, _ = fmt.Fprint(w, `{}`)
			} else {
				_, _ = fmt.Fprint(w, `{"required_status_checks":{"contexts":["test"],"strict":true},"enforce_admins":{"enabled":true},"required_pull_request_reviews":{"required_approving_review_count":2}}`)
			}
		case "/repos/o/r/rules/branches/main":
			_, _ = fmt.Fprint(w, `[{"type":"required_status_checks","ruleset_id":1,"ruleset_name":"policy","parameters":{"required_status_checks":[{"context":"lint"}]}}]`)
		case "/repos/o/r/rulesets/1":
			w.WriteHeader(403)
			_, _ = fmt.Fprint(w, `{}`)
		case "/repos/o/r/environments":
			_, _ = fmt.Fprint(w, `{"environments":[{"name":"prod","protection_rules":[{"type":"required_reviewers"}]}]}`)
		case "/repos/o/r/contents/.github/workflows":
			_, _ = fmt.Fprint(w, `[{"type":"file","path":".github/workflows/deploy.yml"}]`)
		case "/repos/o/r/contents/.github/workflows/deploy.yml":
			_, _ = fmt.Fprintf(w, `{"type":"file","encoding":"base64","content":%q}`, base64.StdEncoding.EncodeToString([]byte(yaml)))
		default:
			t.Errorf("unexpected %s", r.URL)
			w.WriteHeader(404)
		}
	}))
	defer s.Close()
	inv, err := Derive(context.Background(), gh.New(gh.Options{BaseURL: s.URL, HTTP: s.Client(), NoCache: true}), "o/r", "")
	if err != nil {
		t.Fatal(err)
	}
	return inv
}

const deploy = "name: Deploy\non: push\njobs:\n  test:\n    steps:\n      - run: go test ./...\n  deploy:\n    needs: test\n    environment: prod\n"

func TestGatesProtectionForbidden(t *testing.T) {
	inv := derive(t, true, deploy)
	if len(inv.Unavailable) != 2 || inv.Unavailable[0].What != "branch protection" || !strings.HasPrefix(inv.Unavailable[0].Why, "needs admin read") {
		t.Fatalf("%+v", inv)
	}
	kinds := map[string]bool{}
	for _, g := range inv.Gates {
		kinds[g.Kind] = true
	}
	if !kinds["ruleset-check"] || !kinds["needs"] {
		t.Fatal(inv.Gates)
	}
}
func TestGatesFromRulesetAndNeeds(t *testing.T) {
	inv := derive(t, false, deploy)
	if inv.Protection == nil || !inv.Protection.RequiredStatusChecks.Strict || !inv.Protection.EnforceAdmins.Enabled || inv.Protection.RequiredPullRequestReviews.RequiredApprovingReviewCount != 2 {
		t.Fatalf("attributes lost: %+v", inv)
	}
	if len(inv.Rulesets) != 1 || len(inv.Environments) != 1 {
		t.Fatal(inv)
	}
	ids := map[string]bool{}
	for _, g := range inv.Gates {
		if ids[g.ID] {
			t.Fatal("duplicate", g.ID)
		}
		ids[g.ID] = true
	}
	if !ids["required-check:main:test"] || !ids["ruleset-check:main:policy/lint"] || !ids["needs:main:.github/workflows/deploy.yml/deploy/test"] {
		t.Fatal(ids)
	}
}
func TestGatesReusableAndMatrix(t *testing.T) {
	yaml := deploy + "  call:\n    uses: org/repo/.github/workflows/test.yml@v1\n    strategy:\n      matrix: ${{ fromJSON(inputs.matrix) }}\n    environment: ${{ matrix.env }}\n"
	inv := derive(t, false, yaml)
	found := false
	for _, j := range inv.Workflows[0].Jobs {
		if j.ID == "call" {
			found = j.Matrix && j.Uses == "org/repo/.github/workflows/test.yml@v1" && j.Environment == "${{ matrix.env }}"
		}
	}
	if !found {
		t.Fatal(inv.Workflows)
	}
}

func TestGateSourcesKeepLiteralMatrix(t *testing.T) {
	inv := derive(t, false, deploy+"  call:\n    uses: org/repo/.github/workflows/test.yml@v1\n    strategy:\n      matrix: ${{ fromJSON(inputs.matrix) }}\n")
	found := false
	for _, j := range inv.Sources[0].Jobs {
		if j.ID == "call" {
			found = j.Matrix == "${{ fromJSON(inputs.matrix) }}"
		}
	}
	if !found {
		t.Fatal(inv.Sources)
	}
}
func TestTestActionsDoNotTreatCoverageUploadAsTesting(t *testing.T) {
	for _, tc := range []struct {
		action string
		want   bool
	}{{"codecov/codecov-action@v5", false}, {"golangci/golangci-lint-action@v8", true}, {"cypress-io/github-action@v6", true}} {
		f, err := workflow.Parse("test.yml", []byte("jobs:\n  test:\n    steps:\n      - uses: "+tc.action+"\n"))
		if err != nil {
			t.Fatal(err)
		}
		if got := tests(f.Jobs["test"]); got != tc.want {
			t.Errorf("%s got %t want %t", tc.action, got, tc.want)
		}
	}
}

func TestGatesRecordedReplay(t *testing.T) {
	c := gh.New(gh.Options{NoCache: true, HTTP: &http.Client{Transport: ghtest.Replay("../ghtest/testdata/tackle")}})
	inv, err := Derive(context.Background(), c, "schuettc/tackle", "")
	if err != nil {
		t.Fatal(err)
	}
	// The recorded "Branch not protected" 404 is an explicit, complete
	// no-protection state, not an unavailable permission problem.
	if len(inv.Gates) != 2 || len(inv.Unavailable) != 0 || inv.Protection != nil {
		t.Fatalf("%+v", inv)
	}
	absent := false
	for _, s := range inv.Security {
		if s.What == "branch protection" {
			absent = s.Absent && s.Complete && s.Why == "" && len(s.Raw) == 0 && s.URL == "https://api.github.com/repos/schuettc/tackle/branches/main/protection"
		}
	}
	if !absent {
		t.Fatalf("no explicit no-protection snapshot: %+v", inv.Security)
	}
	var a, b bytes.Buffer
	if err := WriteJSON(&a, inv); err != nil {
		t.Fatal(err)
	}
	again, err := Derive(context.Background(), c, "schuettc/tackle", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteJSON(&b, again); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Fatal("nondeterministic JSON")
	}
	md := RenderMarkdown(inv)
	if !strings.Contains(md, "No branch protection: GitHub reports `main` is not protected.") || strings.Contains(md, "not found or no access") {
		t.Fatalf("markdown misreports no protection:\n%s", md)
	}
	for _, g := range inv.Gates {
		if !strings.Contains(md, g.URL) {
			t.Fatal("missing evidence", g.URL)
		}
	}
}

func TestIncompleteRulesetSecurityUnavailable(t *testing.T) {
	inv := derive(t, false, deploy)
	found := false
	for _, u := range inv.Unavailable {
		if strings.Contains(u.What, "ruleset") {
			found = true
		}
	}
	if !found {
		t.Fatal("complete ruleset details unreadable but inventory green")
	}
	if len(inv.Security) == 0 {
		t.Fatal("no raw comparison snapshots")
	}
	for _, s := range inv.Security {
		if strings.Contains(s.What, "ruleset") && s.Complete {
			t.Fatal("unreadable metadata complete", s)
		}
	}
}

func TestWorkflowExecutionSnapshotChanges(t *testing.T) {
	base := deploy
	snapshots := []string{}
	for _, yaml := range []string{base, strings.Replace(base, "    steps:", "    if: false\n    steps:", 1), strings.Replace(base, "- run: go test ./...", "- run: echo skipped", 1), strings.Replace(base, "    needs: test", "    if: always()\n    needs: test", 1), strings.Replace(base, "- run: go test ./...", "- if: false\n        run: go test ./...", 1)} {
		inv := derive(t, false, yaml)
		b, err := json.Marshal(inv.Sources)
		if err != nil {
			t.Fatal(err)
		}
		snapshots = append(snapshots, string(b))
		for _, source := range inv.Sources {
			if source.URL == "" {
				t.Fatal("source without evidence")
			}
			for _, j := range source.Jobs {
				if j.Line < 1 || j.URL == "" {
					t.Fatal("job without line evidence")
				}
				for _, step := range j.Steps {
					if step.Line < 1 || step.URL == "" {
						t.Fatal("step without line evidence")
					}
				}
			}
		}
		for _, g := range inv.Gates {
			if g.Kind == "needs" && !strings.Contains(g.Detail, "not proof") {
				t.Fatal("needs overclaimed", g)
			}
		}
	}
	for i := 1; i < len(snapshots); i++ {
		if snapshots[i] == snapshots[0] {
			t.Fatalf("variant %d execution lost", i)
		}
	}
}

func TestMarkdownCompleteSecurityAndWorkflowEvidence(t *testing.T) {
	inv := derive(t, false, deploy+"  call:\n    if: always()\n    uses: org/repo/.github/workflows/test.yml@v1\n    strategy:\n      matrix: ${{ fromJSON(inputs.matrix) }}\n")
	inv.Security = append(inv.Security, gh.SecuritySnapshot{What: "ruleset:security-only", URL: "https://api.github.com/repos/o/r/rulesets/2", Complete: true, Raw: json.RawMessage(`{"rules":[{"type":"pull_request","parameters":{"require_last_push_approval":true}}],"bypass_actors":[{"actor_id":7}]}`)})
	md := RenderMarkdown(inv)
	for _, text := range []string{"require_last_push_approval", "bypass_actors", "always()", "org/repo/.github/workflows/test.yml@v1", "${{ fromJSON(inputs.matrix) }}", "go test ./..."} {
		if !strings.Contains(md, text) {
			t.Errorf("markdown drops %q", text)
		}
	}
	for _, s := range inv.Security {
		if !strings.Contains(md, s.URL) {
			t.Errorf("security evidence absent: %s", s.URL)
		}
	}
	for _, f := range inv.Sources {
		if !strings.Contains(md, f.URL) {
			t.Fatal("fileURL missing")
		}
		for _, j := range f.Jobs {
			if !strings.Contains(md, j.URL) {
				t.Fatal("jobURL missing")
			}
			for _, step := range j.Steps {
				if !strings.Contains(md, step.URL) {
					t.Fatal("stepURL missing")
				}
			}
		}
	}
	for _, r := range inv.Rulesets {
		if !strings.Contains(md, r.EffectiveURL) {
			t.Fatal("effective rules evidence absent")
		}
	}
}

func TestExecutionEnvAndActionInputsRetained(t *testing.T) {
	yaml := "name: Deploy\non: push\nenv: {CHECKS: enabled}\njobs:\n  test:\n    env: {RUN_TESTS: 'true'}\n    steps:\n      - uses: golangci/golangci-lint-action@v8\n        env: {MODE: strict}\n        with: {args: --timeout=5m}\n  deploy:\n    needs: test\n"
	inv := derive(t, false, yaml)
	encoded, err := json.Marshal(inv.Sources)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"CHECKS", "RUN_TESTS", "MODE", "--timeout=5m"} {
		if !strings.Contains(string(encoded), text) {
			t.Errorf("missing literal env/action input %s", text)
		}
	}
	weaker := derive(t, false, strings.Replace(yaml, "MODE: strict", "MODE: disabled", 1))
	b, err := json.Marshal(weaker.Sources)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) == string(b) {
		t.Fatal("step environment weakening invisible")
	}
	md := RenderMarkdown(inv)
	for _, text := range []string{"CHECKS", "RUN_TESTS", "MODE", "--timeout=5m"} {
		if !strings.Contains(md, text) {
			t.Errorf("markdown drops literal env/action input %s", text)
		}
	}
}

func TestSecurityOnlyRulesetAndReusableMatrixMarkdown(t *testing.T) {
	yaml := "name: Deploy\non: push\njobs:\n  call:\n    uses: org/repo/.github/workflows/test.yml@v1\n    if: always()\n    strategy:\n      matrix: ${{ fromJSON(inputs.matrix) }}\n"
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/o/r":
			_, _ = fmt.Fprint(w, `{"default_branch":"main"}`)
		case "/repos/o/r/branches/main/protection":
			w.WriteHeader(403)
			_, _ = fmt.Fprint(w, `{}`)
		case "/repos/o/r/rules/branches/main":
			_, _ = fmt.Fprint(w, `[{"ruleset_id":7,"ruleset_name":"review-only","type":"pull_request","parameters":{"required_approving_review_count":3}}]`)
		case "/repos/o/r/rulesets/7":
			_, _ = fmt.Fprint(w, `{"id":7,"enforcement":"active","bypass_actors":[{"actor_id":1,"bypass_mode":"pull_request"}],"rules":[{"type":"pull_request","parameters":{"required_approving_review_count":3,"require_code_owner_review":true,"future":null}}]}`)
		case "/repos/o/r/environments":
			_, _ = fmt.Fprint(w, `{"environments":[]}`)
		case "/repos/o/r/contents/.github/workflows":
			_, _ = fmt.Fprint(w, `[{"type":"file","path":".github/workflows/deploy.yml"}]`)
		case "/repos/o/r/contents/.github/workflows/deploy.yml":
			_, _ = fmt.Fprintf(w, `{"type":"file","encoding":"base64","content":%q}`, base64.StdEncoding.EncodeToString([]byte(yaml)))
		default:
			t.Errorf("unexpected %s", r.URL)
			w.WriteHeader(404)
		}
	}))
	defer s.Close()
	inv, err := Derive(context.Background(), gh.New(gh.Options{BaseURL: s.URL, NoCache: true}), "o/r", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Gates) != 0 {
		t.Fatal("invented status-check gate", inv.Gates)
	}
	complete := false
	for _, snapshot := range inv.Security {
		if snapshot.What == "ruleset:7" {
			complete = snapshot.Complete
		}
	}
	if !complete {
		t.Fatal("read security-only ruleset missing")
	}
	md := RenderMarkdown(inv)
	for _, text := range []string{`"required_approving_review_count": 3`, `"require_code_owner_review": true`, `"future": null`, "bypass_mode", "always()", "${{ fromJSON(inputs.matrix) }}", "org/repo/.github/workflows/test.yml@v1", s.URL + "/repos/o/r/rulesets/7", s.URL + "/repos/o/r/rules/branches/main", "#L4", "not a no-gates pass"} {
		if !strings.Contains(md, text) {
			t.Errorf("security-only Markdown dropped %q", text)
		}
	}
}

// Any other 404 on branch protection stays unavailable with its message.
func TestGatesProtectionGeneric404Unavailable(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/o/r":
			_, _ = fmt.Fprint(w, `{"default_branch":"main"}`)
		case "/repos/o/r/rules/branches/main":
			_, _ = fmt.Fprint(w, `[]`)
		case "/repos/o/r/environments":
			_, _ = fmt.Fprint(w, `{"environments":[]}`)
		case "/repos/o/r/contents/.github/workflows":
			_, _ = fmt.Fprint(w, `[]`)
		default:
			w.WriteHeader(404)
			_, _ = fmt.Fprint(w, `{"message":"Not Found"}`)
		}
	}))
	defer s.Close()
	inv, err := Derive(context.Background(), gh.New(gh.Options{BaseURL: s.URL, HTTP: s.Client(), NoCache: true}), "o/r", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Unavailable) != 1 || inv.Unavailable[0].What != "branch protection" || !strings.Contains(inv.Unavailable[0].Why, "not found or no access") {
		t.Fatalf("%+v", inv.Unavailable)
	}
	for _, snap := range inv.Security {
		if snap.What == "branch protection" && (snap.Absent || snap.Complete) {
			t.Fatalf("generic 404 became a no-protection state: %+v", snap)
		}
	}
}

// Final review M3: one unparseable workflow is a named unavailable entry, not
// an aborted inventory.
func TestUnparseableWorkflowIsUnavailable(t *testing.T) {
	for _, tc := range []struct{ name, yaml, why string }{
		{"merge key", "name: Deploy\non: push\njobs:\n  base: &b\n    steps:\n      - run: go test ./...\n  test:\n    <<: *b\n", "merge key"},
		{"malformed", "name: Deploy\non: push\njobs: [\n", ".github/workflows/deploy.yml"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inv := derive(t, false, tc.yaml)
			found := false
			for _, u := range inv.Unavailable {
				if u.What == ".github/workflows/deploy.yml" && strings.Contains(u.Why, tc.why) {
					found = true
				}
			}
			if !found {
				t.Fatalf("unparseable workflow not unavailable: %+v", inv.Unavailable)
			}
			if len(inv.Sources) != 0 || len(inv.Workflows) != 0 {
				t.Fatalf("unparseable workflow produced sources: %+v", inv.Sources)
			}
			// Everything else is still derived.
			if inv.Protection == nil || len(inv.Rulesets) != 1 || len(inv.Environments) != 1 {
				t.Fatalf("inventory aborted: %+v", inv)
			}
		})
	}
}
