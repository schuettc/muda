package scan

import (
	"strings"

	"github.com/schuettc/muda/internal/signal"
)

// driftSummaryPrefix and expressionPinWhat are the producer's only wording
// for a toolchain-drift summary and an expression-valued pin note; the
// readers below invert them exactly, so consumers never parse the wording.
func driftSummaryPrefix(tool string) string {
	return tool + " toolchain versions differ across workflow files: "
}
func expressionPinWhat(path, job, tool string) string {
	return path + " job " + job + " " + tool + " version (expression)"
}

// knownTool reports whether tool is one setupActions names.
func knownTool(tool string) bool {
	for _, in := range setupActions {
		if in.tool == tool {
			return true
		}
	}
	return false
}

// DriftTool reads the tool a toolchain-drift signal is about. ok is false for
// any other signal or for one this producer could not have made.
func DriftTool(s signal.Signal) (string, bool) {
	if s.ID != "toolchain-drift" {
		return "", false
	}
	tool, _, ok := strings.Cut(s.Summary, " ")
	if !ok || !knownTool(tool) || !strings.HasPrefix(s.Summary, driftSummaryPrefix(tool)) {
		return "", false
	}
	return tool, true
}

// ExpressionPinTool reads the tool an expression-valued pin note names. ok is
// false for any other unavailable entry.
func ExpressionPinTool(u Unavailable) (string, bool) {
	rest, ok := strings.CutSuffix(u.What, " version (expression)")
	if !ok {
		return "", false
	}
	i := strings.LastIndex(rest, " ")
	if i < 0 {
		return "", false
	}
	tool := rest[i+1:]
	j := strings.LastIndex(rest[:i], " job ")
	if j <= 0 || !knownTool(tool) {
		return "", false
	}
	job := rest[j+len(" job ") : i]
	if job == "" || strings.Contains(job, " ") || expressionPinWhat(rest[:j], job, tool) != u.What {
		return "", false
	}
	return tool, true
}
