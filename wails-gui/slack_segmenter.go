package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const timeGapSeconds = 30 * 60 // 30 minutes

// Conversation represents a segmented conversation from Slack history.
type Conversation struct {
	ID           string             `json:"id"`
	ConvKey      string             `json:"-"`    // stable key computed during segmentation; avoids re-reading Messages later
	Type         string             `json:"type"` // "thread" or "time_gap"
	ChannelID    string             `json:"channel_id"`
	ThreadTS     string             `json:"thread_ts,omitempty"`
	Topic        string             `json:"topic"`
	Participants []string           `json:"participants"`
	MessageCount int                `json:"message_count"`
	Messages     []SlackScanMessage `json:"messages"`
}

// SlimMessage is a disk-serialization-only struct containing just the fields the LLM needs.
type SlimMessage struct {
	UserID   string `json:"user_id,omitempty"`
	UserName string `json:"user_name,omitempty"`
	Text     string `json:"text"`
}

// SlimConversation is a disk-serialization-only struct with slim messages to reduce storage.
type SlimConversation struct {
	ID           string        `json:"id"`
	Type         string        `json:"type"`
	ChannelID    string        `json:"channel_id"`
	Topic        string        `json:"topic"`
	Participants []string      `json:"participants"`
	MessageCount int           `json:"message_count"`
	Messages     []SlimMessage `json:"messages"`
}

// noiseSubtypesLocal are Slack message subtypes excluded from segmentation.
var noiseSubtypesLocal = map[string]bool{
	"channel_join":    true,
	"channel_leave":   true,
	"channel_topic":   true,
	"channel_purpose": true,
}

// segmentThreads creates one Conversation per thread with replies.
func segmentThreads(scanDir, channelID string) []Conversation {
	threadDir := filepath.Join(scanDir, "threads", channelID)
	entries, err := os.ReadDir(threadDir)
	if err != nil {
		return nil
	}

	var convs []Conversation
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		threadTS := strings.TrimSuffix(entry.Name(), ".jsonl")
		msgs := readJSONL(filepath.Join(threadDir, entry.Name()))
		if len(msgs) == 0 {
			continue
		}

		// Filter noise subtypes.
		var filtered []SlackScanMessage
		for _, msg := range msgs {
			if !noiseSubtypesLocal[msg.SubType] {
				filtered = append(filtered, msg)
			}
		}
		if len(filtered) == 0 {
			continue
		}

		conv := Conversation{
			Type:         "thread",
			ChannelID:    channelID,
			ThreadTS:     threadTS,
			Topic:        generateTopic(filtered),
			Participants: uniqueParticipants(filtered),
			MessageCount: len(filtered),
			Messages:     filtered,
		}
		convs = append(convs, conv)
	}
	return convs
}

// segmentByTimeGap clusters non-threaded messages by 30-minute gaps.
func segmentByTimeGap(channelID string, msgs []SlackScanMessage) []Conversation {
	if len(msgs) == 0 {
		return nil
	}

	// Sort by timestamp.
	sort.Slice(msgs, func(i, j int) bool {
		return msgs[i].TS < msgs[j].TS
	})

	var convs []Conversation
	var current []SlackScanMessage
	var lastTS float64

	for _, msg := range msgs {
		ts := parseSlackTS(msg.TS)
		if len(current) > 0 && (ts-lastTS) > float64(timeGapSeconds) {
			// Gap detected — close current cluster.
			conv := Conversation{
				Type:         "time_gap",
				ChannelID:    channelID,
				Topic:        generateTopic(current),
				Participants: uniqueParticipants(current),
				MessageCount: len(current),
				Messages:     current,
			}
			convs = append(convs, conv)
			current = nil
		}
		current = append(current, msg)
		lastTS = ts
	}

	// Close final cluster.
	if len(current) > 0 {
		conv := Conversation{
			Type:         "time_gap",
			ChannelID:    channelID,
			Topic:        generateTopic(current),
			Participants: uniqueParticipants(current),
			MessageCount: len(current),
			Messages:     current,
		}
		convs = append(convs, conv)
	}

	return convs
}

// ── Helpers ──────────────────────────────────────────────────────────

func readJSONL(path string) []SlackScanMessage {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var msgs []SlackScanMessage
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 256*1024), 256*1024)
	for scanner.Scan() {
		var msg SlackScanMessage
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}
		msgs = append(msgs, msg)
	}
	return msgs
}

func parseSlackTS(ts string) float64 {
	f, err := strconv.ParseFloat(ts, 64)
	if err != nil {
		return 0
	}
	return f
}

// generateTopic creates a topic summary from the first 3 messages.
func generateTopic(msgs []SlackScanMessage) string {
	var parts []string
	for i, msg := range msgs {
		if i >= 3 {
			break
		}
		text := cleanTopicText(msg.Text)
		words := strings.Fields(text)
		if len(words) > 8 {
			words = words[:8]
		}
		if len(words) > 0 {
			parts = append(parts, strings.Join(words, " "))
		}
	}
	if len(parts) == 0 {
		return "(no topic)"
	}
	return strings.Join(parts, " / ")
}

// cleanTopicText removes mentions and URLs from text.
func cleanTopicText(text string) string {
	// Remove Slack user mentions: <@U12345>
	result := text
	for {
		start := strings.Index(result, "<@")
		if start < 0 {
			break
		}
		end := strings.Index(result[start:], ">")
		if end < 0 {
			break
		}
		result = result[:start] + result[start+end+1:]
	}
	// Remove URLs: <http...>
	for {
		start := strings.Index(result, "<http")
		if start < 0 {
			break
		}
		end := strings.Index(result[start:], ">")
		if end < 0 {
			break
		}
		result = result[:start] + result[start+end+1:]
	}
	return strings.TrimSpace(result)
}

func uniqueParticipants(msgs []SlackScanMessage) []string {
	seen := make(map[string]bool)
	var result []string
	for _, msg := range msgs {
		if msg.User != "" && !seen[msg.User] {
			seen[msg.User] = true
			// Prefer user_name if available.
			name := msg.UserName
			if name == "" {
				name = msg.User
			}
			result = append(result, name)
		}
	}
	return result
}
