package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/handoff"
	"wails-gui/storage"
)

// newLifecycleHandoffStore opens a file-backed handoff store in a temp dir whose
// clock follows the Engagement test rig.
func newLifecycleHandoffStore(t *testing.T, rig *engagementTestRig) *handoff.Store {
	t.Helper()
	store, err := handoff.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store.Now = func() time.Time { return rig.clock }
	return store
}

func saveLifecycleHandoff(t *testing.T, store *handoff.Store, entry handoff.Entry) {
	t.Helper()
	if _, err := store.Save(entry); err != nil {
		t.Fatal(err)
	}
}

func TestAbortHandoffCancelsTheRunningTurnBeforeMarkingItAborted(t *testing.T) {
	rig := newEngagementTestRig(t)
	store := newLifecycleHandoffStore(t, rig)
	app := &App{
		handoffStore: store,
		streams:      map[string]context.CancelFunc{},
		tagCancels:   map[string]context.CancelFunc{},
		activeTags:   map[string]bool{},
		runIDByTag:   map[string]string{},
	}
	turnCtx, cancelTurn := context.WithCancel(context.Background())
	defer cancelTurn()
	app.streams["run-1"] = cancelTurn
	app.runIDByTag["sess-1"] = "run-1"
	saveLifecycleHandoff(t, store, handoff.Entry{ID: "hf-run", Title: "The memo", Status: handoff.StatusInProgress, Source: handoff.SourceTriage, ChatSessionID: "sess-1"})

	if err := app.AbortHandoff("hf-run"); err != nil {
		t.Fatal(err)
	}
	if turnCtx.Err() == nil {
		t.Fatal("the running turn was not cancelled")
	}
	if _, ok := app.streams["run-1"]; ok {
		t.Fatal("the cancelled stream was not released")
	}
	entry, _ := store.Get("hf-run")
	if entry.Status != handoff.StatusAborted {
		t.Fatalf("status = %q", entry.Status)
	}
}

func TestAbortHandoffCancelsByTagBeforeTheRunStarts(t *testing.T) {
	rig := newEngagementTestRig(t)
	store := newLifecycleHandoffStore(t, rig)
	app := &App{
		handoffStore: store,
		streams:      map[string]context.CancelFunc{},
		tagCancels:   map[string]context.CancelFunc{},
		activeTags:   map[string]bool{},
		runIDByTag:   map[string]string{},
	}
	turnCtx, cancelTurn := context.WithCancel(context.Background())
	defer cancelTurn()
	app.tagCancels["sess-2"] = cancelTurn
	saveLifecycleHandoff(t, store, handoff.Entry{ID: "hf-start", Title: "The brief", Status: handoff.StatusInProgress, Source: handoff.SourceTriage, ChatSessionID: "sess-2"})

	if err := app.AbortHandoff("hf-start"); err != nil {
		t.Fatal(err)
	}
	if turnCtx.Err() == nil {
		t.Fatal("the turn was not cancelled by its session tag")
	}
	if _, ok := app.tagCancels["sess-2"]; ok {
		t.Fatal("the cancelled turn was not released")
	}
}

func TestAbortHandoffStopsABlockedHandoff(t *testing.T) {
	rig := newEngagementTestRig(t)
	store := newLifecycleHandoffStore(t, rig)
	app := &App{handoffStore: store}
	saveLifecycleHandoff(t, store, handoff.Entry{ID: "hf-stuck", Title: "The export", Status: handoff.StatusBlocked, BlockedReason: handoff.BlockedInterrupted, Source: handoff.SourceTriage})

	if err := app.AbortHandoff("hf-stuck"); err != nil {
		t.Fatalf("a blocked handoff must be stoppable: %v", err)
	}
	entry, _ := store.Get("hf-stuck")
	if entry.Status != handoff.StatusAborted {
		t.Fatalf("status = %q", entry.Status)
	}
}

func TestHandoffLifecyclePostsEachOutcomeInMainChat(t *testing.T) {
	for _, status := range []handoff.Status{handoff.StatusCompleted, handoff.StatusNeedsReview, handoff.StatusBlocked, handoff.StatusAborted} {
		t.Run(string(status), func(t *testing.T) {
			app := mainChatTestApp(t)
			store, err := handoff.NewStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			app.handoffStore = store
			sessionID, err := app.createHandoffExecutionSession(handoff.Entry{Title: "Prepare the report"})
			if err != nil {
				t.Fatal(err)
			}
			transcript := "The report compares the available options.\n\n[Read the report](https://example.com/report)."
			displayTranscript := transcript
			if _, err := app.store.AppendStreamItem(context.Background(), sessionID, storage.StreamItemRow{ID: "work-result", Kind: "assistant", Content: transcript}); err != nil {
				t.Fatal(err)
			}
			entry := handoff.Entry{ID: "result", Title: "Prepare the report", Status: handoff.StatusInProgress, ChatSessionID: sessionID}
			if status == handoff.StatusCompleted {
				entry.Criteria = []string{"The report is available"}
				entry.Workdir = t.TempDir()
				writeEvidenceFile(t, entry.Workdir, "report.md")
				transcript = displayTranscript + "\n\n" + strings.TrimPrefix(handoffResultForFile(t, entry.Criteria[0], "report.md"), "The report is ready.\n\n")
				withHandoffVerifier(t, `{"status":"completed","criteria":[{"text":"The report is available","met":true}]}`, nil)
			}
			saveLifecycleHandoff(t, store, entry)
			transition := func() {
				switch status {
				case handoff.StatusBlocked:
					app.markHandoffBlocked(entry.ID, handoff.BlockedTimeout)
				case handoff.StatusAborted:
					app.markHandoffDone(entry.ID, false, transcript)
				default:
					app.markHandoffDone(entry.ID, true, transcript)
				}
			}
			transition()
			transition()
			saved, err := store.Get(entry.ID)
			if err != nil || saved.Status != status {
				t.Fatalf("saved outcome = %+v, %v", saved, err)
			}
			// A restarted producer reads exactly the same transition identity.
			reopened := &App{store: app.store, handoffStore: store}
			reopened.postHandoffReport(saved)
			items := mainChatProactiveItems(t, app)
			if len(items) != 1 {
				t.Fatalf("transition/replay posted %d messages", len(items))
			}
			item := items[0]
			wantKind := "work_finished"
			if status == handoff.StatusBlocked || status == handoff.StatusAborted {
				wantKind = "work_blocked"
			}
			if item.Kind != wantKind || item.Work == nil || item.Work.Status != status || item.Work.Result != displayTranscript || item.Work.Summary != "The report compares the available options." {
				t.Fatalf("work snapshot = %+v", item)
			}
			if item.Target != (mainChatTarget{Kind: mainChatTargetHandoff, ID: entry.ID}) || !reflect.DeepEqual(item.ActionIDs, expectedHandoffActions(status)) {
				t.Fatalf("work actions = %+v", item)
			}
			if item.Localized["zh-CN"].ActionLabels["review"] != "查看" || !strings.Contains(item.Body, item.Work.Summary) {
				t.Fatalf("work copy = %+v", item)
			}
			if len(mainChatReports(t, app)) != 0 || len(mainChatStatusEntries(t, app)) != 0 {
				t.Fatal("work posted an extra legacy card or folded status")
			}
			if history, err := app.store.LoadStreamItems(context.Background(), sessionID); err != nil || len(history) != 1 {
				t.Fatalf("execution history changed: %+v, %v", history, err)
			}
		})
	}
}

func TestHandoffMainChatReviewPreservesSnapshotAndDeliverables(t *testing.T) {
	app := mainChatTestApp(t)
	workdir := t.TempDir()
	file := filepath.Join(workdir, "report.md")
	if err := os.WriteFile(file, []byte("Report"), 0o600); err != nil {
		t.Fatal(err)
	}
	entry := handoff.Entry{ID: "review", Title: "The report", Status: handoff.StatusNeedsReview, Workdir: workdir, UpdatedAt: 1}
	app.postHandoffResult(entry, "The report covers the first quarter.\n\n[Sources](https://example.com/report)\n<deliverables>\nreport.md | Quarterly report\n</deliverables>")
	items := mainChatProactiveItems(t, app)
	if len(items) != 1 || len(items[0].Work.Deliverables) != 1 || items[0].Work.Deliverables[0].URL != file {
		t.Fatalf("deliverables = %+v", items)
	}
	card := items[0]
	main, _ := app.ensureMainChat(context.Background())
	if prompt := app.injectMainChatReports(main.LocalID); strings.Contains(prompt, "first quarter") || strings.Contains(prompt, file) {
		t.Fatalf("result display entered prompt before review: %s", prompt)
	}
	for i := 0; i < 2; i++ {
		if state, err := app.ResolveMainChatProactiveAction(card.MomentID, "review"); err != nil || state != "opened" {
			t.Fatalf("review = %q, %v", state, err)
		}
	}
	rows, err := app.store.LoadStreamItems(context.Background(), main.LocalID)
	if err != nil || len(rows) != 2 || rows[1].Kind != "assistant" || !strings.Contains(rows[1].Content, "first quarter") || !strings.Contains(rows[1].Content, "https://example.com/report") || !strings.Contains(rows[1].Content, file+" | Quarterly report") {
		t.Fatalf("review result = %+v, %v", rows, err)
	}
	// A later attempt is a new card; reviewing the earlier one remains a replay.
	entry.UpdatedAt = 2
	app.postHandoffResult(entry, "A different result from the next attempt.")
	if _, err := app.ResolveMainChatProactiveAction(card.MomentID, "review"); err != nil {
		t.Fatal(err)
	}
	rows, _ = app.store.LoadStreamItems(context.Background(), main.LocalID)
	if len(rows) != 3 || rows[1].Kind != "assistant" || strings.Contains(rows[1].Content, "different result") {
		t.Fatalf("review changed its original snapshot: %+v", rows)
	}
	if state, err := app.ResolveMainChatProactiveAction(card.MomentID, "dismiss"); err != nil || state != "dismissed" {
		t.Fatalf("dismiss after review = %q, %v", state, err)
	}
}

func TestHandoffMainChatStopAfterReviewUsesLifecycle(t *testing.T) {
	app := mainChatTestApp(t)
	store, err := handoff.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	app.handoffStore = store
	saveLifecycleHandoff(t, store, handoff.Entry{ID: "stop", Title: "The report", Status: handoff.StatusInProgress})
	app.markHandoffBlocked("stop", handoff.BlockedInterrupted)
	card := mainChatProactiveItems(t, app)[0]
	if _, err := app.ResolveMainChatProactiveAction(card.MomentID, "review"); err != nil {
		t.Fatal(err)
	}
	if state, err := app.ResolveMainChatProactiveAction(card.MomentID, "stop"); err != nil || state != "acted" {
		t.Fatalf("stop after review = %q, %v", state, err)
	}
	entry, _ := store.Get("stop")
	if entry.Status != handoff.StatusAborted {
		t.Fatalf("stop did not settle work: %+v", entry)
	}
	if state, err := app.ResolveMainChatProactiveAction(card.MomentID, "stop"); err != nil || state != "acted" {
		t.Fatalf("stop replay = %q, %v", state, err)
	}
}

func TestHandoffMainChatResultRedactsAndBoundsDisplay(t *testing.T) {
	app := mainChatTestApp(t)
	app.postHandoffResult(handoff.Entry{ID: "private", Title: "Report", Status: handoff.StatusCompleted}, "Card 4111 1111 1111 1111\n"+strings.Repeat("details ", 2000))
	item := mainChatProactiveItems(t, app)[0]
	if strings.Contains(item.Body+item.Work.Result, "4111") || len(item.Work.Result) > handoffVerifyTranscriptMax {
		t.Fatalf("unbounded or unredacted work result: %+v", item.Work)
	}
}

func TestHandoffMainChatOldStopCannotCancelANewerAttempt(t *testing.T) {
	app := mainChatTestApp(t)
	store, err := handoff.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	app.handoffStore = store
	saveLifecycleHandoff(t, store, handoff.Entry{ID: "restarted", Title: "The report", Status: handoff.StatusInProgress})
	app.markHandoffBlocked("restarted", handoff.BlockedInterrupted)
	card := mainChatProactiveItems(t, app)[0]
	if _, err := store.UpdateStatus("restarted", handoff.StatusInProgress); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ResolveMainChatProactiveAction(card.MomentID, "stop"); err == nil {
		t.Fatal("old Stop accepted for a newer attempt")
	}
	entry, _ := store.Get("restarted")
	if entry.Status != handoff.StatusInProgress {
		t.Fatalf("old Stop cancelled newer attempt: %+v", entry)
	}
}

func TestHandoffMainChatStopWaitsForAcceptTransaction(t *testing.T) {
	app := mainChatTestApp(t)
	store, err := handoff.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	app.handoffStore = store
	sessionID, err := app.createHandoffExecutionSession(handoff.Entry{Title: "The report"})
	if err != nil {
		t.Fatal(err)
	}
	entry, err := store.Save(handoff.Entry{ID: "concurrent", Title: "The report", Status: handoff.StatusBlocked, ChatSessionID: sessionID, Body: "Prepare the report."})
	if err != nil {
		t.Fatal(err)
	}
	app.postHandoffReport(entry)
	card := mainChatProactiveItems(t, app)[0]
	// Pause the accepted claim while Accept holds the lifecycle lock. Stop
	// must wait for that complete transaction and then reject its stale card.
	claimed := make(chan struct{})
	resume := make(chan struct{})
	resumed := false
	defer func() {
		if !resumed {
			close(resume)
		}
	}()
	firstMutation := true
	store.Now = func() time.Time {
		if firstMutation {
			firstMutation = false
			close(claimed)
			<-resume
		}
		return time.Now()
	}
	accepted := make(chan error, 1)
	go func() {
		_, err := app.AcceptHandoff(entry.ID)
		accepted <- err
	}()
	select {
	case <-claimed:
	case <-time.After(2 * time.Second):
		t.Fatal("accept never claimed the work")
	}
	if app.handoffActionMu.TryLock() {
		app.handoffActionMu.Unlock()
		t.Fatal("Accept does not hold the lifecycle lock")
	}
	stopped := make(chan error, 1)
	go func() {
		_, err := app.ResolveMainChatProactiveAction(card.MomentID, "stop")
		stopped <- err
	}()
	close(resume)
	resumed = true
	if err := <-accepted; err != nil {
		t.Fatal(err)
	}
	if err := <-stopped; err == nil {
		t.Fatal("stale Stop cancelled the accepted attempt")
	}
	saved, _ := store.Get(entry.ID)
	if saved.Status != handoff.StatusInProgress || saved.ChatSessionID != sessionID {
		t.Fatalf("accepted attempt changed: %+v", saved)
	}
}

func TestHandoffMainChatDeliverableURLsPreserveDestination(t *testing.T) {
	app := mainChatTestApp(t)
	for _, destination := range []string{
		"http://localhost:8766/report?format=pdf&id=report#page=2",
		"https://example.com/export?document=quarterly&format=pdf#summary",
		"https://example.com/a%20report?name=Quarterly%20Report#section-2",
	} {
		t.Run(destination, func(t *testing.T) {
			entry := handoff.Entry{ID: destination, Title: "The report", Status: handoff.StatusCompleted}
			app.postHandoffResult(entry, "The report is ready.\n<deliverables>\n"+destination+" | Quarterly report\n</deliverables>")
			items := mainChatProactiveItems(t, app)
			card := items[len(items)-1]
			if len(card.Work.Deliverables) != 1 || card.Work.Deliverables[0].URL != destination {
				t.Fatalf("deliverable destination changed: %+v", card.Work.Deliverables)
			}
			if _, err := app.ResolveMainChatProactiveAction(card.MomentID, "review"); err != nil {
				t.Fatal(err)
			}
			main, _ := app.ensureMainChat(context.Background())
			rows, _ := app.store.LoadStreamItems(context.Background(), main.LocalID)
			if !strings.Contains(rows[len(rows)-1].Content, "](<"+destination+">)") {
				t.Fatalf("review link changed: %s", rows[len(rows)-1].Content)
			}
		})
	}
}

func TestHandoffMainChatDeliverableURLsRejectCredentials(t *testing.T) {
	app := mainChatTestApp(t)
	for _, destination := range []string{
		"https://user:password@example.com/report",
		"https://example.com/report?access_token=secret",
		"https://example.com/report?X-Amz-Signature=secret",
		"https://example.com/report#access_token=secret",
		"https://example.com/report?card=4111%201111%201111%201111",
	} {
		if link := app.mainChatHandoffDeliverable(handoff.Entry{}, deliverableRef{Path: destination}); link.URL != "" {
			t.Errorf("credential-bearing URL survived: %s", link.URL)
		}
	}
}

func expectedHandoffActions(status handoff.Status) []string {
	if status == handoff.StatusCompleted || status == handoff.StatusAborted {
		return []string{"review", "dismiss", "mute_kind"}
	}
	return []string{"review", "stop", "dismiss", "mute_kind"}
}

func TestHandoffMainChatMomentSeparatesAttemptsAtTheSameTime(t *testing.T) {
	entry := handoff.Entry{ID: "work", AttemptID: "first", Status: handoff.StatusBlocked, BlockedReason: handoff.BlockedTimeout, UpdatedAt: 1000}
	first := mainChatHandoffMomentID(entry)
	entry.AttemptID = "second"
	if first == mainChatHandoffMomentID(entry) {
		t.Fatal("same-time restart reuses an old Stop identity")
	}
}

func TestHandoffMainChatStaleStopCannotReportCompletedWorkAsStopped(t *testing.T) {
	app := mainChatTestApp(t)
	store, err := handoff.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	app.handoffStore = store
	entry, err := store.Save(handoff.Entry{ID: "already-done", Status: handoff.StatusNeedsReview, AttemptID: "first"})
	if err != nil {
		t.Fatal(err)
	}
	app.postHandoffReport(entry)
	card := mainChatProactiveItems(t, app)[0]
	if _, err := store.MutateIfStatus(entry.ID, []handoff.Status{handoff.StatusNeedsReview}, func(e *handoff.Entry) { e.Status = handoff.StatusInProgress; e.AttemptID = "second" }); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateStatus(entry.ID, handoff.StatusCompleted); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ResolveMainChatProactiveAction(card.MomentID, "stop"); err == nil {
		t.Fatal("stale Stop returned success for completed work")
	}
	got, _ := store.Get(entry.ID)
	if got.Status != handoff.StatusCompleted {
		t.Fatalf("completed work changed: %+v", got)
	}
}
