package httpapi

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"isoform-relay/internal/hub"
	"isoform-relay/internal/notify"
	"isoform-relay/internal/push"
	"isoform-relay/internal/store"
	"isoform-relay/internal/wire"
)

// relay is a real store, hub manager and HTTP server for one test.
type relay struct {
	t      *testing.T
	dir    string
	store  *store.Store
	hubs   *hub.Manager
	notify *notify.Notifier
	h      *Handler
	srv    *httptest.Server
	mu     sync.Mutex
	clock  time.Time
	claims int
}

// newRelay starts a relay. pushEnvironment "" leaves APNs unconfigured.
func newRelay(t *testing.T, pushEnvironment string) *relay {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "relay.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	n := notify.New()
	hubs := hub.NewManager(st, n)
	handler := New(st, hubs, n, push.Configuration{Environment: pushEnvironment})
	r := &relay{t: t, dir: dir, store: st, hubs: hubs, notify: n, h: handler, clock: time.Now()}
	handler.Now = r.now
	r.srv = httptest.NewServer(handler)
	t.Cleanup(func() {
		hubs.CloseAll()
		r.srv.Close()
		_ = st.Close()
	})
	return r
}

// now is the relay's clock, which tests move forward to exercise expiry.
func (r *relay) now() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.clock
}

func (r *relay) advance(d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clock = r.clock.Add(d)
}

// response is one decoded {ok,data,error} reply.
type response struct {
	Status int
	Header http.Header
	OK     bool
	Data   json.RawMessage
	Error  string
	Raw    []byte
}

// call sends one request and checks the envelope and caching rules that apply
// to every reply.
func (r *relay) call(method, path string, header http.Header, body []byte) response {
	r.t.Helper()
	req, err := http.NewRequest(method, r.srv.URL+path, bytes.NewReader(body))
	if err != nil {
		r.t.Fatal(err)
	}
	for key, values := range header {
		req.Header[key] = values
	}
	if body != nil && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := r.srv.Client().Do(req)
	if err != nil {
		r.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		r.t.Fatal(err)
	}
	result := response{Status: resp.StatusCode, Header: resp.Header, Raw: raw}
	if resp.Header.Get("Cache-Control") != "no-store" {
		r.t.Fatalf("%s %s: response is cacheable", method, path)
	}
	var envelope struct {
		OK    bool            `json:"ok"`
		Data  json.RawMessage `json:"data"`
		Error string          `json:"error"`
	}
	if err = json.Unmarshal(raw, &envelope); err != nil {
		r.t.Fatalf("%s %s: reply is not an envelope: %s", method, path, raw)
	}
	if envelope.OK != (resp.StatusCode < 400) {
		r.t.Fatalf("%s %s: ok flag disagrees with status %d", method, path, resp.StatusCode)
	}
	result.OK, result.Data, result.Error = envelope.OK, envelope.Data, envelope.Error
	return result
}

func bearer(token string) http.Header {
	return http.Header{"Authorization": {"Bearer " + token}}
}

func (r *relay) getWith(path, token string) response {
	return r.call(http.MethodGet, path, bearer(token), nil)
}

func (r *relay) postWith(path, token string, value any) response {
	r.t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		r.t.Fatal(err)
	}
	header := bearer(token)
	return r.call(http.MethodPost, path, header, body)
}

func (r *relay) expectStatus(resp response, want int, context string) {
	r.t.Helper()
	if resp.Status != want {
		r.t.Fatalf("%s: status %d want %d (%s)", context, resp.Status, want, resp.Error)
	}
}

// randomBytes returns n cryptographically random bytes.
func randomBytes(t *testing.T, n int) []byte {
	t.Helper()
	raw := make([]byte, n)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

// randomToken is a 32-byte base64url value, as credentials and bearers are.
func randomToken(t *testing.T) string {
	return base64.RawURLEncoding.EncodeToString(randomBytes(t, 32))
}

// randomCT is an opaque ciphertext of a plausible size.
func randomCT(t *testing.T, plaintext int) string {
	return base64.RawURLEncoding.EncodeToString(randomBytes(t, plaintext+wire.AEADTag))
}

// claimToken derives the claim token from the invitation secret exactly as the
// protocol specifies: HMAC-SHA256(secret, "yansu-claim-v1|" + pairingId).
func claimToken(secret []byte, pairingID string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("yansu-claim-v1|" + pairingID))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func rfc3339(t time.Time) string { return wire.FormatTime(t) }

// hubClient is a hub's WebSocket. It buffers frames that arrive while a
// barrier waits, so ordering is never lost.
type hubClient struct {
	t       *testing.T
	hubID   string
	conn    *websocket.Conn
	pending []map[string]any
}

// dialHub opens the hub socket with the given credential and extra headers.
func (r *relay) dialHub(hubID, credential string, extra http.Header) (*websocket.Conn, *http.Response, error) {
	header := http.Header{}
	if hubID != "" {
		header.Set("X-Yansu-Hub", hubID)
	}
	if credential != "" {
		header.Set("Authorization", "Bearer "+credential)
	}
	for key, values := range extra {
		header[key] = values
	}
	url := "ws" + strings.TrimPrefix(r.srv.URL, "http") + "/v1/hub/ws"
	return websocket.DefaultDialer.Dial(url, header)
}

// connectHub registers a hub and waits for the registered frame.
func (r *relay) connectHub(hubID, credential string) *hubClient {
	r.t.Helper()
	conn, _, err := r.dialHub(hubID, credential, nil)
	if err != nil {
		r.t.Fatalf("hub dial: %v", err)
	}
	r.t.Cleanup(func() { _ = conn.Close() })
	hc := &hubClient{t: r.t, hubID: hubID, conn: conn}
	hc.send(map[string]any{"t": "register", "proto": 1, "hubName": "Test Mac"})
	if frame := hc.recv(); frame["t"] != "registered" {
		r.t.Fatalf("first relay frame %v, want registered", frame["t"])
	}
	return hc
}

func (hc *hubClient) send(frame any) {
	hc.t.Helper()
	if err := hc.conn.WriteJSON(frame); err != nil {
		hc.t.Fatal(err)
	}
}

// readFrame reads one frame from the socket, with a deadline.
func (hc *hubClient) readFrame(timeout time.Duration) (map[string]any, error) {
	if err := hc.conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return nil, err
	}
	var frame map[string]any
	if err := hc.conn.ReadJSON(&frame); err != nil {
		return nil, err
	}
	return frame, nil
}

// recv returns the next frame, buffered or new.
func (hc *hubClient) recv() map[string]any {
	hc.t.Helper()
	if len(hc.pending) > 0 {
		frame := hc.pending[0]
		hc.pending = hc.pending[1:]
		return frame
	}
	frame, err := hc.readFrame(5 * time.Second)
	if err != nil {
		hc.t.Fatalf("hub read: %v", err)
	}
	return frame
}

// recvType returns the next frame of the given type. Other frames stay buffered
// in arrival order. An error frame fails the test unless it is the one wanted.
func (hc *hubClient) recvType(kind string) map[string]any {
	hc.t.Helper()
	for i, frame := range hc.pending {
		if frame["t"] == kind {
			hc.pending = append(hc.pending[:i], hc.pending[i+1:]...)
			return frame
		}
	}
	for {
		frame, err := hc.readFrame(5 * time.Second)
		if err != nil {
			hc.t.Fatalf("hub read waiting for %s: %v", kind, err)
		}
		if frame["t"] == kind {
			return frame
		}
		if frame["t"] == "error" {
			hc.t.Fatalf("relay refused a frame: %v", frame)
		}
		hc.pending = append(hc.pending, frame)
	}
}

// barrier proves every earlier frame was processed: the relay answers an
// invalid heartbeat with an error, and frames are handled in order.
func (hc *hubClient) barrier() {
	hc.t.Helper()
	hc.send(map[string]any{"t": "heartbeat", "barrier": true})
	for {
		frame, err := hc.readFrame(5 * time.Second)
		if err != nil {
			hc.t.Fatalf("barrier read: %v", err)
		}
		if frame["t"] == "error" && frame["ref"] == "heartbeat" {
			return
		}
		hc.pending = append(hc.pending, frame)
	}
}

// invitation is an opened pairing whose secret the device holds.
type invitation struct {
	pairingID  string
	secret     []byte
	claimToken string
}

// openPairing sends pairing_open and waits until the relay has stored it.
func (r *relay) openPairing(hc *hubClient) invitation {
	r.t.Helper()
	pairingID := uuid.NewString()
	secret := randomBytes(r.t, 32)
	token := claimToken(secret, pairingID)
	hc.send(map[string]any{"t": "pairing_open", "pairing": pairingID, "claimTokenHash": wire.Digest(token), "expiresAt": rfc3339(time.Now().Add(10 * time.Minute))})
	hc.barrier()
	return invitation{pairingID: pairingID, secret: secret, claimToken: token}
}

// device is an approved phone.
type device struct {
	id         string
	bearer     string
	hubID      string
	pairingID  string
	claimToken string
	approvalCT string
}

// claimFrom posts a device's claim. Each claim comes from its own forwarded
// address (the test peer is loopback, which the relay trusts for that header),
// so the per-client limit tracks the device, not the test process.
func (r *relay) claimFrom(inv invitation, hubID, deviceID, claimToken, devicePub, ct string) response {
	r.t.Helper()
	body, err := json.Marshal(map[string]any{"hub": hubID, "claimToken": claimToken, "deviceId": deviceID, "devicePub": devicePub, "ct": ct})
	if err != nil {
		r.t.Fatal(err)
	}
	r.mu.Lock()
	r.claims++
	address := fmt.Sprintf("10.20.%d.%d", r.claims/250, r.claims%250+1)
	r.mu.Unlock()
	header := http.Header{"X-Forwarded-For": {address}}
	return r.call(http.MethodPost, "/v1/pairings/"+inv.pairingID+"/claim", header, body)
}

// pairDevice runs the full pairing flow with the given scopes.
func (r *relay) pairDevice(hc *hubClient, scopes []string) device {
	r.t.Helper()
	inv := r.openPairing(hc)
	deviceID := uuid.NewString()
	bearerToken := randomToken(r.t)
	devicePub := randomToken(r.t)
	claim := r.claimFrom(inv, hc.hubID, deviceID, inv.claimToken, devicePub, randomCT(r.t, 200))
	r.expectStatus(claim, http.StatusAccepted, "claim")
	frame := hc.recvType("pairing_claim")
	if frame["pairing"] != inv.pairingID || frame["deviceId"] != deviceID || frame["devicePub"] != devicePub {
		r.t.Fatalf("pairing_claim frame %v", frame)
	}
	approvalCT := randomCT(r.t, 200)
	hc.send(map[string]any{"t": "pairing_approve", "pairing": inv.pairingID, "deviceId": deviceID, "bearerHash": wire.Digest(bearerToken), "scopes": scopes, "expiresAt": rfc3339(time.Now().Add(90 * 24 * time.Hour)), "ct": approvalCT})
	poll := r.pollPairing(inv)
	if poll.State != "approved" || poll.ApprovalCT != approvalCT {
		r.t.Fatalf("approval poll %+v", poll)
	}
	return device{id: deviceID, bearer: bearerToken, hubID: hc.hubID, pairingID: inv.pairingID, claimToken: inv.claimToken, approvalCT: approvalCT}
}

type pollResult struct {
	State      string
	ApprovalCT string
}

// pollPairing polls the pairing with its claim token and decodes the state.
func (r *relay) pollPairing(inv invitation) pollResult {
	r.t.Helper()
	resp := r.call(http.MethodGet, "/v1/pairings/"+inv.pairingID, http.Header{"X-Yansu-Claim": {inv.claimToken}}, nil)
	r.expectStatus(resp, http.StatusOK, "pairing poll")
	var data struct {
		State    string `json:"state"`
		Approval *struct {
			CT string `json:"ct"`
		} `json:"approval"`
	}
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		r.t.Fatal(err)
	}
	result := pollResult{State: data.State}
	if data.Approval != nil {
		result.ApprovalCT = data.Approval.CT
	}
	return result
}

// makeEnvelope builds a valid protocol envelope.
func makeEnvelope(t *testing.T, hubID, deviceID, dir string, seq uint64, kind string, plaintext int) wire.Envelope {
	t.Helper()
	return wire.Envelope{V: 1, Hub: hubID, Dev: deviceID, Dir: dir, Seq: seq, Kind: kind, CT: randomCT(t, plaintext)}
}

func envelopeJSON(t *testing.T, env wire.Envelope) []byte {
	t.Helper()
	raw, err := env.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// frameWithEnvelope builds a hub frame that carries an envelope.
func frameWithEnvelope(t *testing.T, kind string, env wire.Envelope) map[string]any {
	t.Helper()
	return map[string]any{"t": kind, "env": json.RawMessage(envelopeJSON(t, env))}
}

// requestEnvelope returns a device request envelope.
func requestEnvelope(t *testing.T, dev device, seq uint64) wire.Envelope {
	return makeEnvelope(t, dev.hubID, dev.id, "d2h", seq, "req", 80)
}

// rawGet performs a GET without test helpers, so it can run in a goroutine.
func rawGet(url, token string, header http.Header) (int, []byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return 0, nil, err
	}
	for key, values := range header {
		req.Header[key] = values
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := (&http.Client{Timeout: 40 * time.Second}).Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	return resp.StatusCode, raw, err
}

// putPush registers an APNs token for a device.
func (r *relay) putPush(dev device, token, environment string) response {
	r.t.Helper()
	body, err := json.Marshal(map[string]string{"deviceToken": token, "environment": environment})
	if err != nil {
		r.t.Fatal(err)
	}
	return r.call(http.MethodPut, "/v1/push/token", bearer(dev.bearer), body)
}

func (r *relay) putPushWithGrant(dev device, token, environment, grant string) response {
	r.t.Helper()
	body, err := json.Marshal(map[string]string{"deviceToken": token, "environment": environment, "gatewayGrant": grant})
	if err != nil {
		r.t.Fatal(err)
	}
	return r.call(http.MethodPut, "/v1/push/token", bearer(dev.bearer), body)
}

// presenceOf returns the device's view of its hub.
func (r *relay) presenceOf(dev device) (bool, bool) {
	r.t.Helper()
	resp := r.getWith("/v1/presence", dev.bearer)
	r.expectStatus(resp, http.StatusOK, "presence")
	var data struct {
		HubOnline  bool    `json:"hubOnline"`
		LastSeenAt *string `json:"lastSeenAt"`
	}
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		r.t.Fatal(err)
	}
	return data.HubOnline, data.LastSeenAt != nil
}

// waitHubOnline polls presence until the hub's connection state matches want.
func (r *relay) waitHubOnline(dev device, want bool) {
	r.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		online, _ := r.presenceOf(dev)
		if online == want {
			return
		}
		if time.Now().After(deadline) {
			r.t.Fatalf("hub online=%t never reached %t", online, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// briefFrame builds a brief frame for a hub. The envelope must belong to the hub.
func briefFrame(t *testing.T, hubID, deviceID string, seq uint64, notify bool) map[string]any {
	t.Helper()
	env := makeEnvelope(t, hubID, deviceID, "h2d", seq, "brief", 500)
	return map[string]any{"t": "brief", "env": json.RawMessage(envelopeJSON(t, env)), "asOf": rfc3339(time.Now()), "notify": notify}
}

func hexDigest(s string) string { return wire.Digest(s) }

func containsText(body []byte, text string) bool { return bytes.Contains(body, []byte(text)) }

// TestMain keeps the relay's routine connection logs out of test output.
func TestMain(m *testing.M) {
	log.SetOutput(io.Discard)
	os.Exit(m.Run())
}
