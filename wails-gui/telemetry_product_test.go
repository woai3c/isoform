package main

import (
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestTelemetryProductEventShapesAndPrivacy(t *testing.T) {
	app, received := newHTTPTelemetry(t, TelemetryTierDetailed)
	app.telemetry.setLocale("zh-CN")
	private := map[string]any{
		"screen_text": "secret screen", "audio": "secret audio", "filename": "secret.pdf",
		"url": "https://private.example", "people": []string{"a person"},
		"memory": "secret memory", "chat_text": "secret chat", "email": "private@example.com",
		"app_version": "spoofed", "count": 42, "session_id": "local-chat-id",
	}
	app.telemetry.Capture("app.open", private)
	app.telemetry.Capture("chat.turn_sent", private)
	app.telemetry.Capture("playbook.run", private)
	app.telemetry.Capture("iphone.paired", private)
	app.RecordFirstBriefSeen()
	app.RecordFirstBriefSeen()
	app.telemetry.recordProductActivity(30, uuid.NewString(), time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC))
	app.telemetry.Capture("agentloop.step.llm.completed", private)
	app.telemetry.Capture("secret event name", private)
	app.telemetry.Close()

	events := received()
	if len(events) != 7 {
		t.Fatalf("events = %+v, want exactly seven product events", events)
	}
	common := map[string]bool{
		"app_version": true, "flavor": true, "build_channel": true, "os": true, "os_version": true, "locale": true, "telemetry_tier": true,
		"$process_person_profile": true, "$geoip_disable": true,
		"$lib": true, "$lib_version": true, "$os": true, "$os_version": true, "$os_distro": true, "$go_version": true,
	}
	seen := map[string]bool{}
	for _, event := range events {
		seen[event.Event] = true
		if event.DistinctID != app.telemetry.anonymousID {
			t.Fatalf("identity differs: %+v", event)
		}
		if _, err := uuid.Parse(event.DistinctID); err != nil {
			t.Fatal(err)
		}
		props := event.Properties
		if props["app_version"] != strings.TrimSpace(Version) || props["flavor"] != appFlavor() || props["build_channel"] != "development" || props["os"] != telemetryOS() || props["os_version"] == "" || props["locale"] != "zh-CN" || props["telemetry_tier"] != TelemetryTierDetailed {
			t.Fatalf("incorrect common properties: %+v", props)
		}
		if props["$process_person_profile"] != false || props["$geoip_disable"] != true {
			t.Fatalf("profile or geolocation enabled: %+v", props)
		}
		for key := range props {
			allowed := common[key] || (event.Event == "chat.turn_sent" && key == "count") || (event.Event == "app.daily_active" && key == "date") || (event.Event == "app.active_time" && (key == "active_seconds" || key == "session_id"))
			if !allowed {
				t.Errorf("unexpected property %q on %s", key, event.Event)
			}
		}
		if event.Event == "chat.turn_sent" && props["count"] != float64(1) {
			t.Fatalf("chat event is not count-only: %+v", props)
		}
	}
	if len(seen) != 7 {
		t.Fatalf("missing or duplicate event shapes: %+v", seen)
	}
}

func TestTelemetryInstallIdentityAndMilestonesSurviveRestart(t *testing.T) {
	app, received := newHTTPTelemetry(t, TelemetryTierDetailed)
	now := time.Date(2026, 10, 16, 23, 59, 0, 0, time.UTC)
	session := uuid.NewString()
	app.RecordFirstBriefSeen()
	app.telemetry.recordProductActivity(0, session, now)
	app.telemetry.recordProductActivity(10, session, now)
	id := app.telemetry.anonymousID
	app.telemetry.Close()

	app.telemetry = NewPostHogTelemetry()
	app.RecordFirstBriefSeen()
	app.telemetry.recordProductActivity(0, session, now)
	app.telemetry.recordProductActivity(0, session, now.Add(2*time.Minute))
	app.telemetry.Close()
	if app.telemetry.anonymousID != id {
		t.Fatal("install identity changed across clients")
	}
	counts := map[string]int{}
	for _, event := range received() {
		counts[event.Event]++
		if event.DistinctID != id {
			t.Fatalf("unexpected distinct id %q", event.DistinctID)
		}
	}
	if !reflect.DeepEqual(counts, map[string]int{"brief.first_seen": 1, "app.daily_active": 2, "app.active_time": 1}) {
		t.Fatalf("milestones not retained: %+v", counts)
	}
	info, err := os.Stat(filepath.Join(app.telemetry.dataDir, "telemetry_install_id"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("identity permissions: %v (%v)", info, err)
	}
}

func TestTelemetryInstallIDConcurrentCreationAndCorruption(t *testing.T) {
	dir := t.TempDir()
	ids := make(chan string, 12)
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := loadTelemetryInstallID(dir)
			if err != nil {
				t.Error(err)
			}
			ids <- id
		}()
	}
	wg.Wait()
	close(ids)
	id := <-ids
	for next := range ids {
		if id != next {
			t.Fatalf("concurrent creation changed ID: %q vs %q", id, next)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "telemetry_install_id"), []byte("a name is not an ID"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadTelemetryInstallID(dir); err == nil {
		t.Fatal("invalid identity must fail closed, not send it or silently rotate")
	}
}

func TestTelemetryOffRejectsEveryProductEventBeforeEnqueue(t *testing.T) {
	telemetry, recorder := newRecordingTelemetry(t)
	telemetryTierMu.Lock()
	telemetryTier = TelemetryTierOff
	telemetryTierMu.Unlock()
	// Keep an enabled recording client to catch regressions in the enqueue
	// gate itself, independently of whether the setter has retired the client.
	app := &App{telemetry: telemetry}
	for _, event := range []string{"app.open", "chat.turn_sent", "playbook.run", "iphone.paired", "brief.first_seen", "app.daily_active", "app.active_time"} {
		telemetry.Capture(event, map[string]any{"date": "2026-10-16", "active_seconds": float64(30), "session_id": uuid.NewString()})
	}
	app.RecordFirstBriefSeen()
	app.RecordProductActivity(30, uuid.NewString())
	if recorder.count() != 0 {
		t.Fatalf("off tier queued %d events", recorder.count())
	}
	if _, err := os.Stat(filepath.Join(configDirOrFallback(), "telemetry_install_id")); !os.IsNotExist(err) {
		t.Fatalf("off tier created identity: %v", err)
	}
}

func TestTelemetryInvalidActivityIsDropped(t *testing.T) {
	app, received := newHTTPTelemetry(t, TelemetryTierDetailed)
	for _, seconds := range []float64{-1, 31, math.NaN(), math.Inf(1)} {
		app.RecordProductActivity(seconds, uuid.NewString())
	}
	app.RecordProductActivity(1, "chat text is not a session UUID")
	app.RecordProductActivity(1, uuid.Nil.String())
	app.telemetry.Close()
	if len(received()) != 0 {
		t.Fatalf("invalid activity sent events: %+v", received())
	}
}

func TestTelemetryConfigurationIsBuildOnlyAndRequiresBothValues(t *testing.T) {
	newRecordingTelemetry(t)
	oldKey, oldHost := DefaultDesktopPostHogKey, DefaultDesktopPostHogHost
	t.Cleanup(func() { DefaultDesktopPostHogKey, DefaultDesktopPostHogHost = oldKey, oldHost })
	t.Setenv("ISOFORM_DESKTOP_POSTHOG_KEY", "runtime-key")
	t.Setenv("ISOFORM_DESKTOP_POSTHOG_HOST", "https://runtime.example")
	t.Setenv("YANSU_DESKTOP_POSTHOG_KEY", "runtime-key")
	t.Setenv("YANSU_DESKTOP_POSTHOG_HOST", "https://runtime.example")
	t.Setenv("VITE_PUBLIC_POSTHOG_PROD_KEY", "vite-key")
	t.Setenv("VITE_PUBLIC_POSTHOG_PROD_HOST", "https://vite.example")
	for _, config := range [][2]string{{"", ""}, {"key", ""}, {"", "https://ingest.example"}, {"key", "http://untrusted.example"}} {
		DefaultDesktopPostHogKey, DefaultDesktopPostHogHost = config[0], config[1]
		telemetry := NewPostHogTelemetry()
		if telemetry.client != nil || telemetry.enabled.Load() {
			t.Fatalf("incomplete/invalid build config initialized sender: %+v", config)
		}
	}
}

func TestTelemetryBatchesAndFlushesOnQuit(t *testing.T) {
	app, _ := newHTTPTelemetry(t, TelemetryTierDetailed)
	app.telemetry.Close()
	batches := make(chan int, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var batch struct {
			Key    string                   `json:"api_key"`
			Events []receivedTelemetryEvent `json:"batch"`
		}
		if r.URL.Path != "/batch/" || r.Method != http.MethodPost {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&batch); err != nil || batch.Key != "test-key" {
			t.Errorf("invalid batch envelope: %+v (%v)", batch, err)
		}
		batches <- len(batch.Events)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	app.telemetry.config.Endpoint = server.URL
	app.telemetry.config.BatchSize = 3
	app.telemetry.SetSharingEnabled(true)
	app.telemetry.Capture("app.open", nil)
	app.telemetry.Capture("chat.turn_sent", nil)
	select {
	case <-batches:
		t.Fatal("sent a batch before size or interval threshold")
	case <-time.After(20 * time.Millisecond):
	}
	app.telemetry.Capture("playbook.run", nil)
	select {
	case size := <-batches:
		if size != 3 {
			t.Fatalf("batch size = %d", size)
		}
	case <-time.After(time.Second):
		t.Fatal("full batch never flushed")
	}
	app.telemetry.Capture("iphone.paired", nil)
	<-app.telemetry.BeginClose()
	select {
	case size := <-batches:
		if size != 1 {
			t.Fatalf("quit batch size = %d", size)
		}
	default:
		t.Fatal("quit did not flush tail")
	}
}

func TestTelemetryOffMakesZeroHTTPRequests(t *testing.T) {
	app, _ := newHTTPTelemetry(t, TelemetryTierOff)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()
	app.telemetry.config.Endpoint = server.URL
	app.telemetry.config.Interval = time.Millisecond
	app.telemetry.SetSharingEnabled(true)
	app.RecordProductActivity(30, uuid.NewString())
	app.RecordFirstBriefSeen()
	app.telemetry.Capture("app.open", nil)
	app.telemetry.Close()
	if requests.Load() != 0 || app.telemetry.client != nil {
		t.Fatalf("off tier initialized a sender or made %d requests", requests.Load())
	}
}

func TestTelemetryQuitIsAsyncBoundedAndNeverRetries(t *testing.T) {
	for _, mode := range []string{"unreachable", "server-error", "hung"} {
		t.Run(mode, func(t *testing.T) {
			app, _ := newHTTPTelemetry(t, TelemetryTierDetailed)
			app.telemetry.Close()
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				_, _ = io.Copy(io.Discard, r.Body)
				switch mode {
				case "hung":
					<-r.Context().Done()
				default:
					w.WriteHeader(http.StatusServiceUnavailable)
				}
			}))
			defer server.Close()
			app.telemetry.config.Endpoint = server.URL
			if mode == "unreachable" {
				server.Close()
			}
			app.telemetry.SetSharingEnabled(true)
			app.telemetry.Capture("chat.turn_sent", nil)
			start := time.Now()
			done := app.telemetry.BeginClose()
			if time.Since(start) > 100*time.Millisecond {
				t.Fatal("quit callback waited on HTTP")
			}
			select {
			case <-done:
			case <-time.After(telemetryFlushTimeout + time.Second):
				t.Fatal("telemetry kept quit alive past its deadline")
			}
			if requests.Load() > 1 {
				t.Fatalf("retried %d times", requests.Load()-1)
			}
			app.telemetry.Capture("iphone.paired", nil)
			app.telemetry.Close()
			if requests.Load() > 1 {
				t.Fatal("replayed failed batch")
			}
		})
	}
}

func TestTelemetryRepeatedQuitSharesFlushAndOptOutCancelsIt(t *testing.T) {
	app, _ := newHTTPTelemetry(t, TelemetryTierDetailed)
	app.telemetry.Close()
	started, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
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
	app.telemetry.SetSharingEnabled(true)
	app.telemetry.Capture("app.open", nil)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("upload never started")
	}
	first := app.telemetry.BeginClose()
	second := app.telemetry.BeginClose()
	if first != second {
		t.Fatal("repeated quit did not join the same flush")
	}
	select {
	case <-second:
		t.Fatal("quit reported completion before the in-flight upload ended")
	default:
	}
	if err := app.SetTelemetryTier(TelemetryTierOff); err != nil {
		t.Fatal(err)
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("opt-out did not cancel the original quit flush")
	}
	select {
	case <-first:
	case <-time.After(time.Second):
		t.Fatal("quit did not finish after cancellation")
	}
}

func TestHoldTelemetryRequiresSurfaceMapID(t *testing.T) {
	for _, action := range []string{"T14", "T84", "T31", "D01"} {
		props, ok := productTelemetryProperties("today.action", map[string]any{"action": action, "kind": "reach", "outcome": "done", "ms_to_result": 5, "title": "private title"})
		if !ok || props["action"] != action || len(props) != 4 {
			t.Fatalf("%s: %#v allowed %v", action, props, ok)
		}
	}
	if _, ok := productTelemetryProperties("today.action", map[string]any{"action": "calendar_hold", "kind": "reach", "outcome": "done", "ms_to_result": 1}); ok {
		t.Fatal("accepted legacy hold action")
	}
}
