package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"wails-gui/internal/platform/activitymon"
)

func TestCronActivityDetailRedactsPayloadWithoutChangingStore(t *testing.T) {
	isolateActivityPrivacyTest(t)
	app := newPrivacyReadTestApp(t)
	store := app.activity.store
	insertTestSession(t, store, "safe-detail", "dismissed", 1000)
	event := activitymon.ActivityEvent{AppName: "Notes", TypedText: "split credential", KeyChar: "q", KeyChars: []string{"s", "k", "-"}, KeyCode: 12, SpeechText: "spoken " + activityPrivacySecret, ClipboardPreview: "clipboard " + activityPrivacySecret, WindowTitle: "useful note"}
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE activity_events SET data=? WHERE session_id='safe-detail'`, string(raw)); err != nil {
		t.Fatal(err)
	}
	parsed, _ := json.Marshal(map[string]any{"title": "ordinary note", "body": activityPrivacySecret, "metadata": map[string]string{"bundleId": "com.apple.Notes"}})
	if _, err := store.db.Exec(`UPDATE activity_snapshots SET ax_text=?,browser_url=?,parsed_json=? WHERE session_id='safe-detail'`, "OCR "+activityPrivacySecret, "https://example.org/?key="+activityPrivacySecret, string(parsed)); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	(&cronAPIServer{app: app}).handleActivitySessionByID(response, httptest.NewRequest(http.MethodGet, "/activity/sessions/safe-detail", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("detail status=%d body=%s", response.Code, response.Body.String())
	}
	var detail ActivitySessionDetail
	if err := json.Unmarshal(response.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(response.Body.String(), activityPrivacySecret) || strings.Contains(response.Body.String(), "split credential") || !strings.Contains(response.Body.String(), "[REDACTED:") {
		t.Fatalf("unsafe detail: %s", response.Body.String())
	}
	if len(detail.Events) != 1 || detail.Events[0].KeyChar != "" || len(detail.Events[0].KeyChars) != 0 || detail.Events[0].KeyCode != 0 || detail.Events[0].WindowTitle != "useful note" || len(detail.Snapshots) != 1 {
		t.Fatalf("raw key material retained or safe detail structure lost: %+v", detail)
	}
	var nested map[string]any
	if err := json.Unmarshal([]byte(detail.Snapshots[0].ParsedJSON), &nested); err != nil {
		t.Fatal("redaction broke nested parser JSON:", err)
	}
	events, err := store.GetSessionEvents("safe-detail")
	if err != nil || len(events) != 1 || events[0].KeyChar != event.KeyChar || events[0].KeyCode != event.KeyCode || len(events[0].KeyChars) != len(event.KeyChars) || events[0].TypedText != "split credential" || !strings.Contains(events[0].SpeechText, activityPrivacySecret) {
		t.Fatalf("external read changed original evidence: %v %v", events, err)
	}
}
