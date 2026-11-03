package daemon

import (
	"sync"
	"sync/atomic"
)

const sessionQueueCapacity = 5
const maxConcurrentAITasks = 3

// Session holds per-user state including a sequential task queue.
type Session struct {
	queue       chan func()
	aiTaskSlots chan struct{} // semaphore: limits concurrent AI tasks per user
	runningFast int32         // atomic: >0 while a fast command is executing in run()
}

func newSession() *Session {
	s := &Session{
		queue:       make(chan func(), sessionQueueCapacity),
		aiTaskSlots: make(chan struct{}, maxConcurrentAITasks),
	}
	go s.run()
	return s
}

// CanEnqueue reports whether the queue has room for another task.
func (s *Session) CanEnqueue() bool {
	return len(s.queue) < sessionQueueCapacity
}

// Enqueue adds a task to the queue. The caller must check CanEnqueue first.
func (s *Session) Enqueue(task func()) {
	s.queue <- task
}

// close stops the session goroutine after it drains any queued tasks.
func (s *Session) close() {
	close(s.queue)
}

// run processes tasks sequentially from the queue.
// Maintains runningFast while a task is executing so IsAITaskEvent can detect
// the in-flight window where SetPending may not yet have been called.
func (s *Session) run() {
	for task := range s.queue {
		atomic.AddInt32(&s.runningFast, 1)
		task()
		atomic.AddInt32(&s.runningFast, -1)
	}
}

// Len returns the number of tasks currently waiting in the fast queue.
func (s *Session) Len() int {
	return len(s.queue)
}

// IsRunningFast reports whether a fast command is currently executing in the
// session goroutine. Safe to call from any goroutine.
func (s *Session) IsRunningFast() bool {
	return atomic.LoadInt32(&s.runningFast) > 0
}

// TryAcquireAISlot acquires one AI task slot (non-blocking). Returns false if at limit.
func (s *Session) TryAcquireAISlot() bool {
	select {
	case s.aiTaskSlots <- struct{}{}:
		return true
	default:
		return false
	}
}

// ReleaseAISlot returns one AI task slot to the semaphore.
func (s *Session) ReleaseAISlot() {
	<-s.aiTaskSlots
}

// PendingState tracks a command waiting for project selection.
type PendingState struct {
	Command string // the original command (e.g. "sync")
	Args    []string
}

// SessionManager creates and retrieves per-user sessions.
type SessionManager struct {
	mu          sync.Mutex
	sessions    map[string]*Session
	pending     map[string]*PendingState // userKey → pending command awaiting project selection
	lastProject map[string]string        // userKey → last successfully resolved project path
}

// NewSessionManager creates a new SessionManager.
func NewSessionManager() *SessionManager {
	return &SessionManager{
		sessions:    make(map[string]*Session),
		pending:     make(map[string]*PendingState),
		lastProject: make(map[string]string),
	}
}

// SetLastProject stores the last successfully used project path for a user.
func (m *SessionManager) SetLastProject(userKey, path string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastProject[userKey] = path
}

// GetLastProject returns the last project path used by this user, if any.
func (m *SessionManager) GetLastProject(userKey string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	path, ok := m.lastProject[userKey]
	return path, ok
}

// GetOrCreate returns the session for userID, creating it if needed.
func (m *SessionManager) GetOrCreate(userID string) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[userID]; ok {
		return s
	}
	s := newSession()
	m.sessions[userID] = s
	return s
}

// Close drains and stops all session goroutines. Call once on daemon shutdown.
func (m *SessionManager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.sessions {
		s.close()
	}
}

// HasPending returns true if there is a pending project selection for userKey,
// without consuming the state.
func (m *SessionManager) HasPending(userKey string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.pending[userKey]
	return ok
}

// SetPending stores a pending command state for the user.
func (m *SessionManager) SetPending(userID string, state *PendingState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pending[userID] = state
}

// TakePending retrieves and clears the pending state for the user.
func (m *SessionManager) TakePending(userID string) *PendingState {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.pending[userID]
	delete(m.pending, userID)
	return state
}
