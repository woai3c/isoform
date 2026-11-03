package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"wails-gui/storage"
)

func newOpenAnswerObservationApp(t *testing.T) *App {
	t.Helper()
	app := mainChatTestApp(t)
	app.activity = &activityMonitorState{store: newTimelineTestStore(t)}
	return app
}

func createOpenAnswerObservationFixture(t *testing.T, app *App, id string, now time.Time) storage.OpenAnswerRow {
	t.Helper()
	session, err := app.ensureMainChat(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	row := storage.OpenAnswerRow{
		ID: id, ChatSessionID: session.LocalID, AssistantMessageID: "promise-" + id,
		Question: "When will the Metro runoff result be final?", Topic: "Metro runoff",
		Settles: "result", Source: "Official results",
		WindowStart: now.Add(-time.Hour).UnixMilli(), WindowEnd: now.Add(time.Hour).UnixMilli(),
		Delivery: "around", ExpiresAt: now.Add(24 * time.Hour).UnixMilli(),
		State: "open", CreatedAt: now.Add(-3 * time.Hour).UnixMilli(),
	}
	if _, err := app.store.AppendStreamItem(t.Context(), session.LocalID, storage.StreamItemRow{
		ID: row.AssistantMessageID, Kind: "assistant", Content: "I will check.", CreatedAt: row.CreatedAt,
	}); err != nil {
		t.Fatal(err)
	}
	if err := app.store.CreateOpenAnswer(t.Context(), row); err != nil {
		t.Fatal(err)
	}
	if app.cronScheduler == nil {
		app.cronScheduler = NewCronScheduler(app.store, func(string, any) {}, func(string, string) {}, nil)
	}
	return row
}

func addOpenAnswerObservation(t *testing.T, app *App, sessionID string, frameAt time.Time, text string, protected, privateSource, overlapsRange bool) {
	t.Helper()
	status := "dismissed"
	if protected {
		status = "protected"
	}
	appNames := `["Notes"]`
	if privateSource {
		appNames = `["Passwords"]`
	}
	start, end := frameAt.Add(-time.Minute), frameAt.Add(time.Minute)
	if overlapsRange {
		start, end = frameAt.Add(-3*time.Hour), frameAt.Add(3*time.Hour)
	}
	session := ActivitySessionRow{
		ID: sessionID, StartTime: start.UnixMilli(), EndTime: end.UnixMilli(),
		TriggerKind: "periodic_flush", AppNames: appNames, TriageStatus: status,
		CreatedAt: end.UnixMilli(),
	}
	if err := app.activity.store.SaveSessionWithSnapshots(session, []ActivitySnapshotRow{{
		SessionID: sessionID, Timestamp: frameAt.UnixMilli(), Trigger: "periodic_flush",
		StorageTier: "local", App: "notes",
	}}); err != nil {
		t.Fatalf("save observation session: %v", err)
	}
	var snapshotID int64
	if err := app.activity.store.db.QueryRow(`SELECT id FROM activity_snapshots WHERE session_id=?`, sessionID).Scan(&snapshotID); err != nil {
		t.Fatalf("load observation snapshot id: %v", err)
	}
	if err := app.activity.store.SaveOCRFrame(OCRFrameRow{
		SessionID: sessionID, SegmentID: snapshotID,
		FrameTime: float64(frameAt.Sub(time.UnixMilli(session.StartTime))) / float64(time.Second),
		Text:      text, Confidence: 1,
	}); err != nil {
		t.Fatalf("save observation frame: %v", err)
	}
}

func TestOpenAnswerValueBounded(t *testing.T) {
	for _, tc := range []struct {
		value, text string
		want        bool
	}{
		{value: "280", text: "total 1280 mb"},
		{value: "280", text: "price 280 usd", want: true},
		{value: "280", text: "2,280"},
		{value: "280", text: "280.", want: true},
		{value: "280", text: "1.280"},
		{value: "280", text: "280abc"},
		{value: "approved", text: "unapproved"},
		{value: "已批准", text: "签证已批准了", want: true},
		{value: "280", text: "1280 then 280", want: true},
	} {
		t.Run(tc.text, func(t *testing.T) {
			got := openAnswerValueBounded(
				normalizeOpenAnswerObservationText(tc.text),
				normalizeOpenAnswerObservationText(tc.value),
			)
			if got != tc.want {
				t.Fatalf("openAnswerValueBounded(%q, %q) = %v, want %v", tc.text, tc.value, got, tc.want)
			}
		})
	}
}

func TestOpenAnswerSawItAtRejectsPartialNumericValue(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	app := newOpenAnswerObservationApp(t)
	row := createOpenAnswerObservationFixture(t, app, "observation-partial-value", now)
	addOpenAnswerObservation(t, app, row.ChatSessionID, now, "Metro runoff total 1280 mb", false, false, false)
	at, matched := app.openAnswerSawItAt(t.Context(), row, "280", now)
	if matched || at != 0 {
		t.Fatalf("partial numeric OCR value matched at %d", at)
	}
}

func TestCompleteOpenAnswerCheckMarksSawItFromAllowedOCR(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	for _, tc := range []struct {
		name           string
		value          string
		frameText      string
		frameAt        time.Time
		protected      bool
		privateSource  bool
		overlapsRange  bool
		closedEyes     bool
		wantState      string
		wantPosts      int
		wantSawAt      int64
		addObservation bool
	}{
		{
			name:  "matching value and topic",
			value: "280", frameText: "Metro runoff results: 280 votes",
			frameAt: now, wantState: "saw_it", wantSawAt: now.UnixMilli(), addObservation: true,
		},
		{
			name:  "value without topic",
			value: "280", frameText: "Other district results: 280 votes",
			frameAt: now, wantState: "settled", wantPosts: 1, addObservation: true,
		},
		{
			name:  "frame before range",
			value: "280", frameText: "Metro runoff results: 280 votes",
			frameAt: now.Add(-2 * time.Hour), overlapsRange: true, wantState: "settled", wantPosts: 1, addObservation: true,
		},
		{
			name:  "protected session",
			value: "280", frameText: "Metro runoff results: 280 votes",
			frameAt: now, protected: true, wantState: "settled", wantPosts: 1, addObservation: true,
		},
		{
			name:  "private source session",
			value: "280", frameText: "Metro runoff results: 280 votes",
			frameAt: now, privateSource: true, wantState: "settled", wantPosts: 1, addObservation: true,
		},
		{
			name:  "short value",
			value: "42", frameText: "Metro runoff results: 42 votes",
			frameAt: now, wantState: "settled", wantPosts: 1, addObservation: true,
		},
		{
			name:  "Closed Eyes",
			value: "280", frameText: "Metro runoff results: 280 votes",
			frameAt: now, closedEyes: true, wantState: "open", wantPosts: 0, addObservation: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := newOpenAnswerObservationApp(t)
			row := createOpenAnswerObservationFixture(t, app, "observation-"+strings.ReplaceAll(tc.name, " ", "-"), now)
			if err := app.ensureOpenAnswerCheck(row, ""); err != nil {
				t.Fatal(err)
			}
			if tc.addObservation {
				addOpenAnswerObservation(t, app, row.ID, tc.frameAt, tc.frameText, tc.protected, tc.privateSource, tc.overlapsRange)
			}
			app.closedEyes.enabled.Store(tc.closedEyes)
			var events chan []byte
			var assistantSortOrder int
			if tc.wantState == "saw_it" {
				events = app.eventBroadcaster.AddClient("saw-it-reload")
				defer app.eventBroadcaster.RemoveClient("saw-it-reload")
				item, err := app.store.LoadStreamItem(t.Context(), row.AssistantMessageID)
				if err != nil || item == nil {
					t.Fatalf("load promise item = %+v, %v", item, err)
				}
				assistantSortOrder = item.SortOrder
			}
			result := `{"settled":true,"answer":"The result is final.","value":"` + tc.value + `","source":"Official results"}`
			app.completeOpenAnswerCheck(row.ID, result, now, "")

			updated, err := app.store.GetOpenAnswer(t.Context(), row.ID)
			if err != nil || updated.State != tc.wantState || updated.Posts != tc.wantPosts {
				t.Fatalf("completed record = %+v, %v; want state=%s posts=%d", updated, err, tc.wantState, tc.wantPosts)
			}
			if updated.SawAt != tc.wantSawAt {
				t.Fatalf("sawAt = %d, want %d", updated.SawAt, tc.wantSawAt)
			}
			items, err := app.store.LoadStreamItems(t.Context(), row.ChatSessionID)
			if err != nil {
				t.Fatal(err)
			}
			postCount := 0
			for _, item := range items {
				if item.RunID == "open-answer:"+row.ID {
					postCount++
				}
			}
			if postCount != tc.wantPosts {
				t.Fatalf("stream posts = %d, want %d", postCount, tc.wantPosts)
			}
			if tc.wantState == "saw_it" && len(openAnswerCheckJobs(t, app, row.ID)) != 0 {
				t.Fatal("saw_it transition retained its pending check")
			}
			if tc.wantState == "saw_it" {
				foundReload := false
				for len(events) > 0 {
					var event BrowserEvent
					if err := json.Unmarshal(<-events, &event); err != nil {
						t.Fatalf("decode reload event: %v", err)
					}
					if event.Event != mainChatItemsAppendedEvent || len(event.Args) != 1 {
						continue
					}
					payload, ok := event.Args[0].(map[string]any)
					if !ok {
						continue
					}
					sortOrder, ok := payload["sortOrder"].(float64)
					if ok && payload["sessionId"] == row.ChatSessionID &&
						payload["itemId"] == row.AssistantMessageID && payload["kind"] == "assistant" &&
						payload["source"] == "open_answer" && int(sortOrder) == assistantSortOrder {
						foundReload = true
					}
				}
				if !foundReload {
					t.Fatal("saw_it reload event did not include the assistant message identity and sort order")
				}
				for _, state := range []string{
					storage.EngagementStatePending, storage.EngagementStateScheduled, storage.EngagementStateSent,
					storage.EngagementStateSuppressed, storage.EngagementStateExpired,
				} {
					moments, err := app.store.ListEngagementMoments(t.Context(), state, 0)
					if err != nil || len(moments) != 0 {
						t.Fatalf("saw_it created engagement moments in state %s: %+v, %v", state, moments, err)
					}
				}
			}
		})
	}
}

func TestOpenAnswerObservationUsesNFKCAndSpaceNormalizedTopic(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	app := newOpenAnswerObservationApp(t)
	row := createOpenAnswerObservationFixture(t, app, "observation-normalized", now)
	addOpenAnswerObservation(t, app, row.ID, now, "ＭＥＴＲＯ　ＲＵＮＯＦＦ results: 280 votes", false, false, false)
	result := `{"settled":true,"answer":"The result is final.","value":"280","source":"Official results"}`
	app.completeOpenAnswerCheck(row.ID, result, now, "")
	updated, err := app.store.GetOpenAnswer(t.Context(), row.ID)
	if err != nil || updated.State != "saw_it" {
		t.Fatalf("normalized OCR match = %+v, %v", updated, err)
	}
}

func TestOpenAnswerObservationUsesEarliestMatchingFrame(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	app := newOpenAnswerObservationApp(t)
	row := createOpenAnswerObservationFixture(t, app, "observation-earliest", now)
	sessionID := "earliest-observation"
	start, end := now.Add(-time.Minute), now.Add(time.Minute)
	if err := app.activity.store.SaveSessionWithSnapshots(ActivitySessionRow{
		ID: sessionID, StartTime: start.UnixMilli(), EndTime: end.UnixMilli(),
		TriggerKind: "periodic_flush", AppNames: `["Notes"]`, TriageStatus: "dismissed",
		CreatedAt: end.UnixMilli(),
	}, []ActivitySnapshotRow{
		{SessionID: sessionID, Timestamp: now.Add(-30 * time.Second).UnixMilli(), Trigger: "periodic_flush", StorageTier: "local"},
		{SessionID: sessionID, Timestamp: now.Add(-10 * time.Second).UnixMilli(), Trigger: "periodic_flush", StorageTier: "local"},
	}); err != nil {
		t.Fatal(err)
	}
	snapshots, err := app.activity.store.GetSessionSnapshots(sessionID)
	if err != nil || len(snapshots) != 2 {
		t.Fatalf("observation snapshots = %+v, %v", snapshots, err)
	}
	for _, snapshot := range snapshots {
		if err := app.activity.store.SaveOCRFrame(OCRFrameRow{
			SessionID: sessionID, SegmentID: snapshot.ID,
			Text: "Metro runoff results: 280 votes", Confidence: 1,
		}); err != nil {
			t.Fatal(err)
		}
	}
	result := `{"settled":true,"answer":"The result is final.","value":"280","source":"Official results"}`
	app.completeOpenAnswerCheck(row.ID, result, now, "")
	updated, err := app.store.GetOpenAnswer(t.Context(), row.ID)
	if err != nil || updated.State != "saw_it" || updated.SawAt != now.Add(-30*time.Second).UnixMilli() {
		t.Fatalf("earliest OCR match = %+v, %v", updated, err)
	}
}

func TestOpenAnswerObservationFramesUseSegmentRelativeTime(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	app := newOpenAnswerObservationApp(t)
	row := createOpenAnswerObservationFixture(t, app, "observation-segment", now)
	sessionID := "segment-observation"
	start, end := now.Add(-time.Minute), now.Add(time.Minute)
	if err := app.activity.store.SaveSession(ActivitySessionRow{
		ID: sessionID, StartTime: start.UnixMilli(), EndTime: end.UnixMilli(),
		TriggerKind: "periodic_flush", AppNames: `["Notes"]`, TriageStatus: "dismissed",
		CreatedAt: end.UnixMilli(),
	}, nil, []ActivitySegmentRow{{StartTime: start.UnixMilli(), EndTime: end.UnixMilli()}}); err != nil {
		t.Fatal(err)
	}
	var segmentID int64
	if err := app.activity.store.db.QueryRow(`SELECT id FROM activity_segments WHERE session_id=?`, sessionID).Scan(&segmentID); err != nil {
		t.Fatal(err)
	}
	if err := app.activity.store.SaveOCRFrame(OCRFrameRow{
		SessionID: sessionID, SegmentID: segmentID, FrameTime: 30,
		Text: "Metro runoff results: 280 votes", Confidence: 1,
	}); err != nil {
		t.Fatal(err)
	}
	result := `{"settled":true,"answer":"The result is final.","value":"280","source":"Official results"}`
	app.completeOpenAnswerCheck(row.ID, result, now, "")
	updated, err := app.store.GetOpenAnswer(t.Context(), row.ID)
	if err != nil || updated.State != "saw_it" || updated.SawAt != now.Add(-30*time.Second).UnixMilli() {
		t.Fatalf("segment-relative OCR match = %+v, %v", updated, err)
	}
}
