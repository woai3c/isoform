package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"unicode"
)

func validMemoryArea(area string) bool {
	switch area {
	case "", "work", "people", "tools", "routines", "interests", "communication", "other":
		return true
	default:
		return false
	}
}

// classifyMemoryArea uses only the belief's words and existing dimension (the
// store's kind). Source apps, people records and topic metadata are not inputs.
// Sensitive words take precedence over every ordinary area; unknowns stay other.
func classifyMemoryArea(words, kind string) string {
	words = strings.ToLower(words)
	tokens := " " + strings.Join(strings.FieldsFunc(words, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) }), " ") + " "
	contains := func(keywords string) bool {
		for _, word := range strings.Split(keywords, "|") {
			if strings.Contains(tokens, " "+word+" ") || ([]rune(word)[0] > unicode.MaxASCII && strings.Contains(words, word)) {
				return true
			}
		}
		return false
	}
	if contains("health|medical|medicine|medication|medications|doctor|hospital|diagnosis|diagnosed|therapy|therapist|depression|anxiety|adhd|autism|bipolar|cancer|diabetes|pregnant|pregnancy|disability|illness|pain|insulin|mental|allergy|allergies|blood|symptom|symptoms|treatment|exercise|diet|sleep|religion|religious|church|muslim|christian|politics|political|democrat|republican|sexual|sexuality|gay|lesbian|race|ethnicity|salary|debt|bank|password|secret|健康|医疗|疾病|心理|医院|药|宗教|政治|性取向|财务") {
		return "other"
	}
	rules := []struct{ area, words string }{
		{"communication", "email|emails|message|messages|reply|replies|concise|tone|communication|communicate|writing|沟通|邮件|回复"},
		{"tools", "tool|tools|editor|terminal|software|keyboard|browser|bun|npm|pnpm|git|vscode|package|packages|automation|automations|automatic|automatically|automate|automated|replay|replays|replayed|repo|repos|repository|backup|hub|script|scripts|工具|编辑器|终端|自动化|仓库"},
		{"people", "colleague|colleagues|coworker|coworkers|manager|family|friend|friends|partner|teammate|teammates|同事|家人|朋友"},
		{"routines", "routine|routines|daily|weekly|morning|evening|habit|habits|每天|每周|早上|习惯"},
		{"interests", "hobby|hobbies|enjoy|enjoys|music|reading|photography|cooking|hiking|兴趣|音乐|摄影"},
		{"work", "work|project|projects|deadline|deadlines|meeting|meetings|customer|customers|release|工作|项目|会议"},
	}
	// A belief sits in the group its title is about: the first area word in
	// the title wins, so "Uses an automation to check for replies" is tools.
	title, _, _ := strings.Cut(words, "\n")
	titleTokens := " " + strings.Join(strings.FieldsFunc(title, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) }), " ") + " "
	// A reply that replays on its own is about automation, not writing (About
	// you Y33): the reply words don't make it a communication belief.
	automated := false
	for _, word := range []string{"automatic", "automatically", "automate", "automated", "replay", "replays", "replayed"} {
		automated = automated || strings.Contains(titleTokens, " "+word+" ")
	}
	best, bestAt := "", -1
	for _, rule := range rules {
		for _, word := range strings.Split(rule.words, "|") {
			if automated && rule.area == "communication" && (word == "reply" || word == "replies") {
				continue
			}
			at := strings.Index(titleTokens, " "+word+" ")
			if []rune(word)[0] > unicode.MaxASCII {
				at = strings.Index(title, word)
			}
			if at >= 0 && (bestAt < 0 || at < bestAt) {
				best, bestAt = rule.area, at
			}
		}
	}
	if best != "" {
		return best
	}
	for _, rule := range rules {
		if contains(rule.words) {
			return rule.area
		}
	}
	// An execution memory explicitly describing a workflow belongs to routines.
	if kind == "agentic_memory" && contains("workflow|process|steps") {
		return "routines"
	}
	return "other"
}

func memoryAreaWords(content string) string {
	content = legacyMemoryWithoutConfirmation(content)
	doc, ok := parseOKFDocument(content)
	return memoryAreaWordsFromDocument(content, doc, ok)
}

func memoryAreaWordsFromDocument(content string, doc okfDocument, ok bool) string {
	// Use the existing substantive-body reader: it removes only known metadata
	// in converted legacy headers and preserves bold facts and body headings.
	return extractTitleFromDocument(content, doc, ok) + "\n" + memoryConfirmationBodyFromDocument(content, doc, ok)
}

// Legacy backfill is lazy and in memory only, once per unchanged revision.
// Bound the cache and retain hashes and area labels, never private belief text.
var memoryAreaBackfill = struct {
	sync.Mutex
	areas map[[32]byte]string
}{areas: make(map[[32]byte]string)}

func memoryAreaOnRead(content string, fm memoryFrontmatter) string {
	return memoryAreaOnReadWithWords(content, fm, func() string { return memoryAreaWords(content) })
}

func memoryAreaOnReadDocument(doc parsedMemoryEntryDocument) string {
	return memoryAreaOnReadWithWords(doc.content, doc.frontmatter, func() string {
		return memoryAreaWordsFromDocument(doc.metadataContent, doc.doc, doc.ok)
	})
}

func memoryAreaOnReadWithWords(content string, fm memoryFrontmatter, words func() string) string {
	if fm.Area != "" {
		if fm.Area == "communication" {
			// Launch walk Y33: a belief is in How you communicate only when its
			// title says so. A stored area that came from the body is read again.
			title, _, _ := strings.Cut(words(), "\n")
			return classifyMemoryArea(title, fm.Dimension)
		}
		if validMemoryArea(fm.Area) {
			return fm.Area
		}
		return "other"
	}
	key := sha256.Sum256([]byte(content))
	memoryAreaBackfill.Lock()
	defer memoryAreaBackfill.Unlock()
	if area, ok := memoryAreaBackfill.areas[key]; ok {
		return area
	}
	area := classifyMemoryArea(words(), fm.Dimension)
	if len(memoryAreaBackfill.areas) >= 1024 {
		clear(memoryAreaBackfill.areas)
	}
	memoryAreaBackfill.areas[key] = area
	return area
}

// Topic assignments and observed-message evidence survive machine rewrites.
func preserveMemoryTopics(content, previous string) string {
	fm := parseMemoryFrontmatter(previous)
	if doc, ok := parseOKFDocument(content); ok {
		if len(fm.TopicIDs) > 0 {
			doc.Frontmatter["topic_ids"] = fm.TopicIDs
		}
		withMemorySentMessageEvidence(doc.Frontmatter, fm)
		return renderOKFDocument(doc.Frontmatter, doc.Body)
	}
	return content
}

func validateMemoryMetadata(content string) error {
	if doc, ok := parseOKFDocument(legacyMemoryWithoutConfirmation(content)); ok {
		if raw, exists := doc.Frontmatter["area"]; exists {
			if area, ok := raw.(string); !ok || !validMemoryArea(area) {
				return fmt.Errorf("invalid memory area")
			}
		}
		if raw, exists := doc.Frontmatter["topic_ids"]; exists {
			data, err := json.Marshal(raw)
			var ids []string
			if err != nil || json.Unmarshal(data, &ids) != nil {
				return fmt.Errorf("invalid memory topic ids")
			}
			for _, id := range ids {
				if !validChatTopicID(id) {
					return fmt.Errorf("invalid memory topic id")
				}
			}
		}
	}
	fm := parseMemoryFrontmatter(content)
	if !validMemoryArea(fm.Area) {
		return fmt.Errorf("invalid memory area")
	}
	for _, id := range fm.TopicIDs {
		if !validChatTopicID(id) {
			return fmt.Errorf("invalid memory topic id")
		}
	}
	return nil
}
