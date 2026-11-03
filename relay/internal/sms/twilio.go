package sms

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	twilioAPIBase = "https://api.twilio.com"
	// twilioOptedOut is Twilio's "attempt to send to unsubscribed recipient".
	twilioOptedOut = 21610
	twilioTimeout  = 10 * time.Second
	// maxWebhookBytes bounds an inbound webhook body.
	maxWebhookBytes = 64 << 10
	// maxProviderReplyBytes bounds a provider's answer to a send.
	maxProviderReplyBytes = 64 << 10
)

var (
	twilioAccountPattern = regexp.MustCompile(`^AC[0-9a-f]{32}$`)
	twilioServicePattern = regexp.MustCompile(`^MG[0-9a-f]{32}$`)
	twilioMessagePattern = regexp.MustCompile(`^(SM|MM)[0-9a-f]{32}$`)
	twilioTokenPattern   = regexp.MustCompile(`^[0-9A-Za-z]{32,64}$`)
)

// TwilioConfig is a Twilio account's sending settings. WebhookURL is the exact
// public URL configured for incoming messages on the Messaging Service; the
// signature covers it, so it must match character for character.
type TwilioConfig struct {
	AccountSID          string
	AuthToken           string
	MessagingServiceSID string
	WebhookURL          string
}

func (c TwilioConfig) validate() error {
	switch {
	case !twilioAccountPattern.MatchString(c.AccountSID):
		return errors.New("ISOFORM_RELAY_TWILIO_ACCOUNT_SID must look like AC followed by 32 hex digits")
	case !twilioTokenPattern.MatchString(c.AuthToken):
		return errors.New("ISOFORM_RELAY_TWILIO_AUTH_TOKEN must be the account's auth token")
	case !twilioServicePattern.MatchString(c.MessagingServiceSID):
		return errors.New("ISOFORM_RELAY_TWILIO_MESSAGING_SERVICE_SID must look like MG followed by 32 hex digits")
	}
	return validWebhookURL(c.WebhookURL)
}

// validWebhookURL accepts an absolute https URL, or http on a loopback host
// for a local test against a tunnel.
func validWebhookURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return errors.New("ISOFORM_RELAY_SMS_WEBHOOK_URL must be an absolute URL")
	}
	switch parsed.Scheme {
	case "https":
		return nil
	case "http":
		host := parsed.Hostname()
		if ip := net.ParseIP(host); host == "localhost" || (ip != nil && ip.IsLoopback()) {
			return nil
		}
	}
	return errors.New("ISOFORM_RELAY_SMS_WEBHOOK_URL must use https")
}

// Twilio sends through the Programmable Messaging API with a Messaging
// Service and checks inbound webhooks with Twilio's HMAC-SHA1 signature.
// BaseURL and Client exist for tests; production uses the defaults.
type Twilio struct {
	Config  TwilioConfig
	BaseURL string
	Client  *http.Client
}

// NewTwilio checks the configuration and returns the provider.
func NewTwilio(cfg TwilioConfig) (*Twilio, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &Twilio{Config: cfg, BaseURL: twilioAPIBase, Client: &http.Client{Timeout: twilioTimeout}}, nil
}

// Send posts one message. Twilio's error text can quote the number, so only
// the numeric error code is kept.
func (t *Twilio) Send(ctx context.Context, to, body string) (string, error) {
	form := url.Values{"To": {to}, "Body": {body}, "MessagingServiceSid": {t.Config.MessagingServiceSID}}
	endpoint := strings.TrimRight(t.BaseURL, "/") + "/2010-04-01/Accounts/" + t.Config.AccountSID + "/Messages.json"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", ErrUnavailable
	}
	req.SetBasicAuth(t.Config.AccountSID, t.Config.AuthToken)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	client := t.Client
	if client == nil {
		client = &http.Client{Timeout: twilioTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", ErrUnavailable
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxProviderReplyBytes))
	if err != nil {
		return "", ErrUnavailable
	}
	var answer struct {
		SID  string `json:"sid"`
		Code int    `json:"code"`
	}
	_ = json.Unmarshal(raw, &answer)
	switch {
	case resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusOK:
		if !twilioMessagePattern.MatchString(answer.SID) {
			return "", ErrUnavailable
		}
		return answer.SID, nil
	case answer.Code == twilioOptedOut:
		return "", ErrRecipientStopped
	case resp.StatusCode >= 500:
		return "", fmt.Errorf("%w (status %d)", ErrUnavailable, resp.StatusCode)
	default:
		// A 4xx, throttling (429) included, means Twilio did not take the
		// message, so nothing was sent.
		return "", fmt.Errorf("%w (status %d, code %d)", ErrRejected, resp.StatusCode, answer.Code)
	}
}

// VerifyInbound checks X-Twilio-Signature over the configured webhook URL and
// the posted form, then reads the message. The account must be this one.
func (t *Twilio) VerifyInbound(r *http.Request) (Inbound, error) {
	signature := r.Header.Get("X-Twilio-Signature")
	form, err := readWebhookForm(r)
	if err != nil {
		return Inbound{}, err
	}
	if signature == "" || !t.validSignature(signature, form) {
		return Inbound{}, ErrBadSignature
	}
	if form.Get("AccountSid") != t.Config.AccountSID {
		return Inbound{}, ErrBadSignature
	}
	in := Inbound{From: form.Get("From"), To: form.Get("To"), Body: form.Get("Body"), MessageID: form.Get("MessageSid")}
	if in.MessageID == "" {
		in.MessageID = form.Get("SmsMessageSid")
	}
	if !twilioMessagePattern.MatchString(in.MessageID) || in.From == "" {
		return Inbound{}, ErrBadWebhook
	}
	return in, nil
}

// WebhookAck is an empty TwiML response: the relay sends its own replies.
func (t *Twilio) WebhookAck() (string, []byte) {
	return "text/xml", []byte(`<?xml version="1.0" encoding="UTF-8"?><Response></Response>`)
}

// validSignature compares in constant time against the configured URL with
// and without its default port, as Twilio's own validators do.
func (t *Twilio) validSignature(signature string, form url.Values) bool {
	presented, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return false
	}
	valid := false
	for _, candidate := range urlVariants(t.Config.WebhookURL) {
		expected := twilioSignature(t.Config.AuthToken, candidate, form)
		if hmac.Equal(presented, expected) {
			valid = true
		}
	}
	return valid
}

// TwilioSignature is the base64 X-Twilio-Signature for a form posted to
// webhookURL: HMAC-SHA1 under the auth token of the URL followed by every
// parameter name and value, sorted by name.
func TwilioSignature(authToken, webhookURL string, form url.Values) string {
	return base64.StdEncoding.EncodeToString(twilioSignature(authToken, webhookURL, form))
}

func twilioSignature(authToken, webhookURL string, form url.Values) []byte {
	mac := hmac.New(sha1.New, []byte(authToken))
	mac.Write([]byte(webhookURL))
	keys := make([]string, 0, len(form))
	for key := range form {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		values := append([]string(nil), form[key]...)
		sort.Strings(values)
		for _, value := range values {
			mac.Write([]byte(key))
			mac.Write([]byte(value))
		}
	}
	return mac.Sum(nil)
}

// urlVariants returns the URL as configured and with its default port added
// or removed.
func urlVariants(raw string) []string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return []string{raw}
	}
	defaultPort := "443"
	if parsed.Scheme == "http" {
		defaultPort = "80"
	}
	other := *parsed
	switch parsed.Port() {
	case "":
		other.Host = net.JoinHostPort(parsed.Hostname(), defaultPort)
	case defaultPort:
		other.Host = parsed.Hostname()
	default:
		return []string{raw}
	}
	return []string{raw, other.String()}
}

// readWebhookForm reads a bounded form body posted by a provider.
func readWebhookForm(r *http.Request) (url.Values, error) {
	if r.Method != http.MethodPost {
		return nil, ErrBadWebhook
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/x-www-form-urlencoded" {
		return nil, ErrBadWebhook
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxWebhookBytes+1))
	if err != nil || len(raw) > maxWebhookBytes {
		return nil, ErrBadWebhook
	}
	form, err := url.ParseQuery(string(raw))
	if err != nil {
		return nil, ErrBadWebhook
	}
	return form, nil
}
