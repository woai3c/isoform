package main

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"wails-gui/storage"
)

// Chat that waited while the computer was away (phone-connection v1.1.0
// section 2). The phone queues a message while it cannot reach the computer
// and sends it with queuedAt once it can. The answer then reaches the phone as
// a chat notification; answers to several waited messages share one.

const (
	mobileChatUndoneMax = 100
	// mobileChatQueuedSkew is how far ahead of this clock a phone's queue time may be.
	mobileChatQueuedSkew = 5 * time.Minute
	// mobileChatAnswerBatchWindow ends a batch the phone never read.
	mobileChatAnswerBatchWindow = 12 * time.Hour
	mobileChatAnswerNoticeKind  = "chat_answer"
	mobileChatAnswerPreviewMax  = 160
)

// mobileChatAnswerBatch is the answers to waited messages one notification counts.
type mobileChatAnswerBatch struct {
	collapse  string
	count     int
	firstID   string
	firstText string
	at        time.Time
}

// mobileChatAnswerPublishOverride replaces publishing in tests.
var mobileChatAnswerPublishOverride func(mobileNotice)

// mobileChatQueuedAt parses the optional queue time. A time far in the future is not valid.
func mobileChatQueuedAt(raw string, now time.Time) (time.Time, bool) {
	if raw == "" {
		return time.Time{}, true
	}
	at, err := time.Parse(time.RFC3339, raw)
	if err != nil || at.After(now.Add(mobileChatQueuedSkew)) {
		return time.Time{}, false
	}
	return at, true
}

func mobileChatWaitMinutes(queued, now time.Time) int {
	if queued.IsZero() || !now.After(queued) {
		return 0
	}
	return int(now.Sub(queued) / time.Minute)
}

// recordPhoneChatWait records the content-free phone_chat events for an accepted message.
func (bridge *mobileBridge) recordPhoneChatWait(queued time.Time, undone int) {
	telemetry := bridge.app.telemetry
	if telemetry == nil {
		return
	}
	if undone > 0 {
		telemetry.Capture("phone_chat.undone", map[string]any{"count": undone})
	}
	if queued.IsZero() {
		return
	}
	telemetry.Capture("phone_chat.queued", nil)
	telemetry.Capture("phone_chat.sent_after_wait", map[string]any{"wait_minutes": mobileChatWaitMinutes(queued, time.Now())})
}

// mobileChatMarkAskedAt gives the first answer after each question the time it was asked.
func mobileChatMarkAskedAt(views []mobileChatItemView, keep []bool) {
	asked := ""
	for i := range views {
		if !keep[i] {
			continue
		}
		switch views[i].Role {
		case "user":
			asked = views[i].At
		case "assistant":
			if views[i].Kind == "text" && asked != "" {
				views[i].AskedAt = asked
				asked = ""
			}
		}
	}
}

// clearChatAnswers forgets the batch once the phone has read the chat.
func (chat *mobileChatState) clearChatAnswers(chatID string) {
	chat.mu.Lock()
	delete(chat.answers, chatID)
	chat.mu.Unlock()
}

// noticeWaitedChatAnswer counts the answer to a waited message and publishes
// the chat notification, which replaces the earlier one of the same batch.
func (bridge *mobileBridge) noticeWaitedChatAnswer(chatID string, reply *mobileChatReply) {
	answer, ok := bridge.waitedChatAnswer(chatID, reply)
	if !ok {
		return
	}
	now := time.Now()
	chat := bridge.chat
	chat.mu.Lock()
	batch := chat.answers[chatID]
	if batch == nil || now.Sub(batch.at) > mobileChatAnswerBatchWindow {
		batch = &mobileChatAnswerBatch{collapse: "chat-answers-" + uuid.NewString(), firstID: answer.ID, firstText: answer.Content}
		chat.answers[chatID] = batch
	}
	batch.count++
	batch.at = now
	current := *batch
	chat.mu.Unlock()
	notice := mobileChatAnswerNotice(chatID, current, bridge.app.modelDisplayLanguage(), now)
	if mobileChatAnswerPublishOverride != nil {
		mobileChatAnswerPublishOverride(notice)
		return
	}
	if err := bridge.publishMobileNotice(notice, bridge.devicesWithScope(mobileScopePush)); err != nil {
		log.Printf("[mobile-chat] could not send the answer notification for turn %s: %v", reply.turnID, err)
	}
}

// waitedChatAnswer is the stored answer row of the turn, or the hub's copy of it.
func (bridge *mobileBridge) waitedChatAnswer(chatID string, reply *mobileChatReply) (storage.StreamItemRow, bool) {
	if store := bridge.app.storeForMobile(); store != nil {
		if page, err := store.LoadStreamItemsLatest(bridge.ctx, chatID, mobileChatReplyProbe); err == nil {
			for _, item := range page.Items {
				if item.Kind == "assistant" && item.SortOrder > reply.userOrder && strings.TrimSpace(item.Content) != "" {
					return item, true
				}
			}
		}
	}
	for _, row := range reply.rows {
		if row.Kind == "assistant" && strings.TrimSpace(row.Content) != "" {
			return row, true
		}
	}
	return storage.StreamItemRow{}, false
}

// mobileChatAnswerNotice is the sealed notification: the answer's start for
// one answer, one count line for several. It opens Chat at the first answer.
func mobileChatAnswerNotice(chatID string, batch mobileChatAnswerBatch, language string, now time.Time) mobileNotice {
	body := mobileChatClip(strings.Join(strings.Fields(batch.firstText), " "), mobileChatAnswerPreviewMax)
	if batch.count > 1 {
		switch language {
		case "zh-CN":
			body = fmt.Sprintf("你在电脑离开时发的 %d 条消息，Isoform 都回答了。", batch.count)
		default:
			body = fmt.Sprintf("Isoform answered the %d messages you sent while your computer was away.", batch.count)
		}
	}
	return mobileNotice{
		ID: uuid.NewString(), Kind: mobileChatAnswerNoticeKind, Title: "Isoform", Body: body,
		Category: "YANSU_MOMENT", MomentID: batch.firstID, DestinationKind: "chat", DestinationID: chatID,
		Collapse: batch.collapse, Level: "active", CreatedAt: now,
	}
}
