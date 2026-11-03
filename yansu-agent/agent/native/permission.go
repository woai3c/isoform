package native

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// PermissionResult captures the user's response to a permission prompt.
type PermissionResult struct {
	OptionID string
	Granted  bool
	// Always means "persist this trust across processes". The loop
	// translates this into a callback that wails-gui uses to write the
	// MCP trusted-server file.
	Always bool
	// SessionAlways means "trust the same prompt prefix for the rest of
	// this chat session". The loop short-circuits subsequent calls
	// matching the prefix without ever asking the user, but the trust
	// dies with the session (sessionID changes on new/switched chats).
	SessionAlways bool
}

// PermissionManager coordinates in-flight permission requests per
// session. When the loop needs user consent it calls Await, and the
// frontend replies via Respond. Cancel aborts a pending request
// without tearing down the whole run.
type PermissionManager struct {
	mu      sync.Mutex
	pending map[string]chan PermissionResult
}

// NewPermissionManager returns an empty manager.
func NewPermissionManager() *PermissionManager {
	return &PermissionManager{pending: make(map[string]chan PermissionResult)}
}

// Await blocks until a response arrives for sessionID, or until ctx
// is cancelled. Only one pending request per session is allowed at a
// time — a second concurrent Await returns an error.
func (p *PermissionManager) Await(ctx context.Context, sessionID string, timeout time.Duration) (PermissionResult, error) {
	p.mu.Lock()
	if _, exists := p.pending[sessionID]; exists {
		p.mu.Unlock()
		return PermissionResult{}, fmt.Errorf("another permission is already pending for session %s", sessionID)
	}
	ch := make(chan PermissionResult, 1)
	p.pending[sessionID] = ch
	p.mu.Unlock()

	defer func() {
		p.mu.Lock()
		delete(p.pending, sessionID)
		p.mu.Unlock()
	}()

	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	select {
	case res := <-ch:
		return res, nil
	case <-time.After(timeout):
		return PermissionResult{OptionID: "reject", Granted: false}, nil
	case <-ctx.Done():
		return PermissionResult{}, ctx.Err()
	}
}

// Respond delivers the user's choice to the awaiting call. Unknown
// session IDs return an error.
func (p *PermissionManager) Respond(sessionID, optionID string) error {
	p.mu.Lock()
	ch, ok := p.pending[sessionID]
	p.mu.Unlock()
	if !ok {
		return fmt.Errorf("no pending permission for session %s", sessionID)
	}
	granted := optionID == "acceptEdits" ||
		optionID == "allow_once" ||
		optionID == "allow_session" ||
		optionID == "allow_always" ||
		optionID == "accept"
	always := optionID == "allow_always"
	// "Always allow this server" implies session coverage too: the
	// persistent callback writes to disk and refreshes the live adapter,
	// but a write failure or callback exception would otherwise leave
	// the same session re-prompting on the next call. Recording the
	// session grant first guarantees subsequent in-session calls skip
	// the prompt regardless of what happens downstream.
	sessionAlways := optionID == "allow_session" || optionID == "allow_always"
	select {
	case ch <- PermissionResult{
		OptionID:      optionID,
		Granted:       granted,
		Always:        always,
		SessionAlways: sessionAlways,
	}:
		return nil
	default:
		return fmt.Errorf("permission channel full for session %s", sessionID)
	}
}

// Cancel discards the pending permission without marking it as
// accepted or rejected. The waiter observes ctx cancellation.
func (p *PermissionManager) Cancel(sessionID string) {
	p.mu.Lock()
	ch, ok := p.pending[sessionID]
	delete(p.pending, sessionID)
	p.mu.Unlock()
	if ok {
		close(ch)
	}
}

// SessionTrust caches per-(sessionID, tool-prefix) "allow for this
// chat" grants. Trust is held only in memory: when sessionID changes
// (new conversation, switched conversation) the new session has no
// entries, so the user is asked again. Process restart also clears
// everything — that's the contract: session-scoped trust never
// outlives the agent.
//
// Persistent ("Always allow this server") trust is NOT tracked here;
// that goes through a wails-gui callback into the trusted.json file
// and is enforced at the adapter layer via McpToolAdapter.SetTrusted.
type SessionTrust struct {
	mu     sync.RWMutex
	grants map[string]map[string]struct{}
}

// NewSessionTrust returns an empty in-memory trust cache.
func NewSessionTrust() *SessionTrust {
	return &SessionTrust{grants: make(map[string]map[string]struct{})}
}

// Allow records that the given prefix is trusted for the rest of
// sessionID's lifetime. The prefix comes from the tool's
// PermissionPrompt.Prefix (e.g. "mcp__slack__"). An empty prefix is
// ignored — without a prefix there is nothing meaningful to match
// later, and matching every tool would defeat the safety story.
func (s *SessionTrust) Allow(sessionID, prefix string) {
	if s == nil || sessionID == "" || prefix == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Lazy init so callers that bypass NewSessionTrust (e.g. &SessionTrust{})
	// don't panic on the first write to a nil map.
	if s.grants == nil {
		s.grants = make(map[string]map[string]struct{})
	}
	bucket, ok := s.grants[sessionID]
	if !ok {
		bucket = make(map[string]struct{})
		s.grants[sessionID] = bucket
	}
	bucket[prefix] = struct{}{}
}

// IsAllowed reports whether toolName falls under any prefix the user
// granted "allow for this chat" within sessionID.
func (s *SessionTrust) IsAllowed(sessionID, toolName string) bool {
	if s == nil || sessionID == "" || toolName == "" {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	bucket, ok := s.grants[sessionID]
	if !ok {
		return false
	}
	for prefix := range bucket {
		if strings.HasPrefix(toolName, prefix) {
			return true
		}
	}
	return false
}

// ClearSession drops all trust entries for sessionID. Called when a
// chat ends so the in-memory map doesn't grow unbounded across the
// lifetime of a long-running app.
func (s *SessionTrust) ClearSession(sessionID string) {
	if s == nil || sessionID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.grants, sessionID)
}
