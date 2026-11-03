package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// FeishuIMMessageRaw is the raw message from the Feishu history API.
// IMPLEMENTATION NOTE: root_id is assumed present based on the inbound event struct
// (feishu_events_handler.go uses root_id). If it turns out root_id is absent from
// the history API, treat all messages as non-threaded (time-window grouping only).
type FeishuIMMessageRaw struct {
	MessageID  string `json:"message_id"`
	ThreadID   string `json:"thread_id,omitempty"`
	RootID     string `json:"root_id"`
	ParentID   string `json:"parent_id"`
	ChatID     string `json:"chat_id"`
	ChatType   string `json:"chat_type"`
	MsgType    string `json:"msg_type"`
	CreateTime string `json:"create_time"` // Unix ms string
	SenderID   string `json:"sender_id"`   // extracted from sender.id
	SenderType string `json:"sender_type"` // "user" or "app"
	TextBody   string `json:"text_body"`   // extracted from body.content for text msgs
	ChatName   string `json:"chat_name"`   // injected by fetcher for context
}

// FeishuSlimConversation is a grouped Feishu conversation ready for memory pipeline.
type FeishuSlimConversation struct {
	ChatID    string               `json:"chat_id"`
	ChatName  string               `json:"chat_name"`
	ChatType  string               `json:"chat_type"` // "p2p" or "group"
	ThreadID  string               `json:"thread_id"` // root_id if thread, else first message_id
	Messages  []FeishuIMMessageRaw `json:"messages"`
	StartTime int64                `json:"start_time_unix"` // of first message
	EndTime   int64                `json:"end_time_unix"`   // of last message
}

// parseCreateTimeMs parses a Unix-millisecond timestamp string into Unix seconds.
// Returns 0 on parse failure.
func parseCreateTimeMs(s string) int64 {
	ms, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return ms / 1000
}

// segmentFeishuMessagesVersioned reads raw JSONL files from rawDir, groups messages
// into conversations using versioned IDs (for incremental dedup), and writes each
// conversation as <convID>.json into convDir. Returns the list of written filenames.
//
// Versioned conv ID for threads: SHA256(chatID:threadID:latestCreateTime)[:6]
// Versioned conv ID for time-gap clusters: SHA256(chatID:firstMsgCreateTime)[:6]
// This ensures updated threads get new file names while stable clusters keep theirs.
func segmentFeishuMessagesVersioned(rawDir, convDir string) ([]string, error) {
	_ = os.MkdirAll(convDir, 0o700)

	entries, err := os.ReadDir(rawDir)
	if err != nil {
		return nil, fmt.Errorf("read raw dir: %w", err)
	}

	var written []string

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".jsonl" {
			continue
		}

		// Read all messages from this chat's JSONL file.
		f, err := os.Open(filepath.Join(rawDir, entry.Name()))
		if err != nil {
			log.Printf("[feishu-seg] open %s: %v", entry.Name(), err)
			continue
		}
		var msgs []FeishuIMMessageRaw
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
		for sc.Scan() {
			var msg FeishuIMMessageRaw
			if err := json.Unmarshal(sc.Bytes(), &msg); err == nil && strings.TrimSpace(msg.TextBody) != "" {
				msgs = append(msgs, msg)
			}
		}
		_ = f.Close()
		if len(msgs) == 0 {
			continue
		}

		chatID := msgs[0].ChatID

		// Pass 1: group by root_id (threads).
		threadGroups := make(map[string][]FeishuIMMessageRaw)
		threadOrder := []string{}
		var remaining []FeishuIMMessageRaw
		for _, msg := range msgs {
			if msg.RootID != "" {
				if _, seen := threadGroups[msg.RootID]; !seen {
					threadOrder = append(threadOrder, msg.RootID)
				}
				threadGroups[msg.RootID] = append(threadGroups[msg.RootID], msg)
			} else {
				remaining = append(remaining, msg)
			}
		}

		// Pass 1.5: pull thread root messages from remaining into their thread groups.
		// The root message has root_id="" but its message_id matches a threadGroups key.
		var filteredRemaining []FeishuIMMessageRaw
		for _, msg := range remaining {
			if _, isRoot := threadGroups[msg.MessageID]; isRoot {
				// Prepend root message to its thread group.
				threadGroups[msg.MessageID] = append([]FeishuIMMessageRaw{msg}, threadGroups[msg.MessageID]...)
			} else {
				filteredRemaining = append(filteredRemaining, msg)
			}
		}
		remaining = filteredRemaining

		// Write thread conversations with versioned IDs.
		for _, rootID := range threadOrder {
			threadMsgs := threadGroups[rootID]
			conv := buildConversation(threadMsgs, rootID)

			// Versioned: include max create_time across all thread messages.
			var latestCreateTime string
			for _, m := range threadMsgs {
				if m.CreateTime > latestCreateTime {
					latestCreateTime = m.CreateTime
				}
			}
			versionedKey := fmt.Sprintf("%s:%s:%s", chatID, rootID, latestCreateTime)
			h := sha256.Sum256([]byte(versionedKey))
			convID := fmt.Sprintf("conv_%x", h[:6])

			data, err := json.MarshalIndent(conv, "", "  ")
			if err != nil {
				log.Printf("[feishu-seg] marshal thread %s: %v", convID, err)
				continue
			}
			outPath := filepath.Join(convDir, convID+".json")
			if err := os.WriteFile(outPath, data, 0o600); err != nil {
				log.Printf("[feishu-seg] write %s: %v", outPath, err)
				continue
			}
			written = append(written, convID+".json")
		}

		// Pass 2: time-window grouping for non-threaded messages.
		const windowSecs int64 = 30 * 60
		sort.Slice(remaining, func(i, j int) bool {
			return parseCreateTimeMs(remaining[i].CreateTime) < parseCreateTimeMs(remaining[j].CreateTime)
		})

		var window []FeishuIMMessageRaw
		flushWindow := func() {
			if len(window) == 0 {
				return
			}
			conv := buildConversation(window, "")
			// Stable key: chatID + firstMsg create_time.
			stableKey := fmt.Sprintf("%s:%s", chatID, window[0].CreateTime)
			h := sha256.Sum256([]byte(stableKey))
			convID := fmt.Sprintf("conv_%x", h[:6])

			data, err := json.MarshalIndent(conv, "", "  ")
			if err != nil {
				log.Printf("[feishu-seg] marshal cluster %s: %v", convID, err)
				return
			}
			outPath := filepath.Join(convDir, convID+".json")
			if err := os.WriteFile(outPath, data, 0o600); err != nil {
				log.Printf("[feishu-seg] write %s: %v", outPath, err)
				return
			}
			written = append(written, convID+".json")
			window = nil
		}

		for _, msg := range remaining {
			if len(window) == 0 {
				window = append(window, msg)
				continue
			}
			lastTime := parseCreateTimeMs(window[len(window)-1].CreateTime)
			curTime := parseCreateTimeMs(msg.CreateTime)
			if curTime-lastTime <= windowSecs {
				window = append(window, msg)
			} else {
				flushWindow()
				window = append(window, msg)
			}
		}
		flushWindow()
	}

	log.Printf("[feishu-seg] segmented %d conversations (versioned)", len(written))
	return written, nil
}

// buildConversation constructs a FeishuSlimConversation from a slice of messages.
// threadID is set from rootID if non-empty; otherwise falls back to first message_id.
func buildConversation(msgs []FeishuIMMessageRaw, threadID string) FeishuSlimConversation {
	if len(msgs) == 0 {
		return FeishuSlimConversation{}
	}

	chatID := msgs[0].ChatID
	chatName := msgs[0].ChatName
	chatType := msgs[0].ChatType

	var startTime, endTime int64
	for _, m := range msgs {
		t := parseCreateTimeMs(m.CreateTime)
		if startTime == 0 || t < startTime {
			startTime = t
		}
		if t > endTime {
			endTime = t
		}
	}

	if threadID == "" && len(msgs) > 0 {
		threadID = msgs[0].MessageID
	}

	return FeishuSlimConversation{
		ChatID:    chatID,
		ChatName:  chatName,
		ChatType:  chatType,
		ThreadID:  threadID,
		Messages:  msgs,
		StartTime: startTime,
		EndTime:   endTime,
	}
}

// extractTextFromContent parses Feishu's body.content JSON for msg_type=text.
// Returns plain text. Returns "" for non-text types (image, file, etc.).
func extractTextFromContent(msgType, contentJSON string) string {
	switch msgType {
	case "text":
		var obj struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(contentJSON), &obj); err != nil {
			return ""
		}
		return obj.Text
	case "post":
		return extractTextFromPost(contentJSON)
	default:
		return ""
	}
}

// extractTextFromPost extracts plain text from a Feishu rich-text (post) message.
// The content JSON can be either locale-wrapped ({"zh_cn": {"title":..., "content":...}})
// or flat ({"title":..., "content":...}).
func extractTextFromPost(contentJSON string) string {
	type postBody struct {
		Title   string              `json:"title"`
		Content [][]json.RawMessage `json:"content"`
	}

	// Try flat format first: {"title":..., "content":[[...]]}
	var flat postBody
	if err := json.Unmarshal([]byte(contentJSON), &flat); err == nil && flat.Content != nil {
		return buildPostText(flat.Title, flat.Content)
	}

	// Try locale-wrapped format: {"zh_cn": {"title":..., "content":[[...]]}}
	// Use a fixed preference order to ensure deterministic extraction across runs.
	var localeWrapped map[string]json.RawMessage
	if err := json.Unmarshal([]byte(contentJSON), &localeWrapped); err != nil {
		return ""
	}
	preferredLocales := []string{"zh_cn", "en_us", "ja_jp"}
	for _, loc := range preferredLocales {
		raw, ok := localeWrapped[loc]
		if !ok {
			continue
		}
		var body postBody
		if err := json.Unmarshal(raw, &body); err == nil && body.Content != nil {
			return buildPostText(body.Title, body.Content)
		}
	}
	// Fallback: pick the lexicographically first key for any other locale.
	keys := make([]string, 0, len(localeWrapped))
	for k := range localeWrapped {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		var body postBody
		if err := json.Unmarshal(localeWrapped[k], &body); err == nil && body.Content != nil {
			return buildPostText(body.Title, body.Content)
		}
	}
	return ""
}

func buildPostText(title string, content [][]json.RawMessage) string {
	var b strings.Builder
	if title != "" {
		b.WriteString(title)
		b.WriteString("\n")
	}
	for i, paragraph := range content {
		if i > 0 {
			b.WriteString("\n")
		}
		for _, elem := range paragraph {
			var tag struct {
				Tag      string `json:"tag"`
				Text     string `json:"text"`
				UserName string `json:"user_name"`
			}
			if err := json.Unmarshal(elem, &tag); err != nil {
				continue
			}
			switch tag.Tag {
			case "text", "a":
				b.WriteString(tag.Text)
			case "at":
				if tag.UserName != "" {
					b.WriteString("@" + tag.UserName)
				}
			}
		}
	}
	return b.String()
}

// buildFeishuSummary creates a human-readable summary of a FeishuSlimConversation
// for the memory pipeline prompt.
func buildFeishuSummary(conv FeishuSlimConversation, selfUserID, selfLabel string) string {
	var b strings.Builder
	b.WriteString("[Feishu Conversation]\n")
	b.WriteString(fmt.Sprintf("Chat: %s (%s)\n", conv.ChatName, conv.ChatType))
	b.WriteString(formatConnectorBoundUserLine(selfLabel, selfUserID))
	b.WriteString(fmt.Sprintf("Messages: %d\n\n", len(conv.Messages)))
	for _, msg := range conv.Messages {
		name := msg.SenderID
		if name == "" {
			name = "unknown"
		}
		text := msg.TextBody
		if len(text) > 300 {
			text = text[:300] + "..."
		}
		if text == "" {
			text = fmt.Sprintf("[%s]", msg.MsgType)
		}
		b.WriteString(formatConnectorMessageLine(msg.SenderID, name, text, selfUserID))
	}
	return b.String()
}
