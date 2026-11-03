package apnspush

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"isoform-relay/internal/push"
	"isoform-relay/internal/pushgatewayauth"
)

func TestLoadGatewayConfig(t *testing.T) {
	values := map[string]string{}
	get := func(key string) string { return values[key] }
	c, err := LoadGatewayConfig(get)
	if err != nil || c.Configured() {
		t.Fatalf("empty gateway config = %+v, %v", c, err)
	}
	values["ISOFORM_RELAY_PUSH_GATEWAY_URL"] = "https://push.example"
	if _, err := LoadGatewayConfig(get); err == nil {
		t.Fatal("URL without environment accepted")
	}
	values["ISOFORM_RELAY_PUSH_GATEWAY_ENVIRONMENT"] = "development"
	setGatewayIdentity(values, t)
	c, err = LoadGatewayConfig(get)
	if err != nil || c.URL != "https://push.example" || c.Environment != "development" || c.HubID == "" {
		t.Fatalf("configured gateway = %+v, %v", c, err)
	}
	values["ISOFORM_RELAY_APNS_KEY_FILE"] = "/private/apns.p8"
	if _, err := LoadGatewayConfig(get); err == nil {
		t.Fatal("gateway and APNs configurations accepted together")
	}
	delete(values, "ISOFORM_RELAY_APNS_KEY_FILE")
	values["ISOFORM_RELAY_PUSH_GATEWAY_URL"] = "http://198.51.100.7"
	if _, err := LoadGatewayConfig(get); err == nil {
		t.Fatal("non-loopback HTTP gateway accepted")
	}
	values["ISOFORM_RELAY_PUSH_GATEWAY_URL"] = "http://127.0.0.1:8792"
	if _, err := LoadGatewayConfig(get); err != nil {
		t.Fatalf("loopback HTTP gateway rejected: %v", err)
	}
}

func TestGatewayClientMaps429Cooldown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "600")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	config := testGatewayConfig(t, server.URL)
	client := NewGatewayClient(config)
	_, err := client.Send(context.Background(), push.Claim{HubID: config.HubID})
	var cooldown *ProviderCooldownError
	if !errors.As(err, &cooldown) {
		t.Fatalf("429 cooldown error = %T %v", err, err)
	}
}

func TestGatewayClientMaps401And403(t *testing.T) {
	for _, test := range []struct {
		status int
		want   push.Outcome
		config bool
	}{
		{status: http.StatusUnauthorized, want: push.Retry, config: true},
		{status: http.StatusForbidden, want: push.TokenInvalid},
	} {
		t.Run(http.StatusText(test.status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Isoform-Hub-Key") == "" || r.Header.Get("Isoform-Timestamp") == "" || r.Header.Get("Isoform-Signature") == "" {
					t.Error("missing gateway authentication headers")
				}
				w.WriteHeader(test.status)
			}))
			defer server.Close()
			config := testGatewayConfig(t, server.URL)
			outcome, err := NewGatewayClient(config).Send(context.Background(), push.Claim{HubID: config.HubID})
			if outcome != test.want || IsConfigurationError(err) != test.config {
				t.Fatalf("Send = %q, %v", outcome, err)
			}
		})
	}
}

func TestGatewayClientRejectsMismatchedHubWithoutRequest(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests++ }))
	defer server.Close()
	config := testGatewayConfig(t, server.URL)
	outcome, err := NewGatewayClient(config).Send(context.Background(), push.Claim{HubID: "other"})
	if outcome != push.Permanent || err == nil || requests != 0 {
		t.Fatalf("Send = %q, %v, requests=%d", outcome, err, requests)
	}
}

func testGatewayConfig(t *testing.T, url string) GatewayConfig {
	t.Helper()
	hub := generateTestX25519(t)
	gateway := generateTestX25519(t)
	key, _, err := pushgatewayauth.DeriveHubKeyFromPrivate(hub, gateway.PublicKey().Bytes())
	if err != nil {
		t.Fatal(err)
	}
	return GatewayConfig{
		URL: url, Environment: "development", HubID: "00000000-0000-4000-8000-000000000001",
		HubPub: base64.RawURLEncoding.EncodeToString(hub.PublicKey().Bytes()),
		HubKey: base64.RawURLEncoding.EncodeToString(key),
	}
}

func setGatewayIdentity(values map[string]string, t *testing.T) {
	t.Helper()
	config := testGatewayConfig(t, "")
	values["ISOFORM_RELAY_PUSH_GATEWAY_HUB_ID"] = config.HubID
	values["ISOFORM_RELAY_PUSH_GATEWAY_HUB_PUB"] = config.HubPub
	values["ISOFORM_RELAY_PUSH_GATEWAY_HUB_KEY"] = config.HubKey
}

func generateTestX25519(t *testing.T) *ecdh.PrivateKey {
	t.Helper()
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}
