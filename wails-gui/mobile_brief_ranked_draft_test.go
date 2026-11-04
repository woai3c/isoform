package main

import (
	"strings"
	"testing"
)

func TestMorningBriefDraftOnlyCitesSelectedItems(t *testing.T) {
	lead := morningBriefCandidate{ID: "tax", Refs: []string{"e1"}}
	other := morningBriefCandidate{ID: "posts", Refs: []string{"e2", "e3"}}
	selection := morningBriefSelection{Lead: &lead, Needs: []morningBriefCandidate{other}}
	raw := `{"paragraphs":[{"text":"Pay the tax. It is due today.","items":["tax"]},{"text":"Review the posts.","items":["posts"]}]}`
	draft, err := parseMorningBriefDraft(raw, selection)
	if err != nil || len(draft.Paragraphs) != 2 || len(draft.Paragraphs[1].Refs) != 2 || draft.Paragraphs[0].Refs[0] != "e1" {
		t.Fatalf("draft=%+v err=%v", draft, err)
	}
	for _, invalid := range []string{
		`{"paragraphs":[{"text":"Review the posts.","items":["posts"]}]}`,
		`{"paragraphs":[{"text":"Pay the tax.","items":["tax","calendar"]}]}`,
		`{"paragraphs":[{"text":"Pay the tax.","items":["tax","tax"]}]}`,
		`{"paragraphs":[{"text":"Pay the tax.","items":["tax"]}],"extra":true}`,
		`{"paragraphs":[{"text":"` + strings.Repeat("a ", 151) + `","items":["tax"]},{"text":"` + strings.Repeat("a ", 150) + `","items":["posts"]}]}`,
	} {
		if _, err := parseMorningBriefDraft(invalid, selection); err == nil {
			t.Fatalf("invalid draft was accepted: %s", invalid)
		}
	}
}

func TestMorningBriefDraftTrimsTrailingContent(t *testing.T) {
	lead := morningBriefCandidate{ID: "garden", Refs: []string{"e1"}}
	raw := `{"paragraphs":[{"text":"Review the garden plan.","items":["garden"]}]} Additional notes should not appear.`
	draft, err := parseMorningBriefDraft(raw, morningBriefSelection{Lead: &lead})
	if err != nil || len(draft.Paragraphs) != 1 || draft.Paragraphs[0].Text != "Review the garden plan." || len(draft.Paragraphs[0].Refs) != 1 {
		t.Fatalf("draft=%+v err=%v", draft, err)
	}
}
