package httpapi

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestHubCredentialIsBoundToItsHubID(t *testing.T) {
	r := newRelay(t, "")
	hubID, credential := uuid.NewString(), randomToken(t)
	hc := r.connectHub(hubID, credential)
	hc.conn.Close()
	if _, resp, err := r.dialHub(hubID, randomToken(t), nil); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("different credential accepted for a known hub: %v", err)
	}
	if _, resp, err := r.dialHub("", credential, nil); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatal("hub connection without X-Yansu-Hub accepted")
	}
	if _, resp, err := r.dialHub(hubID, "short", nil); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatal("malformed hub credential accepted")
	}
	if _, resp, err := r.dialHub("not-a-uuid", credential, nil); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatal("malformed hub id accepted")
	}
	again := r.connectHub(hubID, credential)
	again.barrier()
}

func TestRegisterMustBeTheFirstFrame(t *testing.T) {
	r := newRelay(t, "")
	hubID, credential := uuid.NewString(), randomToken(t)
	conn, _, err := r.dialHub(hubID, credential, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	hc := &hubClient{t: t, hubID: hubID, conn: conn}
	hc.send(map[string]any{"t": "heartbeat"})
	frame := hc.recv()
	if frame["t"] != "error" || frame["ref"] != "register" {
		t.Fatalf("pre-register frame answer %v", frame)
	}
	if _, err = hc.readFrame(5 * time.Second); err == nil {
		t.Fatal("connection stayed open without register")
	}
}

func TestSilentHubIsDroppedBeforeRegister(t *testing.T) {
	r := newRelay(t, "")
	conn, _, err := r.dialHub(uuid.NewString(), randomToken(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	started := time.Now()
	hc := &hubClient{t: t, conn: conn}
	if _, err = hc.readFrame(15 * time.Second); err == nil {
		t.Fatal("silent connection was not closed")
	}
	if elapsed := time.Since(started); elapsed < 9*time.Second || elapsed > 14*time.Second {
		t.Fatalf("register deadline fired after %s, want about 10 seconds", elapsed)
	}
}

func TestNewerConnectionReplacesOlder(t *testing.T) {
	r := newRelay(t, "")
	hubID, credential := uuid.NewString(), randomToken(t)
	older := r.connectHub(hubID, credential)
	newer := r.connectHub(hubID, credential)
	if frame := older.recv(); frame["t"] != "replaced" {
		t.Fatalf("older connection frame %v, want replaced", frame)
	}
	if _, err := older.readFrame(5 * time.Second); err == nil {
		t.Fatal("replaced connection stayed open")
	}
	dev := r.pairDevice(newer, allScopes)
	online, _ := r.presenceOf(dev)
	if !online {
		t.Fatal("replacement connection is not the live one")
	}
}

func TestMalformedFramesAreRefusedWithoutClosing(t *testing.T) {
	r := newRelay(t, "")
	hubID := uuid.NewString()
	hc := r.connectHub(hubID, randomToken(t))
	dev := r.pairDevice(hc, allScopes)
	oversize := strings.Repeat("x", 300<<10)
	cases := []struct {
		name  string
		frame any
		ref   string
	}{
		{"not an object", "just a string", "unknown"},
		{"unknown type", map[string]any{"t": "mystery"}, "unknown"},
		{"register again", map[string]any{"t": "register", "proto": 1, "hubName": "again"}, "register"},
		{"unknown field", map[string]any{"t": "heartbeat", "extra": true}, "heartbeat"},
		{"pairing without id", map[string]any{"t": "pairing_open"}, "pairing_open"},
		{"bad expiry", map[string]any{"t": "pairing_open", "pairing": uuid.NewString(), "claimTokenHash": strings.Repeat("a", 64), "expiresAt": "tomorrow"}, "pairing_open"},
		{"approval with short bearer hash", map[string]any{"t": "pairing_approve", "pairing": uuid.NewString(), "deviceId": uuid.NewString(), "bearerHash": "abc", "scopes": []string{}, "expiresAt": rfc3339(time.Now().Add(time.Hour)), "ct": randomCT(t, 80)}, "pairing_approve"},
		{"approval with bad scope", map[string]any{"t": "pairing_approve", "pairing": uuid.NewString(), "deviceId": uuid.NewString(), "bearerHash": strings.Repeat("b", 64), "scopes": []string{"Root Access"}, "expiresAt": rfc3339(time.Now().Add(time.Hour)), "ct": randomCT(t, 80)}, "pairing_approve"},
		{"out for an unknown device", frameWithEnvelope(t, "out", makeEnvelope(t, hubID, uuid.NewString(), "h2d", 3, "res", 50)), "out"},
		{"out carrying a request", frameWithEnvelope(t, "out", requestEnvelope(t, dev, 4)), "out"},
		{"out for another hub", frameWithEnvelope(t, "out", makeEnvelope(t, uuid.NewString(), dev.id, "h2d", 5, "res", 50)), "out"},
		{"brief carrying a response", frameWithEnvelope(t, "brief", makeEnvelope(t, hubID, dev.id, "h2d", 6, "res", 50)), "brief"},
		{"brief for an unknown device", briefFrame(t, hubID, uuid.NewString(), 7, false), "brief"},
		{"revoke without device", map[string]any{"t": "revoke"}, "revoke"},
		{"ack with string cursor", map[string]any{"t": "ack", "dev": dev.id, "upTo": "9"}, "ack"},
		{"oversize heartbeat", map[string]any{"t": "heartbeat", "pad": oversize}, "heartbeat"},
	}
	for _, tc := range cases {
		hc.send(tc.frame)
		frame, err := hc.readFrame(5 * time.Second)
		if err != nil {
			t.Fatalf("%s: connection closed: %v", tc.name, err)
		}
		if frame["t"] != "error" || frame["ref"] != tc.ref {
			t.Fatalf("%s: answered %v, want error for %s", tc.name, frame, tc.ref)
		}
	}
	hc.barrier()
	r.expectStatus(r.getWith("/v1/presence", dev.bearer), http.StatusOK, "relay still serves after refusals")
}

func TestRevocationAndUnpairRemoveEverythingForTheDevice(t *testing.T) {
	r := newRelay(t, "development")
	hubID := uuid.NewString()
	hc := r.connectHub(hubID, randomToken(t))
	revoked := r.pairDevice(hc, allScopes)
	kept := r.pairDevice(hc, allScopes)
	r.expectStatus(r.putPush(revoked, strings.Repeat("ab", 32), "development"), http.StatusOK, "push for revoked device")
	hc.send(briefFrame(t, hubID, revoked.id, 1, true))
	r.expectStatus(r.postWith("/v1/requests", revoked.bearer, requestEnvelope(t, revoked, 1)), http.StatusAccepted, "pending request")
	hc.barrier()
	hc.pending = nil

	hc.send(map[string]any{"t": "revoke", "deviceId": revoked.id})
	hc.barrier()
	r.expectStatus(r.getWith("/v1/presence", revoked.bearer), http.StatusUnauthorized, "revoked bearer")
	r.expectStatus(r.getWith("/v1/brief/latest", revoked.bearer), http.StatusUnauthorized, "revoked brief access")
	r.expectStatus(r.getWith("/v1/responses?wait=0", revoked.bearer), http.StatusUnauthorized, "revoked response access")
	if claim, err := r.store.ClaimPush(t.Context(), time.Now().Add(time.Second), time.Minute); err != nil || claim != nil {
		t.Fatalf("revoked device still has a notification: %+v %v", claim, err)
	}
	r.expectStatus(r.getWith("/v1/presence", kept.bearer), http.StatusOK, "other device unaffected")

	r.expectStatus(r.call(http.MethodDelete, "/v1/devices/me", bearer(kept.bearer), nil), http.StatusOK, "self unpair")
	frame := hc.recvType("device_unpaired")
	if frame["deviceId"] != kept.id {
		t.Fatalf("device_unpaired frame %v", frame)
	}
	r.expectStatus(r.getWith("/v1/presence", kept.bearer), http.StatusUnauthorized, "unpaired bearer")
	r.expectStatus(r.call(http.MethodDelete, "/v1/devices/me", bearer(kept.bearer), nil), http.StatusUnauthorized, "second unpair")
}

func TestRevokingAnotherHubsDeviceChangesNothing(t *testing.T) {
	r := newRelay(t, "")
	owner := r.connectHub(uuid.NewString(), randomToken(t))
	dev := r.pairDevice(owner, allScopes)
	intruder := r.connectHub(uuid.NewString(), randomToken(t))
	intruder.send(map[string]any{"t": "revoke", "deviceId": dev.id})
	intruder.barrier()
	r.expectStatus(r.getWith("/v1/presence", dev.bearer), http.StatusOK, "device of another hub")
}
