package main

import (
	"context"
	"encoding/json"
	"os"
	"sort"
	"strings"
	"time"

	"wails-gui/internal/engagement"
	"wails-gui/internal/proactivevoice"
)

const (
	communicationDraftExampleLimit     = 20
	communicationDraftExampleScanLimit = 500
)

type communicationBeliefSource struct {
	ID   string
	Text string
}

type mainChatCommunicationDraft struct {
	BeliefIDs     []string `json:"beliefIds"`
	RecipientName string   `json:"recipientName"`
	Language      string   `json:"language"`
	Sentences     int      `json:"sentences"`
	Greeting      bool     `json:"greeting"`
}

type CommunicationDraftExample struct {
	ID            string   `json:"id"`
	To            string   `json:"to"`
	Subject       string   `json:"subject"`
	Text          string   `json:"text"`
	App           string   `json:"app"`
	RecipientName string   `json:"recipientName"`
	Language      string   `json:"language"`
	Sentences     int      `json:"sentences"`
	Greeting      bool     `json:"greeting"`
	BeliefIDs     []string `json:"beliefIds"`
	CreatedAt     int64    `json:"createdAt"`
}

func (a *App) loadCommunicationBeliefs(ctx context.Context) []communicationBeliefSource {
	allowRead, err := a.memoryReadFilter()
	if err != nil {
		return nil
	}
	entries, err := a.GetMemoryEntries()
	if err != nil {
		return nil
	}
	dir, err := memoryDir()
	if err != nil {
		return nil
	}
	beliefs := make([]communicationBeliefSource, 0, 6)
	for _, entry := range entries {
		fm := parseMemoryFrontmatter(entry.Content)
		if entry.Area != "communication" || !validCommunicationCategory(fm.CommunicationCategory) || (memoryConfirmedAt(entry.Content) == 0 && !strings.EqualFold(entry.Source, "user_edit")) || !memoryInUse(entry.Content) || !allowRead(entry.ID, entry.Content) {
			continue
		}
		forgotten, err := a.memoryForgottenNow(dir, entry.ID, entry.Content, time.Now())
		if err != nil || forgotten || !a.proactiveVoiceMarkdownDependencies(ctx, "memory", entry.ID, entry.Content) {
			continue
		}
		text := communicationBeliefText(entry.Content)
		if text == "" {
			continue
		}
		beliefs = append(beliefs, communicationBeliefSource{ID: entry.ID, Text: text})
	}
	sort.Slice(beliefs, func(i, j int) bool { return beliefs[i].ID < beliefs[j].ID })
	if len(beliefs) > 6 {
		beliefs = beliefs[:6]
	}
	return beliefs
}

func communicationBeliefText(content string) string {
	body := memoryConfirmationBody(content)
	lines := strings.Split(body, "\n")
	for index, line := range lines {
		lines[index] = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- "))
	}
	return strings.TrimSpace(strings.Join(lines, " "))
}

func communicationDraftProvenance(job proactivevoice.Job, result proactivevoice.Output) *mainChatCommunicationDraft {
	if result.Draft == nil || len(job.CommunicationBeliefs) == 0 {
		return nil
	}
	ids := make([]string, 0, len(job.CommunicationBeliefs))
	for _, belief := range job.CommunicationBeliefs {
		ids = append(ids, belief.ID)
	}
	greeting, recipient := communicationGreeting(result.Draft.Text)
	if recipient == "" && len(job.People) > 0 && job.People[0].Name != nil {
		recipient = *job.People[0].Name
	}
	return &mainChatCommunicationDraft{
		BeliefIDs:     ids,
		RecipientName: recipient,
		Language:      communicationLanguage(result.Draft.Text),
		Sentences:     communicationSentenceCount(result.Draft.Text),
		Greeting:      greeting,
	}
}

func (a *App) GetCommunicationDraftExamples(memoryID string) ([]CommunicationDraftExample, error) {
	out := []CommunicationDraftExample{}
	if a == nil || a.store == nil || strings.TrimSpace(memoryID) == "" || a.IsClosedEyesModeEnabled() {
		return out, nil
	}
	ctx := context.Background()
	entry, err := a.GetMemoryEntry(memoryID)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	allowRead, err := a.memoryReadFilter()
	if err != nil {
		return nil, err
	}
	if !allowRead(entry.ID, entry.Content) {
		return out, nil
	}
	dir, err := memoryDir()
	if err != nil {
		return nil, err
	}
	forgotten, err := isMemoryForgotten(dir, entry.ID, entry.Content, time.Now())
	if err != nil {
		return nil, err
	}
	if forgotten {
		return out, nil
	}
	main, err := a.store.LoadMainSession(ctx)
	if err != nil {
		return nil, err
	}
	if main == nil || main.Hidden {
		return out, nil
	}
	page, err := a.store.LoadStreamItemsLatest(ctx, main.LocalID, communicationDraftExampleScanLimit)
	if err != nil {
		return nil, err
	}
	for index := len(page.Items) - 1; index >= 0 && len(out) < communicationDraftExampleLimit; index-- {
		row := page.Items[index]
		if row.ToolName != mainChatProactiveTool {
			continue
		}
		var item mainChatProactive
		if json.Unmarshal([]byte(row.ToolInput), &item) != nil {
			continue
		}
		a.projectProactiveVoiceItem(ctx, &item, a.currentUILanguage())
		out = appendCommunicationDraftExamples(out, row.ID, row.CreatedAt, item, memoryID)
	}
	return out, nil
}

func appendCommunicationDraftExamples(out []CommunicationDraftExample, rowID string, createdAt int64, item mainChatProactive, memoryID string) []CommunicationDraftExample {
	if len(out) >= communicationDraftExampleLimit {
		return out
	}
	if item.Kind == string(engagement.KindWaitingOnReply) && item.Draft && item.DraftContent != nil && item.DraftContent.Communication != nil {
		communication := item.DraftContent.Communication
		for _, id := range communication.BeliefIDs {
			if id != memoryID {
				continue
			}
			out = append(out, CommunicationDraftExample{
				ID: rowID, To: item.DraftContent.To, Subject: item.DraftContent.Subject, Text: item.DraftContent.Text, App: "Mail",
				RecipientName: communication.RecipientName, Language: communication.Language, Sentences: communication.Sentences,
				Greeting: communication.Greeting, BeliefIDs: append([]string(nil), communication.BeliefIDs...), CreatedAt: createdAt,
			})
			break
		}
	}
	for _, child := range item.Items {
		out = appendCommunicationDraftExamples(out, rowID+":"+child.MomentID, createdAt, child, memoryID)
	}
	return out
}
