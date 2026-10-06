package record

import (
	"regexp"
	"strings"
)

// Synthetic policy-proof gate IDs that muda compare --gates emits in its
// unverified/loosened/strengthened lists. They are not inventory gates, so a
// gate: exemption for one is matched by this vocabulary, not by gates.toml.
// compare constructs every such ID from these definitions.
const (
	// PolicyInventory: the recorded or derived inventory is unavailable, or
	// its schema/repository does not match.
	PolicyInventory = "policy:inventory"
	// PolicyInventoryRaw: the raw recorded inventory has unknown or omitted
	// fields, so execution proof cannot be certified.
	PolicyInventoryRaw = "policy:inventory:raw"
	// PolicyInventoryProjection: branch or security projection changed.
	PolicyInventoryProjection = "policy:inventory:projection"
	// PolicySecurity: a complete security inventory is missing.
	PolicySecurity = "policy:security"

	// Parameterized forms: prefix + producer identity.
	PolicyAvailabilityPrefix = "policy:availability:" // + unavailable item (What); a workflow file adds "@" + raw file digest
	PolicyGatePrefix         = "policy:gate:"         // + gate ID; empty for a missing identity
	PolicySecurityPrefix     = "policy:security:"     // + security snapshot identity (What)
	PolicyWorkflowPrefix     = "policy:workflow:"     // + workflow source path + "@" + raw file digest

	// WorkflowDir is the directory whose files' availability IDs must carry
	// a raw file digest.
	WorkflowDir = ".github/workflows/"
)

// WorkflowDigestLen is the number of lowercase hex digits of the SHA-256 of a
// workflow file's raw bytes (as gates records it) that a workflow or
// workflow-availability proof ID carries: the full hash.
const WorkflowDigestLen = 64

// LegacyWorkflowDigestLen is the 12-hex digest prefix that older records'
// exemption locations carry. It is accepted when read and matched by prefix
// (ProofCovers); record exempt no longer writes it.
const LegacyWorkflowDigestLen = 12

// workflowProof matches a file-bound workflow proof ID suffix:
// <non-empty path>@<64 lowercase hex>, or the legacy <path>@<12 lowercase hex>.
// A digest-less form is not a proof ID, so no exemption can cover a workflow
// whose bytes are unknown.
var workflowProof = regexp.MustCompile(`^.+@(?:[0-9a-f]{64}|[0-9a-f]{12})$`)

// fileBound splits a workflow or workflow-availability proof ID into its
// digest-less stem (prefix and path) and digest. ok is false for any other ID.
func fileBound(id string) (stem, digest string, ok bool) {
	if !IsPolicyProofID(id) {
		return "", "", false
	}
	rest, isWorkflow := strings.CutPrefix(id, PolicyWorkflowPrefix)
	if !isWorkflow {
		if rest, ok = strings.CutPrefix(id, PolicyAvailabilityPrefix+WorkflowDir); !ok {
			return "", "", false
		}
	}
	at := strings.LastIndex(rest, "@")
	return id[:len(id)-len(rest)+at], rest[at+1:], true
}

// LegacyProofID reports whether id is a file-bound proof ID carrying the
// older 12-hex digest prefix rather than the full SHA-256.
func LegacyProofID(id string) bool {
	_, digest, ok := fileBound(id)
	return ok && len(digest) == LegacyWorkflowDigestLen
}

// ProofCovers reports whether an exemption's gate ID covers the gate ID
// compare emitted. IDs match exactly, except that a legacy 12-hex file-bound
// exemption covers the same prefix and path whose full digest starts with
// those 12 digits; only prefix-form entries are compared by prefix.
func ProofCovers(exempt, id string) bool {
	if exempt == id {
		return true
	}
	stem, prefix, ok := fileBound(exempt)
	if !ok || len(prefix) != LegacyWorkflowDigestLen {
		return false
	}
	idStem, digest, ok := fileBound(id)
	return ok && idStem == stem && len(digest) == WorkflowDigestLen && strings.HasPrefix(digest, prefix)
}

// IsPolicyProofID reports whether id is exactly one of the synthetic proof IDs
// compare emits: the four fixed IDs, or a parameterized prefix with a
// non-empty suffix (policy:gate: also allows the empty suffix compare uses for
// a missing gate identity). A workflow proof ID must carry its raw file digest
// (policy:workflow:<path>@<64 lowercase hex>, or an older record's 12-hex
// prefix), and so must an availability ID
// naming a file under .github/workflows/ (an unparseable workflow): a file
// that could not be read has no digest and cannot be exempted, nor can the
// unreadable directory itself (policy:availability:.github/workflows).
// Nothing broader is accepted.
func IsPolicyProofID(id string) bool {
	switch id {
	case PolicyInventory, PolicyInventoryRaw, PolicyInventoryProjection, PolicySecurity:
		return true
	}
	if strings.HasPrefix(id, PolicyGatePrefix) {
		return true
	}
	if rest, ok := strings.CutPrefix(id, PolicyWorkflowPrefix); ok {
		return workflowProof.MatchString(rest)
	}
	if rest, ok := strings.CutPrefix(id, PolicyAvailabilityPrefix+WorkflowDir); ok {
		return workflowProof.MatchString(rest)
	}
	// An unreadable workflow directory has no bytes to bind: never exemptable.
	if id == PolicyAvailabilityPrefix+strings.TrimSuffix(WorkflowDir, "/") {
		return false
	}
	for _, p := range []string{PolicyAvailabilityPrefix, PolicySecurityPrefix} {
		if rest, ok := strings.CutPrefix(id, p); ok && rest != "" {
			return true
		}
	}
	return false
}
