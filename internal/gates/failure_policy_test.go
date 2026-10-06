package gates

import (
	"encoding/json"
	"strings"
	"testing"
)

const failurePolicyWorkflow = `name: Deploy
on: push
defaults:
  run:
    shell: bash -e -o pipefail {0}
jobs:
  test:
    runs-on: ubuntu-latest
    continue-on-error: false
    defaults:
      run:
        shell: bash --noprofile -e {0}
    steps:
      - name: Check
        continue-on-error: false
        shell: bash --norc -e {0}
        working-directory: ./checks
        run: |
          go test ./...
          echo checks-finished
  deploy:
    runs-on: ubuntu-latest
    needs: test
    steps:
      - run: echo deploy
`

func encodedSources(t *testing.T, inv *Inventory) string {
	t.Helper()
	raw, err := json.Marshal(inv.Sources)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestFailurePolicySameLineWeakeningChangesSources(t *testing.T) {
	for _, tc := range []struct {
		name, from, to, literal        string
		omitJobDefaults, omitStepShell bool
	}{
		{"job continue-on-error", "\n    continue-on-error: false\n", "\n    continue-on-error: true\n", "Job continue-on-error literal: `true`", false, false},
		{"step continue-on-error", "\n        continue-on-error: false\n", "\n        continue-on-error: true\n", "Step continue-on-error literal: `true`", false, false},
		{"workflow defaults shell", "shell: bash -e -o pipefail {0}", "shell: bash {0}", "bash {0}", true, true},
		{"job defaults shell", "shell: bash --noprofile -e {0}", "shell: bash --noprofile {0}", "bash --noprofile {0}", false, true},
		{"step shell", "shell: bash --norc -e {0}", "shell: bash --norc {0}", "Step shell literal: `bash --norc {0}`", false, false},
		{"step working directory", "working-directory: ./checks", "working-directory: ./different-checks", "Step working-directory literal: `./different-checks`", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Defaults cases must actually inherit the setting under test, not
			// hide the weakening behind a stricter job/step shell override.
			baseYAML := failurePolicyWorkflow
			if tc.omitJobDefaults {
				baseYAML = strings.Replace(baseYAML, "    defaults:\n      run:\n        shell: bash --noprofile -e {0}\n", "", 1)
			}
			if tc.omitStepShell {
				baseYAML = strings.Replace(baseYAML, "        shell: bash --norc -e {0}\n", "", 1)
			}
			base := derive(t, false, baseYAML)
			changed := strings.Replace(baseYAML, tc.from, tc.to, 1)
			if changed == baseYAML || strings.Count(changed, "\n") != strings.Count(baseYAML, "\n") {
				t.Fatal("fixture must change value on the same line")
			}
			weaker := derive(t, false, changed)
			if base.Sources[0].Jobs[0].URL != weaker.Sources[0].Jobs[0].URL || base.Sources[0].Jobs[0].Steps[0].URL != weaker.Sources[0].Jobs[0].Steps[0].URL {
				t.Fatal("evidence lines changed; regression must isolate policy values")
			}
			if encodedSources(t, base) == encodedSources(t, weaker) {
				t.Error("same-line failure-policy weakening is invisible in JSON Sources")
			}
			md := RenderMarkdown(weaker)
			if !strings.Contains(md, tc.literal) {
				t.Errorf("missing policy literal %q", tc.literal)
			}
			for _, url := range []string{weaker.Sources[0].URL, weaker.Sources[0].Jobs[0].URL, weaker.Sources[0].Jobs[0].Steps[0].URL} {
				if !strings.Contains(md, url) {
					t.Errorf("missing policy source URL %s", url)
				}
			}
		})
	}
}

func TestFailurePolicyPresenceAndExpressions(t *testing.T) {
	explicitFalse := derive(t, false, failurePolicyWorkflow)
	absentYAML := strings.ReplaceAll(failurePolicyWorkflow, "continue-on-error: false", "# continue-on-error absent")
	absent := derive(t, false, absentYAML)
	if encodedSources(t, explicitFalse) == encodedSources(t, absent) {
		t.Error("explicit false and absent failure policy collapsed")
	}
	md := RenderMarkdown(absent)
	for _, text := range []string{"Job continue-on-error literal: absent", "Step continue-on-error literal: absent"} {
		if !strings.Contains(md, text) {
			t.Errorf("Markdown does not distinguish absence: %s", text)
		}
	}
	expressionYAML := strings.Replace(failurePolicyWorkflow, "\n    continue-on-error: false\n", "\n    continue-on-error: ${{ inputs.job_failure }}\n", 1)
	expressionYAML = strings.Replace(expressionYAML, "\n        continue-on-error: false\n", "\n        continue-on-error: ${{ inputs.step_failure }}\n", 1)
	expressionYAML = strings.Replace(expressionYAML, "bash -e -o pipefail {0}", "${{ inputs.workflow_shell }}", 1)
	expressionYAML = strings.Replace(expressionYAML, "bash --noprofile -e {0}", "${{ inputs.job_shell }}", 1)
	expressionYAML = strings.Replace(expressionYAML, "bash --norc -e {0}", "${{ inputs.step_shell }}", 1)
	expression := derive(t, false, expressionYAML)
	for _, value := range []string{"${{ inputs.job_failure }}", "${{ inputs.step_failure }}", "${{ inputs.workflow_shell }}", "${{ inputs.job_shell }}", "${{ inputs.step_shell }}"} {
		if !strings.Contains(encodedSources(t, expression), value) || !strings.Contains(RenderMarkdown(expression), value) {
			t.Errorf("expression lost or policy resolved: %s", value)
		}
	}
	var files []struct {
		Defaults string `json:"defaults"`
		Jobs     []struct {
			ContinueOnError string `json:"continue_on_error"`
			Defaults        string `json:"defaults"`
			Steps           []struct {
				ContinueOnError  string `json:"continue_on_error"`
				Shell            string `json:"shell"`
				WorkingDirectory string `json:"working_directory"`
			} `json:"steps"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal([]byte(encodedSources(t, explicitFalse)), &files); err != nil {
		t.Fatal(err)
	}
	if files[0].Defaults != "{run: {shell: bash -e -o pipefail {0}}}" || files[0].Jobs[0].Defaults != "{run: {shell: bash --noprofile -e {0}}}" || files[0].Jobs[0].ContinueOnError != "false" || files[0].Jobs[0].Steps[0].ContinueOnError != "false" || files[0].Jobs[0].Steps[0].Shell != "bash --norc -e {0}" || files[0].Jobs[0].Steps[0].WorkingDirectory != "./checks" {
		t.Errorf("missing exact literal failure settings: %+v", files)
	}
}
