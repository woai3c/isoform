package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/interests"
	"wails-gui/internal/pii"
)

var topicRegistryTestNow = time.Date(2026, 10, 21, 12, 0, 0, 0, time.Local)

func TestTopicActivityHousekeeping(t *testing.T) {
	for _, test := range []struct {
		text string
		want bool
	}{
		{"Emptied the Trash", true},
		{"Moving files in Finder", true},
		{"Renamed a file in Finder", true},
		{"Closed a window", true},
		{"Changed system settings", true},
		{"Moved the launch date to Friday", false},
		{"Closed the deal with Priya", false},
		{"Settings for the launch are ready", false},
	} {
		if got := topicActivityHousekeeping(test.text); got != test.want {
			t.Errorf("topicActivityHousekeeping(%q) = %t, want %t", test.text, got, test.want)
		}
	}
}

func TestTopicLatestSkipsHousekeeping(t *testing.T) {
	meaningful := topicRegistryTestActivity("meaningful", "Halcyon", topicRegistryTestNow.Add(-time.Hour))
	meaningful.LatestChange = "Priya sent the redlines."
	chore := topicRegistryTestActivity("chore", "Halcyon", topicRegistryTestNow)
	chore.LatestChange = "Emptied the Trash"
	build := topicProjectBuild{
		project:    topicProject{ID: "p", Name: "Halcyon"},
		activities: []topicActivityObservation{{Row: chore}, {Row: meaningful}},
	}
	view := topicProjectView(build, nil, topicRegistryData{now: topicRegistryTestNow})
	if view.Latest == nil || view.Latest.Text != meaningful.LatestChange {
		t.Fatalf("latest = %+v, want meaningful activity", view.Latest)
	}
	build.activities = build.activities[:1]
	build.goals = []topicGoalObservation{topicRegistryTestGoal("goal", "Finish the launch", PurposeStateActive, topicRegistryTestNow)}
	view = topicProjectView(build, nil, topicRegistryData{now: topicRegistryTestNow})
	if view.Latest == nil || view.Latest.Text != "Finish the launch" {
		t.Fatalf("latest fallback = %+v", view.Latest)
	}
}

func configureTopicRegistryTest(app *App, now time.Time) {
	svc := app.topicsSvc()
	svc.now = func() time.Time { return now }
	svc.collect = func(at time.Time) topicInput { return topicInput{Now: at} }
	app.interestsSvc().now = func() time.Time { return now }
	app.interestsSvc().readFilter = nil
}

func topicRegistryTestActivity(id, name string, at time.Time, entities ...Entity) ActivityRow {
	raw, _ := json.Marshal(entities)
	return ActivityRow{
		ID: id, DayStartMs: startOfDayLocal(at.UnixMilli()), Status: activityStatusIdle,
		Title: name, LatestChange: name + " latest", Entities: string(raw),
		RunIDs: "[]", ActiveMsByApp: `{"Code":1000}`,
		StartMs: at.Add(-30 * time.Minute).UnixMilli(), EndMs: at.UnixMilli(), UpdatedAt: at.UnixMilli(),
	}
}

func topicRegistryTestGoal(id, title, state string, at time.Time) topicGoalObservation {
	return topicGoalObservation{ID: id, Title: title, State: state, DesiredOutcome: title, At: at.UnixMilli()}
}

func topicRegistryTestFollowing(id, name, kind, why string, lastSeen time.Time) UserTopicsView {
	return UserTopicsView{Interests: true, Topics: []UserTopic{{
		ID: id, Label: name, Kind: kind, Why: why, LastSeenAt: lastSeen.UTC().Format(time.RFC3339), Sources: []string{},
	}}, Removed: []UserTopic{}}
}

func TestTopicRegistryKeyAcrossDaysKeepsOneProjectID(t *testing.T) {
	dayOne := topicRegistryTestActivity("day-one", "Isoform", topicRegistryTestNow.AddDate(0, 0, -1), Entity{Type: entityProject, Name: "Isoform"})
	first := buildTopicRegistry(topicRegistryData{activities: []ActivityRow{dayOne}, now: topicRegistryTestNow})
	if len(first.projects) != 1 {
		t.Fatalf("first projects = %+v", first.projects)
	}
	id := first.projects[0].ID
	dayTwo := topicRegistryTestActivity("day-two", "Isoform", topicRegistryTestNow, Entity{Type: entityProject, Name: "Isoform"})
	second := buildTopicRegistry(topicRegistryData{
		existing: first.projects, activities: []ActivityRow{dayTwo, dayOne}, now: topicRegistryTestNow,
	})
	if len(second.projects) != 1 || second.projects[0].ID != id {
		t.Fatalf("projects across days = %+v, want %q", second.projects, id)
	}
	if second.projects[0].CreatedAt != first.projects[0].CreatedAt || second.projects[0].LastSeenAt != topicRegistryTime(dayTwo.EndMs) {
		t.Fatalf("project timestamps = %+v", second.projects[0])
	}
	keys := second.keys[id]
	if len(keys.ThreadKeys) != 1 || keys.ThreadKeys[0] != "obj:isoform" {
		t.Fatalf("project keys = %+v", keys)
	}
}

func TestTopicRegistryGoalAndMatchingInterestAreOneProject(t *testing.T) {
	bridge, purposeStore, _ := mobileTestGoalBridge(t)
	t.Setenv(topicsStateDirEnvVar, t.TempDir())
	app := bridge.app
	now := topicRegistryTestNow
	configureTopicRegistryTest(app, now)
	input, err := userPurposeCreateInput(userPurposeCreate{Key: "test:registry", Origin: "test", Kind: PurposeKindGoal, Text: "Ship the phone companion"}, now.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	created, _, err := purposeStore.Create(input)
	if err != nil {
		t.Fatal(err)
	}
	goal, err := purposeStore.GetDetail(created.OpenLoop.ID)
	if err != nil {
		t.Fatal(err)
	}
	views, err := collectAndReadTopicViewsForTest(app)
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 || views[0].Kind != TopicKindProject || views[0].Name != goal.Current.Title {
		t.Fatalf("goal topics = %+v", views)
	}
	id := views[0].ID
	interest := testInterest("interest-goal", strings.ToUpper(goal.Current.Title), "A current goal")
	interest.Evidence[0].At = now.Add(-2 * time.Hour).UnixMilli()
	storeTestInterests(t, app, interest)
	views, err = collectAndReadTopicViewsForTest(app)
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 || views[0].ID != id || views[0].Kind != TopicKindProject {
		t.Fatalf("mapped interest created another topic: %+v", views)
	}
	keys, ok := app.topicRegistry().TopicKeys(id)
	if !ok || len(keys.GoalIDs) != 1 || keys.GoalIDs[0] != goal.OpenLoop.ID || keys.InterestID != "interest-goal" {
		t.Fatalf("mapped keys = %+v, %t", keys, ok)
	}
}

func TestTopicRegistrySidebarPinPersistsSeparately(t *testing.T) {
	t.Setenv(topicsStateDirEnvVar, t.TempDir())
	newApp := func() *App {
		app := &App{}
		configureTopicRegistryTest(app, topicRegistryTestNow)
		storeTestInterests(t, app, testInterest("i-hat", "wearable AI hats", "A current prototype"))
		return app
	}
	app := newApp()
	if err := app.PinTopicToSidebar("i-hat", true); err != nil {
		t.Fatal(err)
	}
	state := app.topicsSvc().store.load()
	if !state.SidebarPins["i-hat"] || state.Overrides["i-hat"].Pinned {
		t.Fatalf("sidebar pin reused Your topics state: %+v", state)
	}
	if _, err := app.topicsSvc().update(UserTopicUpdate{ID: "i-hat", Action: "pin"}); err != nil {
		t.Fatal(err)
	}
	if err := app.PinTopicToSidebar("i-hat", false); err != nil {
		t.Fatal(err)
	}
	state = app.topicsSvc().store.load()
	if state.SidebarPins["i-hat"] || !state.Overrides["i-hat"].Pinned {
		t.Fatalf("sidebar unpin changed Your topics pin: %+v", state)
	}
	if err := app.PinTopicToSidebar("i-hat", true); err != nil {
		t.Fatal(err)
	}

	restarted := newApp()
	sidebar, err := restarted.ListSidebarTopics()
	if err != nil || len(sidebar) != 1 || sidebar[0].ID != "i-hat" || !sidebar[0].PinnedToSidebar {
		t.Fatalf("sidebar after restart = %+v, %v", sidebar, err)
	}
	yourTopics, err := restarted.GetUserTopics()
	if err != nil || len(yourTopics.Topics) != 1 || !yourTopics.Topics[0].Pinned {
		t.Fatalf("Your topics pin after restart = %+v, %v", yourTopics, err)
	}
}

func TestYourTopicsUsesTheSidebarPin(t *testing.T) {
	t.Setenv(topicsStateDirEnvVar, t.TempDir())
	app := &App{}
	configureTopicRegistryTest(app, topicRegistryTestNow)
	storeTestInterests(t, app, testInterest("i-hat", "wearable AI hats", "A current prototype"))
	for _, pinned := range []bool{true, false} {
		if err := app.PinTopicToSidebar("i-hat", pinned); err != nil {
			t.Fatal(err)
		}
		view, err := app.GetUserTopics()
		if err != nil || len(view.Topics) != 1 || view.Topics[0].Pinned != pinned {
			t.Fatalf("your topics pinned = %+v, %v; want %t", view, err, pinned)
		}
	}
}

func TestTopicRegistryFollowingNeedsWhyButWorkAlwaysShows(t *testing.T) {
	t.Setenv(topicsStateDirEnvVar, t.TempDir())
	app := newThreadTestApp(t)
	configureTopicRegistryTest(app, topicRegistryTestNow)
	storeTestInterests(t, app, testInterest("i-no-why", "unexplained interest", ""))
	seedThreadMoment(t, app, threadFixtureMoment{id: "work", title: "Work", app: "Code",
		start: topicRegistryTestNow.Add(-time.Hour), end: topicRegistryTestNow.Add(-30 * time.Minute),
		entities: []Entity{{Type: entityProject, Name: "Work"}}})
	views, err := collectAndReadTopicViewsForTest(app)
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 || views[0].Name != "Work" || views[0].Kind != TopicKindProject {
		t.Fatalf("topics = %+v", views)
	}
}

func TestTopicRegistryClosedEyesReturnsFollowingOnly(t *testing.T) {
	t.Setenv(topicsStateDirEnvVar, t.TempDir())
	app := newThreadTestApp(t)
	configureTopicRegistryTest(app, topicRegistryTestNow)
	seedThreadMoment(t, app, threadFixtureMoment{
		id: "safe", title: "Atlas work", app: "Code", start: topicRegistryTestNow.Add(-time.Hour), end: topicRegistryTestNow.Add(-30 * time.Minute),
		entities: []Entity{{Type: entityProject, Name: "Atlas"}, {Type: entityPerson, Name: "Ana Lopez"}},
	})
	views, err := collectAndReadTopicViewsForTest(app)
	if err != nil || len(views) != 1 || views[0].Kind != TopicKindProject {
		t.Fatalf("before Closed Eyes = %+v, %v", views, err)
	}
	storeTestInterests(t, app, testInterest("i-following", "public interest", "A followed interest"))
	app.closedEyes.enabled.Store(true)
	app.interestsSvc().readFilter = app.interestsReadFilter
	if got := app.interestsSvc().list(); len(got) != 0 {
		t.Fatal("control: production source filter must reject observed interests while paused")
	}
	views, err = collectAndReadTopicViewsForTest(app)
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 || views[0].ID != "i-following" || views[0].Kind != TopicKindFollowing {
		t.Fatalf("Closed Eyes topics = %+v", views)
	}
	if views[0].Latest != nil || views[0].Next != nil || views[0].PeopleLine != nil || views[0].SourceCounts != nil || views[0].Status != "" || views[0].Why != nil || views[0].NewStories != 0 {
		t.Fatalf("Closed Eyes returned activity-derived fields: %+v", views[0])
	}
	if _, ok := app.topicRegistry().TopicByID(views[0].ID); !ok {
		t.Fatal("the following topic is not available to the Go registry")
	}
}

func TestTopicRegistryOrdering(t *testing.T) {
	result := buildTopicRegistry(topicRegistryData{
		now: topicRegistryTestNow,
		activities: []ActivityRow{
			topicRegistryTestActivity("today", "Today project", topicRegistryTestNow, Entity{Type: entityProject, Name: "Today project"}),
			topicRegistryTestActivity("moving", "Moving project", topicRegistryTestNow.Add(-48*time.Hour), Entity{Type: entityProject, Name: "Moving project"}),
			topicRegistryTestActivity("old", "Old project", topicRegistryTestNow.Add(-96*time.Hour), Entity{Type: entityProject, Name: "Old project"}),
		},
		goals: []topicGoalObservation{{
			ID: "waiting", Title: "Waiting goal", State: PurposeStateWaiting, DesiredOutcome: "Waiting goal",
			WaitingCondition: "Review the draft", At: topicRegistryTestNow.UnixMilli(),
		}},
		following: topicRegistryTestFollowing("followed", "Followed", string(interests.KindResearch), "A reason", topicRegistryTestNow.Add(-96*time.Hour)),
		finds:     []TopicFind{{ID: "find", TopicID: "followed", Title: "New release", PublishedAt: topicRegistryTestNow.Format("2006-01-02")}},
	})
	if len(result.views) != 5 {
		t.Fatalf("views = %+v", result.views)
	}
	got := []string{}
	for _, view := range result.views {
		got = append(got, view.Name+":"+view.Status)
	}
	want := "Waiting goal:waiting_on_you|Today project:today|Followed:new_stories|Moving project:moving|Old project:"
	if strings.Join(got, "|") != want {
		t.Fatalf("order = %v, want %s", got, want)
	}
}

func TestTopicRegistryOutcomeAndPeopleFields(t *testing.T) {
	row := topicRegistryTestActivity("outcome", "Atlas", topicRegistryTestNow,
		Entity{Type: entityProject, Name: "Atlas"},
		Entity{Type: entityPerson, Name: "Dana"},
		Entity{Type: entityPerson, Name: "Ana"},
		Entity{Type: entityPerson, Name: "Ben"},
		Entity{Type: entityPerson, Name: "Chen"},
		Entity{Type: entityPerson, Name: "Pat Example"},
	)
	result := buildTopicRegistry(topicRegistryData{
		activities: []ActivityRow{row}, now: topicRegistryTestNow, selfName: "Pat Example",
		outcome: func(ActivityRow) *ActivityThreadOutcome {
			return &ActivityThreadOutcome{Kind: "document", State: "draft", Summary: "Draft reviewed", NextStep: "Ship the review"}
		},
	})
	if len(result.views) != 1 {
		t.Fatalf("views = %+v", result.views)
	}
	view := result.views[0]
	if view.Status != TopicStatusWaitingOnYou || view.Latest == nil || view.Latest.Text != "Draft reviewed" || view.Next == nil || view.Next.Text != "Ship the review" {
		t.Fatalf("outcome fields = %+v", view)
	}
	if view.PeopleLine == nil || view.PeopleLine.Count != 4 || len(view.PeopleLine.Names) != 3 {
		t.Fatalf("people line = %+v", view.PeopleLine)
	}
	if view.SourceCounts["activity"] != 1 {
		t.Fatalf("source counts = %+v", view.SourceCounts)
	}
}

func TestTopicRegistryMeetingToday(t *testing.T) {
	result := buildTopicRegistry(topicRegistryData{
		existing: []topicProject{{ID: "project-isoform", Name: "Isoform", MemberKeys: []string{"obj:isoform"}}},
		meetings: []topicMeetingObservation{{
			ID: "meeting", Title: "Isoform sync", StartAt: topicRegistryTestNow.Add(time.Hour).UnixMilli(),
			EndAt: topicRegistryTestNow.Add(2 * time.Hour).UnixMilli(), People: []string{"Ana", "Pat Example"},
		}},
		now: topicRegistryTestNow, selfName: "Pat Example",
	})
	if len(result.views) != 1 {
		t.Fatalf("views = %+v", result.views)
	}
	view := result.views[0]
	if view.Status != TopicStatusToday || view.Next == nil || view.Next.Text != "Isoform sync" || view.SourceCounts["meeting"] != 1 {
		t.Fatalf("meeting fields = %+v", view)
	}
	if view.PeopleLine == nil || view.PeopleLine.Count != 1 || view.PeopleLine.Names[0] != "Ana" {
		t.Fatalf("meeting people = %+v", view.PeopleLine)
	}
}

func TestTopicRegistryTodayLimitAndGoLookup(t *testing.T) {
	t.Setenv(topicsStateDirEnvVar, t.TempDir())
	app := newThreadTestApp(t)
	configureTopicRegistryTest(app, topicRegistryTestNow)
	for i := 0; i < topicRegistryLimit+1; i++ {
		name := fmt.Sprintf("Project%02d", i)
		seedThreadMoment(t, app, threadFixtureMoment{id: fmt.Sprintf("project-%02d", i), title: name, app: "Code",
			start: topicRegistryTestNow.Add(-time.Hour), end: topicRegistryTestNow.Add(-30 * time.Minute),
			entities: []Entity{{Type: entityProject, Name: name}}})
	}
	views, err := collectAndReadTopicViewsForTest(app)
	if err != nil || len(views) != topicRegistryLimit {
		t.Fatalf("Today topics = %d, %v", len(views), err)
	}
	view, ok := app.topicRegistry().TopicByID(topicProjectID("obj:project12"))
	if !ok || view.Name != "Project12" {
		t.Fatalf("lookup beyond Today limit = %+v, %t", view, ok)
	}
}

func TestTopicRegistrySensitiveSourcesNeverCreateProjects(t *testing.T) {
	t.Setenv(topicsStateDirEnvVar, t.TempDir())
	app := newThreadTestApp(t)
	configureTopicRegistryTest(app, topicRegistryTestNow)
	for _, fixture := range []threadFixtureMoment{
		{id: "safe", title: "Safe project", app: "Code", bundle: "com.microsoft.VSCode", entities: []Entity{{Type: entityProject, Name: "Safe project"}}},
		{id: "sensitive", title: "Secret project", app: "Private Notes", bundle: "com.example.private", entities: []Entity{{Type: entityProject, Name: "Secret project"}}},
		{id: "private", title: "Browser secret - Incognito", app: "Chrome", bundle: "com.google.Chrome", entities: []Entity{{Type: entityProject, Name: "Browser secret"}}},
	} {
		fixture.start = topicRegistryTestNow.Add(-time.Hour)
		fixture.end = topicRegistryTestNow.Add(-30 * time.Minute)
		seedThreadMoment(t, app, fixture)
	}
	views, err := collectAndReadTopicViewsForTest(app)
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 || views[0].Name != "Safe project" {
		t.Fatalf("privacy-filtered topics = %+v", views)
	}
	keys, ok := app.topicRegistry().TopicKeys(views[0].ID)
	if !ok || len(keys.ThreadKeys) != 1 || keys.ThreadKeys[0] != "obj:safe" {
		t.Fatalf("privacy-filtered keys = %+v, %t", keys, ok)
	}
}

func TestTopicRegistryReloadKeepsProjectIDs(t *testing.T) {
	t.Setenv(topicsStateDirEnvVar, t.TempDir())
	app := newThreadTestApp(t)
	configureTopicRegistryTest(app, topicRegistryTestNow)
	seedThreadMoment(t, app, threadFixtureMoment{
		id: "atlas", title: "Atlas", app: "Code", start: topicRegistryTestNow.Add(-time.Hour), end: topicRegistryTestNow.Add(-30 * time.Minute),
		entities: []Entity{{Type: entityProject, Name: "Atlas"}},
	})
	before, err := collectAndReadTopicViewsForTest(app)
	if err != nil || len(before) != 1 {
		t.Fatalf("before reload = %+v, %v", before, err)
	}
	restarted := &App{store: app.store, activity: app.activity, cachedDisplayName: "Pat Example"}
	configureTopicRegistryTest(restarted, topicRegistryTestNow)
	after, err := collectAndReadTopicViewsForTest(restarted)
	if err != nil || len(after) != 1 || after[0].ID != before[0].ID {
		t.Fatalf("after reload = %+v, %v; before = %+v", after, err, before)
	}
	stored := restarted.topicsSvc().store.load()
	if len(stored.Projects) != 1 || stored.Projects[0].ID != before[0].ID || stored.Projects[0].CreatedAt == "" || stored.Projects[0].LastSeenAt == "" {
		t.Fatalf("stored project = %+v", stored.Projects)
	}
}

func TestTopicRegistryGoalSharesActivityKeys(t *testing.T) {
	row := topicRegistryTestActivity("work", "Atlas", topicRegistryTestNow, Entity{Type: entityProject, Name: "Atlas"})
	result := buildTopicRegistry(topicRegistryData{now: topicRegistryTestNow, activities: []ActivityRow{row}, goals: []topicGoalObservation{{
		ID: "goal", Title: "Ship Atlas", State: PurposeStateWaiting, ActivityIDs: []string{row.ID},
	}}})
	if len(result.projects) != 1 || len(result.projects[0].GoalIDs) != 1 || result.views[0].Status != TopicStatusWaitingOnYou {
		t.Fatalf("linked goal = %+v", result)
	}
}

func TestTopicRegistryJoinedProjectsKeepAliasesAndPins(t *testing.T) {
	first := buildTopicRegistry(topicRegistryData{now: topicRegistryTestNow, activities: []ActivityRow{
		topicRegistryTestActivity("atlas", "Atlas", topicRegistryTestNow, Entity{Type: entityProject, Name: "Atlas"}),
		topicRegistryTestActivity("beacon", "Beacon", topicRegistryTestNow, Entity{Type: entityChannel, Name: "beacon"}),
	}})
	oldID := first.projects[1].ID
	joined := buildTopicRegistry(topicRegistryData{now: topicRegistryTestNow, existing: first.projects, sidebarPins: map[string]bool{oldID: true}, activities: []ActivityRow{
		topicRegistryTestActivity("bridge", "Atlas", topicRegistryTestNow, Entity{Type: entityProject, Name: "Atlas"}, Entity{Type: entityChannel, Name: "beacon"}),
	}})
	if len(joined.projects) != 1 || !joined.views[0].PinnedToSidebar {
		t.Fatalf("joined = %+v", joined)
	}
	for _, project := range first.projects {
		if _, ok := joined.keys[project.ID]; !ok {
			t.Fatalf("lost durable id %s", project.ID)
		}
	}
}

func TestTopicRegistryOlderOpenOutcomeStillWaits(t *testing.T) {
	result := buildTopicRegistry(topicRegistryData{now: topicRegistryTestNow, activities: []ActivityRow{
		topicRegistryTestActivity("old", "Atlas", topicRegistryTestNow.Add(-time.Hour), Entity{Type: entityProject, Name: "Atlas"}),
		topicRegistryTestActivity("new", "Atlas", topicRegistryTestNow, Entity{Type: entityProject, Name: "Atlas"}),
	}, outcome: func(row ActivityRow) *ActivityThreadOutcome {
		if row.ID == "old" {
			return &ActivityThreadOutcome{Kind: "decision", Question: "Choose a release date"}
		}
		return nil
	}})
	if len(result.views) != 1 || result.views[0].Status != TopicStatusWaitingOnYou {
		t.Fatalf("views = %+v", result.views)
	}
}

func TestTopicRegistryFollowingStatusAndFindRecency(t *testing.T) {
	in := topicRegistryData{now: topicRegistryTestNow, following: topicRegistryTestFollowing("follow", "Research", "research", "Current interest", topicRegistryTestNow)}
	for _, closed := range []bool{false, true} {
		in.closedEyes = closed
		result := buildTopicRegistry(in)
		if len(result.views) != 1 || result.views[0].Status != "" {
			t.Fatalf("activity status on following: %+v", result.views)
		}
	}
	in.finds = []TopicFind{
		{ID: "old", TopicID: "follow", Title: "Old", PublishedAt: topicRegistryTestNow.AddDate(0, 0, -1).Format("2006-01-02")},
		{ID: "opened", TopicID: "follow", Title: "Read", Opened: true},
		{ID: "new", TopicID: "follow", Title: "New", PublishedAt: topicRegistryTestNow.Format("2006-01-02")},
	}
	result := buildTopicRegistry(in)
	if result.views[0].Status != TopicStatusNewStories || result.views[0].Latest.Text != "New" || result.views[0].SourceCounts["find"] != 1 {
		t.Fatalf("find recency = %+v", result.views)
	}
}

func TestTopicRegistryWithdrawnSourceHidesPersistedProjectEverywhere(t *testing.T) {
	t.Setenv(topicsStateDirEnvVar, t.TempDir())
	app := newThreadTestApp(t)
	configureTopicRegistryTest(app, topicRegistryTestNow)
	seedThreadMoment(t, app, threadFixtureMoment{id: "work", title: "Atlas", app: "Code", start: topicRegistryTestNow.Add(-time.Hour), end: topicRegistryTestNow.Add(-30 * time.Minute), entities: []Entity{{Type: entityProject, Name: "Atlas"}}})
	views, err := collectAndReadTopicViewsForTest(app)
	if err != nil || len(views) != 1 {
		t.Fatalf("initial = %+v, %v", views, err)
	}
	id := views[0].ID
	if err := app.PinTopicToSidebar(id, true); err != nil {
		t.Fatal(err)
	}
	if _, err := app.activity.store.db.Exec(`UPDATE activity_sessions SET triage_status='protected' WHERE id='session-work'`); err != nil {
		t.Fatal(err)
	}
	assertHidden := func(app *App) {
		t.Helper()
		views, err := collectAndReadTopicViewsForTest(app)
		if err != nil || len(views) != 0 {
			t.Fatalf("withdrawn = %+v, %v", views, err)
		}
		sidebar, err := app.ListSidebarTopics()
		if err != nil || len(sidebar) != 0 {
			t.Fatalf("sidebar = %+v, %v", sidebar, err)
		}
		if _, ok := app.topicRegistry().TopicByID(id); ok {
			t.Fatal("by-id bypassed withdrawal")
		}
		if _, ok := app.topicRegistry().TopicKeys(id); ok {
			t.Fatal("keys bypassed withdrawal")
		}
	}
	assertHidden(app)
	raw, err := os.ReadFile(app.topicsSvc().store.path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "Atlas") || strings.Contains(string(raw), "obj:atlas") || strings.Contains(string(raw), "session-work") {
		t.Fatalf("withdrawn text retained: %s", raw)
	}
	restarted := &App{store: app.store, activity: app.activity, cachedDisplayName: "Pat Example"}
	configureTopicRegistryTest(restarted, topicRegistryTestNow)
	assertHidden(restarted)
	if _, err := app.activity.store.db.Exec(`UPDATE activity_sessions SET triage_status='done' WHERE id='session-work'`); err != nil {
		t.Fatal(err)
	}
	views, err = collectAndReadTopicViewsForTest(restarted)
	if err != nil || len(views) != 1 || views[0].ID == id || views[0].PinnedToSidebar {
		t.Fatalf("recovery = %+v, %v", views, err)
	}
}

func TestTopicRegistryPinValidationAndWriteFailure(t *testing.T) {
	app := newTopicFindsTestApp(t)
	configureTopicRegistryTest(app, topicRegistryTestNow)
	app.topicsSvc().store.path = ""
	if err := app.PinTopicToSidebar("", true); err != errTopicIDRequired {
		t.Fatalf("empty id = %v", err)
	}
	if err := app.PinTopicToSidebar("missing", true); err != errTopicNotFound {
		t.Fatalf("unknown id = %v", err)
	}
	if err := app.PinTopicToSidebar("missing", false); err == nil {
		t.Fatal("write failure hidden")
	}
}

func TestTopicRegistryWorkIdentityNeverEntersSearchRanking(t *testing.T) {
	t.Setenv(topicsStateDirEnvVar, t.TempDir())
	app := newThreadTestApp(t)
	configureTopicRegistryTest(app, topicRegistryTestNow)
	seedThreadMoment(t, app, threadFixtureMoment{id: "local", title: "Internal Atlas", app: "Code", start: topicRegistryTestNow.Add(-time.Hour), end: topicRegistryTestNow.Add(-30 * time.Minute), entities: []Entity{{Type: entityRepo, Name: "internal/atlas"}}})
	views, err := collectAndReadTopicViewsForTest(app)
	if err != nil || len(views) != 1 {
		t.Fatalf("work topic = %+v, %v", views, err)
	}
	following, err := app.GetUserTopics()
	if err != nil || len(following.Topics) != 0 {
		t.Fatalf("local work entered following/search input: %+v, %v", following, err)
	}
}

func TestTopicRegistryFindsRequireOriginalReadableSearchSources(t *testing.T) {
	app := newTopicFindsTestApp(t)
	configureTopicRegistryTest(app, topicRegistryTestNow)
	state := topicsStateFile{Finds: topicFindsState{RanOn: topicRegistryTestNow.Format("2006-01-02"), Items: []TopicFind{{ID: "find", TopicID: "interest", Title: "Public release"}}}}
	if got := app.topicsSvc().registryFinds(state, topicRegistryTestNow); len(got) != 0 {
		t.Fatal("find without lineage disclosed")
	}
	state.Finds.Sent = []topicSent{{ID: "interest", ReadSources: &topicSentReadSources{Version: 1, UserNamed: true}}}
	if got := app.topicsSvc().registryFinds(state, topicRegistryTestNow); len(got) != 1 {
		t.Fatal("user-named find hidden")
	}
	state.Finds.Sent = append(state.Finds.Sent, topicSent{ID: "other", ReadSources: &topicSentReadSources{Version: 1, Evidence: []interests.Ref{{Source: interests.SourceActivity, Key: "activity:missing"}}}})
	if got := app.topicsSvc().registryFinds(state, topicRegistryTestNow); len(got) != 0 {
		t.Fatal("find with withdrawn co-contributor disclosed")
	}
}

func TestTopicRegistryRealActivityFollowUpJoinsWorkProject(t *testing.T) {
	t.Setenv(topicsStateDirEnvVar, t.TempDir())
	app := newThreadTestApp(t)
	now := time.Now()
	configureTopicRegistryTest(app, now)
	seedThreadMoment(t, app, threadFixtureMoment{id: "work", title: "Atlas", app: "Code", start: now.Add(-time.Hour), end: now.Add(-30 * time.Minute), entities: []Entity{{Type: entityProject, Name: "Atlas"}}})
	before, err := collectAndReadTopicViewsForTest(app)
	if err != nil || len(before) != 1 {
		t.Fatalf("before = %+v, %v", before, err)
	}
	followUp, err := app.addActivityFollowUp("work", "Review the Atlas release", "registry-followup", now.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	after, err := collectAndReadTopicViewsForTest(app)
	if err != nil || len(after) != 1 || after[0].ID != before[0].ID {
		t.Fatalf("follow-up = %+v, %v", after, err)
	}
	keys, ok := app.topicRegistry().TopicKeys(after[0].ID)
	if !ok || !topicStringIn(keys.GoalIDs, followUp.OpenLoopID) {
		t.Fatalf("follow-up missing from work keys: %+v", keys)
	}
}

func TestTopicRegistryGoalsWithoutWorkKeysRemainSeparate(t *testing.T) {
	row := topicRegistryTestActivity("shared", "General work", topicRegistryTestNow)
	result := buildTopicRegistry(topicRegistryData{now: topicRegistryTestNow, activities: []ActivityRow{row}, goals: []topicGoalObservation{
		{ID: "one", Title: "First goal", ActivityIDs: []string{row.ID}},
		{ID: "two", Title: "Second goal", ActivityIDs: []string{row.ID}},
	}})
	if len(result.projects) != 2 {
		t.Fatalf("goals merged without a work key: %+v", result.projects)
	}
}

func TestTopicRegistryReadableRecurrenceSurvivesWithdrawnHistory(t *testing.T) {
	t.Setenv(topicsStateDirEnvVar, t.TempDir())
	app := newThreadTestApp(t)
	configureTopicRegistryTest(app, topicRegistryTestNow)
	seedThreadMoment(t, app, threadFixtureMoment{id: "old", title: "Old private label", app: "Code", start: topicRegistryTestNow.Add(-48 * time.Hour), end: topicRegistryTestNow.Add(-47 * time.Hour), entities: []Entity{{Type: entityProject, Name: "Atlas"}}})
	seedThreadMoment(t, app, threadFixtureMoment{id: "survivor", title: "Atlas", app: "Code", start: topicRegistryTestNow.Add(-24 * time.Hour), end: topicRegistryTestNow.Add(-23 * time.Hour), entities: []Entity{{Type: entityProject, Name: "Atlas"}}})
	before, err := collectAndReadTopicViewsForTest(app)
	if err != nil || len(before) != 1 {
		t.Fatalf("before = %+v, %v", before, err)
	}
	id := before[0].ID
	if err := app.PinTopicToSidebar(id, true); err != nil {
		t.Fatal(err)
	}
	if _, err := app.activity.store.db.Exec(`UPDATE activity_sessions SET triage_status='protected' WHERE id='session-old'`); err != nil {
		t.Fatal(err)
	}
	seedThreadMoment(t, app, threadFixtureMoment{id: "new", title: "Atlas public work", app: "Code", start: topicRegistryTestNow.Add(-time.Hour), end: topicRegistryTestNow.Add(-30 * time.Minute), entities: []Entity{{Type: entityProject, Name: "Atlas"}}})
	after, err := app.ListSidebarTopics()
	if err != nil || len(after) != 1 || after[0].ID != id || after[0].Name != "Atlas" || after[0].SourceCounts["activity"] != 2 {
		t.Fatalf("readable recurrence = %+v, %v", after, err)
	}
	keys, ok := app.topicRegistry().TopicKeys(id)
	if !ok || len(keys.ThreadKeys) != 1 || keys.ThreadKeys[0] != "obj:atlas" {
		t.Fatalf("keys = %+v", keys)
	}
	stored := app.topicsSvc().store.load().Projects
	if len(stored) != 1 || len(stored[0].ActivityIDs) != 2 || topicStringIn(stored[0].ActivityIDs, "old") {
		t.Fatalf("withdrawn history retained in readable membership: %+v", stored)
	}
}

func TestTopicRegistrySourceChangesBeforePublicationNeverPersist(t *testing.T) {
	for _, source := range []string{"activity", "calendar_permission", "topics_file"} {
		t.Run(source, func(t *testing.T) {
			t.Setenv(topicsStateDirEnvVar, t.TempDir())
			app := newThreadTestApp(t)
			configureTopicRegistryTest(app, topicRegistryTestNow)
			seedThreadMoment(t, app, threadFixtureMoment{id: "work", title: "Atlas", app: "Code", start: topicRegistryTestNow.Add(-time.Hour), end: topicRegistryTestNow.Add(-30 * time.Minute), entities: []Entity{{Type: entityProject, Name: "Atlas"}}})
			svc := app.topicsSvc()
			svc.registryBeforePublish = func() {
				if !app.closedEyes.mu.TryLock() {
					t.Fatal("collection held Closed Eyes lock")
				}
				app.closedEyes.mu.Unlock()
				var err error
				switch source {
				case "activity":
					_, err = app.activity.store.db.Exec(`UPDATE activity_sessions SET triage_status='protected' WHERE id='session-work'`)
				case "calendar_permission":
					err = app.store.SetUIState(context.Background(), permissionUIStateKey(calendarPermissionKey), "false")
				case "topics_file":
					_, err = svc.store.update(func(st *topicsStateFile) { st.SidebarPins = map[string]bool{"unrelated": true} })
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			views, err := collectAndReadTopicViewsForTest(app)
			if !errors.Is(err, errTopicRegistryChanged) || len(views) != 0 {
				t.Fatalf("stale result published: %+v, %v", views, err)
			}
			state := svc.store.load()
			if len(state.Projects) != 0 {
				t.Fatalf("stale identity persisted: %+v", state.Projects)
			}
			if source == "topics_file" && !state.SidebarPins["unrelated"] {
				t.Fatal("concurrent choice was lost")
			}
			svc.registryBeforePublish = nil
			if source != "activity" {
				views, err = collectAndReadTopicViewsForTest(app)
				if err != nil || len(views) != 1 {
					t.Fatalf("valid retry = %+v, %v", views, err)
				}
			}
		})
	}
}

func TestTopicRegistryDoesNotAdoptNewStateForAnOlderFollowingView(t *testing.T) {
	app := newTopicFindsTestApp(t)
	configureTopicRegistryTest(app, topicRegistryTestNow)
	item := testInterest("follow", "Research topic", "A current interest")
	storeTestInterests(t, app, item)
	svc := app.topicsSvc()
	svc.following = func(ctx context.Context, revision topicRegistryRevision) ([]interests.Interest, error) {
		if _, err := svc.store.update(func(st *topicsStateFile) {
			st.Overrides = map[string]topicOverride{item.ID: {Key: item.Key, Removed: true}}
		}); err != nil {
			t.Fatal(err)
		}
		return []interests.Interest{item}, ctx.Err()
	}
	views, err := collectAndReadTopicViewsForTest(app)
	if !errors.Is(err, errTopicRegistryChanged) || len(views) != 0 {
		t.Fatalf("removed following published from older snapshot: %+v, %v", views, err)
	}
}

func TestTopicRegistryFollowingWhyAndNewStoriesFields(t *testing.T) {
	following := topicRegistryTestFollowing("follow", "Research", "research", "A current research interest", topicRegistryTestNow)
	base := topicRegistryData{now: topicRegistryTestNow, following: following}
	result := buildTopicRegistry(base)
	if len(result.views) != 1 {
		t.Fatalf("following = %+v", result.views)
	}
	view := result.views[0]
	if view.Why == nil || view.Why.LastSeenAt != following.Topics[0].LastSeenAt || view.Why.InterestWhy != following.Topics[0].Why {
		t.Fatalf("why = %+v", view.Why)
	}
	if _, err := time.Parse(time.RFC3339, view.Why.LastSeenAt); err != nil {
		t.Fatal(err)
	}
	if view.NewStories != 0 {
		t.Fatalf("newStories without finds = %d", view.NewStories)
	}
	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"newStories"`) {
		t.Fatalf("zero count serialized: %s", raw)
	}

	base.finds = []TopicFind{
		{ID: "new-one", TopicID: "follow", Title: "First release", PublishedAt: topicRegistryTestNow.Format("2006-01-02")},
		{ID: "new-two", TopicID: "follow", Title: "Second release", PublishedAt: topicRegistryTestNow.Format("2006-01-02")},
		{ID: "old", TopicID: "follow", Title: "Old release", PublishedAt: topicRegistryTestNow.AddDate(0, 0, -1).Format("2006-01-02")},
		{ID: "opened", TopicID: "follow", Title: "Already read", Opened: true},
		{ID: "other", TopicID: "other", Title: "Unrelated release"},
	}
	result = buildTopicRegistry(base)
	view = result.views[0]
	if view.Status != TopicStatusNewStories || view.NewStories != 2 || view.NewStories != view.SourceCounts["find"] {
		t.Fatalf("new stories = %+v", view)
	}
	if view.LatestFind == nil || view.LatestFind.Text != "First release" {
		t.Fatalf("latest unread find = %+v", view.LatestFind)
	}

	base.finds = []TopicFind{{ID: "read", TopicID: "follow", Title: "Read headline", PublishedAt: topicRegistryTestNow.Format("2006-01-02"), Opened: true}}
	view = buildTopicRegistry(base).views[0]
	if view.LatestFind == nil || view.LatestFind.Text != "Read headline" || view.NewStories != 0 {
		t.Fatalf("opened latest headline or unread count = %+v", view)
	}
	base.finds = []TopicFind{
		{ID: "new-one", TopicID: "follow", Title: "First release", PublishedAt: topicRegistryTestNow.Format("2006-01-02")},
		{ID: "new-two", TopicID: "follow", Title: "Second release", PublishedAt: topicRegistryTestNow.Format("2006-01-02")},
	}
	base.closedEyes = true
	view = buildTopicRegistry(base).views[0]
	if view.Why != nil || view.NewStories != 2 {
		t.Fatalf("Closed Eyes projection changed: %+v", view)
	}

	base.closedEyes = false
	base.following.Topics[0].Why = "  "
	if result := buildTopicRegistry(base); len(result.views) != 0 {
		t.Fatalf("following without a reason got a card: %+v", result.views)
	}
}

func TestTopicRegistryWorkNeverGetsFollowingWhyOrStoryCount(t *testing.T) {
	result := buildTopicRegistry(topicRegistryData{now: topicRegistryTestNow,
		activities: []ActivityRow{topicRegistryTestActivity("work", "Atlas", topicRegistryTestNow, Entity{Type: entityProject, Name: "Atlas"})},
		following:  topicRegistryTestFollowing("interest", "Atlas", "project", "A current project", topicRegistryTestNow),
		finds:      []TopicFind{{ID: "find", TopicID: "interest", Title: "Release", PublishedAt: topicRegistryTestNow.Format("2006-01-02")}},
	})
	if len(result.views) != 1 || result.views[0].Kind != TopicKindProject || result.views[0].Why != nil || result.views[0].NewStories != 0 {
		t.Fatalf("work acquired following fields: %+v", result.views)
	}
	if result.views[0].LatestFind == nil || result.views[0].LatestFind.Text != "Release" {
		t.Fatalf("work latest find = %+v", result.views[0].LatestFind)
	}
	raw, err := json.Marshal(result.views[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"why"`) || strings.Contains(string(raw), `"newStories"`) {
		t.Fatalf("work serialized following fields: %s", raw)
	}
}

func TestTopicRegistryForgottenSourceErasesDiskAndCannotClaimNewWork(t *testing.T) {
	t.Setenv(topicsStateDirEnvVar, t.TempDir())
	app := newThreadTestApp(t)
	configureTopicRegistryTest(app, topicRegistryTestNow)
	seed := func(id string) {
		seedThreadMoment(t, app, threadFixtureMoment{id: id, title: "Atlas", app: "Code", start: topicRegistryTestNow.Add(-time.Hour), end: topicRegistryTestNow.Add(-30 * time.Minute), entities: []Entity{{Type: entityProject, Name: "Atlas"}}})
	}
	seed("forgotten")
	before, err := collectAndReadTopicViewsForTest(app)
	if err != nil || len(before) != 1 {
		t.Fatalf("before: %+v %v", before, err)
	}
	oldID := before[0].ID
	if err := app.PinTopicToSidebar(oldID, true); err != nil {
		t.Fatal(err)
	}
	if err := app.activity.store.DeleteSession("session-forgotten"); err != nil {
		t.Fatal(err)
	}
	if views, err := collectAndReadTopicViewsForTest(app); err != nil || len(views) != 0 {
		t.Fatalf("forgotten: %+v %v", views, err)
	}
	raw, err := os.ReadFile(app.topicsSvc().store.path)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"Atlas", "obj:atlas", "forgotten"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("forgotten identity retained: %s", raw)
		}
	}
	seed("new")
	after, err := collectAndReadTopicViewsForTest(app)
	if err != nil || len(after) != 1 || after[0].ID == oldID || after[0].PinnedToSidebar {
		t.Fatalf("new work absorbed: %+v %v", after, err)
	}
	state := app.topicsSvc().store.load()
	for _, p := range state.Projects {
		if p.ID == oldID && (p.Name != "" || len(p.MemberKeys)+len(p.GoalIDs)+len(p.ActivityIDs) != 0 || p.CreatedAt != "" || p.LastSeenAt != "") {
			t.Fatalf("nonempty tombstone: %+v", p)
		}
	}
}

func TestTopicRegistryHiddenSourceCannotClaimNewWorkOnFirstLoad(t *testing.T) {
	t.Setenv(topicsStateDirEnvVar, t.TempDir())
	app := newThreadTestApp(t)
	configureTopicRegistryTest(app, topicRegistryTestNow)
	oldID := ""
	for _, id := range []string{"old", "new"} {
		if id == "new" {
			if _, err := app.activity.store.db.Exec(`UPDATE activity_sessions SET triage_status='protected' WHERE id='session-old'`); err != nil {
				t.Fatal(err)
			}
		}
		seedThreadMoment(t, app, threadFixtureMoment{id: id, title: "Atlas", app: "Code", start: topicRegistryTestNow.Add(-time.Hour), end: topicRegistryTestNow.Add(-30 * time.Minute), entities: []Entity{{Type: entityProject, Name: "Atlas"}}})
		views, err := collectAndReadTopicViewsForTest(app)
		if err != nil || len(views) != 1 || views[0].SourceCounts["activity"] != 1 {
			t.Fatalf("%s work hidden: %+v %v", id, views, err)
		}
		if id == "old" {
			oldID = views[0].ID
		} else if views[0].ID == oldID {
			t.Fatal("hidden identity claimed new work")
		}
	}
}

func TestTopicRegistryCollectionReleasesPrivacyAndMemoryLocks(t *testing.T) {
	t.Setenv(topicsStateDirEnvVar, t.TempDir())
	app := newThreadTestApp(t)
	configureTopicRegistryTest(app, topicRegistryTestNow)
	seedThreadMoment(t, app, threadFixtureMoment{id: "work", title: "Atlas", app: "Code", start: topicRegistryTestNow.Add(-time.Hour), end: topicRegistryTestNow.Add(-30 * time.Minute), entities: []Entity{{Type: entityProject, Name: "Atlas"}}})
	svc := app.topicsSvc()
	svc.registryDuringCollect = func() {
		for _, lock := range []struct {
			name   string
			try    func() bool
			unlock func()
		}{
			{"memory mutation", memoryMutationMu.TryLock, memoryMutationMu.Unlock},
			{"memory publication", memoryFileLocks.publication.TryLock, memoryFileLocks.publication.Unlock},
			{"Closed Eyes", app.closedEyes.mu.TryLock, app.closedEyes.mu.Unlock},
			{"sensitive apps", pii.TryLockSensitiveAppsForPublication, pii.UnlockSensitiveAppsForPublication},
			{"voice input apps", userVoiceInputAppsPublicationMu.TryLock, userVoiceInputAppsPublicationMu.Unlock},
			{"interests", app.interestsSvc().store.publication.TryLock, app.interestsSvc().store.publication.Unlock},
		} {
			if !lock.try() {
				t.Fatalf("collection holds %s", lock.name)
			}
			lock.unlock()
		}
		app.closedEyes.mu.Lock()
		app.closedEyes.enabled.Store(true)
		app.closedEyes.mu.Unlock()
	}
	views, err := collectAndReadTopicViewsForTest(app)
	if !errors.Is(err, errTopicRegistryChanged) || len(views) != 0 || len(svc.store.load().Projects) != 0 {
		t.Fatalf("policy mutation published: %+v %v", views, err)
	}
}

func TestTopicRegistryActivityBudgetTruncatesWithoutLosingSurface(t *testing.T) {
	t.Setenv(topicsStateDirEnvVar, t.TempDir())
	app := newThreadTestApp(t)
	configureTopicRegistryTest(app, topicRegistryTestNow)
	seedThreadMoment(t, app, threadFixtureMoment{id: "work", title: "Atlas", app: "Code", start: topicRegistryTestNow.Add(-time.Hour), end: topicRegistryTestNow.Add(-30 * time.Minute), entities: []Entity{{Type: entityProject, Name: "Atlas"}}})
	if _, err := collectAndReadTopicViewsForTest(app); err != nil {
		t.Fatal(err)
	}
	old := app.topicsSvc().store.load().Projects[0].ID
	if err := app.PinTopicToSidebar(old, true); err != nil {
		t.Fatal(err)
	}
	tx, err := app.activity.store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := 0; i < topicRegistryActivityLimit; i++ {
		if _, err := tx.Exec(`INSERT INTO activities(id,day_start_ms,start_ms,end_ms,created_at,updated_at) VALUES(?,?,?,?,?,?)`, fmt.Sprintf("extra-%d", i), startOfDayLocal(topicRegistryTestNow.UnixMilli()), topicRegistryTestNow.Add(-time.Hour).UnixMilli(), topicRegistryTestNow.UnixMilli(), topicRegistryTestNow.UnixMilli(), topicRegistryTestNow.UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	views, err := collectAndReadTopicViewsForTest(app)
	if err != nil || len(views) != 1 || views[0].ID != old || !views[0].Truncated {
		t.Fatalf("heavy surface: %+v %v", views, err)
	}
	sidebar, err := app.ListSidebarTopics()
	if err != nil || len(sidebar) != 1 || sidebar[0].ID != old {
		t.Fatalf("heavy sidebar: %+v %v", sidebar, err)
	}
}

func TestTopicRegistryWaitingForMemoryDoesNotHoldClosedEyes(t *testing.T) {
	app := newTodayContextTestApp(t, false)
	t.Setenv(topicsStateDirEnvVar, t.TempDir())
	configureTopicRegistryTest(app, topicRegistryTestNow)
	memoryMutationMu.Lock()
	defer memoryMutationMu.Unlock()
	done := make(chan error, 1)
	go func() { _, err := collectAndReadTopicViewsForTest(app); done <- err }()
	// The complete acquisition times out with every partially acquired barrier
	// released, including Closed Eyes. A queued memory writer never owns it.
	select {
	case err := <-done:
		if !errors.Is(err, errTopicRegistryChanged) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("snapshot acquisition did not honor its bound")
	}
	if !app.closedEyes.mu.TryLock() {
		t.Fatal("Closed Eyes left locked")
	}
	app.closedEyes.mu.Unlock()
}

func TestTopicRegistryClosedEyesBeforeSnapshotDoesNotRetireProjects(t *testing.T) {
	t.Setenv(topicsStateDirEnvVar, t.TempDir())
	a := newThreadTestApp(t)
	configureTopicRegistryTest(a, topicRegistryTestNow)
	seedThreadMoment(t, a, threadFixtureMoment{id: "work", title: "Atlas", app: "Code", start: topicRegistryTestNow.Add(-time.Hour), end: topicRegistryTestNow.Add(-30 * time.Minute), entities: []Entity{{Type: entityProject, Name: "Atlas"}}})
	if _, err := collectAndReadTopicViewsForTest(a); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(a.topicsSvc().store.path)
	if err != nil {
		t.Fatal(err)
	}
	a.topicsSvc().registryBeforeSnapshot = func() { a.closedEyes.mu.Lock(); a.closedEyes.enabled.Store(true); a.closedEyes.mu.Unlock() }
	if views, err := collectAndReadTopicViewsForTest(a); err != nil || len(views) != 0 {
		t.Fatalf("paused result: %+v %v", views, err)
	}
	after, err := os.ReadFile(a.topicsSvc().store.path)
	if err != nil || string(before) != string(after) {
		t.Fatalf("pause destructively retired registry: %v", err)
	}
}
