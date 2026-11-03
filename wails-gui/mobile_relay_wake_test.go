package main

import (
	"encoding/json"
	"testing"
	"time"

	"wails-gui/internal/platform/activitymon"
)

func mobileTestSleepingRelay(t *testing.T) (*mobileBridge, *mobileTestRelay, *mobileTestRelayConn, *mobileTestPhone) {
	t.Helper()
	mobileTestTimes(t, func(timing *mobileRelayTiming) {
		timing.Heartbeat = time.Hour
		timing.ReadTimeout = time.Hour
	})
	bridge, relay := mobileTestHub(t)
	phone := newMobileTestPhone(t)
	mobileTestAddDevice(t, bridge, phone, []string{mobileScopeBriefRead})
	first := relay.acceptRegistration(t, mobileTestListed(phone))
	mobileTestWaitFor(t, "the connection before sleep", func() bool {
		return bridge.connectionStatus().RelayState == mobileRelayConnected
	})
	bridge.app.mobileBridgeMu.Lock()
	bridge.app.mobileBridge = bridge
	bridge.app.mobileBridgeMu.Unlock()
	first.blackhole.Store(true)
	return bridge, relay, first, phone
}

func TestMobileRelayWakeRedialsADeadRegisteredSocket(t *testing.T) {
	bridge, relay, first, phone := mobileTestSleepingRelay(t)
	bridge.app.mobileRelayRecovered()
	// The old socket remains writable. Its hour-long timeout must not be the
	// thing that eventually makes wake recovery attempt another connection.
	second := relay.acceptRegistration(t, mobileTestListed(phone))
	if second == first {
		t.Fatal("wake retained the stale pre-sleep connection")
	}
	mobileTestWaitFor(t, "the registration after wake", func() bool {
		return bridge.connectionStatus().RelayState == mobileRelayConnected
	})
}

func TestMobileRelayGoodbyeUsesRegisteredConnection(t *testing.T) {
	bridge, relay := mobileTestHub(t)
	phone := newMobileTestPhone(t)
	mobileTestAddDevice(t, bridge, phone, []string{mobileScopeBriefRead})
	relay.acceptRegistration(t, mobileTestListed(phone))
	mobileTestWaitFor(t, "registered relay", func() bool { return bridge.connectionStatus().RelayState == mobileRelayConnected })
	bridge.app.mobileBridgeMu.Lock()
	bridge.app.mobileBridge = bridge
	bridge.app.mobileBridgeMu.Unlock()
	for _, cause := range []string{"sleeping", "shutting_down", "closing"} {
		expectedReason := ""
		if cause == "sleeping" {
			expectedReason = bridge.keepAwake.SleepReason()
		}
		bridge.app.mobileRelayGoodbye(cause)
		frame := relay.nextFrame(t, "goodbye")
		var got struct {
			Cause  string `json:"cause"`
			Reason string `json:"reason"`
		}
		if err := json.Unmarshal(frame.raw, &got); err != nil || got.Cause != cause || got.Reason != expectedReason {
			t.Fatalf("goodbye = %s, %v", frame.raw, err)
		}
	}
}

func TestMobileRelayWakeBriefWaitsForFreshRegistrationAndReachesPhone(t *testing.T) {
	bridge, relay, first, phone := mobileTestSleepingRelay(t)
	bridge.app.mobileRelayRecovered()
	brief := MobileBriefV1{Version: 1, BriefID: "2026-10-09-morning", Slot: "morning", AsOf: "2026-10-09T14:00:00Z"}
	if err := bridge.publishBrief(brief, false); err != nil {
		t.Fatal(err)
	}
	bridge.mu.Lock()
	queued, pending := bridge.pendingBriefs[phone.deviceID]
	bridge.mu.Unlock()
	if !pending || queued.AsOf != brief.AsOf || queued.Notify {
		t.Fatalf("wake brief was lost into the stale socket instead of queued: %+v, pending=%v", queued, pending)
	}
	second := relay.acceptRegistration(t, mobileTestListed(phone))
	frame := relay.nextFrame(t, "brief")
	if frame.conn == first || frame.conn != second {
		t.Fatal("wake brief used a connection that had not registered since wake")
	}
	var delivered mobileBriefFrame
	if err := json.Unmarshal(frame.raw, &delivered); err != nil {
		t.Fatal(err)
	}
	var received MobileBriefV1
	if err := json.Unmarshal(phone.openFromHub(t, bridge.identity.Public, bridge.identity.HubID, delivered.Env), &received); err != nil || received.BriefID != brief.BriefID || delivered.Notify {
		t.Fatalf("phone did not receive the non-notifying wake brief: %+v, %v", received, err)
	}
}

func TestMobileRelayWakeInvalidatesBeforeBriefServiceIsNotified(t *testing.T) {
	bridge, _, _, _ := mobileTestSleepingRelay(t)
	clock := &fakeMobileBriefClock{now: mobileBriefTestDay(14, 0)}
	svc, _ := newTestMobileBriefService(t, clock)
	t.Cleanup(svc.Stop)
	bridge.app.mobileBridgeMu.Lock()
	bridge.app.mobileBriefSvc = svc
	bridge.app.mobileBridgeMu.Unlock()
	signals := newContextSignalService(bridge.app)
	signals.emit = nil
	svc.mu.Lock()
	started, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		close(started)
		signals.handleChange(t.Context(), activitymon.SystemStateChange{Kind: activitymon.SystemStateWake, At: clock.Now()})
	}()
	<-started
	deadline := time.Now().Add(time.Second)
	invalidated := false
	for time.Now().Before(deadline) {
		state, _ := bridge.client.snapshot()
		if state != mobileRelayConnected {
			invalidated = true
			break
		}
		time.Sleep(time.Millisecond)
	}
	svc.mu.Unlock()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("wake hook stayed blocked after the brief service was released")
	}
	if !invalidated {
		t.Fatal("wake notified the brief service before invalidating the old relay socket")
	}
}

func TestMobileRelayDuplicateWakeKeepsTheFreshConnection(t *testing.T) {
	bridge, relay, first, phone := mobileTestSleepingRelay(t)
	bridge.app.mobileRelayRecovered()
	second := relay.acceptRegistration(t, mobileTestListed(phone))
	if second == first {
		t.Fatal("wake retained the stale pre-sleep connection")
	}
	mobileTestWaitFor(t, "the registration after wake", func() bool {
		return bridge.connectionStatus().RelayState == mobileRelayConnected
	})
	// The same wake reported again must not close the connection it just made.
	bridge.app.mobileRelayRecovered()
	if state := bridge.connectionStatus().RelayState; state != mobileRelayConnected {
		t.Fatalf("a duplicate wake dropped the fresh connection: %q", state)
	}
}
