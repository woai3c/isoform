package handoff

import (
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

func clockAt(ms int64) func() time.Time {
	return func() time.Time { return time.UnixMilli(ms) }
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s
}

func idsOf(entries []Entry) []string {
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.ID)
	}
	return out
}

// TestSaveGetRoundtrip is the load-bearing test: a colon in the title and
// `- **bold**:` bullets in the body must survive the frontmatter fence intact.
func TestSaveGetRoundtrip(t *testing.T) {
	s := newTestStore(t)
	in := Entry{
		ID:            "hf_1",
		Title:         "Draft Q2: pitch deck", // colon in scalar value
		Status:        StatusActive,
		Source:        SourceTriage,
		SourceID:      "sess_42",
		AppName:       "Zoom",
		Workdir:       "/Users/x/repo",
		KeyFiles:      []string{"a.go", "deck/intro.md"},
		ChatSessionID: "chat_7",
		CreatedAt:     1000,
		UpdatedAt:     1000,
		Body:          "# Objective\n\n- **Working directory**: /Users/x/repo\n\n---\n\nDo the thing.\n",
	}
	if _, err := s.Save(in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := s.Get("hf_1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Title != in.Title {
		t.Errorf("title: got %q want %q", got.Title, in.Title)
	}
	if got.Status != StatusActive || got.Source != SourceTriage {
		t.Errorf("status/source: got %q/%q", got.Status, got.Source)
	}
	if got.Workdir != in.Workdir {
		t.Errorf("workdir: got %q want %q", got.Workdir, in.Workdir)
	}
	if len(got.KeyFiles) != 2 || got.KeyFiles[0] != "a.go" || got.KeyFiles[1] != "deck/intro.md" {
		t.Errorf("keyFiles: got %#v", got.KeyFiles)
	}
	if got.SourceID != "sess_42" || got.ChatSessionID != "chat_7" {
		t.Errorf("sourceId/chatSessionId: got %q/%q", got.SourceID, got.ChatSessionID)
	}
	if got.CreatedAt != 1000 || got.UpdatedAt != 1000 {
		t.Errorf("timestamps: got %d/%d", got.CreatedAt, got.UpdatedAt)
	}
	if got.Body != in.Body {
		t.Errorf("body mismatch:\n got %q\nwant %q", got.Body, in.Body)
	}
}

func TestSaveNewListBatchRetryPreservesChangedTask(t *testing.T) {
	s := newTestStore(t)
	entry := func(id string) Entry {
		return Entry{ID: id, Title: id, Status: StatusActive, Source: SourceMobile,
			ListItem: &ListItem{Who: "reminder", Fingerprint: "fingerprint-" + id, BatchID: strings.Repeat("a", 64)},
			Thread:   &Thread{Origin: &Origin{Kind: OriginList, At: 1000}}}
	}
	want := []Entry{entry("one"), entry("two")}
	// Simulate a process stopping after recording the selection and writing
	// only its first task. No partial selection may reach readers.
	staged := map[string]string{"one": want[0].ListItem.Fingerprint, "two": want[1].ListItem.Fingerprint}
	if err := s.writeListBatchFileLocked(s.listBatchStagePath(want[0].ListItem.BatchID), staged); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save(want[0]); err != nil {
		t.Fatal(err)
	}
	if all, err := s.List(); err != nil || len(all) != 0 {
		t.Fatalf("incomplete batch is visible: %+v, %v", all, err)
	}
	if _, err := s.Get("one"); !os.IsNotExist(err) {
		t.Fatalf("incomplete task is visible: %v", err)
	}
	if stagedEntry, published, err := s.ListBatchEntry("one"); err != nil || published || stagedEntry.ID != "one" {
		t.Fatalf("staged entry = %+v, published=%v, err=%v", stagedEntry, published, err)
	}
	if _, err := s.MutateIfStatus("one", nil, func(e *Entry) { e.Title = "premature" }); !os.IsNotExist(err) {
		t.Fatalf("incomplete task could be changed: %v", err)
	}
	if _, err := s.StageNewListBatch(want); err != nil {
		t.Fatal(err)
	}
	if err := s.PublishListBatch(want[:1]); err == nil {
		t.Fatal("published an incomplete selection")
	}
	if err := s.PublishListBatch(want); err != nil {
		t.Fatal(err)
	}
	if _, published, err := s.ListBatchEntry("one"); err != nil || !published {
		t.Fatalf("published=%v, err=%v", published, err)
	}
	changed, err := s.MutateIfStatus("one", []Status{StatusActive}, func(e *Entry) {
		e.Status = StatusInProgress
		e.Title = "Renamed"
	})
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.StageNewListBatch(want)
	if err != nil || again[0].Title != changed.Title || again[0].Status != StatusInProgress {
		t.Fatalf("retry = %+v, err=%v", again, err)
	}
	conflicting := []Entry{entry("three"), entry("one")}
	conflicting[1].ListItem.Fingerprint = "different"
	if _, err := s.StageNewListBatch(conflicting); err == nil {
		t.Fatal("accepted a different list task under the same id")
	}
	if _, err := s.Get("three"); !os.IsNotExist(err) {
		t.Fatalf("conflicting batch partially saved: %v", err)
	}
	if err := s.Delete("two"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StageNewListBatch(want); err == nil {
		t.Fatal("a retry recreated a task the person deleted")
	}
	if _, err := s.Get("two"); !os.IsNotExist(err) {
		t.Fatalf("deleted task reappeared: %v", err)
	}
}

func TestSaveNewListBatchRollsBackNewEntriesOnFailure(t *testing.T) {
	s := newTestStore(t)
	entry := func(id string) Entry {
		return Entry{ID: id, Status: StatusActive, Source: SourceMobile,
			ListItem: &ListItem{Who: "reminder", Fingerprint: id, BatchID: strings.Repeat("b", 64)},
			Thread:   &Thread{Origin: &Origin{Kind: OriginList}}}
	}
	bad := entry("bad")
	bad.SchemaVersion = SchemaVersion + 1
	if _, err := s.StageNewListBatch([]Entry{entry("first"), bad}); err == nil {
		t.Fatal("accepted a future schema")
	}
	if entries, err := s.List(); err != nil || len(entries) != 0 {
		t.Fatalf("partial batch = %+v, err=%v", entries, err)
	}
	if _, err := os.Stat(s.pathFor("first")); !os.IsNotExist(err) {
		t.Fatalf("failed write left a staged entry: %v", err)
	}
}

func TestCompleteLinkedDraftRequiresMatchingConfirmedOutcome(t *testing.T) {
	s := newTestStore(t)
	entry := Entry{ID: "task", Title: "Reply", Status: StatusActive,
		ListItem: &ListItem{Who: "draft", DraftTaskID: "draft.one"}, Thread: &Thread{Origin: &Origin{Kind: OriginList}}}
	if _, err := s.Save(entry); err != nil {
		t.Fatal(err)
	}
	outcome := Outcome{Kind: "replied", By: OutcomeByIsoform, At: 1000, Params: map[string]string{"person": "Lena"}}
	forged := entry
	forged.Status = StatusCompleted
	forged.Thread = &Thread{Outcome: &outcome}
	if _, err := s.Save(forged); err == nil {
		t.Fatal("ordinary save completed a draft without its action")
	}
	if _, err := s.CompleteLinkedDraft(entry.ID, "draft.other", outcome); err == nil {
		t.Fatal("different draft completed the task")
	}
	completed, err := s.CompleteLinkedDraft(entry.ID, "draft.one", outcome)
	if err != nil || completed.Status != StatusCompleted || completed.Thread.Outcome == nil || completed.Thread.Outcome.Kind != "replied" {
		t.Fatalf("completed = %+v, err=%v", completed, err)
	}
}

func TestSaveDefaultsTimestamps(t *testing.T) {
	s := newTestStore(t)
	s.Now = clockAt(5000)
	out, err := s.Save(Entry{ID: "hf_x", Status: StatusActive})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if out.CreatedAt != 5000 || out.UpdatedAt != 5000 {
		t.Errorf("defaulted timestamps: got %d/%d want 5000/5000", out.CreatedAt, out.UpdatedAt)
	}
}

func TestListSortedNewestFirst(t *testing.T) {
	s := newTestStore(t)
	for _, e := range []Entry{
		{ID: "a", Status: StatusActive, CreatedAt: 100},
		{ID: "b", Status: StatusActive, CreatedAt: 300},
		{ID: "c", Status: StatusActive, CreatedAt: 200},
	} {
		if _, err := s.Save(e); err != nil {
			t.Fatalf("Save %s: %v", e.ID, err)
		}
	}
	all, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("len: got %d want 3", len(all))
	}
	if all[0].ID != "b" || all[1].ID != "c" || all[2].ID != "a" {
		t.Errorf("order: got %s,%s,%s want b,c,a", all[0].ID, all[1].ID, all[2].ID)
	}
}

func TestUpdateStatusBumpsUpdatedAt(t *testing.T) {
	s := newTestStore(t)
	s.Now = clockAt(1000)
	if _, err := s.Save(Entry{ID: "hf", Status: StatusActive, CreatedAt: 1000, UpdatedAt: 1000}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	s.Now = clockAt(2000)
	got, err := s.UpdateStatus("hf", StatusInProgress)
	if err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	if got.Status != StatusInProgress {
		t.Errorf("status: got %q", got.Status)
	}
	if got.UpdatedAt != 2000 {
		t.Errorf("updatedAt: got %d want 2000", got.UpdatedAt)
	}
	if got.CreatedAt != 1000 {
		t.Errorf("createdAt must not change: got %d", got.CreatedAt)
	}
}

func TestMutateIfStatusRejectsUnexpectedStatus(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Save(Entry{ID: "hf", Status: StatusInProgress, Title: "Original"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := s.MutateIfStatus("hf", []Status{StatusActive}, func(e *Entry) {
		e.Title = "Changed"
	}); err == nil {
		t.Fatalf("MutateIfStatus should reject unexpected status")
	}
	got, err := s.Get("hf")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Title != "Original" || got.Status != StatusInProgress {
		t.Fatalf("entry changed unexpectedly: %#v", got)
	}
}

func TestMutateIfStatusAppliesUnderAllowedStatus(t *testing.T) {
	s := newTestStore(t)
	s.Now = clockAt(1000)
	if _, err := s.Save(Entry{ID: "hf", Status: StatusActive, CreatedAt: 1000, UpdatedAt: 1000}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	s.Now = clockAt(2000)
	got, err := s.MutateIfStatus("hf", []Status{StatusActive}, func(e *Entry) {
		e.Status = StatusInProgress
		e.ChatSessionID = "chat-1"
	})
	if err != nil {
		t.Fatalf("MutateIfStatus: %v", err)
	}
	if got.Status != StatusInProgress || got.ChatSessionID != "chat-1" || got.UpdatedAt != 2000 {
		t.Fatalf("mutated entry = %#v", got)
	}
}

func TestDelete(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Save(Entry{ID: "gone", Status: StatusActive}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.Delete("gone"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get("gone"); err == nil {
		t.Errorf("Get after Delete should fail")
	}
	// Deleting a missing file is a no-op.
	if err := s.Delete("never"); err != nil {
		t.Errorf("Delete missing: %v", err)
	}
}

func TestActiveForChip(t *testing.T) {
	s := newTestStore(t)
	if a, err := s.ActiveForChip(); err != nil || a != nil {
		t.Fatalf("empty store: got %#v, %v", a, err)
	}
	_, _ = s.Save(Entry{ID: "old", Status: StatusActive, CreatedAt: 100})
	_, _ = s.Save(Entry{ID: "done", Status: StatusCompleted, CreatedAt: 400})
	_, _ = s.Save(Entry{ID: "new", Status: StatusActive, CreatedAt: 300})
	got, err := s.ActiveForChip()
	if err != nil {
		t.Fatalf("ActiveForChip: %v", err)
	}
	if got == nil || got.ID != "new" {
		t.Errorf("got %#v want newest active 'new'", got)
	}
}

func TestActiveForChipsLimit(t *testing.T) {
	s := newTestStore(t)
	_, _ = s.Save(Entry{ID: "old", Status: StatusActive, CreatedAt: 100})
	_, _ = s.Save(Entry{ID: "done", Status: StatusCompleted, CreatedAt: 400})
	_, _ = s.Save(Entry{ID: "new", Status: StatusActive, CreatedAt: 300})
	_, _ = s.Save(Entry{ID: "middle", Status: StatusActive, CreatedAt: 200})

	all, err := s.ActiveForChips(0)
	if err != nil {
		t.Fatalf("ActiveForChips all: %v", err)
	}
	if got, want := idsOf(all), []string{"new", "middle", "old"}; !slices.Equal(got, want) {
		t.Fatalf("ActiveForChips all = %v, want %v", got, want)
	}

	limited, err := s.ActiveForChips(2)
	if err != nil {
		t.Fatalf("ActiveForChips limited: %v", err)
	}
	if got, want := idsOf(limited), []string{"new", "middle"}; !slices.Equal(got, want) {
		t.Fatalf("ActiveForChips limited = %v, want %v", got, want)
	}
}

func TestMarkStaleTTL(t *testing.T) {
	s := newTestStore(t)
	now := int64(100 * 3600 * 1000) // 100h in ms
	s.Now = clockAt(now)
	// fresh (1h old) stays active; aged (48h old) becomes stale.
	_, _ = s.Save(Entry{ID: "fresh", Status: StatusActive, SourceID: "a", CreatedAt: now - 1*3600*1000})
	_, _ = s.Save(Entry{ID: "aged", Status: StatusActive, SourceID: "b", CreatedAt: now - 48*3600*1000})
	_, _ = s.Save(Entry{ID: "reminder", Status: StatusActive, SourceID: "c", CreatedAt: now - 48*3600*1000, ListItem: &ListItem{Who: "reminder"}})

	expired, err := s.MarkStale(24 * time.Hour)
	if err != nil {
		t.Fatalf("MarkStale: %v", err)
	}
	if len(expired) != 1 {
		t.Fatalf("restaled count: got %d want 1", len(expired))
	}
	if expired[0].ID != "aged" {
		t.Fatalf("expected expired handoff 'aged', got %q", expired[0].ID)
	}
	if e, _ := s.Get("fresh"); e.Status != StatusActive {
		t.Errorf("fresh should stay active, got %q", e.Status)
	}
	if e, _ := s.Get("aged"); e.Status != StatusStale {
		t.Errorf("aged should be stale, got %q", e.Status)
	}
	if e, _ := s.Get("reminder"); e.Status != StatusActive {
		t.Errorf("the person's reminder should stay active, got %q", e.Status)
	}
}

func TestMarkStaleSupersededAndTerminalUntouched(t *testing.T) {
	s := newTestStore(t)
	now := int64(10 * 3600 * 1000)
	s.Now = clockAt(now)
	// Two active handoffs share source "s"; the older is superseded.
	_, _ = s.Save(Entry{ID: "older", Status: StatusActive, SourceID: "s", CreatedAt: now - 2*3600*1000})
	_, _ = s.Save(Entry{ID: "newer", Status: StatusActive, SourceID: "s", CreatedAt: now - 1*3600*1000})
	// A completed handoff (terminal) must never be restaled even if old/superseded.
	_, _ = s.Save(Entry{ID: "doneold", Status: StatusCompleted, SourceID: "s", CreatedAt: now - 5*3600*1000})

	expired, err := s.MarkStale(24 * time.Hour) // TTL large so only supersession fires
	if err != nil {
		t.Fatalf("MarkStale: %v", err)
	}
	if len(expired) != 1 {
		t.Fatalf("restaled count: got %d want 1", len(expired))
	}
	if expired[0].ID != "older" {
		t.Fatalf("expected expired handoff 'older', got %q", expired[0].ID)
	}
	if e, _ := s.Get("older"); e.Status != StatusStale {
		t.Errorf("older should be stale (superseded), got %q", e.Status)
	}
	if e, _ := s.Get("newer"); e.Status != StatusActive {
		t.Errorf("newer should stay active, got %q", e.Status)
	}
	if e, _ := s.Get("doneold"); e.Status != StatusCompleted {
		t.Errorf("completed must stay completed, got %q", e.Status)
	}
}

func TestDeleteIfUnchangedPreservesEditedTask(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	saved, err := store.Save(Entry{ID: "new-task", Title: "Initial", Status: StatusActive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.MutateIfStatus(saved.ID, nil, func(entry *Entry) { entry.Title = "Edited" }); err != nil {
		t.Fatal(err)
	}
	if removed, err := store.DeleteIfUnchanged(saved); err != nil || removed {
		t.Fatalf("removed edited task: %v, %v", removed, err)
	}
	got, err := store.Get(saved.ID)
	if err != nil || got.Title != "Edited" {
		t.Fatalf("edited task = %+v, %v", got, err)
	}
	if removed, err := store.DeleteIfUnchanged(got); err != nil || !removed {
		t.Fatalf("unchanged task: %v, %v", removed, err)
	}
}
