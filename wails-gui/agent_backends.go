package main

import (
	"strings"

	"yansu-agent/agent"

	"wails-gui/internal/handoff"
)

// chatBackendHint marks a chat turn that executes an in-progress handoff, so
// History can distinguish handoff work. Both use the same assistant order.
func (a *App) chatBackendHint(sessionTag string) string {
	if a != nil && a.sessionRunsHandoff(sessionTag) {
		return agent.BackendHintHandoff
	}
	return agent.BackendHintChat
}

// sessionRunsHandoff reports whether a chat session is the execution session
// of a handoff that is in progress.
func (a *App) sessionRunsHandoff(sessionTag string) bool {
	sessionTag = strings.TrimSpace(sessionTag)
	if sessionTag == "" || a == nil || a.handoffStore == nil {
		return false
	}
	entries, err := a.handoffStore.List()
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.ChatSessionID == sessionTag && e.Status == handoff.StatusInProgress {
			return true
		}
	}
	return false
}
