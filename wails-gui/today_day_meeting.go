package main

import (
	"context"
	"errors"
	"strings"
	"time"

	"wails-gui/internal/calendar"
)

var errTodayDayMeetingUnavailable = errors.New("meeting is no longer available")

type TodayDayMeetingTarget struct {
	Kind      string              `json:"kind"` // prep or notes
	Meeting   *TodayBeforeYouTalk `json:"meeting,omitempty"`
	SessionID string              `json:"sessionId,omitempty"`
}

// GetTodayDayMeetingTarget rechecks the exact calendar occurrence and any
// notes before publishing a target. It never writes to a calendar.
func (a *App) GetTodayDayMeetingTarget(id string) (TodayDayMeetingTarget, error) {
	if a == nil || a.store == nil {
		return TodayDayMeetingTarget{}, errTodayDayMeetingUnavailable
	}
	ctx := context.Background()
	stamp, err := a.peopleReadStamp(ctx)
	if err != nil {
		return TodayDayMeetingTarget{}, err
	}
	target, err := a.todayDayMeetingTargetAt(ctx, id, a.todayDayShapeNow())
	if err != nil {
		return TodayDayMeetingTarget{}, err
	}
	release, err := a.lockPeoplePublication(ctx, stamp)
	if err != nil {
		return TodayDayMeetingTarget{}, err
	}
	defer release()
	return target, nil
}

func (a *App) todayDayMeetingTargetAt(ctx context.Context, id string, now time.Time) (TodayDayMeetingTarget, error) {
	if a == nil || a.store == nil || strings.TrimSpace(id) == "" || len(id) > 2048 {
		return TodayDayMeetingTarget{}, errTodayDayMeetingUnavailable
	}
	policy, err := a.mcpReadPrivacy(ctx)
	if err != nil || !policy.calendar {
		return TodayDayMeetingTarget{}, errTodayDayMeetingUnavailable
	}
	row, found, err := a.store.GetCalendarEvent(ctx, id)
	if err != nil {
		return TodayDayMeetingTarget{}, err
	}
	if !found || row.AllDay || row.DeletedAt != 0 || row.Status == "canceled" || row.MyStatus == calendar.StatusDeclined || row.EndAt <= row.StartAt || !policy.calendarAllowed(row) {
		return TodayDayMeetingTarget{}, errTodayDayMeetingUnavailable
	}
	day := time.UnixMilli(row.StartAt).In(time.Local)
	if validateTodayDayShapeDate(day, now) != nil {
		return TodayDayMeetingTarget{}, errTodayDayMeetingUnavailable
	}
	subjects, err := mobileBriefMailSubjects(a)
	if err != nil || mobileBriefValueEchoesMail(row, subjects) {
		return TodayDayMeetingTarget{}, errTodayDayMeetingUnavailable
	}
	event := calendarEventFromRow(row)
	if todayEventKind(event) != "meeting" {
		return TodayDayMeetingTarget{}, errTodayDayMeetingUnavailable
	}
	if row.EndAt <= now.UnixMilli() {
		if row.SessionID == "" {
			return TodayDayMeetingTarget{}, errTodayDayMeetingUnavailable
		}
		notes, err := a.GetMeetingNotesForSessions([]string{row.SessionID})
		if err != nil || len(notes) == 0 {
			return TodayDayMeetingTarget{}, errTodayDayMeetingUnavailable
		}
		return TodayDayMeetingTarget{Kind: "notes", SessionID: row.SessionID}, nil
	}
	name, _, _ := todayEventNames(event)
	view := &TodayBeforeYouTalk{MeetingID: row.ID, MeetingTitle: row.Title, StartAt: row.StartAt, EndAt: row.EndAt, ConferenceURL: safeConferenceURL(row.ConferenceURL), PersonName: name}
	if personID := todayBeforeYouTalkPerson(row); personID != "" {
		detail, err := a.getPersonDetail(personID, newPeopleReadGateCache())
		if err != nil {
			return TodayDayMeetingTarget{}, err
		}
		if strings.TrimSpace(detail.Name) != "" {
			view.PersonID, view.PersonName = detail.ID, detail.Name
			a.fillTodayBeforeYouTalkPerson(ctx, view, detail)
			a.fillTodayBeforeYouTalkLastTime(ctx, view, now)
		}
	}
	if strings.TrimSpace(view.PersonName) == "" {
		view.PersonName = row.Title
	}
	return TodayDayMeetingTarget{Kind: "prep", Meeting: view}, nil
}

// GetTodayPrepMeetingForPerson finds the meeting a Prep me chip opens when the
// card has no single calendar reference: the one meeting still to come today
// with that person id as an attendee. A name is never a lookup key; two
// meetings, or none, withhold the chip.
func (a *App) GetTodayPrepMeetingForPerson(personID string) (string, error) {
	if a == nil || a.store == nil {
		return "", errTodayDayMeetingUnavailable
	}
	ctx := context.Background()
	stamp, err := a.peopleReadStamp(ctx)
	if err != nil {
		return "", err
	}
	id, err := a.todayPrepMeetingForPersonAt(ctx, personID, a.todayDayShapeNow())
	if err != nil {
		return "", err
	}
	release, err := a.lockPeoplePublication(ctx, stamp)
	if err != nil {
		return "", err
	}
	defer release()
	return id, nil
}

func (a *App) todayPrepMeetingForPersonAt(ctx context.Context, personID string, now time.Time) (string, error) {
	personID = strings.TrimSpace(personID)
	if a == nil || a.store == nil || personID == "" || len(personID) > 256 {
		return "", errTodayDayMeetingUnavailable
	}
	policy, err := a.mcpReadPrivacy(ctx)
	if err != nil || !policy.calendar {
		return "", errTodayDayMeetingUnavailable
	}
	local := now.In(time.Local)
	endOfDay := time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, time.Local)
	rows, err := a.store.ListCalendarEvents(ctx, now.UnixMilli(), endOfDay.UnixMilli(), false)
	if err != nil {
		return "", err
	}
	match := ""
	for _, row := range rows {
		if row.StartAt <= now.UnixMilli() || row.StartAt >= endOfDay.UnixMilli() || row.AllDay || row.DeletedAt != 0 || row.Status == "canceled" ||
			row.MyStatus == calendar.StatusDeclined || row.EndAt <= row.StartAt || !policy.calendarAllowed(row) || todayEventKind(calendarEventFromRow(row)) != "meeting" {
			continue
		}
		attends := false
		for _, attendee := range row.Attendees {
			if !attendee.IsMe && attendee.Status != calendar.StatusDeclined && attendee.PrincipalID == personID {
				attends = true
				break
			}
		}
		if !attends {
			continue
		}
		if match != "" && match != row.ID {
			return "", errTodayDayMeetingUnavailable
		}
		match = row.ID
	}
	if match == "" {
		return "", errTodayDayMeetingUnavailable
	}
	target, err := a.todayDayMeetingTargetAt(ctx, match, now)
	if err != nil || target.Kind != "prep" || target.Meeting == nil || target.Meeting.MeetingID != match {
		return "", errTodayDayMeetingUnavailable
	}
	return match, nil
}
