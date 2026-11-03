package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v2"
)

const okfVersion = "0.1"

type okfDocument struct {
	Frontmatter map[string]any
	Body        string
}

func parseOKFDocument(content string) (okfDocument, bool) {
	trimmed := strings.TrimPrefix(content, "\ufeff")
	lines := strings.Split(trimmed, "\n")
	if len(lines) < 3 || strings.TrimSpace(lines[0]) != "---" {
		return okfDocument{}, false
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return okfDocument{}, false
	}
	var fm map[string]any
	if err := yaml.Unmarshal([]byte(strings.Join(lines[1:end], "\n")), &fm); err != nil {
		return okfDocument{}, false
	}
	if strings.TrimSpace(fmt.Sprint(fm["type"])) == "" {
		return okfDocument{}, false
	}
	return okfDocument{
		Frontmatter: fm,
		Body:        strings.TrimLeft(strings.Join(lines[end+1:], "\n"), "\n"),
	}, true
}

func renderOKFDocument(frontmatter map[string]any, body string) string {
	fm := make(map[string]any, len(frontmatter)+1)
	for k, v := range frontmatter {
		if strings.TrimSpace(k) == "" || v == nil {
			continue
		}
		fm[k] = v
	}
	if _, ok := fm["okf_version"]; !ok {
		fm["okf_version"] = okfVersion
	}

	ordered := []string{
		"okf_version",
		"type",
		"title",
		"description",
		"resource",
		"tags",
		"timestamp",
		"source",
		"dimension",
		"apps",
		"activity_sessions",
		"sent_message_count",
		"evidence_first_at",
		"evidence_last_at",
	}
	seen := make(map[string]bool, len(ordered))
	var b strings.Builder
	b.WriteString("---\n")
	for _, key := range ordered {
		if v, ok := fm[key]; ok {
			writeYAMLKey(&b, key, v)
			seen[key] = true
		}
	}
	var rest []string
	for key := range fm {
		if !seen[key] {
			rest = append(rest, key)
		}
	}
	sort.Strings(rest)
	for _, key := range rest {
		writeYAMLKey(&b, key, fm[key])
	}
	b.WriteString("---\n\n")
	b.WriteString(strings.TrimSpace(body))
	b.WriteString("\n")
	return b.String()
}

func writeYAMLKey(b *strings.Builder, key string, value any) {
	out, err := yaml.Marshal(map[string]any{key: value})
	if err != nil {
		return
	}
	b.WriteString(strings.TrimRight(string(out), "\n"))
	b.WriteString("\n")
}

func okfString(fm map[string]any, key string) string {
	v, ok := fm[key]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case fmt.Stringer:
		return strings.TrimSpace(t.String())
	default:
		return strings.TrimSpace(fmt.Sprint(t))
	}
}

func okfStringSlice(fm map[string]any, key string) []string {
	v, ok := fm[key]
	if !ok || v == nil {
		return nil
	}
	switch t := v.(type) {
	case []string:
		return compactStrings(t)
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s := strings.TrimSpace(fmt.Sprint(item)); s != "" {
				out = append(out, s)
			}
		}
		return out
	case string:
		return splitCommaList(t)
	default:
		return nil
	}
}

func splitCommaList(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func compactStrings(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func okfTimestamp(t time.Time) string {
	if t.IsZero() {
		t = time.Now()
	}
	return t.UTC().Format(time.RFC3339)
}

func parseOKFTimestampMs(value string) int64 {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t.UnixMilli()
	}
	if t, err := time.ParseInLocation("2006-01-02 15:04", value, time.Local); err == nil {
		return t.UnixMilli()
	}
	return 0
}

func buildOKFMemoryMarkdown(entryID, title string, updated time.Time, source memorySource, dimension string, points []string) string {
	body := buildBulletSection("## Memory Points", points)
	return renderOKFDocument(map[string]any{
		"type":            "Isoform Memory",
		"title":           title,
		"resource":        "yansu://memory/" + entryID,
		"tags":            []string{"memory", normalizeMemoryDimension(dimension), string(source)},
		"timestamp":       okfTimestamp(updated),
		"source":          string(source),
		"dimension":       normalizeMemoryDimension(dimension),
		"area":            classifyMemoryArea(title+"\n"+strings.Join(points, "\n"), dimension),
		"read_provenance": memoryReadProvenanceJSON(memoryReadSource(source)),
	}, body)
}

func buildOKFKnowledgeMarkdown(entryID, title, description string, ts time.Time, source string, apps, sessionIDs, points []string, kind ...string) string {
	var body strings.Builder
	if strings.TrimSpace(description) != "" {
		body.WriteString("## Description\n\n")
		body.WriteString(strings.TrimSpace(description))
		body.WriteString("\n\n")
	}
	body.WriteString(buildBulletSection("## Key Insights", points))
	fm := map[string]any{
		"type":      "Isoform Knowledge",
		"title":     title,
		"resource":  "yansu://knowledge/" + entryID,
		"tags":      compactStrings([]string{"knowledge", source}),
		"timestamp": okfTimestamp(ts),
	}
	if len(kind) > 0 && validKnowHowKind(kind[0]) {
		fm["kind"] = kind[0]
	}
	if strings.TrimSpace(source) != "" {
		fm["source"] = strings.TrimSpace(source)
	}
	if len(apps) > 0 {
		fm["apps"] = compactStrings(apps)
	}
	if len(sessionIDs) > 0 {
		fm["activity_sessions"] = compactStrings(sessionIDs)
	}
	if strings.TrimSpace(description) != "" {
		fm["description"] = strings.TrimSpace(description)
	}
	return renderOKFDocument(fm, body.String())
}

func isOKFReservedMarkdownFile(name string) bool {
	switch name {
	case "index.md", "log.md":
		return true
	default:
		return false
	}
}

func buildBulletSection(heading string, points []string) string {
	var b strings.Builder
	b.WriteString(heading)
	b.WriteString("\n\n")
	for _, point := range points {
		if point = strings.TrimSpace(point); point != "" {
			b.WriteString("- ")
			b.WriteString(point)
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String()) + "\n"
}
