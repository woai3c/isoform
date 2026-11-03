package main

import (
	"encoding/json"
	"log"
	"sync"
	"time"
)

// wsClient represents a single WebSocket subscriber. The closeOnce field
// ensures the send channel is closed exactly once, preventing panics when
// both the reader and writer goroutines call RemoveClient concurrently.

// BrowserEvent is the JSON envelope sent to WebSocket clients.
// The field names match what the frontend browser-bridge.ts expects.
type BrowserEvent struct {
	Event     string `json:"event"`
	Args      []any  `json:"args"`
	Timestamp int64  `json:"timestamp"`
}

type wsClient struct {
	id        string
	sendCh    chan []byte
	closeOnce sync.Once
}

// EventBroadcaster captures Wails-style events and forwards them to all
// connected WebSocket clients. It is thread-safe.
type EventBroadcaster struct {
	mu      sync.RWMutex
	clients map[string]*wsClient
}

// NewEventBroadcaster creates an EventBroadcaster ready for use.
func NewEventBroadcaster() *EventBroadcaster {
	return &EventBroadcaster{
		clients: make(map[string]*wsClient),
	}
}

// AddClient registers a new WebSocket client and returns its send channel.
// The caller is responsible for reading from the channel and writing to the
// WebSocket connection.
func (eb *EventBroadcaster) AddClient(id string) chan []byte {
	ch := make(chan []byte, 64)
	eb.mu.Lock()
	eb.clients[id] = &wsClient{id: id, sendCh: ch}
	eb.mu.Unlock()
	log.Printf("[browser-events] client connected: %s (total: %d)", id, eb.ClientCount())
	return ch
}

// RemoveClient unregisters a WebSocket client and closes its send channel.
// Safe to call multiple times from different goroutines -- the channel is
// closed at most once via sync.Once.
func (eb *EventBroadcaster) RemoveClient(id string) {
	eb.mu.Lock()
	c, ok := eb.clients[id]
	if ok {
		delete(eb.clients, id)
	}
	eb.mu.Unlock()
	if ok {
		c.closeOnce.Do(func() { close(c.sendCh) })
		log.Printf("[browser-events] client disconnected: %s", id)
	}
}

// Broadcast sends an event to all connected WebSocket clients. Non-blocking:
// if a client's buffer is full the event is dropped for that client.
func (eb *EventBroadcaster) Broadcast(eventName string, data ...interface{}) {
	args := make([]any, len(data))
	copy(args, data)
	evt := BrowserEvent{
		Event:     eventName,
		Args:      args,
		Timestamp: time.Now().UnixMilli(),
	}

	payload, err := json.Marshal(evt)
	if err != nil {
		log.Printf("[browser-events] marshal error: %v", err)
		return
	}

	eb.mu.RLock()
	defer eb.mu.RUnlock()

	for _, c := range eb.clients {
		select {
		case c.sendCh <- payload:
		default:
			log.Printf("[browser-events] dropped event %q for slow client %s", eventName, c.id)
		}
	}
}

// ClientCount returns the number of connected clients.
func (eb *EventBroadcaster) ClientCount() int {
	eb.mu.RLock()
	defer eb.mu.RUnlock()
	return len(eb.clients)
}
