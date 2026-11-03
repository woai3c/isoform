package native

import "testing"

func TestModelSupportsProgrammaticToolCalling(t *testing.T) {
	for _, model := range []string{
		"opus",
		"sonnet",
		"claude-opus-4-7",
		"claude-opus-4-6",
		"claude-sonnet-4-6",
		"claude-opus-4-5-20251101",
		"claude-sonnet-4-5-20250929",
	} {
		t.Run(model, func(t *testing.T) {
			if !modelSupportsProgrammaticToolCalling(model) {
				t.Fatalf("modelSupportsProgrammaticToolCalling(%q) = false, want true", model)
			}
		})
	}

	for _, model := range []string{
		"",
		"haiku",
		"claude-haiku-4-5-20251001",
		"claude-sonnet-3-7",
	} {
		t.Run("unsupported_"+model, func(t *testing.T) {
			if modelSupportsProgrammaticToolCalling(model) {
				t.Fatalf("modelSupportsProgrammaticToolCalling(%q) = true, want false", model)
			}
		})
	}
}

func TestSupportsProgrammaticCalling(t *testing.T) {
	for _, toolName := range []string{"Bash", "Glob", "Grep", "Read", "WebFetch", "Write"} {
		t.Run(toolName, func(t *testing.T) {
			if !supportsProgrammaticCalling(toolName) {
				t.Fatalf("supportsProgrammaticCalling(%q) = false, want true", toolName)
			}
		})
	}

	for _, toolName := range []string{"Programmatic", "cua_driver", "WebSearch", "Edit", "BashOutput", "KillShell"} {
		t.Run("direct_"+toolName, func(t *testing.T) {
			if supportsProgrammaticCalling(toolName) {
				t.Fatalf("supportsProgrammaticCalling(%q) = true, want false", toolName)
			}
		})
	}
}
