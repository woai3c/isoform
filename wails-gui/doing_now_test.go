package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/handoff"
	"wails-gui/storage"
)

func TestSelectDoingNow_PicksTheOneThingToSay(t *testing.T) {
	everything := doingNowInputs{
		paused: true, inMeeting: true, meetingApp: "Zoom", readingCalendar: true,
		working: true, workTitle: "Draft the Q2 deck", watching: true, listening: true,
	}
	cases := []struct {
		name string
		in   func(doingNowInputs) doingNowInputs
		want DoingNow
	}{
		{"a pause outranks everything", func(in doingNowInputs) doingNowInputs { return in }, DoingNow{Kind: doingNowPaused}},
		{"then a call being recorded", func(in doingNowInputs) doingNowInputs { in.paused = false; return in }, DoingNow{Kind: doingNowMeeting, App: "Zoom"}},
		{"then a calendar being read", func(in doingNowInputs) doingNowInputs {
			in.paused, in.inMeeting = false, false
			return in
		}, DoingNow{Kind: doingNowCalendar}},
		{"then running work", func(in doingNowInputs) doingNowInputs {
			in.paused, in.inMeeting, in.readingCalendar = false, false, false
			return in
		}, DoingNow{Kind: doingNowWorking, Title: "Draft the Q2 deck"}},
		{"then watching", func(in doingNowInputs) doingNowInputs {
			in.paused, in.inMeeting, in.readingCalendar, in.working = false, false, false, false
			return in
		}, DoingNow{Kind: doingNowWatching}},
		{"then listening only", func(in doingNowInputs) doingNowInputs {
			return doingNowInputs{listening: true}
		}, DoingNow{Kind: doingNowListening}},
		{"else idle", func(doingNowInputs) doingNowInputs { return doingNowInputs{} }, DoingNow{Kind: doingNowIdle}},
	}
	for _, tc := range cases {
		if got := selectDoingNow(tc.in(everything)); got != tc.want {
			t.Errorf("%s: got %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

func TestSelectDoingNow_ShortensLongTitles(t *testing.T) {
	long := strings.Repeat("word ", 20)
	got := selectDoingNow(doingNowInputs{working: true, workTitle: "  Draft\nthe   deck "})
	if got.Title != "Draft the deck" {
		t.Fatalf("title = %q, want spaces collapsed", got.Title)
	}
	got = selectDoingNow(doingNowInputs{working: true, workTitle: long})
	if n := len([]rune(got.Title)); n > doingNowTitleMaxRunes || !strings.HasSuffix(got.Title, "…") {
		t.Fatalf("long title = %q (%d runes), want at most %d ending in an ellipsis", got.Title, n, doingNowTitleMaxRunes)
	}
	cjk := strings.Repeat("整理第二季度的汇报材料", 6)
	got = selectDoingNow(doingNowInputs{working: true, workTitle: cjk})
	if n := len([]rune(got.Title)); n > doingNowTitleMaxRunes {
		t.Fatalf("CJK title has %d runes", n)
	}
}

func TestDoingNowText(t *testing.T) {
	cases := []struct {
		view DoingNow
		lang string
		want string
	}{
		{DoingNow{Kind: doingNowPaused}, "en", "Paused"},
		{DoingNow{Kind: doingNowMeeting, App: "Zoom"}, "en", "Listening to your Zoom call"},
		{DoingNow{Kind: doingNowMeeting}, "en", "Listening to your call"},
		{DoingNow{Kind: doingNowCalendar}, "en", "Reading your calendar"},
		// The menu bar never names the task, even when it is known.
		{DoingNow{Kind: doingNowWorking, Title: "Draft the Q2 deck"}, "en", "Working on a task"},
		{DoingNow{Kind: doingNowWorking}, "en", "Working on a task"},
		{DoingNow{Kind: doingNowWorking, Title: "起草季度报告"}, "zh-CN", "正在处理一项任务"},
		{DoingNow{Kind: doingNowWatching}, "en", "Observing in background"},
		{DoingNow{Kind: doingNowIdle}, "en", "Not observing"},
		{DoingNow{Kind: doingNowMeeting, App: "Zoom"}, "zh-CN", "正在听你的 Zoom 通话"},
		{DoingNow{Kind: doingNowWatching}, "zh-Hans", "后台观察中"},
		{DoingNow{Kind: doingNowWatching}, "fr", "Observing in background"},
		{DoingNow{Kind: "unknown"}, "en", "Not observing"},
	}
	for _, tc := range cases {
		if got := doingNowText(tc.view, tc.lang); got != tc.want {
			t.Errorf("doingNowText(%+v, %q) = %q, want %q", tc.view, tc.lang, got, tc.want)
		}
	}
}

func TestDoingNowCopyCoversEveryKindInEveryLanguage(t *testing.T) {
	en := doingNowCopy["en"]
	for lang, table := range doingNowCopy {
		if len(table) != len(en) {
			t.Errorf("%s has %d phrases, en has %d", lang, len(table), len(en))
		}
		for key := range en {
			if strings.TrimSpace(table[key]) == "" {
				t.Errorf("%s lacks %q", lang, key)
			}
		}
		for _, kind := range doingNowKinds {
			if _, ok := table[doingNowCopyKey(DoingNow{Kind: kind, App: "App", Title: "Title"})]; !ok {
				t.Errorf("%s lacks the %s kind", lang, kind)
			}
		}
		for key, phrase := range table {
			if strings.Contains(phrase, "{title}") {
				t.Errorf("%s menu-bar phrase %q names the task: %q", lang, key, phrase)
			}
		}
	}
}

// TestDoingNowCopyMatchesTheFrontend keeps the sidebar tooltip and the menu
// bar saying the same thing: every Go phrase exists in the frontend locale
// with the same words, {app} written as {{app}}. The frontend has one phrase
// more, "working", which names the task inside the window only.
func TestDoingNowCopyMatchesTheFrontend(t *testing.T) {
	for lang, table := range doingNowCopy {
		data, err := os.ReadFile(filepath.Join("frontend", "src", "i18n", "locales", lang, "app.json"))
		if err != nil {
			t.Fatalf("read %s app.json: %v", lang, err)
		}
		var locale struct {
			ObservingStatus struct {
				DoingNow map[string]string `json:"doingNow"`
			} `json:"observingStatus"`
		}
		if err := json.Unmarshal(data, &locale); err != nil {
			t.Fatalf("parse %s app.json: %v", lang, err)
		}
		frontend := locale.ObservingStatus.DoingNow
		for key, phrase := range table {
			want := strings.ReplaceAll(phrase, "{app}", "{{app}}")
			if frontend[key] != want {
				t.Errorf("%s observingStatus.doingNow.%s = %q, want %q", lang, key, frontend[key], want)
			}
		}
		if !strings.Contains(frontend["working"], "{{title}}") {
			t.Errorf("%s observingStatus.doingNow.working = %q, want it to name the task", lang, frontend["working"])
		}
		if len(frontend) != len(table)+1 {
			t.Errorf("%s frontend has %d doing-now phrases, want Go's %d plus working", lang, len(frontend), len(table))
		}
	}
}

func TestNoteDoingNowEvent_NudgesOnceWithoutBlocking(t *testing.T) {
	app := &App{}
	// Before the loop starts there is no one to nudge; it must not block.
	app.noteDoingNowEvent(closedEyesModeChangedEvent)

	nudges := make(chan struct{}, 1)
	app.doingNow.nudges = nudges
	app.noteDoingNowEvent("agent:event")
	if len(nudges) != 0 {
		t.Fatal("an unrelated event nudged the loop")
	}
	app.noteDoingNowEvent(closedEyesModeChangedEvent)
	app.noteDoingNowEvent("calendar:synced")
	if len(nudges) != 1 {
		t.Fatalf("nudges = %d, want the two events coalesced into one", len(nudges))
	}

	// The emit funnel nudges too, so every existing emitter is covered.
	<-nudges
	app.emitBrowserEvent("background-listening:changed", true)
	if len(nudges) != 1 {
		t.Fatal("emitBrowserEvent did not nudge the doing-now loop")
	}
}

// doingNowEventKinds records the doing-now:changed events the app sends to
// browser clients. next returns the kind of the next one, waiting up to
// wait; "" when none came.
func doingNowEventKinds(t *testing.T, app *App) (next func(wait time.Duration) string) {
	t.Helper()
	broadcaster := NewEventBroadcaster()
	events := broadcaster.AddClient("doing-now-test")
	t.Cleanup(func() { broadcaster.RemoveClient("doing-now-test") })
	app.eventBroadcaster = broadcaster
	return func(wait time.Duration) string {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		for {
			// An event already sent wins over an expired wait.
			var payload []byte
			select {
			case payload = <-events:
			default:
				select {
				case payload = <-events:
				case <-timer.C:
					return ""
				}
			}
			var event BrowserEvent
			if err := json.Unmarshal(payload, &event); err != nil {
				t.Fatalf("decode event: %v", err)
			}
			if event.Event != doingNowChangedEvent || len(event.Args) != 1 {
				continue
			}
			view, _ := event.Args[0].(map[string]any)
			kind, _ := view["kind"].(string)
			return kind
		}
	}
}

func TestRefreshDoingNow_SendsOnlyChanges(t *testing.T) {
	app := &App{activeTags: map[string]bool{}}
	next := doingNowEventKinds(t, app)

	app.refreshDoingNow()
	if kind := next(0); kind != string(doingNowIdle) {
		t.Fatalf("first refresh sent %q, want idle", kind)
	}
	app.refreshDoingNow()
	if kind := next(0); kind != "" {
		t.Fatalf("an unchanged line was sent again: %q", kind)
	}
	app.closedEyes.enabled.Store(true)
	app.refreshDoingNow()
	if kind := next(0); kind != string(doingNowPaused) {
		t.Fatalf("pause sent %q", kind)
	}
	if app.doingNow.lastText != "Observing paused" {
		t.Fatalf("menu text = %q", app.doingNow.lastText)
	}
}

func TestRefreshDoingNow_ReadsTheLanguageOnceUntilItChanges(t *testing.T) {
	st, err := storage.New(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	app := &App{store: st, activeTags: map[string]bool{}}
	app.closedEyes.enabled.Store(true)
	ctx := context.Background()
	if err := st.SetUIState(ctx, uiLanguageStateKey, "zh-CN"); err != nil {
		t.Fatalf("SetUIState: %v", err)
	}

	app.refreshDoingNow()
	if app.doingNow.lastText != "观察已暂停" {
		t.Fatalf("menu text = %q, want the saved language", app.doingNow.lastText)
	}
	// A write that bypasses SaveUIState is not seen: the language is cached.
	if err := st.SetUIState(ctx, uiLanguageStateKey, "en"); err != nil {
		t.Fatalf("SetUIState: %v", err)
	}
	app.refreshDoingNow()
	if app.doingNow.lastText != "观察已暂停" {
		t.Fatalf("menu text = %q, want the cached language", app.doingNow.lastText)
	}
	// Saving the language through the binding drops the cache.
	if err := app.SaveUIState(uiLanguageStateKey, "en"); err != nil {
		t.Fatalf("SaveUIState: %v", err)
	}
	app.refreshDoingNow()
	if app.doingNow.lastText != "Observing paused" {
		t.Fatalf("menu text = %q, want the new language", app.doingNow.lastText)
	}
}

func TestRunningWork_NamesTheTask(t *testing.T) {
	st, err := storage.New(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	hs, err := handoff.NewStore(filepath.Join(t.TempDir(), "handoff"))
	if err != nil {
		t.Fatalf("handoff.NewStore: %v", err)
	}
	ctx := context.Background()
	if err := st.SaveProject(ctx, storage.ProjectRow{ID: "p", Name: "P", Path: "/tmp/p"}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	save := func(row storage.SessionRow) {
		t.Helper()
		if row.ProjectID == "" {
			row.ProjectID = "p"
		}
		if err := st.SaveSession(ctx, row); err != nil {
			t.Fatalf("SaveSession: %v", err)
		}
	}
	save(storage.SessionRow{LocalID: "main", Label: "Isoform", Kind: storage.SessionKindMain})
	save(storage.SessionRow{LocalID: "side", Label: "Plan the offsite"})
	save(storage.SessionRow{LocalID: "unnamed", Label: ""})
	save(storage.SessionRow{LocalID: "handoff-chat", Label: "Handoff: Export the expense report for Q3 and file it wi..."})
	if _, err := hs.Save(handoff.Entry{ID: "h1", Title: "Export the expense report for Q3 and file it with finance", Status: handoff.StatusInProgress, ChatSessionID: "handoff-chat"}); err != nil {
		t.Fatalf("save handoff: %v", err)
	}

	app := &App{store: st, handoffStore: hs, activeTags: map[string]bool{}}
	run := func(tags ...string) (bool, string) {
		app.mu.Lock()
		app.activeTags = map[string]bool{}
		for _, tag := range tags {
			app.activeTags[tag] = true
		}
		app.mu.Unlock()
		return app.runningWork()
	}

	if working, _ := run(); working {
		t.Fatal("nothing runs, but work was reported")
	}
	if working, title := run("main"); !working || title != "" {
		t.Fatalf("main chat turn = %v %q, want working with no title", working, title)
	}
	if _, title := run("main", "side"); title != "Plan the offsite" {
		t.Fatalf("title = %q, want the side chat's name", title)
	}
	if _, title := run("handoff-chat"); title != "Export the expense report for Q3 and file it with finance" {
		t.Fatalf("title = %q, want the one-time work's whole title", title)
	}
	if _, title := run("phone-row-1"); title != "" {
		t.Fatalf("title = %q for a turn that is not a saved chat", title)
	}

	// A chat named after its turn started is picked up on the next poll.
	if _, title := run("unnamed"); title != "" {
		t.Fatalf("title = %q before the chat has a name", title)
	}
	save(storage.SessionRow{LocalID: "unnamed", Label: "Book the flights"})
	if _, title := run("unnamed"); title != "Book the flights" {
		t.Fatalf("title = %q, want the new name", title)
	}
	// Settled titles are cached: a rename shows only once the turn ends.
	if _, title := run("side"); title != "Plan the offsite" {
		t.Fatalf("title = %q, want the side chat's name", title)
	}
	save(storage.SessionRow{LocalID: "side", Label: "Renamed"})
	if _, title := run("side"); title != "Plan the offsite" {
		t.Fatalf("title = %q, want the cached name while the turn runs", title)
	}
	run()
	if _, title := run("side"); title != "Renamed" {
		t.Fatalf("title = %q, want the new name for a new turn", title)
	}
}

func TestDoingNowLoop_RefreshesOnANudgeAndStops(t *testing.T) {
	app := &App{activeTags: map[string]bool{}}
	next := doingNowEventKinds(t, app)
	app.startDoingNow(context.Background())
	t.Cleanup(app.stopDoingNow)

	if kind := next(2 * time.Second); kind != string(doingNowIdle) {
		t.Fatalf("first line = %q, want idle", kind)
	}
	app.closedEyes.enabled.Store(true)
	app.noteDoingNowEvent(closedEyesModeChangedEvent)
	if kind := next(doingNowPollInterval / 2); kind != string(doingNowPaused) {
		t.Fatalf("after the nudge = %q, want paused before the next poll", kind)
	}

	// A stopped loop sends nothing more, even when nudged.
	nudges := app.doingNow.nudges
	app.stopDoingNow()
	app.closedEyes.enabled.Store(false)
	app.noteDoingNowEvent(closedEyesModeChangedEvent)
	select {
	case nudges <- struct{}{}:
	default:
	}
	if kind := next(300 * time.Millisecond); kind != "" {
		t.Fatalf("a stopped loop sent %q", kind)
	}

	// It can start again and says what changed meanwhile.
	app.startDoingNow(context.Background())
	if kind := next(2 * time.Second); kind != string(doingNowIdle) {
		t.Fatalf("after a restart = %q, want idle", kind)
	}
}

func TestStopDoingNow_WaitsForTheLoop(t *testing.T) {
	app := &App{activeTags: map[string]bool{}}
	for i := 0; i < 20; i++ {
		app.startDoingNow(context.Background())
		done := app.doingNow.done
		app.stopDoingNow()
		select {
		case <-done:
		default:
			t.Fatalf("round %d: stopDoingNow returned while the loop was still running", i)
		}
	}
}
