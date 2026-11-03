package main

import (
	"context"
	"net/mail"
	"strings"
	"time"

	"wails-gui/storage"

	"wails-gui/internal/calendar"
)

// A meeting note goes only to the selected, currently readable attendee's
// address on the calendar event. A name in a model response is not a route.
func (a *App) todayMeetingNoteRecipient(ctx context.Context, meetingID string) (principal, name, address, subject string, err error) {
	meeting, err := a.todayBeforeYouTalkMeetingAt(ctx, meetingID, time.Now())
	if err != nil || meeting == nil {
		return "", "", "", "", errTodayChatItemUnavailable
	}
	event, ok, err := a.store.GetCalendarEvent(ctx, meetingID)
	if err != nil || !ok {
		return "", "", "", "", errTodayChatItemUnavailable
	}
	for _, attendee := range event.Attendees {
		if attendee.IsMe || attendee.Status == calendar.StatusDeclined || attendee.PrincipalID != meeting.PersonID {
			continue
		}
		candidate := strings.TrimSpace(attendee.Email)
		parsed, parseErr := mail.ParseAddress(candidate)
		if parseErr != nil || parsed.Address != candidate || strings.ContainsAny(candidate, "\r\n") || address != "" && address != candidate {
			return "", "", "", "", errTodayChatItemUnavailable
		}
		address = candidate
	}
	subject = strings.TrimSpace(meeting.MeetingTitle)
	if address == "" || subject == "" || len(subject) > 998 || strings.ContainsAny(subject, "\r\n") {
		return "", "", "", "", errTodayChatItemUnavailable
	}
	return meeting.PersonID, meeting.PersonName, address, subject, nil
}

// TodayMeetingNote is the verified route and saved text shown on the draft
// card. A sent or uncertain attempt cannot acquire another send reservation.
type TodayMeetingNote struct {
	Answer  TodayPanelAnswer `json:"answer"`
	Name    string           `json:"name"`
	Address string           `json:"address"`
	Subject string           `json:"subject"`
	State   string           `json:"state"`
}

func todayMeetingNoteSendID(answerID string) string { return "today-meeting-note:" + answerID }

func (a *App) GetTodayMeetingNote(ref TodayChatContextRef) (*TodayMeetingNote, error) {
	if !ref.MeetingNote || ref.MeetingPrep || ref.SourceKind != "calendar" {
		return nil, errTodayChatItemUnavailable
	}
	answer, err := a.GetTodayPanelAnswer(ref)
	if err != nil || answer == nil {
		return nil, err
	}
	ctx := context.Background()
	_, name, address, subject, err := a.todayMeetingNoteRecipient(ctx, ref.SourceID)
	if err != nil {
		return nil, err
	}
	// Recheck after reading the route, so a changed attendee never receives
	// a note generated for a previously selected address.
	current, err := a.GetTodayPanelAnswer(ref)
	if err != nil || current == nil || current.AnswerItemID != answer.AnswerItemID {
		return nil, errTodayChatItemUnavailable
	}
	sent, err := a.store.LoadSingleOutboundSend(ctx, todayMeetingNoteSendID(answer.AnswerItemID))
	if err != nil {
		return nil, err
	}
	state := "ready"
	if sent != nil {
		answer.Answer = sent.Text
		switch sent.State {
		case storage.OutboundSendAccepted, storage.OutboundSendFinalized:
			state = "sent"
		default:
			state = "unknown"
		}
	}
	return &TodayMeetingNote{Answer: *answer, Name: name, Address: address, Subject: subject, State: state}, nil
}

// SendTodayMeetingNote is only called after the user has reviewed the card and
// its Undo window has elapsed. It rechecks the source and uses a durable,
// single-attempt receipt before invoking the existing Mail transport.
func (a *App) SendTodayMeetingNote(ref TodayChatContextRef, answerID, text string) error {
	if a == nil || a.store == nil {
		return errTodayChatItemUnavailable
	}
	mainChatProactiveActionMu.Lock()
	defer mainChatProactiveActionMu.Unlock()
	note, err := a.GetTodayMeetingNote(ref)
	if err != nil || note == nil || note.Answer.AnswerItemID != answerID || note.State != "ready" {
		return errTodayChatItemUnavailable
	}
	ctx := context.Background()
	if !a.proactiveVoicePermission(ctx, mailPermissionKey) {
		return errMailNotAllowed
	}
	principal, name, address, subject, err := a.todayMeetingNoteRecipient(ctx, ref.SourceID)
	if err != nil || name != note.Name || address != note.Address || subject != note.Subject {
		return errTodayChatItemUnavailable
	}
	text = strings.TrimSpace(text)
	if len(text) > mainChatEditedDraftMax {
		return errTodayChatItemUnavailable
	}
	draft := mainChatProactiveDraft{To: address, Subject: subject, Text: text, Channel: "mail", Recipient: name, PrincipalID: principal}
	draft.Revision = mainChatDraftRevision(draft)
	if !mainChatDraftSendable(&draft) {
		return errTodayChatItemUnavailable
	}
	// Check source once more immediately before reserving the send.
	check, err := a.GetTodayPanelAnswer(ref)
	if err != nil || check == nil || check.AnswerItemID != answerID {
		return errTodayChatItemUnavailable
	}
	row := storage.OutboundSendRow{MomentID: todayMeetingNoteSendID(answerID), Revision: draft.Revision, Channel: "mail", PrincipalID: principal, Recipient: name, DraftTo: address, Subject: subject, Text: text, SourceKey: "calendar:" + ref.SourceID}
	return a.sendTodayDraft(ctx, row)
}
