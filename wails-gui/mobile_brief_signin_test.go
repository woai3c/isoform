package main

import (
	"context"
	"testing"
	"time"
	"yansu-agent/agent/native"
)

func TestBriefSignedOutWaitsAndResumesOnce(t *testing.T) {
	clock := &fakeMobileBriefClock{now: mobileBriefTestDay(7, 5)}
	svc, _ := missedBriefRig(t, clock)
	state := "signed_out"
	svc.assistantState = func(_ context.Context, id string) string {
		if id != "codex" {
			t.Fatalf("assistant = %q", id)
		}
		return state
	}
	calls := 0
	svc.gather = func(context.Context, string, time.Time, time.Time) (mobileBriefInput, error) {
		calls++
		return mobileBriefInput{}, mobileBriefAttributeAssistantError(&native.HTTPError{Status: 401}, "codex")
	}
	svc.checkDue(context.Background())
	h := svc.Health()
	if calls != 1 || h.PendingCause != "signed_out" || h.PendingAssistant != "codex" || h.RetryAt != "" {
		t.Fatalf("first: calls %d health %+v", calls, h)
	}
	clock.Advance(30 * time.Minute)
	svc.checkDue(context.Background())
	if calls != 1 {
		t.Fatalf("retried while signed out: %d", calls)
	}
	state = "signed_in"
	svc.mu.Lock()
	svc.running = true
	svc.mu.Unlock()
	svc.checkDue(context.Background())
	if calls != 1 {
		t.Fatalf("started while another brief was running: %d", calls)
	}
	svc.mu.Lock()
	svc.running = false
	svc.mu.Unlock()
	svc.checkDue(context.Background())
	if calls != 2 {
		t.Fatalf("sign-in did not resume once: %d", calls)
	}
	clock.Advance(time.Minute)
	svc.checkDue(context.Background())
	if calls != 2 {
		t.Fatalf("repeated retry without another sign-in: %d", calls)
	}
	// The saved failure still blocks a fresh service after restart.
	svc.authResumed = nil
	state = "signed_out"
	svc.checkDue(context.Background())
	if calls != 2 {
		t.Fatalf("restart retried while signed out: %d", calls)
	}
}

func TestBriefSignedOutMenuRow(t *testing.T) {
	h := MobileBriefHealth{PendingSlot: "morning", PendingCause: "signed_out", PendingAssistant: "codex", PendingAttempts: 3}
	for _, tc := range []struct{ lang, title string }{{"en", "Tried 3 times. Codex is signed out."}, {"zh-CN", "已尝试 3 次。Codex 已退出登录。"}} {
		row := menuBarBriefRow(menuBarWords(tc.lang), tc.lang, MobileBriefV1{}, false, false, h, time.Now())
		if row.Kind != "sign_in" || row.ID != "codex" || row.Title != tc.title {
			t.Fatalf("%s: %+v", tc.lang, row)
		}
	}
}

func TestBriefHealthChangeWakesScheduler(t *testing.T) {
	svc := &mobileBriefService{wake: make(chan struct{}, 1)}
	app := &App{mobileBriefSvc: svc}
	app.assistantHealthDidChange()
	select {
	case <-svc.wake:
	default:
		t.Fatal("health change did not wake scheduler")
	}
}

func TestBriefSignedOutInvalidatesCachedProbe(t *testing.T) {
	clock := &fakeMobileBriefClock{now: mobileBriefTestDay(7, 5)}
	svc, _ := missedBriefRig(t, clock)
	cache := &assistantProbeCache{expires: time.Now().Add(time.Hour)}
	svc.app.assistantProbes = cache
	occurrence := mobileBriefSlotOccurrence{BriefID: "2026-10-09-morning", Slot: "morning", ScheduledAt: clock.Now()}
	svc.recordFailure(occurrence, mobileBriefAttributeAssistantError(&native.HTTPError{Status: 401}, "codex"))
	cache.mu.Lock()
	expires := cache.expires
	cache.mu.Unlock()
	if !expires.IsZero() {
		t.Fatalf("stale sign-in probe still valid until %v", expires)
	}
}
