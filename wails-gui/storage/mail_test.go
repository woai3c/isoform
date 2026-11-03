package storage

import (
	"context"
	"testing"
)

func TestMailMessagesMigrationUpsertGoneAndPrune(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version < 57 {
		t.Fatalf("user_version = %d, %v", version, err)
	}
	var indexes int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name IN ('idx_mail_messages_at', 'idx_mail_messages_thread')`).Scan(&indexes); err != nil || indexes != 2 {
		t.Fatalf("indexes = %d, %v", indexes, err)
	}

	sam := MailMessageRow{ID: "m1", Mailbox: MailboxInbox, SenderEmail: "sam@example.com", SenderName: "Sam",
		To: "me@example.com", ToMe: true, Subject: "Q3 deck", ThreadKey: "t1", At: 5_000, PrincipalID: "p-sam"}
	news := MailMessageRow{ID: "m2", Mailbox: MailboxInbox, SenderEmail: "news@example.com", Subject: "Weekly", ThreadKey: "t2", At: 6_000}
	sent := MailMessageRow{ID: "s1", Mailbox: MailboxSent, SenderEmail: "me@example.com", To: "ann@example.com", Subject: "Plan", ThreadKey: "t3", At: 7_000}
	added, err := s.UpsertMailMessages(ctx, []MailMessageRow{sam, news, sent, {ID: " ", At: 1}, {ID: "no-date"}}, 10)
	if err != nil || len(added) != 3 {
		t.Fatalf("added = %v, %v", added, err)
	}

	// A second read follows Mail's state, keeps first_seen_at and the person.
	sam.Read, sam.Replied, sam.PrincipalID = true, true, ""
	added, err = s.UpsertMailMessages(ctx, []MailMessageRow{sam}, 20)
	if err != nil || len(added) != 0 {
		t.Fatalf("a stored message is not new: %v, %v", added, err)
	}
	rows, err := s.ListMailMessages(ctx, 0)
	if err != nil || len(rows) != 3 || rows[0].ID != "s1" {
		t.Fatalf("rows = %+v, %v", rows, err)
	}
	got := rows[2]
	if got.ID != "m1" || !got.Read || !got.Replied || !got.ToMe || got.PrincipalID != "p-sam" || got.FirstSeenAt != 10 || got.UpdatedAt != 20 {
		t.Fatalf("m1 = %+v", got)
	}

	// Archived in Mail: gone from the inbox, so gone from here.
	removed, err := s.DeleteMailMessagesGone(ctx, MailboxInbox, 0, map[string]bool{"m1": true})
	if err != nil || removed != 1 {
		t.Fatalf("gone = %d, %v", removed, err)
	}
	if rows, _ := s.ListMailMessages(ctx, 0); len(rows) != 2 {
		t.Fatalf("rows after gone = %+v", rows)
	}

	if n, err := s.PruneMailMessages(ctx, 6_000); err != nil || n != 1 {
		t.Fatalf("prune = %d, %v", n, err)
	}
	if n, err := s.DeleteAllMailMessages(ctx); err != nil || n != 1 {
		t.Fatalf("clear = %d, %v", n, err)
	}
}
