package gh

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

var ownerRepo = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

func repoPath(repo string) (string, error) {
	if !ownerRepo.MatchString(repo) || strings.Contains(repo, "..") || strings.HasSuffix(repo, "/.") {
		return "", fmt.Errorf("invalid GitHub OWNER/NAME %q", repo)
	}
	return "/repos/" + repo, nil
}

func (c *Client) list(ctx context.Context, first string, cacheable func([]byte) bool, key string, dest any) error {
	_, err := c.listCounted(ctx, first, cacheable, key, dest)
	return err
}

// listCounted is list, also returning the first page's total_count, or -1 when
// the endpoint does not report one.
func (c *Client) listCounted(ctx context.Context, first string, cacheable func([]byte) bool, key string, dest any) (int, error) {
	total := -1
	err := c.listPages(ctx, first, cacheable, key, dest, func(wrapper map[string]json.RawMessage) {
		if raw, ok := wrapper["total_count"]; ok && total < 0 {
			var n int
			if json.Unmarshal(raw, &n) == nil {
				total = n
			}
		}
	})
	return total, err
}

func (c *Client) listPages(ctx context.Context, first string, cacheable func([]byte) bool, key string, dest any, onPage func(map[string]json.RawMessage)) error {
	var all []json.RawMessage
	seen := map[string]bool{}
	for next := first; next != ""; {
		if seen[next] {
			return fmt.Errorf("GitHub API pagination cycle at %s", key)
		}
		seen[next] = true
		body, headers, err := c.get(ctx, next, cacheable, false)
		if err != nil {
			return err
		}
		var wrapper map[string]json.RawMessage
		if err := json.Unmarshal(body, &wrapper); err != nil {
			return fmt.Errorf("GitHub API %s: decode: %w", key, err)
		}
		onPage(wrapper)
		if raw := bytes.TrimSpace(wrapper[key]); len(raw) == 0 || raw[0] != '[' {
			return fmt.Errorf("GitHub API %s: %s array unavailable", key, key)
		}
		var page []json.RawMessage
		if err := json.Unmarshal(wrapper[key], &page); err != nil {
			return fmt.Errorf("GitHub API %s: missing %s: %w", key, key, err)
		}
		all = append(all, page...)
		next = ""
		for _, part := range strings.Split(headers.Get("Link"), ",") {
			if !strings.Contains(part, `rel="next"`) {
				continue
			}
			left := strings.IndexByte(part, '<')
			right := strings.IndexByte(part, '>')
			if left < 0 || right <= left {
				return fmt.Errorf("GitHub API %s: malformed pagination Link", key)
			}
			next = part[left+1 : right]
			if _, err := c.validateURL(next); err != nil {
				return err
			}
		}
	}
	return json.Unmarshal(mustJSON(all), dest)
}

func mustJSON(value any) []byte { b, _ := json.Marshal(value); return b }

func (c *Client) Runs(ctx context.Context, repo string, since time.Time) ([]Run, error) {
	p, err := repoPath(repo)
	if err != nil {
		return nil, err
	}
	query := url.Values{"created": {">=" + since.UTC().Format(time.RFC3339)}, "per_page": {"100"}}
	return c.runsListing(ctx, p, query, false)
}

// RunsWindow fetches a bounded live listing and enforces exact [since, until).
// GitHub's inclusive range boundary is counted before local filtering.
func (c *Client) RunsWindow(ctx context.Context, repo string, since, until time.Time) ([]Run, error) {
	if since.IsZero() || until.IsZero() || !since.Before(until) {
		return nil, fmt.Errorf("invalid run window: nonzero ordered bounds required")
	}
	p, err := repoPath(repo)
	if err != nil {
		return nil, err
	}
	query := url.Values{"created": {since.UTC().Format(time.RFC3339Nano) + ".." + until.UTC().Format(time.RFC3339Nano)}, "per_page": {"100"}}
	runs, err := c.runsListing(ctx, p, query, true)
	if err != nil {
		return nil, err
	}
	kept := runs[:0]
	for _, r := range runs {
		if !r.CreatedAt.Before(since) && r.CreatedAt.Before(until) {
			kept = append(kept, r)
		}
	}
	return kept, nil
}

func (c *Client) runsListing(ctx context.Context, p string, query url.Values, requireTotal bool) ([]Run, error) {
	var runs []Run
	// Runs listings are always fetched live: a run that was completed may be
	// rerun at any time (status → in_progress, run_attempt++). A TTL-free
	// content-addressed cache would freeze rerun state forever. Completed
	// immutable data (attempt details, job results, logs) are still cached.
	first := c.endpoint(p+"/actions/runs", query)
	var total int
	var err error
	invalidTotal := false
	if requireTotal {
		total = -1
		err = c.listPages(ctx, first, nil, "workflow_runs", &runs, func(wrapper map[string]json.RawMessage) {
			var count *int
			if json.Unmarshal(wrapper["total_count"], &count) != nil || count == nil || *count < 0 {
				invalidTotal = true
				return
			}
			if *count > total {
				total = *count
			}
		})
	} else {
		total, err = c.listCounted(ctx, first, nil, "workflow_runs", &runs)
	}
	if err != nil {
		return nil, err
	}
	if requireTotal && (invalidTotal || total < 0) {
		return nil, &Error{Endpoint: p + "/actions/runs", Status: 200, Msg: "listing unavailable: missing or invalid total_count"}
	}
	if requireTotal && total > 1000 {
		return nil, &Error{Endpoint: p + "/actions/runs", Status: 200, Msg: "listing exceeds GitHub filtered cap of 1000; narrow the window"}
	}
	// A page shift during pagination can repeat a run; count each once.
	seen := make(map[int64]bool, len(runs))
	unique := runs[:0]
	for _, r := range runs {
		if !seen[r.ID] {
			seen[r.ID] = true
			unique = append(unique, r)
		}
	}
	// GitHub caps created-filtered listings (1,000 results) and a page can go
	// missing mid-pagination. Partial data is never returned as the window.
	if total > len(unique) {
		return nil, &Error{Endpoint: p + "/actions/runs", Status: 200,
			Msg: fmt.Sprintf("listing incomplete: total_count %d but %d runs returned (GitHub caps filtered listings at 1000); narrow --since", total, len(unique))}
	}
	return unique, nil
}

func (c *Client) RunAttempts(ctx context.Context, repo string, run Run) ([]Attempt, error) {
	p, err := repoPath(repo)
	if err != nil {
		return nil, err
	}
	if run.ID <= 0 || run.RunAttempt < 1 {
		return nil, fmt.Errorf("invalid run id or attempt")
	}
	attempts := make([]Attempt, 0, run.RunAttempt)
	for n := 1; n <= run.RunAttempt; n++ {
		body, _, err := c.get(ctx, c.endpoint(fmt.Sprintf("%s/actions/runs/%d/attempts/%d", p, run.ID, n), nil), nil, false)
		if err != nil {
			return nil, err
		}
		var attempt Attempt
		if err := json.Unmarshal(body, &attempt); err != nil {
			return nil, err
		}
		if attempt.RunAttempt == 0 {
			attempt.RunAttempt = n
		}
		attempts = append(attempts, attempt)
	}
	return attempts, nil
}

func (c *Client) Jobs(ctx context.Context, repo string, runID int64, attempt int) ([]Job, error) {
	p, err := repoPath(repo)
	if err != nil {
		return nil, err
	}
	if runID <= 0 || attempt < 1 {
		return nil, fmt.Errorf("invalid run id or attempt")
	}
	q := url.Values{"per_page": {"100"}}
	var jobs []Job
	err = c.list(ctx, c.endpoint(fmt.Sprintf("%s/actions/runs/%d/attempts/%d/jobs", p, runID, attempt), q), completedJobs, "jobs", &jobs)
	return jobs, err
}

func (c *Client) JobLog(ctx context.Context, repo string, jobID int64) (string, error) {
	p, err := repoPath(repo)
	if err != nil {
		return "", err
	}
	if jobID <= 0 {
		return "", fmt.Errorf("invalid job id")
	}
	statusBody, _, err := c.get(ctx, c.endpoint(fmt.Sprintf("%s/actions/jobs/%d", p, jobID), nil), nil, false)
	if err != nil {
		return "", err
	}
	var job struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(statusBody, &job); err != nil {
		return "", fmt.Errorf("job %d status: %w", jobID, err)
	}
	if job.Status == "" {
		return "", fmt.Errorf("job %d has no status", jobID)
	}
	var cacheable func([]byte) bool
	if job.Status == "completed" {
		cacheable = func(body []byte) bool { return len(body) > 0 }
	}
	body, _, err := c.get(ctx, c.endpoint(fmt.Sprintf("%s/actions/jobs/%d/logs", p, jobID), nil), cacheable, true)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func (c *Client) Annotations(ctx context.Context, repo string, checkRunID int64) ([]Annotation, error) {
	p, err := repoPath(repo)
	if err != nil {
		return nil, err
	}
	if checkRunID <= 0 {
		return nil, fmt.Errorf("invalid check run id")
	}
	statusBody, _, err := c.get(ctx, c.endpoint(fmt.Sprintf("%s/check-runs/%d", p, checkRunID), nil), nil, false)
	if err != nil {
		return nil, err
	}
	var check struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(statusBody, &check); err != nil {
		return nil, fmt.Errorf("check run %d status: %w", checkRunID, err)
	}
	if check.Status == "" {
		return nil, fmt.Errorf("check run %d has no status", checkRunID)
	}
	var cacheable func([]byte) bool
	if check.Status == "completed" {
		cacheable = func([]byte) bool { return true }
	}
	q := url.Values{"per_page": {"100"}}
	var all []Annotation
	seen := map[string]bool{}
	for next := c.endpoint(fmt.Sprintf("%s/check-runs/%d/annotations", p, checkRunID), q); next != ""; {
		if seen[next] {
			return nil, fmt.Errorf("annotations pagination cycle")
		}
		seen[next] = true
		body, head, err := c.get(ctx, next, cacheable, false)
		if err != nil {
			return nil, err
		}
		var page []Annotation
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, err
		}
		all = append(all, page...)
		next = ""
		for _, part := range strings.Split(head.Get("Link"), ",") {
			if strings.Contains(part, `rel="next"`) {
				l, r := strings.Index(part, "<"), strings.Index(part, ">")
				if l < 0 || r < 0 {
					return nil, fmt.Errorf("bad annotations Link")
				}
				next = part[l+1 : r]
				if _, err := c.validateURL(next); err != nil {
					return nil, err
				}
			}
		}
	}
	return all, nil
}

func (c *Client) Workflows(ctx context.Context, repo string) ([]Workflow, error) {
	p, err := repoPath(repo)
	if err != nil {
		return nil, err
	}
	var workflows []Workflow
	err = c.list(ctx, c.endpoint(p+"/actions/workflows", url.Values{"per_page": {"100"}}), nil, "workflows", &workflows)
	return workflows, err
}

// File reads a regular file at ref, always live: a ref string alone, even a
// 40-hex one, does not prove an immutable commit. A 404 is (nil, false, nil).
func (c *Client) File(ctx context.Context, repo, ref, path string) ([]byte, bool, error) {
	return c.file(ctx, repo, ref, path, false)
}

// RunFile reads a run's own workflow file (r.Path) at the head commit the
// API reported for the run (r.HeadSHA). That is a commit, so its content
// never changes and the read is cached. A run without a head commit is an
// error: there is nothing to read.
func (c *Client) RunFile(ctx context.Context, repo string, r Run) ([]byte, bool, error) {
	if r.HeadSHA == "" {
		return nil, false, fmt.Errorf("run %d has no head commit to read %s at", r.ID, r.Path)
	}
	return c.file(ctx, repo, r.HeadSHA, r.Path, true)
}

func (c *Client) file(ctx context.Context, repo, ref, path string, immutable bool) ([]byte, bool, error) {
	p, err := repoPath(repo)
	if err != nil {
		return nil, false, err
	}
	if strings.HasPrefix(path, "/") || strings.Contains(path, "..") {
		return nil, false, fmt.Errorf("invalid file path %q", path)
	}
	endpoint := c.endpoint(p+"/contents/"+path, url.Values{"ref": {ref}})
	// An immutable read is cached as its content and blob sha only: the API
	// body also carries URLs (a private repository's download_url holds a
	// token), and those never reach the disk.
	cacheKey := "file-content " + endpoint
	useCache := immutable && !c.noCache
	var body []byte
	fromCache := false
	if useCache {
		body, fromCache = c.cached(cacheKey)
	}
	if !fromCache {
		var apiErr *Error
		body, _, err = c.get(ctx, endpoint, nil, false)
		if errors.As(err, &apiErr) && apiErr.Status == 404 {
			return nil, false, nil
		}
		if err != nil {
			return nil, false, err
		}
	}
	var item fileContent
	if err := json.Unmarshal(body, &item); err != nil {
		return nil, false, err
	}
	if item.Type != "file" {
		return nil, false, &NotFileError{Path: path, Type: item.Type}
	}
	if item.Encoding != "base64" {
		return nil, false, fmt.Errorf("GitHub contents %s has unsupported encoding %q", path, item.Encoding)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(item.Content, "\n", ""))
	if err != nil {
		return nil, false, err
	}
	if useCache && !fromCache {
		kept, err := json.Marshal(item)
		if err == nil {
			err = c.store(cacheKey, kept)
		}
		if err != nil {
			return nil, false, fmt.Errorf("cache %s: %w", path, err)
		}
	}
	return decoded, true, nil
}

// fileContent is the part of a contents response muda reads and caches.
type fileContent struct {
	Type     string `json:"type"`
	Encoding string `json:"encoding"`
	Content  string `json:"content"`
	SHA      string `json:"sha"`
}

// NotFileError reports a contents path that is not a regular file: a
// symlink, a submodule or a directory. Callers name it unavailable; it is
// never read through, and never treated as absent.
type NotFileError struct{ Path, Type string }

func (e *NotFileError) Error() string {
	return fmt.Sprintf("GitHub contents %s is a %s entry, not a regular file", e.Path, e.Type)
}

// Reason is the unavailable reason for a workflow path that is not a regular
// file. muda never reads through it, so it is neither a workflow nor a
// removal. GitHub's documentation does not say whether Actions runs one.
func (e *NotFileError) Reason() string {
	return "not a regular workflow file (" + e.Error() + "); muda does not read through it, and cannot certify it as either a workflow or a removal"
}

// DirEntry is one directory listing entry. Type is the contents API's: "file",
// "dir", "symlink" or "submodule". The listing reports a submodule as "file"
// for backwards compatibility; only File shows it is a submodule.
type DirEntry struct{ Path, Type string }

// Entries lists every entry of a directory at ref, of every type.
func (c *Client) Entries(ctx context.Context, repo, ref, path string) ([]DirEntry, error) {
	p, err := repoPath(repo)
	if err != nil {
		return nil, err
	}
	if strings.Contains(path, "..") {
		return nil, fmt.Errorf("invalid directory path %q", path)
	}
	body, _, err := c.get(ctx, c.endpoint(p+"/contents/"+strings.TrimPrefix(path, "/"), url.Values{"ref": {ref}}), nil, false)
	if err != nil {
		return nil, err
	}
	var entries []DirEntry
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

// Dir lists the paths of a directory's "file" entries only.
func (c *Client) Dir(ctx context.Context, repo, ref, path string) ([]string, error) {
	entries, err := c.Entries(ctx, repo, ref, path)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.Type == "file" {
			paths = append(paths, e.Path)
		}
	}
	return paths, nil
}

func (c *Client) DefaultBranch(ctx context.Context, repo string) (string, error) {
	p, err := repoPath(repo)
	if err != nil {
		return "", err
	}
	body, _, err := c.get(ctx, c.endpoint(p, nil), nil, false)
	if err != nil {
		return "", err
	}
	var result struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}
	if result.DefaultBranch == "" {
		return "", fmt.Errorf("repo %s has no default_branch", repo)
	}
	return result.DefaultBranch, nil
}

// FullName is the repository's current owner/name. GitHub redirects a renamed
// or transferred repository's old name to it; the answer is never cached, so a
// name later taken by another repository is seen as that repository.
func (c *Client) FullName(ctx context.Context, repo string) (string, error) {
	p, err := repoPath(repo)
	if err != nil {
		return "", err
	}
	body, _, err := c.get(ctx, c.endpoint(p, nil), nil, false)
	if err != nil {
		return "", err
	}
	var result struct {
		FullName string `json:"full_name"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}
	if result.FullName == "" {
		return "", fmt.Errorf("repo %s has no full_name", repo)
	}
	return result.FullName, nil
}

func (c *Client) BranchProtection(ctx context.Context, repo, branch string) (*Protection, error) {
	p, err := repoPath(repo)
	if err != nil {
		return nil, err
	}
	evidence := c.endpointEscaped(p + "/branches/" + url.PathEscape(branch) + "/protection")
	body, _, err := c.get(ctx, evidence, nil, false)
	var api *Error
	if errors.As(err, &api) && api.Status == 404 && api.APIMessage == BranchNotProtected {
		// GitHub's explicit answer for an unprotected branch: a complete
		// no-protection state. Every other 404 stays an error.
		return &Protection{NotProtected: true, EvidenceURL: evidence}, nil
	}
	if err != nil {
		return nil, err
	}
	var result Protection
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	result.RawSecurity, err = CanonicalSecurity(body)
	if err != nil {
		return nil, err
	}
	result.EvidenceURL = evidence
	return &result, nil
}

func (c *Client) Rulesets(ctx context.Context, repo, branch string) ([]Ruleset, error) {
	p, err := repoPath(repo)
	if err != nil {
		return nil, err
	}
	body, _, err := c.get(ctx, c.endpointEscaped(p+"/rules/branches/"+url.PathEscape(branch)), nil, false)
	if err != nil {
		return nil, err
	}
	if raw := bytes.TrimSpace(body); len(raw) == 0 || raw[0] != '[' {
		return nil, fmt.Errorf("GitHub API %s: effective rules array unavailable", p+"/rules/branches/"+url.PathEscape(branch))
	}
	var rules []struct {
		Type        string `json:"type"`
		RulesetID   int64  `json:"ruleset_id"`
		RulesetName string `json:"ruleset_name"`
		Parameters  struct {
			RequiredStatusChecks []struct {
				Context string `json:"context"`
			} `json:"required_status_checks"`
		} `json:"parameters"`
	}
	if err := json.Unmarshal(body, &rules); err != nil {
		return nil, err
	}
	var rawRules []json.RawMessage
	if err := json.Unmarshal(body, &rawRules); err != nil {
		return nil, err
	}
	byID := map[int64]*Ruleset{}
	rawByID := map[int64][]json.RawMessage{}
	for index, r := range rules {
		if r.RulesetID <= 0 {
			return nil, fmt.Errorf("GitHub API %s: effective rule ruleset identity unavailable", p+"/rules/branches/"+url.PathEscape(branch))
		}
		canonicalRule, err := CanonicalSecurity(rawRules[index])
		if err != nil {
			return nil, err
		}
		rawByID[r.RulesetID] = append(rawByID[r.RulesetID], canonicalRule)
		set := byID[r.RulesetID]
		if set == nil {
			set = &Ruleset{ID: r.RulesetID, Name: r.RulesetName, Enforcement: ""}
			byID[r.RulesetID] = set
		}
		set.Rules = append(set.Rules, struct {
			Type       string `json:"type"`
			Parameters struct {
				RequiredStatusChecks []struct {
					Context string `json:"context"`
				} `json:"required_status_checks"`
			} `json:"parameters"`
		}{Type: r.Type, Parameters: r.Parameters})
	}
	sets := make([]Ruleset, 0, len(byID))
	for id, set := range byID {
		effective, err := CanonicalSecurity(mustJSON(rawByID[id]))
		if err != nil {
			return nil, err
		}
		var sortedRules []json.RawMessage
		if err := json.Unmarshal(effective, &sortedRules); err != nil {
			return nil, err
		}
		sort.Slice(sortedRules, func(i, j int) bool { return string(sortedRules[i]) < string(sortedRules[j]) })
		effective = mustJSON(sortedRules)
		// The effective branch projection stays usable even when full metadata is
		// unreadable, but the incomplete security snapshot is explicitly unavailable.
		detailPath := fmt.Sprintf("%s/rulesets/%d", p, id)
		set.EvidenceURL = c.endpoint(detailPath, nil)
		detail, _, err := c.get(ctx, set.EvidenceURL, nil, false)
		if err != nil {
			var api *Error
			if !errors.As(err, &api) {
				return nil, err
			}
			set.DetailUnavailable = err.Error()
		} else {
			var full Ruleset
			if err := json.Unmarshal(detail, &full); err != nil {
				return nil, err
			}
			if full.ID != id {
				return nil, fmt.Errorf("GitHub API %s: ruleset identity mismatch", detailPath)
			}
			// Keep existing typed effective rule projections for API compatibility.
			set.RawSecurity, err = CanonicalSecurity(detail)
			if err != nil {
				return nil, err
			}
			set.Target = full.Target
			set.Enforcement = full.Enforcement
		}
		set.EffectiveRules = effective
		set.EffectiveURL = c.endpointEscaped(p + "/rules/branches/" + url.PathEscape(branch))
		sets = append(sets, *set)
	}
	sort.Slice(sets, func(i, j int) bool { return sets[i].ID < sets[j].ID })
	return sets, nil
}

func (c *Client) Environments(ctx context.Context, repo string) ([]Environment, error) {
	p, err := repoPath(repo)
	if err != nil {
		return nil, err
	}
	// Decode API entries separately so reserved report field names cannot shadow
	// unknown fields in a future GitHub payload.
	var entries []json.RawMessage
	if err := c.list(ctx, c.endpoint(p+"/environments", url.Values{"per_page": {"100"}}), nil, "environments", &entries); err != nil {
		return nil, err
	}
	envs := []Environment{}
	for _, raw := range entries {
		var env Environment
		if err := json.Unmarshal(raw, &env); err != nil {
			return nil, err
		}
		if env.Name == "" {
			return nil, fmt.Errorf("GitHub API %s: environment identity unavailable", p+"/environments")
		}
		env.RawSecurity, err = CanonicalSecurity(raw)
		if err != nil {
			return nil, err
		}
		env.EvidenceURL = c.endpointEscaped(p + "/environments/" + url.PathEscape(env.Name))
		envs = append(envs, env)
	}
	return envs, nil
}

// Run fetches live run metadata directly, never caching mutable attempt state.
// Identity and a positive final attempt are required before using its jobs.
func (c *Client) Run(ctx context.Context, repo string, runID int64) (*Run, error) {
	p, err := repoPath(repo)
	if err != nil {
		return nil, err
	}
	if runID <= 0 {
		return nil, fmt.Errorf("invalid run id")
	}
	path := fmt.Sprintf("%s/actions/runs/%d", p, runID)
	body, _, err := c.get(ctx, c.endpoint(path, nil), nil, false)
	if err != nil {
		return nil, err
	}
	var run Run
	if err := json.Unmarshal(body, &run); err != nil {
		return nil, fmt.Errorf("GitHub API %s: decode: %w", path, err)
	}
	if run.ID != runID || run.RunAttempt < 1 {
		return nil, fmt.Errorf("GitHub API %s: requested run identity or positive attempt unavailable (id %d, attempt %d)", path, run.ID, run.RunAttempt)
	}
	return &run, nil
}
