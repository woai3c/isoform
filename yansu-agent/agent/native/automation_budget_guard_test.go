package native

import (
	"encoding/json"
	"testing"
)

func TestIsUIAutomationToolCall(t *testing.T) {
	t.Run("cua driver", func(t *testing.T) {
		input := mustJSONForAutomationBudgetTest(t, map[string]any{
			"name": "get_window_state",
			"args": map[string]any{"bundle": "com.google.Chrome"},
		})
		if !isUIAutomationToolCall("cua_driver", input) {
			t.Fatal("cua_driver call was not classified as UI automation")
		}
	})

	t.Run("bash osascript", func(t *testing.T) {
		input := mustJSONForAutomationBudgetTest(t, map[string]any{
			"command": "osascript /tmp/check_page.scpt 2>&1",
		})
		if !isUIAutomationToolCall("Bash", input) {
			t.Fatal("osascript Bash command was not classified as UI automation")
		}
	})

	t.Run("write applescript", func(t *testing.T) {
		input := mustJSONForAutomationBudgetTest(t, map[string]any{
			"path":    "/tmp/check_page.scpt",
			"content": "tell application \"Google Chrome\"\nreturn title of active tab of front window\nend tell",
		})
		if !isUIAutomationToolCall("Write", input) {
			t.Fatal("AppleScript Write call was not classified as UI automation")
		}
	})

	t.Run("screenshot read", func(t *testing.T) {
		input := mustJSONForAutomationBudgetTest(t, map[string]any{
			"path": "/tmp/screen_after_click.png",
		})
		if !isUIAutomationToolCall("Read", input) {
			t.Fatal("screenshot Read call was not classified as UI automation")
		}
	})

	t.Run("ordinary coding tools", func(t *testing.T) {
		bashInput := mustJSONForAutomationBudgetTest(t, map[string]any{
			"command": "go test ./agent/native",
		})
		if isUIAutomationToolCall("Bash", bashInput) {
			t.Fatal("ordinary Bash command was incorrectly classified as UI automation")
		}
		writeInput := mustJSONForAutomationBudgetTest(t, map[string]any{
			"path":    "/tmp/main.go",
			"content": "package main\n",
		})
		if isUIAutomationToolCall("Write", writeInput) {
			t.Fatal("ordinary Write call was incorrectly classified as UI automation")
		}
	})
}

func TestCheckUIAutomationBudgetThresholds(t *testing.T) {
	input := mustJSONForAutomationBudgetTest(t, map[string]any{
		"name": "get_window_state",
		"args": map[string]any{"bundle": "com.google.Chrome"},
	})

	got := CheckUIAutomationBudget(UIAutomationNudgeThreshold-1, "cua_driver", input)
	if got.Kind != "nudge" || got.Count != UIAutomationNudgeThreshold || !got.Counted {
		t.Fatalf("nudge threshold result = %+v", got)
	}

	got = CheckUIAutomationBudget(UIAutomationHardThreshold-1, "cua_driver", input)
	if got.Kind != "hard" || got.Count != UIAutomationHardThreshold || !got.Counted {
		t.Fatalf("hard threshold result = %+v", got)
	}

	ordinary := mustJSONForAutomationBudgetTest(t, map[string]any{"command": "go test ./..."})
	got = CheckUIAutomationBudget(UIAutomationHardThreshold, "Bash", ordinary)
	if got.Kind != "ok" || got.Count != UIAutomationHardThreshold || got.Counted {
		t.Fatalf("ordinary tool should not count against UI automation budget: %+v", got)
	}
}

func mustJSONForAutomationBudgetTest(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
