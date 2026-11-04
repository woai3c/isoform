package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/handoff"
)

const morningBriefNoLeadScores = `{"items":[{"id":"visa","critique":"Waiting on you.","score":0.7,"pick":"need"},{"id":"garden","critique":"Past its slip point.","score":0.6,"pick":"slip"},{"id":"rumor","critique":"Not usable evidence.","score":0,"pick":"omit"}]}`

// A real day: something waits on the user without passing the lead gate,
// something slips, something is too uncertain to cite. The scorer validly
// picks no lead, and the brief is still written, in the fixed order, citing
// only selected items.
func TestMorningBriefPipelineWritesPlainOrderWhenScorerPicksNoLead(t *testing.T) {
	items := []morningBriefCandidate{
		{ID: "visa", Title: "Renew Mara Quill's visa form", NeedsUser: true, Confidence: .9},
		{ID: "garden", Title: "Weekly garden plan", Slipping: true, Confidence: .7},
		{ID: "rumor", Title: "Unconfirmed offsite", NeedsUser: true, Confidence: .3},
	}
	var logged strings.Builder
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	scoreCalls, writes, judges := 0, 0, 0
	draft, leadFallback, err := runMorningBriefPipeline(context.Background(), "Bo", "", nil, nil, nil, nil, items,
		func(context.Context) error { return nil },
		func(_ context.Context, prompt string) (string, error) {
			if strings.Contains(prompt, "chief of staff's first pass") {
				scoreCalls++
				return morningBriefNoLeadScores, nil
			}
			judges++
			return `{"critique":"Pass.","pass":true}`, nil
		}, morningBriefFixtureRefs,
		func(context.Context, morningBriefCandidate, morningBriefOpening, func(context.Context, string) (string, error)) (*MobileBriefPara, error) {
			t.Fatal("an unscored brief has no opening to ground")
			return nil, nil
		}, func(_ context.Context, input morningBriefWriterInput, _ string) (morningBriefDraft, error) {
			writes++
			s := input.Selection
			if s.Lead == nil || s.Lead.ID != "visa" || s.Slip == nil || s.Slip.ID != "garden" || len(s.Needs) != 0 || input.Opening.Text != "" {
				t.Fatalf("writer saw unexpected plain-order input: %+v", input)
			}
			if len(s.Lead.Refs) == 0 || len(s.Slip.Refs) == 0 {
				t.Fatalf("selected items reached the writer without refs: %+v", s)
			}
			return morningBriefDraft{Paragraphs: []MobileBriefPara{
				{Text: "Mara Quill's visa form waits on you.", Refs: s.Lead.Refs},
				{Text: "The garden plan is slipping.", Refs: s.Slip.Refs},
				{Text: "Want to start the visa form?", Refs: s.Lead.Refs},
			}}, nil
		})
	if err != nil || leadFallback || scoreCalls != 1 || writes != 1 || judges != len(morningBriefJudgeQuestions) || len(draft.Paragraphs) != 3 {
		t.Fatalf("draft=%+v leadFallback=%v scoreCalls=%d writes=%d judges=%d err=%s", draft, leadFallback, scoreCalls, writes, judges, safeLogError(err))
	}
	for _, paragraph := range draft.Paragraphs {
		if len(paragraph.Refs) == 0 || containsString(paragraph.Refs, "ref-rumor") {
			t.Fatalf("paragraph without allowed refs: %+v", paragraph)
		}
	}
	if !strings.Contains(logged.String(), "ranked morning scorer picked no lead; writing 3 candidates in the fixed order") || strings.Contains(logged.String(), "Mara") {
		t.Fatalf("plain-order log = %q", logged.String())
	}
}

func TestMorningBriefPlainOrderSelectionLeadsWithTheFirstUsableItem(t *testing.T) {
	for _, tc := range []struct {
		name  string
		items []morningBriefCandidate
		lead  string
	}{
		{"gated lead wins", []morningBriefCandidate{{ID: "a", NeedsUser: true, Confidence: .9}, {ID: "b", NeedsUser: true, OnlyUserKnown: true, OnlyUser: true, Confidence: 1}}, "b"},
		{"needs before slip", []morningBriefCandidate{{ID: "a", Slipping: true, Confidence: 1}, {ID: "b", NeedsUser: true, Confidence: .6}}, "b"},
		{"slip alone", []morningBriefCandidate{{ID: "a", Slipping: true, Confidence: .6}}, "a"},
		{"uncertain plain item", []morningBriefCandidate{{ID: "a", Handled: true, Confidence: 1}, {ID: "b", Confidence: .6}}, "b"},
		{"handled last", []morningBriefCandidate{{ID: "a", Handled: true, Confidence: 1}}, "a"},
		{"below every gate", []morningBriefCandidate{{ID: "a", NeedsUser: true, Confidence: .4}}, ""},
		{"empty", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := morningBriefPlainOrderSelection(tc.items)
			got := ""
			if s.Lead != nil {
				got = s.Lead.ID
			}
			if got != tc.lead {
				t.Fatalf("lead = %q, want %q (%+v)", got, tc.lead, s)
			}
			for _, item := range s.Needs {
				if item.ID == got {
					t.Fatal("the lead is also listed as a need")
				}
			}
			if s.Lead != nil && (s.Slip != nil && s.Slip.ID == got || s.Handled != nil && s.Handled.ID == got) {
				t.Fatal("the lead is listed twice")
			}
		})
	}
}

// Without admitted candidates there is nothing to write: the pipeline still
// refuses, and the service gives the fixed empty statement instead.
func TestMorningBriefPipelineWithoutCandidatesHasNoLead(t *testing.T) {
	for _, items := range [][]morningBriefCandidate{nil, {{ID: "rumor", NeedsUser: true, Confidence: .2}}} {
		_, _, err := runMorningBriefPipeline(context.Background(), "Bo", "", nil, nil, nil, nil, items,
			func(context.Context) error { return nil },
			func(context.Context, string) (string, error) {
				if len(items) == 0 {
					return `{"items":[]}`, nil
				}
				return `{"items":[{"id":"rumor","critique":"Not usable evidence.","score":0,"pick":"omit"}]}`, nil
			}, morningBriefFixtureRefs, groundMorningBriefLead,
			func(context.Context, morningBriefWriterInput, string) (morningBriefDraft, error) {
				t.Fatal("a set without usable candidates reached the writer")
				return morningBriefDraft{}, nil
			})
		if !errors.Is(err, errMorningBriefNoEligibleLead) {
			t.Fatalf("items=%+v err=%s", items, safeLogError(err))
		}
	}
}

// Bo's day: two items wait on him, neither passes the lead gate, so the
// scorer picks no lead. The scheduled brief is written with source refs.
func TestScheduledMorningIsWrittenWhenTheScorerPicksNoLead(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	svc, _ := newTestMobileBriefService(t, &fakeMobileBriefClock{now: now})
	svc.app.mobileBriefSvc, svc.compose = svc, nil
	work, err := handoff.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	svc.app.handoffStore = work
	for i, entry := range []handoff.Entry{
		{ID: "lantern-permit", Title: "Lantern festival permit", Source: handoff.SourceMobile, Status: handoff.StatusBlocked},
		{ID: "orchard-quote", Title: "Orchard Lane quote", Source: handoff.SourceMobile, Status: handoff.StatusBlocked},
	} {
		entry.CreatedAt = now.Add(-time.Duration(i+1) * time.Hour).UnixMilli()
		entry.UpdatedAt = now.Add(-time.Duration(i+1) * time.Minute).UnixMilli()
		if _, err := work.Save(entry); err != nil {
			t.Fatal(err)
		}
	}
	svc.gather = func(_ context.Context, slot string, start, asOf time.Time) (mobileBriefInput, error) {
		input := mobileBriefInput{Slot: slot, WindowStart: start, AsOf: asOf, Location: time.UTC}
		collectMobileBriefWork(svc.app, &input)
		return input, nil
	}
	permit := mobileBriefItemKey("candidate", "history", "lantern-permit")
	quote := mobileBriefItemKey("candidate", "history", "orchard-quote")
	lead, other, leadTitle := permit, quote, "lantern festival permit"
	if quote < permit {
		lead, other, leadTitle = quote, permit, "Orchard Lane quote"
	}
	scores, writes := 0, 0
	svc.modelCall = func(_ context.Context, prompt string) (string, error) {
		switch true {
		case strings.Contains(prompt, "chief of staff's first pass"):
			scores++
			reply, err := json.Marshal(morningBriefScores{Items: []morningBriefScore{
				{ID: permit, Critique: "Blocked on you.", Score: .7, Pick: "need"},
				{ID: quote, Critique: "Blocked on you.", Score: .6, Pick: "need"},
			}})
			return string(reply), err
		case strings.Contains(prompt, "You write the user's morning brief"):
			writes++
			reply, err := json.Marshal(morningBriefDraftReply{Paragraphs: []morningBriefDraftParagraph{
				{Text: "One piece of work is blocked on you.", Items: []string{lead}},
				{Text: "Another piece of work is blocked too.", Items: []string{other}},
				{Text: "Want to unblock the " + leadTitle + "?", Items: []string{lead}},
			}, Answers: []mobileBriefCardWords{{Label: "Unblock it", Message: "Yes, help me unblock the " + leadTitle + "."}, {Label: "Not now", Message: "No, leave it."}}})
			return string(reply), err
		case strings.Contains(prompt, "Judge the morning brief"):
			return "", errors.New("judge unavailable")
		default:
			t.Fatalf("unexpected model call: %s", prompt)
			return "", nil
		}
	}
	svc.publish = func(MobileBriefV1, bool) error { return nil }
	brief, err := svc.generate(context.Background(), mobileBriefSlotMorning, mobileBriefReasonSchedule)
	if err != nil {
		t.Fatalf("the brief failed (%s): %s", mobileBriefFailureKind(err), safeLogError(err))
	}
	svc.wg.Wait()
	if scores != 1 || writes != 1 || !brief.rankedEdition || brief.modelBinding == nil || len(brief.Narrative) != 3 {
		t.Fatalf("scores=%d writes=%d brief=%+v", scores, writes, brief.Narrative)
	}
	for _, paragraph := range brief.Narrative {
		if len(paragraph.Refs) == 0 {
			t.Fatalf("paragraph without source refs: %+v", paragraph)
		}
	}
	stored, found := svc.store.load(brief.BriefID)
	if !found || len(svc.app.mobileBriefForDelivery(stored).Narrative) != 3 {
		t.Fatalf("the plain-order brief was not stored and delivered: %+v", stored.Narrative)
	}
}

func TestScheduledMorningUsesOrdinaryComposerWhenWritingFails(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	svc, _ := newTestMobileBriefService(t, &fakeMobileBriefClock{now: now})
	svc.app.mobileBriefSvc, svc.compose = svc, nil
	work, err := handoff.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	svc.app.handoffStore = work
	for i, entry := range []handoff.Entry{
		{ID: "lantern-permit", Title: "Lantern festival permit", Source: handoff.SourceMobile, Status: handoff.StatusBlocked},
		{ID: "orchard-quote", Title: "Orchard Lane quote", Source: handoff.SourceMobile, Status: handoff.StatusBlocked},
	} {
		entry.CreatedAt = now.Add(-time.Duration(i+1) * time.Hour).UnixMilli()
		entry.UpdatedAt = now.Add(-time.Duration(i+1) * time.Minute).UnixMilli()
		if _, err := work.Save(entry); err != nil {
			t.Fatal(err)
		}
	}
	svc.gather = func(_ context.Context, slot string, start, asOf time.Time) (mobileBriefInput, error) {
		input := mobileBriefInput{Slot: slot, WindowStart: start, AsOf: asOf, Location: time.UTC}
		collectMobileBriefWork(svc.app, &input)
		return input, nil
	}
	permit := mobileBriefItemKey("candidate", "history", "lantern-permit")
	quote := mobileBriefItemKey("candidate", "history", "orchard-quote")
	scores, writes := 0, 0
	svc.modelCall = func(_ context.Context, prompt string) (string, error) {
		switch true {
		case strings.Contains(prompt, "chief of staff's first pass"):
			scores++
			reply, err := json.Marshal(morningBriefScores{Items: []morningBriefScore{
				{ID: permit, Critique: "Blocked on you.", Score: .7, Pick: "need"},
				{ID: quote, Critique: "Blocked on you.", Score: .6, Pick: "need"},
			}})
			return string(reply), err
		case strings.Contains(prompt, "You write the user's morning brief"):
			writes++
			return "", errors.New("writer unavailable")
		case strings.Contains(prompt, "Judge the morning brief"):
			return "", errors.New("judge unavailable")
		default:
			t.Fatalf("unexpected model call: %s", prompt)
			return "", nil
		}
	}
	svc.publish = func(MobileBriefV1, bool) error { return nil }
	brief, err := svc.generate(context.Background(), mobileBriefSlotMorning, mobileBriefReasonSchedule)
	if err != nil {
		t.Fatalf("the brief failed (%s): %s", mobileBriefFailureKind(err), safeLogError(err))
	}
	svc.wg.Wait()
	if scores != 1 || writes == 0 || brief.rankedEdition {
		t.Fatalf("ordinary composer not used: scores=%d writes=%d ranked=%v", scores, writes, brief.rankedEdition)
	}
	if _, found := svc.store.load(brief.BriefID); !found {
		t.Fatal("ordinary brief was not saved")
	}
}
