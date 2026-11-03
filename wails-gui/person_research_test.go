package main

import "testing"

func TestParseDossier_FullBlocks(t *testing.T) {
	raw := `Here is what I found.

<summary>Co-founder and CEO of Isoform.</summary>
<dossier>## Contact
- Email: bo@isoform.ai [1]

## Who They Are
- Founder of BentoML [2]

## Your Relationship
- The user's boss [1]</dossier>
<sources>[{"n":1,"title":"Isoform","url":"https://isoform.ai"},{"n":2,"title":"BentoML","url":"https://bentoml.com"}]</sources>`

	markdown, summary, sources := parseDossier(raw)

	if summary != "Co-founder and CEO of Isoform." {
		t.Errorf("summary = %q", summary)
	}
	if got := "## Contact"; len(markdown) == 0 || markdown[:len(got)] != got {
		t.Errorf("markdown should start with the Contact heading, got %q", markdown)
	}
	// Control blocks must not bleed into the rendered body.
	if containsAny(markdown, "<summary>", "<sources>", "<dossier>") {
		t.Errorf("markdown leaked a control block: %q", markdown)
	}
	if len(sources) != 2 {
		t.Fatalf("expected 2 sources, got %d", len(sources))
	}
	if sources[0].N != 1 || sources[0].URL != "https://isoform.ai" {
		t.Errorf("source[0] = %+v", sources[0])
	}
}

func TestParseDossier_MissingDossierBlockFallsBackToProse(t *testing.T) {
	raw := `## Contact
- Email: x@y.test

<summary>Just a person.</summary>
<sources>[]</sources>`

	markdown, summary, sources := parseDossier(raw)

	if summary != "Just a person." {
		t.Errorf("summary = %q", summary)
	}
	if containsAny(markdown, "<summary>", "<sources>") {
		t.Errorf("fallback markdown leaked a control block: %q", markdown)
	}
	if len(markdown) == 0 {
		t.Errorf("fallback markdown should retain the prose, got empty")
	}
	if len(sources) != 0 {
		t.Errorf("expected no sources, got %d", len(sources))
	}
}

func TestParseDossier_MalformedSourcesYieldsNone(t *testing.T) {
	raw := `<dossier>## Contact
- ok</dossier>
<sources>not json</sources>`

	markdown, _, sources := parseDossier(raw)
	if len(markdown) == 0 {
		t.Errorf("markdown should survive malformed sources")
	}
	if sources != nil {
		t.Errorf("malformed sources should yield nil, got %+v", sources)
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
	}
	return false
}
