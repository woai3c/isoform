package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// countMeetingNotesPasses stands in for the two writers and counts the pass
// each runs.
func countMeetingNotesPasses(t *testing.T) (knowledge, memory *atomic.Int32) {
	t.Helper()
	priorK, priorM := meetingNotesLearnKnowledge, meetingNotesLearnMemory
	t.Cleanup(func() { meetingNotesLearnKnowledge, meetingNotesLearnMemory = priorK, priorM })
	knowledge, memory = &atomic.Int32{}, &atomic.Int32{}
	meetingNotesLearnKnowledge = func(_ *ActivityTriager, _ context.Context, _ sessionGroup, _ string, _ SessionContentProfile) {
		knowledge.Add(1)
	}
	meetingNotesLearnMemory = func(_ *ActivityTriager, _ context.Context, _ sessionGroup, _ string, _ SessionContentProfile) {
		memory.Add(1)
	}
	return knowledge, memory
}

// saveSwitchTestNotes stores one meeting's notes with an outline to learn
// from.
func saveSwitchTestNotes(t *testing.T, app *App, key, sessionID string) {
	t.Helper()
	session := saveMeetingSlice(t, app, sessionID, time.Now().Add(-time.Hour), 10, "")
	if err := app.activity.store.saveMeetingNotesSlice(meetingNotesHeader{Key: key, MeetingApp: "Zoom"}, session.ID, session.StartTime, session.EndTime, 10); err != nil {
		t.Fatal(err)
	}
	if err := app.activity.store.saveMeetingNotesOutline(session.ID, "", studioOutline); err != nil {
		t.Fatal(err)
	}
}

func TestMeetingNotesLearnTheMissingKindWhenASwitchTurnsOn(t *testing.T) {
	app := newMeetingNotesTestApp(t)
	store := app.activity.store
	ctx := context.Background()
	if err := app.store.SetUIState(ctx, triageEnableMemoryKey, "false"); err != nil {
		t.Fatal(err)
	}
	saveSwitchTestNotes(t, app, "meeting-K1", "switch-k1-1")
	knowledge, memory := countMeetingNotesPasses(t)

	// Only "How you do things" is on: the notes are learned for knowledge.
	if ran, err := app.learnFromMeetingNotes(ctx, "meeting-K1"); !ran || err != nil {
		t.Fatalf("first learn: ran=%v err=%v", ran, err)
	}
	if knowledge.Load() != 1 || memory.Load() != 0 {
		t.Fatalf("knowledge=%d memory=%d, want 1 0", knowledge.Load(), memory.Load())
	}
	r, _, _ := store.meetingNotesByKey("meeting-K1")
	if r.LearnedHash == "" || r.LearnedKinds != meetingNotesKindKnowledge {
		t.Fatalf("learned hash=%q kinds=%q, want kinds %q", r.LearnedHash, r.LearnedKinds, meetingNotesKindKnowledge)
	}

	// Turning "About you" on learns the missing kind, and only it.
	if err := app.store.SetUIState(ctx, triageEnableMemoryKey, "true"); err != nil {
		t.Fatal(err)
	}
	app.learnPendingMeetingNotes(ctx)
	if knowledge.Load() != 1 || memory.Load() != 1 {
		t.Fatalf("knowledge=%d memory=%d, want 1 1", knowledge.Load(), memory.Load())
	}
	r, _, _ = store.meetingNotesByKey("meeting-K1")
	if r.LearnedKinds != meetingNotesKindKnowledge+","+meetingNotesKindMemory {
		t.Fatalf("kinds=%q, want both", r.LearnedKinds)
	}

	// Both kinds learned: nothing runs again.
	app.learnPendingMeetingNotes(ctx)
	if ran, _ := app.learnFromMeetingNotes(ctx, "meeting-K1"); ran {
		t.Fatal("fully learned notes were learned again")
	}
	if knowledge.Load() != 1 || memory.Load() != 1 {
		t.Fatalf("knowledge=%d memory=%d, want 1 1", knowledge.Load(), memory.Load())
	}
}

func TestMeetingNotesLearnOnceWhenBothSwitchesAreOn(t *testing.T) {
	app := newMeetingNotesTestApp(t)
	store := app.activity.store
	ctx := context.Background()
	saveSwitchTestNotes(t, app, "meeting-B1", "switch-b1-1")
	knowledge, memory := countMeetingNotesPasses(t)

	if ran, err := app.learnFromMeetingNotes(ctx, "meeting-B1"); !ran || err != nil {
		t.Fatalf("first learn: ran=%v err=%v", ran, err)
	}
	if knowledge.Load() != 1 || memory.Load() != 1 {
		t.Fatalf("knowledge=%d memory=%d, want 1 1", knowledge.Load(), memory.Load())
	}
	r, _, _ := store.meetingNotesByKey("meeting-B1")
	if r.LearnedKinds != meetingNotesKindKnowledge+","+meetingNotesKindMemory {
		t.Fatalf("kinds=%q, want both", r.LearnedKinds)
	}
	app.learnPendingMeetingNotes(ctx)
	if ran, _ := app.learnFromMeetingNotes(ctx, "meeting-B1"); ran || knowledge.Load() != 1 || memory.Load() != 1 {
		t.Fatalf("rerun: ran=%v knowledge=%d memory=%d", ran, knowledge.Load(), memory.Load())
	}
}

func TestMeetingNotesLearnChangedNotesAgainForTheKindsThatAreOn(t *testing.T) {
	app := newMeetingNotesTestApp(t)
	store := app.activity.store
	ctx := context.Background()
	sessionID := "switch-c1-1"
	saveSwitchTestNotes(t, app, "meeting-C1", sessionID)
	knowledge, memory := countMeetingNotesPasses(t)

	if _, err := app.learnFromMeetingNotes(ctx, "meeting-C1"); err != nil {
		t.Fatal(err)
	}
	// Changed notes are learned again for every kind whose switch is on.
	if err := store.saveMeetingNotesOutline(sessionID, "", meetingFollowupExtraction{Summary: "We picked the annual plan."}); err != nil {
		t.Fatal(err)
	}
	if ran, err := app.learnFromMeetingNotes(ctx, "meeting-C1"); !ran || err != nil {
		t.Fatalf("changed notes: ran=%v err=%v", ran, err)
	}
	if knowledge.Load() != 2 || memory.Load() != 2 {
		t.Fatalf("knowledge=%d memory=%d, want 2 2", knowledge.Load(), memory.Load())
	}
	// A kind whose switch is off does not run, and the version records
	// exactly the kinds that were learned.
	if err := app.store.SetUIState(ctx, triageEnableMemoryKey, "false"); err != nil {
		t.Fatal(err)
	}
	if err := store.saveMeetingNotesOutline(sessionID, "", meetingFollowupExtraction{Summary: "We kept the monthly plan."}); err != nil {
		t.Fatal(err)
	}
	if ran, err := app.learnFromMeetingNotes(ctx, "meeting-C1"); !ran || err != nil {
		t.Fatalf("changed notes, memory off: ran=%v err=%v", ran, err)
	}
	if knowledge.Load() != 3 || memory.Load() != 2 {
		t.Fatalf("knowledge=%d memory=%d, want 3 2", knowledge.Load(), memory.Load())
	}
	r, _, _ := store.meetingNotesByKey("meeting-C1")
	if r.LearnedKinds != meetingNotesKindKnowledge {
		t.Fatalf("kinds=%q, want %q", r.LearnedKinds, meetingNotesKindKnowledge)
	}
}

func TestMeetingNotesLearnedBeforeKindsWereRecordedDoNotRelearn(t *testing.T) {
	app := newMeetingNotesTestApp(t)
	store := app.activity.store
	ctx := context.Background()
	saveSwitchTestNotes(t, app, "meeting-O1", "switch-o1-1")
	knowledge, memory := countMeetingNotesPasses(t)

	// A row from before learned_kinds existed: the version was learned, the
	// kinds are unknown.
	r, _, _ := store.meetingNotesByKey("meeting-O1")
	hash := meetingNotesLearningHash(meetingNotesLearningText(r))
	if _, err := store.db.Exec(`UPDATE meeting_notes SET learned_hash = ?, learned_kinds = '' WHERE meeting_key = ?`, hash, r.Key); err != nil {
		t.Fatal(err)
	}
	app.learnPendingMeetingNotes(ctx)
	if ran, _ := app.learnFromMeetingNotes(ctx, "meeting-O1"); ran || knowledge.Load() != 0 || memory.Load() != 0 {
		t.Fatalf("rerun of an upgraded row: ran=%v knowledge=%d memory=%d", ran, knowledge.Load(), memory.Load())
	}
}

func TestMeetingNotesLearnNothingWhenBothSwitchesAreOff(t *testing.T) {
	app := newMeetingNotesTestApp(t)
	store := app.activity.store
	ctx := context.Background()
	for _, key := range []string{triageEnableKnowledgeKey, triageEnableMemoryKey} {
		if err := app.store.SetUIState(ctx, key, "false"); err != nil {
			t.Fatal(err)
		}
	}
	saveSwitchTestNotes(t, app, "meeting-N1", "switch-n1-1")
	knowledge, memory := countMeetingNotesPasses(t)

	app.learnPendingMeetingNotes(ctx)
	if ran, err := app.learnFromMeetingNotes(ctx, "meeting-N1"); ran || err != nil {
		t.Fatalf("both off: ran=%v err=%v", ran, err)
	}
	if knowledge.Load() != 0 || memory.Load() != 0 {
		t.Fatalf("knowledge=%d memory=%d, want 0 0", knowledge.Load(), memory.Load())
	}
	r, _, _ := store.meetingNotesByKey("meeting-N1")
	if r.LearnedHash != "" || r.LearnedKinds != "" {
		t.Fatalf("marked learned with both switches off: hash=%q kinds=%q", r.LearnedHash, r.LearnedKinds)
	}
}

func TestMeetingNotesLearnWhenTheSettingsSwitchTurnsOn(t *testing.T) {
	app := newMeetingNotesTestApp(t)
	store := app.activity.store
	ctx := context.Background()
	if err := app.store.SetUIState(ctx, triageEnableMemoryKey, "false"); err != nil {
		t.Fatal(err)
	}
	saveSwitchTestNotes(t, app, "meeting-S1", "switch-s1-1")
	knowledge, memory := countMeetingNotesPasses(t)
	if _, err := app.learnFromMeetingNotes(ctx, "meeting-S1"); err != nil {
		t.Fatal(err)
	}
	if knowledge.Load() != 1 || memory.Load() != 0 {
		t.Fatalf("knowledge=%d memory=%d, want 1 0", knowledge.Load(), memory.Load())
	}

	// The Settings switch saves through SaveUIState; turning it on learns
	// the missing kind in the background.
	if err := app.SaveUIState(triageEnableMemoryKey, "true"); err != nil {
		t.Fatal(err)
	}
	both := meetingNotesKindKnowledge + "," + meetingNotesKindMemory
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		r, _, _ := store.meetingNotesByKey("meeting-S1")
		if memory.Load() == 1 && r.LearnedKinds == both {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if knowledge.Load() != 1 || memory.Load() != 1 {
		t.Fatalf("knowledge=%d memory=%d, want 1 1", knowledge.Load(), memory.Load())
	}
	r, _, _ := store.meetingNotesByKey("meeting-S1")
	if r.LearnedKinds != both {
		t.Fatalf("kinds=%q, want both", r.LearnedKinds)
	}
}
