package main

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/handoff"

	"wails-gui/storage"
)

func TestTodayDoneTaskOutcomeTracksOpeningsOutsideToday(t *testing.T) {
	now := time.Now()
	finished := now.Add(-2 * time.Hour)
	opened := now.Add(-time.Hour)
	entry := handoff.Entry{ID: "finished-task", Title: "Completed task", Status: handoff.StatusCompleted,
		UpdatedAt: finished.UnixMilli(), Thread: &handoff.Thread{OpenedAt: opened.UnixMilli(), Outcome: &handoff.Outcome{
			Kind: handoff.OutcomeCompleted, By: handoff.OutcomeByIsoform, At: finished.UnixMilli(), Params: map[string]string{"summary": "The report is ready."},
		}}}
	items, authoritative := todayDoneFromHandoffs([]handoff.Entry{entry}, nil, now.Add(-90*time.Minute))
	if len(items) != 1 || items[0].TargetID != entry.ID || items[0].Sentence != "The report is ready." || !authoritative[entry.ID] {
		t.Fatalf("finished task = %+v, authoritative = %+v", items, authoritative)
	}
	items, authoritative = todayDoneFromHandoffs([]handoff.Entry{entry}, nil, now)
	if len(items) != 0 || !authoritative[entry.ID] {
		t.Fatalf("opened task survived the next brief: %+v", items)
	}
	entry.Thread.Outcome.By = handoff.OutcomeByYou
	items, authoritative = todayDoneFromHandoffs([]handoff.Entry{entry}, nil, time.Time{})
	if len(items) != 0 || !authoritative[entry.ID] {
		t.Fatal("user-completed task attributed to Isoform")
	}
	entry.Thread.Outcome.By = handoff.OutcomeByIsoform
	items, authoritative = todayDoneFromHandoffs([]handoff.Entry{entry}, []string{"the report is ready"}, time.Time{})
	if len(items) != 0 || !authoritative[entry.ID] {
		t.Fatal("protected mail subject was exposed through a task outcome")
	}
}

func TestDoneForYouReadsTaskAndDropsItAfterAnExternalOpenAndBrief(t *testing.T) {
	rig := newHandoffTestRig(t)
	now := time.Now()
	entry := handoff.Entry{ID: "finished-task", Title: "Completed task", Status: handoff.StatusCompleted,
		Thread: &handoff.Thread{Outcome: &handoff.Outcome{Kind: handoff.OutcomeCompleted, By: handoff.OutcomeByIsoform,
			At: now.Add(-time.Hour).UnixMilli(), Params: map[string]string{"summary": "The report is ready."}}}}
	if _, err := rig.handoffs.Save(entry); err != nil {
		t.Fatal(err)
	}
	page, err := rig.app.GetDoneForYou(0)
	if err != nil || page.Total != 1 || page.Items[0].TargetID != entry.ID {
		t.Fatalf("finished task page = %+v, %v", page, err)
	}
	opened := now.UnixMilli()
	if _, err := rig.handoffs.MutateThread(entry.ID, func(thread *handoff.Thread) { thread.OpenedAt = opened }); err != nil {
		t.Fatal(err)
	}
	page, err = rig.app.GetDoneForYou(0)
	if err != nil || page.Total != 1 {
		t.Fatalf("opened task left before a brief: %+v, %v", page, err)
	}
	dir, err := mobileBriefStateDir()
	if err != nil {
		t.Fatal(err)
	}
	brief := MobileBriefV1{BriefID: now.Format("2006-01-02") + "-morning", AsOf: formatMobileBriefTime(now.Add(time.Minute)), Slot: mobileBriefSlotMorning, Timezone: "UTC"}
	if err := newMobileBriefStore(dir).save(brief, now); err != nil {
		t.Fatal(err)
	}
	page, err = rig.app.GetDoneForYou(0)
	if err != nil || page.Total != 0 {
		t.Fatalf("opened task remained after the brief: %+v, %v", page, err)
	}
}

func TestTodayDoneRespectsChatOpenedReceiptAfterNextBrief(t *testing.T) {
	rig := newHandoffTestRig(t)
	ctx := context.Background()
	now := time.Now()
	finished := now.Add(-2 * time.Hour)
	opened := now.Add(-time.Hour)
	_, err := rig.state.CreateEngagementMoment(ctx, storage.EngagementMomentRow{ID: "finished-moment", Kind: "work_finished", DedupeKey: "finished-moment", CreatedAt: finished.UnixMilli()})
	if err != nil {
		t.Fatal(err)
	}
	if err := rig.state.RecordEngagementReceipt(ctx, storage.EngagementReceiptRow{MomentID: "finished-moment", Channel: "desktop", State: storage.EngagementReceiptOpened, At: opened.UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	seen, err := rig.app.todayDoneSeenBeforeBrief(ctx, "finished-moment", finished.UnixMilli(), now.Add(-90*time.Minute))
	if err != nil || seen {
		t.Fatalf("opened result left before the next brief: %v, %v", seen, err)
	}
	seen, err = rig.app.todayDoneSeenBeforeBrief(ctx, "finished-moment", finished.UnixMilli(), now)
	if err != nil || !seen {
		t.Fatalf("opened result remained after the next brief: %v, %v", seen, err)
	}
	seen, err = rig.app.todayDoneSeenBeforeBrief(ctx, "finished-moment", now.UnixMilli(), now.Add(time.Minute))
	if err != nil || seen {
		t.Fatalf("an earlier opening was applied to later work: %v, %v", seen, err)
	}
}

func TestTodayDoneFiltersSortsAndKeepsOpenedUntilNextBrief(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	mk := func(id, kind string, ago time.Duration) TodayDone {
		return TodayDone{ID: id, Kind: kind, Sentence: id, TargetID: "target-" + id, At: now.Add(-ago).UnixMilli()}
	}
	items := []TodayDone{mk("old", "lookup", 25*time.Hour), mk("draft", "waiting_on_reply", time.Hour), mk("learned", "learned", time.Hour), mk("finished", "work_finished", 2*time.Hour), mk("answer", "answer_ready", time.Hour), mk("routine", "playbook_result", 3*time.Hour), mk("future", "lookup", -time.Hour)}
	opened := map[string]todayDoneOpened{todayDoneKey("answer"): {At: now.Add(-time.Minute).UnixMilli(), BriefID: "morning", BriefAsOf: "a"}}
	before := selectTodayDone(items, opened, todayDoneBrief{ID: "morning", AsOf: "a"}, now, 2)
	if before.Total != 3 || len(before.Items) != 2 || !reflect.DeepEqual([]string{before.Items[0].ID, before.Items[1].ID}, []string{"answer", "finished"}) || before.Items[0].TargetID != "target-answer" {
		t.Fatalf("same brief = %+v", before)
	}
	after := selectTodayDone(items, opened, todayDoneBrief{ID: "evening", AsOf: "b"}, now, 0)
	if after.Total != 2 || len(after.Items) != 2 || after.Items[0].ID != "finished" || after.Items[1].ID != "routine" {
		t.Fatalf("next brief = %+v", after)
	}
}

func TestDoneForYouReadsFinishedResultsAndPersistsOpenedWithoutContent(t *testing.T) {
	app, chatID, _ := todaySendTestApp(t)
	ctx := context.Background()
	now := time.Now()
	if _, err := app.store.AppendStreamItem(ctx, chatID, storage.StreamItemRow{ID: "promise", Kind: "assistant", Content: "I will check."}); err != nil {
		t.Fatal(err)
	}
	answer := storage.OpenAnswerRow{ID: "answer-id", ChatSessionID: chatID, AssistantMessageID: "promise", Question: "Is it ready?", Topic: "delivery", Settles: "result", Source: "Web", Delivery: "around", WindowStart: now.UnixMilli(), WindowEnd: now.Add(time.Hour).UnixMilli(), ExpiresAt: now.Add(24 * time.Hour).UnixMilli(), State: "open", CreatedAt: now.UnixMilli()}
	if err := app.store.CreateOpenAnswer(ctx, answer); err != nil {
		t.Fatal(err)
	}
	if err := app.store.CommitOpenAnswerTransition(ctx, answer.ID, "settled", now.UnixMilli(), &storage.StreamItemRow{ID: "result-post", Kind: "assistant", Content: "The answer is ready."}); err != nil {
		t.Fatal(err)
	}
	page, err := app.GetDoneForYou(0)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || page.Items[0].ID != "answer:result-post" || page.Items[0].TargetID != "result-post" {
		t.Fatalf("finished results = %+v", page)
	}
	detail, err := app.ReadTodayDoneAnswer("answer:result-post")
	if err != nil || detail.ChatID != chatID || detail.Content != "The answer is ready." {
		t.Fatalf("answer detail = %+v, %v", detail, err)
	}
	app.closedEyes.enabled.Store(true)
	if _, err := app.ReadTodayDoneAnswer("answer:result-post"); err == nil {
		t.Fatal("paused answer was read without permission")
	}
	app.closedEyes.enabled.Store(false)
	if err := app.store.UpdateStreamItemContent(ctx, "result-post", "[REDACTED:private]"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ReadTodayDoneAnswer("answer:result-post"); err == nil {
		t.Fatal("withdrawn answer remained readable")
	}
	if err := app.store.UpdateStreamItemContent(ctx, "result-post", "The answer is ready."); err != nil {
		t.Fatal(err)
	}

	if err := app.MarkTodayDoneOpened("answer:result-post"); err != nil {
		t.Fatal(err)
	}
	same, err := app.GetDoneForYou(0)
	if err != nil || same.Total != 1 {
		t.Fatalf("same brief = %+v, %v", same, err)
	}
	raw, err := app.store.GetUIState(ctx, todayDoneOpenedKey)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "result-post") || strings.Contains(raw, "The answer is ready") {
		t.Fatal("opened state retained result content")
	}
	if err := app.MarkTodayDoneOpened("unknown-result"); err == nil {
		t.Fatal("accepted an unrecognized result")
	}
	dir, err := mobileBriefStateDir()
	if err != nil {
		t.Fatal(err)
	}
	brief := MobileBriefV1{BriefID: now.Format("2006-01-02") + "-morning", AsOf: formatMobileBriefTime(now.Add(time.Second)), Slot: mobileBriefSlotMorning, Timezone: "UTC"}
	if err := newMobileBriefStore(dir).save(brief, now); err != nil {
		t.Fatal(err)
	}
	next, err := app.GetDoneForYou(0)
	if err != nil || next.Total != 0 {
		t.Fatalf("next brief = %+v, %v", next, err)
	}
	if err := app.MarkTodayDoneOpened("answer:result-post"); err == nil {
		t.Fatal("stale opened result was accepted again after the next brief")
	}
	if _, err := app.ReadTodayDoneAnswer("answer:result-post"); err == nil {
		t.Fatal("stale opened answer remained readable from Today")
	}

}

func TestTodayDoneStatusKeepsEachExactTarget(t *testing.T) {
	status := mainChatStatus{Entries: []mainChatStatusEntry{
		{ID: "one", Kind: "work_finished", Text: "Done.", SentAt: 10, Target: mainChatTarget{Kind: "handoff", ID: "work"}},
		{ID: "two", Kind: "playbook_result", Text: "Routine done.", Localized: map[string]string{"zh-CN": "例行工作已完成。"}, SentAt: 20, Target: mainChatTarget{Kind: "automation", ID: "routine", RunID: "run-2"}},
		{ID: "draft", Kind: "waiting_on_reply", Text: "Draft", Target: mainChatTarget{Kind: "handoff", ID: "draft"}},
		{ID: "note", Kind: "learned", Text: "Learned", Target: mainChatTarget{Kind: "knowledge", ID: "note"}},
	}}
	got := todayDoneFromStatus(status, "main")
	if len(got) != 2 || got[0].TargetID != "work" || got[1].TargetID != "routine" || got[1].RunID != "run-2" || got[1].At != 20 || got[1].SentenceLocalized["zh-CN"] != "例行工作已完成。" {
		t.Fatalf("finished status = %+v", got)
	}
}

func TestTodayDoneStatusUsesEachOriginalFinishTime(t *testing.T) {
	app := mainChatTestApp(t)
	first := time.Date(2026, time.October, 30, 12, 0, 0, 0, time.Local)
	for _, item := range []struct {
		id string
		at time.Time
	}{{"old-result", first}, {"new-result", first.Add(time.Minute)}} {
		if app.postMainChatStatus(mainChatStatusEntry{ID: item.id, Kind: "playbook_result", Text: "Done", Target: mainChatTarget{Kind: "automation", ID: "job"}}, item.at) == "" {
			t.Fatal("could not store result")
		}
	}
	entries := mainChatStatusEntries(t, app)
	if len(entries) != 2 || entries[0].SentAt != first.UnixMilli() || entries[1].SentAt != first.Add(time.Minute).UnixMilli() {
		t.Fatalf("status finish times = %+v", entries)
	}
	entries = append(entries, mainChatStatusEntry{ID: "legacy", Kind: "playbook_result", Text: "No date", Target: mainChatTarget{Kind: "automation", ID: "job"}})
	if got := todayDoneFromStatus(mainChatStatus{Entries: entries}, "main"); len(got) != 2 {
		t.Fatalf("undated legacy result was treated as recent: %+v", got)
	}
}
