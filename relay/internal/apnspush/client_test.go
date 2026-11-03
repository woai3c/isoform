package apnspush

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v4"
	"github.com/google/uuid"
	"github.com/sideshow/apns2"
	"isoform-relay/internal/push"
)

func testConfig(t *testing.T, curve elliptic.Curve) (Config, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(curve, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	data, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "test-key.p8")
	if err = os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: data}), 0600); err != nil {
		t.Fatal(err)
	}
	return Config{KeyFile: path, KeyID: "KEY1234567", TeamID: "TEAM123456", Topic: "com.isoform.companion", Environment: "development"}, key
}

func TestConfigurationBoundaries(t *testing.T) {
	if c, err := LoadConfig(func(string) string { return "" }); err != nil || c.Configured() {
		t.Fatal("empty push config must stay disabled")
	}
	if _, err := LoadConfig(func(name string) string {
		if name == "ISOFORM_RELAY_APNS_KEY_ID" {
			return "KEY1234567"
		}
		return ""
	}); err == nil {
		t.Fatal("partial config accepted")
	}
	c, _ := testConfig(t, elliptic.P256())
	values := map[string]string{"ISOFORM_RELAY_APNS_KEY_FILE": c.KeyFile, "ISOFORM_RELAY_APNS_KEY_ID": c.KeyID, "ISOFORM_RELAY_APNS_TEAM_ID": c.TeamID, "ISOFORM_RELAY_APNS_TOPIC": c.Topic, "ISOFORM_RELAY_APNS_ENVIRONMENT": c.Environment}
	if _, err := LoadConfig(func(name string) string { return values[name] }); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{"ISOFORM_RELAY_APNS_KEY_FILE": "relative.p8", "ISOFORM_RELAY_APNS_KEY_ID": "bad", "ISOFORM_RELAY_APNS_TEAM_ID": "bad", "ISOFORM_RELAY_APNS_TOPIC": "https://example.com", "ISOFORM_RELAY_APNS_ENVIRONMENT": "sandbox"} {
		original := values[key]
		values[key] = value
		if _, err := LoadConfig(func(name string) string { return values[name] }); err == nil {
			t.Errorf("accepted invalid %s", key)
		}
		values[key] = original
	}
	if got := c.Configuration().Environment; got != "development" {
		t.Fatalf("configured environment %q", got)
	}
	client, err := NewClient(c)
	if err != nil {
		t.Fatal(err)
	}
	client.Close()
	if err = os.Chmod(c.KeyFile, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = NewClient(c); err == nil {
		t.Fatal("world-readable private key accepted")
	}
	wrongCurve, _ := testConfig(t, elliptic.P384())
	if _, err = NewClient(wrongCurve); err == nil {
		t.Fatal("non-P256 key accepted")
	}
}

func testClaim(c Config) push.Claim {
	return push.Claim{
		DeviceID:       uuid.NewString(),
		HubID:          uuid.NewString(),
		EventID:        "7",
		NotificationID: uuid.NewString(),
		Kind:           push.KindBrief,
		Token:          strings.Repeat("a1", 32),
		Environment:    c.Environment,
		RegisteredAt:   time.Now().Add(-time.Hour),
		ExpiresAt:      time.Now().Add(time.Hour),
	}
}

// This is a local protocol fixture with a synthetic signing key. It checks our
// HTTP/2 request and the fixed payload, not Apple delivery or account setup.
func TestHTTP2PayloadJWTAndStableRequestIdentity(t *testing.T) {
	c, key := testConfig(t, elliptic.P256())
	claim := testClaim(c)
	var firstBearer string
	requests := 0
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.ProtoMajor != 2 || r.Method != http.MethodPost || r.URL.Path != "/3/device/"+claim.Token {
			t.Error("wrong APNs request transport or path")
		}
		for header, want := range map[string]string{"apns-topic": c.Topic, "apns-id": claim.NotificationID, "apns-collapse-id": claim.NotificationID, "apns-push-type": "alert", "apns-priority": "10"} {
			if r.Header.Get(header) != want {
				t.Errorf("wrong %s", header)
			}
		}
		if r.Header.Get("apns-expiration") == "" {
			t.Error("missing finite expiry")
		}
		bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "bearer ")
		parsed, err := jwt.Parse(bearer, func(token *jwt.Token) (any, error) {
			if token.Method != jwt.SigningMethodES256 {
				return nil, errors.New("wrong signing method")
			}
			return &key.PublicKey, nil
		})
		if err != nil || !parsed.Valid {
			t.Error("synthetic provider token signature invalid")
		}
		if parsed != nil && (parsed.Header["kid"] != c.KeyID || parsed.Claims.(jwt.MapClaims)["iss"] != c.TeamID) {
			t.Error("wrong provider identity")
		}
		if firstBearer == "" {
			firstBearer = bearer
		} else if bearer != firstBearer {
			t.Error("provider token was regenerated too soon")
		}
		body, err := io.ReadAll(r.Body)
		if err != nil || len(body) > 4096 {
			t.Error("invalid payload bound")
		}
		want := `{"aps":{"alert":{"title":"Isoform","body":"Your brief is ready."},"sound":"default","mutable-content":1},"yansu":{"kind":"brief","hub":"` + claim.HubID + `","eid":"7"}}`
		var got, expected any
		if err = json.Unmarshal(body, &got); err != nil {
			t.Error(err)
		}
		_ = json.Unmarshal([]byte(want), &expected)
		if !reflect.DeepEqual(got, expected) || strings.Contains(string(body), claim.Token) {
			t.Errorf("payload differs from the protocol: %s", body)
		}
		w.Header().Set("apns-id", claim.NotificationID)
		w.WriteHeader(http.StatusOK)
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	client, err := NewClient(c)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.client.Host = server.URL
	client.client.HTTPClient.Transport = &boundedTransport{base: server.Client().Transport}
	for range 2 {
		outcome, err := client.Send(context.Background(), claim)
		if err != nil || outcome != push.Accepted {
			t.Fatalf("protocol request failed: %v", err)
		}
	}
	if requests != 2 {
		t.Fatal("requests not observed")
	}
}

func TestResponseClassesPreserveNewRegistration(t *testing.T) {
	observed := time.Unix(1000, 500_000_000)
	for _, tc := range []struct {
		status      int
		reason      string
		invalidated time.Time
		want        push.Outcome
	}{
		{200, "", time.Time{}, push.Accepted}, {200, "unexpected", time.Time{}, push.Permanent},
		{410, "Unregistered", time.Unix(1000, 0), push.Permanent},
		{410, "Unregistered", time.Unix(1001, 0), push.TokenInvalid},
		{410, "ExpiredToken", time.Time{}, push.Permanent},
		{400, "BadDeviceToken", time.Time{}, push.TokenInvalid},
		{400, "DeviceTokenNotForTopic", time.Time{}, push.TokenInvalid},
		{403, "Forbidden", time.Time{}, push.Permanent},
		{413, "PayloadTooLarge", time.Time{}, push.Permanent},
		{429, "TooManyRequests", time.Time{}, push.Retry},
		{500, "InternalServerError", time.Time{}, push.Retry},
		{503, "Shutdown", time.Time{}, push.Retry},
	} {
		response := &apns2.Response{StatusCode: tc.status, Reason: tc.reason, Timestamp: apns2.Time{Time: tc.invalidated}}
		if got := classify(response, observed); got != tc.want {
			t.Errorf("%d %s got %s want %s", tc.status, tc.reason, got, tc.want)
		}
	}
}

func TestHTTP2ErrorsAndRedirectDoNotLeakCredentials(t *testing.T) {
	c, _ := testConfig(t, elliptic.P256())
	claim := testClaim(c)
	for _, tc := range []struct {
		name   string
		status int
		body   string
		kind   string
	}{
		{"auth", 403, `{"reason":"InvalidProviderToken"}`, "configuration"},
		{"topic", 400, `{"reason":"TopicDisallowed"}`, "configuration"},
		{"provider-rate", 429, `{"reason":"TooManyProviderTokenUpdates"}`, "cooldown"},
		{"oversize", 503, `{"reason":"` + strings.Repeat("x", 20000), "retry"},
		{"redirect", 307, "", "permanent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				w.Header().Set("Location", "/must-not-follow")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			server.EnableHTTP2 = true
			server.StartTLS()
			defer server.Close()
			client, err := NewClient(c)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			client.client.Host = server.URL
			client.client.HTTPClient.Transport = &boundedTransport{base: server.Client().Transport}
			outcome, err := client.Send(context.Background(), claim)
			if err == nil || strings.Contains(err.Error(), claim.Token) || strings.Contains(err.Error(), client.client.Token.Bearer) {
				t.Fatal("missing safe error or credential leak")
			}
			var configErr *configurationError
			var cooldown *providerCooldownError
			switch tc.kind {
			case "configuration":
				if !errors.As(err, &configErr) {
					t.Fatal("configuration outage not classified")
				}
			case "cooldown":
				if !errors.As(err, &cooldown) {
					t.Fatal("provider cooldown not classified")
				}
			case "retry":
				if outcome != push.Retry {
					t.Fatal("retry not retained")
				}
			case "permanent":
				if outcome != push.Permanent {
					t.Fatal("redirect not rejected")
				}
			}
			if requests != 1 {
				t.Fatal("followed a redirect or silently retried")
			}
		})
	}
}

func TestNotificationPayloadPerKind(t *testing.T) {
	c, _ := testConfig(t, elliptic.P256())
	claim := testClaim(c)
	for _, tc := range []struct {
		kind, eventID, want string
	}{
		{push.KindBrief, "7", `{"aps":{"alert":{"title":"Isoform","body":"Your brief is ready."},"sound":"default","mutable-content":1},"yansu":{"kind":"brief","hub":"` + claim.HubID + `","eid":"7"}}`},
		{push.KindNeedsYou, "event:7", `{"aps":{"alert":{"title":"Isoform","body":"Isoform needs you."},"sound":"default","mutable-content":1,"interruption-level":"time-sensitive"},"yansu":{"kind":"needs_you","hub":"` + claim.HubID + `","eid":"event:7"}}`},
	} {
		claim.Kind, claim.EventID = tc.kind, tc.eventID
		payload, err := notificationFor(claim)
		if err != nil {
			t.Fatalf("%s: %v", tc.kind, err)
		}
		body, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		var got, want any
		if err = json.Unmarshal(body, &got); err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal([]byte(tc.want), &want); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s payload %s, want %s", tc.kind, body, tc.want)
		}
	}
}

func TestUnknownPushKindIsPermanentAndNeverSent(t *testing.T) {
	c, _ := testConfig(t, elliptic.P256())
	client, err := NewClient(c)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	claim := testClaim(c)
	claim.Kind = "mystery"
	outcome, err := client.Send(context.Background(), claim)
	if outcome != push.Permanent || !errors.Is(err, push.ErrUnknownKind) {
		t.Fatalf("unknown kind gave %v (%v)", outcome, err)
	}
}
