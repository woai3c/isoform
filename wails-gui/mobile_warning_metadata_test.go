package main

import (
	"encoding/json"
	"testing"
)

func TestMobileLoopbackWarningCarriesTranslationMetadata(t *testing.T) {
	bridge, _, _, _ := mobileTestOpenInvitation(t)
	mobileTestWaitFor(t, "registration before adding the phone", func() bool {
		return bridge.connectionStatus().RelayState == mobileRelayConnected
	})
	phone := newMobileTestPhone(t)
	mobileTestAddDevice(t, bridge, phone, []string{mobileScopeBriefRead})
	if err := bridge.openPairingWithRelay("https://relay.example.com"); err != nil {
		t.Fatal(err)
	}
	status := bridge.connectionStatus()
	invitation := mobileTestDecodeInvitation(t, status.ConnectionCode)
	if invitation.Warning != "" {
		t.Fatal("public invitation must not carry a loopback warning")
	}
	for name, value := range map[string]any{"status": status} {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]any
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		if fields["warningCode"] != "relay_loopback" || fields["warningHost"] != "127.0.0.1" {
			t.Errorf("%s lacks translation metadata: %s", name, raw)
		}
	}
}
