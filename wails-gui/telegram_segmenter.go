package main

import (
	"fmt"
	"sort"
	"strings"
)

// TelegramRawMessage is a message received from the Telegram daemon.
// It is appended to the local JSONL store as messages arrive in real time.
type TelegramRawMessage struct {
	ChatID   string `json:"chat_id"`
	ChatType string `json:"chat_type"` // "private" | "group" | "supergroup"
	SenderID string `json:"sender_id"`
	Text     string `json:"text"`
	EventTS  string `json:"event_ts"` // Unix seconds, optionally with fractional part
}

// TelegramSlimConversation is a grouped Telegram conversation ready for the memory pipeline.
type TelegramSlimConversation struct {
	ChatID    string               `json:"chat_id"`
	ChatType  string               `json:"chat_type"`
	ThreadID  string               `json:"thread_id"` // EventTS of the first message in the window
	Messages  []TelegramRawMessage `json:"messages"`
	StartTime int64                `json:"start_time_unix"`
	EndTime   int64                `json:"end_time_unix"`
}

// parseTelegramEventTS parses an EventTS string (e.g. "1734567890.123456" or "1734567890")
// into Unix seconds. Returns 0 on failure.
func parseTelegramEventTS(s string) int64 {
	if s == "" {
		return 0
	}
	if dotIdx := strings.Index(s, "."); dotIdx >= 0 {
		s = s[:dotIdx]
	}
	var sec int64
	if _, err := fmt.Sscanf(s, "%d", &sec); err != nil {
		return 0
	}
	return sec
}

// segmentTelegramConversations groups raw Telegram messages into 30-minute time-window
// conversations. Telegram has no thread model, so time-window grouping is the only strategy.
// Messages are first partitioned by ChatID so chats are never interleaved.
func segmentTelegramConversations(msgs []TelegramRawMessage) []TelegramSlimConversation {
	if len(msgs) == 0 {
		return nil
	}

	// Partition by ChatID so messages from different chats are never grouped together.
	byChatID := make(map[string][]TelegramRawMessage)
	var chatOrder []string
	for _, msg := range msgs {
		if _, seen := byChatID[msg.ChatID]; !seen {
			chatOrder = append(chatOrder, msg.ChatID)
		}
		byChatID[msg.ChatID] = append(byChatID[msg.ChatID], msg)
	}

	var result []TelegramSlimConversation
	for _, chatID := range chatOrder {
		result = append(result, segmentSingleChat(byChatID[chatID])...)
	}
	return result
}

// segmentSingleChat applies 30-minute time-window segmentation to messages from a single chat.
func segmentSingleChat(msgs []TelegramRawMessage) []TelegramSlimConversation {
	const windowSecs int64 = 30 * 60

	sort.Slice(msgs, func(i, j int) bool {
		return parseTelegramEventTS(msgs[i].EventTS) < parseTelegramEventTS(msgs[j].EventTS)
	})

	var result []TelegramSlimConversation
	var window []TelegramRawMessage

	for _, msg := range msgs {
		if len(window) == 0 {
			window = append(window, msg)
			continue
		}
		lastTime := parseTelegramEventTS(window[len(window)-1].EventTS)
		curTime := parseTelegramEventTS(msg.EventTS)
		if curTime-lastTime <= windowSecs {
			window = append(window, msg)
		} else {
			result = append(result, buildTelegramConversation(window))
			window = []TelegramRawMessage{msg}
		}
	}
	if len(window) > 0 {
		result = append(result, buildTelegramConversation(window))
	}

	return result
}

func buildTelegramConversation(msgs []TelegramRawMessage) TelegramSlimConversation {
	if len(msgs) == 0 {
		return TelegramSlimConversation{}
	}

	chatID := msgs[0].ChatID
	chatType := msgs[0].ChatType

	var startTime, endTime int64
	for _, m := range msgs {
		t := parseTelegramEventTS(m.EventTS)
		if startTime == 0 || t < startTime {
			startTime = t
		}
		if t > endTime {
			endTime = t
		}
	}

	return TelegramSlimConversation{
		ChatID:    chatID,
		ChatType:  chatType,
		ThreadID:  msgs[0].EventTS,
		Messages:  msgs,
		StartTime: startTime,
		EndTime:   endTime,
	}
}

// buildTelegramSummary creates a human-readable summary for the memory pipeline prompt.
func buildTelegramSummary(conv TelegramSlimConversation, selfUserID, selfLabel string) string {
	var b strings.Builder
	b.WriteString("[Telegram Conversation]\n")
	b.WriteString(fmt.Sprintf("Chat: %s (%s)\n", conv.ChatID, conv.ChatType))
	b.WriteString(formatConnectorBoundUserLine(selfLabel, selfUserID))
	b.WriteString(fmt.Sprintf("Messages: %d\n\n", len(conv.Messages)))
	for _, msg := range conv.Messages {
		name := msg.SenderID
		if name == "" {
			name = "unknown"
		}
		text := msg.Text
		if len(text) > 300 {
			text = text[:300] + "..."
		}
		if text == "" {
			text = "[attachment]"
		}
		b.WriteString(formatConnectorMessageLine(msg.SenderID, name, text, selfUserID))
	}
	return b.String()
}
