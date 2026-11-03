package main

import (
	"errors"
	"testing"
	"time"

	"wails-gui/storage"
)

func TestOpenAnswerMailBindReply(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	anchor := storage.MailMessageRow{ID: "sent", Mailbox: storage.MailboxSent, To: "priya@example.com", Subject: "Plans", ThreadKey: "plans", At: now.Add(-time.Hour).UnixMilli()}
	reply := storage.MailMessageRow{ID: "reply", Mailbox: storage.MailboxInbox, SenderEmail: "priya@example.com", SenderName: "Priya", Subject: "Re: Plans", ThreadKey: "plans", At: now.Add(-time.Minute).UnixMilli()}
	tests := []struct {
		name          string
		rows          []storage.MailMessageRow
		anchorID      string
		correspondent string
		wantReply     string
		wantErr       bool
	}{
		{name: "waiting", rows: []storage.MailMessageRow{anchor}, anchorID: "sent", correspondent: "priya@example.com"},
		{name: "reply header", rows: []storage.MailMessageRow{anchor, reply}, anchorID: "sent", correspondent: "priya@example.com", wantReply: "reply"},
		{name: "reply from another person", rows: []storage.MailMessageRow{anchor, {ID: "other", Mailbox: storage.MailboxInbox, SenderEmail: "other@example.com", Subject: "Plans", ThreadKey: "plans", At: reply.At}}, anchorID: "sent", correspondent: "priya@example.com"},
		{name: "different subject", rows: []storage.MailMessageRow{anchor, {ID: "other", Mailbox: storage.MailboxInbox, SenderEmail: "priya@example.com", Subject: "Other", ThreadKey: "other", At: reply.At}}, anchorID: "sent", correspondent: "priya@example.com"},
		{name: "earlier message", rows: []storage.MailMessageRow{anchor, {ID: "old", Mailbox: storage.MailboxInbox, SenderEmail: "priya@example.com", Subject: "Plans", ThreadKey: "plans", At: anchor.At - 1}}, anchorID: "sent", correspondent: "priya@example.com"},
		{name: "first reply regardless of order", rows: []storage.MailMessageRow{reply, anchor, {ID: "first", Mailbox: storage.MailboxInbox, SenderEmail: "priya@example.com", Subject: "Re: Plans", ThreadKey: "plans", At: anchor.At + 1}}, anchorID: "sent", correspondent: "priya@example.com", wantReply: "first"},
		{name: "missing anchor", rows: []storage.MailMessageRow{reply}, anchorID: "sent", correspondent: "priya@example.com", wantErr: true},
		{name: "wrong anchor", rows: []storage.MailMessageRow{anchor, reply}, anchorID: "reply", correspondent: "priya@example.com", wantErr: true},
		{name: "different recipient", rows: []storage.MailMessageRow{anchor}, anchorID: "sent", correspondent: "other@example.com", wantErr: true},
		{name: "invalid correspondent", rows: []storage.MailMessageRow{anchor}, anchorID: "sent", correspondent: "Priya <priya@example.com>", wantErr: true},
		{name: "duplicate row", rows: []storage.MailMessageRow{anchor, anchor}, anchorID: "sent", correspondent: "priya@example.com", wantErr: true},
		{name: "same timestamp", rows: []storage.MailMessageRow{anchor, {ID: "tie", Mailbox: storage.MailboxInbox, SenderEmail: "priya@example.com", Subject: "Plans", ThreadKey: "plans", At: anchor.At}}, anchorID: "sent", correspondent: "priya@example.com", wantErr: true},
		{name: "future reply", rows: []storage.MailMessageRow{anchor, {ID: "future", Mailbox: storage.MailboxInbox, SenderEmail: "priya@example.com", Subject: "Plans", ThreadKey: "plans", At: now.Add(time.Second).UnixMilli()}}, anchorID: "sent", correspondent: "priya@example.com", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			binding, err := (openAnswerMailSnapshot{rows: tc.rows}).bindReply(tc.anchorID, tc.correspondent, now)
			if tc.wantErr {
				if !errors.Is(err, errOpenAnswerMailBinding) {
					t.Fatalf("binding = %+v, err = %v", binding, err)
				}
				return
			}
			if err != nil || binding.anchor.ID != tc.anchorID {
				t.Fatalf("binding = %+v, err = %v", binding, err)
			}
			if tc.wantReply == "" && binding.reply != nil || tc.wantReply != "" && (binding.reply == nil || binding.reply.ID != tc.wantReply) {
				t.Fatalf("reply = %+v, want %q", binding.reply, tc.wantReply)
			}
			if binding.reply != nil && binding.reply.ID == "reply" && (binding.reply.SenderName != "Priya" || binding.reply.At != reply.At) {
				t.Fatalf("reply header lost name or time: %+v", binding.reply)
			}
		})
	}
}

func TestOpenAnswerMailBindExplicitReplyReference(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	anchor := storage.MailMessageRow{ID: "sent", Mailbox: storage.MailboxSent, To: "priya@example.com", Subject: "Plans", ThreadKey: "plans", At: now.Add(-time.Hour).UnixMilli()}
	reply := storage.MailMessageRow{ID: "reply", Mailbox: storage.MailboxInbox, SenderEmail: "priya@example.com", Subject: "Re: Plans", ThreadKey: "plans", At: now.Add(-time.Minute).UnixMilli()}
	other := anchor
	other.ID = "another-sent"
	tests := []struct {
		name, question, correspondent string
		rows                          []storage.MailMessageRow
		wantErr                       bool
	}{
		{name: "explicit reference", question: `Has priya@example.com replied to "Plans"?`, correspondent: "priya@example.com", rows: []storage.MailMessageRow{anchor, reply}},
		{name: "address case", question: `Has PRIYA@example.com replied to "Plans"?`, correspondent: "priya@example.com", rows: []storage.MailMessageRow{anchor, reply}},
		{name: "address only in longer address", question: `Has xpriya@example.com replied to "Plans"?`, correspondent: "priya@example.com", rows: []storage.MailMessageRow{anchor}, wantErr: true},
		{name: "longer domain", question: `Has priya@example.com.invalid replied to "Plans"?`, correspondent: "priya@example.com", rows: []storage.MailMessageRow{anchor}, wantErr: true},
		{name: "unicode identifier", question: `Has 人priya@example.com replied to "Plans"?`, correspondent: "priya@example.com", rows: []storage.MailMessageRow{anchor}, wantErr: true},
		{name: "unquoted subject", question: `Has priya@example.com replied about Plans?`, correspondent: "priya@example.com", rows: []storage.MailMessageRow{anchor}, wantErr: true},
		{name: "different subject", question: `Has priya@example.com replied to "Other"?`, correspondent: "priya@example.com", rows: []storage.MailMessageRow{anchor}, wantErr: true},
		{name: "model supplied person", question: `Has Priya replied to "Plans"?`, correspondent: "priya@example.com", rows: []storage.MailMessageRow{anchor}, wantErr: true},
		{name: "address only in quoted text", question: `Has anyone replied to "Plans" from "priya@example.com"?`, correspondent: "priya@example.com", rows: []storage.MailMessageRow{anchor}, wantErr: true},
		{name: "ambiguous sent headers", question: `Has priya@example.com replied to "Plans"?`, correspondent: "priya@example.com", rows: []storage.MailMessageRow{anchor, other}, wantErr: true},
		{name: "not a sent recipient", question: `Has other@example.com replied to "Plans"?`, correspondent: "other@example.com", rows: []storage.MailMessageRow{anchor}, wantErr: true},
		{name: "invalid supplied correspondent", question: `Has priya@example.com replied to "Plans"?`, correspondent: "Priya <priya@example.com>", rows: []storage.MailMessageRow{anchor}, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			binding, err := (openAnswerMailSnapshot{rows: tc.rows}).bindExplicitReplyReference(tc.question, tc.correspondent, now)
			if tc.wantErr {
				if !errors.Is(err, errOpenAnswerMailBinding) {
					t.Fatalf("binding = %+v, err = %v", binding, err)
				}
				return
			}
			if err != nil || binding.anchor.ID != "sent" || binding.reply == nil || binding.reply.ID != "reply" {
				t.Fatalf("binding = %+v, err = %v", binding, err)
			}
		})
	}
}

func TestOpenAnswerMailBindNamedReplyReference(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	anchor := storage.MailMessageRow{ID: "sent", Mailbox: storage.MailboxSent, To: "priya@example.com", Subject: "Plans", ThreadKey: "plans", At: now.Add(-time.Hour).UnixMilli()}
	tests := []struct {
		name, question, person, correspondent string
		wantErr                               bool
	}{
		{name: "name and subject", question: `Has Priya replied to "Plans"?`, person: "Priya", correspondent: "priya@example.com"},
		{name: "possessive name", question: `Is Priya's reply to "Plans" here?`, person: "Priya", correspondent: "priya@example.com"},
		{name: "different person", question: `Has Maria replied to "Plans"?`, person: "Priya", correspondent: "priya@example.com", wantErr: true},
		{name: "part of a name", question: `Has Apriya replied to "Plans"?`, person: "Priya", correspondent: "priya@example.com", wantErr: true},
		{name: "name only quoted", question: `Has "Priya" replied to "Plans"?`, person: "Priya", correspondent: "priya@example.com", wantErr: true},
		{name: "no name", question: `Has anyone replied to "Plans"?`, person: "", correspondent: "priya@example.com", wantErr: true},
		{name: "unresolved address", question: `Has Priya replied to "Plans"?`, person: "Priya", correspondent: "other@example.com", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			binding, err := (openAnswerMailSnapshot{rows: []storage.MailMessageRow{anchor}}).bindNamedReplyReference(tc.question, tc.person, tc.correspondent, now)
			if tc.wantErr {
				if !errors.Is(err, errOpenAnswerMailBinding) {
					t.Fatalf("binding = %+v, err = %v", binding, err)
				}
				return
			}
			if err != nil || binding.anchor.ID != "sent" || binding.reply != nil {
				t.Fatalf("binding = %+v, err = %v", binding, err)
			}
		})
	}
}
