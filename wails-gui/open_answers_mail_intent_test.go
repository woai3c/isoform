package main

import (
	"errors"
	"testing"
	"time"

	"wails-gui/internal/openanswer"
	"wails-gui/storage"
)

func TestOpenAnswerMailBindPersistedReplyTurn(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	question := `Has Priya replied to "Plans"?`
	for _, tc := range []struct {
		name     string
		question string
		mutate   func(*testing.T, *App, *openAnswerMailSnapshot, *openanswer.Candidate)
		want     bool
	}{
		{name: "persisted question and known person", want: true},
		{name: "did reply form", question: `Did Priya reply to "Plans"?`, want: true},
		{name: "question about someone else", question: `Has Maria replied to "Plans"?`},
		{name: "mention is not reply intent", question: `Please tell Priya about "Plans"?`},
		{name: "negation is not reply intent", question: `Has Priya not replied to "Plans"?`},
		{name: "candidate changes question", mutate: func(_ *testing.T, _ *App, _ *openAnswerMailSnapshot, c *openanswer.Candidate) {
			c.Question = `Has Maria replied to "Plans"?`
		}},
		{name: "candidate changes source", mutate: func(_ *testing.T, _ *App, _ *openAnswerMailSnapshot, c *openanswer.Candidate) { c.Source = "Calendar" }},
		{name: "duplicate name", mutate: func(t *testing.T, a *App, _ *openAnswerMailSnapshot, _ *openanswer.Candidate) {
			_, err := a.store.CreatePrincipal(t.Context(), storage.PrincipalRow{Kind: storage.PrincipalKindContact, DisplayName: "Priya"})
			if err != nil {
				t.Fatal(err)
			}
		}},
		{name: "shared address", mutate: func(t *testing.T, a *App, _ *openAnswerMailSnapshot, _ *openanswer.Candidate) {
			p, err := a.store.CreatePrincipal(t.Context(), storage.PrincipalRow{Kind: storage.PrincipalKindContact, DisplayName: "Other"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := a.store.BindAppHandle(t.Context(), p.ID, "email", "PRIYA@example.com", "Other"); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "multiple sent headers", mutate: func(_ *testing.T, _ *App, s *openAnswerMailSnapshot, _ *openanswer.Candidate) {
			other := s.rows[0]
			other.ID = "another"
			s.rows = append(s.rows, other)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := mainChatTestApp(t)
			session, err := a.ensureMainChat(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			p, err := a.store.CreatePrincipal(t.Context(), storage.PrincipalRow{Kind: storage.PrincipalKindContact, DisplayName: "Priya"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := a.store.BindAppHandle(t.Context(), p.ID, "email", "priya@example.com", "Priya"); err != nil {
				t.Fatal(err)
			}
			q := question
			if tc.question != "" {
				q = tc.question
			}
			if _, err := a.store.AppendStreamItem(t.Context(), session.LocalID, storage.StreamItemRow{ID: "user", Kind: "user", Content: q, RunID: "run"}); err != nil {
				t.Fatal(err)
			}
			candidate := openanswer.Candidate{Question: q, Settles: "reply", Source: "Mail"}
			snapshot := openAnswerMailSnapshot{rows: []storage.MailMessageRow{
				{ID: "sent", Mailbox: storage.MailboxSent, To: "priya@example.com", Subject: "Plans", ThreadKey: "plans", At: now.Add(-time.Hour).UnixMilli()},
				{ID: "reply", Mailbox: storage.MailboxInbox, SenderEmail: "priya@example.com", SenderName: "Priya", Subject: "Re: Plans", ThreadKey: "plans", At: now.Add(-time.Minute).UnixMilli()},
			}}
			if tc.mutate != nil {
				tc.mutate(t, a, &snapshot, &candidate)
			}
			snapshot.revision, err = a.store.ReadRevision(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			got, err := bindMailReplyTurn(t.Context(), a.store, snapshot, session.LocalID, "user", "run", candidate, now)
			if !tc.want {
				if !errors.Is(err, errOpenAnswerMailBinding) {
					t.Fatalf("binding = %+v, err = %v", got, err)
				}
				return
			}
			if err != nil || got.anchor.ID != "sent" || got.reply == nil || got.reply.ID != "reply" {
				t.Fatalf("binding = %+v, err = %v", got, err)
			}
			if _, err := bindMailReplyTurn(t.Context(), a.store, snapshot, session.LocalID, "user", "other-run", candidate, now); !errors.Is(err, errOpenAnswerMailBinding) {
				t.Fatalf("wrong run: %v", err)
			}
			if _, err := bindMailReplyTurn(t.Context(), a.store, snapshot, "other-session", "user", "run", candidate, now); !errors.Is(err, errOpenAnswerMailBinding) {
				t.Fatalf("wrong session: %v", err)
			}
			if _, err := a.store.CreatePrincipal(t.Context(), storage.PrincipalRow{Kind: storage.PrincipalKindContact, DisplayName: "Another"}); err != nil {
				t.Fatal(err)
			}
			if _, err := bindMailReplyTurn(t.Context(), a.store, snapshot, session.LocalID, "user", "run", candidate, now); !errors.Is(err, storage.ErrReadRevisionChanged) {
				t.Fatalf("stale identity read: %v", err)
			}
			if err := a.store.UpdateStreamItemContent(t.Context(), "user", "Changed"); err != nil {
				t.Fatal(err)
			}
			if _, err := bindMailReplyTurn(t.Context(), a.store, snapshot, session.LocalID, "user", "run", candidate, now); !errors.Is(err, errOpenAnswerMailBinding) {
				t.Fatalf("changed turn: %v", err)
			}
		})
	}
}
