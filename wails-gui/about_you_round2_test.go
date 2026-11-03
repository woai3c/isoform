package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAboutYouRound2AutomatedConfirmationTracksWords(t *testing.T) {
	for _, format := range []string{"legacy", "okf", "converted"} {
		for _, change := range []string{"metadata", "metadata-title", "words", "heading", "body-heading", "bold-fact"} {
			t.Run(format+"/"+change, func(t *testing.T) {
				t.Setenv("HOME", t.TempDir())
				body := "## Memory Points\n\n- I prefer concise reviews.\n\n## Detail\n\nInclude examples.\n"
				if change == "body-heading" {
					body = "# Body heading\n\n" + body
				}
				if change == "bold-fact" {
					body = "- **Preference**: Include examples.\n\n" + body
				}
				original := "# Review preference\n\n- **Source**: activity\n- **Updated**: 2026-10-01 10:00\n- **Activity Session**: old-session\n\n" + body
				candidate := strings.Replace(original, "old-session", "new-session", 1)
				if format == "okf" {
					original = renderOKFDocument(map[string]any{"type": "Isoform Memory", "title": "Review preference", "source": "activity", "activity_sessions": []string{"old-session"}}, body)
					candidate = strings.Replace(original, "old-session", "new-session", 1)
				}
				if format == "converted" {
					original = memoryWithResolvedProvenance("belief", original, memoryReadSource("activity"))
					candidate = strings.Replace(original, "old-session", "new-session", 1)
				}
				at := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
				original, err := setMemoryConfirmedAt(original, at)
				if err != nil {
					t.Fatal(err)
				}
				// A writer may start from the old file, including its stale field.
				candidate, err = setMemoryConfirmedAt(candidate, at)
				if err != nil {
					t.Fatal(err)
				}
				switch change {
				case "metadata-title":
					candidate = strings.Replace(candidate, "Review preference", "Reviews", 1)
				case "words":
					candidate = strings.Replace(candidate, "concise", "detailed", 1)
				case "heading":
					candidate = strings.Replace(candidate, "## Detail", "## New detail", 1)
				case "body-heading":
					candidate = strings.Replace(candidate, "# Body heading", "# Changed heading", 1)
				case "bold-fact":
					candidate = strings.Replace(candidate, "Include examples.", "Skip examples.", 1)
				}
				dir, _ := memoryDir()
				if err := os.WriteFile(filepath.Join(dir, "belief.md"), []byte(original), 0600); err != nil {
					t.Fatal(err)
				}
				app := &App{}
				got, err := app.saveMemoryEntry("belief", candidate, false)
				if err != nil {
					t.Fatal(err)
				}
				want := int64(0)
				if strings.HasPrefix(change, "metadata") {
					want = at.UnixMilli()
				}
				if memoryConfirmedAt(got.Content) != want {
					t.Fatalf("confirmedAt=%d, want %d", memoryConfirmedAt(got.Content), want)
				}
				if want == 0 && strings.Contains(got.Content, "confirmed_at:") {
					t.Fatal("stale field retained")
				}
				view, err := app.GetAboutYou()
				if err != nil || len(view.Beliefs) != 1 {
					t.Fatalf("view=%+v %v", view, err)
				}
				if (view.Beliefs[0].Status == "confirmed") != (want != 0) {
					t.Fatalf("status=%s", view.Beliefs[0].Status)
				}
			})
		}
	}
}

func TestAboutYouRound2UserEditConfirmsChangedWords(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app := &App{}
	got, err := app.SaveMemoryEntry("belief", "# Review preference\n\nI prefer concise reviews.")
	if err != nil {
		t.Fatal(err)
	}
	got, err = app.SaveMemoryEntry("belief", strings.Replace(got.Content, "concise", "detailed", 1))
	if err != nil {
		t.Fatal(err)
	}
	if memoryConfirmedAt(got.Content) == 0 || got.Source != "user_edit" {
		t.Fatal("user edit was not confirmed")
	}
	view, err := app.GetAboutYou()
	if err != nil || view.Beliefs[0].Status != "confirmed" || view.Beliefs[0].Confidence != 4 {
		t.Fatalf("view=%+v %v", view, err)
	}
}

func TestAboutYouRound2GeneratedWritersTrackConfirmedWords(t *testing.T) {
	for _, path := range []string{"activity-retry", "activity-update", "native-retry", "native-update"} {
		for _, changed := range []bool{false, true} {
			name := "same-words"
			if changed {
				name = "changed-words"
			}
			t.Run(path+"/"+name, func(t *testing.T) {
				app := newMCPReadTestApp(t)
				at := seedMCPReadActivity(t, app, "confirmation-session", "Editor", "com.example.editor", "ordinary", "", "")
				session := ActivitySessionRow{ID: "confirmation-session", StartTime: at, AppNames: `["Editor"]`}
				group := sessionGroup{Sessions: []ActivitySessionRow{session}}
				triager := &ActivityTriager{app: app}
				generator := NewMemoryGenerator(app)
				response := &mcMemoryGenerationResponse{Title: "Review preference", MemoryPoints: []string{"Concise reviews."}, Dimension: "user_insight"}
				var id string
				var err error
				if strings.HasPrefix(path, "activity-") {
					id, err = triager.saveActivityMemory(session, group, response)
				} else {
					id, err = generator.saveMemoryFile(memorySourceConversation, "confirmation-thread", response)
				}
				if err != nil {
					t.Fatal(err)
				}
				confirmed, err := app.ConfirmMemoryEntry(id)
				if err != nil {
					t.Fatal(err)
				}
				if changed {
					response.MemoryPoints = []string{"Detailed reviews."}
				}
				switch path {
				case "activity-retry":
					_, err = triager.saveActivityMemory(session, group, response)
				case "activity-update":
					_, err = triager.updateActivityMemory(t.Context(), id, session, group, response)
				case "native-retry":
					_, err = generator.saveMemoryFile(memorySourceConversation, "confirmation-thread", response)
				case "native-update":
					err = generator.updateMemoryFile(t.Context(), id, memorySourceConversation, "confirmation-thread", response, "")
				}
				if err != nil {
					t.Fatal(err)
				}
				got, err := app.GetMemoryEntry(id)
				if err != nil {
					t.Fatal(err)
				}
				want := memoryConfirmedAt(confirmed.Content)
				if changed {
					want = 0
				}
				if memoryConfirmedAt(got.Content) != want {
					t.Fatalf("confirmedAt=%d want %d", memoryConfirmedAt(got.Content), want)
				}
				if changed && !strings.Contains(got.Content, "Detailed reviews.") {
					t.Fatal("fixture did not change the words")
				}
			})
		}
	}
}

func TestAboutYouRound2SourceBookkeepingAndLegacyConversion(t *testing.T) {
	legacy := "# Review preference\n\n- **Source**: activity\n- **Activity Session**:\n  - **Update 2026-10-01** (initial)\n    - old-session\n\n## Memory Points\n\n- Concise reviews.\n"
	confirmed, err := setMemoryConfirmedAt(legacy, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	converted := memoryWithResolvedProvenance("belief", confirmed, memoryReadSource("activity"))
	for _, candidate := range []string{converted, strings.Replace(converted, "Concise", "Detailed", 1)} {
		got := withMemoryObservationSourceID(candidate, confirmed, "new-source")
		want := memoryConfirmedAt(confirmed)
		if strings.Contains(candidate, "Detailed") {
			want = 0
		}
		if memoryConfirmedAt(got) != want {
			t.Fatalf("confirmedAt=%d want %d", memoryConfirmedAt(got), want)
		}
	}
}

func TestAboutYouRound2NativeTranslationDropsOldConfirmation(t *testing.T) {
	app := newMCPReadTestApp(t)
	generator := NewMemoryGenerator(app)
	response := &mcMemoryGenerationResponse{Title: "Review preference", MemoryPoints: []string{"Concise reviews."}, Dimension: "user_insight"}
	id, err := generator.saveMemoryFile(memorySourceConversation, "confirmation-thread", response)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.ConfirmMemoryEntry(id); err != nil {
		t.Fatal(err)
	}
	if err := app.SaveUIState("ui.language", "zh-CN"); err != nil {
		t.Fatal(err)
	}
	// The canonicalizer leaves the old words unchanged; only the subsequent
	// translation changes them. This exercises the final write reconciliation.
	runner := &memoryTranslationFakeRunner{ready: true, responses: []string{
		`{"memoryPoints":["Concise reviews."]}`,
		"# 审阅偏好\n\n## Memory Points\n\n- 简洁的审阅。\n",
	}}
	// Keep optional semantic matching on its unavailable path, as the
	// existing translation fixtures do; reserve these responses for the two
	// word-producing steps under test.
	app.memoryCanonicalizer = func(string, string) (string, error) { return "", errMemorySemanticUnverified }
	app.runner = runner
	if err := generator.updateMemoryFile(t.Context(), id, memorySourceConversation, "confirmation-thread", response, ""); err != nil {
		t.Fatal(err)
	}
	got, err := app.GetMemoryEntry(id)
	if err != nil {
		t.Fatal(err)
	}
	if runner.calls != 2 || !strings.Contains(got.Content, "简洁的审阅") {
		t.Fatalf("fixture did not translate: calls=%d content=%q", runner.calls, got.Content)
	}
	if memoryConfirmedAt(got.Content) != 0 {
		t.Fatal("translation retained confirmation of the old words")
	}
}
