package main

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"wails-gui/storage"
)

// conversationMaxHistoryItems is the rolling-window size for per-thread
// conversation history. Matches the frontend's slice(-20).
const conversationMaxHistoryItems = 20

// visualAnchorPolicy is prepended to a follow-up prompt when the rolling
// history shows the user previously sent an image that the agent already
// identified. It keeps the agent from re-evaluating the image from scratch
// on each turn — LLM vision is not reproducible and re-reading the same
// blurred signboard can flip the answer, which feels like "forgetting".
const visualAnchorPolicy = `<visual-anchor>
If the conversation history already contains your identification of image
content (e.g. a bridge name, place, object, landmark) and the user has not
disputed it, TRUST your prior identification. Do not re-evaluate the image
from scratch or introduce new uncertainty. Continue the conversation
assuming your prior identification is correct unless the user explicitly
contradicts it.
</visual-anchor>`

// imagePlaceholderRE matches the exact marker emitted by buildWeChatHistoryUserMsg
// for image turns: "[user sent N image(s)]". A regex (instead of two substring
// checks) avoids false positives when the user's own typed text happens to
// contain both "[user sent " and "image(s)]" as separate substrings.
var imagePlaceholderRE = regexp.MustCompile(`\[user sent \d+ image\(s\)\]`)

// conversationItem is a single turn stored in the per-thread history.
type conversationItem struct {
	Kind    string `json:"kind"`    // "user" or "assistant"
	Content string `json:"content"` // message text
}

// lastUserTurnHasImagePlaceholder reports whether the MOST RECENT user turn
// in items carries the image placeholder marker. Anchoring is only meaningful
// when the immediately preceding user turn was the image — image turns long
// past in the rolling window are not relevant follow-ups and should not keep
// re-injecting the anchor on every later turn.
func lastUserTurnHasImagePlaceholder(items []conversationItem) bool {
	for i := len(items) - 1; i >= 0; i-- {
		if items[i].Kind != "user" {
			continue
		}
		return imagePlaceholderRE.MatchString(items[i].Content)
	}
	return false
}

// shouldAnchorVisualContext reports whether to prepend visualAnchorPolicy
// to this turn's prompt. It anchors only on a real text follow-up directly
// after an image:
//   - newImageAttempted=false: if the user tried to send a new image this
//     turn (even one that failed to decode or was too large) we want the
//     agent to address the new attempt / its failure note, not answer
//     against the prior image.
//   - userText non-empty: voice/file-only turns or empty payloads are not
//     visual follow-ups; injecting the anchor on them would be off-topic.
//   - The MOST RECENT user turn in history must be the image, not some
//     image many turns ago that the conversation has long moved past.
func shouldAnchorVisualContext(items []conversationItem, newImageAttempted bool, userText string) bool {
	if newImageAttempted {
		return false
	}
	if strings.TrimSpace(userText) == "" {
		return false
	}
	return lastUserTurnHasImagePlaceholder(items)
}

// loadConversationHistory reads the raw items for threadKey from the store.
func loadConversationHistory(store *storage.Store, threadKey string) []conversationItem {
	if store == nil {
		return nil
	}
	raw, err := store.GetUIState(context.Background(), "daemon:history:"+threadKey)
	if err != nil || raw == "" {
		return nil
	}
	var items []conversationItem
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return nil
	}
	return items
}

// renderConversationItems renders items for TaskInput.History injection.
// Returns empty string when items is empty. Named to avoid collision with
// formatConversationHistory in streaming.go, which serialises a different
// row type (storage.StreamItemRow) for the main-chat path.
func renderConversationItems(items []conversationItem) string {
	if len(items) == 0 {
		return ""
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		parts = append(parts, fmt.Sprintf("[%s]: %s", item.Kind, item.Content))
	}
	return strings.Join(parts, "\n\n")
}

// appendAndSummarizeConversation appends a completed exchange to the per-thread
// history (stored under "daemon:history:<threadKey>"), trims to the rolling
// window, persists the trimmed slice, and returns the formatted summary plus
// the running round count (derived from the full history before trimming,
// mirroring the frontend conversation slice).
func appendAndSummarizeConversation(store *storage.Store, threadKey, userMsg, assistantMsg string) (summary string, roundCount int) {
	items := loadConversationHistory(store, threadKey)
	items = append(items, conversationItem{Kind: "user", Content: userMsg})
	items = append(items, conversationItem{Kind: "assistant", Content: assistantMsg})

	users, assistants := 0, 0
	for _, item := range items {
		switch item.Kind {
		case "user":
			users++
		case "assistant":
			assistants++
		}
	}
	roundCount = min(users, assistants)

	if len(items) > conversationMaxHistoryItems {
		items = items[len(items)-conversationMaxHistoryItems:]
	}

	if store != nil {
		if data, err := json.Marshal(items); err == nil {
			_ = store.SetUIState(context.Background(), "daemon:history:"+threadKey, string(data))
		}
	}

	return renderConversationItems(items), roundCount
}
