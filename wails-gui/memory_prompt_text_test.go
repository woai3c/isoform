package main

import (
	"strings"
	"testing"
	"time"
)

// Y42: prompt and template text is never a belief or its detail.
func TestMemoryTextIsPromptOrTemplate(t *testing.T) {
	prompts := []string{
		"You write 1 sentence(s) in sent messages.",
		"Writes 1 sentence(s) in sent messages.",
		"Output-format note: the task asks for ONLY the translated Markdown",
		"Reply with one JSON object only, in this shape",
		"Return only JSON",
		"Hello {company}, here is the update",
		"Rules: keep the answer short",
	}
	for _, text := range prompts {
		if !memoryTextIsPromptOrTemplate(text) {
			t.Fatalf("%q should read as a prompt or template", text)
		}
	}
	beliefs := []string{
		"Writes 1 sentence in sent messages.",
		"Writes 3 sentences in sent messages.",
		"Uses design reviews before merging",
		"You're careful before sending alerts to outside teams",
		"Preferences for short release notes",
	}
	for _, text := range beliefs {
		if memoryTextIsPromptOrTemplate(text) {
			t.Fatalf("%q is a real belief and must not be screened out", text)
		}
	}
}

func TestDropPromptMemoryPointsKeepsOnlyBeliefText(t *testing.T) {
	resp := mcMemoryGenerationResponse{
		Title: "Caution before sending messages to external people",
		MemoryPoints: []string{
			"Output-format note: the task asks for ONLY the translated Markdown",
			"Checks the outgoing draft before it leaves",
		},
	}
	dropPromptMemoryPoints(&resp)
	if len(resp.MemoryPoints) != 1 || resp.MemoryPoints[0] != "Checks the outgoing draft before it leaves" {
		t.Fatalf("points = %q, want only the belief", resp.MemoryPoints)
	}

	promptTitle := mcMemoryGenerationResponse{
		Title:        "Output-format note for the translation task",
		MemoryPoints: []string{"Checks the outgoing draft before it leaves"},
	}
	dropPromptMemoryPoints(&promptTitle)
	if len(promptTitle.MemoryPoints) != 0 {
		t.Fatalf("a prompt title must drop every point, got %q", promptTitle.MemoryPoints)
	}
	dropPromptMemoryPoints(nil)
}

func TestCommunicationSentenceObservationSpellsTheCount(t *testing.T) {
	for text, want := range map[string]string{
		"Sounds good.":                 "Writes 1 sentence in sent messages.",
		"Sounds good. Talk soon.":      "Writes 2 sentences in sent messages.",
		"One. Two. Three. Four. Five.": "Writes 5 or more sentences in sent messages.",
	} {
		message := communicationMessage{Source: "isoform_chat", App: "Isoform", MessageID: "m1", AuthorID: "me", OwnerID: "me", Text: text, SentAt: time.Now(), BodyAvailable: true}
		var got string
		for _, observation := range communicationObservations(message) {
			if observation.Category == "sentences" {
				got = observation.Text
			}
		}
		if got != want {
			t.Fatalf("text %q: sentence observation = %q, want %q", text, got, want)
		}
		if strings.Contains(got, "(s)") {
			t.Fatalf("sentence observation still has a placeholder: %q", got)
		}
	}
}

func TestAssistantHistoryDropsPromptBeliefs(t *testing.T) {
	at := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	sessions := []assistantHistorySession{{Assistant: "claude", At: at}}
	reply := assistantHistoryReply{Beliefs: []assistantHistoryReplyBelief{
		{Text: "Output-format note: the task asks for ONLY the translated Markdown", Session: 1, Sessions: []int{1}},
		{Text: "Runs the tests before every push", Session: 1, Sessions: []int{1}},
	}}
	update := buildAssistantHistoryUpdate(reply, sessions, "", time.Time{})
	if len(update.Beliefs) != 1 || update.Beliefs[0].Text != "Runs the tests before every push" {
		t.Fatalf("beliefs = %+v, want only the working belief", update.Beliefs)
	}
}

func TestMemoryEntryIsPromptTextHidesTaintedEntries(t *testing.T) {
	tainted := "---\ntitle: x\n---\n\n# Caution before sending messages\n\n## Memory Points\n\n- Output-format note: the task asks for ONLY the translated Markdown\n"
	if !memoryEntryIsPromptText("Caution before sending messages", tainted) {
		t.Fatal("an entry with a prompt point must be hidden")
	}
	clean := "# Runs tests\n\n## Memory Points\n\n- Runs the tests before every push\n"
	if memoryEntryIsPromptText("Runs tests", clean) {
		t.Fatal("a clean entry must stay visible")
	}
}
