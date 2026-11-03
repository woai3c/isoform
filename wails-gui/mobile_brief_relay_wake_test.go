package main

import (
	"encoding/json"
	"testing"

	"wails-gui/internal/platform/activitymon"
)

func TestMobileBriefDroppedWakeQueuesUntilFreshRelayRegistration(t *testing.T) {
	bridge, relay, first, phone := mobileTestSleepingRelay(t)
	clock := &fakeMobileBriefClock{now: mobileBriefTestDay(6, 0)}
	svc, _ := newTestMobileBriefService(t, clock)
	t.Cleanup(svc.Stop)
	// Keep the readable source stores when wiring the real local relay.
	bridge.app.store, bridge.app.handoffStore = svc.app.store, svc.app.handoffStore
	svc.app = bridge.app
	svc.systemStateChanged(activitymon.SystemStateChange{Kind: activitymon.SystemStateSleep, At: clock.Now()})
	clock.Set(mobileBriefTestDay(7, 20))
	svc.sleepState = func() activitymon.SystemStateChange {
		return activitymon.SystemStateChange{Kind: activitymon.SystemStateWake, At: clock.Now(), SleepAt: mobileBriefTestDay(6, 0)}
	}
	// The scheduler sees the cached native wake even though its channel event
	// never reached context signals. It must invalidate the relay itself.
	svc.checkDue(t.Context())
	brief, ok := svc.store.latest()
	if !ok || brief.Generation == nil {
		t.Fatal("the lost-wake fallback did not generate a brief")
	}
	bridge.mu.Lock()
	queued, pending := bridge.pendingBriefs[phone.deviceID]
	bridge.mu.Unlock()
	if !pending || queued.Notify || queued.AsOf != brief.AsOf {
		t.Fatalf("lost-wake brief entered the stale socket: queued=%+v pending=%v", queued, pending)
	}
	second := relay.acceptRegistration(t, mobileTestListed(phone))
	frame := relay.nextFrame(t, "brief")
	if frame.conn == first || frame.conn != second {
		t.Fatal("lost-wake brief used a stale registration")
	}
	var delivered mobileBriefFrame
	if err := json.Unmarshal(frame.raw, &delivered); err != nil {
		t.Fatal(err)
	}
	var received MobileBriefV1
	if err := json.Unmarshal(phone.openFromHub(t, bridge.identity.Public, bridge.identity.HubID, delivered.Env), &received); err != nil || received.BriefID != brief.BriefID {
		t.Fatalf("phone did not receive the recovered brief: %+v, %v", received, err)
	}
}
