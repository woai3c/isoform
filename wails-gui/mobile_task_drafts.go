package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"wails-gui/internal/engagement"
	"wails-gui/storage"
)

// A reply Isoform drafted is a task too (Phone Threads §1 and §4): it waits in
// Needs you as "Draft ready", opens a thread with the draft card, and ends with
// an outcome. Its source is the waiting_on_reply card in main Chat, read through
// the same gates the phone's Chat and Today use; nothing new leaves the Mac.

const (
	mobileDraftCandidates = 200
	mobileDraftTaskPrefix = "draft."
	mobileDraftDoneWindow = 7 * 24 * time.Hour
)

type MobileTaskRecipients struct {
	To          []string `json:"to"`
	CC          []string `json:"cc,omitempty"`
	NotIncluded []string `json:"notIncluded,omitempty"`
}

// MobileTaskDraft is the draft card, built from the stored draft and mail
// headers, never from the model's prose.
type MobileTaskDraft struct {
	Channel    string                `json:"channel"`
	Why        *mobileOfferEvidence  `json:"why,omitempty"`
	To         string                `json:"to"`
	Subject    string                `json:"subject,omitempty"`
	Body       string                `json:"body"`
	Recipients *MobileTaskRecipients `json:"recipients,omitempty"`
	State      string                `json:"state"`
	SentAt     string                `json:"sentAt,omitempty"`
	Revision   string                `json:"revision,omitempty"`
}

// mobileDraftTask is one reply draft and what is known about its thread.
type mobileDraftTask struct {
	TaskID    string
	ChatID    string
	Card      mainChatProactive
	At        int64 // when the card was posted
	Sent      *storage.OutboundSendRow
	Inbound   *storage.MailMessageRow
	Others    []string
	RepliedAt int64 // the person answered the thread themselves, after the draft's message
}

func mobileDraftTaskID(momentID string) string {
	sum := sha256.Sum256([]byte("isoform:draft-task\x00" + momentID))
	return mobileDraftTaskPrefix + hex.EncodeToString(sum[:12])
}

func mobileDraftCard(card *mainChatProactive) bool {
	return card != nil && card.Kind == string(engagement.KindWaitingOnReply) && card.Draft && card.DraftContent != nil &&
		strings.TrimSpace(card.DraftContent.Text) != ""
}

// mobileDraftTasks lists the reply drafts waiting for the person and those that
// ended in the last week, newest first. Mail headers answer whether the person
// replied themselves and who else is on the thread; without them the drafts
// still list.
func (a *App) mobileDraftTasks(ctx context.Context, now time.Time) ([]mobileDraftTask, error) {
	store := a.storeForMobile()
	if store == nil {
		return nil, nil
	}
	out := []mobileDraftTask{}
	seen := map[string]bool{}
	// Read the stored cards and gate each with the send check itself: the voice
	// receipt and a fresh read of its mail source. A draft the phone could not
	// send is not listed.
	snapshot, err := store.ReadMainChatWaitingCandidates(ctx, math.MaxInt64, "~", mobileDraftCandidates)
	if err != nil {
		return nil, err
	}
	for i := range snapshot.Items {
		row := snapshot.Items[i]
		if row.ToolName != mainChatProactiveTool {
			continue
		}
		var root mainChatProactive
		if json.Unmarshal([]byte(row.ToolInput), &root) != nil {
			continue
		}
		// As the send route reads it, so the gate below checks the same card.
		normalizeMainChatProactiveActions(&root)
		cards := []mainChatProactive{root}
		if root.Kind == "digest" {
			cards = root.Items
		}
		for _, candidate := range cards {
			if !mobileDraftCard(&candidate) || seen[candidate.MomentID] {
				continue
			}
			// The waiting index holds a display copy; the send reads the stored row.
			stored, err := store.FindProactiveStreamItem(ctx, snapshot.SessionID, candidate.MomentID)
			if err != nil || stored == nil {
				continue
			}
			card, ok := mobileDraftCardIn(stored, candidate.MomentID)
			if !ok || mainChatProactiveRowClass(card, now) != MainChatRowWaiting {
				continue
			}
			if err := a.validateMainChatSend(ctx, &card); err != nil {
				continue
			}
			seen[card.MomentID] = true
			out = append(out, mobileDraftTask{TaskID: mobileDraftTaskID(card.MomentID), ChatID: snapshot.SessionID,
				Card: card, At: row.CreatedAt})
		}
	}
	if main, err := store.LoadMainSession(ctx); err == nil && main != nil {
		sends, err := store.ListOutboundSendsSentSince(ctx, now.Add(-mobileDraftDoneWindow).UnixMilli())
		if err != nil {
			return nil, err
		}
		for i := range sends {
			send := sends[i]
			if seen[send.MomentID] {
				continue
			}
			row, err := store.FindProactiveStreamItem(ctx, main.LocalID, send.MomentID)
			if err != nil || row == nil {
				continue
			}
			card, ok := mobileDraftCardIn(row, send.MomentID)
			if !ok {
				continue
			}
			seen[send.MomentID] = true
			out = append(out, mobileDraftTask{TaskID: mobileDraftTaskID(send.MomentID), ChatID: main.LocalID,
				Card: card, At: row.CreatedAt, Sent: &send})
		}
	}
	a.attachDraftMail(ctx, out)
	sort.SliceStable(out, func(i, j int) bool { return out[i].At > out[j].At })
	return out, nil
}

func mobileDraftCardIn(row *storage.StreamItemRow, momentID string) (mainChatProactive, bool) {
	var root mainChatProactive
	if json.Unmarshal([]byte(row.ToolInput), &root) != nil {
		return mainChatProactive{}, false
	}
	normalizeMainChatProactiveActions(&root)
	if root.MomentID == momentID && mobileDraftCard(&root) {
		return root, true
	}
	for _, child := range root.Items {
		if child.MomentID == momentID && mobileDraftCard(&child) {
			return child, true
		}
	}
	return mainChatProactive{}, false
}

// attachDraftMail finds each draft's message and whether the person answered
// it themselves, from the headers the Mac keeps for about a week.
func (a *App) attachDraftMail(ctx context.Context, tasks []mobileDraftTask) {
	store := a.storeForMobile()
	if store == nil || len(tasks) == 0 {
		return
	}
	rows, err := store.ListMailMessages(ctx, 0)
	if err != nil {
		return
	}
	own := map[string]bool{}
	if a.mailSync != nil {
		own = a.mailSync.ownAddresses()
	}
	for i := range tasks {
		draft := tasks[i].Card.DraftContent
		if draft.Channel != "" && draft.Channel != "mail" {
			continue
		}
		key := mailThreadKey(draft.Subject)
		var inbound *storage.MailMessageRow
		for j := range rows {
			row := rows[j]
			if row.Mailbox == storage.MailboxSent || !mobileDraftSameThread(row, key) || row.PrincipalID != draft.PrincipalID {
				continue
			}
			if inbound == nil || row.At > inbound.At {
				inbound = &rows[j]
			}
		}
		if inbound == nil {
			continue
		}
		tasks[i].Inbound = inbound
		for _, addr := range strings.Split(inbound.To+"\n"+inbound.CC, "\n") {
			addr = strings.TrimSpace(addr)
			if addr == "" || own[addr] || addr == inbound.SenderEmail || len(own) == 0 {
				continue
			}
			tasks[i].Others = append(tasks[i].Others, addr)
		}
		if tasks[i].Sent != nil {
			continue
		}
		for _, row := range rows {
			if row.Mailbox != storage.MailboxSent || !mobileDraftSameThread(row, key) || row.At <= inbound.At {
				continue
			}
			for _, addr := range strings.Split(row.To+"\n"+row.CC, "\n") {
				if strings.TrimSpace(addr) == inbound.SenderEmail && (tasks[i].RepliedAt == 0 || row.At < tasks[i].RepliedAt) {
					tasks[i].RepliedAt = row.At
				}
			}
		}
	}
}

func mobileDraftSameThread(row storage.MailMessageRow, key string) bool {
	return row.ThreadKey == key || mailThreadKey(row.Subject) == key
}

func (t mobileDraftTask) person(a *App) string {
	if store := a.storeForMobile(); store != nil && t.Card.DraftContent.PrincipalID != "" {
		if principal, err := store.GetPrincipal(context.Background(), t.Card.DraftContent.PrincipalID); err == nil {
			if name := strings.TrimSpace(principal.DisplayName); name != "" {
				return strings.Fields(name)[0]
			}
		}
	}
	if t.Inbound != nil && strings.TrimSpace(t.Inbound.SenderName) != "" {
		return strings.Fields(t.Inbound.SenderName)[0]
	}
	return mobileOfferText(t.Card.DraftContent.To, 80)
}

func (t mobileDraftTask) done() bool { return t.Sent != nil || t.RepliedAt > 0 }

func (t mobileDraftTask) view(a *App) MobileTaskView {
	at := time.UnixMilli(t.At).UTC().Format(time.RFC3339)
	view := MobileTaskView{
		ID: t.TaskID, Title: mobileOfferText(t.Card.Headline, mobileTaskTitleMax),
		Status: "draft_ready", Group: mobileTaskGroupNeedsYou, CreatedAt: at, UpdatedAt: at, Unread: true,
	}
	switch t.Card.DraftContent.Channel {
	case "", "mail":
		view.Source = "mail"
	case "slack":
		view.Source = "slack"
	}
	switch {
	case t.Sent != nil:
		view.Status, view.Group = string(storageCompleted), mobileTaskGroupDone
		view.UpdatedAt = mobileTaskTime(t.Sent.SentAt)
		view.Outcome = &MobileTaskOutcome{Kind: "replied", By: "isoform", At: view.UpdatedAt,
			Params: map[string]string{"person": t.person(a)}}
		view.Unread = false
	case t.RepliedAt > 0:
		view.Status, view.Group = string(storageCompleted), mobileTaskGroupDone
		view.UpdatedAt = mobileTaskTime(t.RepliedAt)
		view.Outcome = &MobileTaskOutcome{Kind: "you_did", By: "you", At: view.UpdatedAt,
			Params: map[string]string{"app": "Mail"}}
		view.Unread = false
	}
	return view
}

const storageCompleted = "completed"

func (t mobileDraftTask) detail(a *App) MobileTaskDetail {
	view := t.view(a)
	lang := a.currentUILanguage()
	posted := mobileTaskTime(t.At)
	events := []MobileTaskEvent{}
	if body := mobileTaskThreadText(t.Card.Body, mobileTaskThreadTextMax); body != "" {
		events = append(events, MobileTaskEvent{ID: "card", Kind: "isoform", At: posted, Text: body})
	}
	events = append(events, MobileTaskEvent{ID: "ready", Kind: "isoform", At: posted, Text: mobileDraftCopy(lang, "ready", "")})
	draft := t.Card.DraftContent
	card := &MobileTaskDraft{
		Channel: firstNonEmpty(draft.Channel, "mail"), To: mobileOfferText(draft.To, 200),
		Subject: mobileOfferText(draft.Subject, 300), Body: mobileTaskThreadText(draft.Text, mobileTaskThreadTextMax), Revision: mainChatDraftRevision(*draft),
		State:      "ready",
		Recipients: &MobileTaskRecipients{To: []string{t.person(a)}, NotIncluded: t.Others},
	}
	if t.Inbound != nil {
		text := mobileDraftCopy("en", "why", t.person(a))
		card.Why = &mobileOfferEvidence{
			Text: text, TextLocalized: map[string]string{"en": text, "zh-CN": mobileDraftCopy("zh-CN", "why", t.person(a))},
			Source: "mail", At: mobileTaskTime(t.Inbound.At),
		}
	}
	actions := []string{}
	switch {
	case t.Sent != nil:
		card.State, card.SentAt = "sent", mobileTaskTime(t.Sent.SentAt)
		if t.Sent.Text != "" {
			card.Body = mobileTaskThreadText(t.Sent.Text, mobileTaskThreadTextMax)
		}
	case t.RepliedAt > 0:
		card.State = "not_sent"
		events = append(events, MobileTaskEvent{ID: "replied", Kind: "isoform", At: mobileTaskTime(t.RepliedAt),
			Text: mobileDraftCopy(lang, "replied", t.person(a))})
	default:
		for _, action := range t.Card.ActionIDs {
			switch action {
			case mainChatSendAction:
				actions = append(actions, "send")
			case "not_now":
				actions = append(actions, mobileTaskActionNotNow)
			}
		}
	}
	return MobileTaskDetail{MobileTaskView: view, Steps: []MobileTaskStep{}, Events: events, Actions: actions, Draft: card}
}

// mobileDraftCopy is the Mac's own words in a draft thread, in the UI language.
func mobileDraftCopy(lang, key, person string) string {
	zh := lang == "zh-CN"
	switch key {
	case "ready":
		if zh {
			return "回复写好了。你点“发送”之前什么都不会发出。"
		}
		return "Here's the reply. Nothing goes until you tap Send."
	case "why":
		if zh {
			return person + " 在等你的回复。"
		}
		return person + " is waiting on your reply."
	case "replied":
		if zh {
			return "你已经自己从邮件回复了 " + person + "，所以我把这件事关了。"
		}
		return "You replied to " + person + " yourself from Mail, so I closed this."
	}
	return ""
}

func (a *App) mobileDraftTask(ctx context.Context, id string) (mobileDraftTask, bool, error) {
	tasks, err := a.mobileDraftTasks(ctx, mobileGoalsNow())
	if err != nil {
		return mobileDraftTask{}, false, err
	}
	for _, task := range tasks {
		if task.TaskID == id {
			return task, true, nil
		}
	}
	return mobileDraftTask{}, false, nil
}

func (bridge *mobileBridge) serveDraftTaskDetail(w http.ResponseWriter, id string) {
	task, found, err := bridge.app.mobileDraftTask(bridge.ctx, id)
	if err != nil {
		mobileJSON(w, http.StatusServiceUnavailable, nil, errMobileStoreUnavailable.Error())
		return
	}
	if !found {
		mobileJSON(w, http.StatusNotFound, nil, "this task is no longer on your computer")
		return
	}
	mobileJSON(w, http.StatusOK, task.detail(bridge.app), "")
}

var errMobileDraftAnswered = errors.New("you already replied to this message")

// serveDraftTaskAction sends the draft (or the person's edit of it) or sets it
// aside, through the same action route and receipts as the Chat card. Before
// sending it checks the thread again: when the person already replied from
// Mail, nothing is sent and the task closes as theirs.
func (bridge *mobileBridge) performDraftTaskAction(w http.ResponseWriter, device mobileDeviceRecord, id, action, text, revision string, channel engagement.Channel) {
	app := bridge.app
	task, found, err := app.mobileDraftTask(bridge.ctx, id)
	if err != nil {
		mobileJSON(w, http.StatusServiceUnavailable, nil, errMobileStoreUnavailable.Error())
		return
	}
	if !found {
		mobileJSON(w, http.StatusNotFound, nil, "this task is no longer on your computer")
		return
	}
	if task.done() {
		// Already sent, or answered by the person: show where it stands.
		mobileJSON(w, http.StatusOK, task.detail(app), "")
		return
	}
	switch action {
	case "send":
		if !mobileHasScope(device.Scopes, mobileScopeChatWrite) {
			mobileJSON(w, http.StatusForbidden, nil, "this phone does not have the "+mobileScopeChatWrite+" permission; pair it again to grant it")
			return
		}
		if _, err := app.resolveMainChatProactiveActionTextRevision(task.Card.MomentID, mainChatSendAction, channel, task.ChatID, nil, text, revision); err != nil {
			if errors.Is(err, errMainChatSendUnknown) {
				mobileJSON(w, http.StatusServiceUnavailable, nil, "Mail did not say whether it sent; check Mail › Sent before trying again")
				return
			}
			mobileJSON(w, http.StatusConflict, nil, err.Error())
			return
		}
	case mobileTaskActionNotNow:
		if _, err := app.resolveMainChatProactiveAction(task.Card.MomentID, "not_now", channel, task.ChatID, nil); err != nil {
			mobileJSON(w, http.StatusConflict, nil, err.Error())
			return
		}
	default:
		mobileJSON(w, http.StatusBadRequest, nil, "this action is not available on a draft")
		return
	}
	app.emitBrowserEvent("handoff:updated", map[string]any{"id": id})
	updated, found, err := app.mobileDraftTask(bridge.ctx, id)
	if err != nil || !found {
		// A set-aside draft leaves the list; answer with what was acted on.
		mobileJSON(w, http.StatusOK, task.detail(app), "")
		return
	}
	mobileJSON(w, http.StatusOK, updated.detail(app), "")
}
