package email

import (
	"context"
	"fmt"
	"sync"
)

// FakeMail is one email the fake provider accepted.
type FakeMail struct {
	ID string
	Message
}

// Fake is an in-memory provider for tests. It records what it sends and never
// touches the network.
type Fake struct {
	mu   sync.Mutex
	sent []FakeMail
	fail error
	seq  int
}

// NewFake returns a fake provider.
func NewFake() *Fake { return &Fake{} }

// Send records the message, or returns the failure set by FailWith.
func (f *Fake) Send(_ context.Context, msg Message) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return "", f.fail
	}
	f.seq++
	id := fmt.Sprintf("fake-mail-%d", f.seq)
	f.sent = append(f.sent, FakeMail{ID: id, Message: msg})
	return id, nil
}

// FailWith makes every later send return err; nil restores sending.
func (f *Fake) FailWith(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail = err
}

// Sent returns a copy of the emails sent so far.
func (f *Fake) Sent() []FakeMail {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]FakeMail(nil), f.sent...)
}
