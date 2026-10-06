package notices

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// fakeAPI answers GitHub REST paths from a table; the query is ignored. A
// path absent from the table is a test bug and fails the request loudly.
type fakeAPI map[string]fakeResp

type fakeResp struct {
	status int
	body   string
	err    error
}

func (f fakeAPI) RoundTrip(req *http.Request) (*http.Response, error) {
	r, ok := f[req.URL.Path]
	if !ok {
		return nil, errors.New("fake GitHub has no route for " + req.URL.Path)
	}
	if r.err != nil {
		return nil, r.err
	}
	if r.status == 0 {
		r.status = 200
	}
	return &http.Response{StatusCode: r.status, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader([]byte(r.body))), Request: req}, nil
}

// requireNoEmptySections fails when a markdown heading is followed by nothing
// or directly by a heading of the same or a higher level.
func requireNoEmptySections(t *testing.T, md string) {
	t.Helper()
	lines := strings.Split(md, "\n")
	level := func(s string) int {
		n := 0
		for n < len(s) && s[n] == '#' {
			n++
		}
		if n > 0 && n < len(s) && s[n] == ' ' {
			return n
		}
		return 0
	}
	for i, l := range lines {
		h := level(l)
		if h == 0 {
			continue
		}
		next := ""
		for _, m := range lines[i+1:] {
			if strings.TrimSpace(m) != "" {
				next = m
				break
			}
		}
		if next == "" || (level(next) > 0 && level(next) <= h) {
			t.Errorf("empty markdown section %q", l)
		}
	}
}
