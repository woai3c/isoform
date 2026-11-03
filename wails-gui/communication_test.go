package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"wails-gui/internal/engagement"
	"wails-gui/internal/proactivevoice"
	"wails-gui/storage"
)

func TestCommunicationObservationsRequireOwnAvailableBodies(t *testing.T) {
	base := communicationMessage{Source: "slack", App: "Slack", AuthorID: "owner", OwnerID: "owner", Text: "Hi Dana,\nThanks. Bye.", SentAt: time.UnixMilli(1000), BodyAvailable: true}
	observations := communicationObservations(base)
	if len(observations) != 3 {
		t.Fatalf("own message observations = %+v", observations)
	}
	for _, observation := range observations {
		if strings.Contains(strings.ToLower(observation.Text), "tone") || strings.Contains(strings.ToLower(observation.Text), "persona") {
			t.Fatalf("non-factual category admitted: %+v", observation)
		}
	}
	for _, change := range []func(*communicationMessage){
		func(message *communicationMessage) { message.AuthorID = "someone-else" },
		func(message *communicationMessage) { message.AuthorID = "" },
		func(message *communicationMessage) { message.BodyAvailable = false },
		func(message *communicationMessage) { message.Source = "mail" },
		func(message *communicationMessage) { message.Text = "" },
	} {
		message := base
		change(&message)
		if got := communicationObservations(message); len(got) != 0 {
			t.Fatalf("ineligible message admitted: %+v %+v", message, got)
		}
	}
	greeting, recipient := communicationGreeting(base.Text)
	if !greeting || recipient != "Dana" || communicationSentenceCount(base.Text) != 3 {
		t.Fatalf("draft facts greeting=%v recipient=%q sentences=%d", greeting, recipient, communicationSentenceCount(base.Text))
	}
}

func TestCommunicationEvidenceRoundTripAndPreservation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app := &App{}
	content := buildOKFMemoryMarkdown("belief", "Sent messages", time.UnixMilli(1000), memorySourceConversation, "user_insight", []string{"Writes sent messages in English."})
	doc, _ := parseOKFDocument(content)
	doc.Frontmatter["area"] = "communication"
	doc.Frontmatter["communication_category"] = "language"
	doc.Frontmatter["sent_message_count"] = 2
	doc.Frontmatter["evidence_first_at"] = "2026-10-01T10:00:00Z"
	doc.Frontmatter["evidence_last_at"] = "2026-10-02T10:00:00Z"
	doc.Frontmatter["evidence_message_ids"] = []string{"slack:one"}
	saved, err := app.saveMemoryEntry("belief", renderOKFDocument(doc.Frontmatter, doc.Body), false)
	if err != nil {
		t.Fatal(err)
	}
	doc, _ = parseOKFDocument(saved.Content)
	delete(doc.Frontmatter, "communication_category")
	delete(doc.Frontmatter, "sent_message_count")
	delete(doc.Frontmatter, "evidence_first_at")
	delete(doc.Frontmatter, "evidence_last_at")
	delete(doc.Frontmatter, "evidence_message_ids")
	machine, err := app.saveMemoryEntry("belief", renderOKFDocument(doc.Frontmatter, doc.Body), false)
	if err != nil {
		t.Fatal(err)
	}
	fm := parseMemoryFrontmatter(machine.Content)
	if fm.CommunicationCategory != "language" || fm.SentMessageCount != 2 || fm.EvidenceFirstAt != parseOKFTimestampMs("2026-10-01T10:00:00Z") || fm.EvidenceLastAt != parseOKFTimestampMs("2026-10-02T10:00:00Z") || len(fm.EvidenceMessageIDs) != 1 {
		t.Fatalf("machine rewrite lost evidence: %+v", fm)
	}
	edited, err := app.SaveMemoryEntry("belief", "# Corrected\n\nWrites short sent messages.")
	if err != nil {
		t.Fatal(err)
	}
	fm = parseMemoryFrontmatter(edited.Content)
	if fm.CommunicationCategory != "language" || fm.SentMessageCount != 2 || fm.EvidenceFirstAt == 0 || fm.EvidenceLastAt == 0 {
		t.Fatalf("user edit lost evidence: %+v", fm)
	}
	view, err := app.GetAboutYou()
	if err != nil || len(view.Beliefs) != 1 {
		t.Fatalf("about you: %+v %v", view, err)
	}
	belief := view.Beliefs[0]
	if belief.SentMessageCount != 2 || belief.EvidenceFirstAt != fm.EvidenceFirstAt || belief.EvidenceLastAt != fm.EvidenceLastAt || belief.SessionCount != 0 {
		t.Fatalf("evidence projection mixed sessions and messages: %+v", belief)
	}
}

func TestCommunicationObservationPersistsBoundedDeduplicatedEvidence(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app := &App{}
	message := communicationMessage{Source: "wechat", App: "WeChat", MessageID: "message-1", AuthorID: "owner", OwnerID: "owner", Text: "你好。谢谢。", SentAt: time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC), BodyAvailable: true}
	for range 2 {
		if err := app.observeCommunicationMessage(message); err != nil {
			t.Fatal(err)
		}
	}
	id := communicationMemoryID("WeChat", "language", "zh-CN")
	entry, err := app.GetMemoryEntry(id)
	if err != nil {
		t.Fatal(err)
	}
	fm := parseMemoryFrontmatter(entry.Content)
	if fm.SentMessageCount != 1 || fm.EvidenceFirstAt != message.SentAt.UnixMilli() || fm.EvidenceLastAt != message.SentAt.UnixMilli() || len(fm.EvidenceMessageIDs) != 1 || entry.Area != "communication" {
		t.Fatalf("stored evidence = %+v area=%q", fm, entry.Area)
	}
}

func TestCommunicationObservationPreservesCorrectionAndConfirmation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app := &App{}
	base := time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
	message := communicationMessage{Source: "slack", App: "Slack", MessageID: "first", AuthorID: "owner", OwnerID: "owner", Text: "Thanks.", SentAt: base, BodyAvailable: true}
	if err := app.observeCommunicationMessage(message); err != nil {
		t.Fatal(err)
	}
	id := communicationMemoryID("Slack", "language", "en")
	edited, err := app.SaveMemoryEntry(id, "# My wording\n\nKeep this corrected belief exactly.")
	if err != nil {
		t.Fatal(err)
	}
	before, ok := parseOKFDocument(edited.Content)
	if !ok {
		t.Fatal("corrected memory is not OKF")
	}
	confirmedAt := memoryConfirmedAt(edited.Content)
	message.MessageID, message.SentAt = "second", base.Add(time.Minute)
	if err := app.observeCommunicationMessage(message); err != nil {
		t.Fatal(err)
	}
	current, err := app.GetMemoryEntry(id)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := parseOKFDocument(current.Content)
	if after.Body != before.Body || memoryConfirmedAt(current.Content) != confirmedAt || parseMemoryFrontmatter(current.Content).SentMessageCount != 2 {
		t.Fatalf("observation replaced correction or confirmation: before=%+v after=%+v", before, after)
	}
	for _, key := range []string{"sent_message_count", "evidence_first_at", "evidence_last_at", "evidence_message_ids"} {
		delete(before.Frontmatter, key)
		delete(after.Frontmatter, key)
	}
	if !reflect.DeepEqual(after.Frontmatter, before.Frontmatter) {
		t.Fatalf("observation changed non-evidence frontmatter: before=%+v after=%+v", before.Frontmatter, after.Frontmatter)
	}
}

func TestCommunicationObservationReplayAfterEvidenceIDTruncation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app := &App{}
	base := time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
	observation := communicationObservation{Category: "language", Value: "en", Text: "Writes sent messages in English."}
	message := communicationMessage{Source: "slack", App: "Slack", AuthorID: "owner", OwnerID: "owner", Text: "Thanks.", BodyAvailable: true}
	first := communicationMessage{}
	for index := range communicationMaxEvidenceIDs + 44 {
		message.MessageID = fmt.Sprintf("message-%d", index)
		message.SentAt = base.Add(time.Duration(index) * time.Millisecond)
		if index == 0 {
			first = message
		}
		updated, err := app.saveCommunicationObservation(message, observation)
		if err != nil || !updated {
			t.Fatalf("observation %d updated=%v err=%v", index, updated, err)
		}
	}
	id := communicationMemoryID("Slack", "language", "en")
	entry, err := app.GetMemoryEntry(id)
	if err != nil {
		t.Fatal(err)
	}
	before := parseMemoryFrontmatter(entry.Content)
	if before.SentMessageCount != communicationMaxEvidenceIDs+44 || len(before.EvidenceMessageIDs) != communicationMaxEvidenceIDs {
		t.Fatalf("unexpected bounded evidence: %+v", before)
	}
	updated, err := app.saveCommunicationObservation(first, observation)
	if err != nil || updated {
		t.Fatalf("evicted replay updated=%v err=%v", updated, err)
	}
	entry, err = app.GetMemoryEntry(id)
	if err != nil {
		t.Fatal(err)
	}
	after := parseMemoryFrontmatter(entry.Content)
	if after.SentMessageCount != before.SentMessageCount || after.EvidenceLastAt != before.EvidenceLastAt || len(after.EvidenceMessageIDs) != communicationMaxEvidenceIDs {
		t.Fatalf("replayed old message inflated evidence: before=%+v after=%+v", before, after)
	}
}

func TestCommunicationObservationConcurrentUpdatesDoNotLoseAcceptedCounts(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app := &App{}
	base := time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
	observation := communicationObservation{Category: "language", Value: "en", Text: "Writes sent messages in English."}
	start := make(chan struct{})
	errs := make(chan error, 64)
	var accepted atomic.Int64
	var wg sync.WaitGroup
	for index := range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			message := communicationMessage{Source: "slack", App: "Slack", MessageID: fmt.Sprintf("message-%d", index), AuthorID: "owner", OwnerID: "owner", Text: "Thanks.", SentAt: base.Add(time.Duration(index) * time.Millisecond), BodyAvailable: true}
			updated, err := app.saveCommunicationObservation(message, observation)
			if err != nil {
				errs <- err
				return
			}
			if updated {
				accepted.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	id := communicationMemoryID("Slack", "language", "en")
	entry, err := app.GetMemoryEntry(id)
	if err != nil {
		t.Fatal(err)
	}
	fm := parseMemoryFrontmatter(entry.Content)
	if accepted.Load() == 0 || fm.SentMessageCount != int(accepted.Load()) || len(fm.EvidenceMessageIDs) != int(accepted.Load()) {
		t.Fatalf("accepted=%d count=%d ids=%d", accepted.Load(), fm.SentMessageCount, len(fm.EvidenceMessageIDs))
	}
}

func TestCommunicationObservationCannotRecreateForgottenDeterministicID(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app := mainChatTestApp(t)
	message := communicationMessage{Source: "wechat", App: "WeChat", MessageID: "forgotten-message", AuthorID: "owner", OwnerID: "owner", Text: "你好。", SentAt: time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC), BodyAvailable: true}
	if err := app.observeCommunicationMessage(message); err != nil {
		t.Fatal(err)
	}
	id := communicationMemoryID("WeChat", "language", "zh-CN")
	preview, err := app.GetMemoryForgetPreview(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.ForgetMemoryEntry(id, preview.ConfirmationToken); err != nil {
		t.Fatal(err)
	}
	if err := app.observeCommunicationMessage(message); !errors.Is(err, errMemoryForgotten) {
		t.Fatalf("forgotten deterministic ID was recreated: %v", err)
	}
	if _, err := app.GetMemoryEntry(id); !os.IsNotExist(err) {
		t.Fatalf("forgotten memory exists again: %v", err)
	}
}

func TestCommunicationDraftLoaderRequiresCurrentConfirmedReadableBeliefs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app := mainChatTestApp(t)
	makeBelief := func(id, body string) {
		t.Helper()
		content := buildOKFMemoryMarkdown(id, body, time.Now(), memorySourceConversation, "user_insight", []string{body})
		doc, _ := parseOKFDocument(content)
		doc.Frontmatter["area"] = "communication"
		doc.Frontmatter["communication_category"] = "language"
		if _, err := app.saveMemoryEntry(id, renderOKFDocument(doc.Frontmatter, doc.Body), false); err != nil {
			t.Fatal(err)
		}
	}
	makeBelief("confirmed", "Writes sent messages in English.")
	makeBelief("unconfirmed", "Writes sent messages in Chinese.")
	makeBelief("forgotten", "Starts sent messages with a greeting.")
	tone := buildOKFMemoryMarkdown("tone", "You are warm and personable.", time.Now(), memorySourceConversation, "user_insight", []string{"You are warm and personable."})
	toneDoc, _ := parseOKFDocument(tone)
	toneDoc.Frontmatter["area"] = "communication"
	if _, err := app.saveMemoryEntry("tone", renderOKFDocument(toneDoc.Frontmatter, toneDoc.Body), false); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"confirmed", "forgotten", "tone"} {
		if _, err := app.ConfirmMemoryEntry(id); err != nil {
			t.Fatal(err)
		}
	}
	if err := app.DeleteMemoryEntry("forgotten"); err != nil {
		t.Fatal(err)
	}
	beliefs := app.loadCommunicationBeliefs(t.Context())
	if len(beliefs) != 1 || beliefs[0].ID != "confirmed" {
		t.Fatalf("eligible beliefs = %+v", beliefs)
	}
	entry, err := app.GetMemoryEntry("confirmed")
	if err != nil {
		t.Fatal(err)
	}
	doc, _ := parseOKFDocument(entry.Content)
	doc.Body = "## Memory Points\n\n- Writes two sentences in sent messages.\n"
	if _, err := app.saveMemoryEntry("confirmed", renderOKFDocument(doc.Frontmatter, doc.Body), false); err != nil {
		t.Fatal(err)
	}
	if got := app.loadCommunicationBeliefs(t.Context()); len(got) != 0 {
		t.Fatalf("corrected unconfirmed belief remained eligible: %+v", got)
	}
}

func TestCommunicationDraftProvenanceDeterministicAndHashed(t *testing.T) {
	job := proactivevoice.Job{Kind: "waiting_on_reply", Locale: "en", DraftTo: "addr_1", CommunicationBeliefs: []proactivevoice.CommunicationBelief{{ID: "belief-b", Text: "Writes in English."}, {ID: "belief-a", Text: "Uses greetings."}}}
	result := proactivevoice.Output{Draft: &proactivevoice.Draft{To: "addr_1", Subject: "Re: Plan", Text: "Hi Dana,\nThanks. Bye."}}
	first := communicationDraftProvenance(job, result)
	second := communicationDraftProvenance(job, result)
	if !reflect.DeepEqual(first, second) || first == nil || first.RecipientName != "Dana" || first.Language != "en" || first.Sentences != 3 || !first.Greeting || !reflect.DeepEqual(first.BeliefIDs, []string{"belief-b", "belief-a"}) {
		t.Fatalf("provenance = %+v and %+v", first, second)
	}
	item := mainChatProactive{Kind: "waiting_on_reply"}
	if !applyProactiveVoiceResult(&item, job, result, map[string]string{"addr_1": "dana@example.com"}) || item.DraftContent == nil || !reflect.DeepEqual(item.DraftContent.Communication, first) {
		t.Fatalf("applied draft = %+v", item.DraftContent)
	}
	before := proactiveVoicePresentationHash(item)
	changed := item
	copyDraft := *item.DraftContent
	copyCommunication := *first
	copyCommunication.Greeting = false
	copyDraft.Communication = &copyCommunication
	changed.DraftContent = &copyDraft
	if proactiveVoicePresentationHash(changed) == before {
		t.Fatal("communication provenance is outside the presentation hash")
	}
	withdrawProactiveVoice(&item, "en")
	if item.DraftContent != nil || item.Draft {
		t.Fatal("withdrawal retained communication provenance")
	}
}

func TestCommunicationDraftExamplesFilterCurrentReadableDrafts(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app := mainChatTestApp(t)
	main, err := app.ensureMainChat(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	memory := buildOKFMemoryMarkdown("belief", "Writes in English.", time.Now(), memorySourceConversation, "user_insight", []string{"Writes in English."})
	if _, err := app.SaveMemoryEntry("belief", memory); err != nil {
		t.Fatal(err)
	}
	draft := mainChatProactive{MomentID: "mail-1", Kind: string(engagement.KindWaitingOnReply), Draft: true, DraftContent: &mainChatProactiveDraft{
		To: "dana@example.com", Subject: "Re: Plan", Text: "Hi Dana,\nThanks.",
		Communication: &mainChatCommunicationDraft{BeliefIDs: []string{"belief", "other"}, RecipientName: "Dana", Language: "en", Sentences: 2, Greeting: true},
	}}
	projected := appendCommunicationDraftExamples(nil, "row", 1234, draft, "belief")
	if len(projected) != 1 || projected[0].App != "Mail" || projected[0].CreatedAt != 1234 || projected[0].RecipientName != "Dana" {
		t.Fatalf("positive projection = %+v", projected)
	}
	if got := appendCommunicationDraftExamples(nil, "row", 1234, draft, "missing"); len(got) != 0 {
		t.Fatalf("unrelated memory matched: %+v", got)
	}
	payload, err := json.Marshal(draft)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.store.AppendStreamItem(t.Context(), main.LocalID, storage.StreamItemRow{ID: "legacy", Kind: "system", ToolName: mainChatProactiveTool, ToolInput: string(payload), CreatedAt: 1234}); err != nil {
		t.Fatal(err)
	}
	examples, err := app.GetCommunicationDraftExamples("belief")
	if err != nil || len(examples) != 0 {
		t.Fatalf("legacy draft without current provenance escaped: %+v %v", examples, err)
	}
	if err := app.DeleteMemoryEntry("belief"); err != nil {
		t.Fatal(err)
	}
	examples, err = app.GetCommunicationDraftExamples("belief")
	if err != nil || len(examples) != 0 {
		t.Fatalf("forgotten memory retained examples: %+v %v", examples, err)
	}
}
