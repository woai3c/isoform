package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"wails-gui/internal/pii"
	"wails-gui/storage"
)

func newPrivacyReadTestApp(t *testing.T) *App {
	t.Helper()
	home, err := os.MkdirTemp("/tmp", "ipr-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("HOME", home)
	store, err := storage.New(filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return &App{store: store, activity: &activityMonitorState{store: newTestStore(t)}}
}

func TestCronActivityRoutesApplyReadTimePrivacy(t *testing.T) {
	app := newPrivacyReadTestApp(t)
	store := app.activity.store
	priorApps := pii.GetUserSensitiveApps()
	t.Cleanup(func() { pii.SetUserSensitiveApps(priorApps) })
	pii.SetUserSensitiveApps(map[string]string{"com.example.diary": "Diary", "com.tinyspeck.slackmacgap": "Team conversations"})
	app.saveObservationPauses([]observationPause{{Start: 700_000, End: 760_000}})
	for index, fixture := range []struct {
		id, status, apps, event, parsed, snapshotApp string
	}{
		{id: "safe", status: "dismissed"},
		{id: "protected", status: "protected"},
		{id: "newly-sensitive-name", status: "dismissed", apps: `["Diary"]`},
		{id: "sensitive-bundle", status: "dismissed", event: `{"appBundleId":"com.1password.desktop"}`},
		{id: "private-window", status: "dismissed", event: `{"appName":"Chrome","windowTitle":"Bank - Incognito"}`},
		{id: "default-name", status: "dismissed", apps: `["Passwords"]`},
		{id: "paused-session", status: "dismissed"},
		{id: "snapshot-provenance", status: "dismissed", parsed: `{"metadata":{"bundleId":"com.example.diary"}}`},
		{id: "snapshot-private", status: "dismissed", parsed: `{"title":"Mail - InPrivate"}`},
		{id: "malformed-provenance", status: "dismissed", event: `broken`},
		{id: "snapshot-alias", status: "dismissed", snapshotApp: "slack"},
		{id: "older-safe", status: "dismissed"},
		{id: "sensitive-dictation", status: "dismissed", event: `{"appName":"Notes","speechDictationApp":"Diary","speechText":"private speech"}`},
	} {
		insertTestSession(t, store, fixture.id, fixture.status, int64(index+1)*100_000)
		if fixture.apps != "" {
			if _, err := store.db.Exec(`UPDATE activity_sessions SET app_names = ? WHERE id = ?`, fixture.apps, fixture.id); err != nil {
				t.Fatal(err)
			}
		}
		if fixture.event != "" {
			if _, err := store.db.Exec(`UPDATE activity_events SET data = ? WHERE session_id = ?`, fixture.event, fixture.id); err != nil {
				t.Fatal(err)
			}
		}
		if fixture.parsed != "" {
			if _, err := store.db.Exec(`UPDATE activity_snapshots SET parsed_json = ? WHERE session_id = ?`, fixture.parsed, fixture.id); err != nil {
				t.Fatal(err)
			}
		}
		if fixture.snapshotApp != "" {
			if _, err := store.db.Exec(`UPDATE activity_snapshots SET app = ? WHERE session_id = ?`, fixture.snapshotApp, fixture.id); err != nil {
				t.Fatal(err)
			}
		}
	}
	api := &cronAPIServer{app: app}
	mux := http.NewServeMux()
	mux.HandleFunc("/activity/sessions", api.handleActivitySessions)
	mux.HandleFunc("/activity/sessions/", api.handleActivitySessionByID)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	get := func(path string) (int, []byte) {
		t.Helper()
		resp, err := server.Client().Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, body
	}
	t.Run("list", func(t *testing.T) {
		status, body := get("/activity/sessions?limit=20")
		var sessions []ActivitySessionSummary
		if err := json.Unmarshal(body, &sessions); err != nil || status != http.StatusOK || len(sessions) != 2 || sessions[0].ID != "older-safe" || sessions[1].ID != "safe" {
			t.Fatalf("list exposed protected sessions: status=%d body=%s err=%v", status, body, err)
		}
	})
	t.Run("pagination", func(t *testing.T) {
		status, body := get("/activity/sessions?limit=1&offset=1")
		var sessions []ActivitySessionSummary
		if err := json.Unmarshal(body, &sessions); err != nil || status != http.StatusOK || len(sessions) != 1 || sessions[0].ID != "safe" {
			t.Fatalf("pagination counted protected rows: status=%d body=%s err=%v", status, body, err)
		}
	})
	for _, id := range []string{"protected", "newly-sensitive-name", "sensitive-bundle", "private-window", "default-name", "paused-session", "snapshot-provenance", "snapshot-private", "malformed-provenance", "snapshot-alias", "sensitive-dictation"} {
		t.Run(id, func(t *testing.T) {
			if status, body := get("/activity/sessions/" + id); status != http.StatusNotFound {
				t.Fatalf("protected detail returned: status=%d body=%s", status, body)
			}
		})
	}
	t.Run("safe-detail", func(t *testing.T) {
		status, body := get("/activity/sessions/safe")
		var detail ActivitySessionDetail
		if err := json.Unmarshal(body, &detail); err != nil || status != http.StatusOK || detail.ID != "safe" || len(detail.Events) != 1 || len(detail.Snapshots) != 1 || len(detail.Segments) != 1 {
			t.Fatalf("safe detail unavailable: status=%d body=%s err=%v", status, body, err)
		}
	})
	t.Run("closed-eyes", func(t *testing.T) {
		app.closedEyes.enabled.Store(true)
		defer app.closedEyes.enabled.Store(false)
		if status, body := get("/activity/sessions"); status != http.StatusOK || string(body) != "[]\n" {
			t.Fatalf("closed-eyes list exposed data: status=%d body=%s", status, body)
		}
		if status, body := get("/activity/sessions/safe"); status != http.StatusNotFound {
			t.Fatalf("closed-eyes detail exposed data: status=%d body=%s", status, body)
		}
	})
}

func TestCronActivityDateUsesLocalCalendarDay(t *testing.T) {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	previousLocal := time.Local
	time.Local = loc
	t.Cleanup(func() { time.Local = previousLocal })
	for _, day := range []time.Time{
		time.Date(2026, 3, 8, 0, 0, 0, 0, loc),
		time.Date(2026, 11, 1, 0, 0, 0, 0, loc),
	} {
		t.Run(day.Format("2006-01-02"), func(t *testing.T) {
			app := newPrivacyReadTestApp(t)
			next := day.AddDate(0, 0, 1)
			insertTestSession(t, app.activity.store, "before-midnight", "dismissed", next.Add(-time.Minute).UnixMilli())
			insertTestSession(t, app.activity.store, "after-midnight", "dismissed", next.Add(time.Minute).UnixMilli())
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/activity/sessions?date="+day.Format("2006-01-02"), nil)
			(&cronAPIServer{app: app}).handleActivitySessions(response, request)
			var sessions []ActivitySessionSummary
			if err := json.Unmarshal(response.Body.Bytes(), &sessions); err != nil {
				t.Fatal(err)
			}
			if response.Code != http.StatusOK || len(sessions) != 1 || sessions[0].ID != "before-midnight" {
				t.Fatalf("date query returned status=%d sessions=%+v", response.Code, sessions)
			}
		})
	}
}
