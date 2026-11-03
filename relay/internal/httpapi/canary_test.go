package httpapi

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"isoform-relay/internal/push"
)

// syncBuffer collects log output written from relay goroutines.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.buf.Bytes()...)
}

// sealText returns AES-256-GCM ciphertext of plaintext. The key is discarded, so
// nothing can decrypt it. The relay is never given a key either: this stands in
// for the protocol's ciphertext, whose plaintext must never reach storage.
func sealText(t *testing.T, plaintext string) string {
	t.Helper()
	block, err := aes.NewCipher(randomBytes(t, 32))
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	nonce := randomBytes(t, gcm.NonceSize())
	return base64.RawURLEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte(plaintext), nil))
}

// TestCanaryPlaintextNeverReachesDiskOrLogs sends a recognizable plaintext
// through every ciphertext field and checks that it is absent from the database,
// its WAL and the captured logs. The ciphertext itself must be stored.
func TestCanaryPlaintextNeverReachesDiskOrLogs(t *testing.T) {
	logs := &syncBuffer{}
	previous := log.Writer()
	log.SetOutput(logs)
	t.Cleanup(func() { log.SetOutput(previous) })

	marker := "CANARY-" + hex.EncodeToString(randomBytes(t, 12))
	r := newRelay(t, "development")
	hubID := uuid.NewString()
	hc := r.connectHub(hubID, randomToken(t))
	inv := r.openPairing(hc)
	deviceID := uuid.NewString()
	bearerToken := randomToken(t)
	claimCT := sealText(t, marker+" pairing claim")
	r.expectStatus(r.claimFrom(inv, hubID, deviceID, inv.claimToken, randomToken(t), claimCT), 202, "claim")
	claimed := hc.recvType("pairing_claim")
	if claimed["ct"] != claimCT {
		t.Fatal("relay altered the claim ciphertext")
	}
	approvalCT := sealText(t, marker+" approval")
	hc.send(map[string]any{"t": "pairing_approve", "pairing": inv.pairingID, "deviceId": deviceID, "bearerHash": hexDigest(bearerToken), "scopes": allScopes, "expiresAt": rfc3339(r.now().AddDate(0, 0, 90)), "ct": approvalCT})
	if poll := r.pollPairing(inv); poll.ApprovalCT != approvalCT {
		t.Fatal("approval ciphertext not delivered unchanged")
	}
	dev := device{id: deviceID, bearer: bearerToken, hubID: hubID, pairingID: inv.pairingID}
	requestCT := sealText(t, marker+" request")
	request := makeEnvelope(t, hubID, deviceID, "d2h", 1, "req", 0)
	request.CT = requestCT
	r.expectStatus(r.postWith("/v1/requests", dev.bearer, request), 202, "request")
	responseCT := sealText(t, marker+" response")
	hc.send(map[string]any{"t": "out", "env": envelopeWithCT(t, hubID, deviceID, 1, "res", responseCT)})
	r.expectStatus(r.getWith("/v1/responses?after=0&wait=0", dev.bearer), 200, "responses")
	briefCT := sealText(t, marker+" brief")
	brief := envelopeWithCT(t, hubID, deviceID, 2, "brief", briefCT)
	hc.send(map[string]any{"t": "brief", "env": brief, "asOf": rfc3339(r.now()), "notify": true})
	hc.barrier()
	r.expectStatus(r.getWith("/v1/brief/latest", dev.bearer), 200, "latest brief")
	r.expectStatus(r.putPush(dev, strings.Repeat("9f", 32), "development"), 200, "push")
	// A moment: the hub seals the notice, and the relay stores only that ciphertext.
	noticeCT := sealText(t, marker+" notice")
	noticeEventCT := sealText(t, marker+" notice event")
	noticeID := uuid.NewString()
	hc.send(map[string]any{"t": "out", "env": envelopeWithCT(t, hubID, deviceID, 3, "event", noticeEventCT), "notify": true,
		"push": push.KindMoment, "noticeId": noticeID, "kind": "decision_due", "ct": noticeCT,
		"category": push.CategoryDecision, "level": push.LevelTimeSensitive})
	hc.barrier()
	r.expectStatus(r.postWith("/v1/notices/"+noticeID+"/receipts", dev.bearer, map[string]any{"state": "opened", "action": "", "at": rfc3339(r.now())}), 202, "receipt")
	hc.recvType("receipt")

	ciphertexts := []string{claimCT, approvalCT, requestCT, responseCT, briefCT, noticeCT, noticeEventCT}
	var disk []byte
	for _, name := range []string{"relay.sqlite", "relay.sqlite-wal", "relay.sqlite-shm"} {
		data, err := os.ReadFile(filepath.Join(r.dir, name))
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte(marker)) {
			t.Fatalf("plaintext marker found in %s", name)
		}
		disk = append(disk, data...)
	}
	for _, ct := range ciphertexts {
		if !bytes.Contains(disk, []byte(ct)) {
			t.Fatal("stored ciphertext is missing: the canary is not testing storage")
		}
	}
	if bytes.Contains(logs.Bytes(), []byte(marker)) {
		t.Fatal("plaintext marker found in logs")
	}
	for _, ct := range ciphertexts {
		if bytes.Contains(logs.Bytes(), []byte(ct)) {
			t.Fatal("ciphertext found in logs")
		}
	}
}

// envelopeWithCT builds a canonical envelope object around a given ciphertext.
func envelopeWithCT(t *testing.T, hubID, deviceID string, seq uint64, kind, ct string) json.RawMessage {
	t.Helper()
	env := makeEnvelope(t, hubID, deviceID, "h2d", seq, kind, 0)
	env.CT = ct
	return json.RawMessage(envelopeJSON(t, env))
}
