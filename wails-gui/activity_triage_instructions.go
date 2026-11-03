package main

import "strings"

// triageInstructionKey is the UI-state key holding the user's custom triage
// guidance for a given source ("activity" for screen sessions). Keep in sync
// with the frontend settings that read/write it.
func triageInstructionKey(source string) string {
	return "triage_instructions_" + source
}

// triageInstructionForSource loads the user's custom triage guidance for the
// given source, or "" when unset. Appended (advisory) to the triage prompt.
func (a *App) triageInstructionForSource(source string) string {
	if source == "" {
		return ""
	}
	val, err := a.LoadUIState(triageInstructionKey(source))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(val)
}
