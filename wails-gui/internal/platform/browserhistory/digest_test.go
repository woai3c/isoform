package browserhistory

import (
	"testing"
	"time"
)

func TestSearchQuery(t *testing.T) {
	cases := []struct {
		url, title, want string
		ok               bool
	}{
		{"https://www.google.com/search", "wails webview clipboard - Google Search", "wails webview clipboard", true},
		{"https://www.google.co.uk/search", "eventkit reminders - Google Search", "eventkit reminders", true},
		{"https://www.bing.com/search", "go sqlite wal - Bing", "go sqlite wal", true},
		{"https://duckduckgo.com/", "nspasteboard concealed at DuckDuckGo", "nspasteboard concealed", true},
		{"https://www.baidu.com/s", "提醒事项 API_百度搜索", "提醒事项 API", true},
		{"https://www.google.com/maps", "Maps - Google Search", "", false},
		{"https://news.google.com/search", "Headlines", "", false},
		{"https://example.com/search", "x - Google Search", "", false},
		{"https://google.attacker.example/search", "planted - Google Search", "", false},
	}
	for _, tc := range cases {
		got, ok := SearchQuery(tc.url, tc.title)
		if got != tc.want || ok != tc.ok {
			t.Errorf("SearchQuery(%q, %q) = %q, %v; want %q, %v", tc.url, tc.title, got, ok, tc.want, tc.ok)
		}
	}
}

func TestTopSites(t *testing.T) {
	at := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	visits := []Visit{
		{URL: "https://github.com/a", At: at},
		{URL: "https://www.github.com/b", At: at.Add(time.Minute)},
		{URL: "https://docs.google.com/document/d/...", At: at},
		{URL: "https://www.google.com/search", Title: "x - Google Search", At: at},
		{URL: "http://localhost:34115/", At: at},
		{URL: "chrome://settings", At: at},
		{URL: "https://news.ycombinator.com/", At: at.Add(time.Hour)},
	}
	sites := TopSites(visits, 2)
	if len(sites) != 2 || sites[0].Host != "github.com" || sites[0].Visits != 2 || !sites[0].Last.Equal(at.Add(time.Minute)) {
		t.Fatalf("sites %+v", sites)
	}
	if sites[1].Host != "news.ycombinator.com" {
		t.Fatalf("ties break on the latest visit: %+v", sites)
	}
}

func TestResearchThreads(t *testing.T) {
	at := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	visits := []Visit{
		{URL: "https://www.google.com/search", Title: "eventkit reminders - Google Search", At: at},
		{URL: "https://developer.apple.com/documentation/eventkit", Title: "EventKit | Apple Developer", At: at.Add(time.Minute)},
		{URL: "https://developer.apple.com/documentation/eventkit", Title: "EventKit | Apple Developer", At: at.Add(2 * time.Minute)},
		{URL: "https://stackoverflow.com/questions/1", Title: "Reading reminders on macOS", At: at.Add(5 * time.Minute)},
		{URL: "https://github.com/", Title: "GitHub", At: at.Add(40 * time.Minute)}, // too late for the thread
		{URL: "https://www.google.com/search", Title: "nspasteboard concealed - Google Search", At: at.Add(2 * time.Hour)},
		{URL: "https://nspasteboard.org/", Title: "NSPasteboard.org", At: at.Add(2*time.Hour + time.Minute)},
		{URL: "https://www.google.com/search", Title: "EventKit Reminders - Google Search", At: at.Add(3 * time.Hour)},
	}
	threads := ResearchThreads(visits, 5)
	if len(threads) != 2 {
		t.Fatalf("threads %+v", threads)
	}
	// The same query searched again joins its thread and moves it first.
	if threads[0].Query != "eventkit reminders" || threads[0].Pages != 2 || len(threads[0].Titles) != 2 || !threads[0].Last.Equal(at.Add(3*time.Hour)) {
		t.Fatalf("first thread %+v", threads[0])
	}
	if threads[1].Query != "nspasteboard concealed" || threads[1].Pages != 1 || threads[1].Titles[0] != "NSPasteboard.org" {
		t.Fatalf("second thread %+v", threads[1])
	}
	if got := ResearchThreads(visits, 1); len(got) != 1 {
		t.Fatalf("limit: %+v", got)
	}
}
