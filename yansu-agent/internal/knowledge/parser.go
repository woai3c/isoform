package knowledge

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Knowledge represents a knowledge item
type Knowledge struct {
	UUID      string
	Trigger   string
	Content   string
	Tags      []string
	Path      string
	Action    string // "create", "update", "delete"
	FilePath  string
	UpdatedAt time.Time
}

var headerRegex = regexp.MustCompile(`^#\s+(.+)$`)

// ParseFile parses a knowledge markdown file
func ParseFile(filePath string) (*Knowledge, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	k := &Knowledge{
		Action:   "create", // default
		FilePath: filePath,
	}

	if info, err := os.Stat(filePath); err == nil {
		k.UpdatedAt = info.ModTime()
	}
	if parseOKFKnowledge(data, k) {
		return k, nil
	}

	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	var currentSection string
	var contentBuilder strings.Builder

	for scanner.Scan() {
		line := scanner.Text()

		if match := headerRegex.FindStringSubmatch(line); match != nil {
			// Save previous section content
			if currentSection == "Content" {
				k.Content = strings.TrimSpace(contentBuilder.String())
			}

			currentSection = match[1]
			contentBuilder.Reset()
			continue
		}

		switch currentSection {
		case "UUID":
			if trimmed := strings.TrimSpace(line); trimmed != "" {
				k.UUID = trimmed
			}
		case "Trigger":
			if trimmed := strings.TrimSpace(line); trimmed != "" {
				if k.Trigger == "" {
					k.Trigger = trimmed
				} else {
					k.Trigger += " " + trimmed
				}
			}
		case "Tags":
			if trimmed := strings.TrimSpace(line); trimmed != "" {
				// Parse comma-separated tags
				for _, tag := range strings.Split(trimmed, ",") {
					tag = strings.TrimSpace(tag)
					if tag != "" {
						k.Tags = append(k.Tags, tag)
					}
				}
			}
		case "Path":
			if trimmed := strings.TrimSpace(line); trimmed != "" {
				k.Path = trimmed
			}
		case "Action":
			if trimmed := strings.TrimSpace(line); trimmed != "" {
				k.Action = strings.ToLower(trimmed)
			}
		case "Content":
			contentBuilder.WriteString(line)
			contentBuilder.WriteString("\n")
		}
	}

	// Handle final content section
	if currentSection == "Content" {
		k.Content = strings.TrimSpace(contentBuilder.String())
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	return k, nil
}

func parseOKFKnowledge(data []byte, k *Knowledge) bool {
	content := strings.TrimPrefix(string(data), "\ufeff")
	lines := strings.Split(content, "\n")
	if len(lines) < 3 || strings.TrimSpace(lines[0]) != "---" {
		return false
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return false
	}
	fm := parseSimpleYAMLFrontmatter(lines[1:end])
	if strings.TrimSpace(fm["type"]) == "" {
		return false
	}
	k.UUID = fm["uuid"]
	k.Trigger = fm["title"]
	if k.Trigger == "" {
		k.Trigger = fm["description"]
	}
	k.Tags = splitSimpleYAMLList(fm["tags"])
	k.Path = fm["path"]
	k.Action = strings.ToLower(strings.TrimSpace(fm["action"]))
	if k.Action == "" {
		k.Action = "create"
	}
	if ts := strings.TrimSpace(fm["timestamp"]); ts != "" {
		if parsed, err := time.Parse(time.RFC3339, ts); err == nil {
			k.UpdatedAt = parsed
		}
	}
	k.Content = strings.TrimSpace(strings.Join(lines[end+1:], "\n"))
	return true
}

func parseSimpleYAMLFrontmatter(lines []string) map[string]string {
	out := map[string]string{}
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		idx := strings.Index(line, ":")
		if idx <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		value := strings.TrimSpace(line[idx+1:])
		if value == "" {
			var items []string
			for j := i + 1; j < len(lines); j++ {
				item := strings.TrimSpace(lines[j])
				if !strings.HasPrefix(item, "- ") {
					break
				}
				items = append(items, trimYAMLScalar(strings.TrimSpace(strings.TrimPrefix(item, "- "))))
				i = j
			}
			out[key] = strings.Join(items, ", ")
			continue
		}
		out[key] = trimYAMLScalar(value)
	}
	return out
}

func splitSimpleYAMLList(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
		value = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(value, "["), "]"))
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = trimYAMLScalar(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func trimYAMLScalar(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 {
		if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
			return value[1 : len(value)-1]
		}
	}
	return value
}

// LoadAll loads all knowledge files from a directory
func LoadAll(dir string) ([]*Knowledge, error) {
	var result []*Knowledge

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}
		return nil, fmt.Errorf("failed to read directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		if isReservedKnowledgeFile(entry.Name()) {
			continue
		}

		k, err := ParseFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			// Log but continue
			continue
		}
		result = append(result, k)
	}

	return result, nil
}

func isReservedKnowledgeFile(name string) bool {
	switch name {
	case "_index.md", "index.md", "log.md":
		return true
	default:
		return false
	}
}

// TriggerToFileName converts a trigger to a valid file name
func TriggerToFileName(trigger string) string {
	name := strings.ToLower(trigger)
	// Replace non-alphanumeric characters with hyphens
	name = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(name, "-")
	// Remove leading/trailing hyphens
	name = strings.Trim(name, "-")
	// Limit length
	if len(name) > 50 {
		name = name[:50]
		// Don't cut in the middle of a word
		if lastHyphen := strings.LastIndex(name, "-"); lastHyphen > 30 {
			name = name[:lastHyphen]
		}
	}
	if name == "" {
		name = "knowledge"
	}
	return name
}

// WriteKnowledgeFile writes a knowledge item to a file
func WriteKnowledgeFile(dir, uuid, trigger, content string, tags []string, path string) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	fileName := TriggerToFileName(trigger) + ".md"
	filePath := filepath.Join(dir, fileName)

	// Check if file exists, add suffix if needed
	if _, err := os.Stat(filePath); err == nil {
		// File exists, add a unique suffix
		base := TriggerToFileName(trigger)
		for i := 1; i < 100; i++ {
			fileName = fmt.Sprintf("%s-%d.md", base, i)
			filePath = filepath.Join(dir, fileName)
			if _, err := os.Stat(filePath); os.IsNotExist(err) {
				break
			}
		}
	}

	fileContent := renderOKFKnowledge(uuid, trigger, content, tags, path, "create")

	if err := os.WriteFile(filePath, []byte(fileContent), 0644); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	return nil
}

func renderOKFKnowledge(uuid, trigger, content string, tags []string, path, action string) string {
	fm := map[string]string{
		"okf_version": "0.1",
		"type":        "Yansu Knowledge",
		"title":       trigger,
		"timestamp":   time.Now().UTC().Format(time.RFC3339),
		"uuid":        uuid,
		"action":      action,
	}
	if path != "" {
		fm["path"] = path
	}
	keys := []string{"okf_version", "type", "title", "timestamp", "uuid", "action", "path"}
	var b strings.Builder
	b.WriteString("---\n")
	written := map[string]bool{}
	for _, key := range keys {
		if value, ok := fm[key]; ok && strings.TrimSpace(value) != "" {
			fmt.Fprintf(&b, "%s: %s\n", key, quoteYAMLScalar(value))
			written[key] = true
		}
	}
	if len(tags) > 0 {
		b.WriteString("tags:\n")
		for _, tag := range tags {
			if tag = strings.TrimSpace(tag); tag != "" {
				fmt.Fprintf(&b, "  - %s\n", quoteYAMLScalar(tag))
			}
		}
	}
	var rest []string
	for key := range fm {
		if !written[key] {
			rest = append(rest, key)
		}
	}
	sort.Strings(rest)
	for _, key := range rest {
		fmt.Fprintf(&b, "%s: %s\n", key, quoteYAMLScalar(fm[key]))
	}
	b.WriteString("---\n\n")
	b.WriteString(strings.TrimSpace(content))
	b.WriteString("\n")
	return b.String()
}

func quoteYAMLScalar(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return `""`
	}
	if strings.ContainsAny(value, ":#[]{}\n\r\t") || strings.HasPrefix(value, "-") {
		escaped := strings.ReplaceAll(value, `\`, `\\`)
		escaped = strings.ReplaceAll(escaped, `"`, `\"`)
		return `"` + escaped + `"`
	}
	return value
}

// FindKnowledgeFileByUUID finds a knowledge file by UUID in the given directory
func FindKnowledgeFileByUUID(dir, uuid string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		filePath := filepath.Join(dir, entry.Name())
		k, err := ParseFile(filePath)
		if err != nil {
			continue
		}

		if k.UUID == uuid {
			return filePath
		}
	}

	return ""
}

// DeleteKnowledgeFile deletes a knowledge file by UUID
func DeleteKnowledgeFile(dir, uuid string) error {
	filePath := FindKnowledgeFileByUUID(dir, uuid)
	if filePath == "" {
		return nil // Already doesn't exist
	}
	return os.Remove(filePath)
}
