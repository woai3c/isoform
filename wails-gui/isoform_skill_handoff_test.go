package main

import (
	"encoding/json"
	"strings"
	"testing"

	"wails-gui/internal/handoff"
	"wails-gui/internal/sop"
	"yansu-agent/isoformskill"
)

func TestOneTimeWorkPreservesResultContract(t *testing.T) {
	criteria := []string{"The report exists", "The report answers the user's question"}
	prompt := buildHandoffResultInstructions(handoff.Entry{Criteria: criteria})
	encoded, err := json.Marshal(criteria)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, string(encoded)) {
		t.Fatal("one-time handoff lost exact success criteria")
	}
	assertNativeSkillBoundary(t, prompt)
	assertControlledResultContract(t, isoformskill.Prompt(prompt))
	if strings.Count(prompt, handoffResultFence) != 1 || !strings.HasSuffix(prompt, "\n```") {
		t.Fatal("skill instructions changed the terminal handoff-result contract")
	}
}

func TestPlaybookResultContractDoesNotExpandApprovedScope(t *testing.T) {
	entry := sop.Entry{Title: "Read the report", Body: "Summarize the approved report."}
	for _, sites := range [][]string{nil, {"https://example.com/"}} {
		prompt := playbookRunPrompt(entry, sites)
		assertNativeSkillBoundary(t, prompt)
		assertControlledResultContract(t, isoformskill.Prompt(prompt))
		if !strings.HasSuffix(prompt, entry.Body) {
			t.Fatal("playbook changed its steps")
		}
		if !strings.Contains(prompt, "<playbook_result> and </playbook_result>") {
			t.Fatal("playbook result routing contract was lost")
		}
		if len(sites) == 0 {
			if !strings.Contains(prompt, "This playbook may not open web pages") {
				t.Fatal("skill instructions expanded a playbook's approved scope")
			}
		} else if !strings.Contains(prompt, "only under: https://example.com/") || !strings.Contains(prompt, "Do not click, type, sign in, or change anything") {
			t.Fatal("skill instructions lost the approved site or read-only action limits")
		}
	}
}

func assertControlledResultContract(t *testing.T, prompt string) {
	t.Helper()
	if strings.Count(prompt, isoformskill.ResultContract) != 1 || strings.Count(prompt, "## Report back") != 1 || strings.Count(prompt, isoformskill.Instructions) != 1 {
		t.Fatal("controlled prompt must carry result guidance exactly once")
	}
	for _, manual := range []string{"# Isoform\n", "## Start with the handoff", "## Read context through MCP", "## The CLI"} {
		if strings.Contains(prompt, manual) {
			t.Errorf("controlled prompt contains manual section %q", manual)
		}
	}
}

func TestHandoffSkillResultContractIsSentOnceOnFirstAndContinuedTurns(t *testing.T) {
	for _, turn := range []string{"first", "continued", "follow-up"} {
		t.Run(turn, func(t *testing.T) {
			rig := newAgencyHandoffTestRig(t)
			accepted, err := rig.app.AcceptHandoff(rig.entry.ID)
			if err != nil {
				t.Fatal(err)
			}
			text := accepted.Prompt
			switch turn {
			case "continued":
				continued, err := rig.app.ResolveAgencyHandoffExecution(AgencyHandoffContinuationInput{
					HandoffID: rig.entry.ID, SessionID: accepted.SessionID,
				})
				if err != nil {
					t.Fatal(err)
				}
				text = continued.Prompt
			case "follow-up":
				text = "Check the report again."
			}
			runner := wireHandoffPromptCapture(t, rig.app)
			composed := sendHandoffBoundaryPrompt(t, rig.app, runner, text, accepted.Workdir, accepted.SessionID)
			assertNativeSkillBoundary(t, composed)
			prompt := isoformskill.Prompt(composed)
			assertControlledResultContract(t, prompt)
			if strings.Count(prompt, handoffResultFence) != 1 || strings.Count(prompt, "## Success criteria and result") != 1 {
				t.Fatal("handoff must send its task-specific result contract exactly once")
			}
			criteria, _ := json.Marshal(rig.entry.Criteria)
			if !strings.Contains(prompt, "Success criteria, in order: "+string(criteria)) || !strings.Contains(prompt, text) {
				t.Fatal("handoff lost its current criteria or turn text")
			}
			waitForHandoffSettle(t, rig.handoffTestRig, rig.entry.ID)
		})
	}
}

// The capture runner receives exactly what the native runner consumes; only
// external execution adds the generic result section at its transport boundary.
func assertNativeSkillBoundary(t *testing.T, prompt string) {
	t.Helper()
	if strings.Count(prompt, isoformskill.Instructions) != 1 {
		t.Fatal("native run must retain the trust/scope instructions exactly once")
	}
	for _, manual := range []string{"## Report back", "# Isoform\n", "## Start with the handoff", "## Read context through MCP", "## The CLI"} {
		if strings.Contains(prompt, manual) {
			t.Errorf("native task contains unsolicited manual section %q", manual)
		}
	}
}
