package record

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestCredentialLiteralVersusReference(t *testing.T) {
	for _, key := range []string{"token", "password", "secret", "authorization"} {
		for i, value := range []string{"${{ secrets.CI_TOKEN }}", "${{ github.token }}", "${{ inputs.token }}", "${{ env.TOKEN }}"} {
			t.Run(fmt.Sprintf("%s-reference-%d", key, i), func(t *testing.T) {
				b, err := json.Marshal(map[string]any{"schema": 1, key: value})
				if err != nil {
					t.Fatal(err)
				}
				s := store(t)
				if err := s.SetGatesJSON(b); err != nil {
					t.Fatal(err)
				}
				if _, err := s.Snapshot(); err != nil {
					t.Fatal(err)
				}
				if ps := s.Check(); len(ps) != 0 {
					t.Fatal(ps)
				}
			})
		}
		for i, value := range []string{"literal-value", "literal-${{ secrets.CI_TOKEN }}", "${{ secrets.CI_TOKEN }}-literal", "${{ 'literal-value' }}", "${{ secrets.CI_TOKEN || 'literal-value' }}", "${{ secrets.CI_TOKEN }} ${{ github.token }}", "${{ secrets.CI_TOKEN }", "${{ secrets.ghp_abcdefghijklmnop }}", "${{ secrets.CI_TOKEN }} ghp_abcdefghijklmnop", "${{ secrets.CI_TOKEN }} -----BEGIN PRIVATE KEY-----", "${{ secrets.CI_TOKEN }} https://user:password@example.com"} {
			t.Run(fmt.Sprintf("%s-literal-%d", key, i), func(t *testing.T) {
				b, err := json.Marshal(map[string]any{"schema": 1, key: value})
				if err != nil {
					t.Fatal(err)
				}
				if err := store(t).SetGatesJSON(b); err == nil {
					t.Fatal("credential accepted")
				}
			})
		}
	}
	for i, text := range []string{`token: "${{ secrets.CI_TOKEN }}"`, `password = '${{ inputs.password }}'`} {
		if err := noSecrets([]byte(text)); err != nil {
			t.Fatalf("reference %d rejected: %v", i, err)
		}
	}
	for i, text := range []string{`token: "literal-${{ secrets.CI_TOKEN }}"`, `token: '${{ secrets.CI_TOKEN }}"literal'`, `token: "${{ secrets.CI_TOKEN }}'literal"`, `password = '${{ inputs.password }}literal'`, `token: "${{ 'literal' }}"`, `note: "${{ secrets.CI_TOKEN }} ghp_abcdefghijklmnop"`, `note: "${{ secrets.CI_TOKEN }} -----BEGIN PRIVATE KEY-----"`, `note: "${{ secrets.CI_TOKEN }} https://user:password@example.com"`} {
		if err := noSecrets([]byte(text)); err == nil {
			t.Fatalf("literal %d accepted", i)
		}
	}
}

// A URL whose password is a whole shell or context reference names where a
// credential comes from at run time; it holds none.
func TestCredentialURLReference(t *testing.T) {
	for i, url := range []string{"https://user:${TOKEN}@example.com/simple/", "https://user:$TOKEN@example.com", "https://user:${{ secrets.CI_TOKEN }}@example.com", "https://user:${{secrets.CI_TOKEN}}@example.com"} {
		b, err := json.Marshal(map[string]any{"schema": 1, "run": `echo "url=` + url + `" >> "$GITHUB_OUTPUT"`})
		if err != nil {
			t.Fatal(err)
		}
		if err := noSecrets(b); err != nil {
			t.Errorf("reference %d rejected: %v", i, err)
		}
	}
	for i, url := range []string{"https://user:password@example.com", "https://user:${TOKEN}literal@example.com", "https://user:literal${TOKEN}@example.com", "https://user:${TOKEN@example.com", "https://user:${{ secrets.CI_TOKEN }}x@example.com", "https://user:${TOKEN}@example.com https://user:password@example.com"} {
		b, err := json.Marshal(map[string]any{"schema": 1, "run": `echo "url=` + url + `"`})
		if err != nil {
			t.Fatal(err)
		}
		if err := noSecrets(b); err == nil {
			t.Errorf("literal %d accepted", i)
		}
	}
}
