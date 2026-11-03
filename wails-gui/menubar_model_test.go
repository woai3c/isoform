package main

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"wails-gui/storage"
)

func TestMenuBarIconLifecycle(t *testing.T) {
	var attention menuBarAttention
	steps := []struct {
		paused, open bool
		ids          []string
		want         menuBarIconState
	}{
		{false, false, nil, menuBarIconState{1, false, false}},
		{true, false, nil, menuBarIconState{.45, false, false}},
		{false, false, []string{"a"}, menuBarIconState{1, true, false}},
		{false, true, []string{"a"}, menuBarIconState{1, false, true}},
		{false, false, []string{"a"}, menuBarIconState{1, false, false}},
		{true, false, []string{"b"}, menuBarIconState{.45, true, false}},
		{true, true, []string{"b"}, menuBarIconState{.45, false, true}},
		{false, false, nil, menuBarIconState{1, false, false}},
		{false, false, []string{"b"}, menuBarIconState{1, true, false}},
	}
	for i, step := range steps {
		if got := attention.icon(step.paused, step.open, step.ids); got != step.want {
			t.Errorf("step %d: %+v want %+v", i, got, step.want)
		}
	}
}

// Right-click and Control-click: the short native menu of the 1.0 spec, in
// en and zh-CN; Pause for 1 hour, the state line and Library left it.
func TestMenuBarMenuModel(t *testing.T) {
	want := map[string][]string{
		"en":    {"Open Isoform", "Make my brief now", "Pause for 15 minutes", "Settings…", "Quit Isoform"},
		"zh-CN": {"打开 Isoform", "立即生成简报", "暂停 15 分钟", "设置…", "退出 Isoform"},
	}
	for _, lang := range []string{"en", "zh-CN"} {
		for _, paused := range []bool{false, true} {
			ids, titles := []string{}, []string{}
			byID := map[string]menuBarMenuItem{}
			for _, row := range menuBarMenu(lang, paused) {
				if !row.Separator {
					ids = append(ids, row.ID)
					titles = append(titles, row.Title)
					byID[row.ID] = row
				}
			}
			pause, pauseTitle := "pause15", want[lang][2]
			if paused {
				pause, pauseTitle = "resume", map[string]string{"en": "Resume observing", "zh-CN": "恢复观察"}[lang]
			}
			if wantIDs := []string{"open", "make", pause, "settings", "quit"}; !reflect.DeepEqual(ids, wantIDs) {
				t.Fatalf("%s paused=%v ids %v", lang, paused, ids)
			}
			wantTitles := append([]string{}, want[lang]...)
			wantTitles[2] = pauseTitle
			if !reflect.DeepEqual(titles, wantTitles) {
				t.Fatalf("%s paused=%v titles %v", lang, paused, titles)
			}
			for id, key := range map[string]string{"open": "i", "settings": ",", "quit": "q"} {
				if byID[id].Key != key {
					t.Errorf("%s key %+v", id, byID[id])
				}
			}
			if !reflect.DeepEqual(byID["open"].Modifiers, []string{"command", "shift"}) {
				t.Fatal("open shortcut changed")
			}
		}
	}
	if len(menuBarCopy["en"]) != len(menuBarCopy["zh-CN"]) {
		t.Fatal("copy catalogs differ")
	}
	for k, v := range menuBarCopy["en"] {
		if v == "" || menuBarCopy["zh-CN"][k] == "" {
			t.Errorf("missing copy %q", k)
		}
	}
	// Retired in 1.0.
	for _, retired := range []string{"Pause 1 hour", "Your spoken brief is not ready yet", "Loading...", "Show menu", "Could not read the menu bar. Open Isoform to try again.", "Settings..."} {
		for _, v := range menuBarCopy["en"] {
			if v == retired {
				t.Errorf("retired string %q is back", retired)
			}
		}
	}
}

// The brief row's states, from the newest brief and the schedule.
func TestMenuBarBriefRow(t *testing.T) {
	now := time.Date(2026, 10, 27, 8, 40, 0, 0, time.Local)
	w := menuBarWords("en")
	// Play needs the words as well as the recording.
	morning := MobileBriefV1{BriefID: "2026-10-27-morning", Slot: mobileBriefSlotMorning, LocalDate: "2026-10-27", AsOf: time.Date(2026, 10, 27, 7, 0, 0, 0, time.Local).Format(time.RFC3339),
		Narrative: []MobileBriefPara{{Text: "Good morning. Three meetings today."}},
		Audio:     &MobileBriefAudio{Bytes: 10, Status: mobileBriefAudioStatusOK, DurationSeconds: 48}}
	if got := menuBarBriefRow(w, "en", morning, true, false, "", now); got.Kind != "ready" || got.Title != "Hear your morning brief" || got.Meta != "7:00 AM · 0:48" || got.ID != morning.BriefID {
		t.Fatalf("ready %+v", got)
	}
	evening := MobileBriefV1{BriefID: "2026-10-27-evening", Slot: mobileBriefSlotEvening, LocalDate: "2026-10-27", AsOf: time.Date(2026, 10, 27, 19, 30, 0, 0, time.Local).Format(time.RFC3339)}
	if got := menuBarBriefRow(w, "en", evening, true, false, "", now); got.Kind != "read" || got.Title != "Read your evening brief" || got.Meta != "7:30 PM · no recording" {
		t.Fatalf("read %+v", got)
	}
	next := time.Date(2026, 10, 28, 7, 0, 0, 0, time.Local).UTC().Format(time.RFC3339)
	// The current brief stays on the menu bar on an earlier day (the same one
	// Today and Chat show).
	yesterday := morning
	yesterday.LocalDate = "2026-10-26"
	if got := menuBarBriefRow(w, "en", yesterday, true, false, next, now); got.Kind != "ready" || got.ID != morning.BriefID {
		t.Fatalf("an earlier day's current brief %+v", got)
	}
	if got := menuBarBriefRow(w, "en", MobileBriefV1{}, false, false, next, now); got.Kind != "none" || got.Title != "No brief yet today" || got.Meta != "Next brief tomorrow at 7:00 AM" {
		t.Fatalf("none %+v", got)
	}
	if got := menuBarBriefRow(w, "en", morning, true, true, "", now); got.Kind != "making" || got.Title != "Making your brief…" || got.Meta != "About a minute" {
		t.Fatalf("making %+v", got)
	}
	if got := menuBarBriefRow(menuBarWords("zh-CN"), "zh-CN", evening, true, false, "", now); got.Title != "阅读晚间简报" || got.Meta != "晚上7:30 · 没有录音" {
		t.Fatalf("zh-CN read %+v", got)
	}
	if got := menuBarLabel(w, "Observing", 3); got != "Isoform, observing, 3 waiting on you" && got != "Isoform, Observing, 3 waiting on you" {
		t.Fatalf("label %q", got)
	}
}

func TestMenuBarWaitingReadUsesChatProjection(t *testing.T) {
	app := mainChatTestApp(t)
	main, err := app.ensureMainChat(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{"a", "b", "c", "d", "closed", "routine", "expired"} {
		card := mainChatProactive{MomentID: id, Kind: "menu_fixture", State: "pending", Headline: "Someone is waiting on your reply.", Body: "Halcyon\nDraft ready", Localized: map[string]mainChatProactiveCopy{"en": {Headline: "Someone is waiting on your reply.", Body: "Halcyon\nDraft ready"}, "zh-CN": {Headline: "有人在等你回复。", Body: "Halcyon 草稿已就绪"}}}
		switch id {
		case "closed":
			card.State = "dismissed"
		case "routine":
			card.Kind = "brief_ready"
		case "expired":
			card.ExpiresAt = time.Now().Add(-time.Hour).UnixMilli()
		}
		row := storage.StreamItemRow{ID: id, Kind: "system", ToolName: mainChatProactiveTool, ToolInput: mainChatViewPayload(t, card), CreatedAt: time.Now().Add(time.Duration(i) * time.Second).UnixMilli()}
		if _, err := app.store.AppendStreamItem(t.Context(), main.LocalID, row); err != nil {
			t.Fatal(err)
		}
	}
	page, err := app.ListMainChatWaitingItems("", 50)
	if err != nil {
		t.Fatal(err)
	}
	items := menuBarWaitingItems(page, "zh-CN")
	if len(items) != 3 || items[0].ID != "d" || items[2].ID != "b" || items[0].SessionID != main.LocalID {
		t.Fatalf("waiting %+v", items)
	}
	if items[0].Title != "有人在等你回复。" {
		t.Fatalf("localization %+v", items[0])
	}
	en := menuBarWaitingItems(page, "en")
	if strings.ContainsAny(en[0].Detail, "\n\r") {
		t.Fatal("detail is not one line")
	}
	overview, err := app.GetMainChatOverview()
	if err != nil || overview.Waiting != 4 {
		t.Fatalf("overview %+v %v", overview, err)
	}
	rows, err := app.store.LoadStreamItems(t.Context(), main.LocalID)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.ID == "a" {
			var card mainChatProactive
			_ = json.Unmarshal([]byte(row.ToolInput), &card)
			if card.State != "pending" {
				t.Fatal("reading resolved work")
			}
		}
	}
}
func TestMenuBarRenderEscapesSourcesAndSharesTokens(t *testing.T) {
	view := menuBarView{Words: menuBarWords("en"), Items: []menuBarWaitingItem{{Title: `</script><script>bad()</script>`}}}
	html := renderMenuBarHTML(view)
	if strings.Contains(html, `<script>bad()`) || strings.Contains(html, "/*__MODEL__*/") || strings.Contains(html, "/*__TOKENS__*/") {
		t.Fatal("unsafe or incomplete render")
	}
	css := menuBarTokenCSS(menuBarAppCSS)
	for _, s := range []string{":root{", `:root[data-theme="onedark"]`, "--color-panel:", "--color-ink:", "--color-copper:"} {
		if !strings.Contains(css, s) {
			t.Errorf("missing shared token %s", s)
		}
	}
	for _, s := range []string{"@import", "@theme", "data:image/svg", "background-image"} {
		if strings.Contains(css, s) {
			t.Errorf("unrelated CSS included %s", s)
		}
	}
	for _, s := range []string{"innerHTML", "fetch(", "http://", "https://", "#[0-9]"} {
		if strings.Contains(menuBarHTML, s) {
			t.Errorf("unexpected HTML %s", s)
		}
	}
	for seconds, want := range map[float64]string{48: "0:48", 95: "1:35", 0: "", -1: "", math.Inf(1): ""} {
		if got := menuBarDuration(seconds); got != want {
			t.Errorf("%f => %s", seconds, got)
		}
	}
}

func TestMenuBarWaitingWithdrawsUnverifiableSourceCopy(t *testing.T) {
	app := mainChatTestApp(t)
	main, err := app.ensureMainChat(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	card := mainChatProactive{MomentID: "missing-source", Kind: "waiting_on_reply", State: "pending", Headline: "PRIVATE_HEADLINE", Body: "PRIVATE_DETAIL", VoiceVersion: "v1", VoiceReceipt: "invalid", Localized: map[string]mainChatProactiveCopy{"en": {Headline: "PRIVATE_HEADLINE", Body: "PRIVATE_DETAIL"}, "zh-CN": {Headline: "PRIVATE_HEADLINE", Body: "PRIVATE_DETAIL"}}}
	if _, err := app.store.AppendStreamItem(t.Context(), main.LocalID, storage.StreamItemRow{ID: "private", Kind: "system", ToolName: mainChatProactiveTool, ToolInput: mainChatViewPayload(t, card)}); err != nil {
		t.Fatal(err)
	}
	page, err := app.ListMainChatWaitingItems("", 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, lang := range []string{"en", "zh-CN"} {
		data, _ := json.Marshal(menuBarWaitingItems(page, lang))
		if strings.Contains(string(data), "PRIVATE_") {
			t.Fatalf("%s leaked source text: %s", lang, data)
		}
	}
}

// Bo's popover said "Could not load what is waiting on you" whenever a
// refresh ran past ListMainChatWaitingItems' deadline under load. A slow or
// failed refresh now keeps the last good list; the error is only for a
// launch where no read has worked yet.
func TestMenuBarTimeoutKeepsTheLastList(t *testing.T) {
	timeout := context.DeadlineExceeded
	read := func(lang string, items ...menuBarWaitingItem) menuBarView {
		view := menuBarView{Language: lang, Words: menuBarWords(lang), State: "Observing", Waiting: len(items), Items: items}
		for _, item := range items {
			view.WaitingIDs = append(view.WaitingIDs, item.ID)
		}
		return view
	}
	failed := func(lang string) menuBarView {
		// A failed read carries whatever it had read so far.
		return menuBarView{Language: lang, Words: menuBarWords(lang), State: "Observing", Waiting: 9, Items: []menuBarWaitingItem{{ID: "partial"}}}
	}
	var memory menuBarWaitingMemory

	first := failed("en")
	memory.settle(&first, true, timeout, time.Now())
	if first.Error != menuBarWords("en")["failed"] || len(first.Items) != 0 || first.Waiting != 0 || first.WaitingPending {
		t.Fatalf("no read has worked yet: say so plainly %+v", first)
	}

	good := read("en", menuBarWaitingItem{ID: "a", Title: "Reply to Halcyon"}, menuBarWaitingItem{ID: "b", Title: "Review the draft"})
	memory.settle(&good, true, nil, time.Now())

	slow := failed("en")
	memory.settle(&slow, true, timeout, time.Now())
	if slow.Error != "" || slow.WaitingPending || slow.Waiting != 2 || len(slow.Items) != 2 || slow.Items[0].Title != "Reply to Halcyon" {
		t.Fatalf("a timeout must keep the last list silently %+v", slow)
	}
	if !reflect.DeepEqual(slow.WaitingIDs, []string{"a", "b"}) || slow.Label != menuBarLabel(slow.Words, slow.State, 2) {
		t.Fatalf("ids or label not from the last list %+v", slow)
	}
	data, _ := json.Marshal(slow)
	if strings.Contains(string(data), `"error"`) {
		t.Fatalf("error reached the page: %s", data)
	}

	// The kept list is a copy: a later view cannot change it.
	slow.Items[0].Title = "changed"
	again := failed("en")
	memory.settle(&again, true, timeout, time.Now())
	if again.Items[0].Title != "Reply to Halcyon" {
		t.Fatal("the held list was shared with a view")
	}

	// Another language: the held rows are in English, so placeholders.
	zh := failed("zh-CN")
	memory.settle(&zh, true, timeout, time.Now())
	if zh.Error != "" || !zh.WaitingPending || len(zh.Items) != 0 || zh.Waiting != 0 {
		t.Fatalf("a held list in another language %+v", zh)
	}

	// A privacy change withdraws the list: placeholders, never old rows and
	// never the error, since a read has worked.
	memory.withdraw()
	withdrawn := failed("en")
	memory.settle(&withdrawn, true, timeout, time.Now())
	if withdrawn.Error != "" || !withdrawn.WaitingPending || len(withdrawn.Items) != 0 || withdrawn.Waiting != 0 || len(withdrawn.WaitingIDs) != 0 {
		t.Fatalf("after a withdraw %+v", withdrawn)
	}

	// The next good read holds again, an empty list included.
	empty := read("en")
	memory.settle(&empty, true, nil, time.Now())
	after := failed("en")
	memory.settle(&after, true, timeout, time.Now())
	if after.WaitingPending || after.Error != "" || after.Waiting != 0 || len(after.Items) != 0 {
		t.Fatalf("an empty good list is a list %+v", after)
	}
	if !strings.Contains(renderMenuBarHTML(after), "!!model.waitingPending") {
		t.Fatal("the page ignores waitingPending")
	}
}

// Bo's log on 2026-10-27: with Chat idle, focused or not, the five-second
// status tick read GetMainChatOverview and ListMainChatWaitingItems about 40
// times in five minutes each. Idle ticks now reuse the held list; the list
// is read after a change, the popover opening, another language, a failed
// read once 30 s have passed, and otherwise every 15 minutes.
func TestMenuBarIdleTicksDoNotReadWaiting(t *testing.T) {
	start := time.Date(2026, 10, 27, 17, 42, 0, 0, time.Local)
	var memory menuBarWaitingMemory
	reads := 0
	tick := func(at time.Time, lang string, err error) menuBarView {
		view := menuBarView{Language: lang, Words: menuBarWords(lang), State: "Observing", Items: []menuBarWaitingItem{}}
		read := memory.begin(at, lang)
		if read {
			reads++
			if err == nil {
				view.Waiting, view.WaitingIDs, view.Items = 1, []string{"a"}, []menuBarWaitingItem{{ID: "a", Title: "Reply to Halcyon"}}
			}
		}
		memory.settle(&view, read, err, at)
		return view
	}
	at := start
	step := func(d time.Duration) time.Time { at = at.Add(d); return at }

	tick(at, "en", nil)
	if reads != 1 {
		t.Fatalf("the first tick reads the list, got %d reads", reads)
	}
	// Five idle minutes of five-second ticks: no read, the held list shows.
	for i := 0; i < 60; i++ {
		view := tick(step(5*time.Second), "en", nil)
		if len(view.Items) != 1 || view.Error != "" || view.WaitingPending {
			t.Fatalf("tick %d lost the held list %+v", i, view)
		}
	}
	if reads != 1 {
		t.Fatalf("idle ticks read the waiting list %d times in five minutes", reads-1)
	}

	// A main-chat write, or the popover opening: one read on the next tick.
	memory.changed()
	tick(step(5*time.Second), "en", nil)
	tick(step(5*time.Second), "en", nil)
	if reads != 2 {
		t.Fatalf("a change should read once, got %d reads", reads)
	}

	// Another language reads again at once.
	tick(step(5*time.Second), "zh-CN", nil)
	if reads != 3 {
		t.Fatalf("another language should read, got %d reads", reads)
	}

	// A failed read keeps the list and waits 30 s before the next try.
	memory.changed()
	failedAt := step(5 * time.Second)
	view := tick(failedAt, "zh-CN", context.DeadlineExceeded)
	if reads != 4 || len(view.Items) != 1 || view.Error != "" {
		t.Fatalf("failed read %d %+v", reads, view)
	}
	for at.Before(failedAt.Add(menuBarWaitingRetry - 5*time.Second)) {
		tick(step(5*time.Second), "zh-CN", nil)
	}
	if reads != 4 {
		t.Fatalf("retried before 30 s, got %d reads", reads)
	}
	tick(step(5*time.Second), "zh-CN", nil)
	if reads != 5 {
		t.Fatalf("no retry after 30 s, got %d reads", reads)
	}

	// Cards can expire on their own: one read at 15 minutes, none before.
	lastRead := at
	for at.Before(lastRead.Add(menuBarWaitingMaxAge - 5*time.Second)) {
		tick(step(5*time.Second), "zh-CN", nil)
	}
	if reads != 5 {
		t.Fatalf("read before the 15-minute age, got %d reads", reads)
	}
	tick(step(5*time.Second), "zh-CN", nil)
	if reads != 6 {
		t.Fatalf("no read at the 15-minute age, got %d reads", reads)
	}

	// A privacy withdraw drops the list: the next tick reads.
	memory.withdraw()
	tick(step(5*time.Second), "zh-CN", nil)
	if reads != 7 {
		t.Fatalf("a withdraw should read again, got %d reads", reads)
	}
}

// With #295's in-memory waiting state ready, the menu bar reads it instead
// of the overview and the 50-row waiting list.
func TestMenuBarViewReadsInMemoryWaitingState(t *testing.T) {
	app := mainChatTestApp(t)
	card := mainChatProactive{MomentID: "held", Kind: "menu_fixture", State: "pending", Headline: "Someone is waiting on your reply.", Localized: map[string]mainChatProactiveCopy{"en": {Headline: "Someone is waiting on your reply."}}}
	app.mainChatWaiting.mu.Lock()
	app.mainChatWaiting.state = MainChatWaitingState{SessionID: "main", Count: 7, Ready: true, Items: []StreamItemData{{ID: "held", Kind: "system", ToolName: mainChatProactiveTool, ToolInput: mainChatViewPayload(t, card)}}}
	app.mainChatWaiting.mu.Unlock()
	// The store has no main chat: a slow read would find nothing waiting.
	view, err := app.menuBarView("en", true)
	if err != nil {
		t.Fatal(err)
	}
	if view.Waiting != 7 || len(view.Items) != 1 || view.Items[0].ID != "held" || view.Items[0].SessionID != "main" || !reflect.DeepEqual(view.WaitingIDs, []string{"held"}) {
		t.Fatalf("view did not come from the in-memory state: %+v", view)
	}
	if view.Label != menuBarLabel(view.Words, view.State, 7) {
		t.Fatalf("label %q", view.Label)
	}
}
