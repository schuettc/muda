package cli

import (
	"flag"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var repoName = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// Common holds the flags shared by commands that inspect a GitHub repository.
type Common struct {
	Repo    string
	Since   time.Time
	Until   time.Time // the window's exclusive end; now when --until is omitted
	Format  string
	NoCache bool
	since   string
	until   string
}

func AddCommon(fs *flag.FlagSet) *Common {
	c := &Common{}
	fs.StringVar(&c.Repo, "repo", "", "GitHub OWNER/NAME (default: origin remote)")
	fs.StringVar(&c.since, "since", "30d", "window start (RFC3339 or duration, e.g. 30d or 72h)")
	fs.StringVar(&c.until, "until", "", "window end, exclusive (RFC3339, YYYY-MM-DD or duration ago; default: now)")
	fs.StringVar(&c.Format, "format", "json", "output format: json or md")
	fs.BoolVar(&c.NoCache, "no-cache", false, "bypass disk cache")
	return c
}

// Resolve validates common flags, converts the time window, and resolves origin
// only when --repo was omitted. The injected functions keep git and time out of tests.
func (c *Common) Resolve(gitRemote func() (string, error), now func() time.Time) error {
	if c.Format != "json" && c.Format != "md" {
		return fmt.Errorf("unsupported format %q: expected json or md", c.Format)
	}
	since := c.since
	if since == "" {
		since = "30d"
	}
	at := now()
	var err error
	if c.Since, err = windowTime("--since", since, at); err != nil {
		return err
	}
	if !c.Since.Before(at) {
		return fmt.Errorf("invalid --since %q: the window must start before now", since)
	}
	c.Until = at
	if c.until != "" {
		if c.Until, err = windowTime("--until", c.until, at); err != nil {
			return err
		}
		if c.Until.After(at) {
			return fmt.Errorf("invalid --until %q: the window may not end in the future", c.until)
		}
		if !c.Since.Before(c.Until) {
			return fmt.Errorf("invalid --until %q: the window must end after --since %q", c.until, since)
		}
	}
	if c.Repo == "" {
		remote, err := gitRemote()
		if err != nil {
			return fmt.Errorf("no readable origin remote (%w); pass --repo OWNER/NAME", err)
		}
		c.Repo, err = githubRepo(remote)
		if err != nil {
			return fmt.Errorf("%w; pass --repo OWNER/NAME", err)
		}
	}
	if !repoName.MatchString(c.Repo) || strings.Contains(c.Repo, "..") {
		return fmt.Errorf("invalid GitHub repo %q: expected OWNER/NAME", c.Repo)
	}
	return nil
}

// windowTime parses a window bound: RFC3339, YYYY-MM-DD (UTC midnight) or a
// positive duration such as 30d or 72h, meaning that long before now.
func windowTime(name, value string, now time.Time) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02", value); err == nil {
		return t, nil
	}
	durationText := value
	if strings.HasSuffix(value, "d") {
		days, err := strconv.ParseInt(strings.TrimSuffix(value, "d"), 10, 64)
		if err != nil || days <= 0 || days > int64((1<<63-1)/int64(24*time.Hour)) {
			return time.Time{}, fmt.Errorf("invalid %s %q: expected positive duration or RFC3339", name, value)
		}
		durationText = fmt.Sprintf("%dh", days*24)
	}
	d, err := time.ParseDuration(durationText)
	if err != nil || d <= 0 {
		return time.Time{}, fmt.Errorf("invalid %s %q: expected positive duration or RFC3339", name, value)
	}
	return now.Add(-d), nil
}

// githubRepo accepts git@github.com:O/R, https://github.com/O/R and
// ssh://git@github.com/O/R, each with an optional .git and trailing slashes.
func githubRepo(remote string) (string, error) {
	path := ""
	if strings.HasPrefix(remote, "git@github.com:") {
		path = strings.TrimPrefix(remote, "git@github.com:")
	} else {
		u, err := url.Parse(remote)
		if err != nil {
			return "", fmt.Errorf("origin remote is not a valid GitHub URL")
		}
		sshUser := u.Scheme == "ssh" && u.User != nil && u.User.Username() == "git"
		if _, hasPassword := u.User.Password(); hasPassword {
			sshUser = false
		}
		if (u.Scheme != "https" && !sshUser) || u.Host != "github.com" || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery {
			return "", fmt.Errorf("origin remote %q is not a GitHub repository", safeRemote(u))
		}
		path = strings.TrimPrefix(u.Path, "/")
	}
	path = strings.TrimRight(path, "/")
	path = strings.TrimSuffix(path, ".git")
	if !repoName.MatchString(path) || strings.Contains(path, "..") {
		u, _ := url.Parse(remote)
		return "", fmt.Errorf("origin remote %q is not a GitHub OWNER/NAME repository", safeRemote(u))
	}
	return path, nil
}

// safeRemote keeps the host and path useful for diagnostics without echoing a
// token that a caller embedded as URL userinfo.
func safeRemote(u *url.URL) string {
	if u == nil {
		return "<invalid URL>"
	}
	copy := *u
	copy.User = nil
	copy.RawQuery = ""
	copy.Fragment = ""
	return copy.String()
}
