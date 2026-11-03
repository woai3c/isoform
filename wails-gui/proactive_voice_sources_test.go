package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/calendar"
	"wails-gui/internal/engagement"
	"wails-gui/internal/pii"
	"wails-gui/internal/proactivevoice"
	"wails-gui/storage"
)

func proactiveVoiceTestMail(t *testing.T, app *App, now time.Time) engagement.Moment {
	t.Helper()
	ctx := context.Background()
	person, err := app.store.CreatePrincipal(ctx, storage.PrincipalRow{ID: "person-local-only", Kind: storage.PrincipalKindContact, DisplayName: "Dana Ruiz", Aliases: []string{"D. Ruiz"}})
	if err != nil {
		t.Fatal(err)
	}
	row := storage.MailMessageRow{ID: "mail-local-only", Mailbox: storage.MailboxInbox, SenderEmail: "dana@example.com", SenderName: person.DisplayName, ToMe: true, Subject: "Project review", ThreadKey: "thread-local-only", At: now.Add(-48 * time.Hour).UnixMilli(), PrincipalID: person.ID}
	if _, err := app.store.UpsertMailMessages(ctx, []storage.MailMessageRow{row}, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	return mailWaitingMoment(mailWaitingOnReply([]storage.MailMessageRow{row}, now)[0], person.DisplayName, now)
}

func proactiveVoiceTestFlaggedMail(t *testing.T, app *App, now time.Time, subject string) engagement.Moment {
	t.Helper()
	moment := proactiveVoiceTestMail(t, app, now)
	row := storage.MailMessageRow{
		ID: "mail-local-only", Mailbox: storage.MailboxInbox, SenderEmail: "dana@example.com",
		SenderName: "Dana Ruiz", ToMe: true, Subject: subject, ThreadKey: "thread-local-only",
		At: now.Add(-48 * time.Hour).UnixMilli(), PrincipalID: "person-local-only",
	}
	if _, err := app.store.UpsertMailMessages(context.Background(), []storage.MailMessageRow{row}, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	moment.Params["subject"] = subject
	return moment
}

func TestProactiveVoiceSourceMailUsesCurrentHeadersAndConsent(t *testing.T) {
	app := mainChatTestApp(t)
	now := time.Now()
	moment := proactiveVoiceTestMail(t, app, now)
	moment.Params["name"] = "Stale wrong name"
	moment.Params["subject"] = "stale secret"
	source, reason := app.loadProactiveVoiceSource(context.Background(), moment, now, "en")
	if reason != "" || len(source.Items) != 1 || source.Items[0].FromName == nil || *source.Items[0].FromName != "Dana Ruiz" {
		t.Fatalf("source = %+v reason = %q", source, reason)
	}
	if source.MailBodyConsent || source.RawMailBody != "" || source.Items[0].Subject != nil || source.Items[0].BodyExcerpt != nil || len(source.WritingSamples) != 0 {
		t.Fatalf("headers-only consent leaked extra data: %+v", source)
	}
	if source.DraftRecipient != "dana@example.com" || source.DraftSubject != "Re: Project review" {
		t.Fatalf("header-only draft addressing = %+v", source)
	}
	if source.RawMailSubject != "Project review" || !strings.Contains(strings.Join(source.ContactNames, "|"), "D. Ruiz") {
		t.Fatalf("local detection evidence missing: %+v", source)
	}
	raw, _ := json.Marshal(source.Items)
	for _, secret := range []string{"local-only", "example.com", "Project review", "stale secret"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("item exposed %q: %s", secret, raw)
		}
	}
	if err := app.store.SetUIState(context.Background(), permissionUIStateKey(mailPermissionKey), "false"); err != nil {
		t.Fatal(err)
	}
	if _, reason := app.loadProactiveVoiceSource(context.Background(), moment, now, "en"); reason != "privacy_excluded" {
		t.Fatalf("disabled mail accepted: %q", reason)
	}
}

func TestProactiveVoiceSourceMailProtectionAndAnsweredThread(t *testing.T) {
	app := mainChatTestApp(t)
	now := time.Now()
	moment := proactiveVoiceTestMail(t, app, now)
	old := pii.GetUserSensitiveApps()
	t.Cleanup(func() { pii.SetUserSensitiveApps(old) })
	pii.SetUserSensitiveApps(map[string]string{"com.apple.mail": "Mail"})
	if _, reason := app.loadProactiveVoiceSource(context.Background(), moment, now, "en"); reason != "privacy_excluded" {
		t.Fatalf("protected mail accepted: %q", reason)
	}
	pii.SetUserSensitiveApps(old)
	rows, err := app.store.ListMailMessages(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	rows[0].Replied = true
	if _, err := app.store.UpsertMailMessages(context.Background(), rows, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, reason := app.loadProactiveVoiceSource(context.Background(), moment, now, "en"); reason != "no_source" {
		t.Fatalf("answered thread accepted: %q", reason)
	}
}

func proactiveVoiceTestEvent(t *testing.T, app *App, now time.Time) calendar.Event {
	t.Helper()
	event := prepEvent("calendar-local-only", "Design review", now.Add(15*time.Minute), 30, "Zoom", 4)
	event.Source = calendar.SourceEventKit
	row := calendarEventRow(event)
	for i, attendee := range event.Attendees {
		row.Attendees = append(row.Attendees, storage.CalendarAttendeeRow{Name: attendee.Name, Email: fmt.Sprintf("attendee%d@example.com", i), IsMe: attendee.IsMe})
	}
	if err := app.store.UpsertCalendarEvents(context.Background(), []storage.CalendarEventRow{row}, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	return event
}

func TestProactiveVoiceSourceCalendarCapsNamesAndLocalizesFacts(t *testing.T) {
	app := mainChatTestApp(t)
	now := time.Date(2026, 10, 16, 9, 45, 0, 0, time.Local)
	event := proactiveVoiceTestEvent(t, app, now)
	source, reason := app.loadProactiveVoiceSource(context.Background(), meetingPrepMoment(event, now, time.Local), now, "zh-CN")
	if reason != "" || len(source.People) != 3 || len(source.Items) != 1 {
		t.Fatalf("source = %+v reason = %q", source, reason)
	}
	item := source.Items[0]
	if item.StartsIn.Display != "15 分钟" || item.AttendeeTotal.Display != "4 人" || *item.Place != "视频会议" || !strings.Contains(item.Start.Display, "10:00") {
		t.Fatalf("localized facts = %+v", item)
	}
	raw, _ := json.Marshal(source)
	for _, secret := range []string{"calendar-local-only", "example.com", "https:"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("source leaked %q", secret)
		}
	}
	if err := app.store.SetUIState(context.Background(), permissionUIStateKey(calendarPermissionKey), "false"); err != nil {
		t.Fatal(err)
	}
	if _, reason := app.loadProactiveVoiceSource(context.Background(), meetingPrepMoment(event, now, time.Local), now, "en"); reason != "no_source" {
		t.Fatalf("disabled calendar accepted: %q", reason)
	}
}

func TestProactiveVoiceSourceCalendarRejectsStaleAndProtectedSources(t *testing.T) {
	app := mainChatTestApp(t)
	now := time.Now()
	event := proactiveVoiceTestEvent(t, app, now)
	moment := meetingPrepMoment(event, now, time.Local)
	old := pii.GetUserSensitiveApps()
	t.Cleanup(func() { pii.SetUserSensitiveApps(old) })
	pii.SetUserSensitiveApps(map[string]string{"com.apple.iCal": "Calendar"})
	if _, reason := app.loadProactiveVoiceSource(context.Background(), moment, now, "en"); reason != "no_source" {
		t.Fatalf("protected calendar accepted: %q", reason)
	}
	pii.SetUserSensitiveApps(old)
	row := calendarEventRow(event)
	row.DeletedAt = now.UnixMilli()
	if err := app.store.UpsertCalendarEvents(context.Background(), []storage.CalendarEventRow{row}, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	// The regular sync upsert revives rows. A canceled event is still a
	// reliable current-source rejection through the public store method.
	row.Status = "canceled"
	if err := app.store.UpsertCalendarEvents(context.Background(), []storage.CalendarEventRow{row}, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, reason := app.loadProactiveVoiceSource(context.Background(), moment, now, "en"); reason != "no_source" {
		t.Fatalf("canceled calendar accepted: %q", reason)
	}
}

func TestProactiveVoiceSourceChatReadsOnlyBoundedExplicitLinkedWords(t *testing.T) {
	app := mainChatTestApp(t)
	now := time.Now()
	event := proactiveVoiceTestEvent(t, app, now)
	main, err := app.ensureMainChat(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		_, err := app.store.AppendStreamItem(context.Background(), main.LocalID, storage.StreamItemRow{ID: fmt.Sprintf("user-%d", i), Kind: "user", Content: fmt.Sprintf("Design review note %d", i), CreatedAt: now.Add(-time.Minute).UnixMilli()})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, item := range []storage.StreamItemRow{
		{ID: "assistant", Kind: "assistant", Content: "Design review instruction by assistant"},
		{ID: "tool", Kind: "result", Content: "Design review tool instructions"},
		{ID: "automation", Kind: "user", Content: "Design review automated result", AutomationContext: `{"output":"generated"}`},
		{ID: "unrelated", Kind: "user", Content: "Other project text"},
	} {
		item.CreatedAt = now.Add(-time.Minute).UnixMilli()
		if _, err := app.store.AppendStreamItem(context.Background(), main.LocalID, item); err != nil {
			t.Fatal(err)
		}
	}
	source, reason := app.loadProactiveVoiceSource(context.Background(), meetingPrepMoment(event, now, time.Local), now, "en")
	if reason != "" || len(source.UserWords) != 5 {
		t.Fatalf("words = %+v reason = %q", source.UserWords, reason)
	}
	for _, words := range source.UserWords {
		if !strings.HasPrefix(words.Text, "Design review note ") || words.Source != "chat" {
			t.Fatalf("unattributed text accepted: %+v", words)
		}
	}
}

func TestProactiveVoiceSourceCaptureUsesCodeFactsAndRespectsPause(t *testing.T) {
	previous := proactiveVoiceCaptureSnapshots
	t.Cleanup(func() { proactiveVoiceCaptureSnapshots = previous })
	proactiveVoiceCaptureSnapshots = func(*App) []captureSnapshot {
		return []captureSnapshot{{Kind: "screen", Granted: true, Enabled: true, Running: false}}
	}
	app := mainChatTestApp(t)
	now := time.Now()
	moment := engagement.Moment{Kind: engagement.KindCapturePaused, SubjectID: "screen", CreatedAt: now.Add(-90 * time.Minute), Params: map[string]string{"what": "private window title", "duration": "99 years"}}
	source, reason := app.loadProactiveVoiceSource(context.Background(), moment, now, "zh-CN")
	if reason != "" || len(source.Items) != 1 || source.Items[0].What.Display != "屏幕采集" || source.Items[0].OffFor.Display != "90 分钟" {
		t.Fatalf("source = %+v reason = %q", source, reason)
	}
	proactiveVoiceCaptureSnapshots = func(*App) []captureSnapshot {
		return []captureSnapshot{{Kind: "screen", Granted: true, Enabled: true, Running: true}}
	}
	if _, reason := app.loadProactiveVoiceSource(context.Background(), moment, now, "zh-CN"); reason != "no_source" {
		t.Fatalf("restored capture accepted: %q", reason)
	}
	app.closedEyes.enabled.Store(true)
	if _, reason := app.loadProactiveVoiceSource(context.Background(), moment, now, "zh-CN"); reason != "privacy_excluded" {
		t.Fatalf("paused observer disclosed evidence: %q", reason)
	}
}

func TestProactiveVoiceSourceSessionPrivacyUsesDefaultReads(t *testing.T) {
	store := newTestStore(t)
	app := &App{activity: &activityMonitorState{store: store}}
	row := ActivitySessionRow{ID: "source-session", StartTime: 1, EndTime: 2, AppNames: `["Editor"]`, TriageStatus: "done"}
	if err := store.SaveSession(row, nil, nil); err != nil {
		t.Fatal(err)
	}
	if !app.proactiveVoiceSessionsAllowed([]string{row.ID}) {
		t.Fatal("normal source blocked")
	}
	if err := store.UpdateTriageStatus(row.ID, "protected"); err != nil {
		t.Fatal(err)
	}
	if app.proactiveVoiceSessionsAllowed([]string{row.ID}) {
		t.Fatal("protected source allowed")
	}
	if app.proactiveVoiceSessionsAllowed([]string{"missing-session"}) {
		t.Fatal("missing source allowed")
	}
}

func TestProactiveVoiceSourceCalendarRechecksMergedPrivacy(t *testing.T) {
	for _, change := range []string{"private-event", "private-snapshot", "protected-parser-app", "pause", "invalid-pause-history"} {
		t.Run(change, func(t *testing.T) {
			app := newPrivacyReadTestApp(t)
			previous := pii.GetUserSensitiveApps()
			t.Cleanup(func() { pii.SetUserSensitiveApps(previous) })
			pii.SetUserSensitiveApps(nil)
			ctx := context.Background()
			now := time.Now()
			event := proactiveVoiceTestEvent(t, app, now)
			insertTestSession(t, app.activity.store, "calendar-source", "done", 1000)
			row := calendarEventRow(event)
			row.SessionID = "calendar-source"
			if err := app.store.UpsertCalendarEvents(ctx, []storage.CalendarEventRow{row}, now.UnixMilli()); err != nil {
				t.Fatal(err)
			}
			moment := meetingPrepMoment(event, now, time.Local)
			if _, reason := app.loadProactiveVoiceSource(ctx, moment, now, "en"); reason != "" {
				t.Fatalf("safe initial read rejected: %q", reason)
			}
			var err error
			switch change {
			case "private-event":
				_, err = app.activity.store.db.Exec(`UPDATE activity_events SET data='{"windowTitle":"Project - Incognito"}' WHERE session_id='calendar-source'`)
			case "private-snapshot":
				_, err = app.activity.store.db.Exec(`UPDATE activity_snapshots SET parsed_json='{"title":"Project - InPrivate"}' WHERE session_id='calendar-source'`)
			case "protected-parser-app":
				_, err = app.activity.store.db.Exec(`UPDATE activity_snapshots SET app='slack' WHERE session_id='calendar-source'`)
				pii.SetUserSensitiveApps(map[string]string{"com.tinyspeck.slackmacgap": "Team conversations"})
			case "pause":
				app.saveObservationPauses([]observationPause{{Start: 1000, End: 2000}})
			case "invalid-pause-history":
				err = app.store.SetUIState(ctx, observationPausesKey, "not-json")
			}
			if err != nil {
				t.Fatal(err)
			}
			// Same context and timestamp as the first load: policy/source
			// mutation must invalidate admission before accepting a result.
			if _, reason := app.loadProactiveVoiceSource(ctx, moment, now, "en"); reason != "no_source" {
				t.Fatalf("withdrawn source accepted after %s: %q", change, reason)
			}
		})
	}
}

func TestProactiveVoiceSourceRetainedPrivacyReceipts(t *testing.T) {
	app := newPrivacyReadTestApp(t)
	insertTestSession(t, app.activity.store, "retained-source", "done", 1000)
	if !app.proactiveVoiceSessionsAllowed([]string{"retained-source"}) {
		t.Fatal("safe live source rejected")
	}
	if err := app.activity.store.deleteRetainedSession("retained-source"); err != nil {
		t.Fatal(err)
	}
	if !app.proactiveVoiceSessionsAllowed([]string{"retained-source"}) {
		t.Fatal("retention receipt lost safe source provenance")
	}
	app.saveObservationPauses([]observationPause{{Start: 1000, End: 2000}})
	if app.proactiveVoiceSessionsAllowed([]string{"retained-source"}) {
		t.Fatal("retained source ignored current pause history")
	}
	app.saveObservationPauses(nil)
	if err := app.activity.store.DeleteSession("retained-source"); err != nil {
		t.Fatal(err)
	}
	if app.proactiveVoiceSessionsAllowed([]string{"retained-source"}) {
		t.Fatal("explicit deletion did not override retained receipt")
	}
}

func TestProactiveVoiceSourceMarkdownUsesFullReadProvenance(t *testing.T) {
	for _, kind := range []string{taskContextSourceMemoryKind, taskContextSourceKnowledgeKind} {
		t.Run(kind, func(t *testing.T) {
			app := newPrivacyReadTestApp(t)
			insertTestSession(t, app.activity.store, "visible-source", "done", 1000)
			insertTestSession(t, app.activity.store, "flattened-source", "done", 100_000)
			content := renderOKFDocument(map[string]any{
				"title": "Project notes", "activity_sessions": []string{"visible-source"},
				"read_provenance": memoryReadProvenanceJSON(memoryReadProvenance{Version: 2, Kinds: []string{"activity"}, Sessions: []string{"flattened-source"}}),
			}, "Prepare the project notes.")
			dir, err := memoryDir()
			if kind == taskContextSourceKnowledgeKind {
				dir, err = knowledgeDir()
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "voice-provenance.md"), []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			evidence := PurposeEvidenceRecord{SourceKind: kind, SourceID: "voice-provenance:live", SourceHash: agencyPayloadHash([]byte(strings.TrimSpace(content)))}
			if !app.proactiveVoicePurposeSourceAllowed(context.Background(), evidence) {
				t.Fatal("current safe markdown source rejected")
			}
			if _, err := app.activity.store.db.Exec(`UPDATE activity_events SET data='{"windowTitle":"Project - Incognito"}' WHERE session_id='flattened-source'`); err != nil {
				t.Fatal(err)
			}
			if app.proactiveVoicePurposeSourceAllowed(context.Background(), evidence) {
				t.Fatal("flattened private source bypassed full markdown read filter")
			}
		})
	}
}

func TestProactiveVoiceSourcePurposeRetainsCarriedFieldPrivacy(t *testing.T) {
	app := newPrivacyReadTestApp(t)
	now := time.Now().Add(-time.Second)
	insertTestSession(t, app.activity.store, "purpose-source", "done", 1000)
	content := "# Project notes\n\n- **Activity Session**: purpose-source\n\nPrepare the project notes.\n"
	writePrivacyMemory(t, "voice-purpose", content)
	evidence := purposeTestEvidence("voice-purpose", PurposeEvidenceExplicitUser, PurposeStanceSupport, now.Add(-time.Hour).UnixMilli())
	evidence.SourceKind, evidence.SourceID = taskContextSourceMemoryKind, "voice-purpose:live"
	evidence.SourceHash = agencyPayloadHash([]byte(strings.TrimSpace(content)))
	input := purposeTestCreateInput("voice-purpose", PurposeKindCommitment, "project notes", []PurposeEvidenceInput{evidence}, now.Add(-time.Hour).UnixMilli())
	store := NewPurposeMapStore(app.activity.store.db)
	created, _, err := store.Create(input)
	if err != nil {
		t.Fatal(err)
	}
	detail, err := store.GetDetail(created.OpenLoop.ID)
	if err != nil {
		t.Fatal(err)
	}
	update := purposeTestAppendInput(detail, "voice-purpose-update", PurposeEventRevised, PurposeStanceUpdate, PurposeEvidenceExplicitUser, "current user revision", now.UnixMilli())
	update.Evidence = []PurposeEvidenceInput{userPurposeEvidence("voice-purpose-update", "chat", PurposeStanceUpdate, "Keep working on it", now.UnixMilli())}
	if _, _, err := store.AppendEvent(update); err != nil {
		t.Fatal(err)
	}
	if _, ok := app.proactiveVoicePurpose(context.Background(), created.OpenLoop.ID, now); !ok {
		t.Fatal("safe inherited Purpose fields rejected")
	}
	if _, err := app.activity.store.db.Exec(`UPDATE activity_events SET data='{"windowTitle":"Project - Incognito"}' WHERE session_id='purpose-source'`); err != nil {
		t.Fatal(err)
	}
	if _, ok := app.proactiveVoicePurpose(context.Background(), created.OpenLoop.ID, now); ok {
		t.Fatal("current user revision laundered protected inherited fields")
	}
}

func TestProactiveVoiceSourcePurposeRechecksReminderAndMailHold(t *testing.T) {
	app := mainChatTestApp(t)
	app.activity = &activityMonitorState{store: newTestStore(t)}
	now := time.Now().Truncate(time.Second)
	row := storage.ReminderRow{UID: "local-reminder", Title: "Send project notes", DueAt: now.Add(time.Hour).UnixMilli(), List: "Work"}
	ctx := context.Background()
	if err := app.store.UpsertReminders(ctx, []storage.ReminderRow{row}, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	purpose := NewPurposeMapStore(app.activity.store.db)
	created, _, err := purpose.Create(reminderCreateInput(reminderFromRow(row), now.Add(-time.Hour).UnixMilli(), time.Local))
	if err != nil {
		t.Fatal(err)
	}
	moment := engagement.Moment{Kind: engagement.KindCommitmentSlipping, SubjectID: created.OpenLoop.ID}
	source, reason := app.loadProactiveVoiceSource(ctx, moment, now, "en")
	if reason != "" || len(source.Items) != 1 || *source.Items[0].Progress != "none recorded" {
		t.Fatalf("source = %+v reason = %q", source, reason)
	}
	mailRow := storage.MailMessageRow{ID: "mail-hold", Mailbox: storage.MailboxInbox, Subject: row.Title, At: now.UnixMilli()}
	if _, err := app.store.UpsertMailMessages(ctx, []storage.MailMessageRow{mailRow}, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, reason := app.loadProactiveVoiceSource(ctx, moment, now, "en"); reason != "no_source" {
		t.Fatalf("mail-derived title accepted: %q", reason)
	}
	row.Title = "Edited reminder"
	if err := app.store.UpsertReminders(ctx, []storage.ReminderRow{row}, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, reason := app.loadProactiveVoiceSource(ctx, moment, now, "en"); reason != "no_source" {
		t.Fatalf("stale reminder projection accepted: %q", reason)
	}
}

func TestProactiveVoiceSourceBriefRevalidatesTopItems(t *testing.T) {
	app := mainChatTestApp(t)
	now := time.Now().Truncate(time.Second)
	event := proactiveVoiceTestEvent(t, app, now)
	brief := MobileBriefV1{Version: 1, BriefID: now.Format("2006-01-02") + "-morning", Slot: mobileBriefSlotMorning, AsOf: now.Format(time.RFC3339), Evidence: []MobileBriefRef{
		{Ref: "e1", SourceKind: mobileBriefCalendarSource, SourceID: event.ID},
		{Ref: "e2", SourceKind: "screen_note", SourceID: "private-screen"},
	}, Sections: MobileBriefSections{Meetings: []MobileBriefItem{{Title: event.Title, Refs: []string{"e1"}}, {Title: "Private screen content", Refs: []string{"e2"}}}}, Narrative: []MobileBriefPara{{Text: "Private generated narrative"}}}
	store := newMobileBriefStore(t.TempDir())
	if err := store.save(brief, now); err != nil {
		t.Fatal(err)
	}
	// GetMobileBrief now applies the owner's current delivery policy. A
	// service without an App correctly withholds every stored source.
	app.mobileBriefSvc = &mobileBriefService{app: app, store: store, now: func() time.Time { return now }}
	moment := engagement.Moment{Kind: engagement.KindBriefReady, SubjectID: brief.BriefID}
	source, reason := app.loadProactiveVoiceSource(context.Background(), moment, now, "en")
	if reason != "" || len(source.Items) != 1 || len(source.Items[0].TopItems) != 1 || source.Items[0].TopItems[0].Text != event.Title {
		t.Fatalf("source = %+v reason = %q", source, reason)
	}
	raw, _ := json.Marshal(source)
	if strings.Contains(string(raw), "Private") || strings.Contains(string(raw), "local-only") {
		t.Fatalf("unapproved brief text exposed: %s", raw)
	}
	event.Title = "Changed event"
	if err := app.store.UpsertCalendarEvents(context.Background(), []storage.CalendarEventRow{calendarEventRow(event)}, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, reason := app.loadProactiveVoiceSource(context.Background(), moment, now, "en"); reason != "no_source" {
		t.Fatalf("stale brief accepted: %q", reason)
	}
}

func TestProactiveVoiceSourcePromisedDayUsesMessageTime(t *testing.T) {
	when := time.Date(2026, 10, 15, 23, 30, 0, 0, time.FixedZone("source", -7*60*60))
	tests := []struct{ text, day string }{
		{"I will send Design review tomorrow.", "2026-10-16"},
		{"I'll send Design review tonight.", "2026-10-15"},
		{"I plan to send Design review today.", "2026-10-15"},
		{"I will send Design review by 2026-10-16.", "2026-10-16"},
		{"I will send Design review on 2026/10/16.", "2026-10-16"},
		{"我会在明天发送设计评审。", "2026-10-16"},
		{"我打算今晚发送设计评审。", "2026-10-15"},
		{"Design review happens tomorrow.", ""},
		{"I will send Design review. The meeting is tomorrow.", ""},
		{"I will send Design review before tomorrow.", ""},
		{"I will send Design review tomorrow or Friday.", ""},
		{"I will send Design review today or tomorrow.", ""},
		{"I will send Design review on 2026-02-30.", ""},
		{"I will send Design review next week.", ""},
	}
	for _, test := range tests {
		t.Run(test.text, func(t *testing.T) {
			result := proactiveVoicePromisedDay(test.text, when)
			day := ""
			if result != nil {
				day = result.Format("2006-01-02")
			}
			if day != test.day {
				t.Fatalf("day = %q want %q", day, test.day)
			}
		})
	}
}

func TestProactiveVoiceSourceBriefNamesOffWithholdsTopTitles(t *testing.T) {
	now := time.Now()
	source := proactiveVoiceSource{Items: []proactivevoice.Item{{
		Ref: "brief-local", Type: "brief", Slot: proactiveVoiceSourceDisplay("morning"),
		TopItems: []proactivevoice.TopItem{{Ref: "event-local", Text: "Design review"}},
	}}}
	job, _, reason := assembleProactiveVoiceJob("brief_ready", "en", false, now, source)
	if reason != "no_source" {
		t.Fatalf("names-off titles accepted: %q", reason)
	}
	encoded, err := json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "Design review") {
		t.Fatalf("title entered provider job: %s", encoded)
	}
	if _, _, reason := assembleProactiveVoiceJob("brief_ready", "en", true, now, source); reason != "" {
		t.Fatalf("names-on brief rejected: %q", reason)
	}
}

func TestProactiveVoiceSourceDraftSubjectRejectsRestrictedText(t *testing.T) {
	source := proactiveVoiceSource{
		Items:          []proactivevoice.Item{{Ref: "thread-local", Type: "mail_thread", FromName: proactiveVoiceSourceString("Dana"), WaitingSince: proactiveVoiceSourceDisplay("Tuesday, Oct 13")}},
		RawMailSubject: "Design review", RawMailBody: "Please review the design.",
		DraftRecipient: "dana@example.com", MailBodyConsent: true,
	}
	for _, subject := range []string{"Re: USD 500", "Re: Doctor appointment", "回复：医疗检查"} {
		t.Run(subject, func(t *testing.T) {
			source.DraftSubject = subject
			job, addresses, reason := assembleProactiveVoiceJob("waiting_on_reply", "en", true, time.Now(), source)
			if reason != "unsafe_content" {
				t.Fatalf("restricted draft subject accepted: %q", reason)
			}
			if job.DraftSubject != "" || job.DraftAllowed || len(addresses) != 0 {
				t.Fatalf("restricted draft entered job: %+v", job)
			}
		})
	}
}
