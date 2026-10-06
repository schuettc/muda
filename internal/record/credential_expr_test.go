package record

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// gatesWith builds a gates-shaped inventory whose one step passes value as a
// credential-named action input.
func gatesWith(key, value string) []byte {
	b, _ := json.Marshal(map[string]any{"schema": 1, "workflows": []any{map[string]any{
		"path": ".github/workflows/ci.yml",
		"jobs": []any{map[string]any{"id": "release", "steps": []any{map[string]any{"uses": "actions/checkout@v4", "with": map[string]string{key: value}}}}},
	}}})
	return b
}

// Final review I3: whole-value expressions of context references, operators
// and function calls are references, not credentials.
func TestCredentialExpressionsOfReferences(t *testing.T) {
	for _, value := range []string{
		"${{ secrets.PAT || github.token }}",
		"${{ inputs.token || secrets.GITHUB_TOKEN }}",
		"${{ format(inputs.template, secrets.PAT, github.token) }}",
		"${{ github.event_name == inputs.event && secrets.PAT }}",
		"${{ !inputs.dry_run && (secrets.PAT || github.token) }}",
		"${{ inputs.use_pat != false && secrets.PAT || null }}",
		"${{secrets.PAT||github.token}}",
	} {
		s := store(t)
		if err := s.SetGatesJSON(gatesWith("token", value)); err != nil {
			t.Errorf("%s rejected: %v", value, err)
			continue
		}
		if ps := s.Check(); len(ps) != 0 {
			t.Errorf("%s: check %v", value, ps)
		}
	}
	for i, value := range []string{
		"${{ 'ghp_abcdefghijklmnopqrst' }}",
		"${{ 'not-a-token-shape' }}",
		"${{ secrets.X || 'literal' }}",
		"${{ secrets.X || \"literal\" }}",
		"literal-${{ secrets.X || github.token }}",
		"${{ secrets.X || github.token }}-literal",
		"${{ secrets.X }} ${{ github.token }}",
		"${{ secrets.X || 42 }}",
		"${{ format('{0}', secrets.X) }}",
		"${{ secrets.X || }}",
		"${{ || secrets.X }}",
		"${{ (secrets.X }}",
		"${{ secrets.X) }}",
		"${{ unknownfn(secrets.X) }}",
		"${{ bogus.X }}",
		"${{ secrets }}",
		"${{ secrets.X[0] }}",
		"${{ secrets.X | github.token }}",
		"${{ }}",
		"${{ secrets.ghp_abcdefghijklmnopqrst || github.token }}",
	} {
		err := store(t).SetGatesJSON(gatesWith("github-token", value))
		var p Problem
		if !errors.As(err, &p) || p.Code != "secret-detected" {
			t.Errorf("%s accepted: %v", value, err)
			continue
		}
		if strings.Contains(p.Detail, value) || strings.Contains(p.Error(), value) {
			t.Errorf("detail leaks the value: %q", p.Detail)
		}
		if !strings.Contains(p.Detail, `key "github-token"`) || !strings.Contains(p.Detail, "workflows[0].jobs[0].steps[0].with.github-token") {
			t.Errorf("%d: detail does not name the key: %q", i, p.Detail)
		}
	}
}

// The problem detail names the key and JSON path, never the value.
func TestCredentialDetailNamesKeyAndPath(t *testing.T) {
	value := "${{ secrets.X || 'hunter2-literal' }}"
	err := store(t).SetGatesJSON(gatesWith("github-token", value))
	var p Problem
	if !errors.As(err, &p) {
		t.Fatal(err)
	}
	for _, want := range []string{`"github-token"`, "workflows[0].jobs[0].steps[0].with.github-token"} {
		if !strings.Contains(p.Detail, want) {
			t.Errorf("detail %q lacks %q", p.Detail, want)
		}
	}
	for _, leak := range []string{"hunter2", "secrets.X", value} {
		if strings.Contains(p.Detail, leak) || strings.Contains(p.Error(), leak) {
			t.Errorf("detail leaks %q: %q", leak, p.Detail)
		}
	}
}
