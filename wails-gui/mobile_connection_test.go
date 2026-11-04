package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMobileLoopbackWarningForPhysicalPhones(t *testing.T) {
	previousRelay := DefaultMobileRelayURL
	previousRemoteAccess := DefaultMobileRemoteAccess
	t.Cleanup(func() {
		DefaultMobileRelayURL = previousRelay
		DefaultMobileRemoteAccess = previousRemoteAccess
	})
	DefaultMobileRelayURL = ""
	DefaultMobileRemoteAccess = ""
	t.Setenv("ISOFORM_MOBILE_REMOTE_ACCESS", "")
	t.Setenv("ISOFORM_RELAY_URL", "")
	t.Setenv("YANSU_RELAY_URL", "")

	now := time.Now()
	cases := []struct {
		name, origin, deviceName string
		simulator, expired, warn bool
	}{
		{"physical phone", "http://127.0.0.1:8791", "My iPhone", false, false, true},
		{"localhost", "http://localhost:8791", "My iPhone", false, false, true},
		{"IPv6 loopback", "https://[::1]:8791", "My iPhone", false, false, true},
		{"Simulator name", "http://127.0.0.1:8791", "iPhone Simulator", false, false, false},
		{"explicit Simulator", "http://127.0.0.1:8791", "iPhone 17", true, false, false},
		{"expired phone", "http://127.0.0.1:8791", "My iPhone", false, true, false},
		{"remote relay", "https://relay.example", "My iPhone", false, false, false},
		{"remote access", "", "My iPhone", false, false, false},
		{"name is not a Simulator marker", "http://127.0.0.1:8791", "Simulatorium", false, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			expires := now.Add(time.Hour)
			if tc.expired {
				expires = now.Add(-time.Hour)
			}
			bridge := &mobileBridge{
				app: &App{}, relay: mobileRelayConfig{URL: tc.origin},
				devices: map[string]*mobileDeviceRecord{"phone": {
					MobileDeviceView: MobileDeviceView{DeviceID: "phone", DeviceName: tc.deviceName,
						ExpiresAt: mobileTimestamp(expires), Simulator: tc.simulator},
				}},
			}
			status := bridge.connectionStatus()
			if (status.Warning != "") != tc.warn {
				t.Fatalf("warning = %q, want warning %v", status.Warning, tc.warn)
			}
			if tc.name == "physical phone" && status.Warning != "A phone on another network cannot reach 127.0.0.1." {
				t.Fatalf("warning = %q", status.Warning)
			}
			if tc.name == "physical phone" && status.RemoteAccessOffReason != "saved_relay" {
				t.Fatalf("saved loopback off reason = %q, want saved_relay", status.RemoteAccessOffReason)
			}
			if tc.name == "remote access" &&
				(status.RemoteAccess == nil || status.RemoteAccessOffReason != "" || status.Warning != "") {
				t.Fatalf("remote mode status: %+v", status)
			}
			raw, err := json.Marshal(status)
			if err != nil || strings.Contains(string(raw), `"warning":`) != tc.warn {
				t.Fatalf("additive warning JSON: %s, %v", raw, err)
			}
		})
	}
}

func TestMobileLoopbackPairingWarnsWithoutRefusing(t *testing.T) {
	cases := []struct {
		name, deviceName, platform string
		simulator, warn            bool
	}{
		{"physical phone", "My iPhone", "ios", false, true},
		{"Simulator name", "iPhone Simulator", "ios", false, false},
		{"Simulator platform", "iPhone 17", "ios-simulator", false, false},
		{"explicit Simulator", "iPhone 17", "ios", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bridge, relay, conn, invitation := mobileTestOpenInvitation(t)
			relay.nextFrame(t, "pairing_open")
			if invitation.Warning != "" || bridge.connectionStatus().Warning != "" {
				t.Fatal("an invitation with no paired phone warned")
			}
			phone := newMobileTestPhone(t)
			frame, _ := phone.claimFrame(t, invitation, func(claim *mobileClaimPlaintext) {
				claim.DeviceName, claim.Platform, claim.Simulator = tc.deviceName, tc.platform, tc.simulator
			})
			conn.send(t, frame)
			mobileTestWaitFor(t, "a pending loopback pairing", func() bool {
				return bridge.connectionStatus().PendingPairing != nil
			})
			pending := bridge.connectionStatus()
			if (pending.Warning != "") != tc.warn {
				t.Fatalf("pending warning = %q, want warning %v", pending.Warning, tc.warn)
			}
			if err := bridge.confirmPairing(invitation.Pairing, true); err != nil {
				t.Fatalf("loopback pairing was refused: %v", err)
			}
			relay.nextFrame(t, "pairing_approve")
			if err := bridge.openPairingWithRelay("https://relay.example.com"); err != nil {
				t.Fatal(err)
			}
			status := bridge.connectionStatus()
			if len(status.Devices) != 1 || status.Devices[0].Simulator == tc.warn || (status.Warning != "") != tc.warn {
				t.Fatalf("paired status: %+v", status)
			}
			if got := mobileTestDecodeInvitation(t, status.ConnectionCode).Warning; got != "" {
				t.Fatalf("public invitation warning = %q, want none", got)
			}
			saved, _, err := loadMobileDeviceStore(bridge.stateDir, time.Now())
			if err != nil || saved[phone.deviceID] == nil || saved[phone.deviceID].Simulator == tc.warn {
				t.Fatalf("Simulator identity was not persisted: %+v, %v", saved[phone.deviceID], err)
			}
		})
	}
}

func TestMobileLoopbackWarningUsesUILanguage(t *testing.T) {
	bridge, store := mobileTestChatBridge(t)
	if err := store.SetUIState(t.Context(), uiLanguageStateKey, "zh-CN"); err != nil {
		t.Fatal(err)
	}
	bridge.relay.URL = "http://127.0.0.1:8791"
	bridge.devices["phone"] = &mobileDeviceRecord{MobileDeviceView: MobileDeviceView{
		DeviceID: "phone", DeviceName: "My iPhone", ExpiresAt: mobileTimestamp(time.Now().Add(time.Hour)),
	}}
	if got := bridge.connectionStatus().Warning; got != "其他网络上的手机无法访问 127.0.0.1。" {
		t.Fatalf("Chinese warning = %q", got)
	}
}

func TestMobileStatusWithoutStateCreatesNothing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "mobile")
	t.Setenv("YANSU_MOBILE_STATE_DIR", dir)
	t.Setenv("ISOFORM_RELAY_URL", "")
	t.Setenv("YANSU_RELAY_URL", "")
	t.Setenv("ISOFORM_MOBILE_REMOTE_ACCESS", "0")
	previousRelay := DefaultMobileRelayURL
	previousRemoteAccess := DefaultMobileRemoteAccess
	t.Cleanup(func() {
		DefaultMobileRelayURL = previousRelay
		DefaultMobileRemoteAccess = previousRemoteAccess
	})
	DefaultMobileRelayURL = ""
	DefaultMobileRemoteAccess = ""
	app := &App{}
	status, err := app.GetMobileConnectionStatus()
	if err != nil || status.Enabled || status.ConnectionCode != "" || len(status.Devices) != 0 ||
		status.RelayState != mobileRelayDisabled || status.RelayURL != mobileDefaultRelayURL ||
		status.SavedRelayURL != "" || status.RemoteAccessOffReason != "disabled" {
		t.Fatalf("status: %+v %v", status, err)
	}
	if bridge, err := startConfiguredMobileBridge(app); err != nil || bridge != nil {
		t.Fatalf("startup without saved state created a bridge: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("reading status or starting up created the state directory")
	}
}

func TestMobileEnableAndGenerateRefuseUnsafeOrUnconfiguredStates(t *testing.T) {
	t.Setenv("YANSU_MOBILE_STATE_DIR", t.TempDir())
	t.Setenv("ISOFORM_RELAY_URL", "")
	app := &App{}
	if _, err := app.GenerateMobileConnectionCode(); err == nil {
		t.Fatal("a code was generated before the connection was turned on")
	}
	for _, relayURL := range []string{"http://relay.example", "ws://relay.example", "https://relay.example/with/path"} {
		if _, err := app.EnableMobileConnection(relayURL); err == nil {
			t.Fatalf("the relay %s was accepted", relayURL)
		}
	}
	if status, err := app.GetMobileConnectionStatus(); err != nil || status.Enabled {
		t.Fatalf("a refused enable left the connection on: %+v %v", status, err)
	}
}

func TestMobileConnectionLifecycleThroughBindings(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("YANSU_MOBILE_STATE_DIR", dir)
	t.Setenv("ISOFORM_RELAY_URL", "")
	relay := newMobileTestRelay(t, "", "")
	app := &App{}
	t.Cleanup(func() {
		if bridge := app.mobileBridgeForUse(); bridge != nil {
			bridge.Stop()
		}
	})

	status, err := app.EnableMobileConnection(relay.url())
	if err != nil || !status.Enabled || status.RelayURL != relay.url() || status.HubID == "" || status.HubName == "" {
		t.Fatalf("enable: %+v %v", status, err)
	}
	hubID := status.HubID
	conn := relay.acceptRegistration(t, nil)
	mobileTestWaitFor(t, "a connected relay", func() bool {
		current, _ := app.GetMobileConnectionStatus()
		return current.RelayState == mobileRelayConnected
	})

	if _, err = app.GenerateMobileConnectionCode(); err != errMobileRemoteAccessNotReady {
		t.Fatalf("loopback code error = %v", err)
	}
	if err = app.mobileBridgeForUse().openPairingWithRelay("https://relay.example.com"); err != nil {
		t.Fatal(err)
	}
	status, err = app.GetMobileConnectionStatus()
	if err != nil || status.ConnectionCode == "" || status.ExpiresAt == "" || status.HubID != hubID {
		t.Fatalf("generate: %+v %v", status, err)
	}
	invitation := mobileTestDecodeInvitation(t, status.ConnectionCode)
	relay.nextFrame(t, "pairing_open")

	phone := newMobileTestPhone(t)
	frame, kPair := phone.claimFrame(t, invitation, nil)
	conn.send(t, frame)
	mobileTestWaitFor(t, "a pending claim", func() bool {
		current, _ := app.GetMobileConnectionStatus()
		return current.PendingPairing != nil
	})
	pending, _ := app.GetMobileConnectionStatus()
	if want, _ := mobileConfirmationCode(kPair); pending.PendingPairing.Code != want || pending.PendingPairing.DeviceName != "Test iPhone" {
		t.Fatalf("pending pairing: %+v", pending.PendingPairing)
	}
	status, err = app.ConfirmMobilePairing(pending.PendingPairing.PairingID, true)
	if err != nil || len(status.Devices) != 1 || status.Devices[0].DeviceID != phone.deviceID || status.ConnectionCode != "" {
		t.Fatalf("confirm: %+v %v", status, err)
	}
	if got := status.Devices[0]; len(got.Scopes) != 4 || got.ExpiresAt == "" || got.CreatedAt == "" {
		t.Fatalf("device view is incomplete: %+v", got)
	}
	relay.nextFrame(t, "pairing_approve")

	// The paired device survives a restart, and the connection comes back on its own.
	app.mobileBridgeForUse().Stop()
	restarted := &App{}
	bridge, err := startConfiguredMobileBridge(restarted)
	if err != nil || bridge == nil {
		t.Fatalf("restart: %v", err)
	}
	restarted.mobileBridgeMu.Lock()
	restarted.mobileBridge = bridge
	restarted.mobileBridgeMu.Unlock()
	t.Cleanup(bridge.Stop)
	seen := mobileTimestamp(time.Now())
	relay.acceptRegistration(t, []mobileTestListing{{DeviceID: phone.deviceID, LastSeenAt: &seen}})
	// The relay's device listing is reconciled into the device view after the
	// transport reports connected. Wait for the state the assertion depends on.
	mobileTestWaitFor(t, "a reconnected relay with reconciled presence", func() bool {
		current, _ := restarted.GetMobileConnectionStatus()
		return current.RelayState == mobileRelayConnected && len(current.Devices) == 1 && current.Devices[0].LastSeenAt == seen
	})
	status, _ = restarted.GetMobileConnectionStatus()
	if status.HubID != hubID || len(status.Devices) != 1 || status.Devices[0].LastSeenAt != seen {
		t.Fatalf("restarted status: %+v", status)
	}

	// Revocation removes the device here and asks the relay to delete it there.
	status, err = restarted.RevokeMobileDevice(phone.deviceID)
	if err != nil || len(status.Devices) != 0 {
		t.Fatalf("revoke: %+v %v", status, err)
	}
	relay.nextFrame(t, "revoke")

	// Disabling keeps the saved state and stops the socket. Enabling again reuses the identity.
	status, err = restarted.DisableMobileConnection()
	if err != nil || status.Enabled || status.RelayState != mobileRelayDisabled {
		t.Fatalf("disable: %+v %v", status, err)
	}
	saved, err := loadMobileRelayConfig(dir)
	if err != nil || saved.Enabled {
		t.Fatalf("relay.json still enabled: %+v %v", saved, err)
	}
	status, err = restarted.EnableMobileConnection("")
	if err != nil || !status.Enabled || status.HubID != hubID || status.SavedRelayURL != relay.url() {
		t.Fatalf("re-enable: %+v %v", status, err)
	}
	saved, err = loadMobileRelayConfig(dir)
	if err != nil || saved.URL != relay.url() {
		t.Fatalf("empty EnableMobileConnection changed the saved relay: %+v %v", saved, err)
	}
	relay.acceptRegistration(t, nil)
	mobileTestWaitFor(t, "a reconnected relay after re-enable", func() bool {
		current, _ := restarted.GetMobileConnectionStatus()
		return current.RelayState == mobileRelayConnected
	})
	if _, err := restarted.DisableMobileConnection(); err != nil {
		t.Fatal(err)
	}
}
