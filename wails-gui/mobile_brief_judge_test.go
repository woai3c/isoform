package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestMorningBriefJudgesRunSeparately(t *testing.T) {
	calls := 0
	critique, err := judgeMorningBrief(context.Background(), morningBriefJudgeInput{}, func(_ context.Context, prompt string) (string, error) {
		calls++
		if !strings.Contains(prompt, morningBriefJudgeQuestions[calls-1]) {
			t.Fatalf("wrong judge at call %d", calls)
		}
		if calls == 2 || calls == 4 {
			return `{"critique":"Needs a change.","pass":false}`, nil
		}
		return `{"critique":"Grounded.","pass":true}`, nil
	})
	if err != nil || calls != 6 || strings.Count(critique, "Needs a change.") != 2 {
		t.Fatalf("calls=%d critique=%q err=%v", calls, critique, err)
	}
}

func TestMorningBriefJudgeRejectsInvalidVerdict(t *testing.T) {
	for _, raw := range []string{`{"critique":"ok"}`, `{"critique":"","pass":true}`, `{"critique":"ok","pass":true} {}`, `{"critique":"ok","pass":true,"extra":1}`} {
		if _, err := judgeMorningBrief(context.Background(), morningBriefJudgeInput{}, func(context.Context, string) (string, error) { return raw, nil }); err == nil {
			t.Fatalf("accepted verdict %s", raw)
		}
	}
}

func TestMorningBriefJudgeOneRevisionThenLeadOnly(t *testing.T) {
	fallback := &MobileBriefPara{Text: "Pay the tax because it is due today.", Refs: []string{"e1"}}
	for _, tc := range []struct {
		name       string
		passSecond bool
	}{
		{"revised", true}, {"fallback", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writes, judges := 0, 0
			got, usedFallback, err := writeAndJudgeMorningBrief(context.Background(), morningBriefJudgeInput{}, fallback,
				func(_ context.Context, critique string) (morningBriefDraft, error) {
					writes++
					if writes == 2 && critique != "Needs a change." {
						t.Fatalf("revision did not receive critique: %q", critique)
					}
					return morningBriefDraft{Paragraphs: []MobileBriefPara{{Text: "A draft.", Refs: []string{"e1"}}}, Cards: []MobileBriefCard{{Label: "Open tax"}}}, nil
				}, func(_ context.Context, input morningBriefJudgeInput) (string, error) {
					judges++
					if len(input.Cards) != 1 || input.Cards[0].Label != "Open tax" {
						t.Fatalf("judge did not receive draft chips: %+v", input.Cards)
					}
					if judges == 2 && tc.passSecond {
						return "", nil
					}
					return "Needs a change.", nil
				})
			if err != nil || writes != 2 || judges != 2 || usedFallback == tc.passSecond || len(got.Paragraphs) != 1 {
				t.Fatalf("writes=%d judges=%d fallback=%v got=%+v err=%v", writes, judges, usedFallback, got, err)
			}
			if !tc.passSecond && (got.Paragraphs[0].Text != fallback.Text || len(got.Cards) != 0 || len(got.CardWords) != 0) {
				t.Fatalf("fallback = %+v", got)
			}
		})
	}
}

func TestMorningBriefJudgeFailureWithoutGroundedLead(t *testing.T) {
	writes := 0
	_, fallback, err := writeAndJudgeMorningBrief(context.Background(), morningBriefJudgeInput{}, nil,
		func(context.Context, string) (morningBriefDraft, error) {
			writes++
			return morningBriefDraft{}, errors.New("writer unavailable")
		},
		func(context.Context, morningBriefJudgeInput) (string, error) {
			t.Fatal("unexpected judge call")
			return "", nil
		})
	if writes != 4 || !fallback || safeLogError(err) != "no grounded lead for fallback" {
		t.Fatalf("writes=%d fallback=%v err=%v", writes, fallback, err)
	}
}

func TestMorningBriefWriterAndJudgeRetryUnavailableCalls(t *testing.T) {
	writes, judges := 0, 0
	draft, fallback, err := writeAndJudgeMorningBrief(context.Background(), morningBriefJudgeInput{}, nil,
		func(context.Context, string) (morningBriefDraft, error) {
			writes++
			if writes == 1 {
				return morningBriefDraft{}, errors.New("private writer text")
			}
			return morningBriefDraft{Paragraphs: []MobileBriefPara{{Text: "First."}, {Text: "Second."}}}, nil
		}, func(context.Context, morningBriefJudgeInput) (string, error) {
			judges++
			if judges == 1 {
				return "", errors.New("private judge text")
			}
			return "", nil
		})
	if err != nil || fallback || writes != 3 || judges != 2 || len(draft.Paragraphs) != 2 {
		t.Fatalf("writes=%d judges=%d fallback=%v err=%v", writes, judges, fallback, err)
	}
}

func TestMorningBriefWriterAndJudgeStopOnWithdrawalOrCancellation(t *testing.T) {
	for _, stopped := range []error{errMobileBriefSourcesChanged, context.Canceled, context.DeadlineExceeded} {
		for _, stage := range []string{"writer", "judge"} {
			t.Run(stage+"/"+stopped.Error(), func(t *testing.T) {
				writes, judges := 0, 0
				_, fallback, err := writeAndJudgeMorningBrief(context.Background(), morningBriefJudgeInput{}, nil,
					func(context.Context, string) (morningBriefDraft, error) {
						writes++
						if stage == "writer" {
							return morningBriefDraft{}, stopped
						}
						return morningBriefDraft{Paragraphs: []MobileBriefPara{{Text: "Draft."}}}, nil
					}, func(context.Context, morningBriefJudgeInput) (string, error) { judges++; return "", stopped })
				if !errors.Is(err, stopped) || fallback || writes != 1 || (stage == "judge" && judges != 1) {
					t.Fatalf("writes=%d judges=%d fallback=%v err=%v", writes, judges, fallback, err)
				}
			})
		}
	}
}

func TestMorningBriefFallbackRejectsExtraContent(t *testing.T) {
	for _, text := range []string{"Lead. Reason.", "Lead. Reason. Another item.", "Lead. What next?", "Unfinished"} {
		fallback := &MobileBriefPara{Text: text, Refs: []string{"e1"}}
		_, used, err := writeAndJudgeMorningBrief(context.Background(), morningBriefJudgeInput{}, fallback,
			func(context.Context, string) (morningBriefDraft, error) {
				return morningBriefDraft{}, errors.New("unavailable")
			}, nil)
		if !used || err == nil {
			t.Fatalf("accepted fallback %q", text)
		}
	}
}

func TestMorningBriefRejectsOverBudgetBeforeJudge(t *testing.T) {
	judged := 0
	fallback := &MobileBriefPara{Text: "Pay the tax because it is due today.", Refs: []string{"e1"}}
	got, used, err := writeAndJudgeMorningBrief(context.Background(), morningBriefJudgeInput{}, fallback,
		func(context.Context, string) (morningBriefDraft, error) {
			return morningBriefDraft{Paragraphs: []MobileBriefPara{{Text: strings.Repeat("word ", mobileBriefMaxWords+1)}}}, nil
		}, func(context.Context, morningBriefJudgeInput) (string, error) { judged++; return "", nil })
	if err != nil || !used || judged != 0 || len(got.Paragraphs) != 1 || got.Paragraphs[0].Text != fallback.Text {
		t.Fatalf("used=%v judged=%d got=%+v err=%v", used, judged, got, err)
	}
}

func TestMorningBriefLeadFallbackRequiresIndependentGrounding(t *testing.T) {
	item := morningBriefCandidate{ID: "tax", Title: "Tax", Refs: []string{"e1"}, NeedsUser: true, OnlyUserKnown: true, OnlyUser: true, Confidence: 1}
	opening := morningBriefOpening{Text: "Pay the tax because it is due today.", Probability: 0.9}
	calls := 0
	lead, err := groundMorningBriefLead(context.Background(), item, opening, func(_ context.Context, prompt string) (string, error) {
		indices := []int{0, 3}
		if calls >= len(indices) || !strings.Contains(prompt, morningBriefJudgeQuestions[indices[calls]]) || !strings.Contains(prompt, `"id":"tax"`) {
			t.Fatalf("unexpected grounding prompt: %s", prompt)
		}
		calls++
		return `{"critique":"Supported.","pass":true}`, nil
	})
	if err != nil || calls != 2 || lead == nil || len(lead.Refs) != 1 || lead.Refs[0] != "e1" {
		t.Fatalf("lead=%+v calls=%d err=%v", lead, calls, err)
	}
	for _, verdict := range []string{`{"critique":"Unsupported.","pass":false}`, `{"critique":"Missing verdict."}`} {
		if _, err := groundMorningBriefLead(context.Background(), item, opening, func(context.Context, string) (string, error) { return verdict, nil }); err == nil {
			t.Fatal("unverified lead was admitted as a fallback")
		}
	}
	unknown := item
	unknown.OnlyUserKnown = false
	if _, err := groundMorningBriefLead(context.Background(), unknown, opening, func(context.Context, string) (string, error) {
		t.Fatal("an ineligible lead reached the fallback judge")
		return "", nil
	}); err == nil {
		t.Fatal("an ineligible lead was accepted")
	}
	if _, err := groundMorningBriefLead(context.Background(), item, morningBriefOpening{Text: "Pay the tax. Then do something else."}, func(context.Context, string) (string, error) {
		t.Fatal("multiple sentences reached the fallback judge")
		return "", nil
	}); err == nil {
		t.Fatal("multiple fallback sentences were accepted")
	}
}
