package gh

import (
	"fmt"
	"strings"
)

// ResolveToken prefers explicit environment credentials, then the optional gh
// CLI. Errors never include a token or gh's stderr, which could contain one.
func ResolveToken(getenv func(string) string, ghToken func() (string, error)) (string, error) {
	for _, name := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
		if token := strings.TrimSpace(getenv(name)); token != "" {
			return token, nil
		}
	}
	if token, err := ghToken(); err == nil && strings.TrimSpace(token) != "" {
		return strings.TrimSpace(token), nil
	}
	return "", fmt.Errorf("GitHub authentication unavailable: set GH_TOKEN or GITHUB_TOKEN, or run gh auth token")
}
