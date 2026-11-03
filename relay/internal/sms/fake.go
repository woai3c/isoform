package sms

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"sync"
)

// FakeText is one text the fake provider accepted.
type FakeText struct {
	ID   string
	To   string
	Body string
}

// Fake is an in-memory provider for tests. It records what it sends and
// accepts webhooks signed with HMAC-SHA256 over the sorted form under Secret,
// in X-Fake-Signature. It never touches the network.
type Fake struct {
	Secret []byte

	mu   sync.Mutex
	sent []FakeText
	fail error
	seq  int
}

// NewFake returns a fake provider with the given webhook secret.
func NewFake(secret []byte) *Fake { return &Fake{Secret: secret} }

// Send records the text, or returns the failure set by FailWith.
func (f *Fake) Send(_ context.Context, to, body string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return "", f.fail
	}
	f.seq++
	id := fmt.Sprintf("fake-%d", f.seq)
	f.sent = append(f.sent, FakeText{ID: id, To: to, Body: body})
	return id, nil
}

// FailWith makes every later send return err; nil restores sending.
func (f *Fake) FailWith(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail = err
}

// Sent returns a copy of the texts sent so far.
func (f *Fake) Sent() []FakeText {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]FakeText(nil), f.sent...)
}

// SentTo returns the texts sent to one number.
func (f *Fake) SentTo(number string) []FakeText {
	out := []FakeText{}
	for _, text := range f.Sent() {
		if text.To == number {
			out = append(out, text)
		}
	}
	return out
}

// Sign returns the X-Fake-Signature for a form.
func (f *Fake) Sign(form url.Values) string {
	mac := hmac.New(sha256.New, f.Secret)
	keys := make([]string, 0, len(form))
	for key := range form {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		for _, value := range form[key] {
			mac.Write([]byte(key + "=" + value + "\n"))
		}
	}
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifyInbound checks the fake signature and reads From, To, Body and MessageId.
func (f *Fake) VerifyInbound(r *http.Request) (Inbound, error) {
	signature := r.Header.Get("X-Fake-Signature")
	form, err := readWebhookForm(r)
	if err != nil {
		return Inbound{}, err
	}
	if signature == "" || !hmac.Equal([]byte(signature), []byte(f.Sign(form))) {
		return Inbound{}, ErrBadSignature
	}
	in := Inbound{From: form.Get("From"), To: form.Get("To"), Body: form.Get("Body"), MessageID: form.Get("MessageId")}
	if in.MessageID == "" || in.From == "" {
		return Inbound{}, ErrBadWebhook
	}
	return in, nil
}

// WebhookAck is an empty plain-text answer.
func (f *Fake) WebhookAck() (string, []byte) { return "text/plain", []byte("ok") }
