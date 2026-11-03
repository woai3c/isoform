package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// DiscordScanMessage mirrors a Discord message from the history API.
type DiscordScanMessage struct {
	ID        string `json:"id"`
	ChannelID string `json:"channel_id"`
	Author    struct {
		ID       string `json:"id"`
		Username string `json:"username"`
		Bot      bool   `json:"bot"`
	} `json:"author"`
	Content   string `json:"content"`
	Timestamp string `json:"timestamp"` // ISO 8601
	ThreadID  string `json:"thread_id,omitempty"`
}

// discordConvFileID computes a stable file ID for a conversation (cold-start).
func discordConvFileID(key string) string {
	h := sha256.Sum256([]byte(key))
	return fmt.Sprintf("conv_%x", h[:8])
}

// discordConvFileIDVersioned computes a versioned file ID (incremental scan).
// Includes the latest message ID so updated threads produce new IDs for memory dedup.
func discordConvFileIDVersioned(key, latestMsgID string) string {
	h := sha256.Sum256([]byte(key + ":" + latestMsgID))
	return fmt.Sprintf("conv_%x", h[:8])
}

// segmentDiscordConversations segments Discord messages into Conversation objects.
// Uses the shared Conversation struct but with Discord-specific message parsing.
// Thread messages are grouped by thread_id; non-threaded messages use 30-min time-gap
// or bot-reply boundaries (a bot reply signals that the next human message is a new topic).
func segmentDiscordConversations(messages []DiscordScanMessage, channelID string) []Conversation {
	if len(messages) == 0 {
		return nil
	}

	// Sort by snowflake ID (chronological order).
	sort.Slice(messages, func(i, j int) bool {
		return messages[i].ID < messages[j].ID
	})

	// Separate threaded vs non-threaded messages.
	// Bot messages in threads are included so the full Q&A context is captured
	// (mirrors Slack which keeps bot/app replies in conversation content).
	// Bot messages in the non-threaded stream act as conversation separators.
	threadMessages := make(map[string][]DiscordScanMessage) // thread_id → messages
	var nonThreaded []DiscordScanMessage

	for _, m := range messages {
		if m.ThreadID != "" {
			threadMessages[m.ThreadID] = append(threadMessages[m.ThreadID], m)
		} else {
			nonThreaded = append(nonThreaded, m)
		}
	}

	var convs []Conversation

	// Thread conversations (bot messages included for full Q&A context).
	for threadID, msgs := range threadMessages {
		key := fmt.Sprintf("thread:%s:%s", channelID, threadID)
		convs = append(convs, discordConversationFromMessages(key, "thread", channelID, threadID, msgs))
	}

	// Time-gap / bot-boundary segmentation for non-threaded messages.
	convs = append(convs, segmentDiscordByTimeGap(nonThreaded, channelID)...)

	return convs
}

func segmentDiscordByTimeGap(messages []DiscordScanMessage, channelID string) []Conversation {
	if len(messages) == 0 {
		return nil
	}

	var convs []Conversation
	var current []DiscordScanMessage // human messages only
	var prevTime time.Time
	hadBotReply := false // a bot replied since the last conversation start

	flushCurrent := func() {
		if len(current) == 0 {
			return
		}
		key := fmt.Sprintf("gap:%s:%s", channelID, current[0].ID)
		convs = append(convs, discordConversationFromMessages(key, "time_gap", channelID, "", current))
		current = nil
		hadBotReply = false
	}

	for _, m := range messages {
		t := parseDiscordTimestamp(m.Timestamp)

		if m.Author.Bot {
			// Bot reply signals that the next human message starts a new conversation.
			// Include the bot message in the current segment so the full Q&A is captured
			// (mirrors Slack which keeps bot/app replies in conversation content).
			if len(current) > 0 {
				current = append(current, m)
				hadBotReply = true
			}
			continue
		}

		// Start a new segment on time gap or after a bot has replied.
		if len(current) > 0 {
			timeGap := !prevTime.IsZero() && t.Sub(prevTime) > 30*time.Minute
			if timeGap || hadBotReply {
				flushCurrent()
			}
		}

		current = append(current, m)
		prevTime = t
	}

	flushCurrent()

	return convs
}

func discordConversationFromMessages(key, convType, channelID, threadID string, msgs []DiscordScanMessage) Conversation {
	participants := make(map[string]bool)
	var slackMsgs []SlackScanMessage // reuse existing Conversation.Messages type

	for _, m := range msgs {
		participants[m.Author.Username] = true
		// Convert Discord message to the shared SlackScanMessage format.
		slackMsgs = append(slackMsgs, SlackScanMessage{
			TS:       m.ID,
			User:     m.Author.ID,
			UserName: m.Author.Username,
			Text:     m.Content,
			ThreadTS: threadID,
		})
	}

	var parts []string
	for p := range participants {
		parts = append(parts, p)
	}
	sort.Strings(parts)

	// Build topic from first few messages.
	topic := discordBuildTopic(msgs)

	return Conversation{
		ID:           discordConvFileID(key),
		ConvKey:      key,
		Type:         convType,
		ChannelID:    channelID,
		ThreadTS:     threadID,
		Topic:        topic,
		Participants: parts,
		MessageCount: len(msgs),
		Messages:     slackMsgs,
	}
}

func discordBuildTopic(msgs []DiscordScanMessage) string {
	var parts []string
	for i, m := range msgs {
		if i >= 3 {
			break
		}
		text := m.Content
		if len(text) > 100 {
			text = text[:97] + "..."
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, " | ")
}

// discordSnowflakeFromTime returns the minimum Discord snowflake ID for messages
// at or after the given time. This mirrors Slack's "oldest" timestamp parameter.
// Discord snowflake: bits 63-22 = ms since Discord epoch (2015-01-01 UTC).
func discordSnowflakeFromTime(t time.Time) string {
	const discordEpoch int64 = 1420070400000 // ms
	ms := t.UnixMilli()
	snowflake := (ms - discordEpoch) << 22
	return strconv.FormatInt(snowflake, 10)
}

func parseDiscordTimestamp(ts string) time.Time {
	// Discord sends microseconds: "2023-01-01T12:00:00.123456+00:00" — try Nano first.
	if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, ts); err == nil {
		return t
	}
	// Fallback: treat as snowflake integer.
	if n, err := strconv.ParseInt(ts, 10, 64); err == nil {
		return time.UnixMilli((n >> 22) + 1420070400000)
	}
	return time.Time{}
}

// writeDiscordConversations writes conversation files to the conversations directory.
func writeDiscordConversations(convs []Conversation, conversationsDir string) error {
	if err := os.MkdirAll(conversationsDir, 0o755); err != nil {
		return err
	}

	for _, conv := range convs {
		// Convert messages to SlimMessage for disk serialization.
		slim := make([]SlimMessage, 0, len(conv.Messages))
		for _, m := range conv.Messages {
			slim = append(slim, SlimMessage{
				UserID:   m.User,
				UserName: m.UserName,
				Text:     m.Text,
			})
		}
		conv.Messages = nil // clear full messages before writing

		data, err := json.MarshalIndent(struct {
			Conversation
			Messages []SlimMessage `json:"messages"`
		}{conv, slim}, "", "  ")
		if err != nil {
			return err
		}

		filename := conv.ID + ".json"
		if err := os.WriteFile(filepath.Join(conversationsDir, filename), data, 0o644); err != nil {
			return err
		}
	}

	return nil
}
