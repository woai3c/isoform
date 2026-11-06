package main

import "testing"

// Guards AR62: a redaction tag never reaches an Activity title, in a new model
// title or in a title saved before the fix.
func TestActivityPrivacyMarkerStripWritesAround(t *testing.T) {
	cases := map[string]string{
		"Isoform · [REDACTED:code] release":   "Isoform release",
		"Isoform: [REDACTED:code] release":    "Isoform release",
		"Pick up [REDACTED:api_key]":          "Pick up",
		"[REDACTED:account_number]sed":        "",
		"[REDACTED:code":                      "",
		"[REDACTED:code]":                     "",
		"Review agent's rebuild":              "Review agent's rebuild",
		"Writing [REDACTED:email] memo":       "Writing memo",
		"[redacted:future_kind] Halcyon memo": "Halcyon memo",
	}
	for in, want := range cases {
		if got := activityPrivacyMarkerStrip(in); got != want {
			t.Errorf("activityPrivacyMarkerStrip(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestThreadCleanTitleNeverKeepsAMarker(t *testing.T) {
	got := threadCleanTitle(`"Isoform · [REDACTED:code] release"`)
	if got != "Isoform release" {
		t.Fatalf("threadCleanTitle kept a marker or lost words: %q", got)
	}
	if got := threadCleanTitle("[REDACTED:api_key]"); got != "" {
		t.Fatalf("a title that is only a marker must be empty so the moment title is used, got %q", got)
	}
}

func TestActivityThreadDisplayTitleReadGuardForSavedTitles(t *testing.T) {
	live := map[string]ActivityRow{"m1": {ID: "m1"}}
	// A title saved before the fix, with its moment still readable.
	saved := activityThreadRow{threadState: threadState{Title: "Isoform · [REDACTED:code] release"}, TitleBasis: []string{"m1"}}
	if got, user := activityThreadDisplayTitle(saved, live, "fallback"); user || got != "Isoform release" {
		t.Fatalf("saved title: got %q (user=%v), want %q", got, user, "Isoform release")
	}
	// A saved title with no basis left still never shows its tag.
	unbased := activityThreadRow{threadState: threadState{Title: "Isoform · [REDACTED:code] release"}}
	if got, _ := activityThreadDisplayTitle(unbased, live, "fallback"); got != "Isoform release" {
		t.Fatalf("unbased saved title: got %q", got)
	}
	// A title that is only a tag falls back to the moment title, and that is marker-free too.
	onlyTag := activityThreadRow{threadState: threadState{Title: "[REDACTED:code]"}}
	if got, _ := activityThreadDisplayTitle(onlyTag, live, "Ship [REDACTED:api_key] notes"); got != "Ship notes" {
		t.Fatalf("tag-only title falls back: got %q", got)
	}
	userOnlyTag := activityThreadRow{threadState: threadState{UserTitle: "[REDACTED:code]"}}
	if got, _ := activityThreadDisplayTitle(userOnlyTag, live, "fallback"); got != "fallback" {
		t.Fatalf("a user title that is only a tag falls back, got %q", got)
	}
}

func TestThreadOutcomeTextWritesAroundAModelTag(t *testing.T) {
	if got := threadOutcomeText("Shipped [REDACTED:code] release", 200); got != "Shipped release" {
		t.Fatalf("outcome line kept a marker or lost words: %q", got)
	}
}
