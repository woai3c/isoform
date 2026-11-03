package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
)

func TestTriagePanelKeepsDiskMetadataAndGatesLiveContent(t *testing.T) {
	for _, flag := range []string{"", "0", "1"} {
		t.Run("debug="+flag, func(t *testing.T) {
			t.Setenv(backgroundTranscriptDebugEnv, flag)
			output, path := isolateActivityPrivacyLogs(t)
			app := &App{ctx: context.Background(), eventBroadcaster: NewEventBroadcaster()}
			events := app.eventBroadcaster.AddClient("triage-panel-test")
			defer app.eventBroadcaster.RemoveClient("triage-panel-test")
			setTriageApp(app)
			const title = "private panel title"
			const description = "private panel description\nwith a second line"
			payload, err := json.Marshal(map[string]any{
				"source": "activity", "id": "panel-entry", "decision": "triage",
				"reason": activityPrivateLogField("title", title) + " " + activityPrivateLogField("description", description),
				"title":  title, "description": description, "worthMemory": true,
			})
			if err != nil {
				t.Fatal(err)
			}
			var entry TriageLogEntry
			if err := json.Unmarshal(payload, &entry); err != nil {
				t.Fatal(err)
			}
			logTriageEntry(entry)
			assertPanelContent := func(label string, raw []byte, debug bool) {
				t.Helper()
				var fields map[string]any
				if err := json.Unmarshal(raw, &fields); err != nil {
					t.Fatalf("%s: %v", label, err)
				}
				if fields["worthMemory"] != true || fields["title_bytes"] != float64(len(title)) || fields["description_bytes"] != float64(len(description)) {
					t.Errorf("%s lost structured decision metadata: %s", label, raw)
				}
				if debug {
					if fields["title"] != title || fields["description"] != description {
						t.Errorf("%s debug event lost source fields: %s", label, raw)
					}
				} else {
					assertActivityLogContentVisibility(t, string(raw), false, title, "private panel description")
				}
			}
			select {
			case payload := <-events:
				var event struct {
					Event string            `json:"event"`
					Args  []json.RawMessage `json:"args"`
				}
				if err := json.Unmarshal(payload, &event); err != nil || event.Event != "triage:log" || len(event.Args) != 1 {
					t.Fatalf("unexpected live event: %s, error=%v", payload, err)
				}
				assertPanelContent("live event", event.Args[0], flag == "1")
			default:
				t.Fatal("triage event was not emitted")
			}
			closeTriageLog()
			entries := app.GetTriageLogs()
			if len(entries) != 1 {
				t.Fatalf("reloaded %d entries, want one", len(entries))
			}
			reloaded, err := json.Marshal(entries[0])
			if err != nil {
				t.Fatal(err)
			}
			assertPanelContent("reload", reloaded, false)
			disk, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			assertPanelContent("disk", disk, false)
			assertActivityLogContentVisibility(t, output.String(), false, title)
			if flag != "1" && strings.Contains(output.String(), "private panel description") {
				t.Fatal("application log contains the private panel description")
			}
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatalf("private triage file permissions: info=%v error=%v", info, err)
			}
		})
	}
}

func TestTriageLogReadAllowsWritesAndKeepsSnapshot(t *testing.T) {
	isolateActivityPrivacyLogs(t)
	logTriage("test", "before", "accepted", "metadata")
	wroteDuringScan := false
	entries := readTriageLogs(func(reader io.Reader, appendEntry func(TriageLogEntry)) {
		// The scan seam pauses before any bytes are read. TryLock deterministically
		// diagnoses the old lock scope without a scheduler or wall-clock deadline.
		if !triage.mu.TryLock() {
			t.Error("reading history holds the triage writer lock")
			return
		}
		triage.mu.Unlock()
		logTriage("test", "during-scan", "accepted", "metadata")
		wroteDuringScan = true
		scanTriageLogEntries(reader, appendEntry)
	})
	if !wroteDuringScan {
		t.Fatal("triage writer could not proceed while the history scan paused")
	}
	if len(entries) != 1 || entries[0].ID != "before" {
		t.Fatalf("history scan included writes after its snapshot: %+v", entries)
	}
	latest := (&App{}).GetTriageLogs()
	if len(latest) != 2 || latest[1].ID != "during-scan" {
		t.Fatalf("concurrent write did not survive for the next reload: %+v", latest)
	}
}

func TestTriageLogReadsLegacyAndStructuredRecords(t *testing.T) {
	legacy := `2026-10-16 10:15:32.123 [activity] id=legacy decision=triage worth_memory=true reason=title="Old title" description="Old detail"`
	structured := `{"timestamp":"2026-10-16 10:15:33.123","source":"activity","id":"new","decision":"triage","reason":"title_bytes=9 description_bytes=10","title":"New title","description":"New detail","worthMemory":true}`
	var entries []TriageLogEntry
	scanTriageLogEntries(strings.NewReader(legacy+"\n"+structured+"\n"), func(entry TriageLogEntry) { entries = append(entries, entry) })
	if len(entries) != 2 || entries[0].ID != "legacy" || entries[1].ID != "new" {
		t.Fatalf("mixed-format history was not retained: %+v", entries)
	}
	if !strings.Contains(entries[0].Reason, `title="Old title"`) {
		t.Fatalf("legacy panel fallback was changed: %+v", entries[0])
	}
}
