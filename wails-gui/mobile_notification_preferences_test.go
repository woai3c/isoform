package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"wails-gui/internal/engagement"
)

func TestMobileNotificationPreferencesAuthorizationValidationAndOfflineReplay(t *testing.T) {
	bridge, store := mobileTestChatBridge(t)
	device := mobileTestDevice(mobileScopePush)
	body := `{"idempotencyKey":"queued-quiet-hours","quietStart":"23:15","quietEnd":"07:45"}`
	call := func(device mobileDeviceRecord, payload string, want int) string {
		t.Helper()
		response := mobileTestChatCall(bridge, device, http.MethodPost, "/notifications/preferences", payload)
		if response.Code != want {
			t.Fatalf("status = %d, want %d: %s", response.Code, want, response.Body.String())
		}
		return response.Body.String()
	}
	call(mobileTestDevice(mobileScopeBriefRead), body, http.StatusForbidden)
	// A queued phone write is retryable until the computer's engagement store is ready.
	call(device, body, http.StatusServiceUnavailable)
	bridge.app.engagement = newEngagementService(store, nil, engagementDelivery{})
	svc := bridge.app.engagement
	if err := svc.SetRules(context.Background(), engagement.QuietWindow{Start: 21 * time.Hour, End: 9 * time.Hour}, 3); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetKindEnabled(context.Background(), engagement.KindBriefReady, false); err != nil {
		t.Fatal(err)
	}
	before := svc.Prefs()
	for name, invalid := range map[string]string{
		"missing key":   `{"quietStart":"22:00","quietEnd":"08:00"}`,
		"missing start": `{"idempotencyKey":"bad","quietEnd":"08:00"}`,
		"missing end":   `{"idempotencyKey":"bad","quietStart":"22:00"}`,
		"out of range":  `{"idempotencyKey":"bad","quietStart":"24:00","quietEnd":"08:00"}`,
		"short hour":    `{"idempotencyKey":"bad","quietStart":"2:00","quietEnd":"08:00"}`,
		"extra field":   `{"idempotencyKey":"bad","quietStart":"22:00","quietEnd":"08:00","dailyInterruptBudget":0}`,
		"wrong type":    `{"idempotencyKey":"bad","quietStart":2200,"quietEnd":"08:00"}`,
	} {
		t.Run(name, func(t *testing.T) { call(device, invalid, http.StatusBadRequest) })
	}
	if svc.Prefs().Quiet != before.Quiet {
		t.Fatal("invalid requests changed quiet hours")
	}
	first := call(device, body, http.StatusOK)
	var envelope struct {
		Data EngagementSettingsView `json:"data"`
	}
	if err := json.Unmarshal([]byte(first), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.QuietStart != "23:15" || envelope.Data.QuietEnd != "07:45" || envelope.Data.DailyInterruptBudget != 3 {
		t.Fatalf("updated view: %+v", envelope.Data)
	}
	if got := svc.Prefs(); got.Enabled[engagement.KindBriefReady] || len(got.DigestTimes) != len(before.DigestTimes) {
		t.Fatal("quiet hours changed unrelated preferences")
	}
	// Simulate a lost acknowledgement, an intervening computer edit, then replay
	// from the phone's durable queue. The receipt wins; the edit is not overwritten.
	if err := svc.SetRules(context.Background(), engagement.QuietWindow{Start: 20 * time.Hour, End: 6 * time.Hour}, 4); err != nil {
		t.Fatal(err)
	}
	if replay := call(device, body, http.StatusOK); replay != first {
		t.Fatal("offline replay did not return the original acknowledgement")
	}
	if got := svc.Prefs(); got.Quiet.Start != 20*time.Hour || got.DailyInterruptBudget != 4 {
		t.Fatal("offline replay overwrote a newer computer change")
	}
	fresh := newEngagementService(store, nil, engagementDelivery{})
	if err := fresh.load(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := fresh.Prefs(); got.Quiet.Start != 20*time.Hour || got.Quiet.End != 6*time.Hour || got.DailyInterruptBudget != 4 {
		t.Fatalf("persisted preferences did not survive reload: %+v", got)
	}
	call(device, `{"idempotencyKey":"no-quiet-hours","quietStart":"00:00","quietEnd":"00:00"}`, http.StatusOK)
	if svc.Prefs().Quiet.Contains(time.Now()) {
		t.Fatal("equal start and end must disable quiet hours")
	}
}

func TestEngagementRulesFailurePreservesEffectivePreferences(t *testing.T) {
	rig := newEngagementTestRig(t)
	before := rig.svc.Prefs()
	if err := rig.store.Close(); err != nil {
		t.Fatal(err)
	}
	err := rig.svc.SetRules(context.Background(), engagement.QuietWindow{Start: 23 * time.Hour, End: 7 * time.Hour}, 2)
	if err == nil {
		t.Fatal("a closed store must refuse the change")
	}
	if after := rig.svc.Prefs(); after.Quiet != before.Quiet || after.DailyInterruptBudget != before.DailyInterruptBudget {
		t.Fatal("a failed persistence changed effective preferences")
	}
}

func TestMobileNotificationAndComputerRuleWritesRemainConsistent(t *testing.T) {
	bridge, store := mobileTestChatBridge(t)
	bridge.app.engagement = newEngagementService(store, nil, engagementDelivery{})
	svc := bridge.app.engagement
	device := mobileTestDevice(mobileScopePush)
	var group sync.WaitGroup
	for index := 0; index < 20; index++ {
		group.Add(2)
		go func(index int) {
			defer group.Done()
			err := svc.SetRules(context.Background(), engagement.QuietWindow{Start: time.Duration(index) * time.Hour, End: 7 * time.Hour}, index)
			if err != nil {
				t.Error(err)
			}
		}(index)
		go func(index int) {
			defer group.Done()
			body := fmt.Sprintf(`{"idempotencyKey":"quiet-%d","quietStart":"%02d:30","quietEnd":"08:00"}`, index, index)
			response := mobileTestChatCall(bridge, device, http.MethodPost, "/notifications/preferences", body)
			if response.Code != http.StatusOK {
				t.Errorf("phone write: %d %s", response.Code, response.Body.String())
			}
		}(index)
	}
	group.Wait()
	fresh := newEngagementService(store, nil, engagementDelivery{})
	if err := fresh.load(context.Background()); err != nil {
		t.Fatal(err)
	}
	want, got := svc.Prefs(), fresh.Prefs()
	if got.Quiet != want.Quiet || got.DailyInterruptBudget != want.DailyInterruptBudget {
		t.Fatalf("stored rules and active rules diverged: got %+v, want %+v", got, want)
	}
}
