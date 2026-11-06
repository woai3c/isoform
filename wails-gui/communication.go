package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	communicationMaxSentMessageCount = 100000
	communicationMaxEvidenceIDs      = 256
	communicationMaxMessageBytes     = 16 * 1024
)

type communicationMessage struct {
	Source        string
	App           string
	MessageID     string
	AuthorID      string
	OwnerID       string
	Text          string
	SentAt        time.Time
	BodyAvailable bool
}

type communicationObservation struct {
	Category string
	Value    string
	Text     string
}

func communicationObservations(message communicationMessage) []communicationObservation {
	if message.AuthorID == "" || message.AuthorID != message.OwnerID || !message.BodyAvailable || len(message.Text) == 0 || len(message.Text) > communicationMaxMessageBytes || message.SentAt.IsZero() {
		return nil
	}
	switch message.Source {
	case "isoform_chat", "slack", "wechat":
	default:
		return nil
	}
	text := strings.TrimSpace(message.Text)
	if text == "" {
		return nil
	}
	language := communicationLanguage(text)
	languageText := "Writes sent messages in English."
	if language == "zh-CN" {
		languageText = "Writes sent messages in Chinese."
	}
	sentences := communicationSentenceCount(text)
	sentenceValue := strconv.Itoa(sentences)
	// Y42: a stored belief never carries a "(s)" placeholder, so the count is
	// spelled for one sentence or for many.
	sentenceText := fmt.Sprintf("Writes %d sentences in sent messages.", sentences)
	if sentences == 1 {
		sentenceText = "Writes 1 sentence in sent messages."
	}
	if sentences > 4 {
		sentenceValue = "5+"
		sentenceText = "Writes 5 or more sentences in sent messages."
	}
	greeting, _ := communicationGreeting(text)
	greetingText := "Starts sent messages without a greeting."
	greetingValue := "false"
	if greeting {
		greetingText = "Starts sent messages with a greeting."
		greetingValue = "true"
	}
	return []communicationObservation{
		{Category: "language", Value: language, Text: languageText},
		{Category: "sentences", Value: sentenceValue, Text: sentenceText},
		{Category: "greeting", Value: greetingValue, Text: greetingText},
	}
}

func communicationLanguage(text string) string {
	for _, r := range text {
		if unicode.In(r, unicode.Han) {
			return "zh-CN"
		}
	}
	return "en"
}

func communicationSentenceCount(text string) int {
	count := 0
	for _, sentence := range strings.FieldsFunc(text, func(r rune) bool {
		return strings.ContainsRune(".!?。！？\n", r)
	}) {
		if strings.TrimSpace(sentence) != "" {
			count++
		}
	}
	return max(1, count)
}

var (
	communicationEnglishGreeting = regexp.MustCompile(`(?i)^\s*(?:hi|hello|dear|hey)\s+([\pL\pM][\pL\pM .'’-]{0,79}?)\s*[,，:：!！]`)
	communicationChineseGreeting = regexp.MustCompile(`^\s*([\pL\pM][\pL\pM .'’-]{0,79}?)\s*[,，:：]?\s*(?:你好|您好|嗨)`)
	communicationGreetingStart   = regexp.MustCompile(`(?i)^\s*(?:hi|hello|dear|hey|good\s+(?:morning|afternoon|evening))\b|^\s*(?:你好|您好|嗨|早上好|晚上好)`)
)

func communicationGreeting(text string) (bool, string) {
	recipient := ""
	if match := communicationEnglishGreeting.FindStringSubmatch(text); len(match) == 2 {
		recipient = strings.TrimSpace(match[1])
	}
	if match := communicationChineseGreeting.FindStringSubmatch(text); len(match) == 2 && recipient == "" {
		recipient = strings.TrimSpace(match[1])
	}
	switch strings.ToLower(recipient) {
	case "there", "team", "everyone", "all", "大家", "各位":
		recipient = ""
	}
	greeting := communicationGreetingStart.MatchString(text) || recipient != ""
	if recipient == "" {
		if match := regexp.MustCompile(`^[\pL\pM][\pL\pM .'’-]{0,79}?[,，:：]?\s*(?:你好|您好|嗨)`).FindString(text); match != "" {
			greeting = true
		}
	}
	return greeting, recipient
}

func okfBoundedInt(value any, upper int) int64 {
	var n int64
	switch typed := value.(type) {
	case int:
		n = int64(typed)
	case int64:
		n = typed
	case uint64:
		if typed > uint64(upper) {
			return int64(upper)
		}
		n = int64(typed)
	case float64:
		n = int64(typed)
	case string:
		n, _ = strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
	default:
		return 0
	}
	if n < 0 {
		return 0
	}
	return min(n, int64(upper))
}

func validCommunicationCategory(category string) bool {
	switch category {
	case "language", "sentences", "greeting", "signoff", "removed_words", "answer_language":
		return true
	default:
		return false
	}
}

func withMemorySentMessageEvidence(meta map[string]any, previous memoryFrontmatter) {
	if previous.CommunicationCategory != "" {
		if _, exists := meta["communication_category"]; !exists {
			meta["communication_category"] = previous.CommunicationCategory
		}
	}
	if previous.SentMessageCount > 0 {
		if _, exists := meta["sent_message_count"]; !exists {
			meta["sent_message_count"] = previous.SentMessageCount
		}
	}
	for key, at := range map[string]int64{"evidence_first_at": previous.EvidenceFirstAt, "evidence_last_at": previous.EvidenceLastAt} {
		if at > 0 {
			if _, exists := meta[key]; !exists {
				meta[key] = time.UnixMilli(at).UTC().Format(time.RFC3339)
			}
		}
	}
	if len(previous.EvidenceMessageIDs) > 0 {
		if _, exists := meta["evidence_message_ids"]; !exists {
			meta["evidence_message_ids"] = previous.EvidenceMessageIDs
		}
	}
}

func communicationMemoryID(app, category, value string) string {
	digest := sha256.Sum256([]byte(app + "\x00" + category + "\x00" + value))
	return fmt.Sprintf("communication-%x", digest[:8])
}

func (a *App) observeCommunicationMessage(message communicationMessage) error {
	for _, observation := range communicationObservations(message) {
		if _, err := a.saveCommunicationObservation(message, observation); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) saveCommunicationObservation(message communicationMessage, observation communicationObservation) (bool, error) {
	id := communicationMemoryID(message.App, observation.Category, observation.Value)
	dir, err := memoryDir()
	if err != nil {
		return false, err
	}
	memoryMutationMu.RLock()
	defer memoryMutationMu.RUnlock()
	unlock := memoryFileLocks.Acquire(id)
	defer unlock()

	path := filepath.Join(dir, id+".md")
	current, readErr := os.ReadFile(path)
	if readErr != nil && !os.IsNotExist(readErr) {
		return false, readErr
	}
	isNew := os.IsNotExist(readErr)
	previous := parseMemoryFrontmatter(string(current))
	evidenceID := strings.TrimSpace(message.Source + ":" + message.MessageID)
	if message.MessageID != "" && len(evidenceID) <= 512 {
		for _, existing := range previous.EvidenceMessageIDs {
			if existing == evidenceID {
				return false, nil
			}
		}
	} else {
		evidenceID = ""
	}
	if !isNew && previous.EvidenceLastAt > 0 && message.SentAt.UnixMilli() <= previous.EvidenceLastAt {
		return false, nil
	}

	var doc okfDocument
	if isNew {
		source := memorySourceConversation
		switch message.Source {
		case "slack":
			source = memorySourceSlack
		case "wechat":
			source = memorySourceWeChat
		}
		content := buildOKFMemoryMarkdown(id, observation.Text, message.SentAt, source, "user_insight", []string{observation.Text})
		doc, _ = parseOKFDocument(content)
		doc.Frontmatter["area"] = "communication"
		doc.Frontmatter["apps"] = []string{message.App}
		doc.Frontmatter["communication_category"] = observation.Category
	} else {
		var ok bool
		doc, ok = parseOKFDocument(string(current))
		if !ok {
			return false, fmt.Errorf("communication memory %s is not OKF", id)
		}
	}
	doc.Frontmatter["sent_message_count"] = min(previous.SentMessageCount+1, communicationMaxSentMessageCount)
	first := message.SentAt.UnixMilli()
	if previous.EvidenceFirstAt > 0 {
		first = min(first, previous.EvidenceFirstAt)
	}
	last := max(message.SentAt.UnixMilli(), previous.EvidenceLastAt)
	doc.Frontmatter["evidence_first_at"] = time.UnixMilli(first).UTC().Format(time.RFC3339)
	doc.Frontmatter["evidence_last_at"] = time.UnixMilli(last).UTC().Format(time.RFC3339)
	ids := append([]string(nil), previous.EvidenceMessageIDs...)
	if evidenceID != "" {
		ids = append(ids, evidenceID)
	}
	if len(ids) > communicationMaxEvidenceIDs {
		ids = ids[len(ids)-communicationMaxEvidenceIDs:]
	}
	if len(ids) > 0 {
		doc.Frontmatter["evidence_message_ids"] = ids
	}
	content := renderOKFDocument(doc.Frontmatter, doc.Body)
	if err := validateMemoryMetadata(content); err != nil {
		return false, err
	}
	if err := checkMemoryWriteAllowed(dir, id, content, isNew); err != nil {
		return false, err
	}
	if !isNew {
		if err := snapshotMemoryFile(dir, id); err != nil {
			return false, err
		}
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		return false, err
	}
	a.markMemoryDirty("communication-observation")
	return true, nil
}

func (a *App) observeSlackCommunicationMessages(workDir, channelID, ownerID string) {
	paths := []string{filepath.Join(workDir, "raw", channelID+".jsonl")}
	threadDir := filepath.Join(workDir, "threads", channelID)
	if entries, err := os.ReadDir(threadDir); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".jsonl") {
				paths = append(paths, filepath.Join(threadDir, entry.Name()))
			}
		}
	}
	for _, path := range paths {
		for _, message := range readJSONL(path) {
			if message.SubType != "" {
				continue
			}
			_ = a.observeCommunicationMessage(communicationMessage{Source: "slack", App: "Slack", MessageID: channelID + ":" + message.TS, AuthorID: message.User, OwnerID: ownerID, Text: message.Text, SentAt: slackMessageTime(message.TS), BodyAvailable: true})
		}
	}
}
