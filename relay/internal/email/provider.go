// Package email is the relay's email channel (PROTOCOL.md section 19). A hub
// verifies one address of its own with a code the relay mails to it; after
// that the hub may have results mailed there. A send names no recipient: the
// relay mails only the address the hub verified. The provider sits behind
// Provider, so tests run on Fake and a real provider (SMTP, which every
// transactional mail service offers) is switched on by configuration alone.
//
// Nothing in this package logs an address or a message, and errors never
// carry either one.
package email

import (
	"context"
	"errors"
)

// Message is one plain-text email.
type Message struct {
	To      string
	Subject string
	Text    string
}

// Provider sends email.
type Provider interface {
	// Send delivers the message and returns the provider's message id.
	// ErrRejected reports a message the provider refused outright, so
	// nothing was sent; any other error may or may not have sent it.
	Send(ctx context.Context, msg Message) (string, error)
}

var (
	// ErrRejected reports a send the provider refused; nothing was sent.
	ErrRejected = errors.New("the provider refused the email")
	// ErrUnavailable reports a send that failed in transit.
	ErrUnavailable = errors.New("the provider could not be reached")
)
