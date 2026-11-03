package httpapi

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"isoform-relay/internal/push"
)

func TestPushRegistrationRules(t *testing.T) {
	unconfigured := newRelay(t, "")
	dev := unconfigured.pairDevice(unconfigured.connectHub(uuid.NewString(), randomToken(t)), allScopes)
	unconfigured.expectStatus(unconfigured.putPush(dev, strings.Repeat("ab", 32), "development"), http.StatusServiceUnavailable, "APNs not configured")
	unconfigured.expectStatus(unconfigured.call(http.MethodDelete, "/v1/push/token", bearer(dev.bearer), nil), http.StatusOK, "removal works without APNs")

	r := newRelay(t, "development")
	dev = r.pairDevice(r.connectHub(uuid.NewString(), randomToken(t)), allScopes)
	r.expectStatus(r.putPush(dev, strings.Repeat("ab", 32), "production"), http.StatusServiceUnavailable, "other APNs environment")
	r.expectStatus(r.putPush(dev, "zz", "development"), http.StatusBadRequest, "malformed token")
	r.expectStatus(r.putPush(dev, strings.Repeat("ab", 32), "sandbox"), http.StatusBadRequest, "unknown environment")
	registered := r.putPush(dev, strings.Repeat("AB", 32), "development")
	r.expectStatus(registered, http.StatusOK, "registration")
	if enabled := rawField(t, registered.Data, "enabled"); string(enabled) != "true" {
		t.Fatalf("registration reply %s", registered.Data)
	}
	r.expectStatus(r.call(http.MethodDelete, "/v1/push/token", bearer(dev.bearer), nil), http.StatusOK, "removal")
	r.expectStatus(r.call(http.MethodDelete, "/v1/push/token", bearer(dev.bearer), nil), http.StatusOK, "repeated removal")
}

func TestGatewayPushRegistrationRequiresAndStoresGrant(t *testing.T) {
	r := newRelay(t, "development")
	r.h.Push.Gateway = true
	hubID := uuid.NewString()
	hc := r.connectHub(hubID, randomToken(t))
	dev := r.pairDevice(hc, allScopes)
	missing := r.call(http.MethodPut, "/v1/push/token", bearer(dev.bearer),
		[]byte(`{"deviceToken":"`+strings.Repeat("ab", 32)+`","environment":"development"}`))
	r.expectStatus(missing, http.StatusBadRequest, "gateway registration without grant")
	if missing.Error != "Push registration needs a gateway grant" {
		t.Fatalf("missing grant error %q", missing.Error)
	}

	r.expectStatus(r.putPushWithGrant(dev, strings.Repeat("cd", 32), "development", "grant-value"), http.StatusOK, "grant registration")
	hc.send(briefFrame(t, hubID, dev.id, 1, true))
	hc.barrier()
	claim, err := r.store.ClaimPush(t.Context(), time.Now().Add(time.Second), time.Minute)
	if err != nil || claim == nil || claim.GatewayGrant != "grant-value" {
		t.Fatalf("claim gateway grant: %+v %v", claim, err)
	}
}

func TestDirectAPNsIgnoresGatewayGrantField(t *testing.T) {
	r := newRelay(t, "development")
	hubID := uuid.NewString()
	hc := r.connectHub(hubID, randomToken(t))
	dev := r.pairDevice(hc, allScopes)
	r.expectStatus(r.putPushWithGrant(dev, strings.Repeat("ef", 32), "development", "ignored"), http.StatusOK, "direct registration")
	hc.send(briefFrame(t, hubID, dev.id, 1, true))
	hc.barrier()
	claim, err := r.store.ClaimPush(t.Context(), time.Now().Add(time.Second), time.Minute)
	if err != nil || claim == nil || claim.GatewayGrant != "" {
		t.Fatalf("direct-APNs claim carries gateway grant: %+v %v", claim, err)
	}
}

func TestPushCapPerHubAndTokenMove(t *testing.T) {
	r := newRelay(t, "development")
	hubID := uuid.NewString()
	hc := r.connectHub(hubID, randomToken(t))
	devices := make([]device, 0, 11)
	for range 11 {
		devices = append(devices, r.pairDevice(hc, allScopes))
	}
	for i := range 10 {
		r.expectStatus(r.putPush(devices[i], fmt.Sprintf("%064x", i+1), "development"), http.StatusOK, "registration within the cap")
	}
	r.expectStatus(r.putPush(devices[10], fmt.Sprintf("%064x", 99), "development"), http.StatusConflict, "eleventh registration on the hub")
	// A token already held by a device of this hub moves without using a new slot.
	r.expectStatus(r.putPush(devices[10], fmt.Sprintf("%064x", 1), "development"), http.StatusOK, "token moved from another device")
	hc.send(briefFrame(t, hubID, devices[0].id, 1, true))
	hc.send(briefFrame(t, hubID, devices[10].id, 2, true))
	hc.barrier()
	claim, err := r.store.ClaimPush(t.Context(), time.Now().Add(time.Second), time.Minute)
	if err != nil || claim == nil || claim.DeviceID != devices[10].id {
		t.Fatalf("moved token did not carry notifications: %+v %v", claim, err)
	}
	if next, err := r.store.ClaimPush(t.Context(), time.Now().Add(time.Second), time.Minute); err != nil || next != nil {
		t.Fatalf("previous holder of the moved token still receives notifications: %+v %v", next, err)
	}
}

// eventFrame builds an out frame that carries a sequenced event and asks for a
// push of pushKind when notify is set.
func eventFrame(t *testing.T, hubID, deviceID string, seq uint64, notify bool, pushKind string) map[string]any {
	t.Helper()
	frame := frameWithEnvelope(t, "out", makeEnvelope(t, hubID, deviceID, "h2d", seq, "event", 50))
	frame["notify"] = notify
	frame["push"] = pushKind
	return frame
}

func TestNeedsYouOutFrameQueuesOnePushPerEvent(t *testing.T) {
	r := newRelay(t, "development")
	hubID := uuid.NewString()
	hc := r.connectHub(hubID, randomToken(t))
	bare := r.pairDevice(hc, allScopes)
	registered := r.pairDevice(hc, allScopes)
	r.expectStatus(r.putPush(registered, strings.Repeat("cd", 32), "development"), http.StatusOK, "registration")
	hc.send(eventFrame(t, hubID, bare.id, 1, true, push.KindNeedsYou))
	hc.send(eventFrame(t, hubID, registered.id, 2, true, push.KindNeedsYou))
	hc.send(eventFrame(t, hubID, registered.id, 2, true, push.KindNeedsYou))
	hc.barrier()
	claim, err := r.store.ClaimPush(t.Context(), time.Now().Add(time.Second), time.Minute)
	if err != nil || claim == nil || claim.DeviceID != registered.id || claim.Kind != push.KindNeedsYou || claim.EventID != "event:2" {
		t.Fatalf("needs_you push %+v %v", claim, err)
	}
	if next, err := r.store.ClaimPush(t.Context(), time.Now().Add(time.Second), time.Minute); err != nil || next != nil {
		t.Fatalf("extra push for one event, or for a device without a token: %+v %v", next, err)
	}
	stored := r.getWith("/v1/responses?wait=0", registered.bearer)
	r.expectStatus(stored, http.StatusOK, "responses")
	if string(rawField(t, stored.Data, "envelopes")) == "[]" {
		t.Fatal("notifying event was not stored")
	}
}

func TestNotifyRefusalsStoreNothing(t *testing.T) {
	r := newRelay(t, "development")
	hubID := uuid.NewString()
	hc := r.connectHub(hubID, randomToken(t))
	dev := r.pairDevice(hc, allScopes)
	r.expectStatus(r.putPush(dev, strings.Repeat("ef", 32), "development"), http.StatusOK, "registration")
	onResponse := frameWithEnvelope(t, "out", makeEnvelope(t, hubID, dev.id, "h2d", 1, "res", 50))
	onResponse["notify"], onResponse["push"] = true, push.KindNeedsYou
	refused := []struct {
		name  string
		frame map[string]any
	}{
		{"notify on a response", onResponse},
		{"unknown push kind", eventFrame(t, hubID, dev.id, 2, true, "mystery")},
		{"notify without a kind", eventFrame(t, hubID, dev.id, 3, true, "")},
		{"brief kind on out", eventFrame(t, hubID, dev.id, 4, true, push.KindBrief)},
		{"kind without notify", eventFrame(t, hubID, dev.id, 5, false, push.KindNeedsYou)},
	}
	for _, tc := range refused {
		hc.send(tc.frame)
		if frame := hc.recvType("error"); frame["ref"] != "out" {
			t.Fatalf("%s: answered %v, want an out refusal", tc.name, frame)
		}
	}
	hc.barrier()
	if claim, err := r.store.ClaimPush(t.Context(), time.Now().Add(time.Second), time.Minute); err != nil || claim != nil {
		t.Fatalf("refused frame queued a push: %+v %v", claim, err)
	}
	stored := r.getWith("/v1/responses?wait=0", dev.bearer)
	r.expectStatus(stored, http.StatusOK, "responses")
	if string(rawField(t, stored.Data, "envelopes")) != "[]" {
		t.Fatalf("refused frame stored an envelope: %s", stored.Data)
	}
}

func TestPushKindTravelsWithEachNotification(t *testing.T) {
	r := newRelay(t, "development")
	hubID := uuid.NewString()
	hc := r.connectHub(hubID, randomToken(t))
	dev := r.pairDevice(hc, allScopes)
	r.expectStatus(r.putPush(dev, strings.Repeat("12", 32), "development"), http.StatusOK, "registration")
	hc.send(briefFrame(t, hubID, dev.id, 1, true))
	hc.send(eventFrame(t, hubID, dev.id, 2, true, push.KindNeedsYou))
	hc.barrier()
	var kinds []string
	for range 2 {
		claim, err := r.store.ClaimPush(t.Context(), time.Now().Add(time.Second), time.Minute)
		if err != nil || claim == nil {
			t.Fatalf("push %d missing: %+v %v", len(kinds)+1, claim, err)
		}
		kinds = append(kinds, claim.Kind)
	}
	if fmt.Sprint(kinds) != "[brief needs_you]" {
		t.Fatalf("push kinds %v, want brief then needs_you", kinds)
	}
}
