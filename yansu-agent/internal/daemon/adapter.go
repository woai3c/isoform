package daemon

import (
	"context"
	"time"
)

// Adapter is the interface that any chat platform must implement to integrate
// with the yansu daemon router.
type Adapter interface {
	// Listen connects to the platform and calls handler for each incoming message.
	// Blocks until ctx is cancelled. Must reconnect internally on transient errors.
	Listen(ctx context.Context, handler func(Event)) error
	// Reply posts a plain-text message. If event.ThreadTS is set, replies in thread.
	Reply(event Event, text string)
	// PostAndStream posts a transient "⏳ Running..." indicator, streams ch content
	// as chunked replies, then removes/updates the running indicator when done.
	PostAndStream(event Event, ch <-chan string)
}

// ContextMessage is a single message from conversation history.
type ContextMessage struct {
	UserID    string
	Text      string
	Timestamp time.Time
}

// ContextProvider is an optional interface adapters can implement to supply
// conversation history for AI tasks. Adapters that do not implement this
// interface are silently skipped — the AI task runs without context.
type ContextProvider interface {
	FetchContext(ctx context.Context, event Event) ([]ContextMessage, error)
}
