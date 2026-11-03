package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"wails-gui/internal/pii"
	"wails-gui/storage"
	"yansu-agent/agent"
)

// Block the real indexed lookup after the initial source snapshot. Withdrawal
// must prevent both model execution and durable, phone-visible user history.
func TestTodayChatAdmissionRevalidatesAfterIndexedRetrieval(t *testing.T) {
	for _, change := range []string{"memory deleted", "purpose source made sensitive"} {
		t.Run(change, func(t *testing.T) {
			app := newTodayContextTestApp(t, true)
			main, err := app.ensureMainChat(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			const title = "Confidential acquisition plan"
			ref := TodayChatContextRef{SourceKind: "memory", SourceID: "selected"}
			writeTodayContextMemory(t, ref.SourceID, "# "+title+"\nPrivate source details.")
			withdraw := func() {
				if err := app.DeleteMemoryEntry("selected"); err != nil {
					t.Fatal(err)
				}
			}
			if change == "purpose source made sensitive" {
				base := time.Now().Add(-time.Minute).UnixMilli()
				if err := app.activity.store.SaveSession(ActivitySessionRow{ID: "original", AppNames: `["Notes"]`, StartTime: base, EndTime: base + 1}, nil, nil); err != nil {
					t.Fatal(err)
				}
				evidence := purposeTestEvidence("original", PurposeEvidenceObservedUser, PurposeStanceSupport, base)
				evidence.SourceKind, evidence.SourceID = "activity_session", "original"
				detail := createTodayContextPurpose(t, app, "original", title, evidence)
				ref = TodayChatContextRef{SourceKind: "purpose", SourceID: detail.OpenLoop.ID}
				prior := pii.GetUserSensitiveApps()
				t.Cleanup(func() { pii.SetUserSensitiveApps(prior) })
				withdraw = func() { pii.SetUserSensitiveApps(map[string]string{"com.apple.Notes": "Notes"}) }
			}
			draft, err := app.PrepareTodayChatDraft(ref)
			if err != nil {
				t.Fatal(err)
			}
			ref.GeneratedTitle, ref.GeneratedFingerprint = draft.Prefill.Title, draft.Fingerprint
			prompt, err := app.PrepareTodayChatTurn(main.LocalID, "Help me with \""+draft.Prefill.Title+"\".", ref)
			if err != nil {
				t.Fatal(err)
			}
			index, err := NewMemoryStore(t.TempDir(), MemoryStoreConfig{Provider: "none"})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = index.Close() })
			index.dirty = false
			app.memoryStore = index
			runner := &upcomingCaptureRunner{inputs: make(chan *agent.TaskInput, 1)}
			app.runner = runner
			wait, release := blockTodayLookup(t, index.db)
			type sendResult struct {
				order int
				err   error
			}
			done := make(chan sendResult, 1)
			go func() {
				order, err := app.SendPromptWithTodayContext(prompt, "", "m", "", main.LocalID, nil, nil, false, false, "en", "", StreamItemData{ID: "withdrawn", Kind: "user"}, ref)
				done <- sendResult{order, err}
			}()
			wait()
			withdraw()
			release()
			select {
			case got := <-done:
				if got.order != -1 || !errors.Is(got.err, errTodayChatItemUnavailable) {
					t.Errorf("withdrawn source admitted: order=%d err=%v", got.order, got.err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("send did not finish")
			}
			waitForTagIdle(t, app, main.LocalID)
			select {
			case <-runner.inputs:
				t.Error("withdrawn source reached model")
			default:
			}
			rows, err := app.store.LoadStreamItems(context.Background(), main.LocalID)
			if err != nil || len(rows) != 0 {
				t.Errorf("withdrawn source published history: %+v err=%v", rows, err)
			}
			if _, err := app.PrepareTodayChatDraft(ref); !errors.Is(err, errTodayChatItemUnavailable) {
				t.Fatalf("fresh read did not reject withdrawal: %v", err)
			}
		})
	}
}

func TestTodayChatAdmissionStopWhileSourceWriterIsBusy(t *testing.T) {
	app, tag, ref := todaySendTestApp(t)
	unlock := memoryFileLocks.Acquire("launch")
	defer unlock()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, _, err := app.admitTodayChatTurn(ctx, tag, storage.StreamItemRow{ID: "stopped", Kind: "user"}, ref, nil)
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("source writer wait ignored Stop: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Stop waited for source writer")
	}
	if rows, err := app.store.LoadStreamItems(context.Background(), tag); err != nil || len(rows) != 0 {
		t.Fatalf("canceled source admission published: %+v %v", rows, err)
	}
}
