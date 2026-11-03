package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// drainBrowserEvents returns the names and first arguments the broadcaster
// has queued, oldest first.
func drainBrowserEvents(t *testing.T, events chan []byte) (names []string, args []json.RawMessage) {
	t.Helper()
	for {
		select {
		case raw := <-events:
			var event struct {
				Event string            `json:"event"`
				Args  []json.RawMessage `json:"args"`
			}
			if err := json.Unmarshal(raw, &event); err != nil {
				t.Fatal(err)
			}
			names = append(names, event.Event)
			if len(event.Args) > 0 {
				args = append(args, event.Args[0])
			} else {
				args = append(args, nil)
			}
		default:
			return names, args
		}
	}
}

// Snappy 1.0: Today follows a brief's build through "brief:building" instead
// of a 4-second poll. Start comes before the stored brief, and stop comes
// after it, once Building already reads false.
func TestMobileBriefBuildAnnouncesStartAndStop(t *testing.T) {
	clock := &fakeMobileBriefClock{now: mobileBriefTestDay(7, 0)}
	svc, _ := newTestMobileBriefService(t, clock)
	svc.app.eventBroadcaster = NewEventBroadcaster()
	events := svc.app.eventBroadcaster.AddClient("brief-building")
	svc.checkDue(context.Background())
	if !svc.store.exists("2026-10-09-morning") {
		t.Fatal("the morning brief was not made")
	}
	names, args := drainBrowserEvents(t, events)
	start, stored, stop := -1, -1, -1
	for i, name := range names {
		switch name {
		case mobileBriefBuildingEvent:
			if string(args[i]) == "true" && start < 0 {
				start = i
			}
			if string(args[i]) == "false" {
				stop = i
			}
		case "brief:stored":
			if stored < 0 {
				stored = i
			}
		default:
		}
	}
	if start < 0 || stored < 0 || stop < 0 || !(start < stored && stored < stop) {
		t.Fatalf("want building true, brief:stored, building false in order, got %v %s", names, args)
	}
	if svc.Building() {
		t.Fatal("the build still reads as running after its stop event")
	}
}

// An on-demand brief announces its build too, including one that fails.
func TestMobileBriefOnDemandBuildAnnouncesStop(t *testing.T) {
	clock := &fakeMobileBriefClock{now: mobileBriefTestDay(10, 0)}
	svc, _ := newTestMobileBriefService(t, clock)
	svc.app.eventBroadcaster = NewEventBroadcaster()
	events := svc.app.eventBroadcaster.AddClient("brief-on-demand")
	svc.gather = func(context.Context, string, time.Time, time.Time) (mobileBriefInput, error) {
		return mobileBriefInput{}, context.DeadlineExceeded
	}
	if result, err := svc.RequestMobileBrief(mobileBriefReasonSettings); err != nil || result.Status != "started" {
		t.Fatalf("request = %+v, %v", result, err)
	}
	svc.wg.Wait()
	names, args := drainBrowserEvents(t, events)
	var sawStart, sawStop bool
	for i, name := range names {
		if name != mobileBriefBuildingEvent {
			continue
		}
		sawStart = sawStart || string(args[i]) == "true"
		sawStop = sawStop || string(args[i]) == "false"
	}
	if !sawStart || !sawStop || svc.Building() {
		t.Fatalf("a failed on-demand build must announce its start and stop: %v %s", names, args)
	}
}

// Today receives only the completion of the generation it joined, even if
// unrelated stored-brief notifications arrive while it is running.
func TestTodayBriefRequestTracksJoinedGeneration(t *testing.T) {
	clock := &fakeMobileBriefClock{now: mobileBriefTestDay(10, 0)}
	svc, _ := newTestMobileBriefService(t, clock)
	svc.app.eventBroadcaster = NewEventBroadcaster()
	events := svc.app.eventBroadcaster.AddClient("today-request")
	entered := make(chan struct{})
	release := make(chan struct{})
	svc.gather = func(context.Context, string, time.Time, time.Time) (mobileBriefInput, error) {
		close(entered)
		<-release
		return mobileBriefInput{}, context.DeadlineExceeded
	}
	if _, err := svc.RequestTodayMobileBrief("bad token"); err == nil {
		t.Fatal("tokens must not contain arbitrary content")
	}
	if result, err := svc.RequestTodayMobileBrief("request-one"); err != nil || result.Status != "started" {
		t.Fatalf("first request = %+v, %v", result, err)
	}
	<-entered
	if result, err := svc.RequestTodayMobileBrief("request-two"); err != nil || result.Status != "running" {
		close(release)
		t.Fatalf("joined request = %+v, %v", result, err)
	}
	svc.app.emitBrowserEvent("brief:stored", "unrelated")
	close(release)
	svc.wg.Wait()
	got := map[string]bool{}
	for {
		select {
		case raw := <-events:
			var event struct {
				Event string            `json:"event"`
				Args  []json.RawMessage `json:"args"`
			}
			if err := json.Unmarshal(raw, &event); err != nil {
				t.Fatal(err)
			}
			if event.Event != "today:brief-request-finished" {
				continue
			}
			if len(event.Args) != 2 {
				t.Fatalf("bad completion: %s", raw)
			}
			var token string
			var success bool
			if json.Unmarshal(event.Args[0], &token) != nil || json.Unmarshal(event.Args[1], &success) != nil {
				t.Fatalf("bad completion: %s", raw)
			}
			got[token] = success
		default:
			if len(got) != 2 || got["request-one"] || got["request-two"] {
				t.Fatalf("failed generation outcomes = %v", got)
			}
			return
		}
	}
}
