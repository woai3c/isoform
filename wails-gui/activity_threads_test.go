package main

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/platform/activitymon"
	"yansu-agent/agent"
)

// threadTestRunner answers thread prompts. answer maps a moment title
// fragment to {thread, kind, confidence}; thread names an existing thread by a
// title fragment of one of its moments, or "newN".
type threadTestRunner struct {
	agent.AgentRunner
	prompts []string
	reply   func(prompt string) string
}

func (r *threadTestRunner) ExecuteTask(ctx context.Context, input *agent.TaskInput, callback agent.StreamCallback) (string, error) {
	r.prompts = append(r.prompts, input.Prompt)
	if err := callback(&agent.StreamEvent{Type: "assistant", Content: r.reply(input.Prompt)}); err != nil {
		return "", err
	}
	return "thread-test-run", nil
}

type threadAnswer struct{ thread, kind, confidence, title string }

var threadPromptMomentLine = regexp.MustCompile(`(?m)^- (m\d+) \d\d:\d\d: ([^|\n]+)`)
var threadPromptThreadLine = regexp.MustCompile(`(?m)^- (t\d+) \((work|personal)\): .*\n((?:  - .*\n)*)`)

// threadReply builds a model reply from answers keyed by moment title
// fragments. A thread target "thread:<fragment>" names the existing thread
// that lists a moment with that fragment.
func threadReply(answers map[string]threadAnswer) func(string) string {
	return func(prompt string) string {
		threadAlias := map[string]string{}
		for _, match := range threadPromptThreadLine.FindAllStringSubmatch(prompt, -1) {
			for _, line := range strings.Split(match[3], "\n") {
				if line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- ")); line != "" {
					threadAlias[line] = match[1]
				}
			}
		}
		for _, match := range regexp.MustCompile(`(?m)^- m\d+ \d\d:\d\d: ([^|\n]+).*\| rules joined (t\d+)`).FindAllStringSubmatch(prompt, -1) {
			threadAlias[strings.TrimSpace(match[1])] = match[2]
		}
		var resp threadModelResponse
		titles := map[string]threadModelThread{}
		for _, match := range threadPromptMomentLine.FindAllStringSubmatch(prompt, -1) {
			title := strings.TrimSpace(match[2])
			for fragment, answer := range answers {
				if !strings.Contains(title, fragment) {
					continue
				}
				target := answer.thread
				if strings.HasPrefix(target, "thread:") {
					want := strings.TrimPrefix(target, "thread:")
					target = "new99"
					for line, alias := range threadAlias {
						if strings.Contains(line, want) {
							target = alias
						}
					}
				}
				resp.Moments = append(resp.Moments, threadModelMoment{ID: match[1], Thread: target, Kind: answer.kind, Confidence: answer.confidence, Reason: "test"})
				if answer.title != "" {
					titles[target] = threadModelThread{ID: target, Title: answer.title, Kind: answer.kind}
				}
			}
		}
		for _, thread := range titles {
			resp.Threads = append(resp.Threads, thread)
		}
		raw, _ := json.Marshal(resp)
		return string(raw)
	}
}

type threadFixtureMoment struct {
	id, title, app, bundle string
	start, end             time.Time
	entities               []Entity
}

func seedThreadMoment(t *testing.T, app *App, m threadFixtureMoment) ActivityRow {
	t.Helper()
	store := app.activity.store
	start, end := m.start.UnixMilli(), m.end.UnixMilli()
	bundle := m.bundle
	if bundle == "" {
		bundle = "com.example." + strings.ToLower(strings.ReplaceAll(m.app, " ", ""))
	}
	events := []activitymon.ActivityEvent{{Kind: activitymon.EventFocus, Timestamp: start, AppName: m.app, AppBundleID: bundle, WindowTitle: m.title}}
	sessionID := "session-" + m.id
	appsJSON, _ := json.Marshal([]string{m.app})
	if err := store.SaveSession(ActivitySessionRow{ID: sessionID, StartTime: start, EndTime: end, AppNames: string(appsJSON), TriageStatus: "done"}, events, nil); err != nil {
		t.Fatal(err)
	}
	run := testRun("run-"+m.id, m.app, start, end, end-start, len(events))
	run.AppKey = bundle
	run.SessionIDs = []string{sessionID}
	if err := store.db.QueryRow(`SELECT MIN(id),MAX(id) FROM activity_events WHERE session_id=?`, sessionID).Scan(&run.FirstEventRowID, &run.LastEventRowID); err != nil {
		t.Fatal(err)
	}
	insertComposableTimelineRun(t, store, run, ActivityTimelineGroupSummary{Title: m.title})
	entities, _ := json.Marshal(m.entities)
	row := ActivityRow{
		ID: m.id, DayStartMs: startOfDayLocal(start), Title: m.title, Status: activityStatusIdle,
		RunIDs: composeJSON([]string{run.ID}), ActiveMsByApp: fmt.Sprintf(`{%q:%d}`, m.app, end-start),
		Entities: string(entities), StartMs: start, EndMs: end, CreatedAt: start, UpdatedAt: end,
	}
	if err := store.InsertActivity(row); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertActivityRunLink(run.ID, m.id, run.SummarizedEventCount, end); err != nil {
		t.Fatal(err)
	}
	return row
}

func threadTestDay(t *testing.T) (time.Time, int64) {
	t.Helper()
	now := time.Now()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	return day, day.UnixMilli()
}

func at(day time.Time, hour, minute int) time.Time {
	return day.Add(time.Duration(hour)*time.Hour + time.Duration(minute)*time.Minute)
}

func threadOf(t *testing.T, app *App, activityID string) string {
	t.Helper()
	var id string
	if err := app.activity.store.db.QueryRow(`SELECT thread_id FROM activity_thread_members WHERE activity_id = ?`, activityID).Scan(&id); err != nil {
		return ""
	}
	return id
}

func newThreadTestApp(t *testing.T) *App {
	t.Helper()
	isolateActivityPrivacyTest(t)
	app := newPrivacyReadTestApp(t)
	app.cachedDisplayName = "Pat Example"
	t.Cleanup(func() { activityThreadCacheForget(0) })
	return app
}

func morningFixture(day time.Time) []threadFixtureMoment {
	return []threadFixtureMoment{
		{id: "brief", title: "Fix morning briefing generation in isoform hub session", app: "Claude", start: at(day, 9, 10), end: at(day, 9, 20), entities: []Entity{{Type: entityRepo, Name: "isoform"}, {Type: entityTopic, Name: "morning briefing"}}},
		{id: "hats", title: "Research wearable hat devices in ChatGPT", app: "ChatGPT", start: at(day, 9, 21), end: at(day, 9, 27), entities: []Entity{{Type: entityTopic, Name: "Research wearable hats"}, {Type: entityTopic, Name: "ChatGPT"}}},
		{id: "openbci", title: "Evaluate buying the Open-cEEGrid bundle from OpenBCI Shop", app: "Safari", start: at(day, 9, 53), end: at(day, 10, 5), entities: []Entity{{Type: entityTopic, Name: "OpenBCI Shop"}, {Type: entityTopic, Name: "cEEGrid"}}},
		{id: "workers", title: "Coordinate Claude workers in the isoform hub Claude Code session", app: "Claude", start: at(day, 11, 41), end: at(day, 11, 45), entities: []Entity{{Type: entityProject, Name: "isoform hub"}, {Type: entityTopic, Name: "Claude workers"}}},
	}
}

func TestActivityThreadsInterruptionDoesNotSplitAThread(t *testing.T) {
	app := newThreadTestApp(t)
	day, dayMs := threadTestDay(t)
	for _, m := range morningFixture(day) {
		seedThreadMoment(t, app, m)
	}
	runner := &threadTestRunner{reply: threadReply(map[string]threadAnswer{
		"wearable hat": {thread: "new1", kind: "work", confidence: "high", title: "Wearable EEG research"},
		"OpenBCI":      {thread: "new1", kind: "work", confidence: "high", title: "Wearable EEG research"},
		"briefing":     {thread: "thread:briefing", kind: "work", confidence: "high", title: "Isoform: brief fix, workers"},
		"workers":      {thread: "thread:briefing", kind: "work", confidence: "high", title: "Isoform: brief fix, workers"},
	})}
	app.runner = runner
	if _, err := app.runActivityThreadPass(context.Background(), dayMs, true, at(day, 12, 0)); err != nil {
		t.Fatal(err)
	}
	// Two and a half hours of other work sit between the brief fix and the
	// workers moment; the shared repo keeps them in one thread.
	if threadOf(t, app, "brief") == "" || threadOf(t, app, "brief") != threadOf(t, app, "workers") {
		t.Fatalf("interrupted Isoform work split: brief=%q workers=%q", threadOf(t, app, "brief"), threadOf(t, app, "workers"))
	}
	if threadOf(t, app, "hats") != threadOf(t, app, "openbci") || threadOf(t, app, "hats") == threadOf(t, app, "brief") {
		t.Fatalf("wearable research grouped wrong: hats=%q openbci=%q brief=%q", threadOf(t, app, "hats"), threadOf(t, app, "openbci"), threadOf(t, app, "brief"))
	}
	got, err := app.GetDayThreads(dayMs, dayMs+activityThreadDayMs-1)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Grouped || len(got.Threads) != 2 {
		t.Fatalf("want 2 grouped threads, got %+v", got)
	}
	isoform := got.Threads[0]
	if isoform.Title != "Isoform: brief fix, workers" || isoform.StartMs != at(day, 9, 10).UnixMilli() || isoform.EndMs != at(day, 11, 45).UnixMilli() {
		t.Fatalf("Isoform thread = %+v", isoform)
	}
	if strings.Join(isoform.ActivityIDs, ",") != "brief,workers" {
		t.Fatalf("Isoform moments = %v", isoform.ActivityIDs)
	}
}

func TestActivityThreadsKeepUnrelatedWorkApart(t *testing.T) {
	app := newThreadTestApp(t)
	day, dayMs := threadTestDay(t)
	fixtures := append(morningFixture(day)[:2],
		threadFixtureMoment{id: "reflection", title: "Dictate personal reflections in Isoform main chat", app: "Isoform", start: at(day, 11, 33), end: at(day, 11, 35), entities: []Entity{{Type: entityProject, Name: "Isoform"}, {Type: entityTopic, Name: "Personal reflection"}}},
		threadFixtureMoment{id: "bed", title: "Draft personal notes on a bed purchase in ChatGPT", app: "ChatGPT", start: at(day, 14, 20), end: at(day, 14, 25), entities: []Entity{{Type: entityTopic, Name: "Research wearable hats"}, {Type: entityTopic, Name: "ChatGPT"}}},
		threadFixtureMoment{id: "pr", title: "Review pull requests in example/isoform", app: "Safari", start: at(day, 15, 0), end: at(day, 15, 10), entities: []Entity{{Type: entityRepo, Name: "example/isoform"}}},
	)
	for _, m := range fixtures {
		seedThreadMoment(t, app, m)
	}
	// The model tries to pull the personal moments into work threads; the
	// guards refuse both.
	runner := &threadTestRunner{reply: threadReply(map[string]threadAnswer{
		"wearable hat":  {thread: "new1", kind: "work", confidence: "high", title: "Wearable research"},
		"reflections":   {thread: "thread:briefing", kind: "personal", confidence: "high"},
		"bed purchase":  {thread: "new1", kind: "personal", confidence: "high"},
		"briefing":      {thread: "thread:briefing", kind: "work", confidence: "high", title: "Isoform"},
		"pull requests": {thread: "thread:briefing", kind: "work", confidence: "high", title: "Isoform"},
	})}
	app.runner = runner
	if _, err := app.runActivityThreadPass(context.Background(), dayMs, true, at(day, 16, 0)); err != nil {
		t.Fatal(err)
	}
	isoform := threadOf(t, app, "brief")
	if threadOf(t, app, "pr") != isoform {
		t.Fatalf("shared repo did not join: pr=%q brief=%q", threadOf(t, app, "pr"), isoform)
	}
	if got := threadOf(t, app, "reflection"); got == "" || got == isoform {
		t.Fatalf("personal dictation in Isoform joined work: %q", got)
	}
	if got := threadOf(t, app, "bed"); got == "" || got == threadOf(t, app, "hats") {
		t.Fatalf("a chat-sidebar title joined personal notes to research: %q", got)
	}
	var kind string
	if err := app.activity.store.db.QueryRow(`SELECT kind FROM activity_threads WHERE id = ?`, threadOf(t, app, "reflection")).Scan(&kind); err != nil || kind != threadKindPersonal {
		t.Fatalf("reflection thread kind = %q, %v", kind, err)
	}
	// Rules alone never join on a tool or a topic word.
	if len(runner.prompts) != 1 || !strings.Contains(runner.prompts[0], "Dictate personal reflections") {
		t.Fatalf("reflection was not left to the model: %d prompts", len(runner.prompts))
	}
}

func TestThreadApplyModelGuards(t *testing.T) {
	in := threadPromptInput{
		Threads:    []threadState{{ID: "work", Kind: threadKindWork}, {ID: "home", Kind: threadKindPersonal}},
		Moments:    []threadMoment{{ID: "a", Title: "A"}, {ID: "b", Title: "B"}, {ID: "c", Title: "C"}, {ID: "d", Title: "D"}, {ID: "e", Title: "E"}},
		RuleJoined: map[string]string{"e": "work"},
	}
	_, aliases := buildThreadPrompt(in)
	resp := threadModelResponse{Moments: []threadModelMoment{
		{ID: "m1", Thread: "t1", Kind: "work", Confidence: "low"},      // low confidence
		{ID: "m2", Thread: "t2", Kind: "work", Confidence: "high"},     // work into a personal thread
		{ID: "m3", Thread: "t1", Kind: "personal", Confidence: "high"}, // personal into work
		{ID: "m5", Thread: "t9", Kind: "work", Confidence: "high"},     // rule join, unknown thread: rules stand
	}}
	assignments, created, _ := threadApplyModel(in, aliases, resp)
	got := map[string]string{}
	for _, as := range assignments {
		got[as.MomentID] = as.ThreadID
	}
	for _, id := range []string{"a", "b", "c", "d"} {
		if got[id] != threadSeedKey(id) {
			t.Errorf("moment %s should get its own thread, got %q", id, got[id])
		}
	}
	if _, ok := got["e"]; ok {
		t.Errorf("rule join was overridden: %q", got["e"])
	}
	if len(created) != 4 {
		t.Errorf("created %d own threads, want 4", len(created))
	}

	// A confident answer moves a rule join that only shared an object seen in
	// passing; an unsure one does not.
	in.Moments = []threadMoment{{ID: "f", Title: "F"}, {ID: "g", Title: "G"}}
	in.RuleJoined = map[string]string{"f": "work", "g": "work"}
	in.Threads = append(in.Threads, threadState{ID: "posts", Kind: threadKindWork})
	_, aliases = buildThreadPrompt(in)
	assignments, _, _ = threadApplyModel(in, aliases, threadModelResponse{Moments: []threadModelMoment{
		{ID: "m1", Thread: "t3", Kind: "work", Confidence: "high"},
		{ID: "m2", Thread: "t3", Kind: "work", Confidence: "medium"},
	}})
	if len(assignments) != 1 || assignments[0].MomentID != "f" || assignments[0].ThreadID != "posts" {
		t.Fatalf("rule overrule = %+v", assignments)
	}
}

func TestActivityThreadsUserMoveSurvivesRegroup(t *testing.T) {
	app := newThreadTestApp(t)
	day, dayMs := threadTestDay(t)
	for _, m := range morningFixture(day) {
		seedThreadMoment(t, app, m)
	}
	if _, err := app.runActivityThreadPass(context.Background(), dayMs, false, at(day, 12, 0)); err != nil {
		t.Fatal(err)
	}
	if threadOf(t, app, "brief") != threadOf(t, app, "workers") {
		t.Fatal("rules did not join the shared repo")
	}
	own, err := app.MoveActivityToThread("workers", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := app.RegroupActivityDay(dayMs); err != nil {
		t.Fatal(err)
	}
	if got := threadOf(t, app, "workers"); got != own {
		t.Fatalf("regroup undid the user's move: %q, want %q", got, own)
	}
	// A model pass that wants it back cannot move it either, and it sees the
	// correction as an example.
	runner := &threadTestRunner{reply: threadReply(map[string]threadAnswer{
		"workers": {thread: "thread:briefing", kind: "work", confidence: "high"},
	})}
	app.runner = runner
	if _, err := app.activity.store.db.Exec(`UPDATE activity_thread_members SET reviewed = 0, source = 'rule' WHERE activity_id = 'brief'`); err != nil {
		t.Fatal(err)
	}
	if _, err := app.runActivityThreadPass(context.Background(), dayMs, true, at(day, 12, 30)); err != nil {
		t.Fatal(err)
	}
	if got := threadOf(t, app, "workers"); got != own {
		t.Fatalf("model pass moved the user's moment: %q", got)
	}
	if len(runner.prompts) == 0 || !strings.Contains(runner.prompts[0], "into its own thread") {
		t.Fatalf("the correction was not remembered in the prompt: %v", runner.prompts)
	}
	// Moving to another thread of the day works the same way.
	if _, err := app.MoveActivityToThread("hats", threadOf(t, app, "brief")); err != nil {
		t.Fatal(err)
	}
	if err := app.RegroupActivityDay(dayMs); err != nil {
		t.Fatal(err)
	}
	if threadOf(t, app, "hats") != threadOf(t, app, "brief") {
		t.Fatal("regroup undid a move into another thread")
	}
	if err := app.RenameActivityThread(own, "Hub workers"); err != nil {
		t.Fatal(err)
	}
	got, err := app.GetDayThreads(dayMs, dayMs+activityThreadDayMs-1)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, thread := range got.Threads {
		if thread.ID == own {
			found = thread.Title == "Hub workers" && thread.UserTitled
		}
	}
	if !found {
		t.Fatalf("rename not shown: %+v", got.Threads)
	}
}

func TestActivityThreadsPrivacyFiltersStillApply(t *testing.T) {
	app := newThreadTestApp(t)
	day, dayMs := threadTestDay(t)
	for _, m := range morningFixture(day) {
		seedThreadMoment(t, app, m)
	}
	seedThreadMoment(t, app, threadFixtureMoment{id: "private", title: "Secret isoform plans in Private Notes", app: "Private Notes", bundle: "com.example.private", start: at(day, 10, 0), end: at(day, 10, 10), entities: []Entity{{Type: entityRepo, Name: "isoform"}}})
	runner := &threadTestRunner{reply: threadReply(map[string]threadAnswer{
		"briefing": {thread: "thread:briefing", kind: "work", confidence: "high", title: "Isoform work"},
		"workers":  {thread: "thread:briefing", kind: "work", confidence: "high", title: "Isoform work"},
	})}
	app.runner = runner
	if _, err := app.runActivityThreadPass(context.Background(), dayMs, true, at(day, 12, 0)); err != nil {
		t.Fatal(err)
	}
	if threadOf(t, app, "private") != "" {
		t.Fatal("a private moment joined a thread")
	}
	for _, prompt := range runner.prompts {
		if strings.Contains(prompt, "Secret isoform plans") {
			t.Fatal("a private moment reached the model prompt")
		}
	}
	got, err := app.GetDayThreads(dayMs, dayMs+activityThreadDayMs-1)
	if err != nil {
		t.Fatal(err)
	}
	for _, thread := range got.Threads {
		for _, id := range thread.ActivityIDs {
			if id == "private" {
				t.Fatal("a private moment is listed in a thread")
			}
		}
	}
	// A moment that turns private after grouping leaves its thread, and the
	// title it helped write falls back.
	if _, err := app.activity.store.db.Exec(`UPDATE activities SET active_ms_by_app = '{"Private Notes":1000}' WHERE id = 'workers'`); err != nil {
		t.Fatal(err)
	}
	activityThreadCacheForget(dayMs)
	got, err = app.GetDayThreads(dayMs, dayMs+activityThreadDayMs-1)
	if err != nil {
		t.Fatal(err)
	}
	for _, thread := range got.Threads {
		if strings.Contains(strings.Join(thread.ActivityIDs, ","), "workers") {
			t.Fatal("a moment that became private is still in a thread")
		}
		if thread.Title == "Isoform work" {
			t.Fatal("a title written from a now-private moment is still shown")
		}
	}
}

func TestActivityThreadTimeAddsUpToTheDayLine(t *testing.T) {
	app := newThreadTestApp(t)
	day, dayMs := threadTestDay(t)
	for _, m := range morningFixture(day) {
		seedThreadMoment(t, app, m)
	}
	// A session with no moment (a glance) still counts on the day line.
	if err := app.activity.store.SaveSession(ActivitySessionRow{ID: "session-glance", StartTime: at(day, 13, 0).UnixMilli(), EndTime: at(day, 13, 3).UnixMilli(), AppNames: `["Finder"]`, TriageStatus: "done"},
		[]activitymon.ActivityEvent{{Kind: activitymon.EventFocus, Timestamp: at(day, 13, 0).UnixMilli(), AppName: "Finder", AppBundleID: "com.apple.finder"}}, nil); err != nil {
		t.Fatal(err)
	}
	app.runner = &threadTestRunner{reply: threadReply(map[string]threadAnswer{
		"wearable hat": {thread: "new1", kind: "work", confidence: "high", title: "Wearable research"},
		"OpenBCI":      {thread: "new1", kind: "work", confidence: "high"},
	})}
	if _, err := app.runActivityThreadPass(context.Background(), dayMs, true, at(day, 14, 0)); err != nil {
		t.Fatal(err)
	}
	threads, err := app.GetDayThreads(dayMs, dayMs+activityThreadDayMs-1)
	if err != nil {
		t.Fatal(err)
	}
	spent, total, err := app.activityMomentTimeMs(context.Background(), dayMs, dayMs+activityThreadDayMs)
	if err != nil {
		t.Fatal(err)
	}
	summary, err := app.GetDailySummary(day.Format("2006-01-02"))
	if err != nil {
		t.Fatal(err)
	}
	if total != summary.TotalActiveMs || total == 0 {
		t.Fatalf("thread time base %d != day line %d", total, summary.TotalActiveMs)
	}
	var sum int64
	inThread := map[string]bool{}
	for _, thread := range threads.Threads {
		sum += thread.TimeSpentMs
		for _, id := range thread.ActivityIDs {
			inThread[id] = true
		}
	}
	for id, ms := range spent {
		if !inThread[id] {
			sum += ms
		}
	}
	if sum != summary.TotalActiveMs {
		t.Fatalf("threads plus unthreaded time = %d, day line = %d", sum, summary.TotalActiveMs)
	}
	// The brief-fix moment ran 10 minutes in its own session: its thread shows
	// that time plus the workers moment's 4 minutes, plus half of the counted
	// 1 minute reading gap after it (the hats thread takes the other half).
	if threads.Threads[0].TimeSpentMs != (14*time.Minute + 30*time.Second).Milliseconds() {
		t.Fatalf("Isoform time = %v", time.Duration(threads.Threads[0].TimeSpentMs)*time.Millisecond)
	}
}

func TestActivityThreadTitleFallsBackWhenAMomentIsDeleted(t *testing.T) {
	app := newThreadTestApp(t)
	day, dayMs := threadTestDay(t)
	for _, m := range morningFixture(day) {
		seedThreadMoment(t, app, m)
	}
	app.runner = &threadTestRunner{reply: threadReply(map[string]threadAnswer{
		"briefing": {thread: "thread:briefing", kind: "work", confidence: "high", title: "Isoform: brief fix, workers"},
		"workers":  {thread: "thread:briefing", kind: "work", confidence: "high", title: "Isoform: brief fix, workers"},
	})}
	if _, err := app.runActivityThreadPass(context.Background(), dayMs, true, at(day, 12, 0)); err != nil {
		t.Fatal(err)
	}
	if err := app.activity.store.DeleteActivity("workers"); err != nil {
		t.Fatal(err)
	}
	activityThreadCacheForget(dayMs)
	got, err := app.GetDayThreads(dayMs, dayMs+activityThreadDayMs-1)
	if err != nil {
		t.Fatal(err)
	}
	for _, thread := range got.Threads {
		if strings.Contains(thread.Title, "workers") {
			t.Fatalf("title from a deleted moment still shown: %q", thread.Title)
		}
	}
	// The next pass erases the stored title too.
	if _, err := app.runActivityThreadPass(context.Background(), dayMs, false, at(day, 12, 10)); err != nil {
		t.Fatal(err)
	}
	var stored int
	if err := app.activity.store.db.QueryRow(`SELECT COUNT(*) FROM activity_threads WHERE title LIKE '%workers%'`).Scan(&stored); err != nil || stored != 0 {
		t.Fatalf("a title derived from a deleted moment is still stored (%d, %v)", stored, err)
	}
}

func TestThreadSureKeysSkipToolsAndSelf(t *testing.T) {
	self := threadSelfNames("Pat Example")
	keys := threadSureKeys(threadMoment{
		Apps: []string{"Isoform"},
		Entities: []Entity{
			{Type: entityProject, Name: "Isoform"}, {Type: entityPerson, Name: "Pat Example"},
			{Type: entityTopic, Name: "https://chatgpt.com/c/abc"}, {Type: entityTopic, Name: "https://eney.ai/use-cases"},
			{Type: entityRepo, Name: "OpenBCI/OpenBCI_GUI"}, {Type: entityPerson, Name: "Maya Chen"},
		},
	}, self)
	want := map[string]bool{"site:eney.ai": true, "obj:openbci_gui": true, "person:maya chen": true}
	if len(keys) != len(want) {
		t.Fatalf("keys = %v", keys)
	}
	for k := range want {
		if !keys[k] {
			t.Fatalf("missing %s in %v", k, keys)
		}
	}
}
