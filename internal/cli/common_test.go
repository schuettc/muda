package cli

import (
	"errors"
	"flag"
	"strings"
	"testing"
	"time"
)

func TestCommonResolveSince(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		input string
		want  time.Time
		bad   bool
	}{
		{"30d", now.Add(-720 * time.Hour), false},
		{"72h", now.Add(-72 * time.Hour), false},
		{"2026-09-01T00:00:00Z", time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), false},
		{"2026-09-01", time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), false},
		{"abc", time.Time{}, true},
	} {
		t.Run(tc.input, func(t *testing.T) {
			fs := flag.NewFlagSet("test", flag.ContinueOnError)
			c := AddCommon(fs)
			if err := fs.Parse([]string{"--repo", "o/r", "--since", tc.input}); err != nil {
				t.Fatal(err)
			}
			err := c.Resolve(func() (string, error) { t.Fatal("explicit repo should not read remote"); return "", nil }, func() time.Time { return now })
			if tc.bad {
				if err == nil || !strings.Contains(err.Error(), tc.input) {
					t.Fatalf("expected named error, got %v", err)
				}
				return
			}
			if err != nil || !c.Since.Equal(tc.want) {
				t.Fatalf("since=%v, err=%v; want %v", c.Since, err, tc.want)
			}
			if c.Format != "json" || c.NoCache {
				t.Fatalf("unexpected defaults: %+v", c)
			}
		})
	}
}

func TestCommonRemoteErrorDoesNotLeakCredentials(t *testing.T) {
	for _, remote := range []string{
		"https://ghp_SECRET@not-github.example/o/r.git",
		"https://user:ghp_SECRET@github.com/o/r/tree",
	} {
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		c := AddCommon(fs)
		err := c.Resolve(func() (string, error) { return remote, nil }, time.Now)
		if err == nil || strings.Contains(err.Error(), "ghp_SECRET") {
			t.Errorf("remote error must not expose credentials: %v", err)
		}
	}
}

func TestCommonResolveRepoFromRemote(t *testing.T) {
	for _, tc := range []struct {
		remote, want string
		bad          bool
	}{
		{"git@github.com:o/r.git", "o/r", false},
		{"https://github.com/o/r", "o/r", false},
		{"https://github.com/o/r.git", "o/r", false},
		{"https://example.com/o/r.git", "", true},
	} {
		t.Run(tc.remote, func(t *testing.T) {
			fs := flag.NewFlagSet("test", flag.ContinueOnError)
			c := AddCommon(fs)
			err := c.Resolve(func() (string, error) { return tc.remote, nil }, func() time.Time { return time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC) })
			if tc.bad {
				if err == nil || !strings.Contains(err.Error(), tc.remote) {
					t.Fatalf("expected error naming remote, got %v", err)
				}
				return
			}
			if err != nil || c.Repo != tc.want {
				t.Fatalf("repo=%q, err=%v; want %q", c.Repo, err, tc.want)
			}
		})
	}
}

// Final review M4 and the deferred ssh:// and trailing-slash remote forms.
func TestCommonResolveRemoteForms(t *testing.T) {
	for _, remote := range []string{
		"ssh://git@github.com/o/r.git",
		"ssh://git@github.com/o/r",
		"ssh://git@github.com/o/r/",
		"ssh://git@github.com/o/r.git/",
		"https://github.com/o/r/",
		"https://github.com/o/r.git/",
		"git@github.com:o/r/",
		"git@github.com:o/r.git/",
	} {
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		c := AddCommon(fs)
		if err := c.Resolve(func() (string, error) { return remote, nil }, time.Now); err != nil || c.Repo != "o/r" {
			t.Errorf("%s: repo=%q err=%v", remote, c.Repo, err)
		}
	}
	for _, remote := range []string{
		"ssh://git@example.com/o/r.git",
		"ssh://git:ghp_SECRET@github.com/o/r.git",
		"ssh://other@github.com/o/r.git",
		"ssh://git@github.com/o/r/extra",
		"ssh://git@github.com/o/r?x=ghp_SECRET",
	} {
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		c := AddCommon(fs)
		err := c.Resolve(func() (string, error) { return remote, nil }, time.Now)
		if err == nil || strings.Contains(err.Error(), "ghp_SECRET") || !strings.Contains(err.Error(), "--repo OWNER/NAME") {
			t.Errorf("%s: %v", remote, err)
		}
	}
}

func TestCommonMissingOriginSuggestsRepo(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	c := AddCommon(fs)
	err := c.Resolve(func() (string, error) { return "", errors.New("exit status 128") }, time.Now)
	if err == nil || !strings.Contains(err.Error(), "--repo OWNER/NAME") {
		t.Fatalf("missing origin error lacks --repo hint: %v", err)
	}
}

// The deferred future --since: a window starting at or after now is a usage
// error, never an empty report.
func TestCommonRejectsFutureSince(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	for _, since := range []string{"2026-09-29T12:00:00Z", "2026-09-30", "2027-01-01T00:00:00Z"} {
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		c := AddCommon(fs)
		if err := fs.Parse([]string{"--repo", "o/r", "--since", since}); err != nil {
			t.Fatal(err)
		}
		if err := c.Resolve(nil, func() time.Time { return now }); err == nil || !strings.Contains(err.Error(), since) {
			t.Errorf("%s accepted: %v", since, err)
		}
	}
}

// Final review M6: commands that take the common flags but ignore --since say so.
func TestHelpStatesIgnoredSince(t *testing.T) {
	for name, help := range map[string]string{"gates": gatesHelp, "compare": compareHelp, "logs": logsHelp} {
		if !strings.Contains(help, "--since is ignored") {
			t.Errorf("%s help does not state --since is ignored: %s", name, help)
		}
	}
}

// --until takes the same forms as --since, defaults to now, and must close a
// window that starts at --since and does not reach into the future.
func TestCommonResolveUntil(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		args []string
		want time.Time
		bad  string
	}{
		{"date", []string{"--since", "2026-09-01", "--until", "2026-09-15"}, time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC), ""},
		{"rfc3339", []string{"--since", "2026-09-01", "--until", "2026-09-15T06:30:00Z"}, time.Date(2026, 9, 15, 6, 30, 0, 0, time.UTC), ""},
		{"duration days", []string{"--until", "14d"}, now.Add(-14 * 24 * time.Hour), ""},
		{"duration hours", []string{"--until", "72h"}, now.Add(-72 * time.Hour), ""},
		{"omitted is now", []string{"--since", "2026-09-01"}, now, ""},
		{"explicit now", []string{"--since", "2026-09-01", "--until", "2026-09-29T12:00:00Z"}, now, ""},
		{"equal to since", []string{"--since", "2026-09-15", "--until", "2026-09-15"}, time.Time{}, "2026-09-15"},
		{"before since", []string{"--since", "2026-09-15", "--until", "2026-09-01"}, time.Time{}, "2026-09-01"},
		{"before default since", []string{"--until", "40d"}, time.Time{}, "40d"},
		{"future date", []string{"--since", "2026-09-01", "--until", "2026-09-30"}, time.Time{}, "2026-09-30"},
		{"future instant", []string{"--since", "2026-09-01", "--until", "2026-09-29T12:00:01Z"}, time.Time{}, "2026-09-29T12:00:01Z"},
		{"garbage", []string{"--until", "soon"}, time.Time{}, "soon"},
		{"zero duration", []string{"--until", "0d"}, time.Time{}, "0d"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs := flag.NewFlagSet("test", flag.ContinueOnError)
			c := AddCommon(fs)
			if err := fs.Parse(append([]string{"--repo", "o/r"}, tc.args...)); err != nil {
				t.Fatal(err)
			}
			err := c.Resolve(nil, func() time.Time { return now })
			if tc.bad != "" {
				if err == nil || !strings.Contains(err.Error(), "--until") || !strings.Contains(err.Error(), tc.bad) {
					t.Fatalf("expected a --until error naming %q, got %v", tc.bad, err)
				}
				return
			}
			if err != nil || !c.Until.Equal(tc.want) {
				t.Fatalf("until=%v err=%v; want %v", c.Until, err, tc.want)
			}
		})
	}
}

// Every command that takes the common flags but sets its own window says how
// --until is treated.
func TestHelpStatesIgnoredUntil(t *testing.T) {
	for name, help := range map[string]string{"gates": gatesHelp, "compare": compareHelp, "logs": logsHelp} {
		if !strings.Contains(help, "--until") {
			t.Errorf("%s help does not state how --until is treated: %s", name, help)
		}
	}
	for name, help := range map[string]string{"measure": measureHelp, "notices": noticesHelp, "scan": scanHelp, "compare": compareHelp} {
		if !strings.Contains(help, "[since, until)") {
			t.Errorf("%s help does not state the [since, until) window: %s", name, help)
		}
	}
}
