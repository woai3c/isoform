package main

import (
	"strings"
	"testing"
	"time"

	"wails-gui/internal/pii"
	"wails-gui/internal/topics"
	"wails-gui/storage"
)

var topicTestNow = time.Date(2026, 10, 15, 9, 0, 0, 0, time.Local)

func topicHoursAgo(h float64) time.Time {
	return topicTestNow.Add(-time.Duration(h * float64(time.Hour)))
}

// topicChatsOnThreeDays is one chat per label on each of the last three
// days: chat titles alone become a topic only on a third day.
func topicChatsOnThreeDays(labels ...string) []storage.SessionRow {
	var out []storage.SessionRow
	for day := 0.0; day < 3; day++ {
		for i, label := range labels {
			out = append(out, storage.SessionRow{Label: label, UpdatedAt: topicHoursAgo(1 + float64(i) + 24*day).UnixMilli()})
		}
	}
	return out
}

// protectTopicTestApp marks a diary app protected for one test.
func protectTopicTestApp(t *testing.T) {
	t.Helper()
	previous := pii.GetUserSensitiveApps()
	pii.SetUserSensitiveApps(map[string]string{"com.example.diary": "Diary", "com.google.Chrome.canary": "Chrome Canary"})
	t.Cleanup(func() { pii.SetUserSensitiveApps(previous) })
}

func signalTexts(signals []topics.Signal) string {
	var parts []string
	for _, s := range signals {
		parts = append(parts, string(s.Source)+":"+s.Text)
	}
	return strings.Join(parts, "\n")
}

func TestTopicSignalsApplyEveryExclusion(t *testing.T) {
	protectTopicTestApp(t)
	pausedStart := topicHoursAgo(30).UnixMilli()
	in := topicInput{
		Now:    topicTestNow,
		Pauses: []observationPause{{Start: pausedStart, End: topicHoursAgo(29).UnixMilli()}},
		Visits: []BrowserVisitRow{
			{URL: "https://www.google.com/search", Title: "wails drag regions - Google Search", VisitedAt: topicHoursAgo(2).UnixMilli(), Browser: "Chrome"},
			{URL: "https://wails.io/docs/drag", Title: "Frameless drag regions | Wails", VisitedAt: topicHoursAgo(2).UnixMilli(), Browser: "Chrome"},
			{URL: "https://example.com/paused", Title: "paused visit secret", VisitedAt: topicHoursAgo(29.5).UnixMilli(), Browser: "Chrome"},
			{URL: "https://example.com/canary", Title: "canary visit secret", VisitedAt: topicHoursAgo(3).UnixMilli(), Browser: "Chrome Canary"},
			{URL: "https://example.com/private", Title: "private visit secret - Incognito", VisitedAt: topicHoursAgo(3).UnixMilli(), Browser: "Chrome"},
		},
		Windows: []topicWindowRow{
			{Title: "Dear diary secret", AppName: "Diary", BundleID: "com.example.diary", Count: 9, Last: topicHoursAgo(1).UnixMilli()},
			{Title: "Kubernetes autoscaling - Visual Studio Code", AppName: "Code", BundleID: "com.microsoft.VSCode", Count: 4, Last: topicHoursAgo(1).UnixMilli()},
			{Title: "window during pause secret", AppName: "Code", BundleID: "com.microsoft.VSCode", Count: 4, Last: topicHoursAgo(29.5).UnixMilli()},
		},
		Activities: []ActivityRow{
			{Title: "Writing in the diary secret", ActiveMsByApp: `{"Diary": 600000}`, StartMs: topicHoursAgo(5).UnixMilli(), Entities: `[{"type":"topic","name":"diary secret topic"}]`},
			{Title: "Tuning pod autoscaling", ActiveMsByApp: `{"Code": 1200000}`, StartMs: topicHoursAgo(5).UnixMilli(), Entities: `[{"type":"topic","name":"Kubernetes autoscaling"},{"type":"person","name":"Ana Lopez"}]`},
		},
		Calendar: []storage.CalendarEventRow{
			{Title: "Pricing review with Ana", StartAt: topicHoursAgo(20).UnixMilli(), OrganizerName: "Ana Lopez",
				Attendees: []storage.CalendarAttendeeRow{{Name: "Ben Ito", Email: "ben.ito@example.com"}}},
			{Title: "canceled secret meeting", StartAt: topicHoursAgo(20).UnixMilli(), Status: "canceled"},
			{Title: "Board prep tomorrow", StartAt: topicTestNow.Add(20 * time.Hour).UnixMilli()},
		},
		Sessions: []storage.SessionRow{
			{Label: "Isoform", Kind: storage.SessionKindMain, UpdatedAt: topicHoursAgo(1).UnixMilli()},
			{Label: "Fix wails drag regions", Kind: storage.SessionKindSide, UpdatedAt: topicHoursAgo(1).UnixMilli()},
			{Label: "Transcript: standup secret", UpdatedAt: topicHoursAgo(1).UnixMilli()},
			{Label: "Automation run secret", AutomationResult: true, UpdatedAt: topicHoursAgo(1).UnixMilli()},
		},
		Memories: []MemoryEntry{
			{Title: "Prefers Go for services", Dimension: "user_insight", UpdatedAt: topicHoursAgo(40).UnixMilli()},
			{Title: "agent note secret", Dimension: "agentic_memory", UpdatedAt: topicHoursAgo(40).UnixMilli()},
		},
		Goals:  []topicGoal{{Title: "Ship the iPhone companion", At: topicTestNow.Add(-60 * 24 * time.Hour)}},
		Files:  []FileActivityRow{{Name: "pricing_model_v2.numbers", ActiveAt: topicHoursAgo(3).UnixMilli(), AppName: "Numbers"}},
		People: []string{"Ana Lopez"},
	}
	signals, names := topicSignals(in)
	all := signalTexts(signals)
	if strings.Contains(all, "secret") {
		t.Fatalf("an excluded record became a signal:\n%s", all)
	}
	for _, want := range []string{
		"search:wails drag regions", "page:Frameless drag regions | Wails", "window:Kubernetes autoscaling",
		"entity:Kubernetes autoscaling", "meeting:Pricing review with Ana", "meeting:Board prep tomorrow",
		"chat:Fix wails drag regions", "memory:Prefers Go for services", "goal:Ship the iPhone companion",
		"file:pricing model v2",
	} {
		if !strings.Contains(all, want) {
			t.Fatalf("signal %q missing from:\n%s", want, all)
		}
	}
	for _, s := range signals {
		if s.At.After(topicTestNow) || topicTestNow.Sub(s.At) >= topicWindow {
			t.Fatalf("signal %q is outside the window: %v", s.Text, s.At)
		}
	}
	joined := strings.Join(names, ",")
	for _, want := range []string{"Ana Lopez", "Ben Ito", "ben.ito@example.com"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("names %v miss %q", names, want)
		}
	}

	ranked := rankTopics(in, topicsStateFile{}, topicTestNow)
	for _, topic := range ranked {
		lower := strings.ToLower(topic.Label)
		if strings.Contains(lower, "ana") || strings.Contains(lower, "ben") || strings.Contains(lower, "secret") {
			t.Fatalf("ranked label %q leaks a name or an excluded record", topic.Label)
		}
	}
}

func TestTopicSignalsSkipMessagingAndPeoplePages(t *testing.T) {
	in := topicInput{
		Now: topicTestNow,
		Visits: []BrowserVisitRow{
			{URL: "https://www.linkedin.com/in/someone", Title: "Someone Person secret - LinkedIn", VisitedAt: topicHoursAgo(1).UnixMilli(), Browser: "Chrome"},
			{URL: "https://mail.google.com/mail/u/0/#inbox", Title: "Offer letter secret - Gmail", VisitedAt: topicHoursAgo(1).UnixMilli(), Browser: "Chrome"},
			{URL: "https://mail.qq.com/cgi-bin/frame_html", Title: "secret QQ mail", VisitedAt: topicHoursAgo(1).UnixMilli(), Browser: "Chrome"},
			{URL: "https://chat.google.com/room/x", Title: "secret space", VisitedAt: topicHoursAgo(1).UnixMilli(), Browser: "Chrome"},
			{URL: "https://abc.feishu.cn/messenger/", Title: "secret 飞书", VisitedAt: topicHoursAgo(1).UnixMilli(), Browser: "Chrome"},
			{URL: "https://wails.io/docs", Title: "Wails docs", VisitedAt: topicHoursAgo(1).UnixMilli(), Browser: "Chrome"},
		},
		Windows: []topicWindowRow{
			{Title: "secret DM thread", AppName: "Slack", BundleID: "com.tinyspeck.slackmacgap", Count: 5, Last: topicHoursAgo(1).UnixMilli()},
			{Title: "secret chat", AppName: "Messages", BundleID: "com.apple.MobileSMS", Count: 5, Last: topicHoursAgo(1).UnixMilli()},
			{Title: "secret thread", AppName: "微信", BundleID: "com.tencent.xinWeChat", Count: 5, Last: topicHoursAgo(1).UnixMilli()},
			{Title: "secret group", AppName: "QQ", BundleID: "com.tencent.qq", Count: 5, Last: topicHoursAgo(1).UnixMilli()},
			{Title: "secret 群聊", AppName: "企业微信", BundleID: "com.tencent.WeWorkMac", Count: 5, Last: topicHoursAgo(1).UnixMilli()},
			{Title: "secret inbox", AppName: "Thunderbird", BundleID: "org.mozilla.thunderbird", Count: 5, Last: topicHoursAgo(1).UnixMilli()},
		},
		Memories: []MemoryEntry{{Title: "secret promise to Priya", Dimension: "user_insight", Apps: []string{"Slack"}, UpdatedAt: topicHoursAgo(2).UnixMilli()}},
		Files: []FileActivityRow{
			{Name: "secret_attachment.pdf", ActiveAt: topicHoursAgo(1).UnixMilli(), AppName: "Mail", AppBundleID: "com.apple.mail"},
			{Name: "secret_received.docx", ActiveAt: topicHoursAgo(1).UnixMilli(), AppName: "WeChat", AppBundleID: "com.tencent.xinWeChat"},
		},
	}
	in.Activities = []ActivityRow{{Title: "Negotiating the secret renewal with Priya", ActiveMsByApp: `{"Slack": 900000}`,
		StartMs: topicHoursAgo(2).UnixMilli(), Entities: `[{"type":"topic","name":"secret renewal"}]`}}
	in.Semantic = []SemanticEventRow{{Title: "Reading the secret thread", Apps: `["Mail"]`,
		StartMs: topicHoursAgo(2).UnixMilli(), EndMs: topicHoursAgo(1).UnixMilli(), Entities: `[{"type":"topic","name":"secret thread"}]`}}
	signals, _ := topicSignals(in)
	all := signalTexts(signals)
	if strings.Contains(all, "secret") || !strings.Contains(all, "page:Wails docs") {
		t.Fatalf("messaging or people titles counted:\n%s", all)
	}
}

func TestTopicSignalsUnreadablePauseLogDropsObservedSources(t *testing.T) {
	in := topicInput{
		Now:           topicTestNow,
		PausesUnknown: true,
		Visits:        []BrowserVisitRow{{URL: "https://wails.io/docs", Title: "Wails docs", VisitedAt: topicHoursAgo(1).UnixMilli(), Browser: "Chrome"}},
		Windows:       []topicWindowRow{{Title: "Wails docs", AppName: "Code", Count: 3, Last: topicHoursAgo(1).UnixMilli()}},
		Sessions:      []storage.SessionRow{{Label: "Plan the launch", UpdatedAt: topicHoursAgo(1).UnixMilli()}},
	}
	signals, _ := topicSignals(in)
	if len(signals) != 1 || signals[0].Source != topics.SourceChat {
		t.Fatalf("with no pause log only what the user wrote may count:\n%s", signalTexts(signals))
	}
}

func TestTopicSignalsJournalFillsOlderDaysOnly(t *testing.T) {
	protectTopicTestApp(t)
	yesterday := topicTestNow.AddDate(0, 0, -1).Format("2006-01-02")
	lastWeek := topicTestNow.AddDate(0, 0, -6).Format("2006-01-02")
	in := topicInput{
		Now:    topicTestNow,
		Visits: []BrowserVisitRow{{URL: "https://wails.io", Title: "Wails live", VisitedAt: topicTestNow.AddDate(0, 0, -1).UnixMilli(), Browser: "Chrome"}},
		Journal: []topicJournalDay{
			{Date: yesterday, Browser: "Chrome", Phrases: []topicJournalPhrase{{Text: "duplicate of live day", Source: "page", Count: 3}}},
			{Date: lastWeek, Browser: "Chrome", Phrases: []topicJournalPhrase{{Text: "rust async runtime", Source: "search", Count: 2}}},
			{Date: lastWeek, Browser: "Chrome Canary", Phrases: []topicJournalPhrase{{Text: "protected browser secret", Source: "search", Count: 2}}},
		},
	}
	all := signalTexts(func() []topics.Signal { s, _ := topicSignals(in); return s }())
	if strings.Contains(all, "duplicate of live day") || strings.Contains(all, "secret") {
		t.Fatalf("journal days covered by live visits or protected browsers counted:\n%s", all)
	}
	if !strings.Contains(all, "search:rust async runtime") {
		t.Fatalf("an older journal day was not used:\n%s", all)
	}
}

func TestRecordTopicJournalKeepsFinishedDaysAndHashesLinks(t *testing.T) {
	state := topicsStateFile{}
	yesterday := topicTestNow.AddDate(0, 0, -1)
	rows := []BrowserVisitRow{
		{URL: "https://www.wails.io/docs/", Title: "Wails docs", VisitedAt: yesterday.UnixMilli(), Browser: "Chrome"},
		{URL: "https://wails.io/today", Title: "Today page", VisitedAt: topicTestNow.UnixMilli(), Browser: "Chrome"},
	}
	calls := 0
	phrases := func([]BrowserVisitRow) []topicJournalPhrase {
		calls++
		return []topicJournalPhrase{{Text: "wails docs", Source: "page", Count: 1}}
	}
	recordTopicJournal(&state, rows, phrases, topicTestNow)
	recordTopicJournal(&state, rows, phrases, topicTestNow)
	if len(state.Journal) != 1 || calls != 1 || state.Journal[0].Date != yesterday.Format("2006-01-02") {
		t.Fatalf("journal = %+v after %d digests, want yesterday once", state.Journal, calls)
	}
	if _, ok := state.Visited[topicLinkHash("http://wails.io/docs?utm=1")]; !ok {
		t.Fatalf("a visited link was not remembered by its hash: %v", state.Visited)
	}
	for key := range state.Visited {
		if strings.Contains(key, "wails") {
			t.Fatalf("the journal kept a link in the clear: %q", key)
		}
	}

	state.Journal = append(state.Journal, topicJournalDay{Date: topicTestNow.AddDate(0, 0, -20).Format("2006-01-02"), Browser: "Chrome"})
	state.Visited["old"] = topicTestNow.AddDate(0, 0, -20).UnixMilli()
	pruneTopicsState(&state, topicTestNow)
	if len(state.Journal) != 1 || state.Visited["old"] != 0 {
		t.Fatalf("old journal days or visits were kept: %+v", state)
	}
}

func TestUserTopicsMergesPinsNamesAndRemovals(t *testing.T) {
	ranked := []topics.Topic{
		{ID: "a", Key: "wails drag regions", Label: "Wails drag regions", LastAt: topicHoursAgo(2), Sources: []topics.Source{topics.SourceSearch}},
		{ID: "b", Key: "pricing review", Label: "pricing review"},
		{ID: "c", Key: "kubernetes autoscaling", Label: "Kubernetes autoscaling"},
	}
	state := topicsStateFile{Overrides: map[string]topicOverride{
		"c":    {Key: "kubernetes autoscaling", Pinned: true, At: "1"},
		"b":    {Key: "pricing review", Removed: true, At: "2"},
		"a":    {Key: "wails drag regions", Label: "Wails windows", At: "3"},
		"gone": {Key: "rust async", Label: "Rust async", Pinned: true, At: "0"},
	}}
	view := userTopics(ranked, state)
	var labels []string
	for _, topic := range view.Topics {
		labels = append(labels, topic.Label)
	}
	if strings.Join(labels, "|") != "Rust async|Kubernetes autoscaling|Wails drag regions" {
		t.Fatalf("topics = %v", labels)
	}
	if view.Topics[2].Original != "" {
		t.Fatalf("a label kept at pin time showed as a rename: %+v", view.Topics[2])
	}
	state.Overrides["a"] = topicOverride{Key: "wails drag regions", Label: "Wails windows", Renamed: true, At: "3"}
	view = userTopics(ranked, state)
	if view.Topics[2].Original != "Wails drag regions" || view.Topics[2].LastSeenAt == "" {
		t.Fatalf("renamed topic = %+v", view.Topics[2])
	}
	if len(view.Removed) != 1 || view.Removed[0].ID != "b" {
		t.Fatalf("removed = %+v", view.Removed)
	}

	off := false
	state.Enabled = &off
	if view := userTopics(ranked, state); view.Enabled || len(view.Topics) != 2 {
		t.Fatalf("with the feature off only the user's pins stay listed: %+v", view)
	}
}

func TestTopicsServiceUpdateAndSwitch(t *testing.T) {
	t.Setenv(topicsStateDirEnvVar, t.TempDir())
	svc := newTopicsService(&App{})
	svc.now = func() time.Time { return topicTestNow }
	svc.collect = func(now time.Time) topicInput {
		return topicInput{Now: now, Sessions: topicChatsOnThreeDays("Fix wails drag regions", "Plan pricing review")}
	}
	view := svc.view()
	if len(view.Topics) == 0 {
		t.Fatal("no topics from three days of chats")
	}
	id := view.Topics[0].ID
	for _, bad := range []string{"https://example.com", "/Users/ana/notes", "ana@example.com", "", strings.Repeat("word ", 10)} {
		if _, err := svc.update(UserTopicUpdate{ID: id, Action: "rename", Label: bad}); err == nil {
			t.Fatalf("rename to %q was accepted", bad)
		}
	}
	view, err := svc.update(UserTopicUpdate{ID: id, Action: "rename", Label: "Wails windows"})
	if err != nil || view.Topics[0].Label != "Wails windows" {
		t.Fatalf("rename: %+v, %v", view.Topics, err)
	}
	view, err = svc.update(UserTopicUpdate{ID: id, Action: "remove"})
	if err != nil || len(view.Removed) != 1 {
		t.Fatalf("remove: %+v, %v", view, err)
	}
	for _, topic := range view.Topics {
		if topic.ID == id {
			t.Fatalf("a removed topic is still listed: %+v", view.Topics)
		}
	}
	if _, err := svc.update(UserTopicUpdate{ID: id, Action: "jump"}); err == nil {
		t.Fatal("an unknown change was accepted")
	}

	if _, err := svc.store.update(func(st *topicsStateFile) {
		st.Journal = []topicJournalDay{{Date: "2026-10-14", Browser: "Chrome"}}
		st.Finds.Items = []TopicFind{{ID: "f"}}
	}); err != nil {
		t.Fatal(err)
	}
	view, err = svc.setEnabled(false)
	if err != nil || view.Enabled {
		t.Fatalf("switch off: %+v, %v", view, err)
	}
	state := svc.store.load()
	if len(state.Journal) != 1 || len(state.Finds.Items) != 1 || len(state.Overrides) == 0 {
		t.Fatalf("switching off must retain the journal, finds and user's choices: %+v", state)
	}
}
