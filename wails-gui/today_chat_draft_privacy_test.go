package main

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/pii"
	"wails-gui/storage"
	"yansu-agent/agent"
)

func TestTodayChatNamedDraftRechecksRenamedAndForgottenSources(t *testing.T) {
	for _, change := range []string{"renamed", "forgotten", "stale people"} {
		t.Run(change, func(t *testing.T) {
			app, tag, ref := todaySendTestApp(t)
			draft, err := app.PrepareTodayChatDraft(ref)
			if err != nil {
				t.Fatal(err)
			}
			ref.GeneratedTitle = draft.Prefill.Title
			prompt := "Where do I stand on \"" + draft.Prefill.Title + "\"? Focus on Friday."
			prepared, err := app.PrepareTodayChatTurn(tag, prompt, ref)
			if err != nil || prepared != prompt {
				t.Fatalf("draft changed: %q, %v", prepared, err)
			}
			switch change {
			case "renamed":
				if _, err := app.SaveMemoryEntry("launch", "# Current public title\nCurrent source."); err != nil {
					t.Fatal(err)
				}
				fresh, err := app.PrepareTodayChatTurn(tag, prompt, ref)
				if err != nil || fresh != "Where do I stand on \"Current public title\"? Focus on Friday." {
					t.Fatalf("title refresh changed user additions: %q, %v", fresh, err)
				}
			case "forgotten":
				if err := app.DeleteMemoryEntry("launch"); err != nil {
					t.Fatal(err)
				}
				if _, err := app.PrepareTodayChatTurn(tag, prompt, ref); !errors.Is(err, errTodayChatItemUnavailable) {
					t.Fatalf("forgotten draft before persistence: %v", err)
				}
			case "stale people":
				ref.GeneratedPeople = []string{"Former attendee"}
				if _, err := app.PrepareTodayChatTurn(tag, prompt, ref); !errors.Is(err, errTodayChatItemUnavailable) {
					t.Fatalf("stale attendees before persistence: %v", err)
				}
			}
			runner := &upcomingCaptureRunner{inputs: make(chan *agent.TaskInput, 1)}
			app.runner = runner
			_, err = app.SendPromptWithTodayContext(prepared, "", "m", "", tag, nil, nil, false, false, "en", "", StreamItemData{ID: "today-user", Kind: "user"}, ref)
			if !errors.Is(err, errTodayChatItemUnavailable) {
				t.Fatalf("unsafe named draft sent: %v", err)
			}
			select {
			case <-runner.inputs:
				t.Fatal("stale draft reached model")
			default:
			}
		})
	}
}

func TestTodayChatNamedDraftFiltersPartialPurposeTitleBeforePersistAndSend(t *testing.T) {
	app := newTodayContextTestApp(t, true)
	app.activeTags = map[string]bool{}
	app.eventBroadcaster = NewEventBroadcaster()
	main, err := app.ensureMainChat(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().Add(-time.Minute).UnixMilli()
	if err := app.activity.store.SaveSession(ActivitySessionRow{ID: "original", AppNames: `["Notes"]`, StartTime: base, EndTime: base + 1}, nil, nil); err != nil {
		t.Fatal(err)
	}
	evidence := purposeTestEvidence("original", PurposeEvidenceObservedUser, PurposeStanceSupport, base)
	evidence.SourceKind, evidence.SourceID = "activity_session", "original"
	const confidential = "Atlas confidential acquisition title"
	detail := createTodayContextPurpose(t, app, "original", confidential, evidence)
	store, _ := app.purposeProductStore()
	update := purposeTestAppendInput(detail, "public-outcome", PurposeEventRevised, PurposeStanceUpdate, PurposeEvidenceExplicitUser, "public-outcome", base+10)
	update.Evidence[0].SourceKind = "explicit_user_statement"
	update.DesiredOutcome = "Public launch checklist is complete."
	if _, _, err := store.AppendEvent(update); err != nil {
		t.Fatal(err)
	}
	ref := TodayChatContextRef{SourceKind: "purpose", SourceID: detail.OpenLoop.ID}
	draft, err := app.PrepareTodayChatDraft(ref)
	if err != nil {
		t.Fatal(err)
	}
	ref.GeneratedTitle = draft.Prefill.Title
	prompt := "Where do I stand on \"" + draft.Prefill.Title + "\"? Focus on Friday."
	previous := pii.GetUserSensitiveApps()
	t.Cleanup(func() { pii.SetUserSensitiveApps(previous) })
	pii.SetUserSensitiveApps(map[string]string{"com.apple.Notes": "Notes"})
	fresh, err := app.PrepareTodayChatDraft(ref)
	if err != nil || strings.Contains(fresh.Prefill.Title, confidential) {
		t.Fatalf("partial Purpose preview retained private title: %+v, %v", fresh, err)
	}
	prepared, err := app.PrepareTodayChatTurn(main.LocalID, prompt, ref)
	if err != nil || strings.Contains(prepared, confidential) || !strings.HasSuffix(prepared, "Focus on Friday.") {
		t.Fatalf("private title before history: %q, %v", prepared, err)
	}
	runner := &upcomingCaptureRunner{inputs: make(chan *agent.TaskInput, 1)}
	app.runner = runner
	if _, err := app.SendPromptWithTodayContext(prompt, "", "m", "", main.LocalID, nil, nil, false, false, "en", "", StreamItemData{ID: "today-user", Kind: "user"}, ref); !errors.Is(err, errTodayChatItemUnavailable) {
		t.Fatalf("private title at model boundary: %v", err)
	}
	select {
	case <-runner.inputs:
		t.Fatal("private title reached model")
	default:
	}
}

func TestTodayChatFinalPreflightDoesNotPublishRejectedNamedUserRow(t *testing.T) {
	for _, change := range []string{"renamed", "forgotten", "people"} {
		t.Run(change, func(t *testing.T) {
			app, tag, ref := todaySendTestApp(t)
			draft, err := app.PrepareTodayChatDraft(ref)
			if err != nil {
				t.Fatal(err)
			}
			ref.GeneratedTitle = draft.Prefill.Title
			prompt, err := app.PrepareTodayChatTurn(tag, "Help with \""+draft.Prefill.Title+"\".", ref)
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range []storage.StreamItemRow{{ID: "earlier-user", Kind: "user", Content: "Keep my previous request."}, {ID: "earlier-reply", Kind: "assistant", Content: "Keep the previous answer."}} {
				if _, err := app.store.AppendStreamItem(context.Background(), tag, row); err != nil {
					t.Fatal(err)
				}
			}
			switch change {
			case "renamed":
				if _, err := app.SaveMemoryEntry("launch", "# Current title\nCurrent source"); err != nil {
					t.Fatal(err)
				}
			case "forgotten":
				if err := app.DeleteMemoryEntry("launch"); err != nil {
					t.Fatal(err)
				}
			case "people":
				ref.GeneratedPeople = []string{"Former attendee"}
			}
			app.runner = &upcomingCaptureRunner{inputs: make(chan *agent.TaskInput, 1)}
			if _, err := app.SendPromptWithTodayContext(prompt, "", "m", "", tag, nil, nil, false, false, "en", "", StreamItemData{ID: "rejected-today", Kind: "user"}, ref); !errors.Is(err, errTodayChatItemUnavailable) || err.Error() != errTodayChatItemUnavailable.Error() {
				t.Fatalf("final preflight = %v", err)
			}
			rows, err := app.store.LoadStreamItems(context.Background(), tag)
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != 2 || rows[0].ID != "earlier-user" || rows[1].ID != "earlier-reply" {
				t.Fatalf("rejection changed other rows: %+v", rows)
			}
			if strings.Contains(formatConversationHistory(rows), draft.Prefill.Title) {
				t.Fatal("rejected title entered subsequent ordinary history")
			}
		})
	}
}

func TestTodayChatRejectedAdmissionPreservesOtherRows(t *testing.T) {
	app, tag, ref := todaySendTestApp(t)
	main, err := app.store.LoadSession(context.Background(), tag)
	if err != nil {
		t.Fatal(err)
	}
	other := *main
	other.LocalID, other.Kind = "other-chat", "chat"
	if err := app.store.SaveSession(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	const prompt = "Help with the rejected item."
	for _, row := range []storage.StreamItemRow{{ID: "other-user", SessionLocalID: other.LocalID, Kind: "user", Content: prompt}, {ID: "assistant-row", SessionLocalID: tag, Kind: "assistant", Content: prompt}, {ID: "authored-row", SessionLocalID: tag, Kind: "user", Content: "An unrelated authored request."}} {
		if _, err := app.store.AppendStreamItem(context.Background(), row.SessionLocalID, row); err != nil {
			t.Fatal(err)
		}
	}
	if err := app.DeleteMemoryEntry(ref.SourceID); err != nil {
		t.Fatal(err)
	}
	app.runner = &upcomingCaptureRunner{inputs: make(chan *agent.TaskInput, 1)}
	for _, tc := range []struct{ tag, id string }{{tag, "other-user"}, {tag, "assistant-row"}, {tag, "authored-row"}, {other.LocalID, "other-user"}} {
		if _, err := app.SendPromptWithTodayContext(prompt, "", "m", "", tc.tag, nil, nil, false, false, "en", "", StreamItemData{ID: tc.id, Kind: "user"}, ref); err == nil {
			t.Fatal("invalid source/session accepted")
		}
		if row, err := app.store.LoadStreamItem(context.Background(), tc.id); err != nil || row == nil {
			t.Fatalf("unrelated row %s removed: %v", tc.id, err)
		}
	}
}

func TestTodayChatWaitingDateOmitsPrivateHistoricalTransition(t *testing.T) {
	app := newTodayContextTestApp(t, false)
	active := PurposeOpenLoopEventRecord{ID: "active", LifecycleState: PurposeStateActive, OccurredAt: 10}
	waiting := PurposeOpenLoopEventRecord{ID: "waiting", PredecessorEventID: active.ID, LifecycleState: PurposeStateWaiting, Title: "Private old title", DesiredOutcome: "Private old outcome", WaitingCondition: "Private waiting condition", OccurredAt: 20}
	current := PurposeOpenLoopEventRecord{ID: "public", PredecessorEventID: waiting.ID, LifecycleState: PurposeStateWaiting, Title: "Public current title", DesiredOutcome: "Public current outcome", WaitingCondition: "Public current waiting condition", OccurredAt: 30}
	purpose := PurposeOpenLoopDetail{OpenLoop: PurposeOpenLoopRecord{ID: "purpose"}, Current: current, Events: []PurposeOpenLoopEventRecord{active, waiting, current}, Evidence: []PurposeEvidenceRecord{
		{EventID: active.ID, SourceKind: "explicit_user_statement", SourceID: "active", PrivacyDecision: TaskContextPrivacyInclude},
		{EventID: waiting.ID, SourceKind: "memory", SourceID: "old", PrivacyDecision: TaskContextPrivacyInclude},
		{EventID: current.ID, SourceKind: "explicit_user_statement", SourceID: "public", PrivacyDecision: TaskContextPrivacyInclude},
	}}
	sources := todayChatSources{Purposes: []PurposeOpenLoopDetail{purpose}, Memories: []todayChatRecord{{Kind: "memory", ID: "old", Text: "Private old waiting evidence"}}}
	allowed := app.filterTodayChatPurposes(sources)
	if len(allowed) != 1 || todayChatPurposePrefill(allowed[0]).WaitingSince != waiting.OccurredAt {
		t.Fatalf("readable waiting date missing: %+v", allowed)
	}
	sources.Memories = nil
	filtered := app.filterTodayChatPurposes(sources)
	if len(filtered) != 1 || filtered[0].Current.Title != current.Title || filtered[0].Current.DesiredOutcome != current.DesiredOutcome {
		t.Fatalf("independent public fields lost: %+v", filtered)
	}
	if got := todayChatPurposePrefill(filtered[0]); got.Kind != "open_loop" || got.WaitingSince != 0 {
		t.Fatalf("private old transition exposed a waiting date: %+v", got)
	}
	if len(purpose.Events) != 3 || !reflect.DeepEqual(purpose.Events[1], waiting) {
		t.Fatal("read-time filtering mutated the original Purpose events")
	}
}

func TestTodayChatBusyAdmissionDoesNotPublishNamedRowOrStopExistingTurn(t *testing.T) {
	app, tag, ref := todaySendTestApp(t)
	draft, err := app.PrepareTodayChatDraft(ref)
	if err != nil {
		t.Fatal(err)
	}
	ref.GeneratedTitle, ref.GeneratedFingerprint = draft.Prefill.Title, draft.Fingerprint
	prompt, err := app.PrepareTodayChatTurn(tag, "Help with \""+draft.Prefill.Title+"\".", ref)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.DeleteMemoryEntry(ref.SourceID); err != nil {
		t.Fatal(err)
	}
	_, finish, err := app.beginChatTurn(tag)
	if err != nil {
		t.Fatal(err)
	}
	defer app.finishChatPreparation(tag, finish)
	runner := &upcomingCaptureRunner{inputs: make(chan *agent.TaskInput, 1)}
	app.runner = runner
	_, err = app.SendPromptWithTodayContext(prompt, "", "m", "", tag, nil, nil, false, false, "en", "", StreamItemData{ID: "busy-today", Kind: "user"}, ref)
	if !errors.Is(err, errPromptAlreadyRunning) || err.Error() != errTodayChatItemUnavailable.Error() {
		t.Fatalf("busy rejection = %v", err)
	}
	if row, err := app.store.LoadStreamItem(context.Background(), "busy-today"); err != nil || row != nil {
		t.Fatalf("rejected row survived: %+v %v", row, err)
	}
	app.mu.Lock()
	active := app.activeTags[tag]
	app.mu.Unlock()
	if !active {
		t.Fatal("rejection canceled the admitted turn")
	}
	select {
	case <-runner.inputs:
		t.Fatal("rejected Today send reached runner")
	default:
	}
}

func TestTodayChatFingerprintRejectsNewlyPrivateWaitingDateWithUnchangedTitle(t *testing.T) {
	app := newTodayContextTestApp(t, true)
	app.activeTags = map[string]bool{}
	app.eventBroadcaster = NewEventBroadcaster()
	main, err := app.ensureMainChat(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().Add(-3 * time.Hour).UnixMilli()
	initial := purposeTestEvidence("initial", PurposeEvidenceExplicitUser, PurposeStanceSupport, base)
	initial.SourceKind = "explicit_user_statement"
	detail := createTodayContextPurpose(t, app, "waiting-fingerprint", "Original title", initial)
	const body = "# Waiting source\n\nWaiting since the earlier decision."
	writeTodayContextMemory(t, "waiting-source", body)
	store, _ := app.purposeProductStore()
	waiting := purposeTestAppendInput(detail, "wait", PurposeEventWaiting, PurposeStanceWait, PurposeEvidenceExplicitUser, "wait", base+1000)
	waiting.WaitingCondition = "Original waiting condition"
	waiting.Evidence[0].SourceKind, waiting.Evidence[0].SourceID = "memory", "waiting-source"
	waiting.Evidence[0].SourceHash = agencyPayloadHash([]byte(body))
	if _, _, err := store.AppendEvent(waiting); err != nil {
		t.Fatal(err)
	}
	detail, err = store.GetDetail(detail.OpenLoop.ID)
	if err != nil {
		t.Fatal(err)
	}
	public := purposeTestAppendInput(detail, "public", PurposeEventRevised, PurposeStanceUpdate, PurposeEvidenceExplicitUser, "public", base+2000)
	public.Title, public.DesiredOutcome, public.WaitingCondition = "Public current title", "Public current outcome", "Public current waiting condition"
	public.Evidence[0].SourceKind = "explicit_user_statement"
	if _, _, err := store.AppendEvent(public); err != nil {
		t.Fatal(err)
	}
	ref := TodayChatContextRef{SourceKind: "purpose", SourceID: detail.OpenLoop.ID}
	draft, err := app.PrepareTodayChatDraft(ref)
	if err != nil || draft.Prefill.WaitingSince != waiting.OccurredAt {
		t.Fatalf("initial waiting draft=%+v err=%v", draft, err)
	}
	ref.GeneratedTitle, ref.GeneratedFingerprint = draft.Prefill.Title, draft.Fingerprint
	const authored = "What should I do about Public current title? It has been waiting since Tuesday. Keep my wording."
	prepared, err := app.PrepareTodayChatTurn(main.LocalID, authored, ref)
	if err != nil || prepared != authored {
		t.Fatalf("authored wording changed: %q %v", prepared, err)
	}
	if err := app.DeleteMemoryEntry("waiting-source"); err != nil {
		t.Fatal(err)
	}
	fresh, err := app.PrepareTodayChatDraft(ref)
	if err != nil || fresh.Prefill.Title != draft.Prefill.Title || fresh.Prefill.WaitingSince != 0 || fresh.Fingerprint == draft.Fingerprint {
		t.Fatalf("private waiting date did not invalidate metadata: %+v err=%v", fresh, err)
	}
	if _, err := app.PrepareTodayChatTurn(main.LocalID, authored, ref); !errors.Is(err, errTodayChatItemUnavailable) {
		t.Fatalf("old waiting day accepted for persistence: %v", err)
	}
	app.runner = &upcomingCaptureRunner{inputs: make(chan *agent.TaskInput, 1)}
	if _, err := app.SendPromptWithTodayContext(prepared, "", "m", "", main.LocalID, nil, nil, false, false, "en", "", StreamItemData{ID: "waiting-today", Kind: "user"}, ref); !errors.Is(err, errTodayChatItemUnavailable) {
		t.Fatalf("old waiting day sent: %v", err)
	}
	if row, err := app.store.LoadStreamItem(context.Background(), "waiting-today"); err != nil || row != nil {
		t.Fatalf("private day survived in history: %+v %v", row, err)
	}
}

func TestTodayChatDraftFingerprintAllowsContextCountsToChange(t *testing.T) {
	app, tag, ref := todaySendTestApp(t)
	draft, err := app.PrepareTodayChatDraft(ref)
	if err != nil {
		t.Fatal(err)
	}
	ref.GeneratedFingerprint = draft.Fingerprint
	writeTodayContextMemory(t, "additional-launch", "# Launch follow-up\n\nLaunch review background.")
	fresh, err := app.PrepareTodayChatDraft(ref)
	if err != nil || fresh.Counts.Memories <= draft.Counts.Memories || fresh.Fingerprint != draft.Fingerprint {
		t.Fatalf("counts changed draft identity: old=%+v fresh=%+v err=%v", draft, fresh, err)
	}
	const authored = "Help with my launch plan. Keep my priorities."
	if prepared, err := app.PrepareTodayChatTurn(tag, authored, ref); err != nil || prepared != authored {
		t.Fatalf("new related context rejected edited draft: %q %v", prepared, err)
	}
}
