package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/platform/activitymon"
)

func TestActivityAskReadsSelectedHistoricalDayAndWeek(t *testing.T) {
	app := newTodayContextTestApp(t, true)
	date := time.Now().AddDate(0, 0, -28)
	monday := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.Local).AddDate(0, 0, -(int(date.Weekday())+6)%7)
	for i, offset := range []int{0, 1, 7} {
		id := fmt.Sprintf("historical-%d", i)
		at := monday.AddDate(0, 0, offset).Add(10 * time.Hour).UnixMilli()
		events := []activitymon.ActivityEvent{{Kind: activitymon.EventFocus, Timestamp: at, AppName: "Code", AppBundleID: "com.example.code", WindowTitle: "Project"}}
		store := app.activity.store
		if err := store.SaveSession(ActivitySessionRow{ID: id, StartTime: at, EndTime: at + 60000, AppNames: `["Code"]`, TriageStatus: "done"}, events, nil); err != nil {
			t.Fatal(err)
		}
		run := testRun("run-"+id, "Code", at, at+60000, 60000, len(events))
		run.AppKey, run.EventCount, run.SessionIDs = "com.example.code", len(events), []string{id}
		if err := store.db.QueryRow(`SELECT min(id),max(id) FROM activity_events WHERE session_id=?`, id).Scan(&run.FirstEventRowID, &run.LastEventRowID); err != nil {
			t.Fatal(err)
		}
		if err := insertTimelineRun(store.db, run, 0); err != nil {
			t.Fatal(err)
		}
		if err := store.InsertActivity(ActivityRow{ID: id, Title: fmt.Sprintf("Historical work %d", i), Status: activityStatusIdle, RunIDs: composeJSON([]string{run.ID}), ActiveMsByApp: `{"Code":60000}`, StartMs: at, EndMs: at + 60000}); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		kind         string
		want, absent []string
	}{
		{"summary", []string{"Historical work 0"}, []string{"Historical work 1", "Historical work 2"}},
		{"summary_week", []string{"Historical work 0", "Historical work 1"}, []string{"Historical work 2"}},
	} {
		item, _, err := app.loadTodayChatContextItem(t.Context(), TodayChatContextRef{SourceKind: tc.kind, SourceID: monday.Format("2006-01-02")})
		if err != nil {
			t.Fatal(err)
		}
		for _, word := range tc.want {
			if !strings.Contains(item.Record.Text, word) {
				t.Errorf("%s missing %q: %q", tc.kind, word, item.Record.Text)
			}
		}
		for _, word := range tc.absent {
			if strings.Contains(item.Record.Text, word) {
				t.Errorf("%s included %q: %q", tc.kind, word, item.Record.Text)
			}
		}
	}
	if err := app.activity.store.DeleteActivity("historical-0"); err != nil {
		t.Fatal(err)
	}
	item, _, err := app.loadTodayChatContextItem(t.Context(), TodayChatContextRef{SourceKind: "summary_week", SourceID: monday.Format("2006-01-02")})
	if err != nil || strings.Contains(item.Record.Text, "Historical work 0") {
		t.Fatalf("withdrawn activity remained in week: %q %v", item.Record.Text, err)
	}
}

func TestTodayChatFindUsesCurrentStoredItemsWithoutRefreshingTopics(t *testing.T) {
	app := newTodayContextTestApp(t, true)
	t.Setenv(topicsStateDirEnvVar, t.TempDir())
	now := time.Date(2026, time.October, 16, 12, 0, 0, 0, time.Local)
	for _, tc := range []struct {
		name     string
		ageDays  int
		enabled  bool
		canceled bool
		wantErr  error
	}{
		{name: "fresh", enabled: true},
		{name: "freshness boundary", ageDays: topicFindsKeepDays, enabled: true},
		{name: "expired", ageDays: topicFindsKeepDays + 1, enabled: true, wantErr: errTodayChatItemUnavailable},
		{name: "disabled", wantErr: errTodayChatItemUnavailable},
		{name: "canceled", enabled: true, canceled: true, wantErr: context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := app.topicsSvc().store.update(func(state *topicsStateFile) {
				state.Enabled = &tc.enabled
				state.Finds = topicFindsState{
					RanOn: now.AddDate(0, 0, -tc.ageDays).Format("2006-01-02"),
					Items: []TopicFind{{ID: "selected", Title: "Current selected find", WhatsNew: "Current source summary", URL: "https://example.com/find"}},
				}
			}); err != nil {
				t.Fatal(err)
			}
			_, release := blockTodayLookup(t, app.activity.store.db)
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			if tc.canceled {
				cancel()
			}
			type result struct {
				item todayChatItem
				err  error
			}
			done := make(chan result, 1)
			go func() {
				item, err := app.resolveTodayChatItem(ctx, TodayChatContextRef{SourceKind: "find", SourceID: "selected"}, todayChatSources{}, now)
				done <- result{item: item, err: err}
			}()
			select {
			case got := <-done:
				if !errors.Is(got.err, tc.wantErr) {
					t.Fatalf("resolve error = %v, want %v", got.err, tc.wantErr)
				}
				if tc.wantErr == nil && (got.item.Label != "Current selected find" || got.item.Record.ID != "selected") {
					t.Fatalf("resolved find = %+v", got.item)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("find resolution waited for unrelated topic source reads")
			}
			release()
		})
	}
}

func TestTodayChatLegacyLearningBriefIsUnavailableWithoutCompositeReads(t *testing.T) {
	app := newTodayContextTestApp(t, true)
	now := time.Now()
	brief := buildMobileBriefV1(mobileBriefInput{
		Slot:        mobileBriefSlotMorning,
		WindowStart: now.Add(-time.Hour),
		AsOf:        now,
		Learned: []mobileBriefCandidate{{
			SourceKind: "learning", SourceID: "legacy-learning", Title: "Legacy private learning", At: now,
		}},
	})
	if len(brief.Sections.Learned) != 1 {
		t.Fatalf("missing legacy learning fixture: %+v", brief.Sections.Learned)
	}
	dir, err := mobileBriefStateDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := newMobileBriefStore(dir).save(brief, now); err != nil {
		t.Fatal(err)
	}
	_, release := blockTodayLookup(t, app.activity.store.db)
	done := make(chan error, 1)
	go func() {
		_, err := app.resolveTodayChatItem(context.Background(), TodayChatContextRef{
			SourceKind: "brief_item", SourceID: brief.Sections.Learned[0].ID, BriefID: brief.BriefID,
		}, todayChatSources{}, now)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, errTodayChatItemUnavailable) {
			t.Fatalf("legacy learning resolved with error %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("legacy learning entered the unrelated composite source collector")
	}
	release()
}
