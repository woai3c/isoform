package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/handoff"
)

func TestScheduledMorningUsesRankedPipeline(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	svc, _ := newTestMobileBriefService(t, &fakeMobileBriefClock{now: now})
	svc.app.mobileBriefSvc = svc
	svc.compose = nil
	work, err := handoff.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	svc.app.handoffStore = work
	entry := handoff.Entry{ID: "review-plan", Title: "Review the plan", Source: handoff.SourceMobile, Status: handoff.StatusNeedsReview, CreatedAt: now.Add(-time.Hour).UnixMilli(), UpdatedAt: now.Add(-time.Minute).UnixMilli()}
	if _, err := work.Save(entry); err != nil {
		t.Fatal(err)
	}
	// today-review v1.1.4 #4: the closing question's item gets no chip. The
	// brief leads with and closes on the invoice, so the plan keeps its chip.
	invoice := handoff.Entry{ID: "send-invoice", Title: "Send the invoice", Source: handoff.SourceMobile, Status: handoff.StatusNeedsReview, CreatedAt: now.Add(-2 * time.Hour).UnixMilli(), UpdatedAt: now.Add(-2 * time.Minute).UnixMilli()}
	if _, err := work.Save(invoice); err != nil {
		t.Fatal(err)
	}
	svc.gather = func(_ context.Context, slot string, start, asOf time.Time) (mobileBriefInput, error) {
		input := mobileBriefInput{Slot: slot, WindowStart: start, AsOf: asOf, Location: time.UTC}
		collectMobileBriefWork(svc.app, &input)
		return input, nil
	}
	id := mobileBriefItemKey("candidate", "history", entry.ID)
	invoiceID := mobileBriefItemKey("candidate", "history", invoice.ID)
	scores := morningBriefScores{Items: []morningBriefScore{{ID: id, Critique: "The review awaits your decision.", Score: .8, Pick: "need"}, {ID: invoiceID, Critique: "The invoice awaits your review.", Score: 1, Pick: "need"}}, LeadID: invoiceID,
		Openings: []morningBriefOpening{{Text: "Send the invoice because it awaits your review.", Probability: .9}, {Text: "Review the invoice because it awaits sending.", Probability: .8}, {Text: "Send the invoice because your review is pending.", Probability: .7}}}
	rawScores, err := json.Marshal(scores)
	if err != nil {
		t.Fatal(err)
	}
	writes, judges := 0, 0
	svc.modelCall = func(_ context.Context, prompt string) (string, error) {
		switch true {
		case strings.Contains(prompt, "chief of staff's first pass"):
			return string(rawScores), nil
		case strings.Contains(prompt, "You write the user's morning brief"):
			writes++
			if writes == 2 && !strings.Contains(prompt, "Make the reason clearer") {
				t.Fatal("revision did not receive the judge critique")
			}
			reply, err := json.Marshal(morningBriefDraftReply{Paragraphs: []morningBriefDraftParagraph{
				{Text: "Send the invoice because your review is pending.", Items: []string{invoiceID}},
				{Text: "Review the plan because your decision is pending.", Items: []string{id}, Chip: &mobileBriefCardWords{Label: "Review plan", Message: "Review the plan with me."}},
				{Text: "Want to send the invoice?", Items: []string{invoiceID}},
			}, Answers: []mobileBriefCardWords{{Label: "Send the invoice", Message: "Yes, send the invoice with me."}, {Label: "Not now", Message: "No, leave it."}}})
			return string(reply), err
		case strings.Contains(prompt, "Judge the morning brief"):
			judges++
			if judges == 1 {
				return `{"critique":"Make the reason clearer","pass":false}`, nil
			}
			return `{"critique":"Pass","pass":true}`, nil
		default:
			t.Fatalf("unexpected model call: %s", prompt)
			return "", nil
		}
	}
	var delivered MobileBriefV1
	svc.publish = func(brief MobileBriefV1, _ bool) error { delivered = brief; return nil }
	brief, err := svc.generate(context.Background(), mobileBriefSlotMorning, mobileBriefReasonSchedule)
	if err != nil {
		t.Fatal(err)
	}
	svc.wg.Wait()
	if writes != 2 || judges != 12 || !brief.rankedEdition || brief.modelBinding == nil || len(brief.Narrative) != 3 || len(delivered.Narrative) != 3 {
		t.Fatalf("scheduled brief did not pass through ranked pipeline: writes=%d judges=%d brief=%+v delivered=%+v", writes, judges, brief.Narrative, delivered.Narrative)
	}
	stored, found := svc.store.load(brief.BriefID)
	if !found || !stored.rankedEdition || stored.modelBinding == nil || len(stored.Narrative) != 3 {
		t.Fatal("ranked edition was not stored")
	}
	read := svc.app.mobileBriefWithCards(svc.app.mobileBriefForDelivery(stored))
	if len(read.Narrative) != 3 || read.Narrative[0].Text != "Send the invoice because your review is pending." ||
		read.Narrative[1].Text != "Review the plan because your decision is pending." || read.Narrative[2].Text != "Want to send the invoice?" {
		t.Fatalf("stored paragraphs did not survive delivery: %+v", read.Narrative)
	}
	chips := cardsByKind(read.Cards, mobileBriefCardQuestion)
	answers := cardsByKind(read.Cards, mobileBriefCardAction)
	if len(chips) != 1 || chips[0].Message != "Review the plan with me." || len(answers) != 2 {
		t.Fatalf("prepared chips and answers did not survive delivery: chips=%+v answers=%+v", chips, answers)
	}
}

func TestScheduledMorningWithOnlyAutomationRunsDeliversTheFixedStatement(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	svc, _ := newTestMobileBriefService(t, &fakeMobileBriefClock{now: now})
	svc.app.mobileBriefSvc = svc
	svc.compose = nil
	svc.gather = func(_ context.Context, slot string, start, asOf time.Time) (mobileBriefInput, error) {
		input := mobileBriefInput{Slot: slot, WindowStart: start, AsOf: asOf, Location: time.UTC}
		// The row collectMobileBriefSchedules adds for an automation that ran.
		input.Schedules = []mobileBriefCandidate{{SourceKind: "schedule_run", SourceID: "run-1", Title: "Daily digest", Status: "completed", Kind: "schedule", At: now.Add(-time.Hour)}}
		return input, nil
	}
	svc.modelCall = func(context.Context, string) (string, error) {
		t.Fatal("an empty candidate set must not reach the model")
		return "", nil
	}
	svc.publish = func(MobileBriefV1, bool) error { return nil }
	for attempt := 1; attempt <= 2; attempt++ {
		brief, err := svc.generate(context.Background(), mobileBriefSlotMorning, mobileBriefReasonSchedule)
		if err != nil {
			t.Fatalf("attempt %d failed (%s): %s", attempt, mobileBriefFailureKind(err), safeLogError(err))
		}
		svc.wg.Wait()
		if !mobileBriefStaticEmpty(brief) {
			t.Fatalf("attempt %d narrative = %+v, want the fixed empty statement", attempt, brief.Narrative)
		}
	}
}

func TestScheduledMorningSurvivesAnUnreadableScoreReply(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	svc, _ := newTestMobileBriefService(t, &fakeMobileBriefClock{now: now})
	svc.app.mobileBriefSvc = svc
	svc.compose = nil
	work, err := handoff.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	svc.app.handoffStore = work
	entry := handoff.Entry{ID: "review-plan", Title: "Review the plan", Source: handoff.SourceMobile, Status: handoff.StatusNeedsReview, CreatedAt: now.Add(-time.Hour).UnixMilli(), UpdatedAt: now.Add(-time.Minute).UnixMilli()}
	if _, err := work.Save(entry); err != nil {
		t.Fatal(err)
	}
	svc.gather = func(_ context.Context, slot string, start, asOf time.Time) (mobileBriefInput, error) {
		input := mobileBriefInput{Slot: slot, WindowStart: start, AsOf: asOf, Location: time.UTC}
		collectMobileBriefWork(svc.app, &input)
		return input, nil
	}
	id := mobileBriefItemKey("candidate", "history", entry.ID)
	scores, writes, judges := 0, 0, 0
	svc.modelCall = func(_ context.Context, prompt string) (string, error) {
		switch true {
		case strings.Contains(prompt, "chief of staff's first pass"):
			scores++
			return "Here is my ranking.\n```json\n{\"items\":[{\"id\":\"" + id + "\",\"critique\":\"Awaits you.\"\n```\nHope that helps!", nil
		case strings.Contains(prompt, "You write the user's morning brief"):
			writes++
			reply, err := json.Marshal(morningBriefDraftReply{Paragraphs: []morningBriefDraftParagraph{
				{Text: "Review the plan because your decision is pending.", Items: []string{id}},
				{Text: "Want to review the plan?", Items: []string{id}},
			}, Answers: []mobileBriefCardWords{{Label: "Review the plan", Message: "Yes, review the plan with me."}, {Label: "Leave it", Message: "No, leave it."}}})
			return string(reply), err
		case strings.Contains(prompt, "Judge the morning brief"):
			judges++
			return `{"critique":"Pass","pass":true}`, nil
		default:
			t.Fatalf("unexpected model call: %s", prompt)
			return "", nil
		}
	}
	var logged strings.Builder
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	var delivered MobileBriefV1
	svc.publish = func(brief MobileBriefV1, _ bool) error { delivered = brief; return nil }
	brief, err := svc.generate(context.Background(), mobileBriefSlotMorning, mobileBriefReasonSchedule)
	if err != nil {
		t.Fatalf("the brief failed (%s): %s", mobileBriefFailureKind(err), safeLogError(err))
	}
	svc.wg.Wait()
	if scores != 2 || writes != 1 || judges != len(morningBriefJudgeQuestions) || !brief.rankedEdition || brief.modelBinding == nil || len(brief.Narrative) != 2 || len(delivered.Narrative) != 2 {
		t.Fatalf("scores=%d writes=%d judges=%d brief=%+v delivered=%+v", scores, writes, judges, brief.Narrative, delivered.Narrative)
	}
	if !strings.Contains(logged.String(), "ranked morning scoring fell back to the fixed order: no scores object in reply") || strings.Contains(logged.String(), "Awaits you") {
		t.Fatalf("fallback log = %q", logged.String())
	}
}
