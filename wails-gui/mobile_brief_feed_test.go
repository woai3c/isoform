package main

import (
	"context"
	"encoding/xml"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func feedBrief(id, slot, date, asOf string, audio *MobileBriefAudio, narrative string) MobileBriefV1 {
	brief := MobileBriefV1{BriefID: id, Slot: slot, LocalDate: date, AsOf: asOf, Audio: audio}
	if narrative != "" {
		brief.Narrative = []MobileBriefPara{{Text: narrative}}
	}
	return brief
}

func TestMobileBriefFeedListsSpokenBriefsOnly(t *testing.T) {
	briefs := []MobileBriefV1{
		feedBrief("2026-10-14-morning", "morning", "2026-10-14", "2026-10-14T07:00:30Z", &MobileBriefAudio{Mime: mobileBriefAudioMimeM4A, Bytes: 1234, DurationSeconds: 95}, "You moved the launch forward."),
		feedBrief("2026-10-13-evening", "evening", "2026-10-13", "2026-10-13T18:00:00Z", nil, ""),
		feedBrief("2026-10-13-morning", "morning", "2026-10-13", "2026-10-13T07:00:00Z", &MobileBriefAudio{Mime: mobileBriefAudioMimeWAV, Bytes: 99, DurationSeconds: 3661}, ""),
	}
	items := mobileBriefFeedItemsFrom(briefs)
	if len(items) != 2 || items[0].BriefID != "2026-10-14-morning" || items[1].BriefID != "2026-10-13-morning" {
		t.Fatalf("items = %+v", items)
	}
	body, err := mobileBriefFeedXML(items, "Alex's MacBook Pro", "http://127.0.0.1:47831/feed/abc")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, want := range []string{
		`<rss version="2.0" xmlns:itunes="http://www.itunes.com/dtds/podcast-1.0.dtd">`,
		"<title>Isoform brief</title>",
		"<itunes:block>Yes</itunes:block>",
		"<title>Morning brief, Wednesday, October 14</title>",
		`<enclosure url="http://127.0.0.1:47831/feed/abc/audio/2026-10-14-morning.m4a" length="1234" type="audio/mp4">`,
		`<enclosure url="http://127.0.0.1:47831/feed/abc/audio/2026-10-13-morning.wav" length="99" type="audio/wav">`,
		"<itunes:duration>0:01:35</itunes:duration>",
		"<itunes:duration>1:01:01</itunes:duration>",
		"<description>You moved the launch forward.</description>",
		`<guid isPermaLink="false">2026-10-14-morning</guid>`,
		"Wed, 14 Oct 2026 07:00:30 +0000",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("feed lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "2026-10-13-evening") {
		t.Fatal("a brief without audio must not be an episode")
	}
	var parsed struct {
		Channel struct {
			Items []struct {
				Title string `xml:"title"`
			} `xml:"item"`
		} `xml:"channel"`
	}
	if err := xml.Unmarshal(body, &parsed); err != nil || len(parsed.Channel.Items) != 2 {
		t.Fatalf("feed is not well-formed XML: %v", err)
	}

	many := make([]MobileBriefV1, 0, 20)
	for i := 0; i < 20; i++ {
		day := time.Date(2026, 10, 1, 7, 0, 0, 0, time.UTC).AddDate(0, 0, i)
		many = append(many, feedBrief(day.Format("2006-01-02")+"-morning", "morning", day.Format("2006-01-02"), day.Format(time.RFC3339), &MobileBriefAudio{Mime: mobileBriefAudioMimeM4A, Bytes: 1}, ""))
	}
	if got := len(mobileBriefFeedItemsFrom(many)); got != mobileBriefFeedItems {
		t.Fatalf("the feed keeps %d items, want %d", got, mobileBriefFeedItems)
	}
	if mobileBriefFeedDuration(0) != "" || mobileBriefFeedDuration(59.6) != "0:01:00" {
		t.Fatal("duration formatting")
	}
}

func TestMobileBriefFeedServerServesUnderTheSecretOnly(t *testing.T) {
	stateDir := t.TempDir()
	audioDir := filepath.Join(stateDir, "briefs")
	if err := os.MkdirAll(audioDir, 0700); err != nil {
		t.Fatal(err)
	}
	wav := testWAV(1)
	if err := os.WriteFile(filepath.Join(audioDir, "2026-10-14-morning.wav"), wav, 0600); err != nil {
		t.Fatal(err)
	}
	briefs := []MobileBriefV1{feedBrief("2026-10-14-morning", "morning", "2026-10-14", "2026-10-14T07:00:30Z", &MobileBriefAudio{Mime: mobileBriefAudioMimeWAV, Bytes: len(wav), DurationSeconds: 1}, "")}
	server := newMobileBriefFeedServer(stateDir, audioDir, func() string { return "Test Mac" }, func() []MobileBriefV1 { return briefs })
	url, err := server.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Stop()
	if again, _ := server.Start(); again != url {
		t.Fatal("starting twice must keep the URL")
	}
	if !strings.HasPrefix(url, "http://127.0.0.1:") || !strings.HasSuffix(url, "/brief.xml") || server.URL() != url {
		t.Fatalf("feed url = %q", url)
	}
	secret, _ := os.ReadFile(filepath.Join(stateDir, mobileBriefFeedSecretFile))
	if !strings.Contains(url, strings.TrimSpace(string(secret))) {
		t.Fatal("the URL must carry the stored secret")
	}

	get := func(path string) (int, string, []byte) {
		resp, err := http.Get(path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, resp.Header.Get("Content-Type"), body
	}
	status, contentType, body := get(url)
	if status != http.StatusOK || !strings.HasPrefix(contentType, "application/rss+xml") {
		t.Fatalf("feed: %d %s", status, contentType)
	}
	base := strings.TrimSuffix(url, "/brief.xml")
	if !strings.Contains(string(body), base+"/audio/2026-10-14-morning.wav") {
		t.Fatalf("feed enclosure must point at this server:\n%s", body)
	}
	status, contentType, body = get(base + "/audio/2026-10-14-morning.wav")
	if status != http.StatusOK || contentType != mobileBriefAudioMimeWAV || len(body) != len(wav) {
		t.Fatalf("audio: %d %s %d bytes", status, contentType, len(body))
	}
	for _, bad := range []string{
		strings.Replace(url, strings.TrimSpace(string(secret)), strings.Repeat("0", 32), 1),
		base + "/audio/2026-10-14-morning.m4a",
		base + "/audio/../feed-secret",
		base + "/audio/2026-10-14-evening.wav",
		base + "/other",
		strings.TrimSuffix(base, strings.TrimSpace(string(secret))) + "brief.xml",
	} {
		if status, _, _ := get(bad); status != http.StatusNotFound {
			t.Fatalf("%s answered %d, want 404", bad, status)
		}
	}

	// The secret survives a restart of the server.
	server.Stop()
	if server.URL() != "" {
		t.Fatal("a stopped server has no URL")
	}
	restarted := newMobileBriefFeedServer(stateDir, audioDir, nil, nil)
	again, err := restarted.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Stop()
	if !strings.Contains(again, strings.TrimSpace(string(secret))) {
		t.Fatal("the restarted server must keep the secret path")
	}
	if _, err := newMobileBriefFeedServer("", audioDir, nil, nil).Start(); err == nil {
		t.Fatal("without a state dir there is no feed")
	}
}

func TestMobileBriefScheduleFeedSwitchStartsAndStopsTheServer(t *testing.T) {
	clock := &fakeMobileBriefClock{now: mobileBriefTestDay(10, 0)}
	svc, _ := newTestMobileBriefService(t, clock)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.Start(ctx)
	defer svc.Stop()

	view, err := svc.Schedule()
	if err != nil || view.FeedEnabled || view.FeedURL != "" {
		t.Fatalf("the feed is off by default: %+v %v", view, err)
	}
	view, err = svc.SetSchedule(MobileBriefSchedule{Enabled: true, Morning: "07:00", Evening: "18:00", FeedEnabled: true})
	if err != nil || !view.FeedEnabled || !strings.HasPrefix(view.FeedURL, "http://127.0.0.1:") {
		t.Fatalf("turning the feed on must start it: %+v %v", view, err)
	}
	resp, err := http.Get(view.FeedURL)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("feed not served: %v %v", resp, err)
	}
	resp.Body.Close()
	if !svc.config.load().Feed {
		t.Fatal("the switch must be saved")
	}
	view, err = svc.SetSchedule(MobileBriefSchedule{Enabled: true, Morning: "07:00", Evening: "18:00", FeedEnabled: false})
	if err != nil || view.FeedEnabled || view.FeedURL != "" {
		t.Fatalf("turning the feed off must stop it: %+v %v", view, err)
	}
}

func TestMobileBriefFeedStopReleasesPortBeforeServeStarts(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	address := listener.Addr().String()
	// Start has bound the port, but its Serve goroutine has not run yet.
	feed := &mobileBriefFeedServer{listener: listener, server: &http.Server{}}
	feed.Stop()
	replacement, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("Stop returned before releasing the feed port: %v", err)
	}
	_ = replacement.Close()
}
