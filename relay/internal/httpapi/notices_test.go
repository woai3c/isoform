package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"isoform-relay/internal/push"
)

// momentFrame builds an out frame that carries a sealed notice for the device.
// mutate may change or remove fields to build refusals.
func momentFrame(t *testing.T, hubID, deviceID string, seq uint64, noticeID string, mutate func(map[string]any)) map[string]any {
	t.Helper()
	frame := frameWithEnvelope(t, "out", makeEnvelope(t, hubID, deviceID, "h2d", seq, "event", 50))
	frame["notify"] = true
	frame["push"] = push.KindMoment
	frame["noticeId"] = noticeID
	frame["kind"] = "decision_due"
	frame["ct"] = randomCT(t, 200)
	frame["category"] = push.CategoryDecision
	frame["level"] = push.LevelTimeSensitive
	if mutate != nil {
		mutate(frame)
	}
	return frame
}

func TestMomentOutFrameQueuesOneSealedPushPerNotice(t *testing.T) {
	r := newRelay(t, "development")
	hubID := uuid.NewString()
	hc := r.connectHub(hubID, randomToken(t))
	dev := r.pairDevice(hc, allScopes)
	r.expectStatus(r.putPush(dev, strings.Repeat("ab", 32), "development"), http.StatusOK, "registration")
	noticeID := uuid.NewString()
	frame := momentFrame(t, hubID, dev.id, 1, noticeID, nil)
	hc.send(frame)
	hc.send(frame)
	hc.barrier()
	claim, err := r.store.ClaimPush(t.Context(), time.Now().Add(time.Second), time.Minute)
	if err != nil || claim == nil {
		t.Fatalf("moment push missing: %+v %v", claim, err)
	}
	if claim.Kind != push.KindMoment || claim.DeviceID != dev.id || claim.EventID != "event:1" ||
		claim.Notice.ID != noticeID || claim.Notice.Moment != "decision_due" || claim.Notice.CT != frame["ct"] ||
		claim.Notice.Category != push.CategoryDecision || claim.Notice.Level != push.LevelTimeSensitive || claim.Notice.Collapse != noticeID {
		t.Fatalf("queued notice %+v", claim.Notice)
	}
	if next, err := r.store.ClaimPush(t.Context(), time.Now().Add(time.Second), time.Minute); err != nil || next != nil {
		t.Fatalf("a repeated notice queued a second push: %+v %v", next, err)
	}
	stored := r.getWith("/v1/responses?wait=0", dev.bearer)
	r.expectStatus(stored, http.StatusOK, "responses")
	if string(rawField(t, stored.Data, "envelopes")) == "[]" {
		t.Fatal("the event envelope was not stored for the device")
	}
}

func TestMomentOutFrameRefusalsStoreAndQueueNothing(t *testing.T) {
	r := newRelay(t, "development")
	hubID := uuid.NewString()
	hc := r.connectHub(hubID, randomToken(t))
	dev := r.pairDevice(hc, allScopes)
	r.expectStatus(r.putPush(dev, strings.Repeat("cd", 32), "development"), http.StatusOK, "registration")
	noticeID := uuid.NewString()
	refused := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"no notice id", func(f map[string]any) { delete(f, "noticeId") }},
		{"notice id not a UUID", func(f map[string]any) { f["noticeId"] = "not-a-uuid" }},
		{"moment kind not lowercase words", func(f map[string]any) { f["kind"] = "Decision Due" }},
		{"unknown category", func(f map[string]any) { f["category"] = "YANSU_OTHER" }},
		{"unknown level", func(f map[string]any) { f["level"] = "critical" }},
		{"sealed text over the bound", func(f map[string]any) { f["ct"] = randomCT(t, 3600) }},
		{"collapse id with a space", func(f map[string]any) { f["collapse"] = "decision req" }},
		{"notice fields without notify", func(f map[string]any) { f["notify"] = false; f["push"] = "" }},
		{"notice on a response", func(f map[string]any) {
			f["env"] = json.RawMessage(envelopeJSON(t, makeEnvelope(t, hubID, dev.id, "h2d", 90, "res", 50)))
		}},
		{"unknown field", func(f map[string]any) { f["sound"] = "loud" }},
		{"notice fields on a needs_you push", func(f map[string]any) { f["push"] = "needs_you" }},
	}
	for i, tc := range refused {
		seq := uint64(i + 1)
		frame := momentFrame(t, hubID, dev.id, seq, noticeID, tc.mutate)
		if tc.name == "notice on a response" {
			frame["env"] = json.RawMessage(envelopeJSON(t, makeEnvelope(t, hubID, dev.id, "h2d", seq, "res", 50)))
		}
		hc.send(frame)
		if answer := hc.recvType("error"); answer["ref"] != "out" {
			t.Fatalf("%s: answered %v, want an out refusal", tc.name, answer)
		}
	}
	hc.barrier()
	if claim, err := r.store.ClaimPush(t.Context(), time.Now().Add(time.Second), time.Minute); err != nil || claim != nil {
		t.Fatalf("refused notice queued a push: %+v %v", claim, err)
	}
	stored := r.getWith("/v1/responses?wait=0", dev.bearer)
	r.expectStatus(stored, http.StatusOK, "responses")
	if string(rawField(t, stored.Data, "envelopes")) != "[]" {
		t.Fatalf("refused notice stored an envelope: %s", stored.Data)
	}
}

func TestNoticeReceiptRoutesAuthenticateValidateAndForward(t *testing.T) {
	r := newRelay(t, "development")
	hubID := uuid.NewString()
	hc := r.connectHub(hubID, randomToken(t))
	dev := r.pairDevice(hc, allScopes)
	noPush := r.pairDevice(hc, []string{"brief:read"})
	noticeID := uuid.NewString()
	path := "/v1/notices/" + noticeID + "/receipts"
	at := rfc3339(r.now())
	opened := map[string]any{"state": "opened", "action": "", "at": at}

	r.expectStatus(r.call(http.MethodPost, path, nil, []byte(`{"state":"opened","action":"","at":"`+at+`"}`)), http.StatusUnauthorized, "no bearer")
	r.expectStatus(r.postWith(path, noPush.bearer, opened), http.StatusForbidden, "device without push scope")
	r.expectStatus(r.postWith("/v1/notices/"+noticeID+"/other", dev.bearer, opened), http.StatusNotFound, "unknown suffix")
	r.expectStatus(r.postWith("/v1/notices/not-a-uuid/receipts", dev.bearer, opened), http.StatusNotFound, "notice id not a UUID")
	r.expectStatus(r.getWith(path, dev.bearer), http.StatusMethodNotAllowed, "GET")
	bad := []struct {
		name string
		body map[string]any
	}{
		{"unknown state", map[string]any{"state": "read", "action": "", "at": at}},
		{"acted without an action", map[string]any{"state": "acted", "action": "", "at": at}},
		{"action not an id", map[string]any{"state": "acted", "action": "Approve Now", "at": at}},
		{"time without Z", map[string]any{"state": "opened", "action": "", "at": strings.TrimSuffix(at, "Z")}},
		{"unknown field", map[string]any{"state": "opened", "action": "", "at": at, "text": "hello"}},
	}
	for _, tc := range bad {
		r.expectStatus(r.postWith(path, dev.bearer, tc.body), http.StatusBadRequest, tc.name)
	}

	r.expectStatus(r.postWith(path, dev.bearer, opened), http.StatusAccepted, "opened")
	forwarded := hc.recvType("receipt")
	if forwarded["dev"] != dev.id || forwarded["noticeId"] != noticeID || forwarded["state"] != "opened" || forwarded["action"] != "" || forwarded["at"] != at {
		t.Fatalf("forwarded receipt %v", forwarded)
	}
	// A repeat is accepted and forwarded only once; a new action is a new receipt.
	// The hub is kicked asynchronously, so the acted frame is awaited, not barriered.
	r.expectStatus(r.postWith(path, dev.bearer, opened), http.StatusAccepted, "repeat")
	acted := map[string]any{"state": "acted", "action": "approve", "at": at}
	r.expectStatus(r.postWith(path, dev.bearer, acted), http.StatusAccepted, "acted")
	next := hc.recvType("receipt")
	if next["state"] != "acted" || next["action"] != "approve" {
		t.Fatalf("the repeat was forwarded instead of the acted receipt: %v", next)
	}
	hc.barrier()
	for _, frame := range hc.pending {
		if frame["t"] == "receipt" {
			t.Fatalf("an extra receipt was forwarded: %v", frame)
		}
	}
}

func TestNoticeReceiptWaitsForTheHubAndIsReplayedOnConnect(t *testing.T) {
	r := newRelay(t, "development")
	hubID := uuid.NewString()
	credential := randomToken(t)
	hc := r.connectHub(hubID, credential)
	dev := r.pairDevice(hc, allScopes)
	hc.conn.Close()
	r.waitHubOnline(dev, false)

	noticeID := uuid.NewString()
	at := rfc3339(r.now())
	r.expectStatus(r.postWith(fmt.Sprintf("/v1/notices/%s/receipts", noticeID), dev.bearer, map[string]any{"state": "acted", "action": "approve", "at": at}), http.StatusAccepted, "receipt while the hub is away")
	reconnected := r.connectHub(hubID, credential)
	replayed := reconnected.recvType("receipt")
	if replayed["dev"] != dev.id || replayed["noticeId"] != noticeID || replayed["state"] != "acted" || replayed["action"] != "approve" {
		t.Fatalf("replayed receipt %v", replayed)
	}
}
