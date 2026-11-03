package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func morningBriefFixtureRefs(_ context.Context, selection morningBriefSelection) (map[string][]string, error) {
	refs := map[string][]string{}
	add := func(item morningBriefCandidate) {
		if len(item.Refs) > 0 {
			refs[item.ID] = item.Refs
		} else {
			refs[item.ID] = []string{"ref-" + item.ID}
		}
	}
	if selection.Lead != nil {
		add(*selection.Lead)
	}
	for _, item := range selection.Needs {
		add(item)
	}
	if selection.Slip != nil {
		add(*selection.Slip)
	}
	if selection.Handled != nil {
		add(*selection.Handled)
	}
	return refs, nil
}

func TestMorningBriefPipelineRanksRevisesAndJudges(t *testing.T) {
	items := []morningBriefCandidate{
		{ID: "tax", Title: "Tax", HardDueKnown: true, HardDueToday: true, NeedsUser: true, OnlyUserKnown: true, OnlyUser: true, Confidence: 1},
		{ID: "calendar", Title: "Routine meeting", Confidence: 1},
	}
	response := `{"items":[{"id":"tax","critique":"Due today.","score":1,"pick":"need"},{"id":"calendar","critique":"No preparation.","score":0,"pick":"omit"}],"leadId":"tax","openings":[{"text":"Pay the tax because it is due today.","probability":0.9},{"text":"Pay the tax today because it is due.","probability":0.8},{"text":"The tax is due today, so pay it.","probability":0.7}]}`
	calls, writes, checks, verifications := 0, 0, 0, 0
	draft, fallback, err := runMorningBriefPipeline(context.Background(), "Bo", "launch", nil, nil, []string{"A prior lead."}, []string{"A prior full brief."}, items,
		func(context.Context) error { checks++; return nil },
		func(_ context.Context, prompt string) (string, error) {
			calls++
			if calls == 1 {
				return response, nil
			}
			if !strings.Contains(prompt, "A prior full brief.") {
				t.Fatal("judge did not receive previous brief")
			}
			if calls == 2 {
				return `{"critique":"Make the lead clearer.","pass":false}`, nil
			}
			return `{"critique":"Pass.","pass":true}`, nil
		}, morningBriefFixtureRefs, func(_ context.Context, candidate morningBriefCandidate, opening morningBriefOpening, _ func(context.Context, string) (string, error)) (*MobileBriefPara, error) {
			verifications++
			if candidate.ID != "tax" || opening.Probability != 0.9 {
				t.Fatal("wrong lead passed to grounding")
			}
			return &MobileBriefPara{Text: "Pay the tax. It is due today.", Refs: []string{"e1"}}, nil
		}, func(_ context.Context, input morningBriefWriterInput, critique string) (morningBriefDraft, error) {
			writes++
			if input.Selection.Lead.ID != "tax" || len(input.Reasons) != 1 || input.Reasons["calendar"] != "" || len(input.PriorLeads) != 1 || input.PriorLeads[0] != "A prior lead." {
				t.Fatalf("writer saw unexpected context: %+v", input)
			}
			if writes == 2 && critique != "Make the lead clearer." {
				t.Fatalf("revision critique = %q", critique)
			}
			return morningBriefDraft{Paragraphs: []MobileBriefPara{{Text: "Pay the tax. It is due today.", Refs: []string{"e1"}}}}, nil
		})
	if err != nil || fallback || writes != 2 || calls != 13 || checks < 2*calls || verifications != 0 || len(draft.Paragraphs) != 1 {
		t.Fatalf("draft=%+v fallback=%v writes=%d calls=%d checks=%d err=%v", draft, fallback, writes, calls, checks, err)
	}
}

func TestMorningBriefPipelineStopsWhenSourcesWithdrawn(t *testing.T) {
	withdrawn := errors.New("source withdrawn")
	checks, calls := 0, 0
	_, _, err := runMorningBriefPipeline(context.Background(), "Bo", "", nil, nil, nil, nil, nil,
		func(context.Context) error {
			checks++
			if checks == 2 {
				return withdrawn
			}
			return nil
		},
		func(context.Context, string) (string, error) { calls++; return `{}`, nil },
		morningBriefFixtureRefs,
		func(context.Context, morningBriefCandidate, morningBriefOpening, func(context.Context, string) (string, error)) (*MobileBriefPara, error) {
			t.Fatal("unexpected lead verification")
			return nil, nil
		},
		func(context.Context, morningBriefWriterInput, string) (morningBriefDraft, error) {
			t.Fatal("unexpected writer")
			return morningBriefDraft{}, nil
		})
	if !errors.Is(err, withdrawn) || calls != 1 || checks != 2 {
		t.Fatalf("calls=%d checks=%d err=%v", calls, checks, err)
	}
}

// The October 30 example exercises the selection and draft structure with a
// controlled writer. It does not assert the exact words an actual model uses.
func TestMorningBriefOctober30PipelineStructure(t *testing.T) {
	candidates, scores := morningBriefOctober30Fixture()
	for i := range candidates {
		candidates[i].Refs = []string{candidates[i].ID}
	}
	response, err := json.Marshal(scores)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	draft, fallback, err := runMorningBriefPipeline(context.Background(), "Bo", "launch", nil, nil, nil, nil, candidates,
		func(context.Context) error { return nil },
		func(_ context.Context, _ string) (string, error) {
			calls++
			if calls == 1 {
				return string(response), nil
			}
			return `{"critique":"Grounded and concise.","pass":true}`, nil
		}, morningBriefFixtureRefs,
		func(_ context.Context, lead morningBriefCandidate, opening morningBriefOpening, _ func(context.Context, string) (string, error)) (*MobileBriefPara, error) {
			return &MobileBriefPara{Text: opening.Text, Refs: []string{lead.ID}}, nil
		},
		func(_ context.Context, input morningBriefWriterInput, _ string) (morningBriefDraft, error) {
			if input.Selection.Lead == nil || input.Selection.Lead.ID != "tax" || len(input.Selection.Needs) != 2 || input.Selection.Slip == nil || input.Selection.Handled == nil {
				t.Fatalf("unexpected October selection: %+v", input.Selection)
			}
			for _, item := range input.Selection.Needs {
				if strings.HasPrefix(item.ID, "calendar-") {
					t.Fatalf("schedule-only item reached the writer: %+v", item)
				}
			}
			reply := morningBriefDraftReply{Paragraphs: []morningBriefDraftParagraph{
				{Text: "Pay the tax. It is due today.", Items: []string{input.Selection.Lead.ID}},
				{Text: "Review the launch posts.", Items: []string{input.Selection.Needs[0].ID}},
				{Text: "Prepare for the conversation.", Items: []string{input.Selection.Needs[1].ID}},
				{Text: "Exercise has slipped three days. Find a gap.", Items: []string{input.Selection.Slip.ID}},
				{Text: "The agreement is signed.", Items: []string{input.Selection.Handled.ID}},
			}}
			raw, err := json.Marshal(reply)
			if err != nil {
				return morningBriefDraft{}, err
			}
			return parseMorningBriefDraft(string(raw), input.Selection)
		})
	if err != nil || fallback || len(draft.Paragraphs) == 0 || draft.Paragraphs[0].Refs[0] != "tax" || mobileBriefNarrativeWords(draft.Paragraphs) > mobileBriefMaxWords {
		t.Fatalf("invalid October draft: %+v, fallback=%v, err=%v", draft, fallback, err)
	}
	for _, paragraph := range draft.Paragraphs {
		for _, ref := range paragraph.Refs {
			if strings.HasPrefix(ref, "calendar-") {
				t.Fatalf("calendar-only item appeared in the draft: %+v", paragraph)
			}
		}
	}
}

func TestMorningBriefPipelineGroundsFallbackOnlyAfterRevision(t *testing.T) {
	items := []morningBriefCandidate{{ID: "tax", Title: "Tax", Refs: []string{"e1"}, HardDueKnown: true, HardDueToday: true, NeedsUser: true, OnlyUserKnown: true, OnlyUser: true, Confidence: 1}}
	response := `{"items":[{"id":"tax","critique":"Due today.","score":1,"pick":"need"}],"leadId":"tax","openings":[{"text":"Pay the tax because it is due today.","probability":0.9},{"text":"Pay the tax today because it is due.","probability":0.8},{"text":"The tax is due today, so pay it.","probability":0.7}]}`
	calls, writes, checks := 0, 0, 0
	draft, fallback, err := runMorningBriefPipeline(context.Background(), "Bo", "", nil, nil, nil, nil, items,
		func(context.Context) error { checks++; return nil },
		func(_ context.Context, _ string) (string, error) {
			calls++
			if calls == 1 {
				return response, nil
			}
			if calls <= 13 || calls > 15 {
				return `{"critique":"Needs a revision.","pass":false}`, nil
			}
			return `{"critique":"Grounded.","pass":true}`, nil
		}, morningBriefFixtureRefs, groundMorningBriefLead,
		func(context.Context, morningBriefWriterInput, string) (morningBriefDraft, error) {
			writes++
			return morningBriefDraft{Paragraphs: []MobileBriefPara{{Text: "A draft.", Refs: []string{"e1"}}}}, nil
		})
	if err != nil || !fallback || writes != 4 || calls != 27 || checks < 2*calls || len(draft.Paragraphs) != 1 ||
		draft.Paragraphs[0].Text != "Pay the tax because it is due today." || len(draft.Cards) != 0 {
		t.Fatalf("draft=%+v fallback=%v writes=%d calls=%d checks=%d err=%v", draft, fallback, writes, calls, checks, err)
	}
}

func TestMorningBriefPipelineRetriesWritingWithFixedOrder(t *testing.T) {
	items := []morningBriefCandidate{
		{ID: "tax", Title: "Tax", NeedsUser: true, OnlyUserKnown: true, OnlyUser: true, Confidence: 1},
		{ID: "plan", Title: "Plan", NeedsUser: true, OnlyUserKnown: true, OnlyUser: true, Confidence: 1},
	}
	response := `{"items":[{"id":"tax","critique":"Ready.","score":1,"pick":"need"},{"id":"plan","critique":"Ready.","score":0.8,"pick":"need"}],"leadId":"tax","openings":[{"text":"Review the tax because it is ready.","probability":0.9},{"text":"Review the tax because it awaits review.","probability":0.8},{"text":"Review the tax because it is waiting.","probability":0.7}]}`
	writes := 0
	draft, fallback, err := runMorningBriefPipeline(context.Background(), "Bo", "", nil, nil, nil, nil, items,
		func(context.Context) error { return nil },
		func(_ context.Context, prompt string) (string, error) {
			if strings.Contains(prompt, "chief of staff's first pass") {
				return response, nil
			}
			return `{"critique":"Grounded.","pass":true}`, nil
		}, morningBriefFixtureRefs,
		func(context.Context, morningBriefCandidate, morningBriefOpening, func(context.Context, string) (string, error)) (*MobileBriefPara, error) {
			return &MobileBriefPara{Text: "Review the tax because it is ready.", Refs: []string{"ref-tax"}}, nil
		},
		func(_ context.Context, input morningBriefWriterInput, _ string) (morningBriefDraft, error) {
			writes++
			if input.Opening.Text != "" {
				return morningBriefDraft{}, errors.New("private model error")
			}
			return morningBriefDraft{Paragraphs: []MobileBriefPara{{Text: "Review the plan.", Refs: input.Selection.Lead.Refs}, {Text: "The other item is ready.", Refs: input.Selection.Needs[0].Refs}}}, nil
		})
	if err != nil || fallback || writes != 5 || len(draft.Paragraphs) != 2 {
		t.Fatalf("writes=%d fallback=%v draft=%+v err=%v", writes, fallback, draft, err)
	}
}

func TestMorningBriefPipelineRejectsUnselectedEvidence(t *testing.T) {
	lead := morningBriefCandidate{ID: "tax", Title: "Tax", NeedsUser: true, OnlyUserKnown: true, OnlyUser: true, Confidence: 1}
	response := `{"items":[{"id":"tax","critique":"Due today.","score":1,"pick":"need"}],"leadId":"tax","openings":[{"text":"Pay the tax because it is due today.","probability":0.9},{"text":"Pay the tax today because it is due.","probability":0.8},{"text":"The tax is due today, so pay it.","probability":0.7}]}`
	writes := 0
	_, _, err := runMorningBriefPipeline(context.Background(), "Bo", "", nil, nil, nil, nil, []morningBriefCandidate{lead},
		func(context.Context) error { return nil },
		func(context.Context, string) (string, error) { return response, nil },
		func(context.Context, morningBriefSelection) (map[string][]string, error) {
			return map[string][]string{"tax": {"e1"}, "unselected": {"e2"}}, nil
		}, groundMorningBriefLead,
		func(context.Context, morningBriefWriterInput, string) (morningBriefDraft, error) {
			writes++
			return morningBriefDraft{}, nil
		})
	if err == nil || writes != 0 {
		t.Fatalf("unselected evidence reached writer: writes=%d err=%v", writes, err)
	}
}

func TestMorningBriefPipelineStopsWhenSelectionSourceChanges(t *testing.T) {
	lead := morningBriefCandidate{ID: "tax", Title: "Tax", NeedsUser: true, OnlyUserKnown: true, OnlyUser: true, Confidence: 1}
	response := `{"items":[{"id":"tax","critique":"Due today.","score":1,"pick":"need"}],"leadId":"tax","openings":[{"text":"Pay the tax because it is due today.","probability":0.9},{"text":"Pay the tax today because it is due.","probability":0.8},{"text":"The tax is due today, so pay it.","probability":0.7}]}`
	changed := errors.New("source changed")
	checks, writes := 0, 0
	_, _, err := runMorningBriefPipeline(context.Background(), "Bo", "", nil, nil, nil, nil, []morningBriefCandidate{lead},
		func(context.Context) error {
			checks++
			if checks == 4 {
				return changed
			}
			return nil
		}, func(context.Context, string) (string, error) { return response, nil }, morningBriefFixtureRefs, groundMorningBriefLead,
		func(context.Context, morningBriefWriterInput, string) (morningBriefDraft, error) {
			writes++
			return morningBriefDraft{}, nil
		})
	if !errors.Is(err, changed) || writes != 0 {
		t.Fatalf("changed source reached writer: checks=%d writes=%d err=%v", checks, writes, err)
	}
}

func TestMorningBriefPipelineStopsWhenCanceledAfterScoring(t *testing.T) {
	lead := morningBriefCandidate{ID: "tax", Title: "Tax", NeedsUser: true, OnlyUserKnown: true, OnlyUser: true, Confidence: 1}
	response := `{"items":[{"id":"tax","critique":"Due today.","score":1,"pick":"need"}],"leadId":"tax","openings":[{"text":"Pay the tax because it is due today.","probability":0.9},{"text":"Pay the tax today because it is due.","probability":0.8},{"text":"The tax is due today, so pay it.","probability":0.7}]}`
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resolved := 0
	_, _, err := runMorningBriefPipeline(ctx, "Bo", "", nil, nil, nil, nil, []morningBriefCandidate{lead},
		func(context.Context) error { return nil },
		func(context.Context, string) (string, error) { cancel(); return response, nil },
		func(context.Context, morningBriefSelection) (map[string][]string, error) { resolved++; return nil, nil }, groundMorningBriefLead,
		func(context.Context, morningBriefWriterInput, string) (morningBriefDraft, error) {
			t.Fatal("canceled pipeline reached writer")
			return morningBriefDraft{}, nil
		})
	if !errors.Is(err, context.Canceled) || resolved != 0 {
		t.Fatalf("canceled pipeline continued: resolved=%d err=%v", resolved, err)
	}
}

func TestMorningBriefPipelineFallsBackWhenScoresStayUnreadable(t *testing.T) {
	items := []morningBriefCandidate{
		{ID: "tax", Title: "Tax", HardDueKnown: true, HardDueToday: true, NeedsUser: true, OnlyUserKnown: true, OnlyUser: true, Confidence: 1},
		{ID: "calendar", Title: "Routine meeting", Confidence: 1},
	}
	scoreCalls, judges, writes := 0, 0, 0
	draft, leadFallback, err := runMorningBriefPipeline(context.Background(), "Bo", "launch", nil, nil, nil, nil, items,
		func(context.Context) error { return nil },
		func(_ context.Context, prompt string) (string, error) {
			if strings.Contains(prompt, "chief of staff's first pass") {
				scoreCalls++
				return "Here are the scores:\n```json\n{\"items\": [\n```\nThat is all.", nil
			}
			judges++
			return `{"critique":"Pass.","pass":true}`, nil
		}, morningBriefFixtureRefs,
		func(context.Context, morningBriefCandidate, morningBriefOpening, func(context.Context, string) (string, error)) (*MobileBriefPara, error) {
			t.Fatal("an unscored brief has no opening to ground")
			return nil, nil
		}, func(_ context.Context, input morningBriefWriterInput, _ string) (morningBriefDraft, error) {
			writes++
			if input.Selection.Lead == nil || input.Selection.Lead.ID != "tax" || len(input.Selection.Needs) != 0 || input.Opening.Text != "" {
				t.Fatalf("writer saw unexpected fallback input: %+v", input)
			}
			return morningBriefDraft{Paragraphs: []MobileBriefPara{{Text: "Pay the tax. It is due today.", Refs: input.Selection.Lead.Refs}}}, nil
		})
	if err != nil || leadFallback || scoreCalls != 2 || writes != 1 || judges != len(morningBriefJudgeQuestions) || len(draft.Paragraphs) != 1 {
		t.Fatalf("draft=%+v leadFallback=%v scoreCalls=%d writes=%d judges=%d err=%s", draft, leadFallback, scoreCalls, writes, judges, safeLogError(err))
	}
}
