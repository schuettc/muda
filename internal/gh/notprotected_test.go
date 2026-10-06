package gh

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/schuettc/muda/internal/ghtest"
)

// Final review I2: GitHub's recorded "Branch not protected" 404 is an explicit
// no-protection state, not an unavailable permission problem.
func TestBranchNotProtectedIsExplicitState(t *testing.T) {
	c := New(Options{NoCache: true, HTTP: &http.Client{Transport: ghtest.Replay(filepath.Join("..", "ghtest", "testdata", "tackle"))}})
	p, err := c.BranchProtection(context.Background(), "schuettc/tackle", "main")
	if err != nil {
		t.Fatalf("recorded unprotected branch is an error: %v", err)
	}
	if p == nil || !p.NotProtected || p.EvidenceURL != "https://api.github.com/repos/schuettc/tackle/branches/main/protection" || len(p.RawSecurity) != 0 {
		t.Fatalf("no-protection state = %+v", p)
	}
	// The fixture is the real recorded body; guard that it is what we match.
	b, err := os.ReadFile(filepath.Join("..", "ghtest", "testdata", "tackle", "GET__repos_schuettc_tackle_branches_main_protection_38013c852d0e.json"))
	if err != nil || !json404(b) {
		t.Fatalf("fixture changed: %s %v", b, err)
	}
}

func json404(b []byte) bool {
	return string(b) == `{"message":"Branch not protected","documentation_url":"https://docs.github.com/rest/branches/branch-protection#get-branch-protection","status":"404"}`
}

func TestOtherProtection404StaysUnavailable(t *testing.T) {
	for _, body := range []string{`{"message":"Not Found"}`, `{"message":"branch not protected"}`, `{"message":"Branch not protected."}`, `not json`, ``} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(404)
			_, _ = fmt.Fprint(w, body)
		}))
		p, err := New(Options{BaseURL: srv.URL, NoCache: true}).BranchProtection(context.Background(), "o/r", "main")
		srv.Close()
		var api *Error
		if p != nil || !errors.As(err, &api) || api.Status != 404 || api.Msg != "not found or no access (check repo, path and token permissions)" {
			t.Fatalf("%q: p=%+v err=%v", body, p, err)
		}
	}
	// A 403 with that message is still the permission path, not "no protection".
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		_, _ = fmt.Fprint(w, `{"message":"Branch not protected"}`)
	}))
	defer srv.Close()
	p, err := New(Options{BaseURL: srv.URL, NoCache: true}).BranchProtection(context.Background(), "o/r", "main")
	var api *Error
	if p != nil || !errors.As(err, &api) || api.Status != 403 {
		t.Fatalf("403 changed: p=%+v err=%v", p, err)
	}
}

func TestAbsentSnapshotEquality(t *testing.T) {
	url := "https://api.github.com/repos/o/r/branches/main/protection"
	absent := SecuritySnapshot{What: "branch protection", URL: url, Complete: true, Absent: true}
	present := SecuritySnapshot{What: "branch protection", URL: url, Complete: true, Raw: []byte(`{"enforce_admins":{"enabled":true}}`)}
	again := absent
	if eq, err := absent.Equal(again); err != nil || !eq {
		t.Fatalf("absent→absent: %v %v", eq, err)
	}
	for _, pair := range [][2]SecuritySnapshot{{absent, present}, {present, absent}} {
		if eq, err := pair[0].Equal(pair[1]); err != nil || eq {
			t.Fatalf("absent/present equal: %v %v", eq, err)
		}
	}
	for _, bad := range []SecuritySnapshot{
		{What: "branch protection", URL: url, Complete: false, Absent: true},
		{What: "branch protection", URL: url, Complete: true, Absent: true, Raw: []byte(`{}`)},
		{What: "branch protection", Complete: true, Absent: true},
	} {
		peer := bad
		if eq, err := bad.Equal(peer); err == nil || eq {
			t.Fatalf("invalid absent snapshot accepted: %+v", bad)
		}
		if eq, err := bad.Equal(present); err == nil || eq {
			t.Fatalf("invalid absent snapshot compared: %+v", bad)
		}
	}
}
