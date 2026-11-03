package main

import (
	"context"
	"testing"
	"time"

	"wails-gui/storage"
)

func meetingNoteTestDraft(t *testing.T) (*App, TodayChatContextRef, storage.CalendarEventRow, *fakeProactiveSendTransport) {
	t.Helper()
	isolateActivityPrivacyTest(t)
	a := newTodayContextTestApp(t, true)
	for _, permission := range []string{calendarPermissionKey, mailPermissionKey} {
		if err := a.store.SetUIState(t.Context(), permissionUIStateKey(permission), "true"); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	person := peopleTestContact(t, a, "Mei Chen")
	event := beforeYouTalkEvent("note-event", person.ID, now.Add(20*time.Minute), now.Add(50*time.Minute))
	if err := a.store.UpsertCalendarEvents(t.Context(), []storage.CalendarEventRow{event}, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	ref := TodayChatContextRef{SourceKind: "calendar", SourceID: event.ID, MeetingNote: true, MeetingStartMs: event.StartAt, GeneratedTitle: event.Title, GeneratedPeople: []string{person.DisplayName}}
	main, err := a.ensureMainChat(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.admitTodayChatTurn(t.Context(), main.LocalID, storage.StreamItemRow{ID: "note-user", Kind: "user", Content: "Draft a note."}, ref, nil, true); err != nil {
		t.Fatal(err)
	}
	if err := a.store.BindAnswerChipUserRun(t.Context(), main.LocalID, "note-user", "note-run"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.AppendStreamItem(t.Context(), main.LocalID, storage.StreamItemRow{ID: "note-answer", Kind: "assistant", RunID: "note-run", Content: "See you soon."}); err != nil {
		t.Fatal(err)
	}
	a.savePanelAskAnswer(main.LocalID, "note-user", "note-run", "note-answer", "See you soon.")
	transport := &fakeProactiveSendTransport{}
	a.proactiveSendTransport = transport
	return a, ref, event, transport
}

func TestMeetingNoteOnlySendsReviewedTextOnce(t *testing.T) {
	a, ref, _, transport := meetingNoteTestDraft(t)
	note, err := a.GetTodayMeetingNote(ref)
	if err != nil || note == nil || note.State != "ready" || note.Answer.Answer != "See you soon." || transport.count() != 0 {
		t.Fatalf("draft=%+v sends=%d err=%v", note, transport.count(), err)
	}
	if err := a.SendTodayMeetingNote(ref, "note-answer", "See you at the meeting."); err != nil {
		t.Fatal(err)
	}
	if transport.count() != 1 || transport.calls[0].DraftTo != note.Address || transport.calls[0].Text != "See you at the meeting." {
		t.Fatalf("send=%+v", transport.calls)
	}
	if err := a.SendTodayMeetingNote(ref, "note-answer", "A second edit"); err == nil || transport.count() != 1 {
		t.Fatal("an edited replay sent again")
	}
	note, err = a.GetTodayMeetingNote(ref)
	if err != nil || note == nil || note.State != "sent" || note.Answer.Answer != "See you at the meeting." {
		t.Fatalf("sent state=%+v err=%v", note, err)
	}
}

func TestMeetingNoteRefusesAChangedRecipient(t *testing.T) {
	a, ref, event, transport := meetingNoteTestDraft(t)
	event.Attendees[0].Email = "new@example.test"
	if err := a.store.UpsertCalendarEvents(context.Background(), []storage.CalendarEventRow{event}, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if err := a.SendTodayMeetingNote(ref, "note-answer", "See you soon."); err == nil || transport.count() != 0 {
		t.Fatal("sent a draft after its recipient changed")
	}
}
