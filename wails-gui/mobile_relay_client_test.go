package main

import (
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// mobileTestRelay is a fake relay: a real HTTP server that speaks the hub side of
// the WebSocket protocol, so the client, bridge and crypto all run for real.
type mobileTestRelay struct {
	server     *httptest.Server
	credential string
	hubID      string
	conns      chan *mobileTestRelayConn
	frames     chan mobileTestFrame

	mu     sync.Mutex
	refuse bool
	// learn accepts the first hub that connects and pins its credential, as the
	// relay does when a hub id is seen for the first time.
	learn             bool
	attachmentHandler http.Handler
}

type mobileTestRelayConn struct {
	conn    *websocket.Conn
	writeMu sync.Mutex
	// blackhole models a stale socket after sleep: writes succeed locally but
	// the old relay session no longer handles frames or answers heartbeats.
	blackhole atomic.Bool
}

type mobileTestFrame struct {
	conn *mobileTestRelayConn
	kind string
	raw  []byte
}

// newMobileTestRelay pins the hub identity. An empty credential learns it from the
// first connection instead.
func newMobileTestRelay(t *testing.T, credential, hubID string) *mobileTestRelay {
	t.Helper()
	relay := &mobileTestRelay{credential: credential, hubID: hubID, learn: credential == "",
		conns: make(chan *mobileTestRelayConn, 16), frames: make(chan mobileTestFrame, 512)}
	relay.server = httptest.NewServer(http.HandlerFunc(relay.serve))
	t.Cleanup(relay.server.Close)
	return relay
}

func (relay *mobileTestRelay) url() string { return relay.server.URL }

func (relay *mobileTestRelay) setRefuse(value bool) {
	relay.mu.Lock()
	relay.refuse = value
	relay.mu.Unlock()
}

func (relay *mobileTestRelay) serve(w http.ResponseWriter, r *http.Request) {
	authorization, hubHeader := r.Header.Get("Authorization"), r.Header.Get("X-Yansu-Hub")
	relay.mu.Lock()
	if relay.learn {
		relay.learn = false
		relay.credential, relay.hubID = strings.TrimPrefix(authorization, "Bearer "), hubHeader
	}
	authorized := authorization == "Bearer "+relay.credential && hubHeader == relay.hubID
	refuse := relay.refuse
	attachmentHandler := relay.attachmentHandler
	relay.mu.Unlock()
	if !authorized {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if refuse {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	if r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/v1/hub/attachments/") && attachmentHandler != nil {
		attachmentHandler.ServeHTTP(w, r)
		return
	}
	upgrader := websocket.Upgrader{}
	raw, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer raw.Close()
	conn := &mobileTestRelayConn{conn: raw}
	relay.conns <- conn
	for {
		_, message, err := raw.ReadMessage()
		if err != nil {
			return
		}
		var header struct {
			T string `json:"t"`
		}
		if json.Unmarshal(message, &header) != nil {
			continue
		}
		if conn.blackhole.Load() {
			continue
		}
		relay.frames <- mobileTestFrame{conn: conn, kind: header.T, raw: message}
	}
}

func (relay *mobileTestRelay) nextConn(t *testing.T) *mobileTestRelayConn {
	t.Helper()
	select {
	case conn := <-relay.conns:
		return conn
	case <-time.After(5 * time.Second):
		t.Fatal("the hub did not connect to the relay")
		return nil
	}
}

// nextFrame returns the next frame of kind and skips other traffic such as heartbeats.
func (relay *mobileTestRelay) nextFrame(t *testing.T, kind string) mobileTestFrame {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case frame := <-relay.frames:
			if frame.kind == kind {
				return frame
			}
		case <-deadline:
			t.Fatalf("the hub did not send a %s frame", kind)
		}
	}
}

func (relay *mobileTestRelay) expectNoFrame(t *testing.T, kind string, window time.Duration) {
	t.Helper()
	timer := time.NewTimer(window)
	defer timer.Stop()
	for {
		select {
		case frame := <-relay.frames:
			if frame.kind == kind {
				t.Fatalf("unexpected %s frame", kind)
			}
		case <-timer.C:
			return
		}
	}
}

// mobileTestListing is one entry of the relay's registered device list. A nil
// LastSeenAt is sent as null, which the relay does for a device never seen.
type mobileTestListing struct {
	DeviceID   string  `json:"deviceId"`
	LastSeenAt *string `json:"lastSeenAt"`
}

// mobileTestListed lists the given phones as the relay would after pairing them.
func mobileTestListed(phones ...*mobileTestPhone) []mobileTestListing {
	listing := []mobileTestListing{}
	for _, phone := range phones {
		listing = append(listing, mobileTestListing{DeviceID: phone.deviceID})
	}
	return listing
}

// acceptRegistration answers the hub's register frame with registered and the
// given device list. A nil list is an empty one.
func (relay *mobileTestRelay) acceptRegistration(t *testing.T, devices []mobileTestListing) *mobileTestRelayConn {
	t.Helper()
	if devices == nil {
		devices = []mobileTestListing{}
	}
	return relay.acceptRegistrationFrame(t, map[string]any{"serverTime": mobileTimestamp(time.Now()), "devices": devices})
}

// acceptRegistrationFrame answers the register frame with registered and the given fields.
func (relay *mobileTestRelay) acceptRegistrationFrame(t *testing.T, fields map[string]any) *mobileTestRelayConn {
	t.Helper()
	conn := relay.nextConn(t)
	frame := relay.nextFrame(t, "register")
	var register struct {
		Proto   int    `json:"proto"`
		HubName string `json:"hubName"`
	}
	if err := json.Unmarshal(frame.raw, &register); err != nil || register.Proto != 1 || register.HubName == "" {
		t.Fatalf("malformed register frame: %s", frame.raw)
	}
	if frame.conn != conn {
		t.Fatal("the register frame came from another connection")
	}
	reply := map[string]any{"t": "registered"}
	for key, value := range fields {
		reply[key] = value
	}
	conn.send(t, reply)
	return conn
}

func (c *mobileTestRelayConn) send(t *testing.T, frame any) {
	t.Helper()
	raw, err := json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := c.conn.WriteMessage(websocket.TextMessage, raw); err != nil {
		t.Fatal(err)
	}
}

// mobileTestPhone simulates an iPhone with the production crypto on the device side.
type mobileTestPhone struct {
	deviceID   string
	private    *ecdh.PrivateKey
	bearerHash string
}

func newMobileTestPhone(t *testing.T) *mobileTestPhone {
	t.Helper()
	private, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	bearer := make([]byte, 32)
	if _, err := rand.Read(bearer); err != nil {
		t.Fatal(err)
	}
	// PROTOCOL.md section 6: digests are SHA-256 of the exact base64url text.
	digest := sha256.Sum256([]byte(base64.RawURLEncoding.EncodeToString(bearer)))
	return &mobileTestPhone{deviceID: uuid.NewString(), private: private, bearerHash: hex.EncodeToString(digest[:])}
}

func (p *mobileTestPhone) publicKeyText() string {
	return base64.RawURLEncoding.EncodeToString(p.private.PublicKey().Bytes())
}

func (p *mobileTestPhone) sessionKeys(t *testing.T, hubPublic *ecdh.PublicKey, hubID string) mobileSessionKeys {
	t.Helper()
	keys, err := mobileDeriveSessionKeys(p.private, hubPublic, hubID, p.deviceID)
	if err != nil {
		t.Fatal(err)
	}
	return keys
}

func (p *mobileTestPhone) sealRequest(t *testing.T, hubPublic *ecdh.PublicKey, hubID string, seq uint64, plaintext []byte) mobileEnvelope {
	t.Helper()
	env, err := sealMobileEnvelope(p.sessionKeys(t, hubPublic, hubID).D2H, hubID, p.deviceID, mobileDirD2H, seq, mobileKindRequest, plaintext)
	if err != nil {
		t.Fatal(err)
	}
	return env
}

func (p *mobileTestPhone) openFromHub(t *testing.T, hubPublic *ecdh.PublicKey, hubID string, env mobileEnvelope) []byte {
	t.Helper()
	plaintext, err := openMobileEnvelope(p.sessionKeys(t, hubPublic, hubID).H2D, env, hubID, p.deviceID, mobileDirH2D)
	if err != nil {
		t.Fatalf("the phone could not open a hub envelope: %v", err)
	}
	return plaintext
}

// claimFrame is the pairing_claim the relay forwards for a claim the phone sealed
// under kPair. The override may alter the plaintext to test validation.
func (p *mobileTestPhone) claimFrame(t *testing.T, invitation mobileInvitation, override func(*mobileClaimPlaintext)) (map[string]any, []byte) {
	t.Helper()
	hubPublic, err := mobileParsePublicKey(invitation.HubPub)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := base64.RawURLEncoding.DecodeString(invitation.Secret)
	if err != nil {
		t.Fatal(err)
	}
	kPair, err := mobilePairKey(p.private, hubPublic, secret, invitation.Hub, invitation.Pairing)
	if err != nil {
		t.Fatal(err)
	}
	claim := mobileClaimPlaintext{
		DeviceID: p.deviceID, DeviceName: "Test iPhone", DevicePub: p.publicKeyText(), BearerHash: p.bearerHash,
		Platform: "ios", AppVersion: "1.0 (12)", PushEnvironment: "development",
		Scopes: []string{mobileScopeBriefRead, mobileScopeBriefRefresh, mobileScopePresenceRead, mobileScopePush},
	}
	if override != nil {
		override(&claim)
	}
	plaintext, err := json.Marshal(claim)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := sealMobilePairingMessage(kPair, mobilePairingClaimNonceByte, mobileClaimAAD(invitation.Hub, invitation.Pairing), plaintext)
	if err != nil {
		t.Fatal(err)
	}
	frame := map[string]any{"t": "pairing_claim", "pairing": invitation.Pairing, "deviceId": p.deviceID,
		"devicePub": p.publicKeyText(), "ct": base64.RawURLEncoding.EncodeToString(ciphertext)}
	return frame, kPair
}

// mobileTestTimes shortens the relay clocks for one test.
func mobileTestTimes(t *testing.T, mutate func(*mobileRelayTiming)) {
	t.Helper()
	saved := mobileRelayTimes
	mutate(&mobileRelayTimes)
	t.Cleanup(func() { mobileRelayTimes = saved })
}

// mobileTestHub starts a hub identity and a bridge connected to a fake relay.
func mobileTestHub(t *testing.T) (*mobileBridge, *mobileTestRelay) {
	t.Helper()
	dir := t.TempDir()
	identity, err := createMobileHubIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	relay := newMobileTestRelay(t, identity.Credential, identity.HubID)
	bridge, err := newMobileBridge(&App{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bridge.Stop)
	bridge.mu.Lock()
	bridge.identity = &identity
	bridge.mu.Unlock()
	if err := bridge.enableRelay(relay.url(), false); err != nil {
		t.Fatal(err)
	}
	return bridge, relay
}

// mobileTestAddDevice records a paired device without the pairing flow.
func mobileTestAddDevice(t *testing.T, bridge *mobileBridge, phone *mobileTestPhone, scopes []string) {
	t.Helper()
	now := time.Now()
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	bridge.devices[phone.deviceID] = &mobileDeviceRecord{
		MobileDeviceView: MobileDeviceView{DeviceID: phone.deviceID, DeviceName: "Test iPhone",
			CreatedAt: mobileTimestamp(now), ExpiresAt: mobileTimestamp(now.Add(mobileDeviceLifetime)), Scopes: scopes},
		DevicePub: phone.publicKeyText(),
	}
	if err := bridge.persistDevicesLocked(); err != nil {
		t.Fatal(err)
	}
}

func mobileTestWaitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestMobileRelayBackoffDoublesToTheCap(t *testing.T) {
	for _, test := range []struct {
		name  string
		limit time.Duration
		want  []time.Duration
	}{
		{"healthy", mobileRelayTimes.HealthyBackoff, []time.Duration{4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second}},
		{"unreachable", mobileRelayTimes.MaxBackoff, []time.Duration{4 * time.Second, 8 * time.Second, 16 * time.Second, 32 * time.Second, 64 * time.Second, 128 * time.Second, 256 * time.Second, 5 * time.Minute, 5 * time.Minute}},
	} {
		t.Run(test.name, func(t *testing.T) {
			current := mobileRelayTimes.InitialBackoff
			for index, expected := range test.want {
				current = mobileRelayNextBackoff(current, test.limit)
				if current != expected {
					t.Fatalf("step %d: backoff %s, want %s", index, current, expected)
				}
			}
		})
	}
}

func TestMobileRelayRejectsInsecureRelayAddresses(t *testing.T) {
	identity := mobileHubIdentity{HubID: mobileTestHubID, Credential: "x"}
	for _, origin := range []string{"http://relay.example", "https://relay.example/path"} {
		if _, err := newMobileRelayClient(origin, identity, "Mac", nil); err == nil {
			t.Fatalf("relay %s was accepted", origin)
		}
	}
	if client, err := newMobileRelayClient("http://localhost:8787", identity, "Mac", nil); err != nil || client.wsURL != "ws://localhost:8787/v1/hub/ws" {
		t.Fatalf("development relay: %v", err)
	}
}

func TestMobileRelayRegistersAndHeartbeats(t *testing.T) {
	mobileTestTimes(t, func(timing *mobileRelayTiming) { timing.Heartbeat = 20 * time.Millisecond })
	bridge, relay := mobileTestHub(t)
	relay.acceptRegistration(t, nil)
	mobileTestWaitFor(t, "connected state", func() bool { return bridge.connectionStatus().RelayState == mobileRelayConnected })
	relay.nextFrame(t, "heartbeat")
	if status := bridge.connectionStatus(); status.RelayLastSeen == "" {
		t.Fatal("the last successful registration was not reported")
	}
}

func TestMobileRelayReplacedGoesDormantThenReconnects(t *testing.T) {
	const dormancy = 400 * time.Millisecond
	mobileTestTimes(t, func(timing *mobileRelayTiming) { timing.Dormancy = dormancy })
	_, relay := mobileTestHub(t)
	first := relay.acceptRegistration(t, nil)
	first.send(t, map[string]any{"t": "replaced"})
	started := time.Now()
	select {
	case <-relay.conns:
		t.Fatal("the hub reconnected while it was dormant")
	case <-time.After(dormancy / 2):
	}
	relay.nextConn(t)
	if elapsed := time.Since(started); elapsed < dormancy-50*time.Millisecond {
		t.Fatalf("the hub reconnected after %s, before the dormancy ended", elapsed)
	}
}

func TestMobileRelayReconnectsAfterADrop(t *testing.T) {
	mobileTestTimes(t, func(timing *mobileRelayTiming) {
		timing.InitialBackoff = 20 * time.Millisecond
		timing.MaxBackoff = 40 * time.Millisecond
	})
	bridge, relay := mobileTestHub(t)
	first := relay.acceptRegistration(t, nil)
	first.conn.Close()
	mobileTestWaitFor(t, "offline state", func() bool { return bridge.connectionStatus().RelayState != mobileRelayConnected })
	relay.acceptRegistration(t, nil)
	mobileTestWaitFor(t, "reconnected state", func() bool { return bridge.connectionStatus().RelayState == mobileRelayConnected })
}

func TestMobileRelayRequestRoundTripAckAndReplay(t *testing.T) {
	bridge, relay := mobileTestHub(t)
	phone := newMobileTestPhone(t)
	mobileTestAddDevice(t, bridge, phone, []string{mobileScopeBriefRead})
	conn := relay.acceptRegistration(t, mobileTestListed(phone))
	hubPublic := bridge.identity.Public
	request := []byte(`{"id":"` + mobileTestOtherID + `","method":"GET","path":"/status","query":"","body":""}`)
	conn.send(t, map[string]any{"t": "in", "env": phone.sealRequest(t, hubPublic, bridge.identity.HubID, 1, request)})

	out := relay.nextFrame(t, "out")
	var frame struct {
		Env mobileEnvelope `json:"env"`
	}
	if err := json.Unmarshal(out.raw, &frame); err != nil {
		t.Fatal(err)
	}
	if frame.Env.Dir != mobileDirH2D || frame.Env.Kind != mobileKindResponse || frame.Env.Dev != phone.deviceID {
		t.Fatalf("unexpected response envelope header: %+v", frame.Env)
	}
	var response mobileResponsePlaintext
	if err := json.Unmarshal(phone.openFromHub(t, hubPublic, bridge.identity.HubID, frame.Env), &response); err != nil {
		t.Fatal(err)
	}
	if response.ID != mobileTestOtherID || response.Status != http.StatusOK || len(response.Body) == 0 {
		t.Fatalf("unexpected response: %+v", response)
	}
	ack := relay.nextFrame(t, "ack")
	var acked struct {
		Dev  string `json:"dev"`
		UpTo uint64 `json:"upTo"`
	}
	if err := json.Unmarshal(ack.raw, &acked); err != nil || acked.Dev != phone.deviceID || acked.UpTo != 1 {
		t.Fatalf("ack must name the device and sequence: %s", ack.raw)
	}

	// A redelivered request is acknowledged again but is not served a second time.
	conn.send(t, map[string]any{"t": "in", "env": phone.sealRequest(t, hubPublic, bridge.identity.HubID, 1, request)})
	relay.nextFrame(t, "ack")
	relay.expectNoFrame(t, "out", 300*time.Millisecond)
}

func TestMobileRelayBriefPublishAndPendingFlushAfterReconnect(t *testing.T) {
	mobileTestTimes(t, func(timing *mobileRelayTiming) {
		timing.InitialBackoff = 20 * time.Millisecond
		timing.MaxBackoff = 40 * time.Millisecond
	})
	bridge, relay := mobileTestHub(t)
	phone := newMobileTestPhone(t)
	mobileTestAddDevice(t, bridge, phone, []string{mobileScopeBriefRead})
	conn := relay.acceptRegistration(t, mobileTestListed(phone))
	hubPublic := bridge.identity.Public

	first := MobileBriefV1{Version: 1, BriefID: "2026-10-09-morning", Slot: "morning", AsOf: "2026-10-09T07:00:12Z", HubName: "Mac"}
	if err := bridge.publishBrief(first, true); err != nil {
		t.Fatal(err)
	}
	frame := relay.nextFrame(t, "brief")
	var briefFrame mobileBriefFrame
	if err := json.Unmarshal(frame.raw, &briefFrame); err != nil {
		t.Fatal(err)
	}
	if !briefFrame.Notify || briefFrame.AsOf != first.AsOf || briefFrame.Env.Kind != mobileKindBrief {
		t.Fatalf("unexpected brief frame: %+v", briefFrame)
	}
	var received MobileBriefV1
	if err := json.Unmarshal(phone.openFromHub(t, hubPublic, bridge.identity.HubID, briefFrame.Env), &received); err != nil || received.BriefID != first.BriefID {
		t.Fatalf("the phone did not receive the brief: %v", err)
	}

	// While the relay is down, only the newest brief per device is kept.
	relay.setRefuse(true)
	conn.conn.Close()
	mobileTestWaitFor(t, "offline state", func() bool { return bridge.connectionStatus().RelayState != mobileRelayConnected })
	older := MobileBriefV1{Version: 1, BriefID: "2026-10-09-on_request-101000", Slot: "on_request", AsOf: "2026-10-09T10:10:00Z"}
	newest := MobileBriefV1{Version: 1, BriefID: "2026-10-09-evening", Slot: "evening", AsOf: "2026-10-09T18:00:03Z"}
	if err := bridge.publishBrief(older, false); err != nil {
		t.Fatal(err)
	}
	if err := bridge.publishBrief(newest, true); err != nil {
		t.Fatal(err)
	}
	relay.setRefuse(false)
	relay.acceptRegistration(t, mobileTestListed(phone))
	pending := relay.nextFrame(t, "brief")
	var replayed mobileBriefFrame
	if err := json.Unmarshal(pending.raw, &replayed); err != nil {
		t.Fatal(err)
	}
	var latest MobileBriefV1
	if err := json.Unmarshal(phone.openFromHub(t, hubPublic, bridge.identity.HubID, replayed.Env), &latest); err != nil || latest.BriefID != newest.BriefID {
		t.Fatalf("the flush did not send the newest brief: %v", err)
	}
	relay.expectNoFrame(t, "brief", 200*time.Millisecond)
}

func TestMobileRelayReconcilesWithTheRelayDeviceList(t *testing.T) {
	mobileTestTimes(t, func(timing *mobileRelayTiming) {
		timing.InitialBackoff = 20 * time.Millisecond
		timing.MaxBackoff = 40 * time.Millisecond
	})
	bridge, relay := mobileTestHub(t)
	kept, neverSeen, removed := newMobileTestPhone(t), newMobileTestPhone(t), newMobileTestPhone(t)
	for _, phone := range []*mobileTestPhone{kept, neverSeen, removed} {
		mobileTestAddDevice(t, bridge, phone, []string{mobileScopeBriefRead})
	}
	conn := relay.acceptRegistration(t, mobileTestListed(kept, neverSeen, removed))
	// A brief queued for a device that is removed must not outlive its record.
	bridge.mu.Lock()
	bridge.pendingBriefs[removed.deviceID] = mobileBriefFrame{T: "brief", Env: mobileEnvelope{Dev: removed.deviceID}}
	bridge.mu.Unlock()

	seen := mobileTimestamp(time.Now().Add(-time.Hour))
	conn.conn.Close()
	mobileTestWaitFor(t, "an offline state", func() bool { return bridge.connectionStatus().RelayState != mobileRelayConnected })
	relay.acceptRegistration(t, []mobileTestListing{{DeviceID: kept.deviceID, LastSeenAt: &seen}, {DeviceID: neverSeen.deviceID}})
	mobileTestWaitFor(t, "the unlisted device to be dropped", func() bool {
		bridge.mu.Lock()
		defer bridge.mu.Unlock()
		return bridge.devices[removed.deviceID] == nil
	})

	status := bridge.connectionStatus()
	byID := map[string]MobileDeviceView{}
	for _, device := range status.Devices {
		byID[device.DeviceID] = device
	}
	if len(byID) != 2 || byID[kept.deviceID].LastSeenAt != seen || byID[neverSeen.deviceID].LastSeenAt != "" {
		t.Fatalf("unexpected devices after reconciliation: %+v", status.Devices)
	}
	bridge.mu.Lock()
	_, briefQueued := bridge.pendingBriefs[removed.deviceID]
	bridge.mu.Unlock()
	if briefQueued {
		t.Fatal("a brief for a removed device stayed queued")
	}
	// The removal is saved, so a restart does not bring the device back.
	devices, _, err := loadMobileDeviceStore(bridge.stateDir, time.Now())
	if err != nil || devices[removed.deviceID] != nil || devices[kept.deviceID] == nil {
		t.Fatalf("the reconciled device list was not saved: %v", err)
	}
}

func TestMobileRelayRejectedFramesKeepTheConnection(t *testing.T) {
	bridge, relay := mobileTestHub(t)
	conn := relay.acceptRegistration(t, nil)
	conn.send(t, map[string]any{"t": "error", "ref": "pairing_approve", "error": "the pairing is not claimed"})
	select {
	case <-relay.conns:
		t.Fatal("the hub reconnected after a rejected frame")
	case <-time.After(300 * time.Millisecond):
	}
	if state := bridge.connectionStatus().RelayState; state != mobileRelayConnected {
		t.Fatalf("a rejected frame changed the relay state to %s", state)
	}
}

func TestMobileRelayRegistrationDeadline(t *testing.T) {
	mobileTestTimes(t, func(timing *mobileRelayTiming) {
		timing.RegisterTimeout = 100 * time.Millisecond
		timing.InitialBackoff = 20 * time.Millisecond
		timing.MaxBackoff = 40 * time.Millisecond
	})
	_, relay := mobileTestHub(t)
	first := relay.nextConn(t)
	relay.nextFrame(t, "register")
	// The relay never answers, so the hub drops the socket and tries again.
	if second := relay.nextConn(t); second == first {
		t.Fatal("the hub kept an unanswered registration")
	}
}

func TestMobileRelayRegisteredWithoutDeviceListKeepsRecords(t *testing.T) {
	mobileTestTimes(t, func(timing *mobileRelayTiming) {
		timing.InitialBackoff = 20 * time.Millisecond
		timing.MaxBackoff = 40 * time.Millisecond
	})
	bridge, relay := mobileTestHub(t)
	phone := newMobileTestPhone(t)
	mobileTestAddDevice(t, bridge, phone, []string{mobileScopeBriefRead})
	conn := relay.acceptRegistration(t, mobileTestListed(phone))
	conn.conn.Close()
	relay.acceptRegistrationFrame(t, map[string]any{"serverTime": mobileTimestamp(time.Now())})
	mobileTestWaitFor(t, "a reconnected relay", func() bool { return bridge.connectionStatus().RelayState == mobileRelayConnected })
	bridge.mu.Lock()
	kept := bridge.devices[phone.deviceID] != nil
	bridge.mu.Unlock()
	if !kept {
		t.Fatal("a registration without a device list removed a paired iPhone")
	}
}
