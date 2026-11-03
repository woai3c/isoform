package main

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"testing"
)

const (
	mobileTestHubID    = "6f1c2e4a-7b3d-4c5e-9a1b-2d3e4f5a6b7c"
	mobileTestDeviceID = "0b9d6e2c-3f4a-4e5b-8c7d-1a2b3c4d5e6f"
	mobileTestOtherID  = "a7e3c1d2-5b4a-4f6e-8d9c-0e1f2a3b4c5d"
)

func mobileTestKeyPair(t *testing.T) (*ecdh.PrivateKey, *ecdh.PublicKey) {
	t.Helper()
	private, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return private, private.PublicKey()
}

// mobileTestSessionKeys returns the hub and device views of the same session.
func mobileTestSessionKeys(t *testing.T) (mobileSessionKeys, mobileSessionKeys) {
	t.Helper()
	hubPrivate, hubPublic := mobileTestKeyPair(t)
	devicePrivate, devicePublic := mobileTestKeyPair(t)
	hubKeys, err := mobileDeriveSessionKeys(hubPrivate, devicePublic, mobileTestHubID, mobileTestDeviceID)
	if err != nil {
		t.Fatal(err)
	}
	deviceKeys, err := mobileDeriveSessionKeys(devicePrivate, hubPublic, mobileTestHubID, mobileTestDeviceID)
	if err != nil {
		t.Fatal(err)
	}
	return hubKeys, deviceKeys
}

func TestMobileEnvelopeRoundTripAndHeaderBinding(t *testing.T) {
	hubKeys, deviceKeys := mobileTestSessionKeys(t)
	if !bytes.Equal(hubKeys.D2H, deviceKeys.D2H) || !bytes.Equal(hubKeys.H2D, deviceKeys.H2D) {
		t.Fatal("hub and device derived different session keys")
	}
	if bytes.Equal(hubKeys.D2H, hubKeys.H2D) {
		t.Fatal("the two directions share a key")
	}
	plaintext := []byte(`{"id":"` + mobileTestOtherID + `","method":"GET","path":"/status","query":"","body":""}`)
	env, err := sealMobileEnvelope(deviceKeys.D2H, mobileTestHubID, mobileTestDeviceID, mobileDirD2H, 7, mobileKindRequest, plaintext)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := openMobileEnvelope(hubKeys.D2H, env, mobileTestHubID, mobileTestDeviceID, mobileDirD2H)
	if err != nil || !bytes.Equal(opened, plaintext) {
		t.Fatalf("round trip failed: %v", err)
	}
	// Any change to the authenticated header or the ciphertext must fail.
	tampers := map[string]func(*mobileEnvelope){
		"hub":  func(e *mobileEnvelope) { e.Hub = mobileTestOtherID },
		"dev":  func(e *mobileEnvelope) { e.Dev = mobileTestOtherID },
		"seq":  func(e *mobileEnvelope) { e.Seq = 8 },
		"kind": func(e *mobileEnvelope) { e.Kind = mobileKindResponse },
		"ct": func(e *mobileEnvelope) {
			if e.CT[0] == 'A' {
				e.CT = "B" + e.CT[1:]
			} else {
				e.CT = "A" + e.CT[1:]
			}
		},
		"v": func(e *mobileEnvelope) { e.V = 2 },
	}
	for name, mutate := range tampers {
		tampered := env
		mutate(&tampered)
		if _, err := openMobileEnvelope(hubKeys.D2H, tampered, mobileTestHubID, mobileTestDeviceID, mobileDirD2H); err == nil {
			t.Fatalf("a changed %s was accepted", name)
		}
	}
	// The receiver's expected identity is checked, not just the sender's claim.
	if _, err := openMobileEnvelope(hubKeys.D2H, env, mobileTestOtherID, mobileTestDeviceID, mobileDirD2H); err == nil {
		t.Fatal("an envelope for another hub was accepted")
	}
}

func TestMobileEnvelopeRejectsDirectionSwap(t *testing.T) {
	hubKeys, _ := mobileTestSessionKeys(t)
	response, err := sealMobileEnvelope(hubKeys.H2D, mobileTestHubID, mobileTestDeviceID, mobileDirH2D, 1, mobileKindResponse, []byte(`{"id":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	// Relabeling a hub-to-device message as device-to-hub must fail, whichever key is tried.
	relabeled := response
	relabeled.Dir = mobileDirD2H
	for _, key := range [][]byte{hubKeys.D2H, hubKeys.H2D} {
		if _, err := openMobileEnvelope(key, relabeled, mobileTestHubID, mobileTestDeviceID, mobileDirD2H); err == nil {
			t.Fatal("a hub-to-device envelope opened as device-to-hub")
		}
	}
	if _, err := openMobileEnvelope(hubKeys.D2H, response, mobileTestHubID, mobileTestDeviceID, mobileDirH2D); err == nil {
		t.Fatal("the wrong direction key opened an envelope")
	}
	// A device may send only requests; responses and briefs are refused on the way out.
	if _, err := sealMobileEnvelope(hubKeys.D2H, mobileTestHubID, mobileTestDeviceID, mobileDirD2H, 1, mobileKindResponse, []byte(`{}`)); err == nil {
		t.Fatal("a response was sealed in the device-to-hub direction")
	}
}

func TestMobileEnvelopeReceiveStreamsAreIndependent(t *testing.T) {
	record := &mobileDeviceRecord{}
	record.setLastAccepted(mobileStreamMessage, 17)
	if record.lastAccepted(mobileStreamBrief) != 0 {
		t.Fatal("accepting a message moved the brief stream")
	}
	// A brief numbered below an accepted response is still fresh on its own stream.
	if 5 <= record.lastAccepted(mobileStreamBrief) {
		t.Fatal("a stored brief would be rejected behind a later response")
	}
	record.setLastAccepted(mobileStreamBrief, 5)
	if record.lastAccepted(mobileStreamMessage) != 17 {
		t.Fatal("accepting a brief moved the message stream")
	}
	for kind, want := range map[string]string{
		mobileKindBrief:    mobileStreamBrief,
		mobileKindRequest:  mobileStreamMessage,
		mobileKindResponse: mobileStreamMessage,
		mobileKindEvent:    mobileStreamMessage,
	} {
		if got := mobileStreamForKind(kind); got != want {
			t.Fatalf("kind %s maps to stream %s, want %s", kind, got, want)
		}
	}
}

func TestMobileBriefPaddingRoundTrip(t *testing.T) {
	hubKeys, deviceKeys := mobileTestSessionKeys(t)
	document := []byte(`{"version":1,"briefId":"2026-10-09-morning","slot":"morning"}`)
	padded, err := mobilePadBrief(document)
	if err != nil {
		t.Fatal(err)
	}
	if len(padded) != mobileBriefPadBlock {
		t.Fatalf("a small brief padded to %d bytes", len(padded))
	}
	if trimmed, err := mobileUnpadBrief(padded); err != nil || !bytes.Equal(trimmed, document) {
		t.Fatalf("unpadding did not restore the document: %v", err)
	}
	exact := bytes.Repeat([]byte("a"), mobileBriefPadBlock)
	if padded, err := mobilePadBrief(exact); err != nil || len(padded) != mobileBriefPadBlock {
		t.Fatal("an exact multiple of the block was padded again")
	}
	if _, err := mobilePadBrief(make([]byte, mobileMaxBriefPlaintext+1)); err == nil {
		t.Fatal("an oversized brief was padded")
	}
	if _, err := sealMobileEnvelope(hubKeys.H2D, mobileTestHubID, mobileTestDeviceID, mobileDirH2D, 1, mobileKindBrief, document); err == nil {
		t.Fatal("an unpadded brief was sealed")
	}
	env, err := sealMobileEnvelope(hubKeys.H2D, mobileTestHubID, mobileTestDeviceID, mobileDirH2D, 3, mobileKindBrief, padded)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := openMobileEnvelope(deviceKeys.H2D, env, mobileTestHubID, mobileTestDeviceID, mobileDirH2D)
	if err != nil || !bytes.Equal(opened, document) {
		t.Fatalf("the phone did not recover the brief: %v", err)
	}
}

func TestMobileEnvelopeSizeLimits(t *testing.T) {
	hubKeys, _ := mobileTestSessionKeys(t)
	atLimit := make([]byte, mobileMaxMessagePlaintext)
	if _, err := sealMobileEnvelope(hubKeys.H2D, mobileTestHubID, mobileTestDeviceID, mobileDirH2D, 1, mobileKindResponse, atLimit); err != nil {
		t.Fatalf("a 64 KiB response was refused: %v", err)
	}
	overLimit := make([]byte, mobileMaxMessagePlaintext+1)
	if _, err := sealMobileEnvelope(hubKeys.H2D, mobileTestHubID, mobileTestDeviceID, mobileDirH2D, 2, mobileKindResponse, overLimit); err == nil {
		t.Fatal("a response above 64 KiB was sealed")
	}
	if _, err := sealMobileEnvelope(hubKeys.H2D, mobileTestHubID, mobileTestDeviceID, mobileDirH2D, 0, mobileKindResponse, []byte(`{}`)); err == nil {
		t.Fatal("sequence zero was sealed")
	}
}

func TestMobileSequenceNumbersStayJSONSafe(t *testing.T) {
	hubKeys, deviceKeys := mobileTestSessionKeys(t)
	largest := uint64(1<<53 - 2)
	if mobileMaxSequence != largest {
		t.Fatalf("sequence bound %d is not 2^53-2", mobileMaxSequence)
	}
	env, err := sealMobileEnvelope(hubKeys.H2D, mobileTestHubID, mobileTestDeviceID, mobileDirH2D, largest, mobileKindResponse, []byte(`{}`))
	if err != nil {
		t.Fatalf("the largest JSON-safe sequence was refused: %v", err)
	}
	if _, err := openMobileEnvelope(deviceKeys.H2D, env, mobileTestHubID, mobileTestDeviceID, mobileDirH2D); err != nil {
		t.Fatalf("the largest JSON-safe sequence did not open: %v", err)
	}
	if _, err := sealMobileEnvelope(hubKeys.H2D, mobileTestHubID, mobileTestDeviceID, mobileDirH2D, largest+1, mobileKindResponse, []byte(`{}`)); err == nil {
		t.Fatal("a sequence above 2^53-2 was sealed")
	}
	beyond := env
	beyond.Seq = largest + 1
	if _, err := openMobileEnvelope(deviceKeys.H2D, beyond, mobileTestHubID, mobileTestDeviceID, mobileDirH2D); err == nil {
		t.Fatal("a sequence above 2^53-2 was accepted")
	}
	// A restart skips ahead by a fixed step and never past the bound.
	if got := mobileSkipAhead(0); got != mobileSendSeqSkip {
		t.Fatalf("skip-ahead from zero gave %d", got)
	}
	if got := mobileSkipAhead(largest); got != largest {
		t.Fatalf("skip-ahead passed the bound: %d", got)
	}
}

func TestMobileEnvelopeWireLayout(t *testing.T) {
	want := []byte{0, 0, 0, 0, 1, 2, 3, 4, 5, 6, 7, 8}
	if got := mobileEnvelopeNonce(0x0102030405060708); !bytes.Equal(got, want) {
		t.Fatalf("nonce %x, want %x", got, want)
	}
	aad := string(mobileEnvelopeAAD(mobileEnvelope{V: 1, Hub: "h", Dev: "d", Dir: "d2h", Seq: 17, Kind: "req"}))
	if aad != "yansu-env-v1|1|h|d|d2h|17|req" {
		t.Fatalf("aad %q", aad)
	}
	claimNonce := mobilePairingNonce(mobilePairingClaimNonceByte)
	if !bytes.Equal(claimNonce, []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}) {
		t.Fatalf("claim nonce %x", claimNonce)
	}
	if !bytes.Equal(mobilePairingNonce(mobilePairingApprovalNonceByte), []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2}) {
		t.Fatal("approval nonce layout changed")
	}
}
