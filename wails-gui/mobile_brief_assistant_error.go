package main

import (
	"context"

	"yansu-agent/agent"
)

func (a *App) mobileBriefModelCall(ctx context.Context, prompt string) (string, error) {
	var backend string
	result, err := a.runEphemeralClaudeModelObserved(ctx, agent.BackendHintEphemeral, prompt, a.chatTaskModel(), func(event *agent.StreamEvent) {
		if event != nil && event.Type == "backend_route" {
			backend = event.Backend
		}
	}, mobileBriefNarrativeTimeout)
	return result, mobileBriefAttributeAssistantError(err, backend)
}

// mobileBriefAssistantError retains only a backend actually selected by the
// router. The native runner can use other providers, so its identity cannot
// establish which account rejected authentication.
type mobileBriefAssistantError struct {
	assistant string
	err       error
}

func (e *mobileBriefAssistantError) Error() string { return e.err.Error() }
func (e *mobileBriefAssistantError) Unwrap() error { return e.err }

func mobileBriefAttributeAssistantError(err error, backend string) error {
	if err == nil || !handoffRunAuthFailed(err) {
		return err
	}
	switch backend {
	case string(agent.BackendClaudeCode), string(agent.BackendCodex):
		return &mobileBriefAssistantError{assistant: backend, err: err}
	default:
		return err
	}
}
