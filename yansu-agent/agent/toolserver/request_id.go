package toolserver

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
)

type requestIDKey struct{}

type requestIdentity struct {
	sessionID string
	id        string
}

// The SDK's CallToolRequest omits the JSON-RPC ID. Capture it before dispatch
// rather than minting a different ToolCall.ID whenever a client retries. MCP
// does not promise the model's tool-use ID or a resumable session identity.
// Request counters may restart on a new session, so only calls in the same
// live MCP session can be recognized as retries.
func withRequestID(ctx context.Context, message json.RawMessage, sessionID string) context.Context {
	if sessionID == "" {
		return ctx
	}
	var request struct {
		ID json.RawMessage `json:"id"`
	}
	if json.Unmarshal(message, &request) != nil {
		return ctx
	}
	request.ID = bytes.TrimSpace(request.ID)
	var text string
	if len(request.ID) > 0 && request.ID[0] == '"' && json.Unmarshal(request.ID, &text) == nil {
		return context.WithValue(ctx, requestIDKey{}, requestIdentity{sessionID: sessionID, id: "string:" + text})
	}
	// CLI request counters are integers. Reject other numeric forms instead of
	// rounding JSON floats, which could conflate distinct large request IDs.
	number, err := strconv.ParseInt(string(request.ID), 10, 64)
	if err != nil {
		return ctx
	}
	return context.WithValue(ctx, requestIDKey{}, requestIdentity{sessionID: sessionID, id: "number:" + strconv.FormatInt(number, 10)})
}

func (s *Server) toolCallID(ctx context.Context) (string, bool) {
	identity, ok := ctx.Value(requestIDKey{}).(requestIdentity)
	if !ok {
		return "", false
	}
	// MCP counters restart in a new session. Scope them to both this run's
	// server and its MCP session so GoalCreate and other tools keep using
	// ToolCall.ID without conflating distinct calls after a reconnect.
	return "tool_" + hash([]string{s.gate.Config.RunID, s.dir, identity.sessionID, identity.id}), true
}
