package pushgatewayauth

import (
	"bytes"
	"crypto/ecdh"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

const (
	pushGatewayVectorPath  = "../../../mobile-protocol/vectors-v1.json"
	pushGatewayVectorBody  = `{"token":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","environment":"development"}`
	pushGatewayVectorTime  = "1793548800"
	pushGatewayVectorExp   = int64(1809254400)
	pushGatewayVectorHubID = "6f1c2e4a-7b3d-4c5e-9a1b-2d3e4f5a6b7c"
)

type pushGatewayVector struct {
	GatewayPrivate string `json:"gatewayPrivate"`
	GatewayPublic  string `json:"gatewayPublic"`
	HubPrivate     string `json:"hubPrivate"`
	HubPublic      string `json:"hubPublic"`
	HubKey         string `json:"hubKey"`
	GrantKey       string `json:"grantKey"`
	Body           string `json:"body"`
	Timestamp      string `json:"timestamp"`
	Environment    string `json:"environment"`
	Token          string `json:"token"`
	HubID          string `json:"hubId"`
	ExpiresAt      int64  `json:"expiresAt"`
	Signature      string `json:"signature"`
	Grant          string `json:"grant"`
}

func buildPushGatewayVector(t *testing.T) pushGatewayVector {
	t.Helper()
	gatewaySeed := sha256.Sum256([]byte("isoform push gateway vector gateway private v1"))
	hubSeed := sha256.Sum256([]byte("isoform push gateway vector hub private v1"))
	gatewayPrivate, err := ecdh.X25519().NewPrivateKey(gatewaySeed[:])
	if err != nil {
		t.Fatal(err)
	}
	hubPrivate, err := ecdh.X25519().NewPrivateKey(hubSeed[:])
	if err != nil {
		t.Fatal(err)
	}
	hubKey, _, err := DeriveHubKeyFromPrivate(hubPrivate, gatewayPrivate.PublicKey().Bytes())
	if err != nil {
		t.Fatal(err)
	}
	grantKey, err := DeriveGrantKey(gatewayPrivate.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	token := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	hubPublic := base64.RawURLEncoding.EncodeToString(hubPrivate.PublicKey().Bytes())
	expires := time.Unix(pushGatewayVectorExp, 0).UTC()
	grant, err := NewGrant(grantKey, "development", token, pushGatewayVectorHubID, hubPublic, expires)
	if err != nil {
		t.Fatal(err)
	}
	return pushGatewayVector{
		GatewayPrivate: base64.RawURLEncoding.EncodeToString(gatewayPrivate.Bytes()),
		GatewayPublic:  base64.RawURLEncoding.EncodeToString(gatewayPrivate.PublicKey().Bytes()),
		HubPrivate:     base64.RawURLEncoding.EncodeToString(hubPrivate.Bytes()),
		HubPublic:      hubPublic,
		HubKey:         base64.RawURLEncoding.EncodeToString(hubKey),
		GrantKey:       base64.RawURLEncoding.EncodeToString(grantKey),
		Body:           pushGatewayVectorBody,
		Timestamp:      pushGatewayVectorTime,
		Environment:    "development",
		Token:          token,
		HubID:          pushGatewayVectorHubID,
		ExpiresAt:      pushGatewayVectorExp,
		Signature:      RequestSignature(hubKey, pushGatewayVectorTime, []byte(pushGatewayVectorBody)),
		Grant:          grant,
	}
}

func TestPushGatewayAuthVector(t *testing.T) {
	vector := buildPushGatewayVector(t)
	if testing.Short() {
		return
	}
	raw, err := os.ReadFile(pushGatewayVectorPath)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("UPDATE_PUSH_GATEWAY_VECTOR") == "1" {
		if _, exists := root["pushGatewayAuth"]; exists {
			t.Fatal("pushGatewayAuth already exists; update it manually to preserve the other vectors")
		}
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) == 0 || trimmed[len(trimmed)-1] != '}' {
			t.Fatal("invalid vectors JSON")
		}
		pretty, err := json.MarshalIndent(vector, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		nested := strings.ReplaceAll(string(pretty), "\n", "\n  ")
		inside := strings.TrimRight(string(trimmed[:len(trimmed)-1]), " \t\r\n")
		updated := inside + ",\n  \"pushGatewayAuth\": " + nested + "\n}\n"
		if err := os.WriteFile(pushGatewayVectorPath, []byte(updated), 0644); err != nil {
			t.Fatal(err)
		}
		return
	}
	var recorded pushGatewayVector
	if err := json.Unmarshal(root["pushGatewayAuth"], &recorded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(recorded, vector) {
		t.Fatalf("pushGatewayAuth vector differs:\nrecorded: %+v\ngenerated: %+v", recorded, vector)
	}
}
