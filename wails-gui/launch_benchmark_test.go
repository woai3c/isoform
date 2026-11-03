package main

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"wails-gui/internal/handoff"
	"wails-gui/internal/platform/activitymon"
	"wails-gui/storage"
)

// Real SQLite and brief files, isolated from the user's account. No model,
// network, capture or native window is started by this harness.
func launchFixture(tb testing.TB) *App {
	return launchFixtureRows(tb, 200, 50)
}

func launchFixtureRows(tb testing.TB, sessions, rows int) *App {
	tb.Helper()
	root := tb.TempDir()
	tb.Setenv("HOME", root)
	tb.Setenv("YANSU_AGENT_HOME", root)
	tb.Setenv("YANSU_MOBILE_STATE_DIR", filepath.Join(root, "mobile"))
	state, err := storage.New(filepath.Join(root, "sessions.db"))
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = state.Close() })
	activity, err := NewActivityStore(root)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = activity.Close() })
	work, err := handoff.NewStore(filepath.Join(root, "handoff"))
	if err != nil {
		tb.Fatal(err)
	}
	app := &App{store: state, activity: &activityMonitorState{store: activity, configDir: root}, handoffStore: work}
	ctx := context.Background()
	if err := state.SaveProject(ctx, storage.ProjectRow{ID: "launch", Name: "Launch fixture", Path: root}); err != nil {
		tb.Fatal(err)
	}
	for i := 0; i < sessions; i++ {
		kind := ""
		if i == 0 {
			kind = storage.SessionKindMain
		}
		id := fmt.Sprintf("session-%d", i)
		if err := state.SaveSession(ctx, storage.SessionRow{LocalID: id, ProjectID: "launch", Label: "Local work", Kind: kind}); err != nil {
			tb.Fatal(err)
		}
		for j := 0; j < rows; j++ {
			if _, err := state.AppendStreamItem(ctx, id, storage.StreamItemRow{ID: fmt.Sprintf("row-%d-%d", i, j), Kind: "user", Content: "Review the local release plan.", CreatedAt: time.Now().UnixMilli()}); err != nil {
				tb.Fatal(err)
			}
		}
	}
	now := time.Now()
	for i := 0; i < sessions; i++ {
		at := now.Add(-time.Duration(i+1) * time.Minute).UnixMilli()
		events := make([]activitymon.ActivityEvent, rows)
		for j := range events {
			kinds := []activitymon.EventKind{activitymon.EventKeyboard, activitymon.EventSpeech, activitymon.EventCalendarPeek}
			events[j] = activitymon.ActivityEvent{Timestamp: at + int64(j)*100, Kind: kinds[j%len(kinds)], AppName: "Code", AppBundleID: "com.microsoft.VSCode"}
			if events[j].Kind == activitymon.EventKeyboard {
				events[j].TypedText = "Review the local release plan."
			}
		}
		if err := activity.SaveSession(ActivitySessionRow{ID: fmt.Sprintf("activity-%d", i), StartTime: at, EndTime: at + 30000, TriggerKind: "app_switch", AppNames: `["Code"]`, EventCount: rows, TriageStatus: "skipped"}, events, nil); err != nil {
			tb.Fatal(err)
		}
	}
	svc := newMobileBriefService(app)
	app.mobileBriefSvc = svc
	if _, err := work.Save(handoff.Entry{ID: "release", Title: "Release plan", Source: "manual", Status: handoff.StatusCompleted}); err != nil {
		tb.Fatal(err)
	}
	brief := buildMobileBriefV1(mobileBriefInput{Slot: mobileBriefSlotMorning, AsOf: now, WindowStart: now.Add(-time.Hour), Location: time.Local,
		Moved: []mobileBriefCandidate{{SourceKind: "history", SourceID: "release", Title: "Release plan", At: now}},
	})
	if err := svc.store.save(brief, now); err != nil {
		tb.Fatal(err)
	}
	previous := todayBriefPermissionStatus
	todayBriefPermissionStatus = func(*App) PlatformPermissions { return PlatformPermissions{} }
	tb.Cleanup(func() { todayBriefPermissionStatus = previous })
	return app
}

func BenchmarkLaunchFirstScreen(b *testing.B) {
	app := launchFixture(b)
	app.launch = newLaunchStartup()
	for _, mode := range []string{"refresh", "stored"} {
		b.Run(mode, func(b *testing.B) {
			app.launch.storedReads.Store(mode == "stored")
			for i := 0; i < b.N; i++ {
				if _, err := app.GetLatestBrief(); err != nil {
					b.Fatal(err)
				}
				if _, err := app.GetTodayDayShape(time.Now().In(time.Local).Format("2006-01-02")); err != nil {
					b.Fatal(err)
				}
				if _, err := app.GetMainChatOverview(); err != nil {
					b.Fatal(err)
				}
				if _, err := app.LoadStreamItemsPage("session-0", -1, 50); err != nil {
					b.Fatal(err)
				}
				if _, err := app.GetCaptureCounts(0); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// Reopen already-migrated, populated stores rather than measuring an empty
// database or just the in-process memo. No Wails window/services are started.
func BenchmarkLaunchOpenPersistedStores(b *testing.B) {
	fixture := launchFixture(b)
	root := fixture.activity.configDir
	if err := fixture.store.Close(); err != nil {
		b.Fatal(err)
	}
	if err := fixture.activity.store.Close(); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		state, err := openStartupStorage()
		if err != nil {
			b.Fatal(err)
		}
		app := &App{store: state, launch: newLaunchStartup(), handoffStore: fixture.handoffStore}
		app.prepareActivityReads()
		if app.activity == nil {
			b.Fatal("local source store did not open")
		}
		if app.activity.configDir != root {
			b.Fatal("startup escaped the fixture")
		}
		if _, err := app.GetLatestBrief(); err != nil {
			b.Fatal(err)
		}
		if _, err := app.GetTodayDayShape(time.Now().In(time.Local).Format("2006-01-02")); err != nil {
			b.Fatal(err)
		}
		if _, err := app.GetMainChatOverview(); err != nil {
			b.Fatal(err)
		}
		if _, err := app.LoadStreamItemsPage("session-0", -1, 50); err != nil {
			b.Fatal(err)
		}
		if _, err := app.GetCaptureCounts(0); err != nil {
			b.Fatal(err)
		}
		if err := app.activity.store.Close(); err != nil {
			b.Fatal(err)
		}
		if err := state.Close(); err != nil {
			b.Fatal(err)
		}
	}
}
