package main

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// TodayNeedResolution is a source-checked receipt for an action that changed
// after the waiting row was shown. Its fields are presentation only.
type TodayNeedResolution struct {
	Kind   string `json:"kind"`
	Person string `json:"person,omitempty"`
	At     int64  `json:"at,omitempty"`
}

// GetTodayNeedResolution identifies a reply only when its retained draft and
// current source receipt still pass the same checks as the send action.
func (a *App) GetTodayNeedResolution(momentID string) (TodayNeedResolution, error) {
	var empty TodayNeedResolution
	if a == nil || a.store == nil || !mobileValidID(momentID) {
		return empty, errors.New("waiting item unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stamp, err := a.peopleReadStamp(ctx)
	if err != nil {
		return empty, err
	}
	tasks, err := a.mobileDraftTasks(ctx, time.Now())
	if err != nil {
		return empty, err
	}
	for _, task := range tasks {
		if task.Card.MomentID != momentID || task.RepliedAt <= 0 || a.validateMainChatSend(ctx, &task.Card) != nil {
			continue
		}
		// A nameless recipient is still a proven reply: the row says "your reply".
		person := mobileOfferText(task.recipientName(a), 80)
		release, err := a.lockPeoplePublication(ctx, stamp)
		if err != nil {
			return empty, err
		}
		defer release()
		return TodayNeedResolution{Kind: "you_replied", Person: person, At: task.RepliedAt}, nil
	}
	// A stored terminal state is evidence of an explicit action. The projected
	// card and its original source must both remain readable; a failed lookup
	// or a privacy withdrawal is not evidence that the work is done.
	item, err := a.LoadMainChatProactiveItem(momentID)
	if err != nil || item == nil {
		return empty, errors.New("waiting item could not be checked")
	}
	var projected mainChatProactive
	if err := json.Unmarshal([]byte(item.ToolInput), &projected); err != nil || projected.MomentID != momentID || projected.Kind != "waiting_on_reply" {
		return empty, errors.New("waiting item could not be checked")
	}
	main, err := a.store.LoadMainSession(ctx)
	if err != nil || main == nil {
		return empty, errors.New("waiting item could not be checked")
	}
	row, err := a.store.FindProactiveStreamItem(ctx, main.LocalID, momentID)
	if err != nil || row == nil {
		return empty, errors.New("waiting item could not be checked")
	}
	var root mainChatProactive
	if json.Unmarshal([]byte(row.ToolInput), &root) != nil {
		return empty, errors.New("waiting item could not be checked")
	}
	card := findMainChatProactiveCard(&root, momentID)
	if card == nil || a.validateMainChatSend(ctx, card) != nil {
		return empty, errors.New("waiting item could not be checked")
	}
	switch card.State {
	case "acted", "dismissed":
		release, err := a.lockPeoplePublication(ctx, stamp)
		if err != nil {
			return empty, err
		}
		defer release()
		return TodayNeedResolution{Kind: "resolved"}, nil
	case "pending", "opened":
		return empty, nil
	default:
		return empty, errors.New("waiting item could not be checked")
	}
}
