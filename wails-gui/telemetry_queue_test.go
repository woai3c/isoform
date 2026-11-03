package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/posthog/posthog-go"
)

type receivedTelemetryEvent struct {
	Event      string         `json:"event"`
	DistinctID string         `json:"distinct_id"`
	Properties map[string]any `json:"properties"`
}

// newHTTPTelemetry uses the real SDK and a local HTTP endpoint. A long flush
// interval keeps captures queued until the setting change or explicit Close.
func newHTTPTelemetry(t *testing.T, tier string) (*App, func() []receivedTelemetryEvent) {
	t.Helper()
	home, err := os.MkdirTemp("/tmp", "telemetry-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	t.Setenv("HOME", home)
	telemetryTierMu.Lock()
	previousTier := telemetryTier
	telemetryTier = tier
	telemetryTierMu.Unlock()
	t.Cleanup(func() {
		telemetryTierMu.Lock()
		telemetryTier = previousTier
		telemetryTierMu.Unlock()
	})

	var mu sync.Mutex
	var events []receivedTelemetryEvent
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var batch struct {
			Events []receivedTelemetryEvent `json:"batch"`
		}
		if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
			t.Errorf("decode telemetry batch: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		events = append(events, batch.Events...)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	previousKey, previousHost := DefaultDesktopPostHogKey, DefaultDesktopPostHogHost
	DefaultDesktopPostHogKey, DefaultDesktopPostHogHost = "test-key", server.URL
	t.Cleanup(func() { DefaultDesktopPostHogKey, DefaultDesktopPostHogHost = previousKey, previousHost })
	telemetry := &PostHogTelemetry{
		key: "test-key",
		config: posthog.Config{
			Endpoint: server.URL,
			Interval: time.Hour,
		},
	}
	telemetry.SetSharingEnabled(tier != TelemetryTierOff)
	t.Cleanup(telemetry.Close)
	return &App{telemetry: telemetry}, func() []receivedTelemetryEvent {
		mu.Lock()
		defer mu.Unlock()
		return append([]receivedTelemetryEvent(nil), events...)
	}
}

func TestTelemetryOptOutDiscardsQueuedEventsBeforeShutdown(t *testing.T) {
	app, received := newHTTPTelemetry(t, TelemetryTierDetailed)
	app.telemetry.Capture("app.open", nil)
	if err := app.SetTelemetryTier(TelemetryTierOff); err != nil {
		t.Fatal(err)
	}
	app.telemetry.Capture("chat.turn_sent", nil)
	app.telemetry.Close()
	if events := received(); len(events) != 0 {
		t.Fatalf("opt-out sent queued telemetry: %+v", events)
	}
}

func TestTelemetryOptInStartsWithEmptyQueue(t *testing.T) {
	app, received := newHTTPTelemetry(t, TelemetryTierDetailed)
	app.telemetry.Capture("app.open", nil)
	if err := app.SetTelemetryTier(TelemetryTierOff); err != nil {
		t.Fatal(err)
	}
	if err := app.SetTelemetryTier(TelemetryTierDetailed); err != nil {
		t.Fatal(err)
	}
	app.telemetry.Capture("iphone.paired", nil)
	app.telemetry.Close()
	events := received()
	if len(events) != 1 || events[0].Event != "iphone.paired" {
		t.Fatalf("opt-in sent events %+v, want only iphone.paired", events)
	}
	if _, err := uuid.Parse(events[0].DistinctID); err != nil {
		t.Fatalf("event identity %q is not an anonymous UUID", events[0].DistinctID)
	}
	for _, key := range []string{"$user_id", "email", "name", "display_name"} {
		if value, ok := events[0].Properties[key]; ok {
			t.Errorf("event contains profile identity %s=%v", key, value)
		}
	}
}

func TestTelemetryShutdownDropsQueueWhenSharingIsOff(t *testing.T) {
	app, received := newHTTPTelemetry(t, TelemetryTierDetailed)
	app.telemetry.Capture("app.open", nil)
	// Exercise the network-boundary gate even before the setter retires the
	// old client: shutdown and scheduled flushes must observe the new tier.
	telemetryTierMu.Lock()
	telemetryTier = TelemetryTierOff
	telemetryTierMu.Unlock()
	app.telemetry.Close()
	if events := received(); len(events) != 0 {
		t.Fatalf("shutdown sent telemetry while sharing was off: %+v", events)
	}
}

func TestTelemetryStartupOffDoesNotInitializeClient(t *testing.T) {
	app, received := newHTTPTelemetry(t, TelemetryTierOff)
	app.telemetry = NewPostHogTelemetry()
	t.Cleanup(app.telemetry.Close)
	if app.telemetry.client != nil || app.telemetry.enabled.Load() {
		t.Fatal("startup with sharing off initialized a telemetry client")
	}
	app.telemetry.Capture("app.open", nil)
	if err := app.SetTelemetryTier(TelemetryTierDetailed); err != nil {
		t.Fatal(err)
	}
	app.telemetry.Capture("iphone.paired", nil)
	app.telemetry.Close()
	if events := received(); len(events) != 1 || events[0].Event != "iphone.paired" {
		t.Fatalf("startup opt-in sent events %+v, want only iphone.paired", events)
	}
}

func TestTelemetryOptOutCancelsInflightUploadAndBroadcastsPromptly(t *testing.T) {
	app, _ := newHTTPTelemetry(t, TelemetryTierDetailed)
	app.telemetry.Close()
	started := make(chan struct{})
	canceled := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		select {
		case <-r.Context().Done():
			close(canceled)
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)
	app.telemetry.config.Endpoint = server.URL
	app.telemetry.config.BatchSize = 1
	app.telemetry.config.BatchUploadTimeout = time.Minute
	app.telemetry.SetSharingEnabled(true)
	app.eventBroadcaster = NewEventBroadcaster()
	events := app.eventBroadcaster.AddClient("telemetry-inflight")
	defer app.eventBroadcaster.RemoveClient("telemetry-inflight")
	app.telemetry.Capture("app.open", nil)
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("telemetry upload did not start")
	}

	done := make(chan error, 1)
	go func() { done <- app.SetTelemetryTier(TelemetryTierOff) }()
	select {
	case payload := <-events:
		var event BrowserEvent
		if err := json.Unmarshal(payload, &event); err != nil {
			t.Fatal(err)
		}
		if event.Event != "telemetry:tier-changed" || len(event.Args) != 1 || event.Args[0] != TelemetryTierOff {
			t.Fatalf("unexpected opt-out event: %+v", event)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("opt-out broadcast waited for the telemetry upload timeout")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	select {
	case <-canceled:
	case <-time.After(3 * time.Second):
		t.Fatal("opt-out did not cancel the in-flight HTTP request")
	}
}
