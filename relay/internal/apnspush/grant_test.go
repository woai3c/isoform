package apnspush

import (
	"encoding/json"
	"testing"
	"time"

	"isoform-relay/internal/push"
)

func TestPushGrantPayload(t *testing.T) {
	expires := time.Date(2026, time.November, 1, 12, 34, 56, 0, time.FixedZone("test", 2*60*60))
	payload, err := json.Marshal(pushGrantPayloadFor("6f1c2e4a-7b3d-4c5e-9a1b-2d3e4f5a6b7c", "hub-public", "grant-value", expires))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"aps":{"content-available":1},"isoform":{"kind":"push_grant","hubId":"6f1c2e4a-7b3d-4c5e-9a1b-2d3e4f5a6b7c","hubPub":"hub-public","grant":"grant-value","expiresAt":"2026-11-01T10:34:56Z"}}`
	if string(payload) != want {
		t.Fatalf("payload = %s, want %s", payload, want)
	}
	if len(payload) > push.MaxPayloadBytes {
		t.Fatalf("payload size %d exceeds APNs limit", len(payload))
	}
}
