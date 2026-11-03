package main

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"time"

	"wails-gui/internal/handoff"
)

var errTodayNeedUncheckable = errors.New("today item could not be checked")

// GetTodayNeedCheck reads the source behind any Needs you row before it opens.
// An empty kind means the item is still open; "resolved" (or "you_replied")
// comes only from a successful read that shows the item closed or gone. Any
// failed or unreadable read is an error: the row says Isoform couldn't check.
func (a *App) GetTodayNeedCheck(id, kind, targetKind, targetID string) (TodayNeedResolution, error) {
	if a == nil || !mobileValidID(id) {
		return TodayNeedResolution{}, errTodayNeedUncheckable
	}
	switch targetKind {
	case "chat_item":
		if kind == "waiting_on_reply" {
			return a.GetTodayNeedResolution(id)
		}
		return a.todayChatNeedCheck(id, kind)
	case "handoff":
		if targetID != id {
			return TodayNeedResolution{}, errTodayNeedUncheckable
		}
		return a.todayHandoffNeedCheck(id)
	default:
		return TodayNeedResolution{}, errTodayNeedUncheckable
	}
}

func (a *App) todayChatNeedCheck(momentID, kind string) (TodayNeedResolution, error) {
	var open TodayNeedResolution
	if a.store == nil || a.IsClosedEyesModeEnabled() {
		return open, errTodayNeedUncheckable
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stamp, err := a.peopleReadStamp(ctx)
	if err != nil {
		return open, errTodayNeedUncheckable
	}
	main, err := a.store.LoadMainSession(ctx)
	if err != nil || main == nil {
		return open, errTodayNeedUncheckable
	}
	row, err := a.store.FindProactiveStreamItem(ctx, main.LocalID, momentID)
	if err != nil {
		return open, errTodayNeedUncheckable
	}
	resolved := row == nil
	if row != nil {
		var root mainChatProactive
		if json.Unmarshal([]byte(row.ToolInput), &root) != nil {
			return open, errTodayNeedUncheckable
		}
		card := findMainChatProactiveCard(&root, momentID)
		switch {
		case card == nil:
			resolved = true
		case card.Kind != kind:
			return open, errTodayNeedUncheckable
		case card.State == "acted" || card.State == "dismissed":
			resolved = true
		case card.State == "pending" || card.State == "opened":
			if card.Target.Kind == mainChatTargetHandoff && card.Target.ID != "" {
				work, err := a.todayHandoffNeedCheck(card.Target.ID)
				if err != nil {
					return open, err
				}
				resolved = work.Kind == "resolved"
			}
		default:
			return open, errTodayNeedUncheckable
		}
	}
	if !resolved {
		return open, nil
	}
	release, err := a.lockPeoplePublication(ctx, stamp)
	if err != nil {
		return open, errTodayNeedUncheckable
	}
	defer release()
	return TodayNeedResolution{Kind: "resolved"}, nil
}

func (a *App) todayHandoffNeedCheck(id string) (TodayNeedResolution, error) {
	if a.handoffStore == nil {
		return TodayNeedResolution{}, errTodayNeedUncheckable
	}
	entry, err := a.handoffStore.Get(id)
	if errors.Is(err, fs.ErrNotExist) {
		return TodayNeedResolution{Kind: "resolved"}, nil
	}
	if err != nil {
		return TodayNeedResolution{}, errTodayNeedUncheckable
	}
	switch entry.Status {
	case handoff.StatusActive, handoff.StatusInProgress, handoff.StatusBlocked, handoff.StatusNeedsReview:
		return TodayNeedResolution{}, nil
	case handoff.StatusCompleted, handoff.StatusAborted, handoff.StatusStale:
		return TodayNeedResolution{Kind: "resolved"}, nil
	default:
		return TodayNeedResolution{}, errTodayNeedUncheckable
	}
}
