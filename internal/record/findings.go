package record

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var findingID = regexp.MustCompile(`^F-[0-9]{3,}$`)

// ValidFindingID accepts only canonical generated IDs, with no path components.
func ValidFindingID(id string) bool {
	if !findingID.MatchString(id) {
		return false
	}
	n, err := strconv.Atoi(id[2:])
	return err == nil && n > 0 && fmt.Sprintf("F-%03d", n) == id
}
func allowed(a, b Status) bool {
	return a == b || a == OpenStatus && b == Approved || a == Approved && (b == InPR || b == Reopened) || a == InPR && (b == Fixed || b == Reopened) || a == Reopened && b == Approved || a != Fixed && b == ExemptStatus
}
func validateFinding(f Finding) error {
	if !ValidFindingID(f.ID) {
		return problem("invalid-finding-id", "findings.toml", f.ID)
	}
	if f.Waste == "" || f.Location == "" || f.Recipe == "" || len(f.Evidence) == 0 || f.Estimate == "" || f.Risk == "" {
		return problem("missing-finding-fields", "findings.toml", f.ID+": waste, location, recipe, evidence, estimate and risk required")
	}
	if !validLocation(f.Location) {
		return problem("invalid-location", "findings.toml", f.ID)
	}
	if f.Basis != "measured" && f.Basis != "suspected" {
		return problem("invalid-basis", "findings.toml", f.ID)
	}
	switch f.Status {
	case OpenStatus, Approved, InPR, Fixed, Reopened, ExemptStatus:
	default:
		return problem("invalid-status", "findings.toml", f.ID)
	}
	if f.Status == ExemptStatus && f.ExemptionID == "" {
		return problem("dangling-exemption", "findings.toml", f.ID)
	}
	// A finding under review or fixed names the pull request that carries it.
	if (f.Status == InPR || f.Status == Fixed) && strings.TrimSpace(f.PR) == "" {
		return problem("missing-pr", "findings.toml", f.ID+": in-pr and fixed findings require pr")
	}
	for _, e := range f.Evidence {
		if e.Kind == "" || (e.URL == "" && e.Path == "" && e.RunID == 0 && e.JobID == 0) {
			return problem("invalid-evidence", "findings.toml", f.ID)
		}
	}
	return nil
}
func (s *Store) AddFinding(f Finding) (id string, err error) {
	s, release, err := s.mutation()
	if err != nil {
		return "", err
	}
	defer func() {
		if releaseErr := release(); err == nil {
			err = releaseErr
		}
	}()
	var doc findingsFile
	if err := s.load("findings.toml", &doc); err != nil {
		return "", err
	}
	if f.ID != "" || f.ExemptionID != "" || f.ReopenReason != "" || (f.Status != "" && f.Status != OpenStatus) {
		return "", problem("invalid-new-finding", "findings.toml", "new findings start open with generated IDs")
	}
	max := 0
	for _, old := range doc.Findings {
		if err := validateFinding(old); err != nil {
			return "", err
		}
		n, _ := strconv.Atoi(old.ID[2:])
		if n > max {
			max = n
		}
	}
	f.ID = fmt.Sprintf("F-%03d", max+1)
	f.Status = OpenStatus
	if err := validateFinding(f); err != nil {
		return "", err
	}
	doc.Findings = append(doc.Findings, f)
	if err := s.writeTOML("findings.toml", doc); err != nil {
		return "", err
	}
	return f.ID, nil
}
func (s *Store) SetFinding(id string, p FindingPatch) (err error) {
	s, release, err := s.mutation()
	if err != nil {
		return err
	}
	defer func() {
		if releaseErr := release(); err == nil {
			err = releaseErr
		}
	}()
	if !ValidFindingID(id) {
		return problem("invalid-finding-id", "findings.toml", id)
	}
	var doc findingsFile
	if err := s.load("findings.toml", &doc); err != nil {
		return err
	}
	for i, f := range doc.Findings {
		if f.ID != id {
			continue
		}
		if err := validateFinding(f); err != nil {
			return err
		}
		old, oldExemption := f.Status, f.ExemptionID
		for _, v := range []struct {
			dst *string
			src *string
		}{{&f.Waste, p.Waste}, {&f.Location, p.Location}, {&f.Recipe, p.Recipe}, {&f.Basis, p.Basis}, {&f.Estimate, p.Estimate}, {&f.Risk, p.Risk}, {&f.PR, p.PR}, {&f.ExemptionID, p.ExemptionID}} {
			if v.src != nil {
				*v.dst = *v.src
			}
		}
		if p.Evidence != nil {
			f.Evidence = *p.Evidence
		}
		if p.Status != nil {
			f.Status = *p.Status
		}
		unmerged := old == Fixed && f.Status == Reopened
		if p.ReopenReason != nil {
			if !unmerged {
				return problem("invalid-status-transition", "findings.toml", id+": reopen-reason is only for fixed -> reopened")
			}
			f.ReopenReason = *p.ReopenReason
		}
		switch {
		case unmerged:
			// The record is offline and cannot see the PR: the reason is the
			// human's word that it did not merge. A merged fix needs a new
			// finding, never a reopen.
			if p.ReopenReason == nil || strings.TrimSpace(*p.ReopenReason) == "" {
				return problem("missing-reopen-reason", "findings.toml", id+": fixed -> reopened requires reopen-reason, only when the finding's PR did not merge")
			}
		case old == ExemptStatus && f.Status == Reopened:
			// A declined renewal: only a lapsed exemption may be left, and
			// the reopened finding no longer points at it.
			if p.ExemptionID != nil && *p.ExemptionID != "" {
				return problem("invalid-status-transition", "findings.toml", id+": reopened findings carry no exemption-id")
			}
			lapsed, err := s.lapsed(oldExemption)
			if err != nil {
				return err
			}
			if !lapsed {
				return problem("invalid-status-transition", "findings.toml", fmt.Sprintf("%s: exempt -> reopened only after %s's review-by has passed", id, oldExemption))
			}
			f.ExemptionID = ""
		case !allowed(old, f.Status):
			return problem("invalid-status-transition", "findings.toml", fmt.Sprintf("%s: %s -> %s", id, old, f.Status))
		}
		if err := validateFinding(f); err != nil {
			return err
		}
		if old == InPR && f.Status == Fixed {
			body, readErr := s.read("receipts/" + id + ".md")
			if err := fixProblem(f, body, readErr); err != nil {
				return err
			}
		}
		if f.ExemptionID != "" {
			var es exemptionsFile
			if err := s.load("exemptions.toml", &es); err != nil {
				return err
			}
			matched := false
			for _, e := range es.Exemptions {
				if e.ID == f.ExemptionID {
					review, err := time.Parse("2006-01-02", e.ReviewBy)
					if err != nil || review.Before(today()) {
						return problem("expired-exemption", "findings.toml", id)
					}
					if err := validateExemption(e); err != nil {
						return err
					}
					if e.Waste != f.Waste || e.Location != f.Location {
						return problem("unmatched-exemption", "findings.toml", id)
					}
					matched = true
				}
			}
			if !matched {
				return problem("dangling-exemption", "findings.toml", id)
			}
		}
		doc.Findings[i] = f
		return s.writeTOML("findings.toml", doc)
	}
	return problem("unknown-finding", "findings.toml", id)
}

// lapsed reports whether exemption id exists and its review-by is before
// today (UTC).
func (s *Store) lapsed(id string) (bool, error) {
	var es exemptionsFile
	if err := s.load("exemptions.toml", &es); err != nil {
		return false, err
	}
	for _, e := range es.Exemptions {
		if e.ID == id {
			review, err := time.Parse("2006-01-02", e.ReviewBy)
			return err == nil && review.Before(today()), nil
		}
	}
	return false, nil
}
