package sms

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

const (
	testAccount = "A" + "C0123456789abcdef0123456789abcdef"
	testService = "M" + "G0123456789abcdef0123456789abcdef"
	testToken   = "exampleexampleexampleexampletest"
	testWebhook = "https://relay.example.com/v1/sms/inbound"
	testMessage = "SM0123456789abcdef0123456789abcdef"
)

func testTwilio(t *testing.T, transport http.RoundTripper) *Twilio {
	t.Helper()
	tw, err := NewTwilio(TwilioConfig{AccountSID: testAccount, AuthToken: testToken, MessagingServiceSID: testService, WebhookURL: testWebhook})
	if err != nil {
		t.Fatal(err)
	}
	tw.Client = &http.Client{Transport: transport}
	return tw
}

// TestTwilioSignatureFictionalVector checks parameter sorting and signature encoding.
func TestTwilioSignatureFictionalVector(t *testing.T) {
	form := url.Values{
		"CallSid": {"CA1234567890ABCDE"},
		"Caller":  {"+14155550114"},
		"Digits":  {"1234"},
		"From":    {"+14155550114"},
		"To":      {"+18005550101"},
	}
	got := TwilioSignature("12345", "https://relay.example.com/myapp.php?foo=1&bar=2", form)
	if got != "sswAIrYv8lVN1YaOW70VsLGT/9M=" {
		t.Fatalf("signature %q", got)
	}
}

func inboundForm() url.Values {
	return url.Values{
		"AccountSid": {testAccount},
		"MessageSid": {testMessage},
		"From":       {"+14155550123"},
		"To":         {"+18005550100"},
		"Body":       {"Y"},
		"NumMedia":   {"0"},
	}
}

func webhookRequest(form url.Values, header, signature string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/v1/sms/inbound", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	if signature != "" {
		r.Header.Set(header, signature)
	}
	return r
}

func TestTwilioVerifyInbound(t *testing.T) {
	tw := testTwilio(t, nil)
	form := inboundForm()
	valid := TwilioSignature(testToken, testWebhook, form)

	in, err := tw.VerifyInbound(webhookRequest(form, "X-Twilio-Signature", valid))
	if err != nil {
		t.Fatalf("valid webhook refused: %v", err)
	}
	if in.From != "+14155550123" || in.Body != "Y" || in.MessageID != testMessage || in.To != "+18005550100" {
		t.Fatalf("inbound %+v", in)
	}

	withPort := TwilioSignature(testToken, "https://relay.example.com:443/v1/sms/inbound", form)
	if _, err = tw.VerifyInbound(webhookRequest(form, "X-Twilio-Signature", withPort)); err != nil {
		t.Fatalf("signature over the URL with its default port refused: %v", err)
	}

	tampered := inboundForm()
	tampered.Set("Body", "STOP")
	otherURL := TwilioSignature(testToken, "https://attacker.example/v1/sms/inbound", form)
	otherToken := TwilioSignature("ffffffffffffffffffffffffffffffff", testWebhook, form)
	otherAccount := inboundForm()
	otherAccount.Set("AccountSid", "ACffffffffffffffffffffffffffffffff")
	cases := []struct {
		name string
		req  *http.Request
		want error
	}{
		{"missing signature", webhookRequest(form, "X-Twilio-Signature", ""), ErrBadSignature},
		{"tampered body", webhookRequest(tampered, "X-Twilio-Signature", valid), ErrBadSignature},
		{"other URL", webhookRequest(form, "X-Twilio-Signature", otherURL), ErrBadSignature},
		{"other token", webhookRequest(form, "X-Twilio-Signature", otherToken), ErrBadSignature},
		{"not base64", webhookRequest(form, "X-Twilio-Signature", "%%%"), ErrBadSignature},
		{"other account", webhookRequest(otherAccount, "X-Twilio-Signature", TwilioSignature(testToken, testWebhook, otherAccount)), ErrBadSignature},
	}
	for _, c := range cases {
		if _, err := tw.VerifyInbound(c.req); !errors.Is(err, c.want) {
			t.Fatalf("%s: err %v, want %v", c.name, err, c.want)
		}
	}

	jsonBody := httptest.NewRequest(http.MethodPost, "/v1/sms/inbound", strings.NewReader(`{"Body":"Y"}`))
	jsonBody.Header.Set("Content-Type", "application/json")
	jsonBody.Header.Set("X-Twilio-Signature", valid)
	if _, err = tw.VerifyInbound(jsonBody); !errors.Is(err, ErrBadWebhook) {
		t.Fatalf("JSON body: %v", err)
	}
	huge := inboundForm()
	huge.Set("Body", strings.Repeat("a", maxWebhookBytes))
	if _, err = tw.VerifyInbound(webhookRequest(huge, "X-Twilio-Signature", TwilioSignature(testToken, testWebhook, huge))); !errors.Is(err, ErrBadWebhook) {
		t.Fatalf("oversized body: %v", err)
	}
	noSID := inboundForm()
	noSID.Del("MessageSid")
	if _, err = tw.VerifyInbound(webhookRequest(noSID, "X-Twilio-Signature", TwilioSignature(testToken, testWebhook, noSID))); !errors.Is(err, ErrBadWebhook) {
		t.Fatalf("missing message id: %v", err)
	}
}

// roundTrip is an http.RoundTripper backed by a function, so no request
// leaves the process.
type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func TestTwilioSend(t *testing.T) {
	var seen *http.Request
	var form url.Values
	reply := jsonResponse(http.StatusCreated, `{"sid":"`+testMessage+`","status":"accepted"}`)
	tw := testTwilio(t, roundTrip(func(r *http.Request) (*http.Response, error) {
		seen = r
		raw, _ := io.ReadAll(r.Body)
		form, _ = url.ParseQuery(string(raw))
		return reply, nil
	}))
	id, err := tw.Send(context.Background(), "+14155550123", "Isoform: hello")
	if err != nil || id != testMessage {
		t.Fatalf("send: %q %v", id, err)
	}
	if seen.Method != http.MethodPost || seen.URL.String() != "https://api.twilio.com/2010-04-01/Accounts/"+testAccount+"/Messages.json" {
		t.Fatalf("request %s %s", seen.Method, seen.URL)
	}
	if want := "Basic " + base64.StdEncoding.EncodeToString([]byte(testAccount+":"+testToken)); seen.Header.Get("Authorization") != want {
		t.Fatal("request is not authenticated with the account")
	}
	if form.Get("To") != "+14155550123" || form.Get("Body") != "Isoform: hello" || form.Get("MessagingServiceSid") != testService || form.Get("From") != "" {
		t.Fatalf("form %v", form)
	}

	failures := []struct {
		resp *http.Response
		err  error
		want error
	}{
		{jsonResponse(http.StatusBadRequest, `{"code":21610,"message":"Attempt to send to unsubscribed recipient +14155550123"}`), nil, ErrRecipientStopped},
		{jsonResponse(http.StatusBadRequest, `{"code":21211,"message":"The 'To' number +14155550123 is not a valid phone number."}`), nil, ErrRejected},
		{jsonResponse(http.StatusUnauthorized, `{"code":20003}`), nil, ErrRejected},
		{jsonResponse(http.StatusTooManyRequests, `{"code":20429}`), nil, ErrRejected},
		{jsonResponse(http.StatusInternalServerError, `oops`), nil, ErrUnavailable},
		{jsonResponse(http.StatusCreated, `{"sid":"not a sid"}`), nil, ErrUnavailable},
		{nil, errors.New("dial tcp: +14155550123 Isoform: hello"), ErrUnavailable},
	}
	for i, f := range failures {
		tw.Client.Transport = roundTrip(func(*http.Request) (*http.Response, error) { return f.resp, f.err })
		_, err := tw.Send(context.Background(), "+14155550123", "Isoform: hello")
		if !errors.Is(err, f.want) {
			t.Fatalf("failure %d: %v, want %v", i, err, f.want)
		}
		if strings.Contains(err.Error(), "4155550123") || strings.Contains(err.Error(), "hello") {
			t.Fatalf("failure %d: error carries the number or the body: %v", i, err)
		}
	}
}

func TestTwilioConfigValidation(t *testing.T) {
	good := TwilioConfig{AccountSID: testAccount, AuthToken: testToken, MessagingServiceSID: testService, WebhookURL: testWebhook}
	if err := good.validate(); err != nil {
		t.Fatal(err)
	}
	local := good
	local.WebhookURL = "http://127.0.0.1:8791/v1/sms/inbound"
	if err := local.validate(); err != nil {
		t.Fatalf("loopback http refused: %v", err)
	}
	bad := []func(*TwilioConfig){
		func(c *TwilioConfig) { c.AccountSID = "AC123" },
		func(c *TwilioConfig) { c.AuthToken = "" },
		func(c *TwilioConfig) { c.MessagingServiceSID = "PN0123456789abcdef0123456789abcdef" },
		func(c *TwilioConfig) { c.WebhookURL = "http://relay.example.com/v1/sms/inbound" },
		func(c *TwilioConfig) { c.WebhookURL = "/v1/sms/inbound" },
		func(c *TwilioConfig) { c.WebhookURL = "https://user:pass@relay.example.com/v1/sms/inbound" },
	}
	for i, change := range bad {
		c := good
		change(&c)
		if err := c.validate(); err == nil {
			t.Fatalf("bad config %d accepted", i)
		}
	}
}
