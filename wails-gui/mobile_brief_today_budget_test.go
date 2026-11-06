package main

import (
	"testing"
	"time"
)

// R24: a read of Today refreshes its live signals only when the last refresh is
// past the resync budget, and a burst of source events inside the gap costs one
// queued refresh, never one refresh per event.
func TestTodayRefreshIsCoalescedAndBudgeted(t *testing.T) {
	app := newMobileBriefPurposeTestApp(t)
	t.Setenv("YANSU_MOBILE_STATE_DIR", t.TempDir())
	svc := newMobileBriefService(app)
	now := time.Now()
	if !svc.todayRefreshDue(now) {
		t.Fatal("the first read must start a refresh")
	}
	svc.todayRefreshMu.Lock()
	svc.todayRefreshAt = now
	svc.todayRefreshMu.Unlock()
	if svc.todayRefreshDue(now.Add(todayRefreshBudget - time.Second)) {
		t.Fatal("a read inside the budget started a refresh")
	}
	if !svc.todayRefreshDue(now.Add(todayRefreshBudget)) {
		t.Fatal("a read past the budget must start a refresh")
	}
	for range 20 {
		svc.refreshTodaySoon()
	}
	svc.todayRefreshMu.Lock()
	queued, running := svc.todayRefreshQueued, svc.todayRefreshing
	svc.todayRefreshMu.Unlock()
	if running || !queued {
		t.Fatalf("a burst inside the gap: running=%v queued=%v, want one queued refresh and none running", running, queued)
	}
	if wait := svc.todayRefreshWaitLocked(now); wait <= 0 || wait > todayRefreshGap {
		t.Fatalf("the queued refresh waits %v, want at most the gap %v", wait, todayRefreshGap)
	}
}
