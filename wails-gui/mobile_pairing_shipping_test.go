package main

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestMobilePairingShippingIOSClaimDistinguishesSimulator(t *testing.T) {
	for _, tc := range []struct {
		name      string
		simulator bool
	}{
		{name: "Simulator", simulator: true},
		{name: "physical iPhone"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bridge, relay, _, invitation := mobileTestOpenInvitation(t)
			relay.nextFrame(t, "pairing_open")
			phone := newMobileTestPhone(t)
			hubPublic, err := mobileParsePublicKey(invitation.HubPub)
			if err != nil {
				t.Fatal(err)
			}
			secret, err := base64.RawURLEncoding.DecodeString(invitation.Secret)
			if err != nil {
				t.Fatal(err)
			}
			kPair, err := mobilePairKey(phone.private, hubPublic, secret, invitation.Hub, invitation.Pairing)
			if err != nil {
				t.Fatal(err)
			}

			// Match PairingStore's outbound plaintext, covered by its Swift producer
			// test. Both devices use platform ios and an ordinary UIDevice name.
			claim := map[string]any{
				"deviceId": phone.deviceID, "deviceName": "iPhone 17 Pro", "devicePub": phone.publicKeyText(),
				"bearerHash": phone.bearerHash, "platform": "ios", "appVersion": "1.0 (12)",
				"pushEnvironment": "development",
				"scopes": []string{
					"brief:read", "brief:refresh", "presence:read", "push",
					"chat:read", "chat:write", "memory:correct", "handoff:create",
					"brief:feedback", "offers:read", "offers:decide", "goals:read", "goals:write", "library:read",
				},
			}
			if tc.simulator {
				claim["simulator"] = true
			}
			plaintext, err := json.Marshal(claim)
			if err != nil {
				t.Fatal(err)
			}
			ciphertext, err := sealMobilePairingMessage(kPair, mobilePairingClaimNonceByte,
				mobileClaimAAD(invitation.Hub, invitation.Pairing), plaintext)
			if err != nil {
				t.Fatal(err)
			}
			frame, err := json.Marshal(map[string]any{
				"t": "pairing_claim", "pairing": invitation.Pairing, "deviceId": phone.deviceID,
				"devicePub": phone.publicKeyText(), "ct": base64.RawURLEncoding.EncodeToString(ciphertext),
			})
			if err != nil {
				t.Fatal(err)
			}
			bridge.handlePairingClaim(frame)

			status := bridge.connectionStatus()
			if status.PendingPairing == nil || status.PendingPairing.DeviceName != "iPhone 17 Pro" {
				t.Fatalf("shipping claim was not accepted: %+v", status)
			}
			if (status.Warning == "") != tc.simulator {
				t.Fatalf("pending warning = %q, simulator = %v", status.Warning, tc.simulator)
			}
			if err := bridge.confirmPairing(invitation.Pairing, true); err != nil {
				t.Fatalf("loopback pairing was refused: %v", err)
			}
			relay.nextFrame(t, "pairing_approve")
			status = bridge.connectionStatus()
			if len(status.Devices) != 1 || status.Devices[0].Simulator != tc.simulator {
				t.Fatalf("paired device lost its simulator identity: %+v", status.Devices)
			}
			if (status.Warning == "") != tc.simulator {
				t.Fatalf("paired warning = %q, simulator = %v", status.Warning, tc.simulator)
			}
		})
	}
}
