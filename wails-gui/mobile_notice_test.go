package main

import (
	"bytes"
	"crypto/ecdh"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"wails-gui/internal/engagement"
)

// mobileTestNotice is a decision notice with two actions and an expiry.
func mobileTestNotice(t *testing.T) mobileNotice {
	t.Helper()
	return mobileNotice{
		ID:        uuid.NewString(),
		Kind:      "decision_due",
		Title:     "Approve sending the Q3 reply to Sam?",
		Body:      "Sam has waited since 09:12.",
		Category:  "YANSU_DECISION",
		SubjectID: uuid.NewString(),
		MomentID:  "moment-chat-card",
		Level:     "time-sensitive",
		Actions: []mobileNoticeAction{
			{ID: "approve", Label: "Approve", Auth: true},
			{ID: "decline", Label: "Decline"},
		},
		CreatedAt: time.Now().UTC().Truncate(time.Second),
		ExpiresAt: time.Now().Add(2 * time.Hour).UTC().Truncate(time.Second),
	}
}

func TestMobileNoticeDestinationSurvivesSealing(t *testing.T) {
	notice := mobileTestNotice(t)
	notice.DestinationKind, notice.DestinationID = "task", "task-42"
	key := mobileNoticeKey("hub", "device", mobileTestKey(7))
	sealed, err := sealMobileNotice(key, "hub", "device", notice)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := openMobileNotice(key, "hub", "device", notice.ID, sealed)
	if err != nil || opened.DestinationKind != "task" || opened.DestinationID != "task-42" {
		t.Fatalf("destination = %+v, %v", opened, err)
	}
	notice.DestinationID = "../bad"
	if _, err := sealMobileNotice(key, "hub", "device", notice); err == nil {
		t.Fatal("unsafe destination accepted")
	}
}

func TestMobileNoticeDestinationsUseSourceIDs(t *testing.T) {
	for _, tc := range []struct {
		kind                                                      engagement.Kind
		subject, moment, sourceBrief, dedupeKey, wantKind, wantID string
	}{
		{engagement.KindBriefReady, "2026-10-31-morning", "moment", "", "", "brief", "2026-10-31-morning"},
		{engagement.KindWeeklyRecap, "2026-10-30", "moment", "2026-10-31-morning", "", "brief", "2026-10-31-morning"},
		{engagement.KindWeeklyRecap, "2026-10-30", "moment", "", "", "", ""},
		{engagement.KindWorkFinished, "task-1", "moment", "", "", "task", "task-1"},
		{engagement.KindReminder, "source", "moment-1", "", "", "chat", "moment-1"},
		{engagement.KindReminder, "source", "hashed-moment", "", "chat:direct-card", "chat", "direct-card"},
		{engagement.KindWorkBlocked, "bad/id", "moment", "", "", "", ""},
	} {
		kind, id := mobileNoticeDestination(tc.kind, tc.subject, tc.moment, tc.sourceBrief, tc.dedupeKey)
		if kind != tc.wantKind || id != tc.wantID {
			t.Fatalf("%s: %q %q", tc.kind, kind, id)
		}
	}
}

// mobileTestNoticeKey derives the notice key the way the phone does: from its own
// private key and the hub's public key.
func mobileTestNoticeKey(t *testing.T, phone *mobileTestPhone, hubPublic *ecdh.PublicKey, hubID string) []byte {
	t.Helper()
	secret, err := phone.private.ECDH(hubPublic)
	if err != nil {
		t.Fatal(err)
	}
	return mobileNoticeKey(hubID, phone.deviceID, secret)
}

// mobileTestRecords copies the live records of the given phones.
func mobileTestRecords(t *testing.T, bridge *mobileBridge, phones ...*mobileTestPhone) []mobileDeviceRecord {
	t.Helper()
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	records := []mobileDeviceRecord{}
	for _, phone := range phones {
		if record := bridge.devices[phone.deviceID]; record != nil {
			records = append(records, *record)
		}
	}
	return records
}

func mobileTestKey(fill byte) []byte { return bytes.Repeat([]byte{fill}, 32) }

func TestMobileNoticeKeyIsSharedWithThePhoneAndSeparateFromEnvelopeKeys(t *testing.T) {
	bridge, _ := mobileTestHub(t)
	phone := newMobileTestPhone(t)
	mobileTestAddDevice(t, bridge, phone, []string{mobileScopePush})
	bridge.mu.Lock()
	record := bridge.devices[phone.deviceID]
	hubKey, err := bridge.noticeKeyLocked(record)
	if err != nil {
		bridge.mu.Unlock()
		t.Fatal(err)
	}
	keys, err := bridge.sessionKeysLocked(record)
	bridge.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	phoneKey := mobileTestNoticeKey(t, phone, bridge.identity.Public, bridge.identity.HubID)
	if len(hubKey) != 32 || !bytes.Equal(hubKey, phoneKey) {
		t.Fatal("the hub and the phone derive different notice keys")
	}
	if bytes.Equal(hubKey, keys.D2H) || bytes.Equal(hubKey, keys.H2D) {
		t.Fatal("the notice key equals an envelope key")
	}
	// The key depends on the device and the hub, as the salt says.
	if bytes.Equal(mobileNoticeKey(bridge.identity.HubID, uuid.NewString(), mobileTestKey(1)), mobileNoticeKey(bridge.identity.HubID, phone.deviceID, mobileTestKey(1))) {
		t.Fatal("the notice key does not depend on the device id")
	}
}

func TestMobileNoticeSealOpenRoundTrip(t *testing.T) {
	hubID, deviceID := uuid.NewString(), uuid.NewString()
	key := mobileNoticeKey(hubID, deviceID, mobileTestKey(7))
	n := mobileTestNotice(t)
	ct, err := sealMobileNotice(key, hubID, deviceID, n)
	if err != nil {
		t.Fatal(err)
	}
	if len(ct) > mobileNoticeMaxWire {
		t.Fatalf("ciphertext is %d characters", len(ct))
	}
	opened, err := openMobileNotice(key, hubID, deviceID, n.ID, ct)
	if err != nil {
		t.Fatal(err)
	}
	if opened.ID != n.ID || opened.Kind != n.Kind || opened.Title != n.Title || opened.Body != n.Body ||
		opened.Category != n.Category || opened.SubjectID != n.SubjectID {
		t.Fatalf("opened notice %+v", opened)
	}
	if opened.MomentID != n.MomentID {
		t.Fatalf("opened notice %+v", opened)
	}
	if len(opened.Actions) != 2 || opened.Actions[0] != n.Actions[0] || opened.Actions[1] != n.Actions[1] {
		t.Fatalf("opened actions %+v", opened.Actions)
	}
	if !opened.CreatedAt.Equal(n.CreatedAt) || !opened.ExpiresAt.Equal(n.ExpiresAt) {
		t.Fatalf("opened times %v %v", opened.CreatedAt, opened.ExpiresAt)
	}
	// The random nonce makes every seal of the same notice different.
	again, err := sealMobileNotice(key, hubID, deviceID, n)
	if err != nil || again == ct {
		t.Fatalf("two seals match: %v", err)
	}
	// Without an expiry or actions the notice still round-trips.
	bare := n
	bare.Actions, bare.ExpiresAt = nil, time.Time{}
	ct, err = sealMobileNotice(key, hubID, deviceID, bare)
	if err != nil {
		t.Fatal(err)
	}
	opened, err = openMobileNotice(key, hubID, deviceID, bare.ID, ct)
	if err != nil || len(opened.Actions) != 0 || !opened.ExpiresAt.IsZero() {
		t.Fatalf("bare notice %+v %v", opened, err)
	}
}

func TestMobileNoticeMomentIDIsOptionalAdditiveV1(t *testing.T) {
	n := mobileTestNotice(t)
	raw, err := mobileNoticeEncode(n)
	if err != nil {
		t.Fatal(err)
	}
	var sealed map[string]any
	if err := json.Unmarshal(bytes.TrimRight(raw, " "), &sealed); err != nil {
		t.Fatal(err)
	}
	if sealed["momentId"] != n.MomentID {
		t.Fatalf("momentId = %v", sealed["momentId"])
	}

	n.MomentID = ""
	raw, err = mobileNoticeEncode(n)
	if err != nil {
		t.Fatal(err)
	}
	sealed = nil
	if err := json.Unmarshal(bytes.TrimRight(raw, " "), &sealed); err != nil {
		t.Fatal(err)
	}
	if _, exists := sealed["momentId"]; exists {
		t.Fatalf("empty momentId was emitted: %v", sealed["momentId"])
	}
}

func TestMobileNoticeAADBindsHubDeviceNoticeAndKey(t *testing.T) {
	hubID, deviceID := uuid.NewString(), uuid.NewString()
	key := mobileNoticeKey(hubID, deviceID, mobileTestKey(7))
	n := mobileTestNotice(t)
	ct, err := sealMobileNotice(key, hubID, deviceID, n)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name            string
		hub, device, id string
		key             []byte
	}{
		{"another hub", uuid.NewString(), deviceID, n.ID, key},
		{"another device", hubID, uuid.NewString(), n.ID, key},
		{"another notice id", hubID, deviceID, uuid.NewString(), key},
		{"another key", hubID, deviceID, n.ID, mobileNoticeKey(hubID, deviceID, mobileTestKey(8))},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := openMobileNotice(tc.key, tc.hub, tc.device, tc.id, ct); !errors.Is(err, errMobileNoticeRejected) {
				t.Fatalf("opened a notice bound to other values: %v", err)
			}
		})
	}
}

func TestMobileNoticeTamperingIsRejected(t *testing.T) {
	hubID, deviceID := uuid.NewString(), uuid.NewString()
	key := mobileNoticeKey(hubID, deviceID, mobileTestKey(7))
	n := mobileTestNotice(t)
	ct, err := sealMobileNotice(key, hubID, deviceID, n)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(ct)
	if err != nil {
		t.Fatal(err)
	}
	flipped := bytes.Clone(raw)
	flipped[len(flipped)/2] ^= 0x01
	tampered := map[string]string{
		"flipped byte":   base64.RawURLEncoding.EncodeToString(flipped),
		"truncated":      ct[:len(ct)-4],
		"padded":         ct + "=",
		"nonce only":     base64.RawURLEncoding.EncodeToString(raw[:mobileNoticeNonceSize]),
		"oversized wire": strings.Repeat("A", mobileNoticeMaxWire+1),
	}
	for name, value := range tampered {
		if _, err := openMobileNotice(key, hubID, deviceID, n.ID, value); !errors.Is(err, errMobileNoticeRejected) {
			t.Fatalf("%s was accepted: %v", name, err)
		}
	}
}

func TestMobileNoticeSealRefusesInvalidAndOversizedNotices(t *testing.T) {
	hubID, deviceID := uuid.NewString(), uuid.NewString()
	key := mobileNoticeKey(hubID, deviceID, mobileTestKey(7))
	tests := []struct {
		name   string
		change func(*mobileNotice)
		want   error
	}{
		{"empty title", func(n *mobileNotice) { n.Title = "" }, errMobileNoticeInvalid},
		{"title over 120 bytes", func(n *mobileNotice) { n.Title = strings.Repeat("t", 121) }, errMobileNoticeInvalid},
		{"body over 300 bytes", func(n *mobileNotice) { n.Body = strings.Repeat("b", 301) }, errMobileNoticeInvalid},
		{"unknown category", func(n *mobileNotice) { n.Category = "YANSU_OTHER" }, errMobileNoticeInvalid},
		{"empty action label", func(n *mobileNotice) { n.Actions[0].Label = "" }, errMobileNoticeInvalid},
		{"five actions", func(n *mobileNotice) {
			n.Actions = []mobileNoticeAction{{ID: "a", Label: "A"}, {ID: "b", Label: "B"}, {ID: "c", Label: "C"}, {ID: "d", Label: "D"}, {ID: "e", Label: "E"}}
		}, errMobileNoticeInvalid},
		{"expiry before creation", func(n *mobileNotice) { n.ExpiresAt = n.CreatedAt.Add(-time.Minute) }, errMobileNoticeInvalid},
		{"escaped text over the wire limit", func(n *mobileNotice) {
			// Each "<" becomes a six-byte escape in JSON, so these valid lengths are too large.
			n.Title, n.Body = strings.Repeat("<", 120), strings.Repeat("<", 300)
		}, errMobileNoticeTooLarge},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			n := mobileTestNotice(t)
			tc.change(&n)
			if _, err := sealMobileNotice(key, hubID, deviceID, n); !errors.Is(err, tc.want) {
				t.Fatalf("sealed a notice that should be refused: %v", err)
			}
		})
	}
	largest := mobileTestNotice(t)
	largest.Title, largest.Body = strings.Repeat("t", 120), strings.Repeat("b", 300)
	ct, err := sealMobileNotice(key, hubID, deviceID, largest)
	if err != nil || len(ct) > mobileNoticeMaxWire {
		t.Fatalf("the largest valid notice was refused: %d characters, %v", len(ct), err)
	}
}

func TestMobileNoticePaddingIsRemovedWithoutTouchingContent(t *testing.T) {
	hubID, deviceID := uuid.NewString(), uuid.NewString()
	key := mobileNoticeKey(hubID, deviceID, mobileTestKey(7))
	n := mobileTestNotice(t)
	n.Body = "Ends with spaces   "
	padded, err := mobileNoticeEncode(n)
	if err != nil {
		t.Fatal(err)
	}
	if len(padded)%mobileNoticePadBlock != 0 || !bytes.HasSuffix(padded, []byte(" ")) {
		t.Fatalf("plaintext of %d bytes is not padded to %d", len(padded), mobileNoticePadBlock)
	}
	ct, err := sealMobileNotice(key, hubID, deviceID, n)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := openMobileNotice(key, hubID, deviceID, n.ID, ct)
	if err != nil || opened.Body != n.Body {
		t.Fatalf("content spaces changed on open: %q %v", opened.Body, err)
	}
	if got, err := openMobileNoticePlaintext(key, hubID, deviceID, n.ID, ct); err != nil || !bytes.Equal(got, padded) {
		t.Fatalf("the sealed plaintext is not the padded encoding: %v", err)
	}
}

func TestMobilePublishedNoticeIsSealedForEachPushDevice(t *testing.T) {
	bridge, relay := mobileTestHub(t)
	reader := newMobileTestPhone(t)
	bystander := newMobileTestPhone(t)
	mobileTestAddDevice(t, bridge, reader, []string{mobileScopeBriefRead, mobileScopePush})
	mobileTestAddDevice(t, bridge, bystander, []string{mobileScopeBriefRead})
	relay.acceptRegistration(t, mobileTestListed(reader, bystander))
	// Writing the registration response does not mean the client consumed it.
	mobileTestWaitFor(t, "connected state", func() bool { return bridge.connectionStatus().RelayState == mobileRelayConnected })
	hubPublic, hubID := bridge.identity.Public, bridge.identity.HubID

	bridge.mu.Lock()
	before := bridge.devices[reader.deviceID].SendSeq
	bridge.mu.Unlock()
	n := mobileTestNotice(t)
	if err := bridge.publishMobileNotice(n, mobileTestRecords(t, bridge, reader, bystander)); err != nil {
		t.Fatal(err)
	}
	frame := relay.nextFrame(t, "out")
	var out mobileNoticeFrame
	if err := json.Unmarshal(frame.raw, &out); err != nil {
		t.Fatal(err)
	}
	if !out.Notify || out.Push != mobilePushMoment || out.NoticeID != n.ID || out.Kind != n.Kind ||
		out.Category != n.Category || out.Level != n.Level || out.Collapse != n.ID {
		t.Fatalf("out frame %+v", out)
	}
	if out.Env.Dev != reader.deviceID || out.Env.Dir != mobileDirH2D || out.Env.Kind != mobileKindEvent {
		t.Fatalf("envelope %+v", out.Env)
	}
	relay.expectNoFrame(t, "out", 150*time.Millisecond)

	var event mobileNoticeEvent
	if err := json.Unmarshal(reader.openFromHub(t, hubPublic, hubID, out.Env), &event); err != nil {
		t.Fatal(err)
	}
	if event.Event != mobileEventNotice || event.NoticeID != n.ID || event.Kind != n.Kind {
		t.Fatalf("event payload %+v", event)
	}
	opened, err := openMobileNotice(mobileTestNoticeKey(t, reader, hubPublic, hubID), hubID, reader.deviceID, out.NoticeID, out.CT)
	if err != nil {
		t.Fatalf("the phone could not open the sealed notice: %v", err)
	}
	if opened.Title != n.Title || opened.Body != n.Body || len(opened.Actions) != len(n.Actions) {
		t.Fatalf("sealed notice %+v", opened)
	}

	bridge.mu.Lock()
	after := bridge.devices[reader.deviceID].SendSeq
	bystanderSeq := bridge.devices[bystander.deviceID].SendSeq
	bridge.mu.Unlock()
	if after != before+1 || bystanderSeq != 0 {
		t.Fatalf("send counters: reader %d -> %d, bystander %d", before, after, bystanderSeq)
	}
}

func TestMobileNoticeWithoutAPushDeviceReportsItAndSendsNothing(t *testing.T) {
	bridge, relay := mobileTestHub(t)
	bystander := newMobileTestPhone(t)
	mobileTestAddDevice(t, bridge, bystander, []string{mobileScopeBriefRead})
	relay.acceptRegistration(t, mobileTestListed(bystander))
	if err := bridge.publishMobileNotice(mobileTestNotice(t), mobileTestRecords(t, bridge, bystander)); !errors.Is(err, errMobileNoticeNoRecipient) {
		t.Fatalf("no push device: %v", err)
	}
	relay.expectNoFrame(t, "out", 150*time.Millisecond)

	bad := mobileTestNotice(t)
	bad.Level = "critical"
	if err := bridge.publishMobileNotice(bad, mobileTestRecords(t, bridge, bystander)); !errors.Is(err, errMobileNoticeInvalid) {
		t.Fatalf("invalid level published: %v", err)
	}
}

func TestMobileNoticeReceiptsReachTheHookOnlyWhenValid(t *testing.T) {
	type receiptCall struct {
		device, notice, state, action string
	}
	calls := make(chan receiptCall, 8)
	saved := mobileNoticeReceiptHook
	mobileNoticeReceiptHook = func(device, notice, state, action string, _ time.Time) {
		calls <- receiptCall{device: device, notice: notice, state: state, action: action}
	}
	t.Cleanup(func() { mobileNoticeReceiptHook = saved })

	bridge, relay := mobileTestHub(t)
	reader := newMobileTestPhone(t)
	mobileTestAddDevice(t, bridge, reader, []string{mobileScopePush})
	conn := relay.acceptRegistration(t, mobileTestListed(reader))
	at := mobileTimestamp(time.Now())
	noticeID := uuid.NewString()
	receipt := func(overrides map[string]any) map[string]any {
		frame := map[string]any{"t": "receipt", "dev": reader.deviceID, "noticeId": noticeID, "state": "acted", "action": "approve", "at": at}
		for key, value := range overrides {
			if value == nil {
				delete(frame, key)
			} else {
				frame[key] = value
			}
		}
		return frame
	}
	refused := []map[string]any{
		receipt(map[string]any{"dev": uuid.NewString()}),
		receipt(map[string]any{"noticeId": "not-a-uuid"}),
		receipt(map[string]any{"state": "read"}),
		receipt(map[string]any{"action": ""}),
		receipt(map[string]any{"action": "Approve Now"}),
		receipt(map[string]any{"at": "2026-10-09 18:00:00"}),
		receipt(map[string]any{"text": "hello"}),
	}
	for _, frame := range refused {
		conn.send(t, frame)
	}
	conn.send(t, receipt(nil))

	// Frames are handled in order, so the valid receipt is the first call.
	select {
	case got := <-calls:
		if got.device != reader.deviceID || got.notice != noticeID || got.state != "acted" || got.action != "approve" {
			t.Fatalf("hook received %+v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the valid receipt never reached the hook")
	}
	select {
	case extra := <-calls:
		t.Fatalf("a refused receipt reached the hook: %+v", extra)
	case <-time.After(150 * time.Millisecond):
	}
}

// TestMobileNoticeThroughRelayBinary sends one notice through the real relay
// binary. The hub publishes it, the phone reads the event envelope from its poll,
// the phone posts receipts, and the hub's hook receives each new receipt once.
// It builds the relay, so it runs only with YANSU_MOBILE_E2E=1.
func TestMobileNoticeThroughRelayBinary(t *testing.T) {
	if os.Getenv("YANSU_MOBILE_E2E") != "1" {
		t.Skip("set YANSU_MOBILE_E2E=1 to build and run the relay notice check")
	}
	relayURL := startMobileE2ERelay(t)
	t.Setenv("YANSU_MOBILE_STATE_DIR", t.TempDir())
	t.Setenv("ISOFORM_RELAY_URL", "")
	app := &App{}
	status, err := app.EnableMobileConnection(relayURL)
	if err != nil || !status.Enabled {
		t.Fatalf("enable: %+v %v", status, err)
	}
	t.Cleanup(func() {
		if bridge := app.mobileBridgeForUse(); bridge != nil {
			bridge.Stop()
		}
	})
	mobileTestWaitFor(t, "a connected relay", func() bool {
		current, _ := app.GetMobileConnectionStatus()
		return current.RelayState == mobileRelayConnected
	})
	status, err = mobileE2EOpenInvitation(app)
	if err != nil || status.ConnectionCode == "" {
		t.Fatalf("generate: %+v %v", status, err)
	}
	phone := newMobileE2EPhone(t, relayURL, mobileTestDecodeInvitation(t, status.ConnectionCode))
	phone.claim(t)
	mobileTestWaitFor(t, "a pending claim on the hub", func() bool {
		current, _ := app.GetMobileConnectionStatus()
		return current.PendingPairing != nil
	})
	pending, _ := app.GetMobileConnectionStatus()
	if _, err = app.ConfirmMobilePairing(pending.PendingPairing.PairingID, true); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	phone.awaitApproval(t)

	bridge := app.mobileBridgeForUse()
	if bridge == nil {
		t.Fatal("the hub has no mobile bridge")
	}
	bridge.mu.Lock()
	record := bridge.devices[phone.deviceID]
	var records []mobileDeviceRecord
	if record != nil {
		records = []mobileDeviceRecord{*record}
	}
	bridge.mu.Unlock()
	if len(records) != 1 || !mobileHasScope(records[0].Scopes, mobileScopePush) {
		t.Fatalf("the approved phone has no push scope on the hub: %+v", records)
	}

	type receiptCall struct{ state, action string }
	received := make(chan receiptCall, 8)
	saved := mobileNoticeReceiptHook
	mobileNoticeReceiptHook = func(deviceID, noticeID, state, action string, _ time.Time) {
		if deviceID == phone.deviceID {
			received <- receiptCall{state: state, action: action}
		}
	}
	t.Cleanup(func() { mobileNoticeReceiptHook = saved })

	n := mobileTestNotice(t)
	if err = bridge.publishMobileNotice(n, records); err != nil {
		t.Fatalf("publish through the relay: %v", err)
	}
	// The relay stores the out frame's event envelope only when it accepts the
	// frame, so the phone reading it shows the relay took the notice.
	mobileTestWaitFor(t, "the notice event on the phone poll", func() bool {
		_, found := mobileTestPollNotice(t, phone, bridge.identity.HubID, n.ID)
		return found
	})

	at := mobileTimestamp(time.Now())
	postReceipt := func(state, action string) int {
		status, _ := phone.call(t, "POST", "/v1/notices/"+n.ID+"/receipts", map[string]any{"state": state, "action": action, "at": at}, phone.authed())
		return status
	}
	if code := postReceipt("opened", ""); code != http.StatusAccepted {
		t.Fatalf("opened receipt: %d", code)
	}
	mobileTestExpectReceipt(t, received, receiptCall{state: "opened"})
	if code := postReceipt("opened", ""); code != http.StatusAccepted {
		t.Fatalf("repeated receipt: %d", code)
	}
	select {
	case extra := <-received:
		t.Fatalf("a repeated receipt reached the hub again: %+v", extra)
	case <-time.After(300 * time.Millisecond):
	}
	if code := postReceipt("acted", "approve"); code != http.StatusAccepted {
		t.Fatalf("acted receipt: %d", code)
	}
	mobileTestExpectReceipt(t, received, receiptCall{state: "acted", action: "approve"})
}

// mobileTestPollNotice reads the phone's pending envelopes and reports whether
// the relay holds the notice event with the given notice id.
func mobileTestPollNotice(t *testing.T, phone *mobileE2EPhone, hubID, noticeID string) (mobileNoticeEvent, bool) {
	t.Helper()
	status, reply := phone.call(t, "GET", "/v1/responses?after=0&wait=0", nil, phone.authed())
	if status != http.StatusOK {
		t.Fatalf("responses: %d %s", status, reply.Error)
	}
	var data struct {
		Envelopes []mobileEnvelope `json:"envelopes"`
	}
	if err := json.Unmarshal(reply.Data, &data); err != nil {
		t.Fatal(err)
	}
	for _, env := range data.Envelopes {
		if env.Kind != mobileKindEvent {
			continue
		}
		plaintext, err := openMobileEnvelope(phone.keys.H2D, env, hubID, phone.deviceID, mobileDirH2D)
		if err != nil {
			t.Fatalf("the phone could not open an event: %v", err)
		}
		var event mobileNoticeEvent
		if json.Unmarshal(plaintext, &event) == nil && event.Event == mobileEventNotice && event.NoticeID == noticeID {
			return event, true
		}
	}
	return mobileNoticeEvent{}, false
}

// mobileTestExpectReceipt waits for the next receipt the hub's hook receives.
func mobileTestExpectReceipt[T comparable](t *testing.T, received <-chan T, want T) {
	t.Helper()
	select {
	case got := <-received:
		if got != want {
			t.Fatalf("hook received %+v, want %+v", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("no receipt reached the hub, want %+v", want)
	}
}
