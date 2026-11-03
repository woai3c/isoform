package main

import (
	"encoding/json"
	"strings"
	"testing"

	"wails-gui/internal/platform/activitymon/appparser"
)

func TestRedactParsedContentPIIRedactsPersistedFields(t *testing.T) {
	parsed := &appparser.ParsedContent{
		ContentType: "conversation",
		Title:       "Incident for support@example.com",
		Body:        "session_id=abc123def456ghi789 password=TopSecret2026",
		URL:         "https://files.example.com/report.pdf?signature=abcdef1234567890",
		Channel:     "mailto:ops@example.com",
		Messages: []appparser.ConversationMessage{
			{
				Sender:       "analyst@example.com",
				Content:      "api_key=abcdefghijklmnopqrstuvwxyz123456",
				ThreadParent: "parent@example.com",
			},
		},
		Metadata: map[string]string{
			"app":       "slack",
			"recipient": "phone: +1 (415) 555-0123",
			"session":   "refresh_token=rt-fixture-example",
		},
	}

	count := redactParsedContentPII(parsed)
	if count < 8 {
		t.Fatalf("redactParsedContentPII count = %d, want at least 8", count)
	}

	data, err := json.Marshal(parsed)
	if err != nil {
		t.Fatal(err)
	}
	persisted := string(data)
	for _, raw := range []string{
		"support@example.com",
		"abc123def456ghi789",
		"TopSecret2026",
		"https://files.example.com/report.pdf?signature=abcdef1234567890",
		"ops@example.com",
		"analyst@example.com",
		"abcdefghijklmnopqrstuvwxyz123456",
		"parent@example.com",
		"+1 (415) 555-0123",
		"rt-fixture-example",
	} {
		if strings.Contains(persisted, raw) {
			t.Fatalf("persisted parsed JSON still contains %q: %s", raw, persisted)
		}
	}
	if !strings.Contains(persisted, "[REDACTED:") {
		t.Fatalf("persisted parsed JSON does not contain redaction tags: %s", persisted)
	}
	if parsed.Metadata["app"] != "slack" {
		t.Fatalf("non-sensitive parser metadata changed: %q", parsed.Metadata["app"])
	}
}

func TestContentHashForDedupCanUseRedactedParsedContent(t *testing.T) {
	first := &appparser.ParsedContent{
		ContentType: "conversation",
		Title:       "Shared incident thread",
		Messages: []appparser.ConversationMessage{
			{Sender: "ops@example.com", Content: "password=AlphaSecret2026 session_id=abc123def456ghi789"},
		},
	}
	second := &appparser.ParsedContent{
		ContentType: "conversation",
		Title:       "Shared incident thread",
		Messages: []appparser.ConversationMessage{
			{Sender: "dev@example.com", Content: "password=BetaSecret2026 session_id=zyx987wvu654tsr321"},
		},
	}

	redactParsedContentPII(first)
	redactParsedContentPII(second)

	if got, want := contentHashForDedup(first), contentHashForDedup(second); got != want {
		t.Fatalf("redacted content hashes differ: got %q want %q", got, want)
	}
}
