package compare

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/schuettc/muda/internal/gh"
)

// renamedFrom accepts a record made under another repository name only when
// GitHub says that name is now repo: a rename or transfer redirects the old
// name. It returns the recorded name to rewrite, or "" when nothing differs.
func renamedFrom(ctx context.Context, c *gh.Client, recorded, repo string) (string, error) {
	if recorded == repo {
		return "", nil
	}
	current, err := c.FullName(ctx, recorded)
	if err != nil {
		return "", fmt.Errorf("record is for repository %s, not %s, and GitHub's current name for it is unavailable: %w", recorded, repo, err)
	}
	if !strings.EqualFold(current, repo) {
		return "", fmt.Errorf("record is for repository %s, which GitHub names %s, not %s", recorded, current, repo)
	}
	return recorded, nil
}

// renameRepo rewrites the recorded repository name to repo in every JSON
// string value: the whole value, or a path segment of a URL or API path. Only
// the name changes; a record made before a rename then compares as the same
// payloads read after it. Keys, numbers and field presence are untouched.
func renameRepo(raw []byte, from, repo string) ([]byte, error) {
	segment := regexp.MustCompile(`(?i)(/)` + regexp.QuoteMeta(from) + `([/?#]|$)`)
	var v any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(&v); err != nil {
		return nil, err
	}
	var walk func(any) any
	walk = func(x any) any {
		switch y := x.(type) {
		case map[string]any:
			for k, e := range y {
				y[k] = walk(e)
			}
		case []any:
			for i, e := range y {
				y[i] = walk(e)
			}
		case string:
			if strings.EqualFold(y, from) {
				return repo
			}
			return segment.ReplaceAllString(y, "${1}"+repo+"${2}")
		}
		return x
	}
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	if err := e.Encode(walk(v)); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}
