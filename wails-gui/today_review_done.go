package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"

	"time"
	"wails-gui/internal/handoff"
	"wails-gui/internal/openanswer"
	"wails-gui/internal/pii"
	"wails-gui/storage"
)

// TodayDone carries the identity of a finished result, not its stored source
// payload. Its display sentence is never telemetry.
type TodayDone struct {
	ID                string            `json:"id"`
	Kind              string            `json:"kind"`
	Sentence          string            `json:"sentence"`
	SentenceLocalized map[string]string `json:"sentenceLocalized,omitempty"`
	At                int64             `json:"at"`
	TargetKind        string            `json:"targetKind"`
	TargetID          string            `json:"targetId"`
	RunID             string            `json:"runId,omitempty"`
	ChatID            string            `json:"chatId,omitempty"`
	momentID          string
}

type TodayDonePage struct {
	Items []TodayDone `json:"items"`
	Total int         `json:"total"`
}

type todayDoneOpened struct {
	At        int64  `json:"at"`
	BriefID   string `json:"briefId"`
	BriefAsOf string `json:"briefAsOf"`
}

type todayDoneBrief struct{ ID, AsOf string }

// Only completed outcomes qualify. An opened result remains until a later
// brief replaces the one current when it was opened; a 24-hour cutoff also
// keeps old or future records out.
func selectTodayDone(candidates []TodayDone, opened map[string]todayDoneOpened, current todayDoneBrief, now time.Time, limit int) TodayDonePage {
	out := TodayDonePage{Items: []TodayDone{}}
	seen := map[string]bool{}
	for _, item := range candidates {
		switch item.Kind {
		case "work_finished", "playbook_result", "answer_ready", "lookup":
		default:
			continue
		}
		if item.ID == "" || item.Sentence == "" || item.TargetID == "" || item.At < now.Add(-24*time.Hour).UnixMilli() || item.At > now.UnixMilli() || seen[item.ID] {
			continue
		}
		if state, ok := opened[todayDoneKey(item.ID)]; ok && state.At > 0 && (state.BriefID != current.ID || state.BriefAsOf != current.AsOf) {
			continue
		}
		seen[item.ID] = true
		out.Items = append(out.Items, item)
	}
	sort.SliceStable(out.Items, func(i, j int) bool { return out.Items[i].At > out.Items[j].At })
	out.Total = len(out.Items)
	if limit > 0 && len(out.Items) > limit {
		out.Items = out.Items[:limit]
	}
	return out
}

const todayDoneOpenedKey = "today.done.opened.v1"

var todayDoneOpenedMu sync.Mutex

func todayDoneKey(id string) string {
	hash := sha256.Sum256([]byte(id))
	return hex.EncodeToString(hash[:])
}

func (a *App) currentTodayDoneBrief() todayDoneBrief {
	dir, err := mobileBriefStateDir()
	if err != nil {
		return todayDoneBrief{}
	}
	brief, ok := newMobileBriefStore(dir).latest()
	if !ok {
		return todayDoneBrief{}
	}
	return todayDoneBrief{ID: brief.BriefID, AsOf: brief.AsOf}
}

func (a *App) readTodayDoneOpened(ctx context.Context) (map[string]todayDoneOpened, error) {
	raw, err := a.store.GetUIState(ctx, todayDoneOpenedKey)
	if err != nil {
		return nil, err
	}
	state := map[string]todayDoneOpened{}
	if raw != "" && json.Unmarshal([]byte(raw), &state) != nil {
		return nil, errors.New("invalid opened results state")
	}
	if state == nil {
		state = map[string]todayDoneOpened{}
	}
	return state, nil
}

// Status entries have independent identities even when the chat groups them.
func todayDoneFromStatus(status mainChatStatus, chatID string) []TodayDone {
	var out []TodayDone
	for _, entry := range status.Entries {
		switch entry.Kind {
		case "work_finished", "playbook_result":
		default:
			continue
		}
		// A grouped row moves when a later status is appended. Its timestamp
		// cannot establish when an older result actually finished.
		if entry.ID == "" || entry.Target.ID == "" || entry.SentAt <= 0 {
			continue
		}
		out = append(out, TodayDone{ID: "status:" + entry.ID, Kind: entry.Kind, Sentence: strings.TrimSpace(entry.Text), SentenceLocalized: entry.Localized, At: entry.SentAt, TargetKind: entry.Target.Kind, TargetID: entry.Target.ID, RunID: entry.Target.RunID, ChatID: chatID, momentID: entry.MomentID})
	}
	return out
}

// Chat records an opened receipt only when the exact card or expanded status
// is in view. That local receipt also applies when it was opened outside Today.
func (a *App) todayDoneSeenBeforeBrief(ctx context.Context, momentID string, finishedAt int64, briefAt time.Time) (bool, error) {
	if momentID == "" || finishedAt <= 0 || briefAt.IsZero() || briefAt.UnixMilli() <= finishedAt {
		return false, nil
	}
	id, err := a.mainChatReceiptMomentID(ctx, momentID)
	if err != nil {
		return false, err
	}
	receipts, err := a.store.ListEngagementReceipts(ctx, id)
	if err != nil {
		return false, err
	}
	for _, receipt := range receipts {
		switch receipt.State {
		case storage.EngagementReceiptOpened, storage.EngagementReceiptActed:
			if receipt.At >= finishedAt && receipt.At < briefAt.UnixMilli() {
				return true, nil
			}
		}
	}
	return false, nil
}

// Finished task threads are authoritative for both their outcome and their
// opened receipt, including openings outside Today. Never turn a user-marked
// completion into work attributed to Isoform.
func todayDoneFromHandoffs(entries []handoff.Entry, subjects []string, briefAt time.Time) ([]TodayDone, map[string]bool) {
	items := []TodayDone{}
	authoritative := map[string]bool{}
	for _, entry := range entries {
		if entry.Status != handoff.StatusCompleted || entry.Thread == nil || entry.Thread.Outcome == nil {
			continue
		}
		outcome := entry.Thread.Outcome
		authoritative[entry.ID] = true
		if outcome.Kind != handoff.OutcomeCompleted || outcome.By != handoff.OutcomeByIsoform {
			continue
		}
		if _, visible := mobileTaskDetail(entry, subjects); !visible || pii.IsSensitiveAppName(entry.AppName) {
			continue
		}
		sentence := mobileOfferText(outcome.Params["summary"], mobileTaskSummaryMax)
		if sentence == "" || mobileTaskEchoesMail(sentence, subjects) {
			continue
		}
		at := outcome.At
		if at <= 0 {
			at = entry.UpdatedAt
		}
		if entry.Thread.OpenedAt >= at && at > 0 && !briefAt.IsZero() && briefAt.UnixMilli() > entry.Thread.OpenedAt {
			continue
		}
		items = append(items, TodayDone{ID: "handoff:" + entry.ID, Kind: "work_finished", Sentence: sentence, At: at, TargetKind: "handoff", TargetID: entry.ID})
	}
	return items, authoritative
}

// todayDoneCandidates reads the same privacy-projected main-chat records as
// the rest of the app. Status rows can group several distinct finished items.
func (a *App) todayDoneCandidates(ctx context.Context, now time.Time) ([]TodayDone, error) {
	current := a.currentTodayDoneBrief()
	briefAt := mobileBriefParseTime(current.AsOf)
	handoffItems := []TodayDone{}
	authoritative := map[string]bool{}
	if a.handoffStore != nil {
		entries, err := a.handoffStore.List()
		if err != nil {
			return nil, err
		}
		subjects, err := mobileGoalSubjects(a)
		if err != nil {
			return nil, err
		}
		handoffItems, authoritative = todayDoneFromHandoffs(entries, subjects, briefAt)
	}
	snapshot, err := a.store.ReadMainChatView(ctx, now.Add(-48*time.Hour).UnixMilli())
	if err != nil {
		return nil, err
	}
	projected := a.projectMainChatRows(ctx, snapshot.Items)
	out := handoffItems
	for i, row := range snapshot.Items {
		item := projected[i]
		switch item.ToolName {
		case mainChatStatusTool:
			var status mainChatStatus
			if json.Unmarshal([]byte(item.ToolInput), &status) != nil {
				continue
			}
			for _, item := range todayDoneFromStatus(status, snapshot.SessionID) {
				if item.TargetKind != "handoff" || !authoritative[item.TargetID] {
					out = append(out, item)
				}
			}

		case mainChatProactiveTool:
			var card mainChatProactive
			if json.Unmarshal([]byte(item.ToolInput), &card) != nil {
				continue
			}
			cards := []mainChatProactive{card}
			if card.Kind == "digest" {
				cards = card.Items
			}
			for _, child := range cards {
				if child.Kind != "work_finished" || child.MomentID == "" {
					continue
				}
				if child.Work != nil && authoritative[child.Work.HandoffID] || child.Target.Kind == mainChatTargetHandoff && authoritative[child.Target.ID] {
					continue
				}
				sentence := strings.TrimSpace(child.Body)
				if sentence == "" {
					sentence = strings.TrimSpace(child.Headline)
				}
				out = append(out, TodayDone{ID: "card:" + child.MomentID, Kind: "work_finished", Sentence: sentence, SentenceLocalized: todayProactiveSentences(child), At: row.CreatedAt, TargetKind: "chat_item", TargetID: child.MomentID, ChatID: snapshot.SessionID, momentID: child.MomentID})
			}
		}
	}
	visible := out[:0]
	for _, item := range out {
		seen, err := a.todayDoneSeenBeforeBrief(ctx, item.momentID, item.At, briefAt)
		if err != nil {
			return nil, err
		}
		if !seen {
			visible = append(visible, item)
		}
	}
	out = visible
	// Answer posts can live in any chat, not only the main chat. Reuse the
	// brief's current text and source gates before showing their first line.
	posts, err := a.store.OpenAnswerBriefPosts(ctx, now.Add(-24*time.Hour), now)
	if err != nil {
		return nil, err
	}
	for _, post := range posts {
		if post.OpenedOrActed && (post.OpenedAt <= 0 || !briefAt.IsZero() && briefAt.UnixMilli() > post.OpenedAt) {
			continue
		}
		candidate := openanswer.Candidate{Question: post.Question, Topic: post.Topic, Source: post.Source}
		if !a.openAnswerBriefPostAllowed(ctx, candidate, post.Content) {
			continue
		}
		row, err := a.store.LoadStreamItem(ctx, post.MessageID)
		if err != nil {
			return nil, err
		}
		if row == nil || row.SessionLocalID == "" {
			continue
		}
		line := strings.TrimSpace(strings.SplitN(post.Content, "\n", 2)[0])
		line = pii.DisplayModelText(line, a.modelDisplayLanguage())
		out = append(out, TodayDone{ID: "answer:" + post.MessageID, Kind: "answer_ready", Sentence: line, At: post.CreatedAt, TargetKind: "chat_item", TargetID: post.MessageID, ChatID: row.SessionLocalID})
	}
	return out, ctx.Err()
}

func (a *App) GetDoneForYou(limit int) (TodayDonePage, error) {
	return a.getDoneForYou(context.Background(), limit, time.Now())
}

func (a *App) getDoneForYou(ctx context.Context, limit int, now time.Time) (TodayDonePage, error) {
	if a == nil || a.store == nil {
		return TodayDonePage{Items: []TodayDone{}}, nil
	}
	candidates, err := a.todayDoneCandidates(ctx, now)
	if err != nil {
		return TodayDonePage{}, err
	}
	todayDoneOpenedMu.Lock()
	defer todayDoneOpenedMu.Unlock()
	opened, err := a.readTodayDoneOpened(ctx)
	if err != nil {
		return TodayDonePage{}, err
	}
	return selectTodayDone(candidates, opened, a.currentTodayDoneBrief(), now, limit), nil
}

// MarkTodayDoneOpened records only a hash and brief identity. The result is
// rechecked first; arbitrary IDs cannot create durable state.
func (a *App) MarkTodayDoneOpened(id string) error {
	if a == nil || a.store == nil || id == "" {
		return errors.New("finished result unavailable")
	}
	ctx := context.Background()
	now := time.Now()
	candidates, err := a.todayDoneCandidates(ctx, now)
	if err != nil {
		return err
	}
	todayDoneOpenedMu.Lock()
	defer todayDoneOpenedMu.Unlock()
	opened, err := a.readTodayDoneOpened(ctx)
	if err != nil {
		return err
	}
	brief := a.currentTodayDoneBrief()
	found := false
	for _, item := range selectTodayDone(candidates, opened, brief, now, 0).Items {
		if item.ID == id {
			found = true
			break
		}
	}
	if !found {
		return errors.New("finished result unavailable")
	}
	for key, state := range opened {
		if state.At < now.Add(-24*time.Hour).UnixMilli() {
			delete(opened, key)
		}
	}
	opened[todayDoneKey(id)] = todayDoneOpened{At: now.UnixMilli(), BriefID: brief.ID, BriefAsOf: brief.AsOf}
	raw, err := json.Marshal(opened)
	if err != nil {
		return err
	}
	if err := a.store.SetUIState(ctx, todayDoneOpenedKey, string(raw)); err != nil {
		return err
	}
	a.emitBrowserEvent("today:done-changed")
	return nil
}

// TodayDoneAnswer is a revalidated answer from its exact chat message. The
// list carries only a short sentence; opening reads the current full message.
type TodayDoneAnswer struct {
	ID      string `json:"id"`
	ChatID  string `json:"chatId"`
	Content string `json:"content"`
}

func (a *App) ReadTodayDoneAnswer(id string) (TodayDoneAnswer, error) {
	var empty TodayDoneAnswer
	if a == nil || a.store == nil || !strings.HasPrefix(id, "answer:") {
		return empty, errors.New("finished answer unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Prevent a pause or sensitive-app change from racing the source proof.
	if err := lockTodayChatPublication(ctx, a.closedEyes.mu.TryLock); err != nil {
		return empty, err
	}
	defer a.closedEyes.mu.Unlock()
	if err := lockTodayChatPublication(ctx, pii.TryLockSensitiveAppsForPublication); err != nil {
		return empty, err
	}
	defer pii.UnlockSensitiveAppsForPublication()
	revision, err := a.store.ReadRevision(ctx)
	if err != nil {
		return empty, err
	}
	page, err := a.getDoneForYou(ctx, 0, time.Now())
	if err != nil {
		return empty, err
	}
	for _, item := range page.Items {
		if item.ID != id || item.Kind != "answer_ready" {
			continue
		}
		row, err := a.store.LoadStreamItem(ctx, item.TargetID)
		if err != nil || row == nil || row.SessionLocalID != item.ChatID {
			return empty, errors.New("finished answer unavailable")
		}
		content := pii.DisplayModelText(row.Content, a.modelDisplayLanguage())
		release, err := a.store.LockReadRevision(ctx, revision)
		if err != nil {
			return empty, err
		}
		defer release()
		return TodayDoneAnswer{ID: item.ID, ChatID: item.ChatID, Content: content}, nil
	}
	return empty, errors.New("finished answer unavailable")
}
