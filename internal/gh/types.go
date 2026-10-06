package gh

import (
	"encoding/json"
	"time"
)

type Run struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Path       string `json:"path"`
	Event      string `json:"event"`
	HeadBranch string `json:"head_branch"`
	HeadSHA    string `json:"head_sha"`
	HeadCommit struct {
		TreeID string `json:"tree_id"`
	} `json:"head_commit"`
	Status       string    `json:"status"`
	Conclusion   string    `json:"conclusion"`
	RunAttempt   int       `json:"run_attempt"`
	CreatedAt    time.Time `json:"created_at"`
	RunStartedAt time.Time `json:"run_started_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	HTMLURL      string    `json:"html_url"`
}

type Attempt struct {
	RunAttempt   int       `json:"run_attempt"`
	Status       string    `json:"status"`
	Conclusion   string    `json:"conclusion"`
	CreatedAt    time.Time `json:"created_at"`
	RunStartedAt time.Time `json:"run_started_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type Job struct {
	ID          int64     `json:"id"`
	RunID       int64     `json:"run_id"`
	RunAttempt  int       `json:"run_attempt"`
	Name        string    `json:"name"`
	Status      string    `json:"status"`
	Conclusion  string    `json:"conclusion"`
	CreatedAt   time.Time `json:"created_at"`
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at"`
	Labels      []string  `json:"labels"`
	RunnerName  string    `json:"runner_name"`
	Steps       []Step    `json:"steps"`
	CheckRunURL string    `json:"check_run_url"`
	HTMLURL     string    `json:"html_url"`
}

type Step struct {
	Name        string    `json:"name"`
	Number      int       `json:"number"`
	Status      string    `json:"status"`
	Conclusion  string    `json:"conclusion"`
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at"`
}

type Annotation struct {
	Path            string `json:"path"`
	StartLine       int    `json:"start_line"`
	AnnotationLevel string `json:"annotation_level"`
	Message         string `json:"message"`
	Title           string `json:"title"`
}

type Workflow struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Path  string `json:"path"`
	State string `json:"state"`
}

// BranchNotProtected is the exact GitHub 404 body message for a branch
// without classic branch protection.
const BranchNotProtected = "Branch not protected"

type Protection struct {
	// NotProtected is GitHub's explicit "Branch not protected" answer: a
	// complete no-protection state, never serialized as a protection record.
	NotProtected         bool            `json:"-"`
	RawSecurity          json.RawMessage `json:"raw_security,omitempty"`
	EvidenceURL          string          `json:"evidence_url,omitempty"`
	RequiredStatusChecks struct {
		Contexts []string `json:"contexts"`
		Strict   bool     `json:"strict"`
	} `json:"required_status_checks"`
	EnforceAdmins struct {
		Enabled bool `json:"enabled"`
	} `json:"enforce_admins"`
	RequiredPullRequestReviews struct {
		RequiredApprovingReviewCount int `json:"required_approving_review_count"`
	} `json:"required_pull_request_reviews"`
}

type Ruleset struct {
	EffectiveRules    json.RawMessage `json:"effective_rules,omitempty"`
	EffectiveURL      string          `json:"effective_url,omitempty"`
	DetailUnavailable string          `json:"detail_unavailable,omitempty"`
	RawSecurity       json.RawMessage `json:"raw_security,omitempty"`
	EvidenceURL       string          `json:"evidence_url,omitempty"`
	ID                int64           `json:"id"`
	Name              string          `json:"name"`
	Target            string          `json:"target"`
	Enforcement       string          `json:"enforcement"`
	Rules             []struct {
		Type       string `json:"type"`
		Parameters struct {
			RequiredStatusChecks []struct {
				Context string `json:"context"`
			} `json:"required_status_checks"`
		} `json:"parameters"`
	} `json:"rules"`
}

type Environment struct {
	RawSecurity     json.RawMessage `json:"raw_security,omitempty"`
	EvidenceURL     string          `json:"evidence_url,omitempty"`
	ID              int64           `json:"id"`
	Name            string          `json:"name"`
	ProtectionRules []struct {
		Type string `json:"type"`
	} `json:"protection_rules"`
	DeploymentBranchPolicy struct {
		ProtectedBranches    bool `json:"protected_branches"`
		CustomBranchPolicies bool `json:"custom_branch_policies"`
	} `json:"deployment_branch_policy"`
}
