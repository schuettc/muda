package ghtest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// NonFileWorkflows wraps base so that the .github/workflows directory listing
// shows each path in symlinks as a "symlink" entry, and adds each path in
// submodules as an entry. Both shapes are what the contents API returns,
// checked against public repositories on 2026-10-01: a symlink is listed with
// type "symlink", and fetching it returns its target's content as a "file"
// when the target is a regular file (base's recorded answer stands in for
// that). A submodule is listed with type "file" for backwards compatibility,
// and only fetching it shows type "submodule".
func NonFileWorkflows(base http.RoundTripper, symlinks, submodules []string) http.RoundTripper {
	return nonFile{base: base, symlinks: symlinks, submodules: submodules}
}

type nonFile struct {
	base                 http.RoundTripper
	symlinks, submodules []string
}

func (n nonFile) RoundTrip(req *http.Request) (*http.Response, error) {
	const dir = "/contents/.github/workflows"
	for _, p := range n.submodules {
		if strings.HasSuffix(req.URL.Path, "/contents/"+p) {
			body := fmt.Sprintf(`{"type":"submodule","path":%q,"sha":"0123456789abcdef0123456789abcdef01234567","submodule_git_url":"https://github.com/o/other.git"}`, p)
			return respond(req, body), nil
		}
	}
	resp, err := n.base.RoundTrip(req)
	if err != nil || !strings.HasSuffix(req.URL.Path, dir) || resp.StatusCode != 200 {
		return resp, err
	}
	raw, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return nil, err
	}
	var entries []map[string]any
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, err
	}
	for _, e := range entries {
		for _, p := range n.symlinks {
			if e["path"] == p {
				e["type"] = "symlink"
				e["target"] = "../../shared/ci.yml"
			}
		}
	}
	for _, p := range n.submodules {
		entries = append(entries, map[string]any{"type": "file", "path": p, "name": p[strings.LastIndex(p, "/")+1:]})
	}
	out, err := json.Marshal(entries)
	if err != nil {
		return nil, err
	}
	return respond(req, string(out)), nil
}

func respond(req *http.Request, body string) *http.Response {
	return &http.Response{StatusCode: 200, Status: "200 OK", Header: make(http.Header), Body: io.NopCloser(bytes.NewReader([]byte(body))), Request: req}
}
