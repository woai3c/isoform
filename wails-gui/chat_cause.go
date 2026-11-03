package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"yansu-agent/agent"
	"yansu-agent/agent/native"
)

// Chat error codes. AgentEvent.Code carries one when the computer can prove
// the cause; the line under the error row is read from the code, never from
// the words in the error text.
const (
	chatCodeNoAssistant       = "no_assistant"
	chatCodeSignedOut         = "signed_out"
	chatCodeRunnerUnavailable = "runner_unavailable"
	chatCodeServer            = "server"
	chatCodeAssistantError    = "assistant_error"
)

// errChatNoAssistant is returned by a send that stayed on the computer
// because no AI is configured.
var errChatNoAssistant = errors.New(chatCodeNoAssistant)

// errListRunnerUnavailable ends a list item run whose runner is not available.
var errListRunnerUnavailable = errors.New("the selected task runner is unavailable")

// chatSendAIReady is the send guard's check; tests replace it so a plain go
// test does not depend on the AI configured on the machine running it.
var chatSendAIReady = func(a *App) bool { return a.AIReady() }

// chatErrorCode names an error's cause from proof: the start-time AI check,
// the provider's HTTP status as a number, and the external CLI's own sign-in
// check. Anything it cannot prove is left to the UI's other kinds.
func chatErrorCode(status int, proof handoffStopProof) string {
	switch {
	case proof.NoAIAtStart:
		return chatCodeNoAssistant
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return chatCodeSignedOut
	case proof.External && proof.SignInErr == nil && !proof.SignedIn:
		return chatCodeSignedOut
	case status == http.StatusInternalServerError:
		return chatCodeAssistantError
	case status > http.StatusInternalServerError && status < 600:
		return chatCodeServer
	}
	return ""
}

// chatAssistantName is the {AI} in a cause line: Claude Code, Codex, or the
// provider's name for an API key. Empty when it isn't known.
func chatAssistantName(cfg AIProviderConfig) string {
	switch strings.ToLower(strings.TrimSpace(cfg.Provider)) {
	case "claude-code":
		return "Claude Code"
	case "codex":
		return "Codex"
	}
	id := cfg.PresetID
	if id == "" {
		id = cfg.Provider
	}
	if preset, ok := aiProviderPresetByID(id); ok {
		return preset.Name
	}
	return ""
}

// listRunnerName is the {AI} for a list item's runner.
func listRunnerName(backend string) string {
	switch backend {
	case string(agent.BackendClaudeCode):
		return "Claude Code"
	case string(agent.BackendCodex):
		return "Codex"
	}
	return ""
}

// withChatCause stamps an error event with its proven cause and the AI's name.
func (a *App) withChatCause(evt AgentEvent, status int) AgentEvent {
	if evt.Type != "error" || evt.Code != "" {
		return evt
	}
	evt.Code = chatErrorCode(status, handoffStopProbe(a))
	if evt.Assistant == "" {
		evt.Assistant = chatAssistantName(loadAIProviderConfig())
	}
	return evt
}

// chatFailureEvent is the error event for a run that failed with err. A list
// item whose runner is not available names that runner.
func (a *App) chatFailureEvent(tag, runID string, err error, listBackend string) AgentEvent {
	evt := AgentEvent{Type: "error", Tag: tag, RunID: runID, Error: err.Error()}
	if errors.Is(err, errListRunnerUnavailable) {
		evt.Code, evt.Assistant = chatCodeRunnerUnavailable, listRunnerName(listBackend)
	}
	return a.withChatCause(evt, chatHTTPStatus(err))
}

// keepChatSend ends a send that never left the computer: no runner is called,
// the stream gets a coded error and close, and the caller keeps the message.
func (a *App) keepChatSend(tag string) error {
	a.emitAgentEvent(AgentEvent{Type: "error", Tag: tag, Code: chatCodeNoAssistant})
	a.emitAgentEvent(AgentEvent{Type: "close", Tag: tag})
	if a.telemetry != nil {
		a.telemetry.Capture("chat.send_kept", map[string]any{"code": chatCodeNoAssistant})
	}
	return errChatNoAssistant
}

// chatHTTPStatus returns the provider's HTTP status that err carries, or 0.
func chatHTTPStatus(err error) int {
	var he *native.HTTPError
	if errors.As(err, &he) && he != nil {
		return he.Status
	}
	return 0
}

// chatErrorCause is an error row's cause, stored as its ToolInput JSON (an
// error row has no tool).
type chatErrorCause struct {
	Code      string `json:"code,omitempty"`
	Assistant string `json:"assistant,omitempty"`
}

func chatErrorCauseJSON(code, assistant string) string {
	if code == "" && assistant == "" {
		return ""
	}
	blob, _ := json.Marshal(chatErrorCause{Code: code, Assistant: assistant})
	return string(blob)
}

func parseChatErrorCause(raw string) chatErrorCause {
	var cause chatErrorCause
	if raw != "" && json.Unmarshal([]byte(raw), &cause) != nil {
		return chatErrorCause{}
	}
	return cause
}
