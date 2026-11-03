package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestMorningBriefScorerMakesOneBatchCall(t *testing.T) {
	items := []morningBriefCandidate{{ID: "tax", Title: "Tax", HardDueKnown: true, HardDueToday: true,
		NeedsUser: true, OnlyUserKnown: true, OnlyUser: true, Confidence: 1}}
	response := `{"items":[{"id":"tax","critique":"Due today.","score":1,"pick":"need"}],"leadId":"tax","openings":[{"text":"Pay the tax because it is due today.","probability":0.9},{"text":"Pay the tax today because it is due.","probability":0.8},{"text":"The tax is due today, so pay it.","probability":0.7}]}`
	calls := 0
	_, selected, err := scoreMorningBrief(context.Background(), "Bo", "launch", nil, nil, items, func(_ context.Context, prompt string) (string, error) {
		calls++
		if !strings.Contains(prompt, "chief of staff's first pass") || !strings.Contains(prompt, `"id":"tax"`) {
			t.Fatal("the scorer did not receive the prompt and candidate")
		}
		return response, nil
	})
	if err != nil || calls != 1 || selected.Lead == nil || selected.Lead.ID != "tax" {
		t.Fatalf("calls=%d selection=%+v err=%v", calls, selected, err)
	}
}

func morningBriefOctober30Fixture() ([]morningBriefCandidate, morningBriefScores) {
	candidates := []morningBriefCandidate{
		{ID: "tax", Title: "Cali tax", HardDueKnown: true, HardDueToday: true, NeedsUser: true, OnlyUserKnown: true, OnlyUser: true, Confidence: 1},
		{ID: "posts", Title: "Launch posts", ServesKnown: true, Serves: "launch", NeedsUser: true, OnlyUserKnown: true, OnlyUser: true, Confidence: 1},
		{ID: "one-on-one", Title: "One-on-one", MeetingPrep: true, Within24Hours: true, NeedsUser: true, Confidence: 1},
		{ID: "exercise", Title: "Exercise", Slipping: true, Confidence: 1},
		{ID: "nda", Title: "NDA signed", Handled: true, Confidence: 1},
		{ID: "calendar-1", Title: "Meeting one", Confidence: 1},
		{ID: "calendar-2", Title: "Meeting two", Confidence: 1},
		{ID: "calendar-3", Title: "Meeting three", Confidence: 1},
		{ID: "calendar-4", Title: "Meeting four", Confidence: 1},
	}
	scores := morningBriefScores{LeadID: "tax", Openings: []morningBriefOpening{
		{Text: "Pay the tax today because it is due.", Probability: 0.9},
		{Text: "Pay the tax because it is due today.", Probability: 0.8},
		{Text: "The tax is due today, so pay it.", Probability: 0.7},
	}}
	for _, c := range candidates {
		decision := "omit"
		switch true {
		case c.Handled:
			decision = "handled"
		case c.Slipping:
			decision = "slip"
		case c.NeedsUser:
			decision = "need"
		}
		scores.Items = append(scores.Items, morningBriefScore{ID: c.ID, Critique: "Review its facts.", Score: 0.5, Pick: decision})
	}
	return candidates, scores
}

func TestMorningBriefOctober30Selection(t *testing.T) {
	candidates, scores := morningBriefOctober30Fixture()
	raw, err := json.Marshal(scores)
	if err != nil {
		t.Fatal(err)
	}
	parsed, selected, err := parseMorningBriefScores(string(raw), candidates)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Lead == nil || selected.Lead.ID != "tax" || len(selected.Needs) > 3 || selected.Slip == nil || selected.Slip.ID != "exercise" || selected.Handled == nil || selected.Handled.ID != "nda" {
		t.Fatalf("selection = %+v", selected)
	}
	if len(selected.Needs) != 2 || selected.Needs[0].ID != "posts" || selected.Needs[1].ID != "one-on-one" {
		t.Fatalf("needs-user selection = %+v", selected.Needs)
	}
	for _, item := range selected.Needs {
		if strings.HasPrefix(item.ID, "calendar-") {
			t.Fatalf("schedule-only item selected: %+v", item)
		}
	}
	if mobileBriefWordCount(parsed.Openings[0].Text) >= 20 || mobileBriefWordCount(parsed.Openings[0].Text) > mobileBriefMaxWords {
		t.Fatal("lead exceeds its length ceiling")
	}
}

func TestMorningBriefSelectionEnforcesCapsAndConfidence(t *testing.T) {
	items := []morningBriefCandidate{
		{ID: "unknown-owner", HardDueKnown: true, HardDueToday: true, NeedsUser: true, OnlyUser: true, Confidence: 1},
		{ID: "uncertain-lead", HardDueKnown: true, HardDueToday: true, NeedsUser: true, OnlyUserKnown: true, OnlyUser: true, Confidence: 0.7},
		{ID: "lead", NeedsUser: true, OnlyUserKnown: true, OnlyUser: true, Confidence: 0.8},
		{ID: "low", NeedsUser: true, Confidence: 0.49},
		{ID: "a", NeedsUser: true, Confidence: 1}, {ID: "b", NeedsUser: true, Confidence: 1},
		{ID: "c", NeedsUser: true, Confidence: 1}, {ID: "d", NeedsUser: true, Confidence: 1},
		{ID: "slip1", Slipping: true, Confidence: 1}, {ID: "slip2", Slipping: true, Confidence: 1},
		{ID: "done1", Handled: true, Confidence: 1}, {ID: "done2", Handled: true, Confidence: 1},
	}
	selected := morningBriefSelect(items, nil)
	if selected.Lead == nil || selected.Lead.ID != "lead" || len(selected.Needs) != 3 || selected.Slip == nil || selected.Handled == nil {
		t.Fatalf("selection = %+v", selected)
	}
	for _, item := range selected.Needs {
		if item.ID == "low" {
			t.Fatal("low-confidence item was selected")
		}
	}
}

func TestMorningBriefScoresRejectUntrustedOutput(t *testing.T) {
	items := []morningBriefCandidate{{ID: "a", NeedsUser: true, OnlyUserKnown: true, OnlyUser: true, Confidence: 1}}
	for _, raw := range []string{
		`{"items":[],"leadId":"a","openings":[]}`,
		`{"items":[{"id":"other","critique":"x","score":1}],"leadId":"a","openings":[]}`,
		`{"items":[{"id":"a","critique":"x","score":1,"pick":"need"}],"leadId":"other","openings":[]}`,
		`{"items":[{"id":"a","critique":"x","score":1,"pick":"need"}],"leadId":"a","openings":[{"text":"Do this. Then that.","probability":1},{"text":"Do this because it is due.","probability":0.9},{"text":"Do it because it is due.","probability":0.8}]}`,
	} {
		if _, _, err := parseMorningBriefScores(raw, items); err == nil {
			t.Fatalf("accepted invalid scores: %s", raw)
		}
	}
}

func TestMorningBriefScoresCannotOmitHighestLead(t *testing.T) {
	items := []morningBriefCandidate{
		{ID: "tax", HardDueKnown: true, HardDueToday: true, NeedsUser: true, OnlyUserKnown: true, OnlyUser: true, Confidence: 1},
		{ID: "other", NeedsUser: true, OnlyUserKnown: true, OnlyUser: true, Confidence: 1},
	}
	raw := `{"items":[{"id":"tax","critique":"x","score":1,"pick":"omit"},{"id":"other","critique":"x","score":1,"pick":"need"}],"leadId":"other","openings":[{"text":"Do the other work.","probability":1},{"text":"Do the other task.","probability":0.9},{"text":"Do the other thing.","probability":0.8}]}`
	if _, _, err := parseMorningBriefScores(raw, items); err == nil {
		t.Fatal("accepted an omitted higher-priority lead")
	}
}

const morningBriefTaxScoresReply = `{"items":[{"id":"tax","critique":"Due today.","score":1,"pick":"need"}],"leadId":"tax","openings":[{"text":"Pay the tax because it is due today.","probability":0.9},{"text":"Pay the tax today because it is due.","probability":0.8},{"text":"The tax is due today, so pay it.","probability":0.7}]}`

func morningBriefTaxCandidates() []morningBriefCandidate {
	return []morningBriefCandidate{{ID: "tax", Title: "Tax", HardDueKnown: true, HardDueToday: true,
		NeedsUser: true, OnlyUserKnown: true, OnlyUser: true, Confidence: 1}}
}

func TestMorningBriefScoresReadAroundTheScoresObject(t *testing.T) {
	for name, raw := range map[string]string{
		"code fence":     "```json\n" + morningBriefTaxScoresReply + "\n```",
		"bare fence":     "```\n" + morningBriefTaxScoresReply + "\n```",
		"leading prose":  "Here are the scores you asked for:\n\n" + morningBriefTaxScoresReply,
		"trailing text":  morningBriefTaxScoresReply + "\n\nLet me know if you want a different ranking {or not}.",
		"fence in prose": "I ranked every item.\n```json\n" + morningBriefTaxScoresReply + "\n```\nThe tax leads because it is due today.",
	} {
		scores, selected, err := parseMorningBriefScores(raw, morningBriefTaxCandidates())
		if err != nil || selected.Lead == nil || selected.Lead.ID != "tax" || len(scores.Openings) != 3 {
			t.Fatalf("%s: selection=%+v err=%s", name, selected, safeLogError(err))
		}
	}
}

func TestMorningBriefScoresRejectUnreadableRepliesWithCodeErrors(t *testing.T) {
	for name, raw := range map[string]string{
		"empty":           "",
		"blank":           " \n\t",
		"prose only":      "I could not rank these items today.",
		"invalid json":    `{"items":[{"id":"tax","critique":"Due today.","score":1,"pick":"need"}],"leadId":"tax",`,
		"not an object":   `[1, 2, 3]`,
		"unknown field":   `{"items":[{"id":"tax","critique":"Due today.","score":1,"pick":"need","why":"x"}],"leadId":"","openings":[]}`,
		"no items":        `{"leadId":"tax","openings":[]}`,
		"two objects":     morningBriefTaxScoresReply + "\n" + morningBriefTaxScoresReply,
		"invalid opening": `{"items":[{"id":"tax","critique":"Due today.","score":1,"pick":"need"}],"leadId":"tax","openings":[{"text":"Do this. Then that.","probability":1},{"text":"Pay the tax today because it is due.","probability":0.8},{"text":"The tax is due today, so pay it.","probability":0.7}]}`,
	} {
		_, _, err := parseMorningBriefScores(raw, morningBriefTaxCandidates())
		var code codeError
		if err == nil || !errors.As(err, &code) || strings.Contains(safeLogError(err), "unlisted") {
			t.Fatalf("%s: err = %v (%s), want a code error", name, err, safeLogError(err))
		}
	}
}

func TestMorningBriefScorerRetriesOnceWithAStricterInstruction(t *testing.T) {
	calls := 0
	_, selected, err := scoreMorningBrief(context.Background(), "Bo", "launch", nil, nil, morningBriefTaxCandidates(), func(_ context.Context, prompt string) (string, error) {
		calls++
		if !strings.Contains(prompt, "Reply with only the JSON object") {
			t.Fatal("the scorer prompt does not ask for JSON only")
		}
		if calls == 1 {
			return "Sure! Here is the ranking: {not json", nil
		}
		if !strings.Contains(prompt, morningBriefScoresRetryNote) {
			t.Fatal("the retry did not restate the JSON-only instruction")
		}
		return morningBriefTaxScoresReply, nil
	})
	if err != nil || calls != 2 || selected.Lead == nil || selected.Lead.ID != "tax" {
		t.Fatalf("calls=%d selection=%+v err=%s", calls, selected, safeLogError(err))
	}
}

func TestMorningBriefScorerClassifiesEveryFailure(t *testing.T) {
	for name, call := range map[string]func(context.Context, string) (string, error){
		"unreadable twice": func(context.Context, string) (string, error) { return "No scores today.", nil },
		"call failed": func(context.Context, string) (string, error) {
			return "", errors.New("exit status 1: model said Kris owes $400")
		},
	} {
		calls := 0
		_, _, err := scoreMorningBrief(context.Background(), "Bo", "", nil, nil, morningBriefTaxCandidates(), func(ctx context.Context, prompt string) (string, error) {
			calls++
			return call(ctx, prompt)
		})
		logged := safeLogError(withLogStage("score", err))
		if err == nil || calls != 2 || strings.Contains(logged, "unlisted") || strings.Contains(logged, "Kris") {
			t.Fatalf("%s: calls=%d log=%q", name, calls, logged)
		}
	}
	timeout := func(context.Context, string) (string, error) {
		return "", fmt.Errorf("model: %w", context.DeadlineExceeded)
	}
	if _, _, err := scoreMorningBrief(context.Background(), "Bo", "", nil, nil, morningBriefTaxCandidates(), timeout); mobileBriefFailureKind(err) != "timeout" {
		t.Fatalf("a model timeout lost its kind: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	_, _, err := scoreMorningBrief(ctx, "Bo", "", nil, nil, morningBriefTaxCandidates(), func(context.Context, string) (string, error) {
		calls++
		cancel()
		return "", context.Canceled
	})
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("a canceled brief retried scoring: calls=%d err=%v", calls, err)
	}
}

func TestMorningBriefFallbackOrderIsDeterministic(t *testing.T) {
	items := []morningBriefCandidate{
		{ID: "b-later", SourceKind: "handoff", Due: "2026-11-01T18:00:00Z", NeedsUser: true, Confidence: 1},
		{ID: "undated", SourceKind: "handoff", NeedsUser: true, Confidence: 1},
		{ID: "z-mail", SourceKind: "mail", Due: "2026-11-01T09:00:00Z", NeedsUser: true, Confidence: 1},
		{ID: "a-handoff", SourceKind: "handoff", Due: "2026-11-01T09:00:00Z", NeedsUser: true, Confidence: 1},
		{ID: "lead", SourceKind: "handoff", HardDueKnown: true, HardDueToday: true, NeedsUser: true, OnlyUserKnown: true, OnlyUser: true, Confidence: 1},
	}
	want := []string{"a-handoff", "z-mail", "b-later"}
	for range 3 {
		selected := morningBriefFallbackSelection(items)
		if selected.Lead == nil || selected.Lead.ID != "lead" || len(selected.Needs) != 3 {
			t.Fatalf("selection = %+v", selected)
		}
		for i, item := range selected.Needs {
			if item.ID != want[i] {
				t.Fatalf("fallback needs = %+v, want %v", selected.Needs, want)
			}
		}
		items[0], items[3] = items[3], items[0]
	}
}
