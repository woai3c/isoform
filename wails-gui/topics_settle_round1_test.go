package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func retainedTopicTranscript(t *testing.T, a *App) (string, func(string), func(string, bool)) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "retained.md")
	writeHeader := func(audio string) {
		t.Helper()
		text := "- **Audio**: activity/audio/" + audio + "\n" + withTranscriptActivitySources("# Meeting\n- **Session**: session-atlas\n## Transcript\nFixture\n", []string{"session-atlas"})
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeSidecar := func(audio string, protected bool) {
		t.Helper()
		raw, err := json.Marshal(meetingAudioMeta{SessionID: "session-atlas", App: "Code", Protected: protected})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(meetingWAVMetaPath(a.activity.store.AudioDir(), audio), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.activity.store.db.Exec(`UPDATE activity_sessions SET transcript_path=?,audio_path='' WHERE id='session-atlas'`, path); err != nil {
		t.Fatal(err)
	}
	return path, writeHeader, writeSidecar
}

func TestTopicFullCaptureRejectsHeaderSwapAfterEnumeration(t *testing.T) {
	a, _, _ := registryChatTestApp(t)
	path, header, sidecar := retainedTopicTranscript(t, a)
	header("old.wav")
	sidecar("old.wav", false)
	sidecar("new.wav", false)
	var once sync.Once
	a.topicsSvc().registrySessionFilesCaptured = func() { once.Do(func() { header("new.wav") }) }
	views, err := collectAndReadTopicViewsForTest(a)
	if !errors.Is(err, errTopicRegistryChanged) {
		t.Errorf("changed enumeration accepted: views=%d err=%v", len(views), err)
	}
	a.topicsSvc().registrySessionFilesCaptured = nil
	if err != nil {
		views, err = collectAndReadTopicViewsForTest(a)
		if err != nil || len(views) != 1 {
			t.Fatalf("stable re-enumeration did not recover: %v %v", views, err)
		}
	}
	c := a.topicsSvc().loadRegistryCache()
	if c == nil || !topicStringIn(c.revision.sessionFiles.Paths, meetingWAVMetaPath(a.activity.store.AudioDir(), "new.wav")) {
		t.Error("full proof did not bind the replacement sidecar")
	}
	sidecar("new.wav", true)
	if _, err := a.activity.store.transcriptSourceIDs(ActivitySessionRow{ID: "session-atlas", TranscriptPath: path}, ""); err == nil {
		t.Fatal("protected source control was readable")
	}
	got, err := a.GetTodayTopics()
	if err != nil || len(got.Topics) != 1 || got.Clear {
		t.Fatalf("out-of-band change altered the saved snapshot: %+v %v", got, err)
	}
	views, err = collectAndReadTopicViewsForTest(a)
	if err != nil || len(views) != 0 {
		t.Fatalf("next explicit build retained protected source: %+v %v", views, err)
	}
	got, err = a.GetTodayTopics()
	if err != nil || len(got.Topics) != 0 || !got.Clear {
		t.Fatalf("protected source survived explicit build: %+v %v", got, err)
	}
}

func TestTopicDeferredChatRecoversWithoutRegistryRebuild(t *testing.T) {
	a, _, _ := registryChatTestApp(t)
	svc := a.topicsSvc()
	svc.now = time.Now
	ctx, cancel := context.WithCancel(t.Context())
	a.ctx = ctx
	a.eventBroadcaster = NewEventBroadcaster()
	events := a.eventBroadcaster.AddClient("chat-retry")
	var builds, attempts atomic.Int32
	svc.registryBeforeSnapshot = func() { builds.Add(1) }
	var expire context.CancelFunc
	failed := make(chan struct{})
	svc.registryChatContext = func(parent context.Context) (context.Context, context.CancelFunc) {
		child, stop := context.WithCancel(parent)
		expire = stop
		return child, stop
	}
	svc.registryDuringChat = func(string) {
		if attempts.Add(1) == 1 {
			expire()
			close(failed)
		}
	}
	svc.startRegistryWorker(ctx)
	t.Cleanup(func() { cancel(); svc.stopRegistryWorker() })
	select {
	case <-failed:
	case <-time.After(10 * time.Second):
		t.Fatal("initial chat did not run")
	}
	first, err := a.GetTodayTopics()
	if err != nil || first.Refreshing || len(first.Topics) != 1 {
		t.Fatalf("display did not settle: %+v %v", first, err)
	}
	id := first.Topics[0].ID
	firstAt := svc.loadRegistryCache().at.UnixMilli()
	if _, err := a.ListMainChatTopicItems(id, "", 50); err == nil {
		t.Fatal("failed derivation unexpectedly authorized Open")
	}
	timer := time.NewTimer(4 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-events:
			if attempts.Load() < 2 {
				continue
			}
			if _, err := a.ListMainChatTopicItems(id, "", 50); err != nil {
				continue
			}
			for _, snapshot := range svc.store.load().ChatTopics {
				if snapshot.Topic.ID == id && snapshot.DerivedAt <= firstAt {
					t.Fatal("retry reused the first attempt's derivation/cache clock")
				}
			}
			for i := 0; i < 20; i++ {
				got, err := a.GetTodayTopics()
				if err != nil || got.Refreshing || len(got.Topics) != 1 {
					t.Fatalf("retry disturbed settled display: %+v %v", got, err)
				}
			}
			if builds.Load() != 1 {
				t.Fatalf("chat retry recollected %d registries", builds.Load())
			}
			return
		case <-timer.C:
			t.Fatalf("healthy chat never recovered: attempts=%d builds=%d", attempts.Load(), builds.Load())
		}
	}
}

func TestTopicPageAndPhoneUseCapturedHeaderProof(t *testing.T) {
	a, _, _ := registryChatTestApp(t)
	store := a.activity.store
	dir := t.TempDir()
	original := filepath.Join(dir, "header.md")
	// Distinct retained paths with real 256 KiB headers; hard links bound disk
	// space while keeping the same per-session open/read/stat workload.
	if err := os.WriteFile(original, []byte("# Meeting\n- Evidence: "+strings.Repeat("x", 256*1024)+"\n## Transcript\nFixture"), 0600); err != nil {
		t.Fatal(err)
	}
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT INTO activity_sessions(id,start_time,end_time,app_names,triage_status,trigger_kind,event_count,segment_count,created_at,transcript_path) VALUES(?,1,2,'[]','done','test',0,0,1,?)`)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < topicRegistryFixtureSessions; i++ {
		path := filepath.Join(dir, fmt.Sprintf("retained-%04d.md", i))
		if err := os.Link(original, path); err != nil {
			t.Fatal(err)
		}
		if _, err := stmt.Exec(fmt.Sprintf("retained-%04d", i), path); err != nil {
			t.Fatal(err)
		}
	}
	stmt.Close()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var scans atomic.Int32
	a.topicsSvc().registryRecordingHeadersRead = func() { scans.Add(1) }
	if views, err := collectAndReadTopicViewsForTest(a); err != nil || len(views) != 1 {
		t.Fatalf("full control: %v %v", views, err)
	}
	holdTodayTopicsBuild(t, a)
	before := scans.Load()
	bridge := &mobileBridge{app: a, ctx: t.Context()}
	started := time.Now()
	response := mobileTestChatCall(bridge, mobileTestDevice(mobileScopeBriefRead), http.MethodGet, "/topics", "")
	if response.Code != http.StatusOK {
		t.Fatalf("phone delivery %d after %s: %s", response.Code, time.Since(started), response.Body.String())
	}
	var got mobileTopicListView
	if err := json.Unmarshal(mobileTestDecodeReply(t, response).Data, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Topics) != 1 {
		t.Fatalf("phone lost settled view: %+v", got)
	}
	revision, err := a.captureTopicPageRevision(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	release, err := a.validateTopicPageRevision(t.Context(), revision)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if err := a.publishMainChatRowTopicBatch(t.Context(), nil, revision); err != nil {
		t.Fatal(err)
	}
	if scans.Load() != before {
		t.Fatalf("delivery re-enumerated retained headers %d times", scans.Load()-before)
	}
	t.Logf("sessions=%d header_bytes=%d phone=%s header_enumerations=0", topicRegistryFixtureSessions, 256*1024, time.Since(started))
}

func TestMainChatTopicBackfillWaitsForAuthoritativeChat(t *testing.T) {
	a, session, now := registryChatTestApp(t)
	row := mainChatViewCard(t, "historical-topic", "goal_check_in", "pending", now.AddDate(0, 0, -13))
	row.ToolInput = mainChatViewPayload(t, mainChatRowTopicCard(row.ID))
	if _, err := a.store.AppendStreamItem(t.Context(), session, row); err != nil {
		t.Fatal(err)
	}
	a.eventBroadcaster = NewEventBroadcaster()
	events := a.eventBroadcaster.AddClient("backfill-ready")
	chatEntered, chatRelease, chatResumed := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once, releaseOnce sync.Once
	releaseChat := func() { releaseOnce.Do(func() { close(chatRelease) }) }
	a.topicsSvc().registryDuringChat = func(string) {
		once.Do(func() {
			close(chatEntered)
			select {
			case <-chatRelease:
			case <-t.Context().Done():
			}
			close(chatResumed)
		})
	}
	unblock := holdTodayTopicsBuild(t, a)
	t.Cleanup(releaseChat)
	a.mainChatTopicBackfillRunning.Store(true)
	if err := a.backfillMainChatRowTopics(t.Context(), now); err == nil {
		t.Error("another in-flight backfill was mistaken for completion")
	}
	a.mainChatTopicBackfillRunning.Store(false)
	checkPending := func(stage string) {
		t.Helper()
		if err := a.backfillMainChatRowTopics(t.Context(), now); err == nil {
			t.Errorf("%s claimed completion", stage)
		}
		if done, _ := a.store.GetUIState(t.Context(), mainChatTopicBackfillKey); done == "true" {
			t.Errorf("%s persisted completion before chat publication", stage)
		}
		if tags := mainChatRowStoredTags(t, a, session, row.ID); len(tags) != 0 {
			t.Fatalf("%s unexpected tags: %v", stage, tags)
		}
	}
	checkPending("cold partial")
	unblock()
	select {
	case <-chatEntered:
	case <-time.After(10 * time.Second):
		t.Fatal("chat did not begin")
	}
	checkPending("full display, pending chat")
	releaseChat()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()
	select {
	case <-chatResumed:
	case <-timer.C:
		t.Fatal("chat derivation did not resume after release")
	}
	timer.Reset(10 * time.Second)
	for {
		select {
		case <-events:
		case <-poll.C:
		case <-timer.C:
			svc := a.topicsSvc()
			cache := svc.loadRegistryCache()
			t.Fatalf("completed chat never admitted backfill: pending=%t full=%t chat_complete=%t", svc.registryPending(), svc.registryFullBuildRunning(), cache != nil && cache.chatComplete)
		}
		svc := a.topicsSvc()
		cache := svc.loadRegistryCache()
		if cache == nil || !cache.chatComplete {
			continue
		}
		if err := a.backfillMainChatRowTopics(t.Context(), now); err != nil {
			t.Fatal(err)
		}
		if tags := mainChatRowStoredTags(t, a, session, row.ID); len(tags) != 1 {
			t.Fatalf("historical tag permanently lost: %v", tags)
		}
		if done, _ := a.store.GetUIState(t.Context(), mainChatTopicBackfillKey); done != "true" {
			t.Fatalf("completed backfill marker=%q", done)
		}
		return
	}
}

func TestTopicDeferredChatRetriesAreBounded(t *testing.T) {
	a, _, _ := registryChatTestApp(t)
	svc := a.topicsSvc()
	ctx, cancel := context.WithCancel(t.Context())
	a.ctx = ctx
	var builds, attempts atomic.Int32
	svc.registryBeforeSnapshot = func() { builds.Add(1) }
	exhausted := make(chan struct{})
	var expire context.CancelFunc
	svc.registryChatContext = func(parent context.Context) (context.Context, context.CancelFunc) {
		child, stop := context.WithCancel(parent)
		expire = stop
		return child, stop
	}
	svc.registryDuringChat = func(string) {
		expire()
		if attempts.Add(1) == 4 {
			close(exhausted)
		}
	}
	svc.startRegistryWorker(ctx)
	t.Cleanup(func() { cancel(); svc.stopRegistryWorker() })
	select {
	case <-exhausted:
	case <-time.After(15 * time.Second):
		t.Fatalf("bounded retries did not run: %d", attempts.Load())
	}
	svc.registryMu.Lock()
	pending := svc.registryDeferredChat != nil
	svc.registryMu.Unlock()
	if pending {
		t.Fatal("exhausted chat retry stayed queued")
	}
	for i := 0; i < 20; i++ {
		view, err := a.GetTodayTopics()
		if err != nil || view.Refreshing || len(view.Topics) != 1 {
			t.Fatalf("chat exhaustion unsettled display: %+v %v", view, err)
		}
	}
	if attempts.Load() != 4 || builds.Load() != 1 {
		t.Fatalf("reads restarted work: attempts=%d builds=%d", attempts.Load(), builds.Load())
	}
}

func TestTopicDeferredChatRetryRejectsSidecarWithdrawalAtPublication(t *testing.T) {
	a, _, _ := registryChatTestApp(t)
	_, header, sidecar := retainedTopicTranscript(t, a)
	header("retry.wav")
	sidecar("retry.wav", false)
	svc := a.topicsSvc()
	ctx, cancel := context.WithCancel(t.Context())
	a.ctx = ctx
	var builds, attempts atomic.Int32
	svc.registryBeforeSnapshot = func() { builds.Add(1) }
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	var expire context.CancelFunc
	svc.registryChatContext = func(parent context.Context) (context.Context, context.CancelFunc) {
		child, stop := context.WithCancel(parent)
		expire = stop
		return child, stop
	}
	svc.registryDuringChat = func(string) {
		switch attempts.Add(1) {
		case 1:
			expire()
		case 2:
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
	}
	svc.startRegistryWorker(ctx)
	t.Cleanup(func() { unblock(); cancel(); svc.stopRegistryWorker() })
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("healthy retry did not enter publication race")
	}
	sidecar("retry.wav", true)
	unblock()
	svc.registryMu.Lock()
	c := svc.loadRegistryCache()
	if c == nil || c.chatComplete || len(svc.store.load().ChatTopics) != 0 {
		t.Error("retry published a withdrawn chat proof")
	}
	svc.registryMu.Unlock()
	if _, err := a.ListMainChatTopicItems(topicProjectID("obj:atlas"), "", 50); err == nil {
		t.Fatal("withdrawn retry authorized Open")
	}
	if builds.Load() != 1 {
		t.Fatalf("chat-only retry collected %d registries", builds.Load())
	}
}

func TestTopicRecordingHeaderEnumerationIsByteBounded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oversized.md")
	raw := "- Evidence: " + strings.Repeat("x", topicRecordingHeaderLimit) + "\n- **Audio**: activity/audio/unresolved.wav\n"
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := topicRecordingFileSnapshot(t.Context(), t.TempDir(), map[string]bool{path: true}, map[string]bool{path: true}, nil); !errors.Is(err, errTopicRegistryChanged) {
		t.Fatalf("over-cap dependency closure accepted: %v", err)
	}
}

// Equal end times must not make the bounded bootstrap sort an entire capture
// batch. This checks the SQLite execution cost shape as well as the real reader.
func TestTopicColdRecentIndexBudget(t *testing.T) {
	a := largeTopicRegistryFixtureSized(t, topicColdFixtureSessions)
	a.topicsSvc().registryTestReadBudget = time.Duration(topicColdProofBudgetMillis) * time.Millisecond
	if views, err := collectAndReadTopicViewsForTest(a); err != nil || len(views) == 0 {
		t.Fatalf("full-build snapshot: %d views, %v", len(views), err)
	}
	holdTodayTopicsBuild(t, a)
	var got TodayTopicsView
	elapsed := coldReadBestOfThree(t, func() {
		var err error
		got, err = a.GetTodayTopics()
		if err != nil || !got.Refreshing || len(got.Topics) == 0 {
			t.Fatalf("snapshot read during refresh: %+v %v", got, err)
		}
	})
	t.Logf("snapshot topics=%d best_read=%s", len(got.Topics), elapsed)
	if elapsed >= 3*time.Duration(topicColdProofBudgetMillis)*time.Millisecond {
		t.Fatalf("cold tied-row reader exceeded budget: best=%s", elapsed)
	}
	now := a.topicsSvc().now()
	rows, err := a.activity.store.reader().QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+topicTodayRecentQuery, now.Add(-topicWindow).UnixMilli(), now.UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		t.Log(detail)
		if strings.Contains(strings.ToUpper(detail), "TEMP B-TREE") {
			t.Error("cold window sorts tied capture rows instead of following its index")
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}
