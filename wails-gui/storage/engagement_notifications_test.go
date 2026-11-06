package storage

import (
	"testing"
	"time"
)

func TestEngagementNotificationCohortUsesFirstExternalReceipt(t *testing.T) {
	s := newTestStore(t)
	first := time.Date(2026, 10, 12, 23, 0, 0, 0, time.UTC)
	next := first.Add(9 * time.Hour)
	row, err := s.CreateEngagementMoment(ctx, EngagementMomentRow{ID: "notice", Kind: "work_blocked", DedupeKey: "notice", CreatedAt: first.UnixMilli()})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RecordEngagementChatSent(ctx, row.ID, first); err != nil {
		t.Fatal(err)
	}
	if got, err := s.EngagementNotificationsBetween(ctx, first.Add(-time.Hour), first.Add(time.Hour)); err != nil || len(got) != 0 {
		t.Fatalf("chat counted as a notice: %+v %v", got, err)
	}
	if err := s.SetEngagementMomentState(ctx, row.ID, EngagementStateSent, "sent", EngagementMomentRow{Channel: "desktop+push", Level: "active", SentAt: next.UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	got, err := s.EngagementNotificationsBetween(ctx, next.Add(-time.Minute), next.Add(time.Minute))
	if err != nil || len(got) != 1 || got[0].MomentID != row.ID || got[0].At != next.UnixMilli() {
		t.Fatalf("notice cohort=%+v err=%v", got, err)
	}
}
