package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/pii"
	"wails-gui/storage"
	"yansu-agent/agent"
)

type chatTopicFake struct {
	onKeys func()
	topic  ChatTopic
	keys   ChatTopicKeys
}

func (f *chatTopicFake) TopicByID(ctx context.Context, id string) (ChatTopic, error) {
	if id != f.topic.ID {
		return ChatTopic{}, errTodayChatItemUnavailable
	}
	return f.topic, ctx.Err()
}
func (f *chatTopicFake) TopicKeys(ctx context.Context, id string) (ChatTopicKeys, error) {
	if f.onKeys != nil {
		f.onKeys()
	}
	return f.keys, ctx.Err()
}

func topicChatTestApp(t *testing.T) (*App, string) {
	t.Helper()
	a := newTodayContextTestApp(t, true)
	main, err := a.ensureMainChat(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	writeTodayContextMemory(t, "atlas", "# Atlas\nCurrent release plan")
	a.topicSource = &chatTopicFake{topic: ChatTopic{ID: "atlas", Name: "Atlas", Status: "active", Latest: "Review ready", Next: "Ship", Refs: []TodayChatContextRef{{SourceKind: "memory", SourceID: "atlas"}}}, keys: ChatTopicKeys{ThreadKeys: []string{"obj:atlas"}}}
	fake := a.topicSource.(*chatTopicFake)
	fake.topic.SourceFingerprint, err = a.chatTopicSourceFingerprint(t.Context(), fake.topic.Refs)
	if err != nil {
		t.Fatal(err)
	}
	a.activeTags = map[string]bool{}
	a.tagCancels = map[string]context.CancelFunc{}
	a.sessionTempDirs = map[string]string{}
	a.streams = map[string]context.CancelFunc{}
	a.runIDByTag = map[string]string{}
	a.pendingTraceIDs = map[string]string{}
	return a, main.LocalID
}

type topicCaptureRunner struct {
	fallback bool
	upcomingCaptureRunner
	a      *App
	tag    string
	prompt chan string
}

func (r *topicCaptureRunner) ExecuteTask(ctx context.Context, input *agent.TaskInput, emit agent.StreamCallback) (string, error) {
	// Same callback and persistence order as the real streaming renderer.
	if err := emit(&agent.StreamEvent{Type: "run_created", RunID: "topic-run"}); err != nil {
		return "", err
	}
	runID := "topic-run"
	if r.fallback {
		if _, err := r.a.store.AppendStreamItem(ctx, r.tag, storage.StreamItemRow{ID: "first-attempt", Kind: "assistant", RunID: runID, Content: "First attempt"}); err != nil {
			return "", err
		}
		runID = "fallback-run"
		if err := emit(&agent.StreamEvent{Type: "run_created", RunID: runID}); err != nil {
			return "", err
		}
	}
	text := input.Prompt
	if input.PromptFunc != nil {
		text, _ = input.PromptFunc()
	}
	r.prompt <- text
	_, err := r.a.store.AppendStreamItem(ctx, r.tag, storage.StreamItemRow{ID: "reply", Kind: "assistant", Content: "Reply", RunID: runID})
	return runID, err
}

func TestTopicSendOnlyWordsTagsRunAndNeverCreatesSession(t *testing.T) {
	a, tag := topicChatTestApp(t)
	r := &topicCaptureRunner{a: a, tag: tag, prompt: make(chan string, 1)}
	a.runner = r
	before, err := a.store.LoadSessions(t.Context(), standaloneChatProjectID)
	if err != nil {
		t.Fatal(err)
	}
	sent, err := a.SendMainChatWithTopic("Help with Atlas on 2026-10-21, password: correct-horse-battery-staple", "atlas")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case prompt := <-r.prompt:
		if !strings.Contains(prompt, "Review ready") || !strings.Contains(prompt, "Ship") || strings.Contains(prompt, "correct-horse-battery-staple") {
			t.Fatalf("context/redaction: %s", prompt)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runner not called")
	}
	waitForTagIdle(t, a, tag)
	rows, err := a.store.LoadStreamItems(t.Context(), tag)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range rows {
		if row.ID == sent.UserItemID {
			found = true
			if row.Content != todayChatRedactPrompt("Help with Atlas on 2026-10-21, password: correct-horse-battery-staple") || strings.Contains(row.Content, "Review ready") || row.RunID != "topic-run" {
				t.Fatalf("bubble=%+v", row)
			}
		}
	}
	if !found {
		t.Fatal("missing user")
	}
	page, err := a.ListMainChatTopicItems("atlas", "", 50)
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("topic page=%+v err=%v", page, err)
	}
	after, err := a.store.LoadSessions(t.Context(), standaloneChatProjectID)
	if err != nil || len(before) != len(after) || sent.SessionID != tag {
		t.Fatalf("session changed: before=%d after=%d err=%v", len(before), len(after), err)
	}
}

func seedTopicEvent(t *testing.T, a *App, id, appName, status string, at int64) {
	t.Helper()
	apps, _ := json.Marshal([]string{appName})
	if err := a.activity.store.SaveSession(ActivitySessionRow{ID: "s-" + id, AppNames: string(apps), TriageStatus: status, StartTime: at - 1000, EndTime: at}, nil, nil); err != nil {
		t.Fatal(err)
	}
	run := testRun("r-"+id, appName, at-1000, at, 1000, 1)
	run.SessionIDs = []string{"s-" + id}
	run.FirstEventRowID, run.LastEventRowID = at, at
	insertComposableTimelineRun(t, a.activity.store, run, ActivityTimelineGroupSummary{})
	appTimes, _ := json.Marshal(map[string]int64{appName: 1000})
	if err := a.activity.store.InsertActivity(ActivityRow{ID: id, Title: "Atlas " + id, RunIDs: fmt.Sprintf(`["r-%s"]`, id), ActiveMsByApp: string(appTimes), Entities: `[{"type":"project","name":"Atlas"}]`, StartMs: at - 1000, EndMs: at}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.activity.store.db.Exec(`INSERT INTO activity_threads(id,day_start_ms,title,created_at,updated_at) VALUES(?,0,'unused private synthesis',1,1)`, "t-"+id); err != nil {
		t.Fatal(err)
	}
	if _, err := a.activity.store.db.Exec(`INSERT INTO activity_thread_members(activity_id,thread_id,day_start_ms,source,updated_at) VALUES(?,?,0,'rule',1)`, id, "t-"+id); err != nil {
		t.Fatal(err)
	}
}

func seedTopicMessage(t *testing.T, a *App, tag, id, topic string, at int64) {
	t.Helper()
	if _, err := a.store.AppendUserStreamItemAtomic(t.Context(), tag, storage.StreamItemRow{ID: id, Kind: "user", Content: id, TopicID: topic}); err != nil {
		t.Fatal(err)
	}
	// Use a run row with a chosen timestamp for deterministic merged ordering.
	if err := a.store.BindStreamItemTopicRun(t.Context(), tag, id, "run-"+id); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.AppendStreamItem(t.Context(), tag, storage.StreamItemRow{ID: "reply-" + id, Kind: "assistant", Content: id, RunID: "run-" + id, CreatedAt: at}); err != nil {
		t.Fatal(err)
	}
}

func TestTopicReadFiltersMergesAndPagesTies(t *testing.T) {
	a, tag := topicChatTestApp(t)
	at := time.Now().Add(-time.Hour).UnixMilli()
	seedTopicEvent(t, a, "a", "Code", "done", at)
	seedTopicEvent(t, a, "b", "Code", "done", at+1)
	seedTopicMessage(t, a, tag, "alpha", "atlas", at)
	seedTopicMessage(t, a, tag, "other", "other", at)
	ordinary, err := a.LoadStreamItemsPage(tag, -1, 100)
	if err != nil {
		t.Fatal(err)
	}
	var all []MainChatTopicItem
	cursor := ""
	for i := 0; i < 10; i++ {
		page, err := a.ListMainChatTopicItems("atlas", cursor, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) != 1 {
			t.Fatalf("empty page: %+v", page)
		}
		all = append(page.Items, all...)
		if !page.HasMore {
			break
		}
		if cursor == page.NextCursor {
			t.Fatal("cursor stalled")
		}
		cursor = page.NextCursor
	}
	if len(all) != 4 {
		t.Fatalf("items=%+v", all)
	}
	seen := map[string]bool{}
	for i, item := range all {
		if seen[item.ID] {
			t.Fatalf("duplicate %s", item.ID)
		}
		seen[item.ID] = true
		if i > 0 && (all[i-1].At > item.At || (all[i-1].At == item.At && all[i-1].ID > item.ID)) {
			t.Fatalf("unordered %+v", all)
		}
		if item.Kind == "message" {
			if strings.Contains(item.ID, "other") {
				t.Fatal("wrong topic")
			}
			found := false
			for _, row := range ordinary.Items {
				if row.ID == item.Message.ID {
					found = true
					left, _ := json.Marshal(row)
					right, _ := json.Marshal(item.Message)
					if string(left) != string(right) {
						t.Fatalf("projection differs: %s %s", left, right)
					}
				}
			}
			if !found {
				t.Fatal("not ordinary projection")
			}
		}
	}
	if _, err := a.ListMainChatTopicItems("other", cursor, 1); err != errTopicCursor {
		t.Fatalf("cross-topic cursor: %v", err)
	}
}

func TestTopicPrivacyForgetAndClosedEyes(t *testing.T) {
	a, tag := topicChatTestApp(t)
	at := time.Now().Add(-time.Hour).UnixMilli()
	seedTopicMessage(t, a, tag, "kept", "atlas", at)
	seedTopicEvent(t, a, "safe", "Code", "done", at)
	seedTopicEvent(t, a, "mail", "Mail", "done", at)
	seedTopicEvent(t, a, "slack", "Slack", "done", at)
	seedTopicEvent(t, a, "protected", "Code", "protected", at)
	seedTopicEvent(t, a, "private - Incognito", "Code", "done", at)
	seedTopicEvent(t, a, "sensitive", "Journal", "done", at)
	prior := pii.GetUserSensitiveApps()
	t.Cleanup(func() { pii.SetUserSensitiveApps(prior) })
	pii.SetUserSensitiveApps(map[string]string{"local.journal": "Journal"})
	page, err := a.ListMainChatTopicItems("atlas", "", 100)
	if err != nil || len(page.Items) != 3 {
		t.Fatalf("privacy page=%+v err=%v", page, err)
	}
	if err := a.activity.store.DeleteActivity("safe"); err != nil {
		t.Fatal(err)
	}
	page, err = a.ListMainChatTopicItems("atlas", "", 100)
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("forgotten event survived: %+v %v", page, err)
	}
	seedTopicEvent(t, a, "new-safe", "Code", "done", at+10)
	page, err = a.ListMainChatTopicItems("atlas", "", 100)
	if err != nil || len(page.Items) != 3 {
		t.Fatalf("closed eyes control: %+v %v", page, err)
	}
	a.closedEyes.enabled.Store(true)
	if _, err := a.buildTodayChatContext(t.Context(), TodayChatContextRef{SourceKind: "topic", SourceID: "atlas"}); err != errTodayChatObservationPaused {
		t.Fatalf("closed eyes source: %v", err)
	}
	page, err = a.ListMainChatTopicItems("atlas", "", 100)
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("closed eyes page=%+v %v", page, err)
	}
	if _, err := a.SendMainChatWithTopic("Help", "atlas"); err == nil {
		t.Fatal("closed eyes sent source")
	}
}

func TestTopicSourceCorrectionAndMissingRegistryFailClosed(t *testing.T) {
	a, tag := topicChatTestApp(t)
	if _, err := a.buildTodayChatContext(t.Context(), TodayChatContextRef{SourceKind: "topic", SourceID: "atlas"}); err != nil {
		t.Fatal(err)
	}
	writeTodayContextMemory(t, "atlas", "# Atlas\nCorrected private source; stale metadata must not stand")
	if _, err := a.buildTodayChatContext(t.Context(), TodayChatContextRef{SourceKind: "topic", SourceID: "atlas"}); err == nil {
		t.Fatal("stale metadata admitted")
	}
	if _, err := a.SendMainChatWithTopic("Help", "atlas"); err == nil {
		t.Fatal("stale metadata sent")
	}
	if _, err := a.ListMainChatTopicItems("atlas", "", 50); err == nil {
		t.Fatal("stale topic association read")
	}

	rows, err := a.store.LoadStreamItems(t.Context(), tag)
	if err != nil || len(rows) != 0 {
		t.Fatalf("rejected send published rows: %+v %v", rows, err)
	}
	a.topicSource = nil
	if _, err := a.SendMainChatWithTopic("Help", "atlas"); err == nil {
		t.Fatal("missing registry sent")
	}
}

func TestTopicForgetOriginalSourceAndPauseWithdrawEvents(t *testing.T) {
	a, tag := topicChatTestApp(t)
	at := time.Now().Add(-time.Hour).UnixMilli()
	seedTopicMessage(t, a, tag, "kept", "atlas", at)
	seedTopicEvent(t, a, "source", "Code", "done", at)
	page, err := a.ListMainChatTopicItems("atlas", "", 100)
	if err != nil || len(page.Items) != 3 {
		t.Fatalf("control: %+v %v", page, err)
	}
	if err := a.activity.store.DeleteSession("s-source"); err != nil {
		t.Fatal(err)
	}
	page, err = a.ListMainChatTopicItems("atlas", "", 100)
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("forgotten source: %+v %v", page, err)
	}
	seedTopicEvent(t, a, "paused", "Code", "done", at+10000)
	a.saveObservationPauses([]observationPause{{Start: at + 9500, End: at + 11000}})
	page, err = a.ListMainChatTopicItems("atlas", "", 100)
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("paused source: %+v %v", page, err)
	}
}

func TestTopicSendDoesNotCreateMissingMainChat(t *testing.T) {
	a, _ := topicChatTestApp(t)
	// A separate real store with no sessions exercises the missing-main path.
	empty := mainChatTestApp(t)
	empty.topicSource = a.topicSource
	if _, err := empty.SendMainChatWithTopic("Help", "atlas"); err == nil {
		t.Fatal("missing main sent")
	}
	if _, err := empty.store.LoadMainSession(t.Context()); err == nil {
		t.Fatal("send created a main session")
	}
}

func TestTopicAdmissionRevalidatesAfterRetrieval(t *testing.T) {
	a, tag := topicChatTestApp(t)
	index, err := NewMemoryStore(t.TempDir(), MemoryStoreConfig{Provider: "none"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = index.Close() })
	index.dirty = false
	a.memoryStore = index
	a.runner = &upcomingCaptureRunner{inputs: make(chan *agent.TaskInput, 1)}
	wait, release := blockTodayLookup(t, index.db)
	done := make(chan error, 1)
	go func() { _, err := a.SendMainChatWithTopic("Help with Atlas", "atlas"); done <- err }()
	wait()
	writeTodayContextMemory(t, "atlas", "# Atlas\nThe original source was corrected during retrieval")
	release()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("stale topic admitted")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("send remained blocked")
	}
	rows, err := a.store.LoadStreamItems(t.Context(), tag)
	if err != nil || len(rows) != 0 {
		t.Fatalf("partial history: %+v %v", rows, err)
	}
}

func TestTopicContextUsesOnlyReadableThreadTitleBasis(t *testing.T) {
	a, _ := topicChatTestApp(t)
	at := time.Now().Add(-time.Hour).UnixMilli()
	seedTopicEvent(t, a, "safe", "Code", "done", at)
	seedTopicEvent(t, a, "hidden", "Mail", "done", at+1)
	if _, err := a.activity.store.db.Exec(`UPDATE activity_threads SET title='withheld synthesis',title_basis='["safe","hidden"]' WHERE id='t-safe'`); err != nil {
		t.Fatal(err)
	}
	got, err := a.buildTodayChatContext(t.Context(), TodayChatContextRef{SourceKind: "topic", SourceID: "atlas"})
	if err != nil || strings.Contains(got.Text, "withheld synthesis") || !strings.Contains(got.Text, "Atlas safe") {
		t.Fatalf("title basis: %s %v", got.Text, err)
	}
	if _, err := a.activity.store.db.Exec(`UPDATE activity_threads SET user_title='My release work' WHERE id='t-safe'`); err != nil {
		t.Fatal(err)
	}
	got, err = a.buildTodayChatContext(t.Context(), TodayChatContextRef{SourceKind: "topic", SourceID: "atlas"})
	if err != nil || !strings.Contains(got.Text, "My release work") {
		t.Fatalf("user thread title: %s %v", got.Text, err)
	}
}

func TestTopicReadRejectsConcurrentPrivacyRevision(t *testing.T) {
	a, _ := topicChatTestApp(t)
	at := time.Now().Add(-time.Hour).UnixMilli()
	seedTopicEvent(t, a, "safe", "Code", "done", at)
	page, err := a.ListMainChatTopicItems("atlas", "", 50)
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("control: %+v %v", page, err)
	}
	a.topicSource.(*chatTopicFake).onKeys = func() {
		if err := a.store.SetUIState(t.Context(), "topic-privacy-revision", "withdrawn"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.ListMainChatTopicItems("atlas", "", 50); err != storage.ErrReadRevisionChanged {
		t.Fatalf("stale read published: %v", err)
	}
}

func TestTopicSendRetainsTagsAcrossRoutedFallbackRuns(t *testing.T) {
	a, tag := topicChatTestApp(t)
	r := &topicCaptureRunner{a: a, tag: tag, prompt: make(chan string, 1), fallback: true}
	a.runner = r
	sent, err := a.SendMainChatWithTopic("Help with Atlas", "atlas")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-r.prompt:
	case <-time.After(5 * time.Second):
		t.Fatal("fallback stopped")
	}
	waitForTagIdle(t, a, tag)
	user, err := a.store.LoadStreamItem(t.Context(), sent.UserItemID)
	if err != nil || user.RunID != "fallback-run" {
		t.Fatalf("fallback binding: %+v %v", user, err)
	}
	page, err := a.ListMainChatTopicItems("atlas", "", 50)
	if err != nil || len(page.Items) != 3 {
		t.Fatalf("fallback rows: %+v %v", page, err)
	}
}

func TestTopicPostAdmissionWithdrawalBlocksDispatch(t *testing.T) {
	for _, change := range []string{"correct", "forget", "closed-eyes"} {
		t.Run(change, func(t *testing.T) {
			a, tag := topicChatTestApp(t)
			a.runner = nil
			a.readyCh = make(chan struct{})
			ready := a.readyCh
			sent, err := a.SendMainChatWithTopic("Help with Atlas", "atlas")
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "correct":
				writeTodayContextMemory(t, "atlas", "# Atlas\nCorrected after admission")
			case "forget":
				if err := a.DeleteMemoryEntry("atlas"); err != nil {
					t.Fatal(err)
				}
			case "closed-eyes":
				a.closedEyes.enabled.Store(true)
			}
			capture := &upcomingCaptureRunner{inputs: make(chan *agent.TaskInput, 1)}
			a.mu.Lock()
			a.runner = capture
			a.mu.Unlock()
			close(ready)
			waitForTagIdle(t, a, tag)
			select {
			case <-capture.inputs:
				t.Fatal("withdrawn topic reached runner")
			default:
			}
			user, err := a.store.LoadStreamItem(t.Context(), sent.UserItemID)
			if err != nil || user == nil || user.Content != "Help with Atlas" {
				t.Fatalf("accepted words lost: %+v %v", user, err)
			}
		})
	}
}

func TestTopicHistoryOmitsWithdrawnSourceAfterRun(t *testing.T) {
	a, tag := topicChatTestApp(t)
	r := &topicCaptureRunner{a: a, tag: tag, prompt: make(chan string, 1)}
	a.runner = r
	if _, err := a.SendMainChatWithTopic("Help with Atlas", "atlas"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-r.prompt:
	case <-time.After(5 * time.Second):
		t.Fatal("runner missing")
	}
	waitForTagIdle(t, a, tag)
	if _, bound, err := a.sourceBoundChatHistory(t.Context(), tag); err != nil || !bound {
		t.Fatalf("valid topic history: %v %v", bound, err)
	}
	if err := a.DeleteMemoryEntry("atlas"); err != nil {
		t.Fatal(err)
	}
	if rows, bound, err := a.sourceBoundChatHistory(t.Context(), tag); err != nil || bound || len(rows) != 0 {
		t.Fatalf("withdrawn topic history: rows=%+v bound=%v err=%v", rows, bound, err)
	}
}

func TestTopicReadRejectsMalformedCursorIdentity(t *testing.T) {
	a, _ := topicChatTestApp(t)
	for _, id := range []string{"bogus", "message:", "event:"} {
		raw, _ := json.Marshal(topicItemCursor{TopicID: "atlas", At: 123, ID: id})
		if _, err := a.ListMainChatTopicItems("atlas", base64.RawURLEncoding.EncodeToString(raw), 1); err != errTopicCursor {
			t.Fatalf("cursor %q: %v", id, err)
		}
	}
}

type topicIgnoringCallbackRunner struct {
	upcomingCaptureRunner
	a       *App
	tag     string
	checked chan error
}

func (r *topicIgnoringCallbackRunner) ExecuteTask(ctx context.Context, input *agent.TaskInput, emit agent.StreamCallback) (string, error) {
	if input.Prompt == "" || input.BeforeDispatch == nil {
		r.checked <- fmt.Errorf("missing composed prompt or dispatch gate")
		return "", nil
	}
	if err := r.a.store.DeleteStreamItemsFrom(ctx, r.tag, 0); err != nil {
		r.checked <- err
		return "", err
	}
	// Match the native runner: ignore the run_created callback result.
	_ = emit(&agent.StreamEvent{Type: "run_created", RunID: "orphaned-run"})
	// Even an incorrectly detached dispatch context cannot bypass the durable
	// seed check; the real native runner also observes ctx cancellation.
	err := input.BeforeDispatch(context.Background())
	if err == nil || ctx.Err() == nil {
		r.checked <- fmt.Errorf("orphaned topic could dispatch: gate=%v cancel=%v", err, ctx.Err())
	} else {
		r.checked <- nil
	}
	return "orphaned-run", err
}

func TestTopicIgnoredRunCreatedErrorStillRefusesDispatch(t *testing.T) {
	a, tag := topicChatTestApp(t)
	runner := &topicIgnoringCallbackRunner{a: a, tag: tag, checked: make(chan error, 1)}
	a.runner = runner
	if _, err := a.SendMainChatWithTopic("Help with Atlas", "atlas"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-runner.checked:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not check dispatch")
	}
	waitForTagIdle(t, a, tag)
}

func TestTopicHistoryWindowExcludesOldProofs(t *testing.T) {
	a, tag := topicChatTestApp(t)
	if _, err := a.store.AppendUserStreamItemAtomic(t.Context(), tag, storage.StreamItemRow{ID: "old", Kind: "user", TopicID: "atlas", Content: "old private answer", AutomationContext: `{"topicContext":{"version":0}}`}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 30; i++ {
		if _, err := a.store.AppendStreamItem(t.Context(), tag, storage.StreamItemRow{ID: fmt.Sprint("recent-", i), Kind: "assistant", Content: "ordinary recent answer"}); err != nil {
			t.Fatal(err)
		}
	}
	rows, bound, err := a.sourceBoundChatHistory(t.Context(), tag)
	if err != nil || bound || len(rows) != 30 {
		t.Fatalf("old proof affected current window: rows=%d bound=%v err=%v", len(rows), bound, err)
	}
}

func TestTopicHistoryTurnCostBoundedWithLifetimeSends(t *testing.T) {
	for _, n := range []int{0, 1000} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			a, tag := topicChatTestApp(t)
			for i := 0; i < n; i++ {
				metadata := fmt.Sprintf(`{"topicContext":{"version":1,"refs":[{"sourceKind":"memory","sourceId":"old-%d"}],"fingerprint":"%064d"}}`, i, i)
				if _, err := a.store.AppendUserStreamItemAtomic(t.Context(), tag, storage.StreamItemRow{ID: fmt.Sprint("old-", i), Kind: "user", Content: "old topic", TopicID: "atlas", AutomationContext: metadata}); err != nil {
					t.Fatal(err)
				}
			}
			metadata, err := a.topicChatAdmissionMetadataFromSources(t.Context(), []TodayChatContextRef{{SourceKind: "memory", SourceID: "atlas"}}, a.collectTodayChatSources(t.Context(), time.Now()))
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 30; i++ {
				row := storage.StreamItemRow{ID: fmt.Sprint("window-", i), Kind: "user", Content: "recent topic", TopicID: "atlas", AutomationContext: metadata}
				if _, err := a.store.AppendUserStreamItemAtomic(t.Context(), tag, row); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel, err := a.beginChatTurn(tag)
			if err != nil {
				t.Fatal(err)
			}
			defer a.finishChatPreparation(tag, cancel)
			for i := 0; i < 5; i++ {
				rows, bound, err := a.sourceBoundChatHistory(ctx, tag)
				if err != nil || !bound || len(rows) != 30 {
					t.Fatalf("window: rows=%d bound=%v err=%v", len(rows), bound, err)
				}
			}
			loader := &streamItemHistoryLoader{store: a.store, loadRows: a.modelChatHistoryRows}
			if _, err := loader.LoadHistory(tag); err != nil {
				t.Fatal(err)
			}
			cache := &topicTurnFromContext(ctx).history
			if cache.collections != 1 || cache.checks != 1 {
				t.Fatalf("lifetime=%d collections=%d proofs=%d", n, cache.collections, cache.checks)
			}
			// An external file edit must invalidate the cache even with unchanged DBs.
			writeTodayContextMemory(t, "atlas", "# Atlas\nCorrected source")
			rows, bound, err := a.sourceBoundChatHistory(ctx, tag)
			if err != nil || bound || len(rows) != 0 || cache.collections != 2 || cache.checks != 2 {
				t.Fatalf("cached withdrawal: rows=%d bound=%v err=%v collections=%d checks=%d", len(rows), bound, err, cache.collections, cache.checks)
			}
		})
	}
}

type topicHistoryCaptureRunner struct {
	upcomingCaptureRunner
	checked chan error
}

func (r *topicHistoryCaptureRunner) ExecuteTask(ctx context.Context, input *agent.TaskInput, _ agent.StreamCallback) (string, error) {
	if input.PromptFunc != nil {
		input.Prompt, input.History = input.PromptFunc()
	}
	if input.BeforeDispatch != nil {
		if err := input.BeforeDispatch(ctx); err != nil {
			r.checked <- err
			return "", err
		}
	}
	r.inputs <- input
	r.checked <- nil
	return "followup", nil
}
func TestTopicHistoryStaleExchangeKeepsLaterAssistantChoice(t *testing.T) {
	for _, origin := range []promptOrigin{promptOriginMac, promptOriginPhone} {
		for _, change := range []string{"edit", "delete", "forget", "closed-eyes", "during-compose", "valid", "outside-window"} {
			t.Run(string(origin)+"/"+change, func(t *testing.T) {
				a, tag := topicChatTestApp(t)
				topicRunner := &topicCaptureRunner{a: a, tag: tag, prompt: make(chan string, 1)}
				a.runner = topicRunner
				if _, err := a.SendMainChatWithTopic("private old question", "atlas"); err != nil {
					t.Fatal(err)
				}
				select {
				case <-topicRunner.prompt:
				case <-time.After(5 * time.Second):
					t.Fatal("topic runner missing")
				}
				waitForTagIdle(t, a, tag)
				if _, err := a.store.AppendStreamItem(t.Context(), tag, storage.StreamItemRow{ID: "ordinary-control", Kind: "assistant", Content: "ordinary retained answer"}); err != nil {
					t.Fatal(err)
				}

				switch change {
				case "edit":
					writeTodayContextMemory(t, "atlas", "# Atlas\nCorrected source")
				case "delete":
					dir, err := memoryDir()
					if err != nil {
						t.Fatal(err)
					}
					if err := os.Remove(filepath.Join(dir, "atlas.md")); err != nil {
						t.Fatal(err)
					}
				case "forget":
					if err := a.DeleteMemoryEntry("atlas"); err != nil {
						t.Fatal(err)
					}
				case "closed-eyes":
					a.closedEyes.enabled.Store(true)
				case "outside-window":
					for i := 0; i < 30; i++ {
						if _, err := a.store.AppendStreamItem(t.Context(), tag, storage.StreamItemRow{ID: fmt.Sprint("fresh-", i), Kind: "assistant", Content: "recent ordinary answer"}); err != nil {
							t.Fatal(err)
						}
					}
				}
				capture := &topicHistoryCaptureRunner{upcomingCaptureRunner: upcomingCaptureRunner{inputs: make(chan *agent.TaskInput, 1)}, checked: make(chan error, 1)}
				a.runner = capture
				if err := a.executePrompt("new ordinary question", "", "chosen-model", "provider-session", tag, nil, false, false, "en", nil, func(text, lang string) string {
					if change == "during-compose" {
						writeTodayContextMemory(t, "atlas", "# Atlas\nChanged during composition")
					}
					return text
				}, "", "", origin); err != nil {
					t.Fatal(err)
				}
				select {
				case err := <-capture.checked:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("later turn did not run")
				}
				input := <-capture.inputs
				waitForTagIdle(t, a, tag)
				wantBound := change == "valid"
				if input.RequireNativeDispatch != wantBound || input.Model != "chosen-model" || input.BackendHint != agent.BackendHintChat {
					t.Fatalf("assistant route changed: %+v", input)
				}
				if change == "valid" || change == "outside-window" {
					if input.SessionID != "provider-session" {
						t.Fatalf("valid session reset: %q", input.SessionID)
					}
				} else if input.SessionID == "" || input.SessionID == "provider-session" {
					t.Fatalf("withdrawn session was not rotated to a stable identity: %q", input.SessionID)
				}
				if !wantBound && (strings.Contains(input.History, "private old question") || strings.Contains(input.History, "Reply")) {
					t.Fatalf("stale exchange in text history: %s", input.History)
				}
				if !strings.Contains(input.History, "ordinary retained answer") && change != "outside-window" {
					t.Fatalf("readable history lost: %s", input.History)
				}

				loader := &streamItemHistoryLoader{store: a.store, loadRows: a.modelChatHistoryRows}
				history, err := loader.LoadHistory(tag)
				if err != nil {
					t.Fatal(err)
				}
				for _, item := range history {
					if !wantBound && (strings.Contains(item.Content, "private old question") || item.Content == "Reply") {
						t.Fatalf("stale exchange in native history: %+v", item)
					}
				}
			})
		}
	}
}

func TestTopicEventScanBudgetPreservesContinuationAndInterleavedMessages(t *testing.T) {
	a, tag := topicChatTestApp(t)
	at := time.Now().Add(-time.Hour).UnixMilli()
	seedTopicEvent(t, a, "older", "Code", "done", at)
	seedTopicMessage(t, a, tag, "older-message", "atlas", at-1)
	// Unrelated and unthreaded moments still consume the candidate budget.
	for i := 1; i <= topicEventScanLimit+3; i++ {
		if err := a.activity.store.InsertActivity(ActivityRow{ID: fmt.Sprint("unrelated-", i), Title: "Other work", StartMs: at + int64(i) - 1, EndMs: at + int64(i)}); err != nil {
			t.Fatal(err)
		}
	}
	scan, err := a.scanChatTopicEvents(t.Context(), ChatTopicKeys{ThreadKeys: []string{"obj:atlas"}}, topicItemCursor{At: math.MaxInt64, ID: "~"}, 50)
	if err != nil || scan.scanned != topicEventScanLimit || !scan.more || len(scan.items) != 0 {
		t.Fatalf("unbounded scan: %+v %v", scan, err)
	}
	page, err := a.ListMainChatTopicItems("atlas", "", 50)
	// The newer user row may be returned; the older reply must wait for events.
	if err != nil || !page.HasMore || page.NextCursor == "" {
		t.Fatalf("continuation: %+v %v", page, err)
	}
	for _, item := range page.Items {
		if item.ID == "reply-older-message" || item.At <= at {
			t.Fatalf("passed unscanned events: %+v", item)
		}
	}
	older, err := a.ListMainChatTopicItems("atlas", page.NextCursor, 50)
	if err != nil || older.HasMore {
		t.Fatalf("older continuation: %+v %v", older, err)
	}
	event, reply := false, false
	for i, item := range older.Items {
		event = event || item.ID == "event:older"
		reply = reply || item.ID == "message:reply-older-message"
		if i > 0 && (older.Items[i-1].At > item.At || (older.Items[i-1].At == item.At && older.Items[i-1].ID > item.ID)) {
			t.Fatal("out of time order")
		}
	}
	if !event || !reply {
		t.Fatalf("lost interleaved items: %+v", older)
	}
}

func TestTopicHistoryPhoneBridgeContinuesAfterForget(t *testing.T) {
	a, tag := topicChatTestApp(t)
	initial := &topicCaptureRunner{a: a, tag: tag, prompt: make(chan string, 1)}
	a.runner = initial
	if _, err := a.SendMainChatWithTopic("old topic question", "atlas"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-initial.prompt:
	case <-time.After(5 * time.Second):
		t.Fatal("missing topic runner")
	}
	waitForTagIdle(t, a, tag)
	if err := a.DeleteMemoryEntry("atlas"); err != nil {
		t.Fatal(err)
	}
	capture := &topicHistoryCaptureRunner{upcomingCaptureRunner: upcomingCaptureRunner{inputs: make(chan *agent.TaskInput, 1)}, checked: make(chan error, 1)}
	a.runner = capture
	bridge, err := newMobileBridge(a, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bridge.Stop)
	row, _, err := a.store.MobileChat(t.Context(), tag)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := bridge.startChatTurn(row, "phone followup", nil); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-capture.checked:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("missing phone runner")
	}
	input := <-capture.inputs
	waitForTagIdle(t, a, tag)
	if input.RequireNativeDispatch || input.BackendHint != agent.BackendHintChat || strings.Contains(input.History, "old topic question") || strings.Contains(input.History, "Reply") {
		t.Fatalf("phone history/route: %+v", input)
	}
}

func TestTopicHistoryCacheInvalidatesActivityAndLivePolicy(t *testing.T) {
	for _, change := range []string{"activity-db", "main-db", "sensitive", "closed-eyes"} {
		t.Run(change, func(t *testing.T) {
			a, tag := topicChatTestApp(t)
			at := time.Now().Add(-time.Hour).UnixMilli()
			seedTopicEvent(t, a, "source", "Code", "done", at)
			metadata, err := a.topicChatAdmissionMetadataFromSources(t.Context(), []TodayChatContextRef{{SourceKind: "activity", SourceID: "source"}}, a.collectTodayChatSources(t.Context(), time.Now()))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := a.store.AppendUserStreamItemAtomic(t.Context(), tag, storage.StreamItemRow{ID: "source-user", Kind: "user", Content: "source words", TopicID: "atlas", AutomationContext: metadata}); err != nil {
				t.Fatal(err)
			}
			ctx, cancel, err := a.beginChatTurn(tag)
			if err != nil {
				t.Fatal(err)
			}
			defer a.finishChatPreparation(tag, cancel)
			if _, bound, err := a.sourceBoundChatHistory(ctx, tag); err != nil || !bound {
				t.Fatalf("valid: %v %v", bound, err)
			}
			prior := pii.GetUserSensitiveApps()
			t.Cleanup(func() { pii.SetUserSensitiveApps(prior) })
			switch change {
			case "activity-db":
				if _, err := a.activity.store.db.Exec(`UPDATE activity_sessions SET triage_status='protected' WHERE id='s-source'`); err != nil {
					t.Fatal(err)
				}
			case "main-db":
				if err := a.store.SetUIState(t.Context(), observationPausesKey, fmt.Sprintf(`[{"start":%d,"end":%d}]`, at-2000, at+1)); err != nil {
					t.Fatal(err)
				}
			case "sensitive":
				pii.SetUserSensitiveApps(map[string]string{"local.code": "Code"})
			case "closed-eyes":
				a.closedEyes.enabled.Store(true)
			}
			rows, bound, err := a.sourceBoundChatHistory(ctx, tag)
			if err != nil || bound || len(rows) != 0 {
				t.Fatalf("cached access survived %s: rows=%d bound=%v err=%v", change, len(rows), bound, err)
			}

		})
	}
}

func TestTopicHistoryFrozenRequestStopsButNextTurnContinues(t *testing.T) {
	a, tag := topicChatTestApp(t)
	metadata, err := a.topicChatAdmissionMetadataFromSources(t.Context(), []TodayChatContextRef{{SourceKind: "memory", SourceID: "atlas"}}, a.collectTodayChatSources(t.Context(), time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.AppendUserStreamItemAtomic(t.Context(), tag, storage.StreamItemRow{ID: "old-user", Kind: "user", Content: "old topic", TopicID: "atlas", AutomationContext: metadata}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel, err := a.beginChatTurn(tag)
	if err != nil {
		t.Fatal(err)
	}
	rows, bound, err := a.sourceBoundChatHistory(ctx, tag)
	if err != nil || !bound {
		t.Fatalf("valid history: %v %v", bound, err)
	}
	freezeTopicChatHistory(ctx, rows)
	writeTodayContextMemory(t, "atlas", "# Atlas\nEdited before dispatch")
	if err := a.validateTurnTopicContext(ctx); err != errChatSourceAccessChanged {
		t.Fatalf("frozen request reused withdrawn history: %v", err)
	}
	a.finishChatPreparation(tag, cancel)
	next, nextCancel, err := a.beginChatTurn(tag)
	if err != nil {
		t.Fatal(err)
	}
	defer a.finishChatPreparation(tag, nextCancel)
	if rows, bound, err := a.sourceBoundChatHistory(next, tag); err != nil || bound || len(rows) != 0 {
		t.Fatalf("next turn poisoned: %+v %v %v", rows, bound, err)
	}
	if err := a.validateTurnTopicContext(next); err != nil {
		t.Fatalf("next ordinary turn refused: %v", err)
	}
}
