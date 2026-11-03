package main

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestTopicNamesWithholdPrivacyMarkersAndMainChat(t *testing.T) {
	for _, name := range []string{
		"Safari sk-proj-" + strings.Repeat("a", 48),
		"~/src/sk-proj-" + strings.Repeat("a", 48) + ".md",
		"Safari [REDACTED:account_number]", "[REDACTED", "[redacted:code] notes",
		"Safari [masked] notes", "Safari [...] notes", "~/src/[REDACTED:code].md",
		"Isoform Main chat", "Isoform Chat", "Chat page", "Main chat", "Chat - Isoform", "Isoform 聊天",
	} {
		t.Run(name, func(t *testing.T) {
			if got := topicHumanName(name); got != "" {
				t.Fatalf("unsafe subject became a display name: %q", got)
			}
			row := topicRegistryTestActivity("unsafe", name, topicRegistryTestNow, Entity{Type: entityProject, Name: name})
			initial := buildTopicRegistry(topicRegistryData{activities: []ActivityRow{row}, now: topicRegistryTestNow})
			if len(initial.views) != 0 || len(initial.projects) != 0 {
				t.Fatalf("unsafe work was surfaced: %+v", initial)
			}
			stored := topicProject{ID: "project_unsafe", Name: name, MemberKeys: []string{"obj:unsafe"}, ActivityIDs: []string{"unsafe"}, Aliases: []string{"project_legacy"}}
			pins := map[string]bool{stored.ID: true, "project_legacy": true, "follow-unsafe": true}
			in := topicRegistryData{existing: []topicProject{stored}, activities: []ActivityRow{row}, now: topicRegistryTestNow,
				sidebarPins: pins, following: UserTopicsView{Interests: true, Topics: []UserTopic{{ID: "follow-unsafe", Label: name, Why: "Current project"}}}}
			for read := 0; read < 2; read++ {
				result := buildTopicRegistry(in)
				if len(result.views) != 0 || len(result.projects) != 1 {
					t.Fatalf("read %d surfaced unsafe work or follow: %+v", read, result)
				}
				want := stored
				want.Name = ""
				if !reflect.DeepEqual(result.projects[0], want) || len(pins) != 3 {
					t.Fatalf("withholding changed saved identity or pins: %+v, want %+v", result.projects[0], want)
				}
				in.existing = result.projects
			}
			in.closedEyes = true
			if got := buildTopicRegistry(in); len(got.views) != 0 {
				t.Fatalf("closed eyes surfaced unsafe follow: %+v", got.views)
			}
		})
	}
	for _, name := range []string{"Isoform chief of staff interface", "Chat page design", "Main chat recordings", "Safari research"} {
		if topicHumanName(name) != name {
			t.Errorf("legitimate subject withheld: %q", name)
		}
	}
}

func TestTopicUnsafeColdSnapshotNamesAreWithheld(t *testing.T) {
	a, _, now := registryChatTestApp(t)
	if views, err := collectAndReadTopicViewsForTest(a); err != nil || len(views) != 1 {
		t.Fatalf("control registry: %+v, %v", views, err)
	}
	for _, name := range []string{"Isoform Main chat", "Chat page", "Safari [REDACTED:code]", "Safari [...]"} {
		svc := a.topicsSvc()
		snapshot := svc.snapshots.Current()
		if snapshot == nil || len(snapshot.Topics) == 0 {
			t.Fatal("missing persisted topic snapshot")
		}
		snapshot.Topics[0].Topic.Name = name
		if err := svc.snapshots.Replace(snapshot); err != nil {
			t.Fatal(err)
		}
		a.topics = newTopicsService(a)
		a.topics.now = func() time.Time { return now }
		a.topics.registryWorkerContext = t.Context()
		got, _, _ := a.topics.todayViews()
		if len(got) != 0 {
			t.Fatalf("cold snapshot surfaced unsafe name %q: %+v", name, got)
		}
	}
}

func TestTopicUnsafeMembersDoNotJoinRealWorkOrMintNewIDs(t *testing.T) {
	row := topicRegistryTestActivity("recordings", "Thinking Cap recordings", topicRegistryTestNow,
		Entity{Type: entityProject, Name: "Isoform Main chat"}, Entity{Type: entityProject, Name: "Thinking Cap"})
	result := buildTopicRegistry(topicRegistryData{activities: []ActivityRow{row}, now: topicRegistryTestNow})
	if len(result.views) != 1 || result.views[0].Name != "Thinking Cap" || len(result.projects[0].MemberKeys) != 1 {
		t.Fatalf("container contaminated real work: %+v", result)
	}
	a := newThreadTestApp(t)
	t.Setenv(topicsStateDirEnvVar, t.TempDir())
	configureTopicRegistryTest(a, topicRegistryTestNow)
	seedThreadMoment(t, a, threadFixtureMoment{id: "unsafe", title: "Isoform Main chat", app: "Code",
		start: topicRegistryTestNow.Add(-time.Hour), end: topicRegistryTestNow.Add(-30 * time.Minute),
		entities: []Entity{{Type: entityProject, Name: "Isoform Main chat"}}})
	if _, err := a.topicsSvc().store.update(func(state *topicsStateFile) {
		state.Projects = []topicProject{{ID: "project_unsafe", Name: "Isoform Main chat", MemberKeys: []string{"obj:isoform"}, ActivityIDs: []string{"unsafe"}, Aliases: []string{"project_legacy"}}}
		state.SidebarPins = map[string]bool{"project_unsafe": true, "project_legacy": true}
	}); err != nil {
		t.Fatal(err)
	}
	for read := 0; read < 3; read++ {
		if views, err := collectAndReadTopicViewsForTest(a); err != nil || len(views) != 0 {
			t.Fatalf("read %d: %+v, %v", read, views, err)
		}
		state := (&topicsStore{path: a.topicsSvc().store.path}).load()
		if len(state.Projects) != 1 || state.Projects[0].ID != "project_unsafe" || state.Projects[0].Name != "" ||
			!reflect.DeepEqual(state.Projects[0].Aliases, []string{"project_legacy"}) || !state.SidebarPins["project_unsafe"] || !state.SidebarPins["project_legacy"] {
			t.Fatalf("read %d changed identity/pins or minted hidden projects: %+v", read, state)
		}
	}
}

func TestTopicUnsafeSavedSnapshotNeverBecomesBriefChip(t *testing.T) {
	a, _, _ := registryChatTestApp(t)
	if views, err := collectAndReadTopicViewsForTest(a); err != nil || len(views) != 1 {
		t.Fatalf("control registry: %+v, %v", views, err)
	}
	brief := MobileBriefV1{NarrativeStatus: "ok", Evidence: []MobileBriefRef{{Ref: "e1", SourceKind: "activity", SourceID: "atlas"}},
		Narrative: []MobileBriefPara{{Text: "Atlas release is ready.", Refs: []string{"e1"}}}}
	if got := a.mobileBriefWithParagraphTopics(t.Context(), brief); got.Narrative[0].TopicID == "" {
		t.Fatal("control has no topic chip")
	}
	for _, name := range []string{"Isoform Main chat", "Chat page", "Safari [REDACTED:code]", "Safari [masked]"} {
		if _, err := a.topicsSvc().store.update(func(state *topicsStateFile) {
			state.ChatTopics[0].Topic.Name = name
		}); err != nil {
			t.Fatal(err)
		}
		got := a.mobileBriefWithParagraphTopics(t.Context(), brief)
		if got.Narrative[0].TopicID != "" || got.Narrative[0].TopicName != "" {
			t.Fatalf("saved unsafe snapshot became a chip: %+v", got.Narrative)
		}
		legacy := brief
		legacy.Narrative = []MobileBriefPara{{Text: "Atlas release is ready.", TopicID: "project_legacy", TopicName: name}}
		if got := mobileBriefHygiene(legacy); got.Narrative[0].TopicID != "" || got.Narrative[0].TopicName != "" {
			t.Fatalf("legacy topic annotation survived: %+v", got.Narrative)
		}
	}
}

func TestTopicUnsafeGoalsNeverAllocateProjectsAcrossReadsAndReopen(t *testing.T) {
	for _, name := range []string{"Safari [masked]", "Safari [REDACTED:code]", "Isoform Chat"} {
		t.Run(name, func(t *testing.T) {
			bridge, purpose, now := mobileTestGoalBridge(t)
			t.Setenv(topicsStateDirEnvVar, t.TempDir())
			a := bridge.app
			configureTopicRegistryTest(a, now)
			a.topicSource = &registryTopicSource{app: a}
			goal := mobileTestPurpose(t, purpose, "unsafe", PurposeKindGoal, name, false, nil)
			path := a.topicsSvc().store.path
			pins := map[string]bool{"project_unsafe_goal": true, "project_legacy_goal": true}
			check := func(wantIDs []string) {
				t.Helper()
				for read := 0; read < 4; read++ {
					if views, err := collectAndReadTopicViewsForTest(a); err != nil || len(views) != 0 {
						t.Fatalf("read %d surfaced unsafe goal: %+v, %v", read, views, err)
					}
					saved := (&topicsStore{path: path}).load()
					ids := []string{}
					for _, project := range saved.Projects {
						ids = append(ids, project.ID)
						if project.Name != "" || !reflect.DeepEqual(project.Aliases, []string{"project_legacy_goal"}) {
							t.Fatalf("unsafe reservation changed: %+v", project)
						}
					}
					if !reflect.DeepEqual(ids, wantIDs) || !reflect.DeepEqual(saved.SidebarPins, pins) {
						t.Fatalf("read %d minted ids or changed pins: ids=%v want=%v pins=%v", read, ids, wantIDs, saved.SidebarPins)
					}
				}
			}
			if _, err := a.topicsSvc().store.update(func(state *topicsStateFile) { state.SidebarPins = pins }); err != nil {
				t.Fatal(err)
			}
			check([]string{}) // A newly observed unsafe goal never allocates an id.
			if _, err := a.topicsSvc().store.update(func(state *topicsStateFile) {
				state.Projects = []topicProject{{ID: "project_unsafe_goal", Name: name, GoalIDs: []string{goal.OpenLoop.ID}, Aliases: []string{"project_legacy_goal"}}}
			}); err != nil {
				t.Fatal(err)
			}
			check([]string{"project_unsafe_goal"})
			baseDir := filepath.Dir(filepath.Dir(a.activity.store.segmentsDir))
			if err := a.activity.store.Close(); err != nil {
				t.Fatal(err)
			}
			activity, err := NewActivityStore(baseDir)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = activity.Close() })
			a.activity = &activityMonitorState{store: activity}
			a.topics = newTopicsService(a)
			configureTopicRegistryTest(a, now)
			if _, err := NewPurposeMapStore(activity.db).GetDetail(goal.OpenLoop.ID); err != nil {
				t.Fatalf("goal must remain persisted after reopen: %v", err)
			}
			check([]string{"project_unsafe_goal"})
		})
	}
}
