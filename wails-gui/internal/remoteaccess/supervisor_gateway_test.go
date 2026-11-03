package remoteaccess

import (
	"strings"
	"testing"
)

func TestRelayEnvironmentPassesGatewayIdentityWithoutPrivateKey(t *testing.T) {
	config := Config{
		PushGatewayURL:         "https://push.example",
		PushGatewayEnvironment: "production",
		PushGatewayHubID:       "hub-id",
		PushGatewayHubPub:      "hub-public",
		PushGatewayHubKey:      "derived-key",
	}
	env := strings.Join(relayEnvironment(config, "http://127.0.0.1:8791", t.TempDir()), "\n")
	for _, want := range []string{
		"ISOFORM_RELAY_PUSH_GATEWAY_URL=https://push.example",
		"ISOFORM_RELAY_PUSH_GATEWAY_ENVIRONMENT=production",
		"ISOFORM_RELAY_PUSH_GATEWAY_HUB_ID=hub-id",
		"ISOFORM_RELAY_PUSH_GATEWAY_HUB_PUB=hub-public",
		"ISOFORM_RELAY_PUSH_GATEWAY_HUB_KEY=derived-key",
	} {
		if !strings.Contains(env, want) {
			t.Fatalf("environment missing %q: %s", want, env)
		}
	}
	if strings.Contains(env, "HUB_PRIVATE") {
		t.Fatalf("environment contains a hub private key: %s", env)
	}
}
