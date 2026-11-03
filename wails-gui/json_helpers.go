package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// stripCodeFences removes a leading ```json / ``` fence and a trailing ```
// fence from an LLM response, returning the trimmed inner content.
func stripCodeFences(s string) string {
	s = strings.TrimSpace(s)
	if after, ok := strings.CutPrefix(s, "```json"); ok {
		s = after
	} else if after, ok := strings.CutPrefix(s, "```"); ok {
		s = after
	}
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}

// extractJSONFromMixedContent extracts a JSON object from mixed content.
// It first looks for JSON within ```json blocks, then falls back to brace matching.
// This mirrors backend/internal/utils/json.go:ExtractJSONFromMixedContent.
func extractJSONFromMixedContent(content string) string {
	content = strings.TrimSpace(content)

	// First try to find JSON within ```json blocks
	if start := strings.Index(content, "```json"); start != -1 {
		start = strings.Index(content[start:], "\n") + start + 1
		if end := strings.Index(content[start:], "```"); end != -1 {
			return strings.TrimSpace(content[start : start+end])
		}
	}

	// Try to find JSON object by looking for { and }
	return extractJSONWithBraceMatching(content)
}

// extractJSONWithBraceMatching extracts a JSON object from mixed content
// by finding the first '{' and its matching '}'.
func extractJSONWithBraceMatching(content string) string {
	start := strings.Index(content, "{")
	if start == -1 {
		return content
	}

	braceCount := 0
	for i := start; i < len(content); i++ {
		switch content[i] {
		case '{':
			braceCount++
		case '}':
			braceCount--
			if braceCount == 0 {
				return strings.TrimSpace(content[start : i+1])
			}
		}
	}

	return content
}

// unmarshalJSONBestEffort tries strict unmarshaling first, then falls back
// to extractJSONFromMixedContent for LLM responses that contain trailing text.
func unmarshalJSONBestEffort(raw string, v any) error {
	if err := json.Unmarshal([]byte(raw), v); err != nil {
		extracted := extractJSONFromMixedContent(raw)
		if err2 := json.Unmarshal([]byte(extracted), v); err2 != nil {
			return fmt.Errorf("parse %q: %w", raw, err2)
		}
	}
	return nil
}
