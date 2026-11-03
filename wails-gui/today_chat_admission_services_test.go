package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"wails-gui/internal/handoff"
	"wails-gui/storage"
)

func TestTodayChatFileServiceMutationsWaitThroughHistoryCommit(t *testing.T) {
	for _, mutation := range []string{"topics update", "handoff save", "handoff revise", "handoff attempt", "handoff delete", "handoff retention", "brief replacement", "brief retention"} {
		t.Run(mutation, func(t *testing.T) {
			app := newTodayContextTestApp(t, true)
			t.Setenv(topicsStateDirEnvVar, filepath.Join(os.Getenv("HOME"), "topics"))
			main, err := app.ensureMainChat(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			app.handoffStore, err = handoff.NewStore(filepath.Join(os.Getenv("HOME"), "handoffs"))
			if err != nil {
				t.Fatal(err)
			}
			entry := handoff.Entry{ID: "selected", Title: "Original title", Status: handoff.StatusActive, CreatedAt: 1, AttemptID: "selected-attempt"}
			if _, err := app.handoffStore.Save(entry); err != nil {
				t.Fatal(err)
			}
			ref := TodayChatContextRef{SourceKind: "handoff", SourceID: "selected"}
			var mutate func() error
			switch mutation {
			case "topics update":
				ref.SourceKind = "find"
				mutate = func() error {
					_, err := app.topicsSvc().store.update(func(state *topicsStateFile) { state.Finds.Items = []TopicFind{{ID: "selected", Title: "Revised find"}} })
					return err
				}
			case "handoff save":
				mutate = func() error { entry.Title = "Revised title"; _, err := app.handoffStore.Save(entry); return err }
			case "handoff revise":
				mutate = func() error {
					_, err := app.handoffStore.MutateIfStatus(entry.ID, nil, func(current *handoff.Entry) { current.Title = "Revised title" })
					return err
				}
			case "handoff attempt":
				mutate = func() error {
					_, err := app.handoffStore.MutateIfAttempt(entry.ID, entry.AttemptID, nil, func(current *handoff.Entry) { current.Title = "Revised attempt title" })
					return err
				}
			case "handoff delete":
				mutate = func() error { return app.handoffStore.Delete(entry.ID) }
			case "handoff retention":
				mutate = func() error { _, err := app.handoffStore.MarkStale(time.Millisecond); return err }
			case "brief replacement", "brief retention":
				ref.SourceKind, ref.BriefID = "brief_item", "2026-10-17-evening"
				at := time.Date(2026, 10, 17, 18, 0, 0, 0, time.Local)
				brief := MobileBriefV1{BriefID: ref.BriefID, AsOf: at.Format(time.RFC3339)}
				// A distinct store instance must obey the same publication fence.
				writer := newMobileBriefStore(os.Getenv("YANSU_MOBILE_STATE_DIR"))
				if err := writer.save(brief, at); err != nil {
					t.Fatal(err)
				}
				mutate = func() error {
					if mutation == "brief retention" {
						writer.mu.Lock()
						defer writer.mu.Unlock()
						writer.sweepLocked(at.AddDate(0, 0, mobileBriefRetentionDays+1))
						return nil
					}
					brief.AsOf = at.Add(time.Minute).Format(time.RFC3339)
					return writer.save(brief, at)
				}
			}
			release, err := app.lockTodayChatFileServices(t.Context(), ref)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			files, err := app.readTodayChatAdmissionFiles(t.Context(), ref)
			if err != nil {
				t.Fatal(err)
			}
			revision, err := app.store.ReadRevision(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			started, done := make(chan struct{}), make(chan error, 1)
			order, err := app.store.AppendUserStreamItemAtomicIfCurrent(t.Context(), main.LocalID, storage.StreamItemRow{ID: "accepted", Kind: "user", Content: "Original title"}, revision, func() error {
				if err := app.validateTodayChatAdmissionFiles(t.Context(), ref, files); err != nil {
					return err
				}
				// The source was just validated, but the transaction is not yet
				// committed. A writer must not invalidate it in this interval.
				go func() { close(started); done <- mutate() }()
				<-started
				select {
				case err := <-done:
					t.Errorf("source mutation completed before history commit: %v", err)
				case <-time.After(25 * time.Millisecond):
				}
				return app.validateTodayChatAdmissionFiles(t.Context(), ref, files)
			})
			if err != nil || order != 0 {
				t.Fatalf("stable source was not admitted: order=%d err=%v", order, err)
			}
			row, err := app.store.LoadStreamItem(t.Context(), "accepted")
			if err != nil || row == nil || row.Content != "Original title" {
				t.Fatalf("admitted turn missing before writer release: row=%+v err=%v", row, err)
			}
			release()
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("source writer did not resume: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("source writer stayed blocked after publication")
			}
			if err := app.validateTodayChatAdmissionFiles(t.Context(), ref, files); !errors.Is(err, errTodayChatItemUnavailable) {
				t.Fatalf("completed mutation did not invalidate old source: %v", err)
			}
		})
	}
}

func TestTodayChatFileServiceLockCancellationReleasesEarlierBarriers(t *testing.T) {
	app := newTodayContextTestApp(t, false)
	t.Setenv(topicsStateDirEnvVar, filepath.Join(os.Getenv("HOME"), "topics"))
	topics := app.topicsSvc().store
	topics.publication.Lock()
	defer topics.publication.Unlock()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	_, err := app.lockTodayChatFileServices(ctx, TodayChatContextRef{SourceKind: "brief_item"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked service lock did not respect deadline: %v", err)
	}
	if !mobileBriefPublicationMu.TryLock() {
		t.Fatal("cancellation retained the earlier brief barrier")
	}
	mobileBriefPublicationMu.Unlock()
}
