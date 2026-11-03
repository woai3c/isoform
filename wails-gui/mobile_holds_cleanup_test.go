package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"wails-gui/internal/calendar"
	"wails-gui/internal/engagement"
	"wails-gui/internal/handoff"
)

func mobileHeldTaskHold(t *testing.T) (*mobileBridge, *fakeHoldEventStore, *time.Time, mobileDeviceRecord, mobileHoldProposal) {
	t.Helper()
	bridge, calendar, now := mobileHoldTestBridge(t)
	device := mobileTestDevice(mobileScopeHandoff, mobileScopeBriefRead)
	code, proposal := mobileHoldProposalCall(t, bridge, device, true)
	if code != http.StatusOK {
		t.Fatalf("proposal %d", code)
	}
	input := mobileHoldAction{IdempotencyKey: "hold", Action: "hold", Token: proposal.Token, Slot: proposal.Proposal.Slots[0], CalendarID: "work"}
	if got := mobileHoldActionCall(t, bridge, device, input, true); got.Code != http.StatusOK {
		t.Fatalf("hold %d %s", got.Code, got.Body.String())
	}
	*now = now.Add(6 * time.Second)
	if err := bridge.app.holds.runDue(); err != nil || len(calendar.saved) != 1 {
		t.Fatalf("write: %v %d", err, len(calendar.saved))
	}
	return bridge, calendar, now, device, proposal
}

func mobileTaskActionCall(t *testing.T, bridge *mobileBridge, device mobileDeviceRecord, key, action string) int {
	t.Helper()
	w := httptest.NewRecorder()
	bridge.performTaskAction(w, device, "plan", taskActionInput{IdempotencyKey: key, Action: action}, engagement.ChannelPush)
	return w.Code
}

func mobilePlanDetail(t *testing.T, bridge *mobileBridge) (MobileTaskDetail, handoff.Leftover) {
	t.Helper()
	entry, err := bridge.app.handoffStore.Get("plan")
	if err != nil || entry.Thread == nil || len(entry.Thread.Leftovers) != 1 {
		t.Fatalf("task %+v: %v", entry, err)
	}
	detail, visible := bridge.app.mobileTaskThreadDetail(t.Context(), entry, nil)
	if !visible || detail.Hold == nil {
		t.Fatalf("detail %+v", detail)
	}
	return detail, entry.Thread.Leftovers[0]
}

func TestMobileHoldCleanupAsksOnceAndKeepEndsIt(t *testing.T) {
	bridge, _, _, device, _ := mobileHeldTaskHold(t)
	if detail, _ := mobilePlanDetail(t, bridge); detail.HoldCleanup {
		t.Fatal("asked before the work was done")
	}
	if code := mobileTaskActionCall(t, bridge, device, "done", mobileTaskActionMarkDone); code != http.StatusOK {
		t.Fatalf("mark done %d", code)
	}
	detail, leftover := mobilePlanDetail(t, bridge)
	if !detail.HoldCleanup || leftover.State != handoff.LeftoverAsked {
		t.Fatalf("no clean-up ask: %+v %+v", detail.HoldCleanup, leftover)
	}
	if code := mobileTaskActionCall(t, bridge, device, "keep", mobileTaskActionKeepHold); code != http.StatusOK {
		t.Fatalf("keep %d", code)
	}
	detail, leftover = mobilePlanDetail(t, bridge)
	if detail.HoldCleanup || leftover.State != handoff.LeftoverKept || detail.Hold.State != "held" {
		t.Fatalf("keep did not end the asking: %+v %+v", detail.HoldCleanup, leftover)
	}
	if code := mobileTaskActionCall(t, bridge, device, "keep-again", mobileTaskActionKeepHold); code != http.StatusConflict {
		t.Fatalf("second keep %d", code)
	}
}

func TestMobileHoldCleanupRemoveDeletesTheEvent(t *testing.T) {
	bridge, calendar, _, device, proposal := mobileHeldTaskHold(t)
	if code := mobileTaskActionCall(t, bridge, device, "done", mobileTaskActionMarkDone); code != http.StatusOK {
		t.Fatalf("mark done %d", code)
	}
	remove := mobileHoldAction{IdempotencyKey: "remove", Action: "remove_hold", HoldID: proposal.Proposal.ID}
	if got := mobileHoldActionCall(t, bridge, device, remove, true); got.Code != http.StatusOK || len(calendar.removed) != 1 {
		t.Fatalf("remove %d %s", got.Code, got.Body.String())
	}
	detail, leftover := mobilePlanDetail(t, bridge)
	if detail.HoldCleanup || leftover.State != handoff.LeftoverRemoved {
		t.Fatalf("after remove %+v %+v", detail.HoldCleanup, leftover)
	}
	restore := mobileHoldAction{IdempotencyKey: "restore", Action: "undo_remove_hold", HoldID: proposal.Proposal.ID}
	if got := mobileHoldActionCall(t, bridge, device, restore, true); got.Code != http.StatusOK {
		t.Fatalf("restore %d %s", got.Code, got.Body.String())
	}
	if detail, leftover = mobilePlanDetail(t, bridge); detail.HoldCleanup || leftover.State != handoff.LeftoverKept {
		t.Fatalf("undo remove asked again %+v %+v", detail.HoldCleanup, leftover)
	}
}

func TestMobileHoldCleanupNotAskedAfterTheHoldStarted(t *testing.T) {
	bridge, _, now, device, proposal := mobileHeldTaskHold(t)
	start, err := time.Parse(time.RFC3339, proposal.Proposal.Slots[0].Start)
	if err != nil {
		t.Fatal(err)
	}
	*now = start.Add(time.Minute)
	if code := mobileTaskActionCall(t, bridge, device, "done", mobileTaskActionMarkDone); code != http.StatusOK {
		t.Fatalf("mark done %d", code)
	}
	if detail, leftover := mobilePlanDetail(t, bridge); detail.HoldCleanup || leftover.State != handoff.LeftoverPlaced {
		t.Fatalf("asked after start %+v %+v", detail.HoldCleanup, leftover)
	}
}

func TestMobileHoldFilledUpNamesTheEvent(t *testing.T) {
	bridge, store, _ := mobileHoldTestBridge(t)
	device := mobileTestDevice(mobileScopeHandoff, mobileScopeBriefRead)
	code, proposal := mobileHoldProposalCall(t, bridge, device, true)
	if code != http.StatusOK || len(proposal.Proposal.Slots) < 2 {
		t.Fatalf("proposal %d %+v", code, proposal.Proposal.Slots)
	}
	slot := proposal.Proposal.Slots[0]
	store.events = []calendar.RawEvent{{UID: "standup", CalendarID: "work", Title: "Design review", Start: slot.Start, End: slot.End}}
	input := mobileHoldAction{IdempotencyKey: "hold", Action: "hold", Token: proposal.Token, Slot: slot, CalendarID: "work"}
	var reply struct {
		Data HoldStatus `json:"data"`
	}
	got := mobileHoldActionCall(t, bridge, device, input, true)
	if err := json.Unmarshal(got.Body.Bytes(), &reply); err != nil || got.Code != http.StatusOK {
		t.Fatalf("hold %d %s", got.Code, got.Body.String())
	}
	if reply.Data.State != "conflict" || reply.Data.ConflictTitle != "Design review" || reply.Data.ConflictStart == "" || reply.Data.Start == "" || reply.Data.Start == slot.Start {
		t.Fatalf("conflict %+v", reply.Data)
	}
}
