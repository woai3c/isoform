package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"wails-gui/storage"
)

// The confirm card for removing a schedule from the main chat. UpcomingRemove
// posts it; nothing is removed until the user presses Remove on it, which
// calls ResolveUpcomingConfirm. The card's state lives in its own row, so it
// survives a restart, and a card answers once and only within an hour.

// upcomingConfirmTool is the ToolName of the card's system row.
const upcomingConfirmTool = "upcoming_confirm"

// upcomingConfirmTTL is how long a card can be answered.
const upcomingConfirmTTL = time.Hour

// Card states.
const (
	upcomingConfirmPending = "pending"
	upcomingConfirmRemoved = "removed"
	upcomingConfirmKept    = "kept"
	upcomingConfirmExpired = "expired"
	upcomingConfirmGone    = "gone"
)

// upcomingConfirmPayload is the card row's ToolInput.
type upcomingConfirmPayload struct {
	Action    string `json:"action"`
	JobID     string `json:"jobId"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	ExpiresAt int64  `json:"expiresAt"`
}

// upcomingConfirmMu serializes answers, so two presses on one card cannot
// both act on it. It also guards upcomingConfirmIssued.
var upcomingConfirmMu sync.Mutex

// upcomingPendingConfirm is what this process remembers about a card it
// posted. The card's row only displays; the schedule an answer removes comes
// from here, so a row written by anything else (or edited) can never remove
// a schedule, and a card from before a restart reads as expired.
type upcomingPendingConfirm struct {
	jobID     string
	expiresAt time.Time
}

var upcomingConfirmIssued = map[string]upcomingPendingConfirm{}

// UpcomingConfirmResult is what the card shows after the user answered.
type UpcomingConfirmResult struct {
	Status string `json:"status"`
}

// askToRemoveUpcoming checks that the target can be removed and posts the
// confirm card.
func (a *App) askToRemoveUpcoming(target upcomingTarget, now time.Time) (string, error) {
	switch target.Kind {
	case upcomingKindBrief:
		return "", errors.New("The brief cannot be removed. Offer to pause it instead.")
	case upcomingKindNews:
		return "", errors.New("Daily News cannot be removed. Offer to pause it instead.")
	default:
	}
	if target.Item.System {
		return "", errors.New("Isoform runs this for itself, so it cannot be removed. It can be paused.")
	}
	unapproved, err := a.agencyScheduleUnapproved(target.Job)
	if err != nil {
		return "", err
	}
	if unapproved {
		return "", upcomingRefusal(errAgencyScheduleAwaitsUser)
	}
	if a.store == nil {
		return "", errors.New("Upcoming is not available right now.")
	}
	main, err := a.ensureMainChat(context.Background())
	if err != nil {
		return "", errors.New("The confirm card could not be shown, so nothing was removed.")
	}
	payload, err := json.Marshal(upcomingConfirmPayload{
		Action:    "remove",
		JobID:     target.Job.ID,
		Title:     strings.TrimSpace(target.Job.Name),
		Status:    upcomingConfirmPending,
		ExpiresAt: now.Add(upcomingConfirmTTL).UnixMilli(),
	})
	if err != nil {
		return "", err
	}
	text := fmt.Sprintf("Remove %s from Upcoming? Confirm on the card on your computer.", target.subject())
	itemID := uuid.NewString()
	upcomingConfirmMu.Lock()
	for id, issued := range upcomingConfirmIssued {
		if now.After(issued.expiresAt) {
			delete(upcomingConfirmIssued, id)
		}
	}
	upcomingConfirmIssued[itemID] = upcomingPendingConfirm{jobID: target.Job.ID, expiresAt: now.Add(upcomingConfirmTTL)}
	upcomingConfirmMu.Unlock()
	if a.appendMainChatItem(context.Background(), main.LocalID, storage.StreamItemRow{
		ID:        itemID,
		Kind:      "system",
		Content:   text,
		ToolName:  upcomingConfirmTool,
		ToolInput: string(payload),
	}, "upcoming") == "" {
		upcomingConfirmMu.Lock()
		delete(upcomingConfirmIssued, itemID)
		upcomingConfirmMu.Unlock()
		return "", errors.New("The confirm card could not be shown, so nothing was removed.")
	}
	return fmt.Sprintf("A confirm card for removing %s is now in the chat. Nothing is removed until the user presses Remove on it; it expires in an hour. Tell the user in one sentence to confirm on the card.", target.subject()), nil
}

// ResolveUpcomingConfirm answers a confirm card: remove is the user's press
// on Remove, otherwise Keep. Only a pending card in the main chat that has
// not expired is acted on.
func (a *App) ResolveUpcomingConfirm(itemID string, remove bool) (UpcomingConfirmResult, error) {
	if a.store == nil {
		return UpcomingConfirmResult{}, errors.New("upcoming is not available")
	}
	ctx := context.Background()
	main, err := a.ensureMainChat(ctx)
	if err != nil {
		return UpcomingConfirmResult{}, err
	}
	upcomingConfirmMu.Lock()
	defer upcomingConfirmMu.Unlock()
	row, err := a.store.LoadStreamItem(ctx, strings.TrimSpace(itemID))
	if err != nil || row == nil || row.SessionLocalID != main.LocalID || row.Kind != "system" || row.ToolName != upcomingConfirmTool {
		return UpcomingConfirmResult{}, errors.New("this confirmation is not in the chat")
	}
	var payload upcomingConfirmPayload
	if err := json.Unmarshal([]byte(row.ToolInput), &payload); err != nil || payload.Action != "remove" || payload.JobID == "" {
		return UpcomingConfirmResult{}, errors.New("this confirmation cannot be read")
	}
	if payload.Status != upcomingConfirmPending {
		return UpcomingConfirmResult{Status: payload.Status}, nil
	}
	pending, known := upcomingConfirmIssued[row.ID]
	var status string
	var removed *upcomingTarget
	switch {
	case !known || pending.jobID != payload.JobID:
		// Posted before a restart, or not posted by this process at all.
		status = upcomingConfirmExpired
	default:
		payload.ExpiresAt = pending.expiresAt.UnixMilli()
		status, removed, err = a.answerUpcomingConfirm(payload, remove, time.Now())
		if err != nil {
			return UpcomingConfirmResult{}, err
		}
	}
	delete(upcomingConfirmIssued, row.ID)
	payload.Status = status
	if updated, err := json.Marshal(payload); err == nil {
		if err := a.store.UpdateStreamItemToolInput(ctx, row.ID, string(updated)); err != nil {
			log.Printf("[upcoming] confirm card state not saved: %v", err)
		} else {
			a.mainChatRowsChanged(row.SessionLocalID)
		}
	}
	if removed != nil {
		a.postUpcomingReport(*removed, upcomingReportRemoved, "", fmt.Sprintf("Removed %s from Upcoming.", removed.subject()))
	}
	return UpcomingConfirmResult{Status: status}, nil
}

// answerUpcomingConfirm carries out the user's answer to a pending card. It
// returns the card's new state and, when a schedule was removed, its target
// for the record line.
func (a *App) answerUpcomingConfirm(payload upcomingConfirmPayload, remove bool, now time.Time) (string, *upcomingTarget, error) {
	if !remove {
		return upcomingConfirmKept, nil, nil
	}
	if now.UnixMilli() > payload.ExpiresAt {
		return upcomingConfirmExpired, nil, nil
	}
	// The stored row decides whether the schedule still exists; the
	// scheduler's cache can be empty while it loads.
	row, err := a.store.LoadCronJob(context.Background(), payload.JobID)
	if errors.Is(err, sql.ErrNoRows) {
		return upcomingConfirmGone, nil, nil
	}
	if err != nil || row == nil {
		// The card stays pending.
		return "", nil, errors.New("the schedule could not be read; try again")
	}
	job := jobRowToState(row)
	// The same checks as the chat tool: the app's own jobs are never
	// removed from a card, and a proposal waiting for approval is the
	// user's to remove in Upcoming, never from a card the agent posted.
	if upcomingSystemSurface(job.SurfaceKind) || !upcomingListsSurface(job.SurfaceKind) {
		return "", nil, errors.New("Isoform runs this for itself, so it cannot be removed")
	}
	if err := a.deleteCronJobFromAPI(job.ID); err != nil {
		return "", nil, upcomingRefusal(err)
	}
	return upcomingConfirmRemoved, &upcomingTarget{Kind: upcomingKindSchedule, Job: job}, nil
}
