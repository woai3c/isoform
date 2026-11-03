package storage

import (
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestEngagementMomentsDedupeOpenRows(t *testing.T) {
	s := newTestStore(t)
	first, err := s.CreateEngagementMoment(ctx, EngagementMomentRow{Kind: "decision_due", DedupeKey: "decision_due:r1", SubjectID: "r1"})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == "" || first.State != EngagementStatePending || first.CreatedAt == 0 {
		t.Fatalf("defaults not applied: %+v", first)
	}
	if _, err := s.CreateEngagementMoment(ctx, EngagementMomentRow{Kind: "decision_due", DedupeKey: "decision_due:r1"}); !errors.Is(err, ErrEngagementMomentOpen) {
		t.Fatalf("a second open moment with the same key must be refused, got %v", err)
	}
	sentAt := time.Now().UnixMilli()
	if err := s.SetEngagementMomentState(ctx, first.ID, EngagementStateSent, "ok", EngagementMomentRow{Channel: "push", Level: "active", SentAt: sentAt, Score: 0.8}); err != nil {
		t.Fatal(err)
	}
	again, err := s.CreateEngagementMoment(ctx, EngagementMomentRow{Kind: "decision_due", DedupeKey: "decision_due:r1"})
	if err != nil {
		t.Fatalf("once sent, the key may open again: %v", err)
	}
	got, err := s.GetEngagementMoment(ctx, first.ID)
	if err != nil || got == nil {
		t.Fatalf("GetEngagementMoment: %v %v", got, err)
	}
	if got.State != EngagementStateSent || got.Channel != "push" || got.Level != "active" || got.SentAt != sentAt || got.Score != 0.8 {
		t.Fatalf("sent fields not stored: %+v", got)
	}
	last, err := s.LastSentEngagementMoments(ctx, time.Now().Add(-time.Hour))
	if err != nil || last["decision_due:r1"].UnixMilli() != sentAt {
		t.Fatalf("LastSentEngagementMoments = %v %v", last, err)
	}
	if err := s.SetEngagementMomentState(ctx, again.ID, EngagementStateSuppressed, "cooldown", EngagementMomentRow{}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetEngagementMomentState(ctx, "missing", EngagementStateSent, "", EngagementMomentRow{}); err == nil {
		t.Fatal("updating a missing moment must fail")
	}
}

func TestEngagementDueAndExpiry(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	pending, _ := s.CreateEngagementMoment(ctx, EngagementMomentRow{Kind: "work_finished", DedupeKey: "work_finished:a"})
	held, _ := s.CreateEngagementMoment(ctx, EngagementMomentRow{Kind: "meeting_ended", DedupeKey: "meeting_ended:b"})
	if err := s.SetEngagementMomentState(ctx, held.ID, EngagementStateScheduled, "passive", EngagementMomentRow{HoldUntil: now.Add(time.Hour).UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	stale, _ := s.CreateEngagementMoment(ctx, EngagementMomentRow{Kind: "brief_ready", DedupeKey: "brief_ready:c", ExpiresAt: now.Add(-time.Minute).UnixMilli()})

	due, err := s.DueEngagementMoments(ctx, now, 0)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, m := range due {
		ids[m.ID] = true
	}
	if !ids[pending.ID] || ids[held.ID] || !ids[stale.ID] {
		t.Fatalf("due set wrong: pending=%v held=%v stale=%v", ids[pending.ID], ids[held.ID], ids[stale.ID])
	}
	later, _ := s.DueEngagementMoments(ctx, now.Add(2*time.Hour), 0)
	found := false
	for _, m := range later {
		if m.ID == held.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("a scheduled moment becomes due once its hold ends")
	}
	n, err := s.ExpireEngagementMoments(ctx, now)
	if err != nil || n != 1 {
		t.Fatalf("ExpireEngagementMoments = %d %v", n, err)
	}
	expired, _ := s.ListEngagementMoments(ctx, EngagementStateExpired, 0)
	if len(expired) != 1 || expired[0].ID != stale.ID {
		t.Fatalf("expired rows: %+v", expired)
	}
}

func TestEngagementReceiptsAndStats(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	var sent []EngagementMomentRow
	for i := 0; i < 3; i++ {
		m, err := s.CreateEngagementMoment(ctx, EngagementMomentRow{Kind: "goal_check_in", DedupeKey: "goal_check_in:" + string(rune('a'+i))})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.SetEngagementMomentState(ctx, m.ID, EngagementStateSent, "ok", EngagementMomentRow{Channel: "push", SentAt: now.UnixMilli()}); err != nil {
			t.Fatal(err)
		}
		sent = append(sent, m)
	}
	if err := s.RecordEngagementReceipt(ctx, EngagementReceiptRow{MomentID: sent[0].ID, Channel: "push", State: EngagementReceiptDelivered}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordEngagementReceipt(ctx, EngagementReceiptRow{MomentID: sent[0].ID, Channel: EngagementCompletionChannel, State: EngagementReceiptActed, Action: EngagementCompletionAction}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordEngagementReceipt(ctx, EngagementReceiptRow{MomentID: sent[0].ID, Channel: EngagementCompletionChannel, State: EngagementReceiptActed, Action: EngagementCompletionAction}); err != nil {
		t.Fatalf("a repeated receipt must be ignored, not fail: %v", err)
	}
	if err := s.RecordEngagementReceipt(ctx, EngagementReceiptRow{MomentID: sent[1].ID, Channel: "push", State: EngagementReceiptDismissed}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordEngagementReceipt(ctx, EngagementReceiptRow{MomentID: sent[2].ID, Channel: "push", State: EngagementReceiptMuted}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordEngagementReceipt(ctx, EngagementReceiptRow{MomentID: "", State: "opened"}); err == nil {
		t.Fatal("a receipt without a moment must fail")
	}
	receipts, err := s.ListEngagementReceipts(ctx, sent[0].ID)
	if err != nil || len(receipts) != 4 {
		t.Fatalf("ListEngagementReceipts = %+v %v", receipts, err)
	}
	stats, err := s.EngagementStatsSince(ctx, now.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	got := stats["goal_check_in"]
	if got.Sent != 3 || got.Acted != 1 || got.Dismissed != 1 || got.Muted != 1 || got.Opened != 0 {
		t.Fatalf("stats = %+v", got)
	}
}

func TestEngagementStakesOpenLookupAndSentSince(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	row, err := s.CreateEngagementMoment(ctx, EngagementMomentRow{Kind: "work_finished", DedupeKey: "work_finished:x", Stakes: 0.9})
	if err != nil {
		t.Fatal(err)
	}
	open, err := s.GetOpenEngagementMomentByKey(ctx, "work_finished:x")
	if err != nil || open == nil || open.ID != row.ID || open.Stakes != 0.9 {
		t.Fatalf("open lookup = %+v %v", open, err)
	}
	if missing, _ := s.GetOpenEngagementMomentByKey(ctx, "nope"); missing != nil {
		t.Fatal("unknown keys return nil")
	}
	if err := s.SetEngagementMomentState(ctx, row.ID, EngagementStateSent, "ok", EngagementMomentRow{Score: 0.3, Level: "active", SentAt: now.UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	if closed, _ := s.GetOpenEngagementMomentByKey(ctx, "work_finished:x"); closed != nil {
		t.Fatal("a sent moment is no longer open")
	}
	sent, err := s.SentEngagementMomentsSince(ctx, now.Add(-time.Minute), 0)
	if err != nil || len(sent) != 1 || sent[0].Stakes != 0.9 || sent[0].Score != 0.3 || sent[0].Level != "active" {
		t.Fatalf("SentEngagementMomentsSince = %+v %v", sent, err)
	}
}

func TestEngagementPrefsUpsert(t *testing.T) {
	s := newTestStore(t)
	if err := s.UpsertEngagementPref(ctx, EngagementPrefRow{Kind: "person_reappeared", Weight: 0.5, Enabled: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertEngagementPref(ctx, EngagementPrefRow{Kind: "person_reappeared", Weight: -1, Enabled: 0, MutedUntil: 42}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertEngagementPref(ctx, EngagementPrefRow{Kind: " "}); err == nil {
		t.Fatal("a blank kind must fail")
	}
	prefs, err := s.ListEngagementPrefs(ctx)
	if err != nil || len(prefs) != 1 {
		t.Fatalf("ListEngagementPrefs = %+v %v", prefs, err)
	}
	if p := prefs[0]; p.Kind != "person_reappeared" || p.Weight != -1 || p.Enabled != 0 || p.MutedUntil != 42 || p.UpdatedAt == 0 {
		t.Fatalf("pref not updated in place: %+v", p)
	}
}

func TestEngagementRulesRoundTrip(t *testing.T) {
	s := newTestStore(t)
	if row, err := s.GetEngagementRules(ctx); err != nil || row != nil {
		t.Fatalf("unset rules read as nil: %+v %v", row, err)
	}
	if err := s.SetEngagementRules(ctx, EngagementRulesRow{QuietStartMinutes: 23 * 60, QuietEndMinutes: 7*60 + 30, DailyInterruptBudget: 4}); err != nil {
		t.Fatal(err)
	}
	row, err := s.GetEngagementRules(ctx)
	if err != nil || row == nil || row.QuietStartMinutes != 23*60 || row.QuietEndMinutes != 450 || row.DailyInterruptBudget != 4 {
		t.Fatalf("rules did not round-trip: %+v %v", row, err)
	}
}

func TestEngagementLifecycleReceiptsAreDurableAndIdempotent(t *testing.T) {
	s := newTestStore(t)
	start := time.Date(2026, time.October, 12, 10, 0, 0, 0, time.UTC)
	row, err := s.CreateEngagementMoment(ctx, EngagementMomentRow{
		Kind: "decision_due", DedupeKey: "lifecycle", CreatedAt: start.UnixMilli(),
	})
	if err != nil {
		t.Fatal(err)
	}
	scheduledAt := start.Add(time.Minute).UnixMilli()
	if err := s.SetEngagementMomentState(ctx, row.ID, EngagementStateScheduled, "quiet", EngagementMomentRow{UpdatedAt: scheduledAt}); err != nil {
		t.Fatal(err)
	}
	sentAt := start.Add(2 * time.Minute).UnixMilli()
	if err := s.SetEngagementMomentState(ctx, row.ID, EngagementStateScheduled, "partly sent", EngagementMomentRow{
		Channel: "desktop", SentAt: sentAt, UpdatedAt: sentAt,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetEngagementMomentState(ctx, row.ID, EngagementStateSent, "sent", EngagementMomentRow{
		Channel: "desktop+push", SentAt: start.Add(3 * time.Minute).UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}
	for _, receipt := range []EngagementReceiptRow{
		{State: EngagementReceiptOpened, Action: "open", At: sentAt + 20},
		{State: EngagementReceiptDelivered, At: sentAt + 10},
		{State: EngagementReceiptActed, Action: "completed", At: sentAt + 30},
		{State: EngagementReceiptDismissed, At: sentAt + 40},
		{State: EngagementReceiptMuted, At: sentAt + 50},
		{State: EngagementReceiptOpened, Action: "different action", At: sentAt + 60},
	} {
		receipt.MomentID, receipt.Channel = row.ID, "desktop"
		if err := s.RecordEngagementReceipt(ctx, receipt); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.GetEngagementMoment(ctx, row.ID)
	if err != nil || got.SentAt != sentAt {
		t.Fatalf("retry must preserve first send: %+v, %v", got, err)
	}
	receipts, err := s.ListEngagementReceipts(ctx, row.ID)
	if err != nil || len(receipts) != 9 {
		t.Fatalf("eight states plus second channel send: %+v, %v", receipts, err)
	}
	counts := map[string]int{}
	for _, receipt := range receipts {
		counts[receipt.State]++
		if receipt.State == EngagementReceiptOpened && (receipt.Action != "open" || receipt.At != sentAt+20) {
			t.Fatalf("replay replaced first receipt: %+v", receipt)
		}
		if receipt.State == EngagementReceiptSent && receipt.Channel == "push" && receipt.At != start.Add(3*time.Minute).UnixMilli() {
			t.Fatalf("second channel must keep its own send time: %+v", receipt)
		}
		if receipt.State == EngagementReceiptScheduled && receipt.At != scheduledAt {
			t.Fatalf("retry replaced initial scheduled receipt: %+v", receipt)
		}
	}
	if counts[EngagementReceiptSent] != 2 || counts[EngagementReceiptCreated] != 1 || counts[EngagementReceiptScheduled] != 1 {
		t.Fatalf("lifecycle states = %+v", counts)
	}
}

func TestEngagementReceiptValidationAndAtomicLifecycle(t *testing.T) {
	s := newTestStore(t)
	if err := s.RecordEngagementReceipt(ctx, EngagementReceiptRow{MomentID: "unknown", State: EngagementReceiptOpened}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("unknown moment: %v", err)
	}
	row, err := s.CreateEngagementMoment(ctx, EngagementMomentRow{Kind: "work_blocked", DedupeKey: "validation"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RecordEngagementReceipt(ctx, EngagementReceiptRow{MomentID: row.ID, State: "unsupported"}); err == nil {
		t.Fatal("unknown receipt state accepted")
	}
	if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER reject_test_receipts BEFORE INSERT ON engagement_receipts
		BEGIN SELECT RAISE(ABORT, 'receipt unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordEngagementChatSent(ctx, row.ID, time.Now()); err == nil {
		t.Fatal("expected chat receipt failure")
	}
	if got, err := s.GetEngagementMoment(ctx, row.ID); err != nil || got.SentAt != 0 {
		t.Fatalf("chat send must roll back with receipt: %+v, %v", got, err)
	}
	if err := s.SetEngagementMomentState(ctx, row.ID, EngagementStateSent, "sent", EngagementMomentRow{SentAt: time.Now().UnixMilli()}); err == nil {
		t.Fatal("expected receipt failure")
	}
	got, err := s.GetEngagementMoment(ctx, row.ID)
	if err != nil || got.State != EngagementStatePending || got.SentAt != 0 {
		t.Fatalf("state must roll back with receipt: %+v, %v", got, err)
	}
	if _, err := s.CreateEngagementMoment(ctx, EngagementMomentRow{ID: "failed-create", Kind: "work_blocked", DedupeKey: "failed-create"}); err == nil {
		t.Fatal("expected created receipt failure")
	}
	if got, err := s.GetEngagementMoment(ctx, "failed-create"); err != nil || got != nil {
		t.Fatalf("creation must roll back with receipt: %+v, %v", got, err)
	}
}

func TestEngagementStatsBetweenUsesDistinctSentCohort(t *testing.T) {
	s := newTestStore(t)
	end := time.Date(2026, time.October, 17, 10, 0, 0, 0, time.UTC)
	start := end.Add(-7 * 24 * time.Hour)
	makeMoment := func(id, kind string, created, scheduled, sent time.Time) EngagementMomentRow {
		t.Helper()
		row, err := s.CreateEngagementMoment(ctx, EngagementMomentRow{ID: id, Kind: kind, DedupeKey: id, CreatedAt: created.UnixMilli()})
		if err != nil {
			t.Fatal(err)
		}
		if !scheduled.IsZero() {
			if err := s.SetEngagementMomentState(ctx, row.ID, EngagementStateScheduled, "hold", EngagementMomentRow{UpdatedAt: scheduled.UnixMilli()}); err != nil {
				t.Fatal(err)
			}
		}
		if !sent.IsZero() {
			if err := s.SetEngagementMomentState(ctx, row.ID, EngagementStateSent, "sent", EngagementMomentRow{Channel: "desktop", SentAt: sent.UnixMilli()}); err != nil {
				t.Fatal(err)
			}
		}
		return row
	}
	add := func(id, channel, state string, at time.Time) {
		t.Helper()
		action := ""
		if state == EngagementReceiptActed {
			channel, action = EngagementCompletionChannel, EngagementCompletionAction
		}
		if err := s.RecordEngagementReceipt(ctx, EngagementReceiptRow{MomentID: id, Channel: channel, State: state, Action: action, At: at.UnixMilli()}); err != nil {
			t.Fatal(err)
		}
	}
	makeMoment("a", "decision_due", start, start, start)
	makeMoment("b", "decision_due", start.Add(-time.Hour), start.Add(time.Hour), start.Add(time.Hour))
	makeMoment("held", "decision_due", start, start.Add(time.Hour), time.Time{})
	makeMoment("old", "decision_due", start.Add(-time.Hour), time.Time{}, start.Add(-time.Minute))
	makeMoment("future", "decision_due", end.Add(time.Second), time.Time{}, end.Add(time.Second))
	makeMoment("other", "work_blocked", start, time.Time{}, end)
	add("a", "desktop", EngagementReceiptDelivered, start)
	add("a", "push", EngagementReceiptDelivered, start.Add(time.Minute))
	add("a", "desktop", EngagementReceiptOpened, start.Add(time.Minute))
	add("a", "desktop", EngagementReceiptActed, start.Add(2*time.Hour))
	add("a", "desktop", EngagementReceiptDismissed, start.Add(3*time.Hour))
	add("a", "push", EngagementReceiptMuted, start.Add(4*time.Hour))
	add("b", "desktop", EngagementReceiptDismissed, start.Add(2*time.Hour))
	add("b", "desktop", EngagementReceiptOpened, start)                    // Before this send.
	add("b", "desktop", EngagementReceiptActed, end.Add(time.Millisecond)) // After this snapshot.
	add("old", "desktop", EngagementReceiptOpened, start.Add(time.Hour))
	add("held", "desktop", EngagementReceiptMuted, start.Add(time.Hour))
	add("other", "desktop", EngagementReceiptDelivered, end)
	stats, err := s.EngagementStatsBetween(ctx, start, end)
	if err != nil {
		t.Fatal(err)
	}
	want := EngagementKindStats{Kind: "decision_due", Created: 2, Scheduled: 3, Sent: 2, Delivered: 1, Opened: 1, Acted: 1, Dismissed: 2, Muted: 1, DismissedOrMuted: 2, OpenedOrActed: 1, NotificationSent: 2, NotificationOpenedOrActed: 1, NotificationActed: 1, NotificationUseful: 1, Channels: []string{"desktop", "push"}}
	if got := stats["decision_due"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("weekly stats = %+v, want %+v", got, want)
	}
	if got := stats["work_blocked"]; got.Created != 1 || got.Sent != 1 || got.Delivered != 1 {
		t.Fatalf("other kind at upper boundary = %+v", got)
	}
}

func TestEngagementCompletionCandidatesIncludePartialDeliveries(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	for _, id := range []string{"partial", "acted", "old", "future", "unsent"} {
		if _, err := s.CreateEngagementMoment(ctx, EngagementMomentRow{ID: id, Kind: "work_blocked", DedupeKey: id}); err != nil {
			t.Fatal(err)
		}
	}
	for id, sent := range map[string]time.Time{"partial": now, "acted": now, "old": now.Add(-3 * time.Hour), "future": now.Add(time.Second)} {
		if err := s.SetEngagementMomentState(ctx, id, EngagementStateScheduled, "partly sent", EngagementMomentRow{Channel: "desktop", SentAt: sent.UnixMilli()}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RecordEngagementReceipt(ctx, EngagementReceiptRow{MomentID: "acted", Channel: EngagementCompletionChannel, State: EngagementReceiptActed, Action: EngagementCompletionAction}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.EngagementCompletionCandidates(ctx, now.Add(-2*time.Hour), now)
	if err != nil || len(rows) != 1 || rows[0].ID != "partial" {
		t.Fatalf("completion candidates = %+v, %v", rows, err)
	}
}

func TestEngagementWeekCountsRequireActedReceipt(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	for _, id := range []string{"opened", "acted", "before-send"} {
		if _, err := s.CreateEngagementMoment(ctx, EngagementMomentRow{ID: id, Kind: "work_blocked", DedupeKey: id}); err != nil {
			t.Fatal(err)
		}
		if err := s.SetEngagementMomentState(ctx, id, EngagementStateScheduled, "partial", EngagementMomentRow{SentAt: now.UnixMilli()}); err != nil {
			t.Fatal(err)
		}
	}
	for _, receipt := range []EngagementReceiptRow{
		{MomentID: "opened", State: EngagementReceiptOpened, At: now.Add(time.Minute).UnixMilli()},
		{MomentID: "acted", Channel: EngagementCompletionChannel, State: EngagementReceiptActed, Action: EngagementCompletionAction, At: now.Add(time.Minute).UnixMilli()},
		{MomentID: "acted", Channel: EngagementCompletionChannel, State: EngagementReceiptActed, Action: EngagementCompletionAction, At: now.Add(2 * time.Minute).UnixMilli()},
		{MomentID: "before-send", Channel: EngagementCompletionChannel, State: EngagementReceiptActed, Action: EngagementCompletionAction, At: now.Add(-time.Minute).UnixMilli()},
	} {
		if err := s.RecordEngagementReceipt(ctx, receipt); err != nil {
			t.Fatal(err)
		}
	}
	counts, err := s.EngagementCountsSince(ctx, now.Add(-time.Hour))
	if err != nil || counts.Sent != 3 || counts.ActedOn != 1 {
		t.Fatalf("counts = %+v, %v; want 3 sent, 1 completed", counts, err)
	}
}

func TestEngagementChatSendPreservesRoutingAndFirstSend(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	row, err := s.CreateEngagementMoment(ctx, EngagementMomentRow{
		Kind: "work_blocked", DedupeKey: "chat-first", State: EngagementStateScheduled,
		Channel: "push", Reason: "retry", HoldUntil: now.Add(time.Minute).UnixMilli(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RecordEngagementChatSent(ctx, "missing", now); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing chat moment = %v", err)
	}
	for _, at := range []time.Time{now, now.Add(time.Minute)} {
		if err := s.RecordEngagementChatSent(ctx, row.ID, at); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.GetEngagementMoment(ctx, row.ID)
	if err != nil || got.State != row.State || got.Channel != row.Channel || got.Reason != row.Reason || got.HoldUntil != row.HoldUntil || got.SentAt != now.UnixMilli() {
		t.Fatalf("chat send changed routing or first send: %+v, %v", got, err)
	}
	receipts, err := s.ListEngagementReceipts(ctx, row.ID)
	if err != nil || len(receipts) != 3 {
		t.Fatalf("chat lifecycle receipts = %+v, %v", receipts, err)
	}
	var sent EngagementReceiptRow
	for _, receipt := range receipts {
		if receipt.State == EngagementReceiptSent {
			sent = receipt
		}
	}
	if sent.Channel != "desktop" || sent.At != now.UnixMilli() {
		t.Fatalf("chat receipt = %+v", sent)
	}
}

func TestEngagementRestoredSendKeepsEarliestDelivery(t *testing.T) {
	s := newTestStore(t)
	first := time.Date(2026, time.October, 12, 10, 0, 0, 0, time.UTC)
	row, err := s.CreateEngagementMoment(ctx, EngagementMomentRow{
		Kind: "work_blocked", DedupeKey: "restored-delivery", CreatedAt: first.Add(-time.Hour).UnixMilli(),
	})
	if err != nil {
		t.Fatal(err)
	}
	// A chat report is recorded while the earlier external delivery's state
	// write is waiting to retry. The retry restores the actual first send.
	if err := s.RecordEngagementChatSent(ctx, row.ID, first.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	for _, at := range []time.Time{first, first.Add(2 * time.Minute)} {
		if err := s.SetEngagementMomentState(ctx, row.ID, EngagementStateSent, "restored", EngagementMomentRow{Channel: "push", SentAt: at.UnixMilli()}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.GetEngagementMoment(ctx, row.ID)
	if err != nil || got.SentAt != first.UnixMilli() {
		t.Fatalf("earlier delivery not restored: %+v, %v", got, err)
	}
	stats, err := s.EngagementStatsBetween(ctx, first, first)
	if err != nil || stats[row.Kind].Sent != 1 {
		t.Fatalf("original send missing from its cohort: %+v, %v", stats, err)
	}
	// Recovery of an older durable chat report uses the same minimum rule.
	older := first.Add(-time.Second)
	if err := s.RecordEngagementChatSent(ctx, row.ID, older); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetEngagementMoment(ctx, row.ID)
	if err != nil || got.SentAt != older.UnixMilli() || got.State != EngagementStateSent || got.Channel != "push" {
		t.Fatalf("earlier chat evidence changed routing or was lost: %+v, %v", got, err)
	}
}

func TestEngagementLegacyActedRemainsInteractionAndCompletionCandidate(t *testing.T) {
	s := newTestStore(t)
	sentAt := time.Date(2026, time.October, 12, 10, 0, 0, 0, time.UTC)
	until := sentAt.Add(2 * time.Hour)
	for _, legacy := range []struct{ id, channel, action string }{
		{"opened", "push", "open"},
		{"approved", "desktop", "approve"},
		{"scheduled", "text", "schedule"},
		{"wrong-action", EngagementCompletionChannel, "open"},
		{"wrong-channel", "desktop", EngagementCompletionAction},
	} {
		if _, err := s.CreateEngagementMoment(ctx, EngagementMomentRow{
			ID: legacy.id, Kind: "work_blocked", DedupeKey: legacy.id,
			State: EngagementStateSent, Channel: legacy.channel,
			CreatedAt: sentAt.Add(-time.Hour).UnixMilli(), SentAt: sentAt.UnixMilli(),
		}); err != nil {
			t.Fatal(err)
		}
		if err := s.RecordEngagementReceipt(ctx, EngagementReceiptRow{
			MomentID: legacy.id, Channel: legacy.channel, State: EngagementReceiptActed,
			Action: legacy.action, At: sentAt.Add(time.Minute).UnixMilli(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	// A normal opened receipt and an old acted/open receipt on the same item
	// remain one interaction in the summary.
	if err := s.RecordEngagementReceipt(ctx, EngagementReceiptRow{
		MomentID: "opened", Channel: "desktop", State: EngagementReceiptOpened,
		At: sentAt.Add(2 * time.Minute).UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}
	assertCounts := func(acted, candidates int) {
		t.Helper()
		stats, err := s.EngagementStatsBetween(ctx, sentAt, until)
		if err != nil || stats["work_blocked"].Sent != 5 || stats["work_blocked"].Opened != 5 || stats["work_blocked"].Acted != acted {
			t.Fatalf("legacy receipt summary = %+v, %v", stats, err)
		}
		counts, err := s.EngagementCountsSince(ctx, sentAt)
		if err != nil || counts.Sent != 5 || counts.ActedOn != acted {
			t.Fatalf("legacy receipt counts = %+v, %v", counts, err)
		}
		rows, err := s.EngagementCompletionCandidates(ctx, sentAt, until)
		if err != nil || len(rows) != candidates {
			t.Fatalf("completion candidates = %+v, %v", rows, err)
		}
	}
	assertCounts(0, 5)
	if err := s.RecordEngagementReceipt(ctx, EngagementReceiptRow{
		MomentID: "opened", Channel: EngagementCompletionChannel, State: EngagementReceiptActed,
		Action: EngagementCompletionAction, At: sentAt.Add(time.Hour).UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}
	assertCounts(1, 4)
	if err := s.RecordEngagementReceipt(ctx, EngagementReceiptRow{
		MomentID: "wrong-action", Channel: EngagementCompletionChannel, State: EngagementReceiptActed,
		Action: EngagementCompletionAction, At: sentAt.Add(time.Hour).UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}
	assertCounts(2, 3)
	receipts, err := s.ListEngagementReceipts(ctx, "opened")
	if err != nil {
		t.Fatal(err)
	}
	legacyPreserved := false
	for _, receipt := range receipts {
		if receipt.State == EngagementReceiptActed && receipt.Channel == "push" && receipt.Action == "open" {
			legacyPreserved = true
		}
	}
	if !legacyPreserved {
		t.Fatal("legacy interaction history was rewritten")
	}
}

func TestEngagementStatsChannelsFollowReceiptWindow(t *testing.T) {
	s := newTestStore(t)
	start := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	end := start.Add(30 * 24 * time.Hour)
	row, err := s.CreateEngagementMoment(ctx, EngagementMomentRow{Kind: "brief_ready", DedupeKey: "channels", CreatedAt: start.UnixMilli()})
	if err != nil {
		t.Fatal(err)
	}
	sent := start.Add(time.Hour)
	if err := s.SetEngagementMomentState(ctx, row.ID, EngagementStateSent, "sent", EngagementMomentRow{Channel: "desktop", SentAt: sent.UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	for _, receipt := range []EngagementReceiptRow{
		{Channel: "push", State: EngagementReceiptDelivered, At: sent.UnixMilli()},
		{Channel: "today_quiet", State: EngagementReceiptSent, At: end.UnixMilli()},
		{Channel: "text", State: EngagementReceiptSent, At: start.UnixMilli()}, // Before the measured send.
		{Channel: "email", State: EngagementReceiptSent, At: end.Add(time.Millisecond).UnixMilli()},
		{Channel: "text", State: EngagementReceiptOpened, At: sent.UnixMilli()}, // Interaction is not delivery proof.
	} {
		receipt.MomentID = row.ID
		if err := s.RecordEngagementReceipt(ctx, receipt); err != nil {
			t.Fatal(err)
		}
	}
	stats, err := s.EngagementStatsBetween(ctx, start, end)
	if err != nil {
		t.Fatal(err)
	}
	if got := stats[row.Kind]; got.Sent != 1 || got.OpenedOrActed != 1 || !reflect.DeepEqual(got.Channels, []string{"desktop", "push", "today_quiet"}) {
		t.Fatalf("receipt-backed channels and counts: %+v", got)
	}
}
