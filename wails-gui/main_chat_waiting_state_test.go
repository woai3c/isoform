package main

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"wails-gui/internal/engagement"
	"wails-gui/storage"
)

func newWaitingStateFixture(t *testing.T) (*mainChatReadMemoFixture, chan []byte) {
	t.Helper()
	fixture := newMainChatReadMemoFixture(t)
	broadcaster := NewEventBroadcaster()
	events := broadcaster.AddClient("waiting-state-test")
	fixture.app.eventBroadcaster = broadcaster
	if initial := fixture.app.GetMainChatWaiting(); initial.Ready || initial.Revision != 0 {
		t.Fatalf("initial waiting state=%+v, want unready revision 0", initial)
	}
	fixture.app.startMainChatWaitingLifecycle(t.Context())
	t.Cleanup(fixture.app.stopMainChatWaitingLifecycle)
	startup := waitForMainChatWaiting(t, fixture.app, func(state MainChatWaitingState) bool { return state.Ready })
	if startup.Revision != 1 {
		t.Fatalf("startup state=%+v, want one completed initial compute", startup)
	}
	for {
		select {
		case <-events:
		default:
			return fixture, events
		}
	}
}

func waitForMainChatWaiting(t *testing.T, app *App, match func(MainChatWaitingState) bool) MainChatWaitingState {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		state := app.GetMainChatWaiting()
		if match(state) {
			return state
		}
		time.Sleep(5 * time.Millisecond)
	}
	state := app.GetMainChatWaiting()
	t.Fatalf("waiting state did not match before deadline: %+v", state)
	return state
}

func nextMainChatWaitingEvent(t *testing.T, events <-chan []byte) (MainChatWaitingState, time.Time) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case raw := <-events:
			var event BrowserEvent
			if err := json.Unmarshal(raw, &event); err != nil {
				t.Fatal(err)
			}
			if event.Event != mainChatWaitingChangedEvent || len(event.Args) != 1 {
				continue
			}
			payload, err := json.Marshal(event.Args[0])
			if err != nil {
				t.Fatal(err)
			}
			var state MainChatWaitingState
			if err := json.Unmarshal(payload, &state); err != nil {
				t.Fatal(err)
			}
			return state, time.Now()
		case <-deadline:
			t.Fatal("timed out waiting for main-chat waiting state event")
			return MainChatWaitingState{}, time.Time{}
		}
	}
}

func appendWaitingStateTestCard(t *testing.T, fixture *mainChatReadMemoFixture, id string, expiresAt time.Time) string {
	t.Helper()
	return postWaitingStateTestCard(t, fixture.app, id, expiresAt)
}

func postWaitingStateTestCard(t *testing.T, app *App, id string, expiresAt time.Time) string {
	t.Helper()
	card := mainChatProactive{
		MomentID: "waiting-state-" + id, Kind: string(engagement.KindWaitingOnReply),
		Headline: "A reply is waiting", Body: "Review the message and respond.",
		State: "pending", Target: mainChatTarget{Kind: "mail", ID: "message-" + id},
		ActionIDs: []string{"not_now", "dismiss"}, ActionLabels: map[string]string{"not_now": "Not now", "dismiss": "Dismiss"},
		Evidence: []mainChatEvidenceRef{},
	}
	if !expiresAt.IsZero() {
		card.ExpiresAt = expiresAt.UnixMilli()
	}
	return app.postProactiveItem(card, "waiting-state:"+id)
}

func TestMainChatWaitingStateCreateResolveAndExpire(t *testing.T) {
	fixture, events := newWaitingStateFixture(t)
	started := time.Now()
	itemID := appendWaitingStateTestCard(t, fixture, "created", time.Time{})
	if itemID == "" {
		t.Fatal("waiting card was not appended")
	}
	state, emittedAt := nextMainChatWaitingEvent(t, events)
	if elapsed := emittedAt.Sub(started); elapsed > 200*time.Millisecond {
		t.Fatalf("create-to-event took %s, want under 200ms", elapsed)
	}
	if state.Count != 1 || !state.Ready {
		t.Fatalf("created state=%+v, want ready count 1", state)
	}
	fresh, err := fixture.app.GetMainChatOverview()
	if err != nil || fresh.Waiting != state.Count {
		t.Fatalf("create equivalence: state=%d overview=%d err=%v", state.Count, fresh.Waiting, err)
	}

	started = time.Now()
	result, err := fixture.app.ResolveMainChatProactiveAction("waiting-state-created", "dismiss")
	if err != nil || result != "dismissed" {
		t.Fatalf("resolve waiting card: result=%q err=%v", result, err)
	}
	state, emittedAt = nextMainChatWaitingEvent(t, events)
	if elapsed := emittedAt.Sub(started); elapsed > 200*time.Millisecond {
		t.Fatalf("resolve-to-event took %s, want under 200ms", elapsed)
	}
	if state.Count != 0 {
		t.Fatalf("resolved state=%+v, want count 0", state)
	}

	expires := time.Now().Add(180 * time.Millisecond)
	if appendWaitingStateTestCard(t, fixture, "expires", expires) == "" {
		t.Fatal("expiring card was not appended")
	}
	state, _ = nextMainChatWaitingEvent(t, events)
	if state.Count != 1 {
		t.Fatalf("pre-expiry state=%+v, want count 1", state)
	}
	state, _ = nextMainChatWaitingEvent(t, events)
	if state.Count != 0 {
		t.Fatalf("expired state=%+v, want count 0", state)
	}
}

func TestMainChatWaitingStatePrivacyAndGetter(t *testing.T) {
	fixture, _ := newWaitingStateFixture(t)
	rowID, entry, _, original := fixture.addWorkCard(t, "protected", "desktop", "")
	fixture.app.mainChatRowsChanged(fixture.mainID)
	waitForMainChatWaiting(t, fixture.app, func(state MainChatWaitingState) bool { return state.Count == 1 })

	if err := fixture.app.AddSensitiveApp("com.example.notes", "Notes"); err != nil {
		t.Fatal(err)
	}
	state := waitForMainChatWaitingMatchesFreshReads(t, fixture.app)
	withdrawn, ok := mainChatItemByID(state.Items, rowID)
	if !ok {
		t.Fatalf("protected waiting card %s disappeared: %+v", rowID, state.Items)
	}
	assertWithdrawnMainChatMemoItem(t, withdrawn, original, fixture.app.currentUILanguage(), entry)
	overview, err := fixture.app.GetMainChatOverview()
	if err != nil {
		t.Fatal(err)
	}
	page, err := fixture.app.ListMainChatWaitingItems("", 5)
	if err != nil {
		t.Fatal(err)
	}
	if state.Count != overview.Waiting || !reflect.DeepEqual(state.Items, page.Items) {
		t.Fatalf("privacy state diverged: state=%+v overview=%+v page=%+v", state, overview, page)
	}

	if err := fixture.app.SetClosedEyesModeEnabled(true); err != nil {
		t.Fatal(err)
	}
	state = waitForMainChatWaitingMatchesFreshReads(t, fixture.app)
	overview, err = fixture.app.GetMainChatOverview()
	if err != nil {
		t.Fatal(err)
	}
	page, err = fixture.app.ListMainChatWaitingItems("", 5)
	if err != nil {
		t.Fatal(err)
	}
	if state.Count != overview.Waiting || !reflect.DeepEqual(state.Items, page.Items) {
		t.Fatalf("closed-eyes state diverged: state=%+v overview=%+v page=%+v", state, overview, page)
	}
	fixture.app.store = nil
	if got := fixture.app.GetMainChatWaiting(); !got.Ready || got.Count != state.Count {
		t.Fatalf("memory-only getter got %+v after store close, want %+v", got, state)
	}
}

func waitForMainChatWaitingMatchesFreshReads(t *testing.T, app *App) MainChatWaitingState {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		state := app.GetMainChatWaiting()
		overview, overviewErr := app.GetMainChatOverview()
		page, pageErr := app.ListMainChatWaitingItems("", 5)
		if overviewErr == nil && pageErr == nil && state.Ready &&
			state.Count == overview.Waiting && reflect.DeepEqual(state.Items, page.Items) {
			return state
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("waiting state did not match fresh overview and list reads")
	return MainChatWaitingState{}
}

func TestMainChatWaitingStateStartupReadiness(t *testing.T) {
	fixture, _ := newWaitingStateFixture(t)
	state := fixture.app.GetMainChatWaiting()
	if !state.Ready || state.Revision != 1 {
		t.Fatalf("startup state=%+v, want ready after one compute", state)
	}
}

func TestMainChatWaitingUserSendUpdatesConversationSummary(t *testing.T) {
	app, mainID := topicChatTestApp(t)
	broadcaster := NewEventBroadcaster()
	events := broadcaster.AddClient("waiting-user-send-test")
	app.eventBroadcaster = broadcaster
	app.startMainChatWaitingLifecycle(t.Context())
	t.Cleanup(app.stopMainChatWaitingLifecycle)
	startup := waitForMainChatWaiting(t, app, func(state MainChatWaitingState) bool { return state.Ready })
	if startup.SessionID != mainID {
		t.Fatalf("startup state=%+v, want main session %q", startup, mainID)
	}
	for {
		select {
		case <-events:
		default:
			goto drained
		}
	}
drained:
	runner := &topicCaptureRunner{a: app, tag: mainID, prompt: make(chan string, 1)}
	app.runner = runner
	before, err := app.GetMainChatOverview()
	if err != nil {
		t.Fatal(err)
	}
	sent, err := app.SendMainChat("Update the main chat summary")
	if err != nil {
		t.Fatal(err)
	}
	if sent.SessionID != mainID {
		t.Fatalf("sent session=%q, want %q", sent.SessionID, mainID)
	}
	state, _ := nextMainChatWaitingEvent(t, events)
	if state.Conversations != before.Conversations+1 {
		t.Fatalf("event conversations=%d, want %d; state=%+v", state.Conversations, before.Conversations+1, state)
	}
	select {
	case <-runner.prompt:
	case <-time.After(5 * time.Second):
		t.Fatal("runner not called")
	}
	waitForTagIdle(t, app, mainID)
	fresh, err := app.GetMainChatOverview()
	if err != nil {
		t.Fatal(err)
	}
	if state.Day != fresh.Day || state.Conversations != fresh.Conversations {
		t.Fatalf("event summary day=%q conversations=%d, fresh overview day=%q conversations=%d",
			state.Day, state.Conversations, fresh.Day, fresh.Conversations)
	}
}

func TestMainChatWaitingStateStartupCreationResyncsSession(t *testing.T) {
	app := mainChatTestApp(t)
	app.startMainChatWaitingLifecycle(t.Context())
	t.Cleanup(app.stopMainChatWaitingLifecycle)
	startup := waitForMainChatWaiting(t, app, func(state MainChatWaitingState) bool { return state.Ready })
	if startup.SessionID != "" {
		t.Fatalf("startup session=%q, want no main chat", startup.SessionID)
	}

	main, err := app.ensureMainChat(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	state, refreshed := waitForMainChatWaitingState(app, 200*time.Millisecond, func(state MainChatWaitingState) bool {
		return state.SessionID == main.LocalID
	})
	if !refreshed {
		t.Errorf("main-chat creation did not refresh waiting session: got %+v, want session %q", state, main.LocalID)
	}

	if id := postWaitingStateTestCard(t, app, "startup-created", time.Time{}); id == "" {
		t.Fatal("waiting card producer did not append a card")
	}
	state = waitForMainChatWaiting(t, app, func(state MainChatWaitingState) bool { return state.Count == 1 })
	overview, err := app.GetMainChatOverview()
	if err != nil || state.Count != 1 || state.SessionID != main.LocalID || overview.Waiting != state.Count {
		t.Fatalf("startup producer state=%+v overview=%+v err=%v", state, overview, err)
	}
}

func TestMainChatWaitingStateRecreatedMainChatAcceptsWrites(t *testing.T) {
	app := mainChatTestApp(t)
	original, err := app.ensureMainChat(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	app.startMainChatWaitingLifecycle(t.Context())
	t.Cleanup(app.stopMainChatWaitingLifecycle)
	startup := waitForMainChatWaiting(t, app, func(state MainChatWaitingState) bool { return state.Ready })
	if startup.SessionID != original.LocalID {
		t.Fatalf("startup session=%q, want original main chat %q", startup.SessionID, original.LocalID)
	}

	recreatedStore, err := storage.New(filepath.Join(t.TempDir(), "recreated.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = recreatedStore.Close() })
	app.store = recreatedStore
	recreated, err := app.ensureMainChat(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if recreated.LocalID == original.LocalID {
		t.Fatalf("recreated main chat reused session ID %q", original.LocalID)
	}
	if id := postWaitingStateTestCard(t, app, "recreated", time.Time{}); id == "" {
		t.Fatal("waiting card producer did not append a card")
	}
	state := waitForMainChatWaiting(t, app, func(state MainChatWaitingState) bool { return state.Count == 1 })
	overview, err := app.GetMainChatOverview()
	if err != nil || state.Count != 1 || state.SessionID != recreated.LocalID || overview.Waiting != state.Count {
		t.Fatalf("recreated producer state=%+v overview=%+v err=%v", state, overview, err)
	}
}

func waitForMainChatWaitingState(app *App, timeout time.Duration, match func(MainChatWaitingState) bool) (MainChatWaitingState, bool) {
	deadline := time.Now().Add(timeout)
	for {
		state := app.GetMainChatWaiting()
		if match(state) {
			return state, true
		}
		if time.Now().After(deadline) {
			return state, false
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestMainChatWaitingStateCoalescesDirtyMarks(t *testing.T) {
	fixture := newMainChatReadMemoFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var computes atomic.Int32
	fixture.app.mainChatWaiting.beforeCompute = func() {
		if computes.Add(1) == 1 {
			close(entered)
			<-release
		}
	}
	fixture.app.startMainChatWaitingLifecycle(t.Context())
	t.Cleanup(fixture.app.stopMainChatWaitingLifecycle)
	<-entered
	for i := 0; i < 50; i++ {
		fixture.app.markMainChatWaitingDirty("coalescing-test")
	}
	close(release)
	waitForMainChatWaiting(t, fixture.app, func(state MainChatWaitingState) bool {
		fixture.app.mainChatWaiting.mu.Lock()
		running := fixture.app.mainChatWaiting.running
		fixture.app.mainChatWaiting.mu.Unlock()
		return state.Ready && !running
	})
	if got := computes.Load(); got > 2 {
		t.Fatalf("50 dirty marks caused %d computes, want at most 2", got)
	}
}
