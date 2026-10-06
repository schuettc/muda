package gh

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"

	"github.com/schuettc/muda/internal/ghtest"
	"strconv"
	"strings"
	"time"

	tools "github.com/schuettc/tools-common"
)

type Options struct {
	Token, BaseURL, CacheDir string
	NoCache                  bool
	HTTP                     *http.Client
	Sleep                    func(time.Duration)
}

type Client struct {
	base     *url.URL
	token    string
	http     *http.Client
	cacheDir string
	noCache  bool
	sleep    func(time.Duration)
}

type Error struct {
	Endpoint string
	Status   int
	Msg      string
	// APIMessage is GitHub's JSON body "message", kept for exact matching of
	// documented states; it is never printed.
	APIMessage string
}

func (e *Error) Error() string {
	return fmt.Sprintf("GitHub API %s (HTTP %d): %s", e.Endpoint, e.Status, e.Msg)
}

type logRedirectKey struct{}

func New(o Options) *Client {
	if o.BaseURL == "" {
		o.BaseURL = "https://api.github.com"
	}
	base, _ := url.Parse(o.BaseURL)
	if o.CacheDir == "" {
		o.CacheDir = tools.CacheDir("muda")
	}
	if o.HTTP == nil {
		o.HTTP = http.DefaultClient
	}
	client := *o.HTTP // never mutate the caller's client
	if dir := os.Getenv("MUDA_RECORD"); dir != "" && base != nil {
		client.Transport = ghtest.Record(dir, base.Host, client.Transport)
	}
	prior := client.CheckRedirect
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > 10 {
			return fmt.Errorf("too many GitHub API redirects")
		}
		if len(via) == 0 {
			return fmt.Errorf("redirect without origin")
		}
		if req.URL.Scheme != via[0].URL.Scheme || req.URL.Host != via[0].URL.Host {
			req.Header.Del("Authorization")
			if via[0].Context().Value(logRedirectKey{}) == nil {
				return fmt.Errorf("API redirected to another host")
			}
		}
		if prior != nil {
			return prior(req, via)
		}
		return nil
	}
	// A nil Sleep uses a context-aware timer (see wait).
	return &Client{base: base, token: o.Token, http: &client, cacheDir: o.CacheDir, noCache: o.NoCache, sleep: o.Sleep}
}

// validateURL ensures both pagination and ordinary API calls stay on the base
// origin. A Link header must never turn the API token into a cross-host request.
func (c *Client) validateURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || c.base == nil || c.base.Scheme == "" || c.base.Host == "" || u.Scheme != c.base.Scheme || u.Host != c.base.Host || u.User != nil {
		return nil, fmt.Errorf("GitHub API endpoint points outside the configured API host")
	}
	return u, nil
}

func (c *Client) endpoint(path string, query url.Values) string {
	u := *c.base
	u.Path = strings.TrimRight(u.Path, "/") + "/" + strings.TrimLeft(path, "/")
	u.RawQuery = query.Encode()
	return u.String()
}

// endpointEscaped accepts a path with an already-escaped segment, such as a
// branch containing '/'. Setting RawPath prevents url.URL from escaping '%' again.
func (c *Client) endpointEscaped(path string) string {
	u, _ := url.Parse(c.endpoint(path, nil))
	decoded, err := url.PathUnescape(u.Path)
	if err == nil {
		u.RawPath = u.Path
		u.Path = decoded
	}
	return u.String()
}

func (c *Client) get(ctx context.Context, raw string, cacheable func([]byte) bool, log bool) ([]byte, http.Header, error) {
	u, err := c.validateURL(raw)
	if err != nil {
		return nil, nil, err
	}
	cacheKey := u.EscapedPath() + "?" + u.RawQuery
	if !c.noCache && cacheable != nil {
		if body, found := c.cached(cacheKey); found {
			return body, nil, nil
		}
	}
	if log {
		ctx = context.WithValue(ctx, logRedirectKey{}, true)
	}
	waited := time.Duration(0)
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, nil, fmt.Errorf("GET %s: %w", u.EscapedPath(), err)
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		if c.token != "" {
			req.Header.Set("Authorization", "Bearer "+c.token)
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, nil, fmt.Errorf("GitHub API %s: %w", u.EscapedPath(), err)
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, (64<<20)+1))
		closeErr := resp.Body.Close()
		if closeErr != nil && readErr == nil {
			readErr = closeErr
		}
		if len(body) > 64<<20 {
			return nil, nil, fmt.Errorf("GitHub API %s: response exceeds 64 MiB limit", u.EscapedPath())
		}
		if readErr != nil {
			return nil, nil, fmt.Errorf("GitHub API %s: %w", u.EscapedPath(), readErr)
		}
		if resp.StatusCode == 429 || (resp.StatusCode == 403 && (resp.Header.Get("Retry-After") != "" || resp.Header.Get("X-RateLimit-Remaining") == "0")) {
			wait := retryAfter(resp.Header)
			if waited+wait > 5*time.Minute {
				return nil, nil, &Error{Endpoint: u.EscapedPath(), Status: resp.StatusCode, Msg: "rate limit retry budget exceeded; reset at " + resetTime(resp.Header, time.Now())}
			}
			waited += wait
			if err := c.wait(ctx, wait); err != nil {
				return nil, nil, err
			}
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			msg := fmt.Sprintf("request failed (%s)", http.StatusText(resp.StatusCode))
			switch resp.StatusCode {
			case 403:
				msg = "forbidden: token may lack actions:read or administration:read permission"
			case 404:
				msg = "not found or no access (check repo, path and token permissions)"
			}
			var answer struct {
				Message string `json:"message"`
			}
			_ = json.Unmarshal(body, &answer)
			return nil, nil, &Error{Endpoint: u.EscapedPath(), Status: resp.StatusCode, Msg: msg, APIMessage: answer.Message}
		}
		if log {
			if dir := os.Getenv("MUDA_RECORD"); dir != "" {
				if err := ghtest.SaveLog(dir, u.EscapedPath(), body); err != nil {
					return nil, nil, fmt.Errorf("record log %s: %w", u.EscapedPath(), err)
				}
			}
		}
		if !c.noCache && cacheable != nil && resp.Header.Get("Link") == "" && cacheable(body) {
			if err := c.store(cacheKey, body); err != nil {
				return nil, nil, fmt.Errorf("cache %s: %w", u.EscapedPath(), err)
			}
		}
		return body, resp.Header, nil
	}
}

// wait sleeps for d unless ctx is cancelled first. An injected Sleep runs in
// its own goroutine so that cancellation still returns promptly.
func (c *Client) wait(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var done <-chan time.Time
	if c.sleep == nil {
		t := time.NewTimer(d)
		defer t.Stop()
		done = t.C
	} else {
		ch := make(chan time.Time, 1)
		go func() { c.sleep(d); ch <- time.Time{} }()
		done = ch
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return ctx.Err()
	}
}

// resetTime reports when the rate limit resets as RFC3339 UTC, from
// X-RateLimit-Reset (epoch seconds) or Retry-After (seconds or HTTP date).
func resetTime(h http.Header, now time.Time) string {
	if n, err := strconv.ParseInt(h.Get("X-RateLimit-Reset"), 10, 64); err == nil && n > 0 {
		return time.Unix(n, 0).UTC().Format(time.RFC3339)
	}
	if n, err := strconv.ParseInt(h.Get("Retry-After"), 10, 64); err == nil && n > 0 {
		return now.Add(time.Duration(n) * time.Second).UTC().Format(time.RFC3339)
	}
	if date, err := http.ParseTime(h.Get("Retry-After")); err == nil {
		return date.UTC().Format(time.RFC3339)
	}
	return "an unknown time"
}

func retryAfter(h http.Header) time.Duration {
	if n, err := strconv.ParseInt(h.Get("Retry-After"), 10, 64); err == nil && n > 0 {
		if n > 300 {
			return 301 * time.Second
		}
		return time.Duration(n) * time.Second
	}
	if date, err := http.ParseTime(h.Get("Retry-After")); err == nil {
		if wait := time.Until(date); wait > 0 {
			return wait
		}
	}
	if reset, err := strconv.ParseInt(h.Get("X-RateLimit-Reset"), 10, 64); err == nil {
		wait := time.Until(time.Unix(reset, 0))
		if wait > 0 {
			return wait
		}
	}
	return time.Second
}

func completedJobs(body []byte) bool {
	var wrapper struct {
		Jobs []struct {
			Status string `json:"status"`
		} `json:"jobs"`
	}
	if json.Unmarshal(body, &wrapper) != nil {
		return false
	}
	for _, job := range wrapper.Jobs {
		if job.Status != "completed" {
			return false
		}
	}
	return len(wrapper.Jobs) > 0
}
