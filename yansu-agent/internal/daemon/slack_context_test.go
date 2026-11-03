package daemon

import (
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// buildPromptWithContext tests
// ---------------------------------------------------------------------------

func TestBuildPromptWithContext_NoContext(t *testing.T) {
	result := buildPromptWithContext(nil, "hello")
	if result != "hello" {
		t.Errorf("got %q, want %q", result, "hello")
	}
}

func TestBuildPromptWithContext_WithContext(t *testing.T) {
	msgs := []ContextMessage{
		{UserID: "U1", Text: "first", Timestamp: time.Date(2026, 2, 26, 10, 0, 0, 0, time.UTC)},
		{UserID: "U2", Text: "second", Timestamp: time.Date(2026, 2, 26, 10, 1, 0, 0, time.UTC)},
	}
	result := buildPromptWithContext(msgs, "my request")

	if !strings.Contains(result, "[Conversation context") {
		t.Error("missing context header")
	}
	if !strings.Contains(result, "U1 (2026-02-26 10:00:00): first") {
		t.Error("missing first context message")
	}
	if !strings.Contains(result, "U2 (2026-02-26 10:01:00): second") {
		t.Error("missing second context message")
	}
	if !strings.Contains(result, "[User request]") {
		t.Error("missing user request header")
	}
	if !strings.HasSuffix(result, "my request") {
		t.Error("should end with user text")
	}
}

func TestBuildPromptWithContext_OversizedContext(t *testing.T) {
	// Context that exceeds maxPromptChars → oldest dropped, user text intact.
	longText := strings.Repeat("x", 1000)
	msgs := make([]ContextMessage, 200)
	for i := range msgs {
		msgs[i] = ContextMessage{
			UserID:    "U1",
			Text:      longText,
			Timestamp: time.Now(),
		}
	}
	userText := "my request"
	result := buildPromptWithContext(msgs, userText)

	if len(result) > maxPromptChars {
		t.Errorf("result len %d exceeds maxPromptChars %d", len(result), maxPromptChars)
	}
	if !strings.HasSuffix(result, userText) {
		t.Error("user text should always be preserved")
	}
}

func TestBuildPromptWithContext_UserTextExceedsLimit(t *testing.T) {
	// User text alone > maxPromptChars → context dropped, user text as-is.
	bigUserText := strings.Repeat("y", maxPromptChars+1000)
	msgs := []ContextMessage{
		{UserID: "U1", Text: "context", Timestamp: time.Now()},
	}
	result := buildPromptWithContext(msgs, bigUserText)
	if result != bigUserText {
		t.Error("when user text exceeds limit, should return user text as-is")
	}
}
