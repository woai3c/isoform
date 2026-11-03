package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func optionalMemoryObservation(title string, points ...string) string {
	return "# " + title + "\n\n- **Source**: activity\n\n## Memory Points\n\n- " + strings.Join(points, "\n- ") + "\n"
}

func TestMemoryOptionalSemanticsOfflineForgetAndReject(t *testing.T) {
	for _, operation := range []string{"forget", "reject", "preview-bound reject"} {
		t.Run(operation, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			app := &App{runner: nil}
			content := optionalMemoryObservation("Private preference", "Prefers dark mode.")
			if _, err := app.saveMemoryEntry("private", content, false); err != nil {
				t.Fatal(err)
			}
			var err error
			switch operation {
			case "reject":
				err = app.RejectMemoryAttribution("private", "Not about me")
			default:
				preview, previewErr := app.GetMemoryForgetPreview("private")
				if previewErr != nil {
					t.Fatalf("offline preview failed: %v", previewErr)
				}
				if preview.RetentionDays != 30 || len(preview.Items) != 1 {
					t.Fatalf("offline preview omitted deletion scope: %+v", preview)
				}
				switch operation {
				case "preview-bound reject":
					err = app.RejectMemoryAttributionWithPreview("private", "Not about me", preview.ConfirmationToken)
				default:
					err = app.ForgetMemoryEntry("private", preview.ConfirmationToken)
				}
			}
			if err != nil {
				t.Fatalf("offline %s failed: %v", operation, err)
			}
			dir, _ := memoryDir()
			if _, err := os.Stat(filepath.Join(dir, "private.md")); !os.IsNotExist(err) {
				t.Fatalf("offline %s retained memory: %v", operation, err)
			}
			if _, err := app.saveMemoryEntry("observed-again", content, false); !errors.Is(err, errMemoryForgotten) {
				t.Fatalf("offline %s did not suppress re-observation: %v", operation, err)
			}
		})
	}
}

func TestMemoryOptionalSemanticsDeletionNeverCallsCanonicalizer(t *testing.T) {
	for _, reject := range []bool{false, true} {
		name := "forget"
		if reject {
			name = "reject"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			app := &App{}
			if _, err := app.saveMemoryEntry("private", optionalMemoryObservation("Private preference", "Prefers dark mode."), false); err != nil {
				t.Fatal(err)
			}
			calls := 0
			app.memoryCanonicalizer = func(string, string) (string, error) {
				calls++
				return "", errors.New("provider unavailable")
			}
			var err error
			switch reject {
			case true:
				err = app.RejectMemoryAttribution("private", "Not about me")
			default:
				preview, previewErr := app.GetMemoryForgetPreview("private")
				if previewErr != nil {
					err = previewErr
				} else {
					err = app.ForgetMemoryEntry("private", preview.ConfirmationToken)
				}
			}
			if err != nil || calls != 0 {
				t.Fatalf("local %s contacted semantic provider: calls=%d err=%v", name, calls, err)
			}
		})
	}
}

func TestMemoryOptionalSemanticsUnavailableProviderDoesNotBlockUnrelatedMemory(t *testing.T) {
	for _, model := range []string{"configured", "different-provider/model"} {
		t.Run(model, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			app := &App{memoryCanonicalizer: func(string, string) (string, error) {
				return "", errors.New("provider unavailable")
			}}
			dir, err := memoryDir()
			if err != nil {
				t.Fatal(err)
			}
			pinnedModel := model
			if pinnedModel == "configured" {
				pinnedModel = effectiveLightTaskModel(app)
			}
			prepared := memoryForgetSemantics{Version: memorySemanticVersion, Model: pinnedModel, Hashes: []string{memoryForgetHash("prepared fact")}}
			removed := optionalMemoryObservation("Display", "Prefers dark mode.")
			if err := recordMemoryTombstone(dir, "removed", []string{removed}, time.Now(), prepared); err != nil {
				t.Fatal(err)
			}
			unrelated := optionalMemoryObservation("Calendar", "Checks the calendar each morning.")
			if _, err := app.saveMemoryEntry("unrelated", unrelated, false); err != nil {
				t.Fatalf("optional semantic provider blocked unrelated observation: %v", err)
			}
			if forgotten, err := isMemoryForgotten(dir, "external-unrelated", unrelated, time.Now()); err != nil || forgotten {
				t.Fatalf("optional semantic provider blocked unrelated read: forgotten=%v err=%v", forgotten, err)
			}
		})
	}
}

func TestMemoryOptionalSemanticsOfflineFactOverlapAndBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name      string
		forgotten []string
		candidate string
		want      bool
	}{
		{"forgotten fact inside larger record", []string{"Prefers dark mode."}, optionalMemoryObservation("Habits", "Prefers dark mode.", "Checks the calendar each morning."), true},
		{"forgotten sentence inside plain text", []string{"Prefers dark mode."}, "# Habits\n\n- **Source**: activity\n\nPrefers dark mode. Checks the calendar each morning.\n", true},
		{"one fact from forgotten multifactual record", []string{"Prefers dark mode.", "Uses keyboard navigation."}, optionalMemoryObservation("Navigation", "Uses keyboard navigation."), true},
		{"same title only", []string{"Prefers dark mode."}, optionalMemoryObservation("Display", "Uses a large monitor."), false},
		{"unrelated fact", []string{"Prefers dark mode."}, optionalMemoryObservation("Calendar", "Checks the calendar each morning."), false},
		{"opposite polarity", []string{"Prefers dark mode."}, optionalMemoryObservation("Display", "Does not prefer dark mode."), false},
		{"different subject", []string{"Prefers dark mode."}, optionalMemoryObservation("Display", "Alice prefers dark mode."), false},
		{"different value", []string{"Prefers dark mode."}, optionalMemoryObservation("Display", "Prefers light mode."), false},
		{"qualified fact", []string{"Prefers dark mode."}, optionalMemoryObservation("Display", "Prefers dark mode only at night."), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			removed := optionalMemoryObservation("Display", tc.forgotten...)
			if err := recordMemoryTombstone(dir, "removed", []string{removed}, time.Now()); err != nil {
				t.Fatal(err)
			}
			forgotten, err := isMemoryForgotten(dir, "new-observation", tc.candidate, time.Now())
			if err != nil || forgotten != tc.want {
				t.Fatalf("fact suppression=%v err=%v want=%v", forgotten, err, tc.want)
			}
		})
	}
}

func TestMemoryOptionalSemanticsOrderedListsDoNotShareOrdinals(t *testing.T) {
	dir := t.TempDir()
	original := "# Display\n\n- **Source**: activity\n\n1. Prefers dark mode.\n"
	if err := recordMemoryTombstone(dir, "removed", []string{original}, time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, point string
		want        bool
	}{
		{"same ordinal unrelated fact", "1. Checks the calendar each morning.", false},
		{"same ordinal forgotten fact", "1. Prefers dark mode.", true},
		{"different ordinal forgotten fact", "2. Prefers dark mode.", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := "# New observation\n\n- **Source**: activity\n\n" + tc.point + "\n"
			forgotten, err := isMemoryForgotten(dir, "new", candidate, time.Now())
			if err != nil || forgotten != tc.want {
				t.Fatalf("ordered-list suppression=%v err=%v want=%v", forgotten, err, tc.want)
			}
		})
	}
}

func TestMemoryOptionalSemanticsPreparedFactOverlap(t *testing.T) {
	// Receipt-matching unit fixtures, not live model integration tests. Explicit
	// preparation models evidence cached before the user asks to forget.
	dark := memorySemanticFact{Subject: "self", Relation: "prefer", Value: "dark mode", Polarity: "positive", Qualifiers: []string{}}
	keyboard := memorySemanticFact{Subject: "self", Relation: "use", Value: "keyboard navigation", Polarity: "positive", Qualifiers: []string{}}
	calendar := memorySemanticFact{Subject: "self", Relation: "check", Value: "calendar", Polarity: "positive", Qualifiers: []string{"each morning"}}
	for _, tc := range []struct {
		name           string
		original       string
		originalFacts  []memorySemanticFact
		candidate      string
		candidateFacts []memorySemanticFact
	}{
		{"paraphrase inside larger record", optionalMemoryObservation("Display", "Prefers dark mode."), []memorySemanticFact{dark}, optionalMemoryObservation("Habits", "Likes dark UI themes.", "Reviews the day's calendar at breakfast."), []memorySemanticFact{dark, calendar}},
		{"paraphrase of one forgotten fact", optionalMemoryObservation("Habits", "Prefers dark mode.", "Uses keyboard navigation."), []memorySemanticFact{dark, keyboard}, optionalMemoryObservation("Navigation", "Navigates with keyboard shortcuts."), []memorySemanticFact{keyboard}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			response := semanticReviewResponse(t, tc.originalFacts...)
			app := &App{memoryCanonicalizer: func(string, string) (string, error) { return response, nil }}
			if _, err := app.saveMemoryEntry("removed", tc.original, false); err != nil {
				t.Fatal(err)
			}
			model := effectiveLightTaskModel(app)
			if _, err := app.canonicalMemoryHash(tc.original, model); err != nil {
				t.Fatal(err)
			}
			response = semanticReviewResponse(t, tc.candidateFacts...)
			if _, err := app.canonicalMemoryHash(tc.candidate, model); err != nil {
				t.Fatal(err)
			}
			preview, err := app.GetMemoryForgetPreview("removed")
			if err != nil {
				t.Fatal(err)
			}
			if err := app.ForgetMemoryEntry("removed", preview.ConfirmationToken); err != nil {
				t.Fatal(err)
			}
			if _, err := app.saveMemoryEntry("new-observation", tc.candidate, false); !errors.Is(err, errMemoryForgotten) {
				t.Fatalf("forgotten atomic meaning was restored: %v", err)
			}
		})
	}
}

func TestMemoryOptionalSemanticsPreservesIndependentRecordAndUserEdit(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir, err := memoryDir()
	if err != nil {
		t.Fatal(err)
	}
	content := optionalMemoryObservation("Display", "Prefers dark mode.")
	retained := optionalMemoryObservation("Independent observation", "Prefers dark mode.", "Uses a large monitor.")
	if err := os.WriteFile(filepath.Join(dir, "retained.md"), []byte(retained), 0600); err != nil {
		t.Fatal(err)
	}
	if err := recordMemoryTombstone(dir, "removed", []string{content}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if forgotten, err := isMemoryForgotten(dir, "retained", retained, time.Now()); err != nil || forgotten {
		t.Fatalf("independent preexisting record suppressed: %v, %v", forgotten, err)
	}
	app := &App{}
	if _, err := app.SaveMemoryEntry("explicit-correction", content); err != nil {
		t.Fatalf("explicit user edit suppressed: %v", err)
	}
}

func TestMemoryOptionalSemanticsProviderSwitchBeforeForgetRetainsPreparedFacts(t *testing.T) {
	// Unit fixture: a fact receipt was prepared by the previous provider; the
	// provider used after forgetting returns that same fact alongside a new one.
	t.Setenv("HOME", t.TempDir())
	app := &App{}
	original := optionalMemoryObservation("Display", "Prefers dark mode.")
	if _, err := app.saveMemoryEntry("removed", original, false); err != nil {
		t.Fatal(err)
	}
	dark := memorySemanticFact{Subject: "self", Relation: "prefer", Value: "dark mode", Polarity: "positive", Qualifiers: []string{}}
	calendar := memorySemanticFact{Subject: "self", Relation: "check", Value: "calendar", Polarity: "positive", Qualifiers: []string{"each morning"}}
	response := semanticReviewResponse(t, dark)
	calls := 0
	app.memoryCanonicalizer = func(string, string) (string, error) {
		calls++
		return response, nil
	}
	currentModel := effectiveLightTaskModel(app)
	previousModel := currentModel + "-previous-provider"
	if _, err := app.canonicalMemoryHash(original, previousModel); err != nil {
		t.Fatal(err)
	}
	dir, _ := memoryDir()
	if _, ok := cachedMemorySemanticHash(dir, currentModel, original); ok {
		t.Fatal("fixture unexpectedly has a current-provider receipt")
	}
	preview, err := app.GetMemoryForgetPreview("removed")
	if err != nil {
		t.Fatal(err)
	}
	if err := app.ForgetMemoryEntry("removed", preview.ConfirmationToken); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("provider switch triggered canonicalization while forgetting: calls=%d", calls)
	}
	response = semanticReviewResponse(t, dark, calendar)
	candidate := optionalMemoryObservation("Habits", "Likes dark UI themes.", "Reviews the day's calendar at breakfast.")
	if _, err := app.saveMemoryEntry("new-observation", candidate, false); !errors.Is(err, errMemoryForgotten) {
		t.Fatalf("previous-provider fact was restored inside new-provider observation: %v", err)
	}
}
