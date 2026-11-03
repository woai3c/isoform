package main

import (
	"context"
	"sort"
	"strings"
	"time"

	"wails-gui/internal/engagement"
	"wails-gui/storage"
)

const mainChatFollowupMinSamples = 3

func mainChatFollowupDelay(rows []storage.MailMessageRow, send storage.OutboundSendRow, before time.Time) time.Duration {
	threadKey := ""
	for _, row := range rows {
		if engagement.DedupeKey(engagement.KindWaitingOnReply, send.PrincipalID, row.ThreadKey) == send.SourceKey {
			threadKey = row.ThreadKey
			break
		}
	}
	if threadKey == "" {
		return 0
	}
	history := make([]storage.MailMessageRow, 0, len(rows))
	for _, row := range rows {
		if row.ThreadKey == threadKey && row.At > 0 && row.At < before.UnixMilli() {
			history = append(history, row)
		}
	}
	sort.Slice(history, func(i, j int) bool {
		if history[i].At != history[j].At {
			return history[i].At < history[j].At
		}
		return history[i].ID < history[j].ID
	})
	pendingSent := []storage.MailMessageRow{}
	delays := []time.Duration{}
	for _, row := range history {
		switch row.Mailbox {
		case storage.MailboxSent:
			addressed := false
			for _, address := range strings.Split(row.To+"\n"+row.CC, "\n") {
				addressed = addressed || strings.EqualFold(strings.TrimSpace(address), send.DraftTo)
			}
			if addressed {
				pendingSent = append(pendingSent, row)
			}
		case storage.MailboxInbox:
			if row.PrincipalID != send.PrincipalID {
				continue
			}
			for i, outgoing := range pendingSent {
				if outgoing.At >= row.At {
					continue
				}
				delays = append(delays, time.Duration(row.At-outgoing.At)*time.Millisecond)
				pendingSent = append(pendingSent[:i], pendingSent[i+1:]...)
				break
			}
		}
	}
	if len(delays) < mainChatFollowupMinSamples {
		return 0
	}
	sort.Slice(delays, func(i, j int) bool { return delays[i] < delays[j] })
	middle := len(delays) / 2
	if len(delays)%2 == 1 {
		return delays[middle]
	}
	return (delays[middle-1] + delays[middle]) / 2
}

func (a *App) mainChatFollowupTime(ctx context.Context, send storage.OutboundSendRow, sentAt time.Time) time.Time {
	fallback := sentAt.Add(mainChatSendFollowupAfter)
	if send.Channel != "mail" || send.SourceKey == "" {
		return fallback
	}
	rows, err := a.store.ListMailMessages(ctx, sentAt.Add(-mailWindowDays*24*time.Hour).UnixMilli())
	if err != nil {
		return fallback
	}
	if delay := mainChatFollowupDelay(rows, send, sentAt); delay > 0 {
		return sentAt.Add(delay)
	}
	return fallback
}

func (a *App) reconcileMainChatFollowups(ctx context.Context, rows []storage.MailMessageRow) {
	waiting, err := a.store.ListOutboundSendsWaiting(ctx)
	if err != nil {
		return
	}
	changed := false
	for _, send := range waiting {
		if send.Channel != "mail" || send.SourceKey == "" {
			continue
		}
		replied := false
		for _, row := range rows {
			if row.Mailbox == storage.MailboxInbox && row.PrincipalID == send.PrincipalID && row.At > send.SentAt &&
				engagement.DedupeKey(engagement.KindWaitingOnReply, send.PrincipalID, row.ThreadKey) == send.SourceKey {
				replied = true
				break
			}
		}
		if !replied {
			continue
		}
		if err := a.store.RetireOutboundSendFollowup(ctx, send.MomentID, send.Revision); err != nil {
			continue
		}
		if a.engagement != nil {
			_ = a.engagement.Retire(ctx, mainChatFollowupDedupeKey(send), "reply_received")
		}
		changed = true
	}
	if changed {
		a.emitBrowserEvent("people:loops-changed")
		a.emitBrowserEvent("topics:updated")
	}
}
