package gates

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

func WriteJSON(w io.Writer, r *Inventory) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}
func RenderMarkdown(r *Inventory) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# muda gates: %s\n\nCandidate gates for default branch `%s` (repository settings), with workflows at `%s`; confirm their roles before changing them.\n", r.Repo, r.Branch, r.Ref)
	if len(r.Gates) > 0 {
		b.WriteString("\n## Gates\n")
		for _, g := range r.Gates {
			fmt.Fprintf(&b, "\n- `%s`: %s; protects %s\n  - %s\n", g.ID, g.Detail, g.Protects, g.URL)
		}
	}
	for _, s := range r.Security {
		if s.What == "branch protection" && s.Absent {
			fmt.Fprintf(&b, "\n## Branch security settings\n\nNo branch protection: GitHub reports `%s` is not protected.\n", r.Branch)
		}
	}
	if r.Protection != nil {
		fmt.Fprintf(&b, "\n## Branch security settings\n\nRequired reviews: %d; strict status checks: %t; enforce admins: %t.\n", r.Protection.RequiredPullRequestReviews.RequiredApprovingReviewCount, r.Protection.RequiredStatusChecks.Strict, r.Protection.EnforceAdmins.Enabled)
		fmt.Fprintf(&b, "\nSource: https://github.com/%s/settings/branches\n", r.Repo)
	}

	if len(r.Security) > 0 {
		b.WriteString("\n## Complete security evidence\n\nRaw canonical payloads retain unknown fields and null/presence. Incomplete sources cannot certify unchanged security.\n")
		for _, snapshot := range r.Security {
			fmt.Fprintf(&b, "\n### %s\n\nSource: %s\n\nComplete: %t.\n", snapshot.What, snapshot.URL, snapshot.Complete)
			if snapshot.Absent {
				b.WriteString("\nNot configured: GitHub reports no branch protection.\n")
			}
			if snapshot.Why != "" {
				fmt.Fprintf(&b, "\nUnavailable: %s\n", snapshot.Why)
			}
			if len(snapshot.Raw) > 0 {
				var pretty bytes.Buffer
				if err := json.Indent(&pretty, snapshot.Raw, "", "  "); err == nil {
					fmt.Fprintf(&b, "\n```json\n%s\n```\n", pretty.String())
				} else {
					fmt.Fprintf(&b, "\nRaw security unavailable: %v\n", err)
				}
			}
		}
		for _, set := range r.Rulesets {
			fmt.Fprintf(&b, "\nEffective rules source: %s\n", set.EffectiveURL)
		}
	}
	if len(r.Sources) > 0 {
		b.WriteString("\n## Workflow execution evidence\n\nLiteral conditions, failure settings and commands; dependencies alone do not prove enforcement. Absent settings are not resolved to implicit policy.\n")
		for _, f := range r.Sources {
			fmt.Fprintf(&b, "\n### %s\n\nWorkflow %s: %s\n", f.Path, f.Name, f.URL)
			fmt.Fprintf(&b, "\nWorkflow defaults literal: %s\n", policyLiteral(f.Defaults))
			if f.Env != "" {
				fmt.Fprintf(&b, "\nWorkflow env literal: `%s`\n", f.Env)
			}
			for _, j := range f.Jobs {
				fmt.Fprintf(&b, "\n- Job `%s` (%s), line %d: %s\n  - if: `%s`; needs: `%s`; environment: `%s`; uses: `%s`\n", j.ID, j.Name, j.Line, j.URL, j.If, strings.Join(j.Needs, ", "), j.Environment, j.Uses)
				fmt.Fprintf(&b, "  - Job continue-on-error literal: %s\n  - Job defaults literal: %s\n", policyLiteral(j.ContinueOnError), policyLiteral(j.Defaults))
				if j.Env != "" {
					fmt.Fprintf(&b, "  - Job env literal: `%s`\n", j.Env)
				}
				if j.Matrix != "" {
					fmt.Fprintf(&b, "  - Matrix literal:\n\n```yaml\n%s\n```\n", j.Matrix)
				}
				for _, step := range j.Steps {
					fmt.Fprintf(&b, "  - Step %s, line %d: %s\n    - if: `%s`; uses: `%s`\n", step.Name, step.Line, step.URL, step.If, step.Uses)
					fmt.Fprintf(&b, "    - Step continue-on-error literal: %s\n    - Step shell literal: %s\n    - Step working-directory literal: %s\n", policyLiteral(step.ContinueOnError), policyLiteral(step.Shell), policyLiteral(step.WorkingDirectory))
					if step.Env != "" {
						fmt.Fprintf(&b, "    - Step env literal: `%s`\n", step.Env)
					}
					if len(step.With) > 0 {
						inputs, _ := json.Marshal(step.With)
						fmt.Fprintf(&b, "    - Action inputs: `%s`\n", inputs)
					}
					if step.Run != "" {
						fmt.Fprintf(&b, "\n```text\n%s\n```\n", step.Run)
					}
				}
			}
		}
	}
	if len(r.Unavailable) > 0 {
		b.WriteString("\n## Unavailable\n")
		for _, u := range r.Unavailable {
			fmt.Fprintf(&b, "\n- %s: %s\n", u.What, u.Why)
		}
	}
	if len(r.Gates) == 0 {
		b.WriteString("\nNo candidate gates observed in readable sources; this is not a no-gates pass.\n")
	}
	return b.String()
}

// policyLiteral shows absence without inventing a boolean or shell default.
func policyLiteral(value string) string {
	if value == "" {
		return "absent"
	}
	return "`" + value + "`"
}
