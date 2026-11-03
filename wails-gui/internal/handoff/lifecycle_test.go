package handoff

import (
	"fmt"
	"slices"
	"testing"
	"time"
)

// TestSweepExpiresOnlyActiveEntries pins the lifecycle rule: an active entry
// ages to stale after the TTL, while blocked and needs_review entries wait for
// the user however old they are.
func TestSweepExpiresOnlyActiveEntries(t *testing.T) {
	s := newTestStore(t)
	created := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	for _, e := range []Entry{
		{ID: "hf_active", Status: StatusActive, Source: SourceAgency, CreatedAt: created.UnixMilli()},
		{ID: "hf_blocked", Status: StatusBlocked, BlockedReason: BlockedInterrupted, Source: SourceAgency, CreatedAt: created.UnixMilli()},
		{ID: "hf_review", Status: StatusNeedsReview, Source: SourceAgency, CreatedAt: created.UnixMilli()},
		{ID: "hf_done", Status: StatusCompleted, Source: SourceAgency, CreatedAt: created.UnixMilli()},
		{ID: "hf_running", Status: StatusInProgress, Source: SourceAgency, CreatedAt: created.UnixMilli()},
		{ID: "hf_stopped", Status: StatusAborted, Source: SourceAgency, CreatedAt: created.UnixMilli()},
		{ID: "hf_stale", Status: StatusStale, Source: SourceAgency, CreatedAt: created.UnixMilli()},
	} {
		if _, err := s.Save(e); err != nil {
			t.Fatal(err)
		}
	}
	s.Now = func() time.Time { return created.Add(72 * time.Hour) }

	expired, err := s.MarkStale(24 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(expired) != 1 || expired[0].ID != "hf_active" {
		t.Fatalf("expired = %+v, want only hf_active", expired)
	}
	for id, want := range map[string]Status{
		"hf_active":  StatusStale,
		"hf_blocked": StatusBlocked,
		"hf_review":  StatusNeedsReview,
		"hf_done":    StatusCompleted,
		"hf_running": StatusInProgress,
		"hf_stopped": StatusAborted,
		"hf_stale":   StatusStale,
	} {
		got, err := s.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != want {
			t.Errorf("%s status = %q, want %q", id, got.Status, want)
		}
	}
}

func TestLifecycleTransitionTable(t *testing.T) {
	states := []Status{StatusActive, StatusInProgress, StatusNeedsReview, StatusBlocked, StatusCompleted, StatusAborted, StatusStale}
	allowed := map[Status][]Status{
		StatusActive:      {StatusActive, StatusInProgress, StatusAborted, StatusStale},
		StatusInProgress:  {StatusActive, StatusInProgress, StatusNeedsReview, StatusBlocked, StatusCompleted, StatusAborted, StatusStale},
		StatusNeedsReview: {StatusNeedsReview, StatusInProgress, StatusAborted},
		StatusBlocked:     {StatusBlocked, StatusInProgress, StatusAborted},
		StatusCompleted:   {StatusCompleted},
		StatusAborted:     {StatusAborted, StatusInProgress},
		StatusStale:       {StatusStale, StatusInProgress, StatusAborted},
	}
	for _, from := range states {
		for _, to := range states {
			t.Run(fmt.Sprintf("%s_to_%s", from, to), func(t *testing.T) {
				wantAllowed := slices.Contains(allowed[from], to)
				if got := CanTransition(from, to); got != wantAllowed {
					t.Fatalf("CanTransition = %v, want %v", got, wantAllowed)
				}
				s := newTestStore(t)
				if _, err := s.Save(Entry{ID: "work", Status: from, UpdatedAt: 123}); err != nil {
					t.Fatal(err)
				}
				_, err := s.UpdateStatus("work", to)
				if (err == nil) != wantAllowed {
					t.Fatalf("UpdateStatus error = %v, want allowed = %v", err, wantAllowed)
				}
				e, err := s.Get("work")
				if err != nil {
					t.Fatal(err)
				}
				if !wantAllowed && (e.Status != from || e.UpdatedAt != 123) {
					t.Fatalf("rejected transition mutated entry: %+v", e)
				}
			})
		}
	}
	if CanTransition(Status("future"), StatusActive) || CanTransition(StatusActive, Status("future")) {
		t.Fatal("unknown state accepted by transition table")
	}
}

func TestSaveCannotBypassLifecycleTransition(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Save(Entry{ID: "done", Status: StatusCompleted}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save(Entry{ID: "done", Status: StatusInProgress}); err == nil {
		t.Fatal("Save restarted completed work")
	}
}

func TestActiveEntryExpiresAtTwentyFourHours(t *testing.T) {
	s := newTestStore(t)
	created := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	if _, err := s.Save(Entry{ID: "waiting", Status: StatusActive, CreatedAt: created.UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	s.Now = func() time.Time { return created.Add(24*time.Hour - time.Millisecond) }
	if expired, err := s.MarkStale(24 * time.Hour); err != nil || len(expired) != 0 {
		t.Fatalf("entry expired early: %+v, %v", expired, err)
	}
	s.Now = func() time.Time { return created.Add(24 * time.Hour) }
	if expired, err := s.MarkStale(24 * time.Hour); err != nil || len(expired) != 1 {
		t.Fatalf("entry did not expire at TTL: %+v, %v", expired, err)
	}
}

func TestMutateIfAttemptRejectsLateSettlement(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Save(Entry{ID: "work", Status: StatusInProgress, AttemptID: "old", LastActivityAt: 123}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateStatus("work", StatusBlocked); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MutateIfStatus("work", []Status{StatusBlocked}, func(e *Entry) {
		e.Status = StatusInProgress
		e.AttemptID = "new"
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MutateIfAttempt("work", "old", []Status{StatusInProgress}, func(e *Entry) {
		e.Status = StatusCompleted
	}); err == nil {
		t.Fatal("a late old result completed the new attempt")
	}
	got, err := s.MutateIfAttempt("work", "new", []Status{StatusInProgress}, func(e *Entry) {
		e.LastActivityAt = 456
	})
	if err != nil || got.Status != StatusInProgress || got.LastActivityAt != 456 {
		t.Fatalf("current attempt activity = %+v, %v", got, err)
	}
	if _, err := s.MutateIfAttempt("work", "new", []Status{StatusActive}, func(e *Entry) {
		e.Status = StatusCompleted
	}); err == nil {
		t.Fatal("attempt check bypassed status check")
	}
}

func TestMutationCannotChangeIdentity(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Save(Entry{ID: "original", Status: StatusActive}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MutateIfStatus("original", nil, func(e *Entry) { e.ID = "replacement" }); err == nil {
		t.Fatal("mutation changed entry identity")
	}
	all, err := s.List()
	if err != nil || len(all) != 1 || all[0].ID != "original" {
		t.Fatalf("identity mutation touched records: %+v, %v", all, err)
	}
}

// TestStartAgainMovesNeedsReviewBackToInProgress checks the store half of
// "Start again": the claim accepts needs_review and keeps the chat session.
func TestStartAgainMovesNeedsReviewBackToInProgress(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Save(Entry{ID: "hf_again", Status: StatusNeedsReview, ChatSessionID: "chat-1", Body: "brief"}); err != nil {
		t.Fatal(err)
	}
	claimed, err := s.MutateIfStatus("hf_again", []Status{StatusActive, StatusStale, StatusNeedsReview}, func(e *Entry) {
		e.Status = StatusInProgress
		e.Verification = nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if claimed.Status != StatusInProgress || claimed.ChatSessionID != "chat-1" || claimed.Verification != nil {
		t.Fatalf("claimed = %+v", claimed)
	}
	// A blocked entry is not a claim target: the claim fails and nothing moves.
	if _, err := s.Save(Entry{ID: "hf_stuck", Status: StatusBlocked, Body: "brief"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MutateIfStatus("hf_stuck", []Status{StatusActive, StatusStale, StatusNeedsReview}, func(e *Entry) {
		e.Status = StatusInProgress
	}); err == nil {
		t.Fatal("a blocked entry was claimed")
	}
}

// TestChipsSurfaceOnlyWorkTheUserCanActOnNow pins which statuses reach the
// composer chip and Today: waiting, blocked and needs-review entries, newest
// first, with the limit applied to that list.
func TestChipsSurfaceOnlyWorkTheUserCanActOnNow(t *testing.T) {
	s := newTestStore(t)
	for _, e := range []Entry{
		{ID: "waiting", Status: StatusActive, CreatedAt: 100},
		{ID: "blocked", Status: StatusBlocked, BlockedReason: BlockedTimeout, CreatedAt: 200},
		{ID: "working", Status: StatusInProgress, CreatedAt: 300},
		{ID: "review", Status: StatusNeedsReview, CreatedAt: 400},
		{ID: "done", Status: StatusCompleted, CreatedAt: 500},
		{ID: "stopped", Status: StatusAborted, CreatedAt: 600},
		{ID: "stale", Status: StatusStale, CreatedAt: 700},
	} {
		if _, err := s.Save(e); err != nil {
			t.Fatal(err)
		}
	}

	all, err := s.ActiveForChips(0)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := idsOf(all), []string{"review", "blocked", "waiting"}; !slices.Equal(got, want) {
		t.Fatalf("chips = %v, want %v", got, want)
	}
	if all[1].Status != StatusBlocked || all[1].BlockedReason != BlockedTimeout {
		t.Fatalf("blocked chip lost its reason: %+v", all[1])
	}

	limited, err := s.ActiveForChips(2)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := idsOf(limited), []string{"review", "blocked"}; !slices.Equal(got, want) {
		t.Fatalf("limited chips = %v, want %v", got, want)
	}

	chip, err := s.ActiveForChip()
	if err != nil || chip == nil || chip.ID != "review" {
		t.Fatalf("single chip = %+v err=%v, want the newest actionable entry", chip, err)
	}
}
