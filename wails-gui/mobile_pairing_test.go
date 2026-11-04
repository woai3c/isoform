package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func mobileTestDecodeInvitation(t *testing.T, code string) mobileInvitation {
	t.Helper()
	prefix := ""
	switch {
	case strings.HasPrefix(code, mobileInvitationPrefix):
		prefix = mobileInvitationPrefix
	case strings.HasPrefix(code, mobileLegacyInvitationPrefix):
		prefix = mobileLegacyInvitationPrefix
	default:
		t.Fatalf("invitation has the wrong scheme: %.20s", code)
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(code, prefix))
	if err != nil {
		t.Fatal(err)
	}
	var invitation mobileInvitation
	if err := mobileDecodeStrict(raw, &invitation); err != nil {
		t.Fatal(err)
	}
	return invitation
}

// mobileTestOpenInvitation connects a hub and opens one invitation through the
// production path, returning the decoded invitation.
func mobileTestOpenInvitation(t *testing.T) (*mobileBridge, *mobileTestRelay, *mobileTestRelayConn, mobileInvitation) {
	t.Helper()
	bridge, relay := mobileTestHub(t)
	conn := relay.acceptRegistration(t, nil)
	mobileTestWaitFor(t, "relay connected", func() bool { return bridge.connectionStatus().RelayState == mobileRelayConnected })
	if err := bridge.openPairingWithRelay("https://relay.example.com"); err != nil {
		t.Fatal(err)
	}
	status := bridge.connectionStatus()
	if status.ConnectionCode == "" {
		t.Fatal("no invitation was shown after opening a pairing")
	}
	return bridge, relay, conn, mobileTestDecodeInvitation(t, status.ConnectionCode)
}

func TestMobilePairingInvitationAndOpenFrame(t *testing.T) {
	bridge, relay, _, invitation := mobileTestOpenInvitation(t)
	if !strings.HasPrefix(bridge.connectionStatus().ConnectionCode, mobileInvitationPrefix) {
		t.Fatal("new pairing invitations must use the Isoform scheme")
	}
	if invitation.V != 1 || invitation.Hub != bridge.identity.HubID || invitation.Relay != "https://relay.example.com" {
		t.Fatalf("invitation metadata is wrong: %+v", invitation)
	}
	secret, err := base64.RawURLEncoding.DecodeString(invitation.Secret)
	if err != nil || len(secret) != 32 || !mobileValidUUID(invitation.Pairing) {
		t.Fatal("invitation secret or pairing ID is malformed")
	}
	if hubPub := base64.RawURLEncoding.EncodeToString(bridge.identity.Public.Bytes()); invitation.HubPub != hubPub {
		t.Fatal("invitation carries the wrong hub public key")
	}
	if remaining := time.Until(time.Unix(invitation.Exp, 0)); remaining < 9*time.Minute || remaining > mobilePairingLifetime {
		t.Fatalf("invitation lifetime is %s", remaining)
	}
	open := relay.nextFrame(t, "pairing_open")
	var frame struct {
		Pairing        string `json:"pairing"`
		ClaimTokenHash string `json:"claimTokenHash"`
		ExpiresAt      string `json:"expiresAt"`
	}
	if err := json.Unmarshal(open.raw, &frame); err != nil {
		t.Fatal(err)
	}
	token := mobileClaimToken(secret, invitation.Pairing)
	digest := sha256.Sum256([]byte(token))
	if frame.Pairing != invitation.Pairing || frame.ClaimTokenHash != hex.EncodeToString(digest[:]) {
		t.Fatal("the relay was given a claim-token hash the invitation cannot satisfy")
	}
	if strings.Contains(string(open.raw), invitation.Secret) {
		t.Fatal("the invitation secret was sent to the relay")
	}
	if other := mobileClaimToken(secret, uuid.NewString()); other == token {
		t.Fatal("the claim token does not depend on the pairing ID")
	}
	if _, ok := mobileParseTimestamp(frame.ExpiresAt); !ok {
		t.Fatal("pairing_open carried no expiry")
	}
}

func TestMobilePairingInvitationDecoderAcceptsLegacyScheme(t *testing.T) {
	bridge, _, _, invitation := mobileTestOpenInvitation(t)
	code := strings.Replace(bridge.connectionStatus().ConnectionCode, mobileInvitationPrefix, mobileLegacyInvitationPrefix, 1)
	if decoded := mobileTestDecodeInvitation(t, code); decoded != invitation {
		t.Fatalf("legacy invitation decoded differently: %+v", decoded)
	}
}

func TestMobilePairingClaimShowsTheMatchingCode(t *testing.T) {
	bridge, relay, conn, invitation := mobileTestOpenInvitation(t)
	relay.nextFrame(t, "pairing_open")
	phone := newMobileTestPhone(t)
	frame, kPair := phone.claimFrame(t, invitation, nil)
	conn.send(t, frame)
	mobileTestWaitFor(t, "a pending claim", func() bool { return bridge.connectionStatus().PendingPairing != nil })
	status := bridge.connectionStatus()
	want, err := mobileConfirmationCode(kPair)
	if err != nil || len(want) != 6 {
		t.Fatalf("phone-side code: %v", err)
	}
	if status.PendingPairing.Code != want || status.PendingPairing.PairingID != invitation.Pairing || status.PendingPairing.DeviceName != "Test iPhone" {
		t.Fatalf("the hub shows a different confirmation than the phone derives: %+v", status.PendingPairing)
	}
}

func TestMobilePairingRejectsInconsistentClaims(t *testing.T) {
	cases := map[string]func(*mobileClaimPlaintext){
		"device key differs from the frame": func(c *mobileClaimPlaintext) {
			c.DevicePub = base64.RawURLEncoding.EncodeToString(make([]byte, 32))
		},
		"device id differs from the frame": func(c *mobileClaimPlaintext) { c.DeviceID = uuid.NewString() },
		"name longer than 120 bytes":       func(c *mobileClaimPlaintext) { c.DeviceName = strings.Repeat("n", 121) },
		"scope outside release 1":          func(c *mobileClaimPlaintext) { c.Scopes = append(c.Scopes, "chat") },
		"malformed bearer hash":            func(c *mobileClaimPlaintext) { c.BearerHash = strings.Repeat("F", 64) },
		"unknown push environment":         func(c *mobileClaimPlaintext) { c.PushEnvironment = "staging" },
	}
	for name, override := range cases {
		t.Run(name, func(t *testing.T) {
			bridge, relay, conn, invitation := mobileTestOpenInvitation(t)
			relay.nextFrame(t, "pairing_open")
			// The override changes only the sealed plaintext, so the clear frame
			// still names the real device and the mismatch is what must be caught.
			frame, _ := newMobileTestPhone(t).claimFrame(t, invitation, override)
			conn.send(t, frame)
			// Each rejected claim counts toward consumption; the first is not held.
			mobileTestWaitFor(t, "the claim to be counted", func() bool {
				bridge.mu.Lock()
				defer bridge.mu.Unlock()
				return bridge.pairing != nil && bridge.pairing.Failures == 1
			})
			if bridge.connectionStatus().PendingPairing != nil {
				t.Fatal("an inconsistent claim was held for confirmation")
			}
		})
	}
}

func TestMobilePairingThreeFailuresConsumeTheInvitation(t *testing.T) {
	bridge, relay, conn, invitation := mobileTestOpenInvitation(t)
	relay.nextFrame(t, "pairing_open")
	phone := newMobileTestPhone(t)
	bad, _ := phone.claimFrame(t, invitation, nil)
	bad["ct"] = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 64))
	conn.send(t, bad)
	conn.send(t, bad)
	mobileTestWaitFor(t, "two failures", func() bool {
		bridge.mu.Lock()
		defer bridge.mu.Unlock()
		return bridge.pairing != nil && bridge.pairing.Failures == 2
	})
	if bridge.connectionStatus().ConnectionCode == "" {
		t.Fatal("the invitation was consumed before the third failure")
	}
	conn.send(t, bad)
	decline := relay.nextFrame(t, "pairing_decline")
	if !strings.Contains(string(decline.raw), invitation.Pairing) {
		t.Fatal("the decline does not name the consumed pairing")
	}
	if status := bridge.connectionStatus(); status.ConnectionCode != "" {
		t.Fatal("a consumed invitation is still shown")
	}
}

func TestMobilePairingRecoversAfterOneBadClaimAndIgnoresRedelivery(t *testing.T) {
	bridge, relay, conn, invitation := mobileTestOpenInvitation(t)
	relay.nextFrame(t, "pairing_open")
	phone := newMobileTestPhone(t)
	bad, _ := phone.claimFrame(t, invitation, nil)
	bad["ct"] = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{9}, 64))
	conn.send(t, bad)
	good, _ := phone.claimFrame(t, invitation, nil)
	conn.send(t, good)
	mobileTestWaitFor(t, "the good claim", func() bool { return bridge.connectionStatus().PendingPairing != nil })
	// The relay replays the claim after a reconnect; it must not count as a failure.
	conn.send(t, good)
	conn.send(t, good)
	time.Sleep(100 * time.Millisecond)
	bridge.mu.Lock()
	failures := bridge.pairing.Failures
	bridge.mu.Unlock()
	if failures != 1 || bridge.connectionStatus().PendingPairing == nil {
		t.Fatalf("redelivery changed the pairing: failures=%d", failures)
	}
}

func TestMobilePairingApprovalIsSealedForTheDevice(t *testing.T) {
	bridge, relay, conn, invitation := mobileTestOpenInvitation(t)
	relay.nextFrame(t, "pairing_open")
	phone := newMobileTestPhone(t)
	frame, kPair := phone.claimFrame(t, invitation, nil)
	conn.send(t, frame)
	mobileTestWaitFor(t, "a pending claim", func() bool { return bridge.connectionStatus().PendingPairing != nil })

	if err := bridge.confirmPairing(invitation.Pairing, true); err != nil {
		t.Fatal(err)
	}
	approve := relay.nextFrame(t, "pairing_approve")
	var approval struct {
		Pairing    string   `json:"pairing"`
		DeviceID   string   `json:"deviceId"`
		BearerHash string   `json:"bearerHash"`
		Scopes     []string `json:"scopes"`
		ExpiresAt  string   `json:"expiresAt"`
		CT         string   `json:"ct"`
	}
	if err := json.Unmarshal(approve.raw, &approval); err != nil {
		t.Fatal(err)
	}
	if approval.DeviceID != phone.deviceID || approval.BearerHash != phone.bearerHash {
		t.Fatal("approval names the wrong device or bearer")
	}
	ciphertext, err := base64.RawURLEncoding.DecodeString(approval.CT)
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := openMobilePairingMessage(kPair, mobilePairingApprovalNonceByte, mobileApprovalAAD(bridge.identity.HubID, invitation.Pairing), ciphertext)
	if err != nil {
		t.Fatalf("the phone cannot open the approval: %v", err)
	}
	var sealed mobileApprovalPlaintext
	if err := mobileDecodeStrict(plaintext, &sealed); err != nil {
		t.Fatal(err)
	}
	if sealed.DeviceID != phone.deviceID || sealed.ExpiresAt != approval.ExpiresAt || sealed.HubName == "" ||
		sealed.HubPub != invitation.HubPub || strings.Join(sealed.Scopes, ",") != strings.Join(approval.Scopes, ",") {
		t.Fatalf("approval plaintext is inconsistent: %+v", sealed)
	}
	if status := bridge.connectionStatus(); len(status.Devices) != 1 || status.ConnectionCode != "" || status.PendingPairing != nil {
		t.Fatalf("approval did not store the device and consume the invitation: %+v", status)
	}
	if err := bridge.confirmPairing(invitation.Pairing, true); err == nil {
		t.Fatal("a consumed claim was approved twice")
	}
}

func TestMobilePairingDeclineConsumesTheClaim(t *testing.T) {
	bridge, relay, conn, invitation := mobileTestOpenInvitation(t)
	relay.nextFrame(t, "pairing_open")
	frame, _ := newMobileTestPhone(t).claimFrame(t, invitation, nil)
	conn.send(t, frame)
	mobileTestWaitFor(t, "a pending claim", func() bool { return bridge.connectionStatus().PendingPairing != nil })
	if err := bridge.confirmPairing(invitation.Pairing, false); err != nil {
		t.Fatal(err)
	}
	relay.nextFrame(t, "pairing_decline")
	if status := bridge.connectionStatus(); status.PendingPairing != nil || len(status.Devices) != 0 {
		t.Fatal("a declined claim left state behind")
	}
}

func TestMobilePairingInvitationExpiresLocally(t *testing.T) {
	bridge, _, _, invitation := mobileTestOpenInvitation(t)
	bridge.mu.Lock()
	bridge.pairing.ExpiresAt = time.Now().Add(-time.Second)
	bridge.mu.Unlock()
	if status := bridge.connectionStatus(); status.ConnectionCode != "" {
		t.Fatal("an expired invitation was still shown")
	}
	if err := bridge.confirmPairing(invitation.Pairing, true); err == nil {
		t.Fatal("an expired invitation was confirmed")
	}
}

func TestMobileInvitationRelayReachability(t *testing.T) {
	for _, tc := range []struct {
		origin string
		valid  bool
	}{
		{"http://127.0.0.1:57559", false}, {"https://localhost", false},
		{"https://[::1]", false}, {"https://169.254.1.2", false},
		{"https://192.168.1.3", false}, {"https://100.64.0.1", false},
		{"https://relay.local", false}, {"https://relay.example.com", true},
		{"https://8.8.8.8", true}, {"https://[2606:4700:4700::1111]", true},
	} {
		t.Run(tc.origin, func(t *testing.T) {
			if got := mobileInvitationRelayReachable(tc.origin); got != tc.valid {
				t.Fatalf("got %v", got)
			}
		})
	}
	bridge, relay := mobileTestHub(t)
	relay.acceptRegistration(t, nil)
	mobileTestWaitFor(t, "relay connected", func() bool { return bridge.connectionStatus().RelayState == mobileRelayConnected })
	if err := bridge.openPairing(); err != errMobileRemoteAccessNotReady {
		t.Fatalf("loopback error = %v", err)
	}
	if bridge.connectionStatus().ConnectionCode != "" {
		t.Fatal("loopback invitation was shown")
	}
	if err := bridge.openPairingWithRelay("https://relay.example.com"); err != nil {
		t.Fatal(err)
	}
	if got := mobileTestDecodeInvitation(t, bridge.connectionStatus().ConnectionCode).Relay; got != "https://relay.example.com" {
		t.Fatalf("relay = %q", got)
	}
}
