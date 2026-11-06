package main

import (
	"context"
	"os"
	"time"
)

// A saved first page may hold proactive cards. Their words come from calendar,
// reminders, meeting notes and receipts, and a withdrawal of one of those
// sources changes neither the privacy stamp nor the stream item version. So a
// copy that holds cards is served only after each card's raw row has been run
// through the same projection the live read uses (projectStreamItemsContext and
// the page view, projectMainChatView), on the card rows alone. The gates read
// the same state the live path reads, so they cannot drift from it.
//
// All the card checks share one deadline. When they do not all finish in time,
// or any card cannot be confirmed, the read recomputes and serves the live page,
// never the copy's cards.
const streamPageCardBudget = 100 * time.Millisecond

// streamPageCardCount counts the proactive cards at the top level of a page.
func streamPageCardCount(page StreamItemPageData) int {
	n := 0
	for _, item := range page.Items {
		if item.ToolName == mainChatProactiveTool {
			n++
		}
	}
	return n
}

// rawProactiveCards copies the proactive cards of items as stored. It must run
// before projectStreamItems, which rewrites the items in place. The copy holds
// the values as the row stores them, so a read can project them again.
func rawProactiveCards(items []StreamItemData) []StreamItemData {
	var out []StreamItemData
	for _, item := range items {
		if item.ToolName == mainChatProactiveTool {
			out = append(out, item)
		}
	}
	return out
}

// confirmProactiveCards projects the copy's cards through the live gates under
// one shared deadline and returns the page with its cards replaced. It returns
// the time the check took and a reason, which is empty only when every card is
// confirmed. It never reads the stream store beyond the handful of lookups the
// live gates make for these rows, and it never touches the rest of the page.
func (a *App) confirmProactiveCards(key streamPageKey, saved streamPageSnapshot, now time.Time, budget time.Duration) (StreamItemPageData, int64, string) {
	start := time.Now()
	elapsed := func() int64 { return time.Since(start).Milliseconds() }
	stored := make(map[string]StreamItemData, len(saved.Cards))
	for _, card := range saved.Cards {
		stored[card.ID] = card
	}
	var positions []int
	var clones []StreamItemData
	for i, item := range saved.Page.Items {
		if item.ToolName != mainChatProactiveTool {
			continue
		}
		card, ok := stored[item.ID]
		if !ok {
			return StreamItemPageData{}, elapsed(), "card-unstored"
		}
		positions = append(positions, i)
		clones = append(clones, card)
	}
	if len(clones) == 0 {
		return saved.Page, elapsed(), ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	done := make(chan struct{})
	// On a timeout this goroutine finishes alone: it works on its own clones and
	// releases its publication fence when it ends.
	go func() {
		defer close(done)
		a.projectStreamItemsContext(ctx, clones)
		release, err := a.projectMainChatView(ctx, key.session, clones, now)
		if err == nil {
			release()
		}
	}()
	select {
	case <-done:
	case <-ctx.Done():
		return StreamItemPageData{}, elapsed(), "card-check-timeout"
	}
	if ctx.Err() != nil {
		return StreamItemPageData{}, elapsed(), "card-check-timeout"
	}
	page := saved.Page
	page.Items = append([]StreamItemData(nil), saved.Page.Items...)
	for n, i := range positions {
		// The live gates must reproduce the card as the copy saved it. A card they
		// change (a withdrawal, a removed source) is not confirmed.
		if clones[n].ID != page.Items[i].ID || clones[n].Content != page.Items[i].Content || clones[n].ToolInput != page.Items[i].ToolInput {
			return StreamItemPageData{}, elapsed(), "card-changed"
		}
		page.Items[i] = clones[n]
	}
	return page, elapsed(), ""
}

// removeStreamPageCopy deletes a key's saved copy at once. A copy that holds a
// card the live gates did not confirm must not wait for the next save: its raw
// card row would otherwise stay on disk after the card was withdrawn.
func removeStreamPageCopy(key streamPageKey) {
	if path, err := streamPageFile(key); err == nil {
		_ = os.Remove(path)
	}
}
