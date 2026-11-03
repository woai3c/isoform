package main

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestTopicHumanNames(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{"riffado/connector", "Riffado connector"},
		{"rc1-handoff.md", "RC 1 handoff"},
		{"~/src/Thinking_Cap", "Thinking Cap"},
		{"poolsideai/pool", "Poolside"},
		{"ISO form chief of staff interface", "Isoform chief of staff interface"},
		{"docs/release_notes.txt", "Release notes"},
		{"/Users/person/src/Meridian/launch_plan.pdf", "Launch plan"},
		{"发布计划.md", "发布计划"},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			got := topicHumanName(tc.raw)
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
			if strings.ContainsAny(got, "/~\\") || topicNameExtension.MatchString(got) {
				t.Fatalf("display name contains a path or file extension: %q", got)
			}
			if again := topicHumanName(got); again != got {
				t.Fatalf("name is unstable: %q -> %q", got, again)
			}
		})
	}
}

func TestTopicNamesPreserveHumanProductSpelling(t *testing.T) {
	for _, name := range []string{"Node.js", "Next.js", "iOS", "macOS", "iTerm2", "iPhone", "GitHub", "OpenAI", "Claude Code", "cEEGrid", "ffmpeg"} {
		t.Run(name, func(t *testing.T) {
			row := topicRegistryTestActivity("product", name+": development", topicRegistryTestNow, Entity{Type: entityProject, Name: name})
			result := buildTopicRegistry(topicRegistryData{activities: []ActivityRow{row}, now: topicRegistryTestNow})
			if len(result.projects) != 1 || result.projects[0].Name != name || topicHumanName(name) != name || migrateTopicDisplayName(topicProject{Name: name}).Name != name {
				t.Fatalf("human spelling changed: %+v, want %q", result.projects, name)
			}
		})
	}
}

func TestTopicBareFilesUseAdmittedActivitySubject(t *testing.T) {
	for _, source := range []string{entityRepo, entityProject} {
		for _, file := range []string{"main.ts", "main.js", "main.py", "styles.css", "App.swift"} {
			t.Run(source+"/"+file, func(t *testing.T) {
				row := topicRegistryTestActivity("file", "Atlas release planning", topicRegistryTestNow, Entity{Type: source, Name: file})
				initial := buildTopicRegistry(topicRegistryData{activities: []ActivityRow{row}, now: topicRegistryTestNow})
				if len(initial.views) != 1 || initial.views[0].Name != "Atlas release planning" {
					t.Fatalf("file identifier became a topic name: %+v", initial.views)
				}
				observation := newTopicActivityObservation(row, "")
				if !reflect.DeepEqual(initial.projects[0].MemberKeys, observation.Keys) || initial.projects[0].ID != topicProjectID(observation.Keys[0]) {
					t.Fatal("filename display pass changed source identity")
				}
			})
		}
		for _, product := range []string{"Node.js", "Next.js"} {
			t.Run(source+"/"+product, func(t *testing.T) {
				row := topicRegistryTestActivity("product", product+": release planning", topicRegistryTestNow, Entity{Type: source, Name: product})
				result := buildTopicRegistry(topicRegistryData{activities: []ActivityRow{row}, now: topicRegistryTestNow})
				if len(result.views) != 1 || result.views[0].Name != product {
					t.Fatalf("product spelling lost to file cleanup: %+v", result.views)
				}
			})
		}
	}
	// Both repo and project evidence own product spelling even when the
	// window title names other work. Only positive file evidence overrides it.
	for _, source := range []string{entityRepo, entityProject} {
		for _, product := range []string{"Node.js", "Next.js", "Effect.ts"} {
			row := topicRegistryTestActivity("product", "Atlas release planning", topicRegistryTestNow, Entity{Type: source, Name: product})
			result := buildTopicRegistry(topicRegistryData{activities: []ActivityRow{row}, now: topicRegistryTestNow})
			if len(result.views) != 1 || result.views[0].Name != product {
				t.Fatalf("%s product spelling changed: %+v", source, result.views)
			}
			row.EvidenceTokens = []ActivityEvidenceToken{{Kind: "path", Value: "~/src/atlas/" + product}}
			result = buildTopicRegistry(topicRegistryData{activities: []ActivityRow{row}, now: topicRegistryTestNow})
			if len(result.views) != 1 || result.views[0].Name != "Atlas release planning" {
				t.Fatalf("%s observed file did not take its activity subject: %+v", source, result.views)
			}
		}
		for _, file := range []string{"src/release.ts", "~/src/atlas/release.js", `C:\src\release.py`} {
			row := topicRegistryTestActivity("file", "Atlas release planning", topicRegistryTestNow, Entity{Type: source, Name: file})
			result := buildTopicRegistry(topicRegistryData{activities: []ActivityRow{row}, now: topicRegistryTestNow})
			if len(result.views) != 1 || result.views[0].Name != "Atlas release planning" {
				t.Fatalf("%s path did not take its activity subject: %+v", source, result.views)
			}
		}
	}
}

func TestTopicBareFileStoredNameMigratesWithoutChangingIdentity(t *testing.T) {
	a, _, now := registryChatTestApp(t)
	if _, err := a.activity.store.db.Exec(`UPDATE activities SET entities='[{"type":"project","name":"main.js"}]',title='Atlas release planning' WHERE id='atlas'`); err != nil {
		t.Fatal(err)
	}
	row := topicRegistryTestActivity("atlas", "main.js", now, Entity{Type: entityProject, Name: "main.js"})
	stored := buildTopicRegistry(topicRegistryData{activities: []ActivityRow{row}, now: now}).projects[0]
	stored.Name, stored.Aliases = "main.js", []string{"project_legacy_file"}
	if _, err := a.topicsSvc().store.update(func(state *topicsStateFile) {
		state.Projects = []topicProject{stored}
		state.SidebarPins = map[string]bool{stored.Aliases[0]: true}
	}); err != nil {
		t.Fatal(err)
	}
	for read := 0; read < 2; read++ {
		views, err := collectAndReadTopicViewsForTest(a)
		if err != nil || len(views) != 1 || views[0].Name != "Atlas release planning" || views[0].ID != stored.ID || !views[0].PinnedToSidebar {
			t.Fatalf("stored file migration read %d: %+v, %v", read, views, err)
		}
		saved := (&topicsStore{path: a.topicsSvc().store.path}).load()
		if len(saved.Projects) != 1 || saved.Projects[0].Name != "Atlas release planning" || saved.Projects[0].ID != stored.ID ||
			!reflect.DeepEqual(saved.Projects[0].Aliases, stored.Aliases) || !reflect.DeepEqual(saved.Projects[0].MemberKeys, stored.MemberKeys) || !saved.SidebarPins[stored.Aliases[0]] {
			t.Fatalf("display migration changed persisted identity: %+v", saved.Projects)
		}
	}
}

func TestTopicRegistryMigratesReadableStoredName(t *testing.T) {
	app := newThreadTestApp(t)
	t.Setenv(topicsStateDirEnvVar, t.TempDir())
	configureTopicRegistryTest(app, topicRegistryTestNow)
	seedThreadMoment(t, app, threadFixtureMoment{
		id: "connector", title: "Riffado connector: authentication", app: "Code",
		start: topicRegistryTestNow.Add(-time.Hour), end: topicRegistryTestNow.Add(-30 * time.Minute),
		entities: []Entity{{Type: entityRepo, Name: "riffado/connector"}},
	})
	views, err := collectAndReadTopicViewsForTest(app)
	if err != nil || len(views) != 1 {
		t.Fatalf("initial registry = %+v, %v", views, err)
	}
	id := views[0].ID
	store := app.topicsSvc().store
	if _, err := store.update(func(state *topicsStateFile) {
		state.Projects[0].Name = "riffado/connector"
		state.Projects[0].Aliases = []string{"project_legacy"}
		state.SidebarPins = map[string]bool{"project_legacy": true}
	}); err != nil {
		t.Fatal(err)
	}
	for read := 0; read < 2; read++ {
		views, err = collectAndReadTopicViewsForTest(app)
		if err != nil || len(views) != 1 || views[0].ID != id || views[0].Name != "Riffado connector" || !views[0].PinnedToSidebar {
			t.Fatalf("read %d = %+v, %v", read, views, err)
		}
		state := (&topicsStore{path: store.path}).load()
		if state.Projects[0].Name != "Riffado connector" || state.Projects[0].ID != id || !reflect.DeepEqual(state.Projects[0].Aliases, []string{"project_legacy"}) || !state.SidebarPins["project_legacy"] {
			t.Fatalf("saved migration = %+v", state.Projects)
		}
	}
}

func TestTopicNamesUseAdmittedProjectEvidence(t *testing.T) {
	for _, tc := range []struct {
		raw, title, brand, want string
	}{
		{"riffado/connector", "Riffado connector - authentication", "", "Riffado connector"},
		{"riffado/connector", "Riffado connector - authentication", "Code", "Riffado connector"},
		{"poolsideai/pool", "Poolside: design review", "Poolside", "Poolside"},
		{"acme/odd-slug", "MoonBeam: editor", "MoonBeam", "MoonBeam"},
		{"acme/thinking_cap", "Thinking Cap: recordings", "", "Thinking Cap"},
		{"~/src/README.md", "Thinking Cap recordings", "", "Thinking Cap recordings"},
		{"~/src/a8f201", "Thinking Cap recordings", "", "Thinking Cap recordings"},
		{"capture_829.json", "Thinking Cap recordings", "", "Thinking Cap recordings"},
		{"ISO form chief of staff interface", "Isoform chief of staff interface", "", "Isoform chief of staff interface"},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			entities := []Entity{{Type: entityRepo, Name: tc.raw}}
			if tc.brand != "" {
				entities = append(entities, Entity{Type: entityProject, Name: tc.brand})
			}
			row := topicRegistryTestActivity("work", tc.title, topicRegistryTestNow, entities...)
			observation := newTopicActivityObservation(row, "")
			result := buildTopicRegistry(topicRegistryData{activities: []ActivityRow{row}, now: topicRegistryTestNow})
			if len(result.projects) != 1 || result.projects[0].Name != tc.want {
				t.Fatalf("projects = %+v, want %q", result.projects, tc.want)
			}
			if !reflect.DeepEqual(result.projects[0].MemberKeys, observation.Keys) {
				t.Fatalf("original matching aliases changed: %+v", result.projects[0])
			}
			if result.projects[0].ID != topicProjectID(observation.Keys[0]) {
				t.Fatalf("display name changed the id seed: %+v", result.projects[0])
			}
		})
	}
}

func TestTopicNameMigrationOnlyChangesDisplayNameAndPersists(t *testing.T) {
	store := &topicsStore{path: t.TempDir() + "/topics.json"}
	old := topicProject{
		ID: "project_kept", Name: "rc1-handoff.md", MemberKeys: []string{"obj:rc1-handoff.md"},
		Aliases: []string{"project_old"}, ActivityIDs: []string{"activity"}, GoalIDs: []string{"goal"},
		CreatedAt: topicRegistryTime(topicRegistryTestNow.UnixMilli()), LastSeenAt: topicRegistryTime(topicRegistryTestNow.UnixMilli()),
	}
	pins := map[string]bool{old.ID: true, "project_old": true}
	if _, err := store.update(func(state *topicsStateFile) {
		state.Projects, state.SidebarPins = []topicProject{old}, pins
	}); err != nil {
		t.Fatal(err)
	}
	state := store.load()
	result := buildTopicRegistry(topicRegistryData{existing: state.Projects, sidebarPins: state.SidebarPins, now: topicRegistryTestNow})
	want := old
	want.Name = "RC 1 handoff"
	if migrated := migrateTopicDisplayName(old); !reflect.DeepEqual(migrated, want) {
		t.Fatalf("name migration changed another field: %+v", migrated)
	}
	// The registry independently re-derives people on every build.
	buildWant := want
	buildWant.PeopleKeys = []string{}
	if len(result.projects) != 1 || !reflect.DeepEqual(result.projects[0], buildWant) || !result.views[0].PinnedToSidebar || result.views[0].ID != old.ID {
		t.Fatalf("migration changed identity or membership: %+v %+v", result.projects, result.views)
	}
	if _, err := store.update(func(state *topicsStateFile) { state.Projects = result.projects }); err != nil {
		t.Fatal(err)
	}
	restarted := (&topicsStore{path: store.path}).load()
	if !reflect.DeepEqual(restarted.Projects, []topicProject{want}) || !reflect.DeepEqual(restarted.SidebarPins, pins) {
		t.Fatalf("migration did not persist name and pins: %+v", restarted)
	}
	again := buildTopicRegistry(topicRegistryData{existing: restarted.Projects, sidebarPins: restarted.SidebarPins, now: topicRegistryTestNow})
	if !reflect.DeepEqual(again.projects, result.projects) {
		t.Fatalf("second build changed names: %+v", again.projects)
	}
}
