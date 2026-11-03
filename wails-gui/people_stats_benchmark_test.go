package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"wails-gui/internal/platform/activitymon"
	"wails-gui/storage"
)

// Opt in with: go test -run '^$' -bench '^BenchmarkPeople' -benchtime=1x .
// Setup is excluded; all databases and files belong to the fixture.

func newPeopleBenchApp(b testing.TB) *App {
	b.Helper()
	home := os.Getenv(peopleProfileFixtureHomeEnv)
	if home == "" {
		home = b.TempDir()
	} else if err := os.MkdirAll(home, 0700); err != nil {
		b.Fatal(err)
	}
	b.Setenv("HOME", home)
	b.Setenv("YANSU_MOBILE_STATE_DIR", filepath.Join(home, "mobile"))
	b.Setenv(topicsStateDirEnvVar, filepath.Join(home, "topics"))
	state, err := storage.New(filepath.Join(home, "state.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = state.Close() })
	activity, err := NewActivityStore(filepath.Join(home, "activity"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = activity.Close() })
	return &App{store: state, activity: &activityMonitorState{store: activity}}
}

// peopleBenchSession records one readable observed session and sights each
// given person in it with a message preview.
func peopleBenchSession(b *testing.B, a *App, id string, at int64, people ...storage.PrincipalRow) {
	b.Helper()
	event := activitymon.ActivityEvent{Kind: activitymon.EventFocus, Timestamp: at, AppName: "Code", AppBundleID: "com.example.code", WindowTitle: "work"}
	if err := a.activity.store.SaveSession(ActivitySessionRow{ID: id, AppNames: `["Code"]`, StartTime: at, EndTime: at + 60000, TriageStatus: "done"}, []activitymon.ActivityEvent{event}, nil); err != nil {
		b.Fatal(err)
	}
	for _, p := range people {
		if err := a.store.BumpPrincipalSighting(context.Background(), p.ID, "wechat", id, at, "preview text"); err != nil {
			b.Fatal(err)
		}
	}
}

// peopleBenchFixture builds peopleCount people sighted across sessionCount
// shared sessions; the first person is sighted in every session.
func peopleBenchFixture(b *testing.B, a *App, peopleCount, sessionCount int) []storage.PrincipalRow {
	b.Helper()
	ctx := context.Background()
	people := make([]storage.PrincipalRow, 0, peopleCount)
	for i := 0; i < peopleCount; i++ {
		p, err := a.CreateContact(fmt.Sprintf("Person %02d", i), "colleague", nil, nil)
		if err != nil {
			b.Fatal(err)
		}
		row, err := a.store.GetPrincipal(ctx, p.ID)
		if err != nil {
			b.Fatal(err)
		}
		people = append(people, row)
	}
	at := time.Now().Add(-time.Hour).UnixMilli()
	for s := 0; s < sessionCount; s++ {
		// Every session sights the first person and every fifth after that, so
		// sessions are shared across people.
		members := []storage.PrincipalRow{people[0]}
		for i := s%5 + 1; i < peopleCount; i += 5 {
			members = append(members, people[i])
		}
		peopleBenchSession(b, a, fmt.Sprintf("session-%02d", s), at+int64(s)*1000, members...)
	}
	return people
}

// BenchmarkPeopleStatsLineage measures the per-session lineage gates behind
// ListPrincipalsWithStats: walking every sighting per person against sharing
// one read cache across the read.
func BenchmarkPeopleStatsLineage(b *testing.B) {
	a := newPeopleBenchApp(b)
	ctx := context.Background()
	people := peopleBenchFixture(b, a, 60, 40)
	statsMap, err := a.store.GetAllPrincipalStats(ctx)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.Run("walk-per-sighting", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			for _, p := range people {
				a.peopleStatsReadable(ctx, p, statsMap[p.ID], nil)
			}
		}
	})
	b.Run("shared-read-cache", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			cache := newPeopleReadGateCache()
			for _, p := range people {
				a.peopleStatsReadable(ctx, p, statsMap[p.ID], cache)
			}
		}
	})
}

// BenchmarkPeopleOverviewRead measures one full overview read with every path
// walking lineage per person against one shared cache generation.
func BenchmarkPeopleOverviewRead(b *testing.B) {
	a := newPeopleBenchApp(b)
	peopleBenchFixture(b, a, 60, 40)
	b.ReportAllocs()
	b.Run("walk-per-path", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if _, err := a.listPeopleOverview(nil); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("shared-read-cache", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if _, err := a.listPeopleOverview(newPeopleReadGateCache()); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkPersonDetailRead measures one person-detail read for the person
// sighted in every session, per-path walks against one cache generation.
func BenchmarkPersonDetailRead(b *testing.B) {
	a := newPeopleBenchApp(b)
	people := peopleBenchFixture(b, a, 60, 40)
	b.ReportAllocs()
	b.Run("walk-per-path", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if _, err := a.getPersonDetail(people[0].ID, nil); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("shared-read-cache", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if _, err := a.getPersonDetail(people[0].ID, newPeopleReadGateCache()); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func peopleBenchSeedTopic(b *testing.B, a *App, id string, at time.Time) {
	b.Helper()
	start, end := at.UnixMilli(), at.Add(30*time.Minute).UnixMilli()
	event := activitymon.ActivityEvent{Kind: activitymon.EventFocus, Timestamp: start, AppName: "Code", AppBundleID: "com.example.code", WindowTitle: id}
	if err := a.activity.store.SaveSession(ActivitySessionRow{ID: "session-" + id, StartTime: start, EndTime: end, AppNames: `["Code"]`, TriageStatus: "done"}, []activitymon.ActivityEvent{event}, nil); err != nil {
		b.Fatal(err)
	}
	run := testRun("run-"+id, "Code", start, end, end-start, 1)
	run.AppKey = "com.example.code"
	run.SessionIDs = []string{"session-" + id}
	run.FirstEventRowID, run.LastEventRowID = 1, 1
	if err := insertTimelineRun(a.activity.store.db, run, 0); err != nil {
		b.Fatal(err)
	}
	if err := a.activity.store.UpsertTimelineGroupSummary(timelineRunToGroup(run, nil), ActivityTimelineGroupSummary{Title: id}, "bench", "", ""); err != nil {
		b.Fatal(err)
	}
	if err := a.activity.store.MarkTimelineRunSummaryDone(run.ID, run.SummarizedEventCount); err != nil {
		b.Fatal(err)
	}
	entities, _ := json.Marshal([]Entity{{Type: entityProject, Name: "Project " + id}, {Type: entityPerson, Name: "Mei Chen"}})
	appTimes, _ := json.Marshal(map[string]int64{"Code": end - start})
	if err := a.activity.store.InsertActivity(ActivityRow{ID: id, DayStartMs: startOfDayLocal(start), Title: "Project " + id, Status: activityStatusIdle, RunIDs: fmt.Sprintf(`["%s"]`, run.ID), ActiveMsByApp: string(appTimes), Entities: string(entities), StartMs: start, EndMs: end, CreatedAt: start, UpdatedAt: end}); err != nil {
		b.Fatal(err)
	}
	if err := a.activity.store.UpsertActivityRunLink(run.ID, id, run.SummarizedEventCount, end); err != nil {
		b.Fatal(err)
	}
}

// BenchmarkPeopleSharedTopicsRead measures the two sources of shared topics
// for one person-detail read: rebuilding the registry against the committed
// snapshot the adapter reads.
func BenchmarkPeopleSharedTopicsRead(b *testing.B) {
	a := newPeopleBenchApp(b)
	configureTopicRegistryTest(a, time.Now())
	at := time.Now().Add(-2 * time.Hour)
	for i := 0; i < 24; i++ {
		peopleBenchSeedTopic(b, a, fmt.Sprintf("topic-%02d", i), at.Add(time.Duration(i)*time.Minute))
	}
	if _, err := a.todayTopicViews(); err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	b.ReportAllocs()
	b.Run("registry-rebuild", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if _, err := a.topicsSvc().registryViews(ctx); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("committed-snapshot", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			topics, err := (registryPeopleWorkTopics{a}).ReadableWorkTopics(ctx)
			if err != nil || len(topics) == 0 {
				b.Fatalf("topics = %d, %v", len(topics), err)
			}
		}
	})
}
