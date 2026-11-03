package toolserver

import (
	"container/list"
	"encoding/json"

	"github.com/mark3labs/mcp-go/mcp"
)

const (
	maxReplayResultBytes = 8 << 20
	maxReplayCalls       = 4096
)

type callOutcome uint8

const (
	callPending callOutcome = iota
	callSucceeded
	callFailed
)

// The tombstone survives result eviction and session disconnect. Refusing new
// IDs at the run limit bounds metadata without allowing an old ID to run again.
type completedCall struct {
	name         string
	argumentHash string
	outcome      callOutcome
	cachedResult *list.Element
}

type cachedCall struct {
	id        string
	sessionID string
	encoded   []byte
}

func (call completedCall) replay() *mcp.CallToolResult {
	if call.cachedResult != nil {
		var result mcp.CallToolResult
		if json.Unmarshal(call.cachedResult.Value.(cachedCall).encoded, &result) == nil {
			return &result
		}
	}
	return mcp.NewToolResultError("tool call result no longer available, not run again")
}

// completeCall and evictResult require callMu. Only encoded results are kept,
// so the budget includes image data and JSON escaping without retaining a
// second copy of the native or MCP result. Replays do not refresh FIFO order.
func (s *Server) completeCall(id, sessionID string, result *mcp.CallToolResult) {
	call := s.calls[id]
	call.outcome = callSucceeded
	if result.IsError {
		call.outcome = callFailed
	}
	s.calls[id] = call
	if tool := s.gate.Config.Registry.Get(call.name); tool != nil {
		if fresh, ok := tool.(interface{ RequiresFreshRead() bool }); ok && fresh.RequiresFreshRead() {
			return
		}
	}
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded) > maxReplayResultBytes {
		return
	}
	for s.resultBytes+len(encoded) > maxReplayResultBytes {
		s.evictResult(s.results.Front())
	}
	call.cachedResult = s.results.PushBack(cachedCall{id: id, sessionID: sessionID, encoded: encoded})
	s.resultBytes += len(encoded)
	s.calls[id] = call
}

func (s *Server) evictResult(element *list.Element) {
	result := element.Value.(cachedCall)
	call := s.calls[result.id]
	call.cachedResult = nil
	s.calls[result.id] = call
	s.resultBytes -= len(result.encoded)
	s.results.Remove(element)
}

func (s *Server) releaseSessionResults(sessionID string) {
	s.callMu.Lock()
	defer s.callMu.Unlock()
	for element := s.results.Front(); element != nil; {
		next := element.Next()
		if element.Value.(cachedCall).sessionID == sessionID {
			s.evictResult(element)
		}
		element = next
	}
}
