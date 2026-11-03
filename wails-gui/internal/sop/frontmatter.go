package sop

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

const frontmatterFence = "---"

// Parse turns raw SOP markdown (e.g. an SOP.md the author agent wrote) into an
// Entry. Callers own the machine fields (ID/Status/Source/version/timestamps)
// and should override them after parsing.
func Parse(id, content string) Entry { return parseEntry(id, content) }

// parseEntry parses an SOP.md file into an Entry. `id` seeds the entry ID and
// is used when the frontmatter omits one (derived from the bundle dir name). A
// file without a leading fence is treated as a bare body so a hand-edited or
// malformed file is never silently dropped.
func parseEntry(id, content string) Entry {
	e := Entry{ID: id, Scope: "mine"}
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
		case "id":
			if val != "" {
				e.ID = val
			}
		case "title":
			e.Title = val
		case "description":
			e.Description = val
		case "trigger":
			e.Trigger = val
		case "status":
			e.Status = Status(val)
		case "source":
			e.Source = Source(val)
		case "scope":
			e.Scope = val
		case "topicIds":
			if err := json.Unmarshal([]byte(val), &e.TopicIDs); err != nil {
				e.metadataErr = fmt.Errorf("sop: invalid topicIds: %w", err)
			}
		case "sourceIds":
			var arr []string
			if err := json.Unmarshal([]byte(val), &arr); err == nil {
				e.SourceIDs = arr
			}
		case "readProvenance":
			e.ReadProvenance = val
		case "sites":
			var arr []string
			if err := json.Unmarshal([]byte(val), &arr); err == nil {
				e.Sites = arr
			}
		case "updateExisting":
			e.UpdateExisting = val
		case "version":
			e.Version, _ = strconv.Atoi(val)
		case "createdAt":
			e.CreatedAt, _ = strconv.ParseInt(val, 10, 64)
		case "updatedAt":
			e.UpdatedAt, _ = strconv.ParseInt(val, 10, 64)
		}
	}

	e.Body = strings.TrimLeft(strings.Join(lines[end+1:], "\n"), "\n")
	return e
}

// serialize renders an Entry to its on-disk SOP.md form.
func (e Entry) serialize() string {
	ids := e.SourceIDs
	if ids == nil {
		ids = []string{}
	}
	idsJSON, _ := json.Marshal(ids)

	var b strings.Builder
	b.WriteString(frontmatterFence + "\n")
	fmt.Fprintf(&b, "id: %s\n", e.ID)
	fmt.Fprintf(&b, "title: %s\n", oneLine(e.Title))
	fmt.Fprintf(&b, "description: %s\n", oneLine(e.Description))
	fmt.Fprintf(&b, "trigger: %s\n", oneLine(e.Trigger))
	fmt.Fprintf(&b, "status: %s\n", e.Status)
	fmt.Fprintf(&b, "source: %s\n", e.Source)
	scope := e.Scope
	if scope == "" {
		scope = "mine"
	}
	fmt.Fprintf(&b, "scope: %s\n", scope)
	if len(e.TopicIDs) > 0 {
		topicsJSON, _ := json.Marshal(e.TopicIDs)
		fmt.Fprintf(&b, "topicIds: %s\n", topicsJSON)
	}
	fmt.Fprintf(&b, "sourceIds: %s\n", string(idsJSON))
	if e.ReadProvenance != "" {
		fmt.Fprintf(&b, "readProvenance: %s\n", oneLine(e.ReadProvenance))
	}
	if len(e.Sites) > 0 {
		sitesJSON, _ := json.Marshal(e.Sites)
		fmt.Fprintf(&b, "sites: %s\n", string(sitesJSON))
	}
	fmt.Fprintf(&b, "version: %d\n", e.Version)
	fmt.Fprintf(&b, "createdAt: %d\n", e.CreatedAt)
	fmt.Fprintf(&b, "updatedAt: %d\n", e.UpdatedAt)
	b.WriteString(frontmatterFence + "\n\n")

	body := strings.TrimLeft(e.Body, "\n")
	b.WriteString(body)
	if !strings.HasSuffix(body, "\n") {
		b.WriteString("\n")
	}
	return b.String()
}

// oneLine collapses newlines so a scalar frontmatter value can never break the
// `key: value` line format.
func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.TrimSpace(s)
}
