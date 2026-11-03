package handoff

import (
	"encoding/json"
	"strconv"
	"strings"
)

const frontmatterFence = "---"

// Parse turns raw handoff markdown (e.g. a HANDOFF.md the author agent wrote)
// into an Entry. Callers own the machine fields (Status/Source/timestamps/
// chatSessionId) and should override them after parsing.
func Parse(id, content string) Entry { return parseEntry(id, content) }

// parseEntry parses a handoff markdown file into an Entry. `id` seeds the entry
// ID and is used when the frontmatter omits one (e.g. derived from the
// filename). A file without a leading fence is treated as a bare body so a
// hand-edited or malformed file is never silently dropped.
func parseEntry(id, content string) Entry {
	e := Entry{ID: id}
	content = strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(content, "\n")

	if len(lines) == 0 || strings.TrimSpace(lines[0]) != frontmatterFence {
		e.Body = strings.TrimLeft(content, "\n")
		return e
	}

	// The closing fence is the first bare `---` after line 0. Body `---` rules
	// (markdown horizontal rules) come after it and are never confused for it.
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == frontmatterFence {
			end = i
			break
		}
	}
	if end < 0 {
		e.Body = strings.TrimLeft(content, "\n")
		return e
	}

	for i := 1; i < end; i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		// Split on the FIRST colon so values may themselves contain colons.
		idx := strings.Index(line, ":")
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		switch key {
		case "schemaVersion":
			e.SchemaVersion, _ = strconv.Atoi(val)
		case "id":
			if val != "" {
				e.ID = val
			}
		case "title":
			e.Title = val
		case "status":
			e.Status = Status(val)
		case "source":
			e.Source = Source(val)
		case "sourceId":
			e.SourceID = val
		case "sourceLink":
			e.SourceLink = val
		case "appName":
			e.AppName = val
		case "workdir":
			e.Workdir = val
		case "keyFiles":
			var arr []string
			if err := json.Unmarshal([]byte(val), &arr); err == nil {
				e.KeyFiles = arr
			}
		case "chatSessionId":
			e.ChatSessionID = val
		case "cronJobId":
			e.CronJobID = val
		case "attemptId":
			e.AttemptID = val
		case "attemptStartedAt":
			e.AttemptStartedAt, _ = strconv.ParseInt(val, 10, 64)
		case "lastActivityAt":
			e.LastActivityAt, _ = strconv.ParseInt(val, 10, 64)
		case "blockedReason":
			e.BlockedReason = val
		case "criteria":
			var arr []string
			if err := json.Unmarshal([]byte(val), &arr); err == nil {
				e.Criteria = arr
			}
		case "verification":
			var v Verification
			if err := json.Unmarshal([]byte(val), &v); err == nil {
				e.Verification = &v
			}
		case "createdAt":
			e.CreatedAt, _ = strconv.ParseInt(val, 10, 64)
		case "updatedAt":
			e.UpdatedAt, _ = strconv.ParseInt(val, 10, 64)
		}
	}

	e.Body = strings.TrimLeft(strings.Join(lines[end+1:], "\n"), "\n")
	return e
}
