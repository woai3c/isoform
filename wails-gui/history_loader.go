package main

import (
	"context"
	"encoding/json"
	"log"
	"sort"
	"strings"

	"yansu-agent/agent/native"

	"wails-gui/storage"
)

// streamItemHistoryLoader adapts storage.Store's LoadStreamItems into
// native.HistoryLoader so the in-process agent loop can resume prior
// turns instead of treating each message as a fresh session.
//
// Stream items now persist the Anthropic-assigned tool_use_id (v38+),
// so we can rebuild proper tool_use/tool_result pairs. Items from
// older sessions that lack a tool_use_id are silently skipped (the
// model still sees user + assistant text from those turns).
//
// Compaction (v39 stream item kinds) introduces compact_boundary /
// compact_summary rows. When a boundary exists, items with sort_order
// strictly before it are dropped and replaced with a synthetic
// user/assistant pair carrying the matching summary text. The most
// recent boundary wins; earlier boundaries are already represented
// inside the summary the latest one points at.
type streamItemHistoryLoader struct {
	store      *storage.Store
	loadRows   func(context.Context, string) ([]storage.StreamItemRow, error)
	sourceRows func(context.Context, []storage.StreamItemRow) []storage.StreamItemRow
}

func (l *streamItemHistoryLoader) LoadHistory(sessionLocalID string) ([]native.HistoryItem, error) {
	if l == nil || l.store == nil || sessionLocalID == "" {
		return nil, nil
	}
	load := l.store.LoadStreamItems
	if l.loadRows != nil {
		load = l.loadRows
	}
	rows, err := load(context.Background(), sessionLocalID)
	if err != nil {
		return nil, err
	}

	if l.sourceRows != nil {
		rows = l.sourceRows(context.Background(), rows)
	} else if l.loadRows == nil {
		rows = filterSourceBoundChatRows(rows, nil)
	}
	// Raw context-tool pairs are not a native registry/history cache. Existing
	// user/assistant conversation history retains its established contract;
	// source-bound assistant/compaction lineage is a separate baseline gap.
	rows = withoutExternalContextToolHistory(rows)
	rows, summaryHead := applyLatestCompactBoundary(rows)

	// Pre-pass: collect every tool_use_id that exists in this session's
	// rows. Used below to drop reverse-orphan tool_result rows whose id
	// was never declared by any tool_use row. A single missing tool_use
	// row dooms every subsequent send to the Anthropic API with a 400
	// — and once a session reaches that state, it stays dead until the
	// orphan is removed. Filtering here heals already-corrupted
	// sessions on the next load without requiring user data wipe.
	toolUseIDs := make(map[string]bool)
	for _, r := range rows {
		if (r.Kind == "tool_use" || r.Kind == "tool") && r.ToolUseID != "" {
			toolUseIDs[r.ToolUseID] = true
		}
	}

	out := make([]native.HistoryItem, 0, len(rows)+len(summaryHead))
	out = append(out, summaryHead...)
	for _, r := range rows {
		switch r.Kind {
		case "user":
			if r.Content == "" && r.Images == "" {
				continue
			}
			out = append(out, native.HistoryItem{
				Kind:    "user",
				Content: r.Content,
				Images:  r.Images,
			})
		case "assistant":
			if r.Content == "" {
				continue
			}
			out = append(out, native.HistoryItem{
				Kind:    "assistant",
				Content: r.Content,
			})
		case "tool_use", "tool":
			// Skip legacy items without a tool_use_id — they can't be
			// paired and would cause a 400 from the API.
			if r.ToolUseID == "" {
				continue
			}
			out = append(out, native.HistoryItem{
				Kind:      "tool_use",
				ToolName:  r.ToolName,
				ToolInput: r.ToolInput,
				ToolUseID: r.ToolUseID,
			})
		case "tool_result", "result":
			if r.ToolUseID == "" {
				continue
			}
			if !toolUseIDs[r.ToolUseID] {
				log.Printf("[history-loader] WARN dropping orphan tool_result session=%s sortOrder=%d tool_use_id=%s tool=%s — no matching tool_use row in this session",
					sessionLocalID, r.SortOrder, r.ToolUseID, r.ToolName)
				continue
			}
			out = append(out, native.HistoryItem{
				Kind:      "tool_result",
				Content:   r.Content,
				ToolName:  r.ToolName,
				ToolUseID: r.ToolUseID,
			})
		}
	}

	// The frontend persists the current turn's user message to the DB
	// before calling SendPrompt, so the last user row here IS the prompt
	// the loop is about to append from cfg.Prompt. Drop trailing user
	// rows to avoid duplicating that message in the request.
	for len(out) > 0 && out[len(out)-1].Kind == "user" {
		out = out[:len(out)-1]
	}
	return out, nil
}

// applyLatestCompactBoundary scans rows for the most recent
// compact_boundary, drops everything strictly before it (the rows
// represented by the summary), and returns the suffix plus a synthetic
// [user with summary, assistant ack] HistoryItem prefix.
//
// Behaviour when no boundary exists: rows pass through unchanged and
// the returned head is empty — the legacy load path.
//
// The boundary's content carries JSON metadata; we look up the matching
// compact_summary row by ID. If the summary row is missing (corrupt
// state, partial write) we fall back to the most recent compact_summary
// before the boundary. If even that's missing we drop the older rows
// without re-injection — the loss is bounded to one chunk and the
// model still has the recent verbatim tail.
func applyLatestCompactBoundary(rows []storage.StreamItemRow) ([]storage.StreamItemRow, []native.HistoryItem) {
	boundaryIdx := -1
	for i := len(rows) - 1; i >= 0; i-- {
		if rows[i].Kind == "compact_boundary" {
			boundaryIdx = i
			break
		}
	}
	if boundaryIdx < 0 {
		return rows, nil
	}

	var meta struct {
		SummaryID string `json:"summary_id"`
	}
	_ = json.Unmarshal([]byte(rows[boundaryIdx].Content), &meta)

	var summaryText string
	for _, r := range rows {
		if r.Kind == "compact_summary" && r.ID == meta.SummaryID {
			summaryText = r.Content
			break
		}
	}
	if summaryText == "" {
		for i := boundaryIdx - 1; i >= 0; i-- {
			if rows[i].Kind == "compact_summary" {
				summaryText = rows[i].Content
				break
			}
		}
	}

	tail := rows[boundaryIdx+1:]
	cleanTail := tail[:0]
	for _, r := range tail {
		if r.Kind == "compact_summary" || r.Kind == "compact_boundary" {
			continue
		}
		cleanTail = append(cleanTail, r)
	}

	if strings.TrimSpace(summaryText) == "" {
		return cleanTail, nil
	}
	head := []native.HistoryItem{
		{
			Kind:    "user",
			Content: "[Conversation summary follows — earlier messages have been compacted to fit the context window.]\n\n" + summaryText,
		},
		{
			Kind:    "assistant",
			Content: "Understood — I have the summary. Continuing.",
		},
	}
	return cleanTail, head
}

// No per-turn lineage exists for model replies or compaction. Once a source is
// withheld, all later rows may quote it and must stay outside every history view.
func filterSourceBoundChatRows(rows []storage.StreamItemRow, sourceText func(string, string) (string, bool)) []storage.StreamItemRow {
	out := make([]storage.StreamItemRow, 0, len(rows))
	dependencies := []string{}
	seen := map[string]bool{}
	for _, row := range rows {
		if mobileOfferMetadataPresent(row.AutomationContext) && !seen[row.AutomationContext] {
			dependencies = append(dependencies, row.AutomationContext)
			seen[row.AutomationContext] = true
		}
		if len(dependencies) != 0 {
			if sourceText == nil {
				return out
			}
			toolInput, toolText, allowed := sourceBoundToolInput(row.ToolInput)
			if !allowed {
				return out
			}
			originalText := row.Content + "\n" + row.ToolName + "\n" + row.ToolInput + "\n" + toolText
			for _, dependency := range dependencies {
				// Check decoded argument values before redaction can disguise a held
				// mail subject. The phone and model both decode JSON tool arguments.
				if _, allowed := sourceText(originalText, dependency); !allowed {
					return out
				}
			}
			if redactActivityModelText(row.ToolName) != row.ToolName {
				return out
			}
			row.Content = redactActivityModelText(row.Content)
			row.ToolInput = toolInput
		}
		out = append(out, row)
	}
	return out
}

// Decode before checking or redacting: normal JSON encoding escapes ampersands,
// and credentials can be hidden in escaped string values. Unchanged JSON keeps
// its exact representation; changed values are encoded safely with exact numbers.
func sourceBoundToolInput(raw string) (string, string, bool) {
	if strings.TrimSpace(raw) == "" {
		return raw, "", true
	}
	if len(raw) > 1<<20 || !json.Valid([]byte(raw)) || !sourceBoundToolUniqueKeys(raw) {
		return "", "", false
	}
	decode := func(text string) (any, error) {
		var value any
		decoder := json.NewDecoder(strings.NewReader(text))
		decoder.UseNumber()
		err := decoder.Decode(&value)
		return value, err
	}
	value, err := decode(raw)
	if err != nil {
		return "", "", false
	}
	parts := []string{}
	changed, remaining := false, 10000
	var walk func(any, int) (any, bool)
	walk = func(value any, depth int) (any, bool) {
		remaining--
		if depth > 32 || remaining < 0 {
			return nil, false
		}
		switch item := value.(type) {
		case string:
			parts = append(parts, item)
			// Some tools carry a second JSON document in a string argument.
			// Follow the same nested-object handling as activity payload redaction.
			trimmed := strings.TrimSpace(item)
			if (strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")) && json.Valid([]byte(trimmed)) {
				if !sourceBoundToolUniqueKeys(trimmed) {
					return nil, false
				}
				nested, err := decode(trimmed)
				if err != nil {
					return nil, false
				}
				before, err := json.Marshal(nested)
				if err != nil {
					return nil, false
				}
				nested, allowed := walk(nested, depth+1)
				if !allowed {
					return nil, false
				}
				after, err := json.Marshal(nested)
				if err != nil {
					return nil, false
				}
				if string(before) != string(after) {
					item = string(after)
				}
			}
			redacted := redactActivityModelText(item)
			changed = changed || redacted != item
			return redacted, true
		case json.Number:
			parts = append(parts, string(item))
			redacted := redactActivityModelText(string(item))
			if redacted != string(item) {
				changed = true
				return redacted, true
			}
			return item, true
		case []any:
			for i, child := range item {
				redacted, allowed := walk(child, depth+1)
				if !allowed {
					return nil, false
				}
				item[i] = redacted
			}
			return item, true
		case map[string]any:
			keys := make([]string, 0, len(item))
			for key := range item {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				parts = append(parts, key)
				// Replacing an argument key could collide with another key or
				// change the tool schema; withhold that tool and its descendants.
				if redactActivityModelText(key) != key {
					return nil, false
				}
				redacted, allowed := walk(item[key], depth+1)
				if !allowed {
					return nil, false
				}
				item[key] = redacted
			}
			return item, true
		default:
			return value, true
		}
	}
	value, allowed := walk(value, 0)
	if !allowed {
		return "", "", false
	}
	decodedText := strings.Join(parts, "\n")
	if !changed {
		return raw, decodedText, true
	}
	encoded, err := json.Marshal(value)
	return string(encoded), decodedText, err == nil
}

// Map decoding discards overwritten arguments before read gates can inspect
// them. Check decoded object keys first, including escaped spellings. Each
// object has its own key set; ordinary, source-free history bypasses this gate.
// Callers already checked json.Valid, so the scanner consumes exactly one value.
func sourceBoundToolUniqueKeys(raw string) bool {
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	remaining := 10000
	var scan func(int) bool
	scan = func(depth int) bool {
		remaining--
		if depth > 32 || remaining < 0 {
			return false
		}
		token, err := decoder.Token()
		if err != nil {
			return false
		}
		delimiter, container := token.(json.Delim)
		if !container {
			return true
		}
		switch delimiter {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				keyToken, err := decoder.Token()
				key, isString := keyToken.(string)
				if err != nil || !isString || seen[key] {
					return false
				}
				seen[key] = true
				if !scan(depth + 1) {
					return false
				}
			}
			end, err := decoder.Token()
			return err == nil && end == json.Delim('}')
		case '[':
			for decoder.More() {
				if !scan(depth + 1) {
					return false
				}
			}
			end, err := decoder.Token()
			return err == nil && end == json.Delim(']')
		default:
			return false
		}
	}
	return scan(0)
}

func withoutExternalContextToolHistory(rows []storage.StreamItemRow) []storage.StreamItemRow {
	ids := make(map[string]bool)
	isContextTool := func(name string) bool {
		return allowsTool(chatContextToolNames, strings.TrimPrefix(name, "mcp__isoform__"))
	}
	for _, row := range rows {
		if isContextTool(row.ToolName) && row.ToolUseID != "" {
			ids[row.ToolUseID] = true
		}
	}
	out := make([]storage.StreamItemRow, 0, len(rows))
	for _, row := range rows {
		switch row.Kind {
		case "tool", "tool_use", "tool_result", "result":
			if isContextTool(row.ToolName) || ids[row.ToolUseID] {
				continue
			}
		}
		out = append(out, row)
	}
	return out
}
