// Command record captures a public repository's Actions/API snapshot to
// fixtures. Review the generated files for secrets and private data before
// committing; no Authorization headers or signed blob URLs are recorded.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/schuettc/muda/internal/gh"
)

func main() {
	var repo, since, out string
	flag.StringVar(&repo, "repo", "", "public GitHub OWNER/NAME")
	flag.StringVar(&since, "since", "", "YYYY-MM-DD start date")
	flag.StringVar(&out, "out", "", "fixture directory")
	flag.Parse()
	if repo == "" || since == "" || out == "" {
		fmt.Fprintln(os.Stderr, "required: --repo, --since, --out")
		os.Exit(2)
	}
	start, err := time.Parse("2006-01-02", since)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid --since: expected YYYY-MM-DD")
		os.Exit(2)
	}
	// run() owns the context lifetime; defer cancel() works correctly because
	// we return normally rather than calling os.Exit inside the cancellable scope.
	if err := run(repo, out, start); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run creates the context, resolves credentials and drives capture. It owns
// the context so that defer cancel() is guaranteed to fire on any exit path,
// including panics — unlike calling os.Exit inside a function with a deferred
// cancel (gocritic exitAfterDefer).
func run(repo, out string, start time.Time) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	token, err := gh.ResolveToken(os.Getenv, func() (string, error) {
		value, err := exec.CommandContext(ctx, "gh", "auth", "token").Output()
		return strings.TrimSpace(string(value)), err
	})
	if err == nil {
		err = os.Setenv("MUDA_RECORD", out)
	}
	if err == nil {
		client := gh.New(gh.Options{Token: token, NoCache: true})
		err = capture(ctx, client, repo, start)
	}
	return err
}

func capture(ctx context.Context, c *gh.Client, repo string, since time.Time) error {
	branch, err := c.DefaultBranch(ctx, repo)
	if err != nil {
		return err
	}
	// Public repos may have no classic branch protection; record a 404 as
	// "unavailable" and continue. Any other error (403 permission denied,
	// network failure, 5xx) indicates a real problem and must not be swallowed.
	if _, err := c.BranchProtection(ctx, repo, branch); err != nil {
		var apiErr *gh.Error
		if errors.As(err, &apiErr) && apiErr.Status == 404 {
			fmt.Fprintf(os.Stderr, "branch protection unavailable: %v\n", err)
		} else {
			return fmt.Errorf("branch protection: %w", err)
		}
	}
	if _, err := c.Rulesets(ctx, repo, branch); err != nil {
		return err
	}
	if _, err := c.Environments(ctx, repo); err != nil {
		return err
	}
	flows, err := c.Workflows(ctx, repo)
	if err != nil {
		return err
	}
	paths, err := c.Dir(ctx, repo, branch, ".github/workflows")
	if err != nil {
		return err
	}
	for _, path := range paths {
		if _, _, err := c.File(ctx, repo, branch, path); err != nil {
			return err
		}
	}
	runs, err := c.Runs(ctx, repo, since)
	if err != nil {
		return err
	}
	logRecorded := false
	for _, run := range runs {
		if _, err := c.RunAttempts(ctx, repo, run); err != nil {
			return err
		}
		for attempt := 1; attempt <= run.RunAttempt; attempt++ {
			jobs, err := c.Jobs(ctx, repo, run.ID, attempt)
			if err != nil {
				return err
			}
			for _, job := range jobs {
				if !logRecorded && job.Status == "completed" && job.Conclusion == "success" {
					if _, err := c.JobLog(ctx, repo, job.ID); err != nil {
						return fmt.Errorf("sample log job %d: %w", job.ID, err)
					}
					logRecorded = true
				}
				// Jobs' check_run_url identifies the annotation endpoint. Missing
				// check runs are reported, not treated as an empty set.
				parts := strings.Split(job.CheckRunURL, "/")
				id, err := strconv.ParseInt(parts[len(parts)-1], 10, 64)
				if err != nil {
					return fmt.Errorf("job %d has no check run id: %w", job.ID, err)
				}
				if _, err := c.Annotations(ctx, repo, id); err != nil {
					return err
				}
			}
		}
	}
	fmt.Printf("recorded %s: %d workflows, %d workflow files, %d runs since %s\n", repo, len(flows), len(paths), len(runs), since.Format("2006-01-02"))
	return nil
}
