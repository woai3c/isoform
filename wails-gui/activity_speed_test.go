package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"wails-gui/internal/platform/activitymon"
)

// A synthetic busy morning; never reads the running computer's activity.
func TestActivitySpeedFixture(t *testing.T) {
	app := newThreadTestApp(t)
	day, _ := threadTestDay(t)
	for i := 0; i < 26; i++ {
		start := at(day, 8, i*8)
		seedThreadMoment(t, app, threadFixtureMoment{id: fmt.Sprintf("fixture-%02d", i), title: fmt.Sprintf("Public fixture task %d", i), app: "Finder", bundle: "com.apple.finder", start: start, end: start.Add(5 * time.Minute)})
		events := make([]activitymon.ActivityEvent, 100)
		for j := range events {
			events[j] = activitymon.ActivityEvent{Kind: activitymon.EventFocus, Timestamp: start.UnixMilli() + int64(j)*1000, AppName: "Finder", AppBundleID: "com.apple.finder", WindowTitle: "Public fixture", TypedText: "Ordinary fixture text"}
		}
		if err := app.activity.store.SaveSession(ActivitySessionRow{ID: fmt.Sprintf("capture-%02d", i), StartTime: start.UnixMilli(), EndTime: start.Add(5 * time.Minute).UnixMilli(), AppNames: `["Finder"]`}, events, nil); err != nil {
			t.Fatal(err)
		}
	}

	// Full library files are intentionally real: the old loading gate waited
	// for their contents even though the sidebar only needed counts.
	kd, err := knowledgeDir()
	if err != nil {
		t.Fatal(err)
	}
	md, err := memoryDir()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200; i++ {
		name := fmt.Sprintf("fixture-%03d.md", i)
		text := fmt.Sprintf("# Fixture learning %d\n- **Date**: %s\n\n%s", i, day.Format("2006-01-02"), strings.Repeat("Public synthetic library text. ", 256))
		for _, dir := range []string{kd, md} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	monthReader, optimized := any(app).(interface {
		GetActivityMonthStats(string) ([]DayStatBrief, error)
	})
	times := map[string]float64{}
	payload := map[string]any{"date": day.Format("2006-01-02"), "optimized": optimized}
	var mu sync.Mutex
	var wg sync.WaitGroup
	started := time.Now()
	read := func(name string, fn func() (any, error)) {
		defer wg.Done()
		start := time.Now()
		data, err := fn()
		if err != nil {
			t.Errorf("%s: %v", name, err)
		}
		mu.Lock()
		times[name] = float64(time.Since(start).Microseconds()) / 1000
		payload[name] = data
		mu.Unlock()
	}
	launch := func(name string, fn func() (any, error)) { wg.Add(1); go read(name, fn) }
	loadLibraries := func() {
		launch("GetKnowledgeEntries", func() (any, error) { return app.GetKnowledgeEntries() })
		launch("GetMemoryEntries", func() (any, error) { return app.GetMemoryEntries() })
	}
	launch("GetActivitiesInRange", func() (any, error) {
		return app.GetActivitiesInRange(day.UnixMilli(), day.AddDate(0, 0, 1).UnixMilli())
	})
	launch("EnsureDailySummary", func() (any, error) {
		data, err := app.GetDailySummary(day.Format("2006-01-02"))
		if optimized {
			loadLibraries()
		}
		return data, err
	})
	first := time.Date(day.Year(), day.Month(), 1, 0, 0, 0, 0, time.Local)
	first = first.AddDate(0, 0, -(int(first.Weekday())+6)%7)
	if !optimized {
		loadLibraries()
		// The old closed picker launches six weekly summaries and a second
		// copy of both library reads alongside the visible page's calls.
		for i := 0; i < 6; i++ {
			date := first.AddDate(0, 0, i*7).Format("2006-01-02")
			launch(fmt.Sprintf("week-%d", i), func() (any, error) { return app.GetWeeklySummary(date) })
		}
		launch("heatmap-knowledge", func() (any, error) { return app.GetKnowledgeEntries() })
		launch("heatmap-memory", func() (any, error) { return app.GetMemoryEntries() })
	}
	wg.Wait()
	full := time.Since(started)
	if len(payload["GetActivitiesInRange"].([]ActivityView)) != 26 {
		t.Fatal("fixture lost rows")
	}
	arrowStart := time.Now()
	if optimized {
		if _, err := monthReader.GetActivityMonthStats(day.Format("2006-01")); err != nil {
			t.Fatal(err)
		}
	} else {
		for i := 0; i < 6; i++ {
			if _, err := app.GetWeeklySummary(first.AddDate(0, 0, i*7).Format("2006-01-02")); err != nil {
				t.Fatal(err)
			}
		}
	}
	arrow := time.Since(arrowStart)
	payload["timingsMs"] = times
	payload["fullMs"] = float64(full.Microseconds()) / 1000
	payload["monthMs"] = float64(arrow.Microseconds()) / 1000
	if path := os.Getenv("ACTIVITY_SPEED_FIXTURE"); path != "" {
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("fixture: optimized=%t; 26 activities, 52 sessions, 2626 events, 200 knowledge + 200 memory files; request ms=%v full=%s month=%s", optimized, times, full, arrow)
}
