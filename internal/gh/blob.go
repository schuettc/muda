package gh

import (
	"net/url"
	"strings"
)

// BlobURL is the github.com link to path at ref in repo. Each '/'-separated
// segment of ref and path is path-escaped, so a ref such as "feature#12"
// links to that ref rather than to "feature" plus a fragment, and a ref such
// as "muda/F-001" keeps its '/'. gates and scan share it, so one ref gives one
// link style.
func BlobURL(repo, ref, path string) string {
	return "https://github.com/" + repo + "/blob/" + EscapeSegments(ref) + "/" + EscapeSegments(path)
}

// EscapeSegments path-escapes each '/'-separated segment of p.
func EscapeSegments(p string) string {
	parts := strings.Split(p, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}
