package record

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

func validateReceipt(r Receipt) error {
	if err := schemaCheck(r.Schema, "receipts"); err != nil {
		return err
	}
	if !ValidFindingID(r.FindingID) {
		return problem("invalid-finding-id", "receipts", r.FindingID)
	}
	if r.Before == "" || r.After == "" || len(r.RunLinks) == 0 || r.GateResult == "" || strings.TrimSpace(r.PR) == "" {
		return problem("missing-receipt-fields", "receipts", r.FindingID+": pr, before, after, run-links and gate-result required")
	}
	switch r.GateStatus {
	case GateVerified, GateExempted, GateUnverified, GateWeakened:
	default:
		return problem("invalid-gate-status", "receipts", r.FindingID+": gate-status must be VERIFIED, EXEMPTED, UNVERIFIED or WEAKENED")
	}
	for _, link := range r.RunLinks {
		if !strings.HasPrefix(link, "https://github.com/") || !strings.Contains(link, "/actions/runs/") {
			return problem("invalid-run-link", "receipts", r.FindingID)
		}
	}
	return nil
}
func parseReceipt(b []byte, name string) (Receipt, error) {
	var r Receipt
	parts := strings.SplitN(string(b), "+++\n", 3)
	if len(parts) != 3 || parts[0] != "" {
		return r, problem("invalid-receipt", name, "expected +++ TOML front block")
	}
	err := decodeTOML([]byte(parts[1]), name, &r)
	return r, err
}
func (s *Store) WriteReceipt(id string, r Receipt) (err error) {
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
		return problem("invalid-finding-id", "receipts", id)
	}
	if r.Schema == 0 {
		r.Schema = Schema
	}
	if r.FindingID == "" {
		r.FindingID = id
	}
	if r.FindingID != id {
		return problem("receipt-id-mismatch", "receipts", id)
	}
	if err := validateReceipt(r); err != nil {
		return err
	}
	var fs findingsFile
	if err := s.load("findings.toml", &fs); err != nil {
		return err
	}
	found := false
	for _, f := range fs.Findings {
		if f.ID == id {
			found = true
		}
	}
	if !found {
		return problem("dangling-receipt", "receipts", id)
	}
	name := "receipts/" + id + ".md"
	if b, err := s.read(name); err == nil {
		if _, err = parseReceipt(b, name); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err = s.receiptsDir(); err != nil {
		return err
	}
	front, err := encode(r)
	if err != nil {
		return err
	}
	body := fmt.Sprintf("+++\n%s+++\n\n# %s receipt\n\n## Before\n\n%s\n\n## After\n\n%s\n\n## Runs\n\n", front, id, r.Before, r.After)
	for _, link := range r.RunLinks {
		body += "- " + link + "\n"
	}
	body += "\n## Gate result\n\n" + r.GateResult + "\n"
	return s.write(name, []byte(body))
}

// fixProblem reports why finding f may not be (or remain) fixed given its
// receipt body: it must exist, parse, pass the gates (VERIFIED or EXEMPTED)
// and belong to the finding's current PR, not an earlier cycle.
func fixProblem(f Finding, body []byte, readErr error) error {
	name := "receipts/" + f.ID + ".md"
	if readErr != nil {
		return problem("unverified-fix", "findings.toml", f.ID+": fixed requires "+name)
	}
	r, err := parseReceipt(body, name)
	if err != nil {
		return problem("unverified-fix", "findings.toml", f.ID+": unreadable "+name)
	}
	if !GatePassed(r.GateStatus) {
		return problem("unverified-fix", "findings.toml", f.ID+": receipt gate-status "+r.GateStatus+" is not VERIFIED or EXEMPTED")
	}
	if r.PR != f.PR {
		return problem("unverified-fix", "findings.toml", f.ID+": receipt pr does not match the finding's pr")
	}
	return nil
}

// receiptsDir creates receipts/ when a cloned record lacks it (git keeps no
// empty directory).
func (s *Store) receiptsDir() error {
	r, err := s.root()
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	if err = safe(r, "receipts"); err != nil {
		return err
	}
	if err = r.Mkdir("receipts", 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	return nil
}
