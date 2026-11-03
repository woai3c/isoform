package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
)

const defaultGatewayResultCapBytes = 64 * 1024

var gmailHeaderAllowlist = map[string]bool{
	"from":    true,
	"to":      true,
	"subject": true,
	"date":    true,
}

var gmailDropFields = map[string]bool{
	"payload":     true,
	"body":        true,
	"raw":         true,
	"parts":       true,
	"attachments": true,
}

// FormatGatewayToolResult converts an MCP tool result into the string content
// sent back to the model as a tool_result. It keeps ordinary small results
// unchanged, applies a Gmail-specific JSON slimming pass for fetch-email
// tools, and enforces a final byte cap for every result shape.
func FormatGatewayToolResult(upstreamToolName string, result *mcpgo.CallToolResult) string {
	text := gatewayResultText(result)
	if text == "" {
		return ""
	}
	if isGoogleSuperFetchEmails(upstreamToolName) {
		if sanitized, ok := sanitizeGmailResultText(text); ok {
			text = sanitized
		}
	}
	return capGatewayResult(text, defaultGatewayResultCapBytes)
}

func gatewayResultText(result *mcpgo.CallToolResult) string {
	if result == nil {
		return ""
	}
	var texts []string
	for _, item := range result.Content {
		switch v := item.(type) {
		case mcpgo.TextContent:
			texts = append(texts, v.Text)
		default:
			data, err := json.Marshal(item)
			if err == nil {
				texts = append(texts, string(data))
			}
		}
	}
	if len(texts) > 0 {
		return strings.Join(texts, "\n")
	}
	if result.StructuredContent != nil {
		data, err := json.Marshal(result.StructuredContent)
		if err == nil {
			return string(data)
		}
	}
	return ""
}

func isGoogleSuperFetchEmails(toolName string) bool {
	upper := strings.ToUpper(toolName)
	return strings.Contains(upper, "GOOGLESUPER_FETCH_EMAILS")
}

func sanitizeGmailResultText(text string) (string, bool) {
	var decoded any
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	if err := dec.Decode(&decoded); err != nil {
		return "", false
	}
	sanitized := sanitizeGmailValue(decoded)
	data, err := json.Marshal(sanitized)
	if err != nil {
		return "", false
	}
	return string(data), true
}

func sanitizeGmailValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, child := range v {
			lower := strings.ToLower(key)
			if gmailDropFields[lower] {
				if lower == "payload" {
					if payload, ok := child.(map[string]any); ok {
						if headers, ok := payload["headers"]; ok {
							if _, exists := out["headers"]; !exists {
								out["headers"] = sanitizeGmailHeaders(headers)
							}
						}
					}
				}
				continue
			}
			if lower == "headers" {
				out[key] = sanitizeGmailHeaders(child)
				continue
			}
			out[key] = sanitizeGmailValue(child)
		}
		return out
	case []any:
		out := make([]any, 0, len(v))
		for _, child := range v {
			out = append(out, sanitizeGmailValue(child))
		}
		return out
	default:
		return value
	}
}

func sanitizeGmailHeaders(value any) any {
	switch headers := value.(type) {
	case []any:
		out := make([]any, 0, len(headers))
		for _, header := range headers {
			h, ok := header.(map[string]any)
			if !ok {
				continue
			}
			name, _ := h["name"].(string)
			if !gmailHeaderAllowlist[strings.ToLower(name)] {
				continue
			}
			out = append(out, sanitizeGmailValue(h))
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(headers))
		for key, child := range headers {
			if gmailHeaderAllowlist[strings.ToLower(key)] {
				out[key] = sanitizeGmailValue(child)
			}
		}
		return out
	default:
		return value
	}
}

func capGatewayResult(text string, capBytes int) string {
	if capBytes <= 0 || len(text) <= capBytes {
		return text
	}
	var decoded any
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	if err := dec.Decode(&decoded); err == nil {
		return marshalTruncatedJSONPrefix(text, len(text), capBytes)
	}
	return capTextGatewayResult(text, capBytes)
}

func capTextGatewayResult(text string, capBytes int) string {
	suffix := fmt.Sprintf("\n\n[gateway result truncated: original_bytes=%d cap_bytes=%d", len(text), capBytes)
	prefixBudget := capBytes - len(suffix) - len(" prefix_bytes=]")
	if prefixBudget < 0 {
		prefixBudget = 0
	}
	prefix := truncateStringBytes(text, prefixBudget)
	suffix = fmt.Sprintf("%s prefix_bytes=%d]", suffix, len(prefix))
	for len(prefix)+len(suffix) > capBytes && len(prefix) > 0 {
		prefix = truncateStringBytes(prefix, len(prefix)-1)
		suffix = fmt.Sprintf("\n\n[gateway result truncated: original_bytes=%d cap_bytes=%d prefix_bytes=%d]", len(text), capBytes, len(prefix))
	}
	return prefix + suffix
}

func marshalTruncatedJSONPrefix(text string, originalBytes, capBytes int) string {
	prefix := truncateStringBytes(text, capBytes)
	payload := map[string]any{
		"truncated":      true,
		"original_bytes": originalBytes,
		"cap_bytes":      capBytes,
		"prefix_bytes":   len(prefix),
		"content_prefix": prefix,
	}
	data, err := json.Marshal(payload)
	for err == nil && len(data) > capBytes && len(prefix) > 0 {
		overflow := len(data) - capBytes
		nextLen := len(prefix) - overflow
		if nextLen >= len(prefix) {
			nextLen = len(prefix) - 1
		}
		if nextLen < 0 {
			nextLen = 0
		}
		prefix = truncateStringBytes(prefix, nextLen)
		payload["prefix_bytes"] = len(prefix)
		payload["content_prefix"] = prefix
		data, err = json.Marshal(payload)
	}
	if err != nil || len(data) > capBytes {
		return capTextGatewayResult(text, capBytes)
	}
	return string(data)
}

func truncateStringBytes(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	if limit <= 0 {
		return ""
	}
	prefix := text[:limit]
	for len(prefix) > 0 && !utf8.ValidString(prefix) {
		prefix = prefix[:len(prefix)-1]
	}
	return prefix
}
