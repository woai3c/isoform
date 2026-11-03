package main

import (
	"errors"
	"net/http"
	"reflect"
	"testing"

	"wails-gui/internal/handoff"
)

func TestMobileTaskHandoffStartRunsAndReplays(t *testing.T) {
	rig := newHandoffTestRig(t)
	entry, err := rig.app.createTaskEntry(CreateTaskInput{Title: "Run tests", Brief: "hold"}, handoff.SourceMobile)
	if err != nil {
		t.Fatal(err)
	}
	bridge, err := newMobileBridge(rig.app, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bridge.Stop)
	turns := &mobileTestTurns{}
	mobileChatRunnerOverride = mobileTestMessageOverride(turns)
	t.Cleanup(func() { mobileTestResetChatRunner(t, bridge) })
	device := mobileTestDevice(mobileScopeHandoff)
	path := "/handoffs/" + entry.ID + "/start"
	response := mobileTestChatCall(bridge, device, http.MethodPost, path, `{"idempotencyKey":"start"}`)
	if response.Code != http.StatusAccepted {
		t.Fatalf("start: %d %s", response.Code, response.Body.String())
	}
	mobileTestWaitFor(t, "handoff prompt", func() bool { return turns.count() == 1 })
	saved, _ := rig.handoffs.Get(entry.ID)
	if saved.Status != handoff.StatusInProgress || mobileTaskView(saved).Group != mobileTaskGroupRunning || saved.ChatSessionID == "" {
		t.Fatalf("started task: %+v", saved)
	}
	if !bridge.chatRunning(saved.ChatSessionID) {
		t.Fatal("handoff did not use the cancellable chat turn")
	}
	replay := mobileTestChatCall(bridge, device, http.MethodPost, path, `{"idempotencyKey":"start"}`)
	if replay.Code != response.Code || replay.Body.String() != response.Body.String() || turns.count() != 1 {
		t.Fatalf("replay: %d %s", replay.Code, replay.Body.String())
	}
	if !bridge.cancelChatTurn(saved.ChatSessionID) {
		t.Fatal("normal chat cancel did not stop handoff turn")
	}
}

func TestMobileTaskHandoffStartRefusesUnstartableAndUnscoped(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status handoff.Status
		source handoff.Source
		scope  string
		code   int
	}{
		{"stale agency", handoff.StatusStale, handoff.SourceAgency, mobileScopeHandoff, http.StatusConflict},
		{"unverified agency", handoff.StatusActive, handoff.SourceAgency, mobileScopeHandoff, http.StatusConflict},
		{"completed", handoff.StatusCompleted, handoff.SourceMobile, mobileScopeHandoff, http.StatusConflict},
		{"already running", handoff.StatusInProgress, handoff.SourceMobile, mobileScopeHandoff, http.StatusConflict},
		{"scope", handoff.StatusActive, handoff.SourceMobile, mobileScopeBriefRead, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rig := newHandoffTestRig(t)
			original, err := rig.handoffs.Save(handoff.Entry{ID: "hf-refused", Title: "Work", Body: "Do work", Status: tc.status, Source: tc.source})
			if err != nil {
				t.Fatal(err)
			}
			bridge, err := newMobileBridge(rig.app, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(bridge.Stop)
			response := mobileTestChatCall(bridge, mobileTestDevice(tc.scope), http.MethodPost, "/handoffs/hf-refused/start", `{"idempotencyKey":"refused"}`)
			if response.Code != tc.code {
				t.Fatalf("response: %d %s", response.Code, response.Body.String())
			}
			saved, _ := rig.handoffs.Get(original.ID)
			if !reflect.DeepEqual(saved, original) {
				t.Fatalf("refused entry changed: %+v", saved)
			}
		})
	}
}

func TestMobileTaskHandoffSendFailureRollsBackClaimAndNewSession(t *testing.T) {
	rig := newHandoffTestRig(t)
	original, err := rig.handoffs.Save(handoff.Entry{ID: "hf-fail", Title: "Work", Body: "Do work", Status: handoff.StatusBlocked,
		Source: handoff.SourceMobile, BlockedReason: handoff.BlockedTimeout, AttemptID: "previous", AttemptStartedAt: 100, LastActivityAt: 200})
	if err != nil {
		t.Fatal(err)
	}
	var session string
	_, err = rig.app.acceptHandoff(original.ID, func(result HandoffAcceptResult) error {
		session = result.SessionID
		return errors.New("send unavailable")
	})
	if err == nil {
		t.Fatal("failed send accepted")
	}
	saved, _ := rig.handoffs.Get(original.ID)
	saved.UpdatedAt = original.UpdatedAt
	if !reflect.DeepEqual(saved, original) {
		t.Fatalf("rollback: got %+v want %+v", saved, original)
	}
	if session == "" || rig.app.chatSessionExists(session) {
		t.Fatal("new execution chat was not discarded")
	}
}

func TestMobileTaskHandoffStartEmptyPromptRollsBack(t *testing.T) {
	rig := newHandoffTestRig(t)
	original, err := rig.handoffs.Save(handoff.Entry{ID: "hf-empty", Title: "Work", Status: handoff.StatusActive, Source: handoff.SourceMobile})
	if err != nil {
		t.Fatal(err)
	}
	bridge, err := newMobileBridge(rig.app, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bridge.Stop)
	response := mobileTestChatCall(bridge, mobileTestDevice(mobileScopeHandoff), http.MethodPost, "/handoffs/hf-empty/start", `{"idempotencyKey":"empty"}`)
	if response.Code != http.StatusConflict {
		t.Fatalf("response: %d %s", response.Code, response.Body.String())
	}
	saved, _ := rig.handoffs.Get(original.ID)
	saved.UpdatedAt = original.UpdatedAt
	if !reflect.DeepEqual(saved, original) {
		t.Fatalf("failed chat send changed entry: %+v", saved)
	}
}
