// Package sms is the relay's text message channel (PROTOCOL.md section 18).
// A hub subscribes one verified number; the relay sends the texts the hub
// writes through an SMS provider, enforces the subscription state, the caps
// and the carrier keywords (STOP, START, HELP), and forwards other replies to
// the hub. The provider sits behind Provider, so tests run on Fake and a real
// provider is switched on by configuration alone.
//
// Nothing in this package logs a phone number or a message body, and errors
// never carry either one.
package sms

import (
	"context"
	"errors"
	"net/http"
)

// Provider sends texts and authenticates the provider's inbound webhooks.
type Provider interface {
	// Send delivers body to the E.164 number and returns the provider's
	// message id. ErrRecipientStopped reports a number the provider itself
	// has opted out; ErrRejected a request the provider refused outright, so
	// nothing was sent. Any other error may or may not have sent the text.
	Send(ctx context.Context, to, body string) (string, error)
	// VerifyInbound checks the webhook's signature and returns the message.
	// A missing or wrong signature is ErrBadSignature.
	VerifyInbound(r *http.Request) (Inbound, error)
	// WebhookAck is the body the provider expects in answer to a webhook.
	WebhookAck() (contentType string, body []byte)
}

// Inbound is one text the provider received. From and To are E.164.
type Inbound struct {
	From      string
	To        string
	Body      string
	MessageID string
}

var (
	// ErrBadSignature reports a webhook whose signature is missing or wrong.
	ErrBadSignature = errors.New("webhook signature is missing or invalid")
	// ErrBadWebhook reports a signed webhook without the fields a text needs.
	ErrBadWebhook = errors.New("webhook is not a text message")
	// ErrRecipientStopped reports a number the provider will not text.
	ErrRecipientStopped = errors.New("the provider reports the number opted out")
	// ErrRejected reports a send the provider refused; nothing was sent.
	ErrRejected = errors.New("the provider refused the text")
	// ErrUnavailable reports a send that failed in transit.
	ErrUnavailable = errors.New("the provider could not be reached")
)
