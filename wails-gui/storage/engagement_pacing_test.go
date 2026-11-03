package storage

import (
	"testing"
	"time"
)

func TestEngagementPacingKeepsDeliveryDaySeparateFromFirstChat(t *testing.T) {
	s := newTestStore(t)
	chatAt := time.Date(2026, time.October, 12, 23, 0, 0, 0, time.UTC)
	notifiedAt := chatAt.Add(9 * time.Hour)
	row, err := s.CreateEngagementMoment(ctx, EngagementMomentRow{
		Kind: "work_blocked", DedupeKey: "chat-before-notification", CreatedAt: chatAt.UnixMilli(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RecordEngagementChatSent(ctx, row.ID, chatAt); err != nil {
		t.Fatal(err)
	}
	for _, at := range []time.Time{notifiedAt, notifiedAt.Add(time.Hour)} {
		if err := s.SetEngagementMomentState(ctx, row.ID, EngagementStateSent, "sent", EngagementMomentRow{
			Channel: "push", Level: "active", SentAt: at.UnixMilli(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.GetEngagementMoment(ctx, row.ID)
	if err != nil || got.SentAt != chatAt.UnixMilli() {
		t.Fatalf("first measured send changed: %+v, %v", got, err)
	}
	pacing, err := s.EngagementPacingBetween(ctx, notifiedAt, notifiedAt.Add(time.Hour))
	if err != nil || len(pacing) != 1 || pacing[0].At != notifiedAt.UnixMilli() || pacing[0].Level != "active" || pacing[0].DedupeKey != row.DedupeKey {
		t.Fatalf("delivery pacing changed or duplicated: %+v, %v", pacing, err)
	}
	stats, err := s.EngagementStatsBetween(ctx, notifiedAt, notifiedAt.Add(time.Hour))
	if err != nil || stats[row.Kind].Sent != 0 {
		t.Fatalf("pacing must not move first-send measurement: %+v, %v", stats, err)
	}
	stats, err = s.EngagementStatsBetween(ctx, chatAt, notifiedAt.Add(time.Hour))
	if err != nil || stats[row.Kind].Sent != 1 {
		t.Fatalf("internal pacing receipt inflated weekly sends: %+v, %v", stats, err)
	}
}

func TestEngagementPacingRestoresLegacyDeliveryAndExcludesUnpacedChat(t *testing.T) {
	s := newTestStore(t)
	now := time.Date(2026, time.October, 13, 8, 0, 0, 0, time.UTC)
	for _, row := range []EngagementMomentRow{
		{ID: "legacy", Kind: "work_blocked", DedupeKey: "legacy", State: EngagementStateSent, Level: "active", SentAt: now.UnixMilli()},
		{ID: "chat-only", Kind: "work_blocked", DedupeKey: "chat-only", State: EngagementStateSent, Channel: "desktop", SentAt: now.UnixMilli()},
		{ID: "future", Kind: "work_blocked", DedupeKey: "future", State: EngagementStateSent, Level: "active", SentAt: now.Add(time.Hour).UnixMilli()},
	} {
		if _, err := s.CreateEngagementMoment(ctx, row); err != nil {
			t.Fatal(err)
		}
	}
	// An upgrade keeps old delivery rows that predate separate pacing evidence.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM engagement_receipts WHERE moment_id = 'legacy' AND channel = ?`, engagementPacingChannel); err != nil {
		t.Fatal(err)
	}
	pacing, err := s.EngagementPacingBetween(ctx, now.Add(-time.Hour), now)
	if err != nil || len(pacing) != 1 || pacing[0].MomentID != "legacy" || pacing[0].At != now.UnixMilli() || pacing[0].Level != "active" {
		t.Fatalf("legacy pacing = %+v, %v", pacing, err)
	}
}

func TestEngagementPacingReceiptCommitsWithDeliveryState(t *testing.T) {
	s := newTestStore(t)
	row, err := s.CreateEngagementMoment(ctx, EngagementMomentRow{Kind: "work_blocked", DedupeKey: "atomic-pacing"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER reject_test_pacing BEFORE INSERT ON engagement_receipts
		WHEN NEW.channel = 'pacing' BEGIN SELECT RAISE(ABORT, 'pacing unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.SetEngagementMomentState(ctx, row.ID, EngagementStateSent, "sent", EngagementMomentRow{
		Channel: "push", Level: "active", SentAt: time.Now().UnixMilli(),
	}); err == nil {
		t.Fatal("expected pacing receipt failure")
	}
	got, err := s.GetEngagementMoment(ctx, row.ID)
	if err != nil || got.State != EngagementStatePending || got.SentAt != 0 {
		t.Fatalf("delivery state did not roll back: %+v, %v", got, err)
	}
	receipts, err := s.ListEngagementReceipts(ctx, row.ID)
	if err != nil || len(receipts) != 1 || receipts[0].State != EngagementReceiptCreated {
		t.Fatalf("partial receipt committed without pacing: %+v, %v", receipts, err)
	}
}
