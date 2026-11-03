package main

import (
	"strings"
	"testing"

	"wails-gui/internal/handoff"
)

func TestResolveAgencyHandoffExecutionRevalidatesEveryLifecycleBoundary(
	t *testing.T,
) {
	tests := []struct {
		name          string
		status        handoff.Status
		body          string
		storedSession string
		inputSession  string
		deleteEntry   bool
		wantAllowed   bool
		wantError     string
	}{
		{name: "in progress", status: handoff.StatusInProgress, body: "retained prompt", storedSession: "session-1", inputSession: "session-1", wantAllowed: true},
		{name: "completed", status: handoff.StatusCompleted, body: "retained prompt", storedSession: "session-1", inputSession: "session-1", wantAllowed: true},
		{name: "active", status: handoff.StatusActive, body: "retained prompt", storedSession: "session-1", inputSession: "session-1", wantError: "no longer available"},
		{name: "stale", status: handoff.StatusStale, body: "retained prompt", storedSession: "session-1", inputSession: "session-1", wantError: "no longer available"},
		{name: "aborted", status: handoff.StatusAborted, body: "retained prompt", storedSession: "session-1", inputSession: "session-1", wantError: "no longer available"},
		{name: "session mismatch", status: handoff.StatusInProgress, body: "retained prompt", storedSession: "session-1", inputSession: "session-2", wantError: "no longer matches"},
		{name: "missing body", status: handoff.StatusInProgress, storedSession: "session-1", inputSession: "session-1", wantError: "no retained conversation context"},
		{name: "missing Handoff", status: handoff.StatusInProgress, body: "retained prompt", storedSession: "session-1", inputSession: "session-1", deleteEntry: true, wantError: "no longer available"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, err := handoff.NewStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			entry, err := store.Save(handoff.Entry{
				ID:            "handoff-lifecycle",
				Title:         "Continue bounded work",
				Status:        test.status,
				Source:        handoff.SourceAgency,
				ChatSessionID: test.storedSession,
				Body:          test.body,
			})
			if err != nil {
				t.Fatal(err)
			}
			if test.deleteEntry {
				if err := store.Delete(entry.ID); err != nil {
					t.Fatal(err)
				}
			}
			result, resolveErr := (&App{handoffStore: store}).ResolveAgencyHandoffExecution(
				AgencyHandoffContinuationInput{
					HandoffID: entry.ID,
					SessionID: test.inputSession,
				},
			)
			if test.wantAllowed {
				if resolveErr != nil || result.Prompt != test.body ||
					result.SessionID != test.storedSession {
					t.Fatalf("resolved=%+v err=%v", result, resolveErr)
				}
				return
			}
			if resolveErr == nil || !strings.Contains(resolveErr.Error(), test.wantError) {
				t.Fatalf("resolve error=%v want substring %q", resolveErr, test.wantError)
			}
		})
	}
}

// A continued run re-enters its chat through the normal send path, so it gets
// the same narrow prompt as any one-time work: brief, workdir and result
// contract, never the personal context blocks.
func TestContinuedHandoffRunGetsTheTaskNotThePerson(t *testing.T) {
	rig := newAgencyHandoffTestRig(t)
	accepted, err := rig.app.AcceptHandoff(rig.entry.ID)
	if err != nil {
		t.Fatalf("start verified Agency work: %v", err)
	}
	resolved, err := rig.app.ResolveAgencyHandoffExecution(AgencyHandoffContinuationInput{
		HandoffID: rig.entry.ID,
		SessionID: accepted.SessionID,
	})
	if err != nil {
		t.Fatal(err)
	}
	seedHandoffBoundaryContext(t, rig.app, resolved.SessionID)
	runner := wireHandoffPromptCapture(t, rig.app)

	composed := sendHandoffBoundaryPrompt(t, rig.app, runner, resolved.Prompt, resolved.Workdir, resolved.SessionID)

	for _, want := range []string{rig.entry.Body, "<handoff-workdir>", handoffResultFence} {
		if !strings.Contains(composed, want) {
			t.Errorf("continued handoff prompt lost %q", want)
		}
	}
	for _, skipped := range handoffSkippedBlocks {
		if strings.Contains(composed, skipped) {
			t.Errorf("continued handoff prompt carries personal context %q", skipped)
		}
	}
	waitForHandoffSettle(t, rig.handoffTestRig, rig.entry.ID)
}
