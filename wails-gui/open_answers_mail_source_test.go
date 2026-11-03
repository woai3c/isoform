package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"wails-gui/storage"
)

func TestOpenAnswerMailSnapshotRequiresCurrentObserver(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	tests := []struct {
		name   string
		change func(mailTestService)
		want   bool
	}{
		{name: "current", want: true},
		{name: "disabled", change: func(s mailTestService) { *s.enabled = false }},
		{name: "protected", change: func(s mailTestService) { *s.guarded = true }},
		{name: "paused", change: func(s mailTestService) { *s.paused = true }},
		{name: "mail closed", change: func(s mailTestService) { s.mail.running = false }},
		{name: "automation denied", change: func(s mailTestService) { s.mail.state = mailAutomationDenied }},
		{name: "automation undecided", change: func(s mailTestService) { s.mail.state = mailAutomationUndecided }},
		{name: "stopped", change: func(s mailTestService) { s.cancel = nil }},
		{name: "cancelled parent", change: func(s mailTestService) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			s.loopCtx = ctx
		}},
		{name: "never read", change: func(s mailTestService) { s.lastSync = time.Time{} }},
		{name: "stale", change: func(s mailTestService) { s.lastSync = now.Add(-2*mailSyncInterval - time.Second) }},
		{name: "future read", change: func(s mailTestService) { s.lastSync = now.Add(time.Second) }},
		{name: "failed read", change: func(s mailTestService) { s.lastErr = "failed" }},
		{name: "closed on last read", change: func(s mailTestService) { s.notRunning = true }},
		{name: "one timed out read", change: func(s mailTestService) { s.slowReads = 1 }},
		{name: "stalled", change: func(s mailTestService) { s.slowReads = mailSlowLimit }},
		{name: "inconclusive inbox", change: func(s mailTestService) { s.emptyReads[storage.MailboxInbox] = 1 }},
		{name: "inconclusive sent", change: func(s mailTestService) { s.emptyReads[storage.MailboxSent] = 1 }},
		{name: "confirmed empty", change: func(s mailTestService) { s.emptyReads[storage.MailboxSent] = 2 }, want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestMailService(t, now, nil)
			s.cancel = func() {}
			s.loopCtx = context.Background()
			s.lastSync = now
			s.emptyReads = map[string]int{}
			_, err := s.store.UpsertMailMessages(t.Context(), []storage.MailMessageRow{
				{ID: "current", Mailbox: storage.MailboxInbox, At: now.Add(-time.Hour).UnixMilli(), SenderEmail: "priya@example.com"},
				{ID: "old", Mailbox: storage.MailboxInbox, At: now.Add(-storage.MailRetention - time.Second).UnixMilli()},
			}, now.UnixMilli())
			if err != nil {
				t.Fatal(err)
			}
			s.lastReadRows = map[string]openAnswerMailFingerprint{"current": fingerprintOpenAnswerMail(storage.MailMessageRow{
				ID: "current", Mailbox: storage.MailboxInbox, At: now.Add(-time.Hour).UnixMilli(), SenderEmail: "priya@example.com",
			})}
			if tc.change != nil {
				tc.change(s)
			}
			snapshot, err := s.openAnswerSnapshot(t.Context(), now)
			if !tc.want {
				if !errors.Is(err, errOpenAnswerMailUnavailable) || len(snapshot.rows) != 0 {
					t.Fatalf("snapshot = %+v, err = %v", snapshot, err)
				}
				return
			}
			if err != nil || len(snapshot.rows) != 1 || snapshot.rows[0].ID != "current" {
				t.Fatalf("snapshot = %+v, err = %v", snapshot, err)
			}
			if s.mail.runs != 0 {
				t.Fatal("snapshot must not read Mail or its bodies")
			}
			if _, err := s.store.UpsertMailMessages(context.Background(), []storage.MailMessageRow{{ID: "later", Mailbox: storage.MailboxInbox, At: now.UnixMilli()}}, now.UnixMilli()); err != nil {
				t.Fatal(err)
			}
			release, err := s.store.LockReadRevision(t.Context(), snapshot.revision)
			if release != nil {
				release()
			}
			if !errors.Is(err, storage.ErrReadRevisionChanged) {
				t.Fatalf("stale revision error = %v", err)
			}
		})
	}
}

func TestOpenAnswerMailSnapshotRechecksPrivacy(t *testing.T) {
	now := time.Now()
	s := newTestMailService(t, now, nil)
	s.cancel = func() {}
	s.loopCtx = context.Background()
	s.lastSync = now
	s.lastReadRows = map[string]openAnswerMailFingerprint{}
	calls := 0
	s.protected = func() bool { calls++; return calls > 1 }
	snapshot, err := s.openAnswerSnapshot(t.Context(), now)
	if !errors.Is(err, errOpenAnswerMailUnavailable) || len(snapshot.rows) != 0 {
		t.Fatalf("snapshot after protection changed = %+v, err = %v", snapshot, err)
	}
}

func TestOpenAnswerMailSnapshotUnavailableDuringRead(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	s := newTestMailService(t, now, nil)
	s.cancel = func() {}
	s.loopCtx = context.Background()
	inbox := testHeader{ID: "original", From: "Priya <priya@example.com>", Subject: "Plans", At: now.Add(-time.Hour).UnixMilli(), To: []string{"me@example.com"}}
	sent := testHeader{ID: "sent", From: "Me <me@example.com>", Subject: "Plans", At: now.Add(-time.Minute).UnixMilli(), To: []string{"priya@example.com"}}
	s.mail.out = mailSnapshotJSON(t, []testHeader{inbox}, []testHeader{sent})
	if _, err := s.Sync(t.Context(), "initial"); err != nil {
		t.Fatal(err)
	}
	if snapshot, err := s.openAnswerSnapshot(t.Context(), now); err != nil || len(snapshot.rows) != 2 {
		t.Fatalf("initial snapshot: rows=%d, err=%v", len(snapshot.rows), err)
	}

	reply := testHeader{ID: "reply", From: "Priya <priya@example.com>", Subject: "Re: Plans", At: now.UnixMilli(), To: []string{"me@example.com"}}
	next := []byte(mailSnapshotJSON(t, []testHeader{inbox, reply}, []testHeader{sent}))
	started, release, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	s.run = func(ctx context.Context, _ string) ([]byte, error) {
		close(started)
		select {
		case <-release:
			return next, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	go func() {
		_, err := s.Sync(t.Context(), "next")
		finished <- err
	}()
	<-started
	_, err := s.openAnswerSnapshot(t.Context(), now)
	close(release)
	if syncErr := <-finished; syncErr != nil {
		t.Fatalf("next read: %v", syncErr)
	}
	if !errors.Is(err, errOpenAnswerMailUnavailable) {
		t.Fatalf("snapshot during pending read: %v", err)
	}
	snapshot, err := s.openAnswerSnapshot(t.Context(), now)
	if err != nil || len(snapshot.rows) != 3 {
		t.Fatalf("snapshot after completed read: rows=%d, err=%v", len(snapshot.rows), err)
	}
	for _, row := range snapshot.rows {
		if row.ID == mailMessageKey(storage.MailboxInbox, "reply") {
			return
		}
	}
	t.Fatal("completed snapshot omitted the reply")
}

func TestOpenAnswerMailSnapshotObserverLifecycle(t *testing.T) {
	now := time.Now()
	s := newTestMailService(t, now, nil)
	s.lastSync = now
	ctx, cancel := context.WithCancel(t.Context())
	s.Start(ctx)
	defer s.Stop()
	if _, err := s.openAnswerSnapshot(t.Context(), now); !errors.Is(err, errOpenAnswerMailUnavailable) {
		t.Fatalf("observer before first read: %v", err)
	}
	s.writeMu.Lock()
	s.lastReadRows = map[string]openAnswerMailFingerprint{}
	s.writeMu.Unlock()
	if _, err := s.openAnswerSnapshot(t.Context(), now); err != nil {
		t.Fatalf("active observer: %v", err)
	}
	cancel()
	if _, err := s.openAnswerSnapshot(t.Context(), now); !errors.Is(err, errOpenAnswerMailUnavailable) {
		t.Fatalf("cancelled observer: %v", err)
	}
	s.Stop()
	if _, err := s.openAnswerSnapshot(t.Context(), now); !errors.Is(err, errOpenAnswerMailUnavailable) {
		t.Fatalf("stopped observer: %v", err)
	}
	s.Start(t.Context())
	if _, err := s.openAnswerSnapshot(t.Context(), now); !errors.Is(err, errOpenAnswerMailUnavailable) {
		t.Fatalf("restarted observer before new read: %v", err)
	}
}

func TestOpenAnswerMailSnapshotRejectsChangedHeaders(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	for _, tc := range []struct {
		name   string
		change func(*storage.MailMessageRow)
	}{
		{"sender", func(row *storage.MailMessageRow) { row.SenderEmail = "someone@example.com" }},
		{"name", func(row *storage.MailMessageRow) { row.SenderName = "Someone" }},
		{"recipient", func(row *storage.MailMessageRow) { row.To = "someone@example.com" }},
		{"subject", func(row *storage.MailMessageRow) { row.Subject = "Other" }},
		{"thread", func(row *storage.MailMessageRow) { row.ThreadKey = "other" }},
		{"date", func(row *storage.MailMessageRow) { row.At++ }},
		{"read state", func(row *storage.MailMessageRow) { row.Read = !row.Read }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestMailService(t, now, nil)
			s.cancel = func() {}
			s.loopCtx = context.Background()
			current := testHeader{ID: "current", From: "Priya <priya@example.com>", Subject: "Plans", At: now.Add(-time.Hour).UnixMilli(), To: []string{"me@example.com"}}
			s.mail.out = mailSnapshotJSON(t, []testHeader{current}, []testHeader{current})
			if _, err := s.Sync(t.Context(), "test"); err != nil {
				t.Fatal(err)
			}
			rows, err := s.store.ListMailMessages(t.Context(), now.Add(-storage.MailRetention).UnixMilli())
			if err != nil || len(rows) != 2 {
				t.Fatalf("stored rows = %+v, err = %v", rows, err)
			}
			tc.change(&rows[0])
			if _, err := s.store.UpsertMailMessages(t.Context(), rows[:1], now.UnixMilli()); err != nil {
				t.Fatal(err)
			}
			if _, err := s.openAnswerSnapshot(t.Context(), now); !errors.Is(err, errOpenAnswerMailUnavailable) {
				t.Fatalf("changed header error = %v", err)
			}
		})
	}
}

func TestOpenAnswerMailSnapshotUsesLatestResolvedPerson(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	s := newTestMailService(t, now, nil)
	s.cancel = func() {}
	s.loopCtx = context.Background()
	current := testHeader{ID: "current", From: "Priya <priya@example.com>", Subject: "Plans", At: now.Add(-time.Hour).UnixMilli(), To: []string{"me@example.com"}}
	s.mail.out = mailSnapshotJSON(t, []testHeader{current}, []testHeader{current})
	if _, err := s.Sync(t.Context(), "test"); err != nil {
		t.Fatal(err)
	}
	rows, err := s.store.ListMailMessages(t.Context(), now.Add(-storage.MailRetention).UnixMilli())
	if err != nil || len(rows) != 2 {
		t.Fatalf("stored rows = %+v, err = %v", rows, err)
	}
	rows[0].PrincipalID = "old-person"
	if _, err := s.store.UpsertMailMessages(t.Context(), rows[:1], now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.openAnswerSnapshot(t.Context(), now)
	if err != nil || len(snapshot.rows) != 2 {
		t.Fatalf("snapshot = %+v, err = %v", snapshot, err)
	}
	for _, row := range snapshot.rows {
		if row.ID == rows[0].ID && row.PrincipalID != "" {
			t.Fatalf("snapshot used retained person: %+v", row)
		}
	}
}

func TestOpenAnswerMailSnapshotRejectsChangedResolvedPerson(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	s := newTestMailService(t, now, map[string]string{"priya@example.com": "priya"})
	s.cancel = func() {}
	s.loopCtx = context.Background()
	current := testHeader{ID: "current", From: "Priya <priya@example.com>", Subject: "Plans", At: now.Add(-time.Hour).UnixMilli(), To: []string{"me@example.com"}}
	s.mail.out = mailSnapshotJSON(t, []testHeader{current}, []testHeader{current})
	if _, err := s.Sync(t.Context(), "test"); err != nil {
		t.Fatal(err)
	}
	rows, err := s.store.ListMailMessages(t.Context(), now.Add(-storage.MailRetention).UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	var inbox storage.MailMessageRow
	for _, row := range rows {
		if row.Mailbox == storage.MailboxInbox {
			inbox = row
		}
	}
	if inbox.PrincipalID != "priya" {
		t.Fatalf("resolved inbox = %+v", inbox)
	}
	inbox.PrincipalID = "someone-else"
	if _, err := s.store.UpsertMailMessages(t.Context(), []storage.MailMessageRow{inbox}, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.openAnswerSnapshot(t.Context(), now); !errors.Is(err, errOpenAnswerMailUnavailable) {
		t.Fatalf("changed person error = %v", err)
	}
}

func TestOpenAnswerMailSnapshotExcludesRetainedMissingRows(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	s := newTestMailService(t, now, nil)
	s.cancel = func() {}
	s.loopCtx = context.Background()
	old := testHeader{ID: "old", From: "Priya <priya@example.com>", Subject: "Plans", At: now.Add(-storage.MailRetention + time.Minute).UnixMilli(), To: []string{"me@example.com"}}
	current := testHeader{ID: "current", From: "Priya <priya@example.com>", Subject: "Other", At: now.Add(-time.Hour).UnixMilli(), To: []string{"me@example.com"}}
	s.mail.out = mailSnapshotJSON(t, []testHeader{old, current}, []testHeader{current})
	if _, err := s.Sync(t.Context(), "test"); err != nil {
		t.Fatal(err)
	}
	// The old row remains stored because it is within the gone margin.
	// Reuse the same timestamp to show timestamps alone cannot prove a row
	// came from the last read.
	s.mail.out = mailSnapshotJSON(t, []testHeader{current}, []testHeader{current})
	if _, err := s.Sync(t.Context(), "test"); err != nil {
		t.Fatal(err)
	}
	stored, err := s.store.ListMailMessages(t.Context(), now.Add(-storage.MailRetention).UnixMilli())
	if err != nil || len(stored) != 3 {
		t.Fatalf("retained rows = %+v, err = %v", stored, err)
	}
	snapshot, err := s.openAnswerSnapshot(t.Context(), now)
	if err != nil || len(snapshot.rows) != 2 {
		t.Fatalf("snapshot = %+v, err = %v", snapshot, err)
	}
	for _, row := range snapshot.rows {
		if row.ID == mailMessageKey(storage.MailboxInbox, "old") {
			t.Fatal("snapshot included a retained row absent from the latest read")
		}
	}
	if _, err := s.store.DeleteAllMailMessages(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.openAnswerSnapshot(t.Context(), now); !errors.Is(err, errOpenAnswerMailUnavailable) {
		t.Fatalf("missing latest rows must fail closed: %v", err)
	}
}

func TestOpenAnswerMailSnapshotRejectsIncompleteReadAndRecovers(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	valid := testHeader{ID: "current", From: "Priya <priya@example.com>", Subject: "Plans", At: now.Add(-time.Hour).UnixMilli(), To: []string{"me@example.com"}}
	for _, tc := range []struct {
		name  string
		inbox []testHeader
		pause bool
	}{
		{name: "missing id", inbox: []testHeader{valid, {From: "other@example.com", At: valid.At}}},
		{name: "missing date", inbox: []testHeader{valid, {ID: "undated", From: "other@example.com"}}},
		{name: "invalid sender", inbox: []testHeader{valid, {ID: "unknown", From: "invalid", At: valid.At}}},
		{name: "invalid recipient", inbox: []testHeader{{ID: "current", From: valid.From, Subject: valid.Subject, At: valid.At, To: []string{"me@example.com", "invalid"}}}},
		{name: "duplicate id", inbox: []testHeader{valid, valid}},
		{name: "filtered during pause", inbox: []testHeader{valid}, pause: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestMailService(t, now, nil)
			s.cancel = func() {}
			s.loopCtx = context.Background()
			if tc.pause {
				*s.pauseLog = []observationPause{{Start: now.Add(-2 * time.Hour).UnixMilli(), End: now.Add(-time.Minute).UnixMilli()}}
			}
			s.mail.out = mailSnapshotJSON(t, tc.inbox, []testHeader{valid})
			if _, err := s.Sync(t.Context(), "test"); err != nil {
				t.Fatal(err)
			}
			if _, err := s.openAnswerSnapshot(t.Context(), now); !errors.Is(err, errOpenAnswerMailUnavailable) {
				t.Fatalf("incomplete read error = %v", err)
			}
			*s.pauseLog = nil
			s.mail.out = mailSnapshotJSON(t, []testHeader{valid}, []testHeader{valid})
			if _, err := s.Sync(t.Context(), "test"); err != nil {
				t.Fatal(err)
			}
			snapshot, err := s.openAnswerSnapshot(t.Context(), now)
			if err != nil || len(snapshot.rows) != 2 {
				t.Fatalf("complete subsequent read = %+v, %v", snapshot, err)
			}
		})
	}
}

func TestOpenAnswerMailSnapshotRejectsUnverifiedProperties(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	s := newTestMailService(t, now, nil)
	s.cancel = func() {}
	s.loopCtx = context.Background()
	// Two empty reads normally establish an empty mailbox. An unverified
	// property read must not become evidence even after the second read.
	s.mail.out = `{"running":true,"complete":false,"me":[],"inbox":[],"sent":[]}`
	for i := 0; i < 2; i++ {
		if _, err := s.Sync(t.Context(), "test"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.openAnswerSnapshot(t.Context(), now); !errors.Is(err, errOpenAnswerMailUnavailable) {
		t.Fatalf("unverified properties error = %v", err)
	}
	s.mail.out = mailSnapshotJSON(t, nil, nil)
	if _, err := s.Sync(t.Context(), "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.openAnswerSnapshot(t.Context(), now); !errors.Is(err, errOpenAnswerMailUnavailable) {
		t.Fatalf("one complete empty read after incomplete reads: %v", err)
	}
	if _, err := s.Sync(t.Context(), "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.openAnswerSnapshot(t.Context(), now); err != nil {
		t.Fatalf("complete subsequent read: %v", err)
	}
	s.mail.err = errors.New("read failed")
	if _, err := s.Sync(t.Context(), "test"); err == nil {
		t.Fatal("expected a failed observer read")
	}
	s.mail.err = nil
	if _, err := s.Sync(t.Context(), "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.openAnswerSnapshot(t.Context(), now); !errors.Is(err, errOpenAnswerMailUnavailable) {
		t.Fatalf("one complete empty read after failed read: %v", err)
	}
	if _, err := s.Sync(t.Context(), "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.openAnswerSnapshot(t.Context(), now); err != nil {
		t.Fatalf("second complete empty read after failure: %v", err)
	}
}
