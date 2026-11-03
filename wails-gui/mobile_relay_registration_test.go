package main

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
)

// mobileTestRegistrationGate pauses the real bridge callback after the socket
// received registered, making publication during reconciliation deterministic.
type mobileTestRegistrationGate struct {
	bridge  *mobileBridge
	entered chan struct{}
	release chan struct{}
}

// A queued control frame blocks inside JSON encoding after reconciliation and
// queue extraction, before the following pending brief reaches the socket.
type mobileTestReplayBarrier struct {
	entered chan struct{}
	release chan struct{}
}

func (barrier *mobileTestReplayBarrier) MarshalJSON() ([]byte, error) {
	select {
	case barrier.entered <- struct{}{}:
	default:
	}
	<-barrier.release
	return []byte("true"), nil
}

func TestMobileRelayNewBriefWaitsForPendingReplay(t *testing.T) {
	bridge, relay := mobileTestHub(t)
	phone := newMobileTestPhone(t)
	mobileTestAddDevice(t, bridge, phone, []string{mobileScopeBriefRead})
	older := MobileBriefV1{Version: 1, BriefID: "2026-10-09-morning", Slot: "morning", AsOf: "2026-10-09T07:00:00Z"}
	newer := MobileBriefV1{Version: 1, BriefID: "2026-10-09-evening", Slot: "evening", AsOf: "2026-10-09T18:00:00Z"}
	if err := bridge.publishBrief(older, false); err != nil {
		t.Fatal(err)
	}
	barrier := &mobileTestReplayBarrier{entered: make(chan struct{}, 1), release: make(chan struct{})}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(barrier.release) }) }
	t.Cleanup(release)
	bridge.mu.Lock()
	bridge.pendingControl = append(bridge.pendingControl, map[string]any{"t": "heartbeat", "replayBarrier": barrier})
	bridge.mu.Unlock()
	relay.acceptRegistration(t, mobileTestListed(phone))
	select {
	case <-barrier.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("registration never reached pending replay")
	}
	if state, _ := bridge.client.snapshot(); state == mobileRelayConnected {
		t.Error("connected was published before the pending brief replay finished")
	}
	started, published := make(chan struct{}), make(chan error, 1)
	go func() {
		close(started)
		published <- bridge.publishBrief(newer, false)
	}()
	<-started
	overtook := false
	var publishErr error
	select {
	case publishErr = <-published:
		overtook = true
	case <-time.After(100 * time.Millisecond):
	}
	release()
	if !overtook {
		select {
		case publishErr = <-published:
		case <-time.After(5 * time.Second):
			t.Fatal("new brief stayed blocked after replay completed")
		}
	}
	if publishErr != nil {
		t.Fatal(publishErr)
	}
	if overtook {
		t.Fatal("a new brief overtook an older pending brief during registration replay")
	}
	for _, expected := range []MobileBriefV1{older, newer} {
		frame := relay.nextFrame(t, "brief")
		var sent mobileBriefFrame
		if err := json.Unmarshal(frame.raw, &sent); err != nil {
			t.Fatal(err)
		}
		if sent.AsOf != expected.AsOf {
			t.Fatalf("brief order: got %s, want %s", sent.AsOf, expected.AsOf)
		}
	}
}

func (gate *mobileTestRegistrationGate) relayRegistered(client *mobileRelayClient, presence []mobileRelayPresence, listed bool) {
	select {
	case gate.entered <- struct{}{}:
	default:
	}
	select {
	case <-gate.release:
		gate.bridge.relayRegistered(client, presence, listed)
	case <-gate.bridge.ctx.Done():
	}
}

func (gate *mobileTestRegistrationGate) relayFrame(kind string, raw []byte) {
	gate.bridge.relayFrame(kind, raw)
}

func (gate *mobileTestRegistrationGate) relayStateChanged() {
	gate.bridge.relayStateChanged()
}

func mobileTestGatedRegistration(t *testing.T) (*mobileBridge, *mobileTestRelay, *mobileTestRegistrationGate) {
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
	gate := &mobileTestRegistrationGate{bridge: bridge, entered: make(chan struct{}, 1), release: make(chan struct{})}
	client, err := newMobileRelayClient(relay.url(), identity, "Mac", gate)
	if err != nil {
		t.Fatal(err)
	}
	bridge.identity, bridge.client = &identity, client
	bridge.relay.Enabled = true
	t.Cleanup(bridge.Stop)
	client.start(bridge.ctx)
	return bridge, relay, gate
}

func (gate *mobileTestRegistrationGate) wait(t *testing.T) {
	t.Helper()
	select {
	case <-gate.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("registration did not reach the bridge callback")
	}
}

func TestMobileRelayConnectedWaitsForDeviceReconciliation(t *testing.T) {
	bridge, relay, gate := mobileTestGatedRegistration(t)
	kept, removed := newMobileTestPhone(t), newMobileTestPhone(t)
	mobileTestAddDevice(t, bridge, kept, []string{mobileScopeBriefRead})
	mobileTestAddDevice(t, bridge, removed, []string{mobileScopeBriefRead})
	seen := mobileTimestamp(time.Now().Add(-time.Minute))
	relay.acceptRegistration(t, []mobileTestListing{{DeviceID: kept.deviceID, LastSeenAt: &seen}})
	gate.wait(t)
	if state, _ := bridge.client.snapshot(); state == mobileRelayConnected {
		t.Fatal("connected was published before the relay's device list was reconciled")
	}
	close(gate.release)
	mobileTestWaitFor(t, "connected after reconciliation", func() bool {
		return bridge.connectionStatus().RelayState == mobileRelayConnected
	})
	status := bridge.connectionStatus()
	if len(status.Devices) != 1 || status.Devices[0].DeviceID != kept.deviceID || status.Devices[0].LastSeenAt != seen {
		t.Fatalf("connected status contains stale device data: %+v", status.Devices)
	}
}

func TestMobileRelayRegistrationCannotFlushAnOlderBriefAfterANewPublish(t *testing.T) {
	bridge, relay, gate := mobileTestGatedRegistration(t)
	phone := newMobileTestPhone(t)
	mobileTestAddDevice(t, bridge, phone, []string{mobileScopeBriefRead})
	older := MobileBriefV1{Version: 1, BriefID: "2026-10-09-morning", Slot: "morning", AsOf: "2026-10-09T07:00:00Z"}
	newer := MobileBriefV1{Version: 1, BriefID: "2026-10-09-evening", Slot: "evening", AsOf: "2026-10-09T18:00:00Z"}
	if err := bridge.publishBrief(older, false); err != nil {
		t.Fatal(err)
	}
	relay.acceptRegistration(t, mobileTestListed(phone))
	gate.wait(t)
	if err := bridge.publishBrief(newer, false); err != nil {
		t.Fatal(err)
	}
	close(gate.release)
	mobileTestWaitFor(t, "the completed registration", func() bool {
		return bridge.connectionStatus().RelayState == mobileRelayConnected
	})
	frame := relay.nextFrame(t, "brief")
	var first mobileBriefFrame
	if err := json.Unmarshal(frame.raw, &first); err != nil {
		t.Fatal(err)
	}
	if first.AsOf == older.AsOf {
		frame = relay.nextFrame(t, "brief")
		if err := json.Unmarshal(frame.raw, &first); err != nil {
			t.Fatal(err)
		}
	}
	if first.AsOf != newer.AsOf {
		t.Fatalf("newest brief was not delivered last: %+v", first)
	}
	// The old implementation sends newer before entering relayRegistered,
	// then flushes older after it. The relay would retain that stale document.
	relay.expectNoFrame(t, "brief", 100*time.Millisecond)
}
