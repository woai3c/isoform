package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"wails-gui/internal/pii"
	"wails-gui/internal/platform/activitymon"
)

func TestActivityEmbeddingUsesCurrentPrivacyAtRemoteBoundary(t *testing.T) {
	isolateActivityPrivacyTest(t)
	app := newPrivacyReadTestApp(t)
	store := app.activity.store
	var requests []embeddingRequest
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request embeddingRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			http.Error(w, "decode", 400)
			return
		}
		requests = append(requests, request)
		response := embeddingResponse{}
		for i := range request.Input {
			response.Data = append(response.Data, embeddingResponseData{Index: i, Embedding: []float64{0.5, 0.25}})
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer endpoint.Close()
	store.SetEmbedder(newOpenAIProvider("openai", "test", endpoint.URL, "test"))
	for i, fixture := range []struct {
		id, apps, status string
		event            activitymon.ActivityEvent
	}{
		{id: "safe", apps: `["Notes"]`},
		{id: "sensitive", apps: `["Private Notes"]`},
		{id: "protected", apps: `["Notes"]`, status: "protected"},
		{id: "private-title", apps: `["Chrome"]`, event: activitymon.ActivityEvent{WindowTitle: "Bank - Incognito"}},
		{id: "private-flag", apps: `["Chrome"]`, event: activitymon.ActivityEvent{PrivateWindow: true}},
		{id: "paused", apps: `["Notes"]`},
	} {
		start := int64(i+1) * 100_000
		if err := store.SaveSession(ActivitySessionRow{ID: fixture.id, StartTime: start, EndTime: start + 1000, AppNames: fixture.apps, TriageStatus: fixture.status}, []activitymon.ActivityEvent{fixture.event}, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := store.db.Exec(`INSERT INTO activity_ocr_frames(session_id,segment_id,frame_time,text) VALUES(?,0,1,?)`, fixture.id, "useful OCR "+activityPrivacySecret); err != nil {
			t.Fatal(err)
		}
	}
	app.saveObservationPauses([]observationPause{{Start: 600_000, End: 602_000}})
	gate := app.activityEmbeddingGate(store)
	for _, id := range []string{"sensitive", "protected", "private-title", "private-flag", "paused"} {
		if err := store.EmbedOCRFrames(t.Context(), id, gate); err != nil {
			t.Fatal(err)
		}
	}
	if len(requests) != 0 {
		t.Fatalf("protected frames sent: %v", requests)
	}
	app.closedEyes.enabled.Store(true)
	store.BackfillEmbeddings(t.Context(), gate)
	if len(requests) != 0 {
		t.Fatalf("closed-eyes backlog sent: %v", requests)
	}
	app.closedEyes.enabled.Store(false)
	if err := store.EmbedOCRFrames(t.Context(), "safe", gate); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 1 || len(requests[0].Input) != 1 || strings.Contains(requests[0].Input[0], activityPrivacySecret) || !strings.Contains(requests[0].Input[0], "[REDACTED:") {
		t.Fatalf("unredacted or missing remote input: %v", requests)
	}
	if _, err := store.db.Exec(`UPDATE activity_ocr_frames SET embedding=NULL WHERE session_id='safe'`); err != nil {
		t.Fatal(err)
	}
	pii.AddUserSensitiveApp("com.apple.Notes", "Notes")
	if err := store.EmbedOCRFrames(t.Context(), "safe", gate); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 1 {
		t.Fatal("post-capture protection sent OCR")
	}
}

func TestActivityEmbeddingFailsClosedAndRechecksAfterResponse(t *testing.T) {
	isolateActivityPrivacyTest(t)
	store := newTestStore(t)
	insertTestSession(t, store, "source", "dismissed", 1000)
	if _, err := store.db.Exec(`INSERT INTO activity_ocr_frames(session_id,segment_id,frame_time,text) VALUES('source',0,1,'ordinary OCR')`); err != nil {
		t.Fatal(err)
	}
	provider := &activityEmbeddingTestProvider{}
	store.SetEmbedder(provider)
	for _, gate := range []activityEmbeddingReadGate{nil, func(string) (bool, error) { return false, errors.New("read unavailable") }} {
		if err := store.EmbedOCRFrames(t.Context(), "source", gate); err == nil {
			t.Fatal("missing privacy proof succeeded")
		}
	}
	if len(provider.batches) != 0 {
		t.Fatal("unreadable privacy sent OCR")
	}
	checks := 0
	gate := func(string) (bool, error) { checks++; return checks < 3, nil }
	if err := store.EmbedOCRFrames(t.Context(), "source", gate); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM activity_ocr_frames WHERE embedding IS NOT NULL AND embedding!=''`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if len(provider.batches) != 1 || count != 0 {
		t.Fatalf("late protection did not suppress publish: batches=%d saved=%d", len(provider.batches), count)
	}
}

func TestActivityEmbeddingBackfillFiltersBeforeSessionCap(t *testing.T) {
	isolateActivityPrivacyTest(t)
	store := newTestStore(t)
	app := &App{activity: &activityMonitorState{store: store}}
	provider := &activityEmbeddingTestProvider{}
	store.SetEmbedder(provider)
	for i, id := range []string{"private-one", "private-two", "private-three", "safe-four"} {
		apps := `["Private Notes"]`
		if id == "safe-four" {
			apps = `["Notes"]`
		}
		if err := store.SaveSession(ActivitySessionRow{ID: id, StartTime: int64(i+1) * 1000, EndTime: int64(i+1)*1000 + 1, AppNames: apps}, nil, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := store.db.Exec(`INSERT INTO activity_ocr_frames(session_id,segment_id,frame_time,text) VALUES(?,0,1,?)`, id, "source "+id); err != nil {
			t.Fatal(err)
		}
	}
	gate := app.activityEmbeddingGate(store)
	ids, err := store.listEmbeddingBackfillSessionIDs(t.Context(), gate)
	if err != nil || len(ids) != 1 || ids[0] != "safe-four" {
		t.Fatalf("private backlog consumed cap: %v err=%v", ids, err)
	}
	store.BackfillEmbeddings(t.Context(), gate)
	if len(provider.batches) != 1 || provider.batches[0][0] != "source safe-four" {
		t.Fatalf("safe backlog starved: %v", provider.batches)
	}
}
