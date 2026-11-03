//go:build darwin

package main

import (
	"bytes"
	"encoding/json"
	"log"
	"strings"
	"testing"
	"time"
)

// menuBarBlockedBriefSource stands in for a brief service whose latest-brief
// check is slow: the play click must not wait for it.
type menuBarBlockedBriefSource struct {
	mobileBriefSource
	release chan struct{}
}

func (s menuBarBlockedBriefSource) LatestMobileBrief() (MobileBriefV1, bool) {
	<-s.release
	return MobileBriefV1{}, false
}

func enableMenuBarForTest(t *testing.T) {
	t.Helper()
	menuBarPresentation.Lock()
	stopped := menuBarPresentation.stopped
	menuBarPresentation.stopped = false
	menuBarPresentation.Unlock()
	t.Cleanup(func() {
		menuBarPresentation.Lock()
		menuBarPresentation.stopped = stopped
		menuBarPresentation.Unlock()
	})
}

// Bo, 2026-10-27: play from the menu bar was slow to react. The click must
// reach Today's player at once, whatever the brief check costs.
func TestMenuBarPlayReachesThePlayerWithoutWaitingForTheBriefCheck(t *testing.T) {
	app := mainChatTestApp(t)
	enableMenuBarForTest(t)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	app.mobileBriefSvc = menuBarBlockedBriefSource{release: release}
	events := app.eventBroadcaster.AddClient("menubar-play")
	defer app.eventBroadcaster.RemoveClient("menubar-play")
	go func() { _ = app.runMenuBarCommand("play:2026-10-27-evening") }()
	select {
	case payload := <-events:
		var event struct {
			Event string   `json:"event"`
			Args  []string `json:"args"`
		}
		if json.Unmarshal(payload, &event) != nil || event.Event != "menubar:brief-play" || len(event.Args) != 1 || event.Args[0] != "2026-10-27-evening" {
			t.Fatalf("play event=%s", payload)
		}
	case <-time.After(time.Second):
		t.Fatal("play waited for the brief check before telling Today's player")
	}
}

func TestMenuBarPlayRejectsAMalformedBriefID(t *testing.T) {
	app := mainChatTestApp(t)
	enableMenuBarForTest(t)
	events := app.eventBroadcaster.AddClient("menubar-play-invalid")
	defer app.eventBroadcaster.RemoveClient("menubar-play-invalid")
	var buf bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(previous) })
	if err := app.runMenuBarCommand("play:../../etc"); err != nil {
		t.Fatal(err)
	}
	select {
	case payload := <-events:
		t.Fatalf("a malformed id must not reach the player: %s", payload)
	case <-time.After(100 * time.Millisecond):
	}
	if !strings.Contains(buf.String(), "[menubar] action=play brief=") || !strings.Contains(buf.String(), "result=failed:invalid-id") {
		t.Fatalf("missing outcome line: %q", buf.String())
	}
}

// Every play click ends in one diagnosable line: loading at dispatch, then
// started with the elapsed time once Today's player reports it playing.
func TestMenuBarPlayLogsItsOutcome(t *testing.T) {
	app := mainChatTestApp(t)
	enableMenuBarForTest(t)
	events := app.eventBroadcaster.AddClient("menubar-play-log")
	defer app.eventBroadcaster.RemoveClient("menubar-play-log")
	var buf bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(previous) })
	const id = "2026-10-27-evening"
	if err := app.runMenuBarCommand("play:" + id); err != nil {
		t.Fatal(err)
	}
	<-events
	app.SetMenuBarBriefPlayback(id, "loading", 0, 0)
	app.SetMenuBarBriefPlayback(id, "playing", 0, 60)
	logged := buf.String()
	if !strings.Contains(logged, "[menubar] action=play brief="+id+" result=loading dispatch_ms=") {
		t.Fatalf("missing dispatch line: %q", logged)
	}
	if !strings.Contains(logged, "[menubar] action=play brief="+id+" result=started elapsed_ms=") {
		t.Fatalf("missing started line: %q", logged)
	}
	if strings.Count(logged, "result=started") != 1 {
		t.Fatalf("one started line per click: %q", logged)
	}
}
