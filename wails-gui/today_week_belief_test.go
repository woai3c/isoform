package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func todayWeekBeliefFixture(t *testing.T) (*App, MobileBriefV1, string) {
	t.Helper()
	app, brief, now := yourWeekFrozenWindowFixture(t, []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday}, "2026-10-19", intPointer(5), 2, nil)
	before := "# A belief\n\n- **Source**: user_edit\n\n## Memory Points\n- Prefers mornings.\n"
	writeTodayContextMemory(t, "week-belief", before)
	ctx := context.Background()
	raw, err := app.store.GetUIState(ctx, yourWeekSnapshotKey+"2026-10-23")
	if err != nil {
		t.Fatal(err)
	}
	var snap yourWeekSnapshot
	if err := json.Unmarshal([]byte(raw), &snap); err != nil {
		t.Fatal(err)
	}
	entry, err := app.GetMemoryEntry("week-belief")
	if err != nil {
		t.Fatal(err)
	}
	snap.Memories = []yourWeekSnapshotRef{{ID: "week-belief", AtMs: entry.ModTime, Digest: todayWeekBeliefDigest(before)}}
	rawBytes, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.store.SetUIState(ctx, yourWeekSnapshotKey+"2026-10-23", string(rawBytes)); err != nil {
		t.Fatal(err)
	}
	service := app.ensureMobileBriefService().(*mobileBriefService)
	if err := service.store.save(brief, now); err != nil {
		t.Fatal(err)
	}
	view, err := app.GetTodayBriefByID(brief.BriefID)
	if err != nil || view.Brief == nil || view.Brief.YourWeek == nil || len(view.Brief.YourWeek.Learned) == 0 {
		t.Fatalf("fixture week not readable: %+v, %v", view.Brief, err)
	}
	return app, brief, before
}

func TestTodayWeekBeliefCorrectionAndUndo(t *testing.T) {
	for _, words := range []string{"", "I prefer afternoons."} {
		t.Run(words, func(t *testing.T) {
			app, brief, before := todayWeekBeliefFixture(t)
			token, err := app.CorrectTodayWeekBelief(brief.BriefID, "week-belief", "A belief", words, "test")
			if err != nil {
				t.Fatal(err)
			}
			entry, err := app.GetMemoryEntry("week-belief")
			if err != nil {
				t.Fatal(err)
			}
			if words == "" && memoryInUse(entry.Content) {
				t.Fatal("blank correction is still in use")
			}
			if words != "" && (!strings.Contains(entry.Content, words) || !memoryInUse(entry.Content)) {
				t.Fatalf("wording not saved: %s", entry.Content)
			}
			view, err := app.GetTodayBriefByID(brief.BriefID)
			if err != nil {
				t.Fatal(err)
			}
			if view.Brief != nil && view.Brief.YourWeek != nil && len(view.Brief.YourWeek.Learned) != 0 {
				t.Fatal("the old weekly belief survived its correction")
			}
			if strings.Contains(token, words) && words != "" {
				t.Fatal("receipt contains wording")
			}
			if !app.CanUndoTodayWeekBelief(token) {
				t.Fatal("valid undo receipt did not revalidate")
			}
			if app.CanUndoTodayWeekBelief(token+"x") || app.UndoTodayWeekBelief(token+"x", "test") == nil {
				t.Fatal("a modified undo receipt was accepted")
			}
			app.closedEyes.enabled.Store(true)
			if app.CanUndoTodayWeekBelief(token) {
				t.Fatal("receipt revalidated during pause")
			}
			if err := app.UndoTodayWeekBelief(token, "test"); err == nil {
				t.Fatal("undo bypassed observation pause")
			}
			app.closedEyes.enabled.Store(false)
			if err := app.UndoTodayWeekBelief(token, "test"); err != nil {
				t.Fatal(err)
			}
			entry, err = app.GetMemoryEntry("week-belief")
			if err != nil || entry.Content != before {
				t.Fatalf("undo = %q, %v", entry.Content, err)
			}
			view, err = app.GetTodayBriefByID(brief.BriefID)
			if err != nil || view.Brief == nil || view.Brief.YourWeek == nil || len(view.Brief.YourWeek.Learned) != 1 {
				t.Fatal("undo did not restore the weekly belief")
			}
			if err := app.UndoTodayWeekBelief(token, "test"); err == nil {
				t.Fatal("replayed undo")
			}
		})
	}
}

func TestTodayWeekBeliefRejectsMissingSelectionAndLaterEdit(t *testing.T) {
	app, brief, _ := todayWeekBeliefFixture(t)
	if _, err := app.CorrectTodayWeekBelief(brief.BriefID, "other", "A belief", "words", "test"); err == nil {
		t.Fatal("accepted a belief outside the week")
	}
	if _, err := app.CorrectTodayWeekBelief(brief.BriefID, "week-belief", "old label", "words", "test"); err == nil {
		t.Fatal("accepted a changed displayed belief")
	}
	token, err := app.CorrectTodayWeekBelief(brief.BriefID, "week-belief", "A belief", "Changed", "test")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := app.GetMemoryEntry("week-belief")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.SaveMemoryEntry("week-belief", entry.Content+"\nAnother edit.\n"); err != nil {
		t.Fatal(err)
	}
	if app.CanUndoTodayWeekBelief(token) {
		t.Fatal("stale receipt revalidated after another edit")
	}
	if err := app.UndoTodayWeekBelief(token, "test"); err == nil {
		t.Fatal("undo overwrote a later edit")
	}
}

func TestTodayWeekBeliefRewordsFirstSentenceWithoutTouchingMetadata(t *testing.T) {
	for _, tc := range []struct{ before, after string }{
		{"# Belief\n\n- **Source**: user_edit\n\n## Memory Points\n- First point. Second point.\n", "# Belief\n\n- **Source**: user_edit\n\n## Memory Points\n- Revised. Second point.\n"},
		{"---\nsource: manual\n---\n# Belief\n\nFirst thought. Another thought.\n", "---\nsource: manual\n---\n# Belief\n\nRevised. Another thought.\n"},
	} {
		if got := todayWeekRewordMemory(tc.before, "Revised."); got != tc.after {
			t.Fatalf("reworded = %q, want %q", got, tc.after)
		}
	}
}
