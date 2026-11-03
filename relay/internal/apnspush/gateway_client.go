package apnspush

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"isoform-relay/internal/push"
	"isoform-relay/internal/pushgatewayauth"
	"isoform-relay/internal/wire"
)

const gatewayResponseLimit = 16 * 1024

type GatewayConfig struct {
	URL         string
	Environment string
	HubID       string
	HubPub      string
	HubKey      string
}

func (c GatewayConfig) Configured() bool { return c.URL != "" }

func LoadGatewayConfig(getenv func(string) string) (GatewayConfig, error) {
	c := GatewayConfig{
		URL:         strings.TrimSpace(getenv("ISOFORM_RELAY_PUSH_GATEWAY_URL")),
		Environment: strings.TrimSpace(getenv("ISOFORM_RELAY_PUSH_GATEWAY_ENVIRONMENT")),
		HubID:       strings.TrimSpace(getenv("ISOFORM_RELAY_PUSH_GATEWAY_HUB_ID")),
		HubPub:      strings.TrimSpace(getenv("ISOFORM_RELAY_PUSH_GATEWAY_HUB_PUB")),
		HubKey:      strings.TrimSpace(getenv("ISOFORM_RELAY_PUSH_GATEWAY_HUB_KEY")),
	}
	if (c.URL == "") != (c.Environment == "") {
		return c, errors.New("ISOFORM_RELAY_PUSH_GATEWAY_URL and ISOFORM_RELAY_PUSH_GATEWAY_ENVIRONMENT must be set together")
	}
	if c.URL == "" && (c.HubID != "" || c.HubPub != "" || c.HubKey != "") {
		return c, errors.New("push gateway hub identity and key require ISOFORM_RELAY_PUSH_GATEWAY_URL")
	}
	apnsNames := []string{
		"ISOFORM_RELAY_APNS_KEY_FILE", "ISOFORM_RELAY_APNS_KEY_ID", "ISOFORM_RELAY_APNS_TEAM_ID",
		"ISOFORM_RELAY_APNS_TOPIC", "ISOFORM_RELAY_APNS_ENVIRONMENT",
	}
	apnsConfigured := false
	for _, name := range apnsNames {
		apnsConfigured = apnsConfigured || strings.TrimSpace(getenv(name)) != ""
	}
	if c.Configured() && apnsConfigured {
		return c, errors.New("push gateway and direct APNs configuration are mutually exclusive")
	}
	if !c.Configured() {
		return c, nil
	}
	if c.HubID == "" || c.HubPub == "" || c.HubKey == "" {
		return c, errors.New("ISOFORM_RELAY_PUSH_GATEWAY_HUB_ID, _HUB_PUB and _HUB_KEY are required with the gateway URL")
	}
	if !wire.ValidUUID(c.HubID) {
		return c, errors.New("ISOFORM_RELAY_PUSH_GATEWAY_HUB_ID must be a UUID")
	}
	if _, _, err := pushgatewayauth.DecodeHubPublicKey(c.HubPub); err != nil {
		return c, errors.New("ISOFORM_RELAY_PUSH_GATEWAY_HUB_PUB must be a base64url X25519 public key")
	}
	hubKey, err := base64.RawURLEncoding.DecodeString(c.HubKey)
	if err != nil || len(hubKey) != 32 || base64.RawURLEncoding.EncodeToString(hubKey) != c.HubKey {
		return c, errors.New("ISOFORM_RELAY_PUSH_GATEWAY_HUB_KEY must be a base64url 32-byte key")
	}
	switch c.Environment {
	case "development", "production":
	default:
		return c, errors.New("ISOFORM_RELAY_PUSH_GATEWAY_ENVIRONMENT must be development or production")
	}
	parsed, err := url.Parse(c.URL)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Path != "" && parsed.Path != "/") {
		return c, errors.New("ISOFORM_RELAY_PUSH_GATEWAY_URL must be an origin")
	}
	switch parsed.Scheme {
	case "https":
	case "http":
		ip := net.ParseIP(parsed.Hostname())
		if parsed.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return c, errors.New("push gateway URLs must use HTTPS except on loopback")
		}
	default:
		return c, errors.New("push gateway URLs must use HTTPS except on loopback")
	}
	c.URL = strings.TrimRight(c.URL, "/")
	return c, nil
}

type GatewayClient struct {
	config GatewayConfig
	client *http.Client
}

func NewGatewayClient(config GatewayConfig) *GatewayClient {
	return &GatewayClient{
		config: config,
		client: &http.Client{Timeout: 20 * time.Second},
	}
}

type gatewayNotice struct {
	ID       string `json:"id"`
	CT       string `json:"ct"`
	Moment   string `json:"moment"`
	Category string `json:"category"`
	Level    string `json:"level"`
	Collapse string `json:"collapse"`
}

type gatewayRequest struct {
	Token          string        `json:"token"`
	Environment    string        `json:"environment"`
	Kind           string        `json:"kind"`
	Hub            string        `json:"hub"`
	Device         string        `json:"device"`
	EventID        string        `json:"eventId"`
	NotificationID string        `json:"notificationId"`
	Notice         gatewayNotice `json:"notice"`
	RegisteredAt   string        `json:"registeredAt"`
	ExpiresAt      string        `json:"expiresAt"`
	Grant          string        `json:"grant"`
}

type gatewayResponse struct {
	Outcome push.Outcome `json:"outcome"`
}

func (c *GatewayClient) Send(ctx context.Context, claim push.Claim) (push.Outcome, error) {
	if claim.HubID != c.config.HubID {
		return push.Permanent, errors.New("push claim hub does not match the gateway configuration")
	}
	notice := gatewayNotice{
		ID: claim.Notice.ID, CT: claim.Notice.CT, Moment: claim.Notice.Moment,
		Category: claim.Notice.Category, Level: claim.Notice.Level, Collapse: claim.Notice.Collapse,
	}
	body, err := json.Marshal(gatewayRequest{
		Token: claim.Token, Environment: claim.Environment, Kind: claim.Kind,
		Hub: claim.HubID, Device: claim.DeviceID, EventID: claim.EventID,
		NotificationID: claim.NotificationID, Notice: notice,
		RegisteredAt: claim.RegisteredAt.UTC().Format(time.RFC3339),
		ExpiresAt:    claim.ExpiresAt.UTC().Format(time.RFC3339), Grant: claim.GatewayGrant,
	})
	if err != nil {
		return push.Retry, errors.New("push gateway request could not be encoded")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.config.URL, "/")+"/v1/push", bytes.NewReader(body))
	if err != nil {
		return push.Retry, errors.New("push gateway request could not be created")
	}
	req.Header.Set("Content-Type", "application/json")
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	hubKey, err := base64.RawURLEncoding.DecodeString(c.config.HubKey)
	if err != nil || len(hubKey) != 32 {
		return push.Retry, errors.New("push gateway hub key is invalid")
	}
	req.Header.Set("Isoform-Hub-Key", c.config.HubPub)
	req.Header.Set("Isoform-Timestamp", timestamp)
	req.Header.Set("Isoform-Signature", pushgatewayauth.RequestSignature(hubKey, timestamp, body))
	resp, err := c.client.Do(req)
	if err != nil {
		return push.Retry, errors.New("push gateway request could not be confirmed")
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		data, err := io.ReadAll(io.LimitReader(resp.Body, gatewayResponseLimit+1))
		if err != nil || len(data) > gatewayResponseLimit {
			return push.Retry, errors.New("push gateway returned an invalid response")
		}
		var result gatewayResponse
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&result); err != nil {
			return push.Retry, errors.New("push gateway returned an invalid response")
		}
		switch result.Outcome {
		case push.Accepted, push.Retry, push.Permanent, push.TokenInvalid:
			return result.Outcome, nil
		default:
			return push.Retry, errors.New("push gateway returned an unknown outcome")
		}
	case http.StatusServiceUnavailable:
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, gatewayResponseLimit))
		return push.Retry, errors.New("push gateway is unavailable")
	case http.StatusTooManyRequests:
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, gatewayResponseLimit))
		if retryAfter(resp.Header.Get("Retry-After"), time.Now()) >= 10*time.Minute {
			return push.Retry, &providerCooldownError{}
		}
		return push.Retry, errors.New("push gateway rate limit reached")
	case http.StatusUnauthorized:
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, gatewayResponseLimit))
		return push.Retry, &configurationError{}
	case http.StatusForbidden:
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, gatewayResponseLimit))
		return push.TokenInvalid, nil
	default:
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, gatewayResponseLimit))
		return push.Retry, fmt.Errorf("push gateway returned HTTP %d", resp.StatusCode)
	}
}

func retryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if seconds, err := time.ParseDuration(value + "s"); err == nil && seconds >= 0 {
		return seconds
	}
	if when, err := http.ParseTime(value); err == nil && when.After(now) {
		return when.Sub(now)
	}
	return 0
}

func IsConfigurationError(err error) bool {
	var target *configurationError
	return errors.As(err, &target)
}

func ProviderCooldown(err error) (time.Duration, bool) {
	var target *providerCooldownError
	if !errors.As(err, &target) {
		return 0, false
	}
	return 20 * time.Minute, true
}
