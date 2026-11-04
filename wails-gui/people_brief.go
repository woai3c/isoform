package main

import (
	"context"
	"errors"
	"strings"
	"time"
)

// PersonBrief is "Brief me before we talk" in a person's detail on Who you
// know: Today's Before you talk lines, keyed by person instead of a meeting.
// An empty line is left out by the page.
type PersonBrief struct {
	PersonID       string `json:"personId"`
	YouOwe         string `json:"youOwe,omitempty"`
	TheyAsked      string `json:"theyAsked,omitempty"`
	HasUnsentDraft bool   `json:"hasUnsentDraft,omitempty"`
	LastTime       string `json:"lastTime,omitempty"`
	LastTimeAt     int64  `json:"lastTimeAt,omitempty"`
	LastDurationMs int64  `json:"lastDurationMs,omitempty"`
}

// GetPersonBrief fills the Before you talk lines for one person from what is
// open between you and your last meeting together. It writes nothing.
func (a *App) GetPersonBrief(personID string) (perfValue *PersonBrief, perfErr error) {
	defer logPageBinding("GetPersonBrief", time.Now(), &perfValue)
	if a == nil || a.store == nil {
		return nil, errors.New("the store is not ready")
	}
	personID = strings.TrimSpace(personID)
	if personID == "" {
		return nil, errors.New("no person")
	}
	ctx := context.Background()
	stamp, err := a.peopleReadStamp(ctx)
	if err != nil {
		return nil, err
	}
	detail, err := a.GetPersonDetail(personID)
	if err != nil {
		return nil, err
	}
	view := &TodayBeforeYouTalk{PersonID: personID, PersonName: detail.Name}
	a.fillTodayBeforeYouTalkPerson(ctx, view, detail)
	a.fillTodayBeforeYouTalkLastTime(ctx, view, time.Now())
	release, err := a.lockPeoplePublication(ctx, stamp)
	if err != nil {
		return nil, err
	}
	defer release()
	return &PersonBrief{
		PersonID:       personID,
		YouOwe:         view.YouOwe,
		TheyAsked:      view.TheyAsked,
		HasUnsentDraft: view.HasUnsentDraft,
		LastTime:       view.LastTime,
		LastTimeAt:     view.LastTimeAt,
		LastDurationMs: view.LastDurationMs,
	}, nil
}

// personReplyDraftMoment proves momentID is a reply owed to this person that
// still waits on a real mail source.
func (a *App) personReplyDraftMoment(ctx context.Context, personID, momentID string) error {
	detail, err := a.GetPersonDetail(personID)
	if err != nil {
		return errTodayReplyDraftUnavailable
	}
	for _, loop := range detail.Loops {
		if loop.Kind == "reply_owed" && loop.ID == momentID {
			if _, ok := a.mobileBriefWaitingReply(ctx, momentID, time.Now()); !ok {
				return errTodayReplyDraftUnavailable
			}
			return nil
		}
	}
	return errTodayReplyDraftUnavailable
}

// OpenPersonReplyDraft is Draft a reply in a person's detail: Today's reply
// draft for the reply owed, keyed by person. It never sends; the task's Send
// path repeats its source checks. CancelTodayReplyDraft(requestID) stops it.
func (a *App) OpenPersonReplyDraft(personID, momentID, requestID string) (string, error) {
	ctx, finish, err := startTodayReplyRun(requestID)
	if err != nil {
		return "", err
	}
	defer finish()
	todayReplyDraftMu.Lock()
	defer todayReplyDraftMu.Unlock()
	if ctx.Err() != nil {
		return "", errTodayReplyDraftUnavailable
	}
	if err := a.personReplyDraftMoment(ctx, personID, momentID); err != nil {
		return "", err
	}
	if id, err := a.DesktopDraftTaskID(momentID); err == nil {
		todayReplyRunMu.Lock()
		if ctx.Err() != nil {
			todayReplyRunMu.Unlock()
			return "", errTodayReplyDraftUnavailable
		}
		todayReplyRuns[requestID].committed = true
		todayReplyRunMu.Unlock()
		return id, nil
	}
	row, err := a.store.GetEngagementMoment(ctx, momentID)
	if err != nil || row == nil {
		return "", errTodayReplyDraftUnavailable
	}
	main, err := a.ensureMainChat(ctx)
	if err != nil {
		return "", errTodayReplyDraftUnavailable
	}
	card, err := a.makeTodayReplyDraft(ctx, momentFromRow(*row))
	if err != nil {
		return "", errTodayReplyDraftUnavailable
	}
	if err := a.personReplyDraftMoment(ctx, personID, momentID); err != nil {
		return "", err
	}
	mainChatProactiveActionMu.Lock()
	defer mainChatProactiveActionMu.Unlock()
	todayReplyRunMu.Lock()
	if ctx.Err() != nil {
		todayReplyRunMu.Unlock()
		return "", errTodayReplyDraftUnavailable
	}
	posted := a.installTodayReplyDraft(ctx, main.LocalID, card)
	if posted != "" {
		todayReplyRuns[requestID].committed = true
	}
	todayReplyRunMu.Unlock()
	if posted == "" {
		return "", errTodayReplyDraftUnavailable
	}
	id, err := a.DesktopDraftTaskID(momentID)
	if err != nil {
		return "", errTodayReplyDraftUnavailable
	}
	return id, nil
}
