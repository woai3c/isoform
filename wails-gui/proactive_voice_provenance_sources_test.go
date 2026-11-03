package main

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"wails-gui/storage"
)

func TestProactiveVoiceDependencyCollectorCanonicalAndFailClosed(t *testing.T) {
	ctx, collector := proactiveVoiceDependencyContext(context.Background())
	proactiveVoiceRecordDependency(ctx, "calendar", "z", "private source text")
	proactiveVoiceRecordDependency(ctx, "activity_session", "a", "first revision")
	proactiveVoiceRecordDependency(ctx, "calendar", "z", "private source text")
	dependencies, ok := collector.dependencies()
	if !ok || len(dependencies) != 2 || dependencies[0].Kind != "activity_session" {
		t.Fatalf("dependencies = %+v, complete=%v", dependencies, ok)
	}
	raw, err := json.Marshal(dependencies)
	if err != nil || strings.Contains(string(raw), "private source text") || strings.Contains(string(raw), "first revision") {
		t.Fatalf("receipt retained source content: %s, %v", raw, err)
	}
	proactiveVoiceRecordDependency(ctx, "calendar", "z", "changed revision")
	if _, ok := collector.dependencies(); ok {
		t.Fatal("conflicting revisions accepted within one source read")
	}
	ctx, collector = proactiveVoiceDependencyContext(context.Background())
	proactiveVoiceRecordDependency(ctx, "calendar", "", "missing origin")
	if _, ok := collector.dependencies(); ok {
		t.Fatal("missing source identity accepted")
	}
}

func TestProactiveVoiceProvenanceDetectsSameDisplayCalendarSourceReplacement(t *testing.T) {
	app := newPrivacyReadTestApp(t)
	ctx := context.Background()
	now := time.Now()
	event := proactiveVoiceTestEvent(t, app, now)
	insertTestSession(t, app.activity.store, "original-session", "done", 1000)
	insertTestSession(t, app.activity.store, "replacement-session", "done", 1000)
	row, found, err := app.store.GetCalendarEvent(ctx, event.ID)
	if err != nil || !found {
		t.Fatalf("calendar fixture: found=%v err=%v", found, err)
	}
	row.SessionID = "original-session"
	if err := app.store.UpsertCalendarEvents(ctx, []storage.CalendarEventRow{row}, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	moment := meetingPrepMoment(event, now, time.Local)
	original, reason := app.loadProactiveVoiceSource(ctx, moment, now, "en")
	if reason != "" {
		t.Fatalf("original source rejected: %q", reason)
	}
	row.SessionID = "replacement-session"
	if err := app.store.UpsertCalendarEvents(ctx, []storage.CalendarEventRow{row}, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	replacement, reason := app.loadProactiveVoiceSource(ctx, moment, now, "en")
	if reason != "" || !reflect.DeepEqual(original.Items, replacement.Items) || !reflect.DeepEqual(original.People, replacement.People) {
		t.Fatalf("replacement changed display facts or was rejected: %q", reason)
	}
	if reflect.DeepEqual(original.Provenance, replacement.Provenance) {
		t.Fatal("identical display facts hid a replaced activity dependency")
	}
	public, err := json.Marshal(original)
	if err != nil || strings.Contains(string(public), "original-session") || strings.Contains(string(public), event.ID) {
		t.Fatalf("local dependency identities escaped source serialization: %s %v", public, err)
	}
}

func TestProactiveVoiceProvenanceCapturesPrincipalAndExplicitChatClosure(t *testing.T) {
	app := newPrivacyReadTestApp(t)
	ctx := context.Background()
	now := time.Now()
	event := proactiveVoiceTestEvent(t, app, now)
	principal, err := app.store.CreatePrincipal(ctx, storage.PrincipalRow{ID: "voice-self", Kind: storage.PrincipalKindSelf, DisplayName: "Morgan Lane", CreatedFrom: "onboarding"})
	if err != nil {
		t.Fatal(err)
	}
	insertTestSession(t, app.activity.store, "name-source", "done", 1000)
	if err := app.store.BumpPrincipalSighting(ctx, principal.ID, "slack", "name-source", 1000, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := app.store.BindAppHandle(ctx, principal.ID, "slack", "local-handle", "Morgan"); err != nil {
		t.Fatal(err)
	}
	main, err := app.ensureMainChat(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.store.AppendStreamItem(ctx, main.LocalID, storage.StreamItemRow{ID: "voice-user-word", Kind: "user", Content: "Design review needs the blue option.", CreatedAt: now.Add(-time.Minute).UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	moment := meetingPrepMoment(event, now, time.Local)
	original, reason := app.loadProactiveVoiceSource(ctx, moment, now, "en")
	if reason != "" || original.UserName != "Morgan" || len(original.UserWords) != 1 {
		t.Fatalf("source rejected or input absent: %q %+v", reason, original)
	}
	for _, kind := range []string{"principal", "principal_sightings", "principal_handles", "activity_session", "calendar", "chat_session", "chat_user_message"} {
		found := false
		for _, dependency := range original.Provenance {
			found = found || dependency.Kind == kind
		}
		if !found {
			t.Errorf("missing %s input provenance", kind)
		}
	}
	if _, err := app.store.AppendStreamItem(ctx, main.LocalID, storage.StreamItemRow{ID: "voice-generated-row", Kind: "system", Content: "A meeting is about to start."}); err != nil {
		t.Fatal(err)
	}
	unchanged, reason := app.loadProactiveVoiceSource(ctx, moment, now, "en")
	if reason != "" || !reflect.DeepEqual(original.Provenance, unchanged.Provenance) {
		t.Fatalf("storing a result invalidated its own receipt: %q", reason)
	}
	if err := app.store.UpdateStreamItemContent(ctx, "voice-user-word", "Design review needs the green option."); err != nil {
		t.Fatal(err)
	}
	edited, reason := app.loadProactiveVoiceSource(ctx, moment, now, "en")
	if reason != "" || reflect.DeepEqual(original.Provenance, edited.Provenance) {
		t.Fatalf("edited explicit user input did not change provenance: %q", reason)
	}
}
