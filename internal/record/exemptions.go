package record

import (
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// now is the record's clock; tests substitute it. Expiry is judged on the
// UTC calendar day.
var now = time.Now

func today() time.Time { return now().UTC().Truncate(24 * time.Hour) }

var numericStep = regexp.MustCompile(`^[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$`)
var exemptionID = regexp.MustCompile(`^E-[A-Za-z0-9][A-Za-z0-9_-]*$`)

// Locations have a full grammar, not a permissive prefix match. Gate IDs are
// opaque producer identities (colons and workflow-path slashes are significant).
// Workflow paths are relative slash-separated components; job and step names
// may contain spaces/colons. Percent escaping represents reserved slashes or
// percent signs in names; stored identities are not normalized or fuzzy-matched.
func validLocation(v string) bool {
	if id, ok := strings.CutPrefix(v, "gate:"); ok {
		return stableText(id) // Do not parse workflow markers inside opaque gate IDs.
	}
	rest, ok := strings.CutPrefix(v, "workflow:")
	if !ok {
		return false
	}
	path, job, hasJob := strings.Cut(rest, "/job:")
	for _, part := range strings.Split(path, "/") {
		// Raw colons are reserved for grammar markers in workflow path components.
		decoded, err := url.PathUnescape(part)
		if strings.Contains(part, ":") || err != nil || strings.Contains(decoded, "/") || !stableText(decoded) {
			return false
		}
	}
	if !hasJob {
		return true
	}
	parts := strings.Split(job, "/")
	if len(parts) > 2 || !escapedComponent(parts[0]) {
		return false
	}
	if len(parts) == 1 {
		return true
	}
	step, ok := strings.CutPrefix(parts[1], "step:")
	if !ok || !escapedComponent(step) {
		return false
	}
	decoded, err := url.PathUnescape(step)
	return err == nil && !numericStep.MatchString(strings.TrimSpace(decoded))
}

func escapedComponent(v string) bool {
	if v == "" {
		return false
	}
	decoded, err := url.PathUnescape(v)
	return err == nil && stableText(decoded)
}

func stableText(v string) bool {
	if strings.TrimSpace(v) == "" || strings.Contains(v, "\\") {
		return false
	}
	for _, r := range v {
		if unicode.IsControl(r) {
			return false
		}
	}
	for _, part := range strings.Split(v, "/") {
		if strings.TrimSpace(part) == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}
func validateExemption(e Exemption) error {
	if !exemptionID.MatchString(e.ID) {
		return problem("invalid-exemption-id", "exemptions.toml", e.ID)
	}
	if e.Waste == "" || e.Location == "" || e.Reason == "" || e.AgreedBy == "" || e.Date == "" || e.ReviewBy == "" {
		return problem("missing-exemption-fields", "exemptions.toml", e.ID+": waste, location, reason, agreed-by, date and review-by required")
	}
	if !validLocation(e.Location) {
		return problem("invalid-location", "exemptions.toml", e.ID)
	}
	date, err := time.Parse("2006-01-02", e.Date)
	if err != nil {
		return problem("invalid-date", "exemptions.toml", e.ID+": date")
	}
	review, err := time.Parse("2006-01-02", e.ReviewBy)
	if err != nil || review.Before(date) {
		return problem("invalid-date", "exemptions.toml", e.ID+": review-by")
	}
	return nil
}
func (s *Store) Exempt(e Exemption) (err error) {
	s, release, err := s.mutation()
	if err != nil {
		return err
	}
	defer func() {
		if releaseErr := release(); err == nil {
			err = releaseErr
		}
	}()
	if err := validateExemption(e); err != nil {
		return err
	}
	// New exemptions bind the full raw-file SHA-256; older records' 12-hex
	// prefixes still load and match, but are never written again.
	if id, ok := strings.CutPrefix(e.Location, "gate:"); ok && LegacyProofID(id) {
		return problem("invalid-location", "exemptions.toml", e.ID+": a workflow proof ID needs the full 64-hex SHA-256 that muda compare reports")
	}
	// Stored exemptions may lapse later; a new one may not start expired.
	if review, _ := time.Parse("2006-01-02", e.ReviewBy); review.Before(today()) {
		return problem("invalid-date", "exemptions.toml", e.ID+": review-by is before today (UTC)")
	}
	var doc exemptionsFile
	if err := s.load("exemptions.toml", &doc); err != nil {
		return err
	}
	for _, old := range doc.Exemptions {
		if old.ID == e.ID {
			return problem("duplicate-exemption", "exemptions.toml", e.ID)
		}
	}
	doc.Exemptions = append(doc.Exemptions, e)
	return s.writeTOML("exemptions.toml", doc)
}
