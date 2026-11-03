package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
)

func TestFormatGatewayToolResultSmallTextUnchanged(t *testing.T) {
	result := mcpgo.NewToolResultText("small result")

	got := FormatGatewayToolResult("echo", result)

	if got != "small result" {
		t.Fatalf("FormatGatewayToolResult() = %q, want small result", got)
	}
}

func TestFormatGatewayToolResultLargeTextTruncated(t *testing.T) {
	large := strings.Repeat("x", defaultGatewayResultCapBytes+100)
	result := mcpgo.NewToolResultText(large)

	got := FormatGatewayToolResult("echo", result)

	if len(got) >= len(large) {
		t.Fatalf("large result was not truncated: got %d bytes, original %d", len(got), len(large))
	}
	if len(got) > defaultGatewayResultCapBytes {
		t.Fatalf("truncated result exceeded cap: got %d bytes, cap %d", len(got), defaultGatewayResultCapBytes)
	}
	if !strings.Contains(got, "[gateway result truncated:") {
		t.Fatalf("truncation metadata missing from %q", got[len(got)-120:])
	}
	if !strings.Contains(got, "original_bytes=") || !strings.Contains(got, "cap_bytes=") {
		t.Fatalf("truncation byte metadata missing from %q", got[len(got)-120:])
	}
}

func TestFormatGatewayToolResultGmailLikeJSONSanitized(t *testing.T) {
	raw := map[string]any{
		"messages": []any{
			map[string]any{
				"id":       "msg-1",
				"threadId": "thread-1",
				"snippet":  "hello from the inbox",
				"payload": map[string]any{
					"headers": []any{
						map[string]any{"name": "From", "value": "sender@example.com"},
						map[string]any{"name": "To", "value": "me@example.com"},
						map[string]any{"name": "Subject", "value": "Quarterly report"},
						map[string]any{"name": "Date", "value": "Tue, 12 May 2026 10:00:00 +0000"},
						map[string]any{"name": "Delivered-To", "value": "me@example.com"},
					},
					"body":  map[string]any{"data": strings.Repeat("a", 1024)},
					"parts": []any{map[string]any{"body": map[string]any{"data": "nested"}}},
				},
				"raw":         strings.Repeat("b", 1024),
				"attachments": []any{map[string]any{"filename": "big.pdf", "data": "payload"}},
			},
		},
	}
	data, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}

	got := FormatGatewayToolResult("GOOGLESUPER_FETCH_EMAILS", mcpgo.NewToolResultText(string(data)))

	if strings.Contains(got, "payload") || strings.Contains(got, "raw") ||
		strings.Contains(got, "parts") || strings.Contains(got, "attachments") {
		t.Fatalf("sanitized Gmail result still contains large fields: %s", got)
	}
	for _, want := range []string{"msg-1", "thread-1", "hello from the inbox", "Quarterly report", "sender@example.com", "me@example.com"} {
		if !strings.Contains(got, want) {
			t.Fatalf("sanitized Gmail result missing %q: %s", want, got)
		}
	}
	if strings.Contains(got, "X-Noise") || strings.Contains(got, "Delivered-To") {
		t.Fatalf("non-allowlisted headers should be removed: %s", got)
	}
}
