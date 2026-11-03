package main

import (
	"strings"
	"testing"
	"time"
)

// Card copy follows the computer's clock; tests pin it to 12-hour so they
// read the same on every Mac, and switch it explicitly where it matters.
func init() { computerUses24Hour = func() bool { return false } }

func TestComputerClockFollowsTheComputersSetting(t *testing.T) {
	saved := computerUses24Hour
	t.Cleanup(func() { computerUses24Hour = saved })
	at := time.Date(2026, 10, 30, 16, 44, 0, 0, time.Local)
	computerUses24Hour = func() bool { return false }
	if got := computerClock(at, "en"); got != "4:44 PM" {
		t.Fatalf("12-hour English clock = %q", got)
	}
	if got := computerClock(at, "zh-CN"); got != "下午4:44" {
		t.Fatalf("12-hour Chinese clock = %q", got)
	}
	if got := (&App{}).GetClockFormat(); got != "12h" {
		t.Fatalf("clock format = %q", got)
	}
	computerUses24Hour = func() bool { return true }
	if got := computerClock(at, "en"); got != "16:44" {
		t.Fatalf("24-hour clock = %q", got)
	}
	if got := (&App{}).GetClockFormat(); got != "24h" {
		t.Fatalf("clock format = %q", got)
	}
	start := time.Date(2026, 10, 30, 7, 5, 0, 0, time.Local).Format(time.RFC3339)
	computerUses24Hour = func() bool { return false }
	item := mainChatProactive{Kind: "meeting_prep", Localized: map[string]mainChatProactiveCopy{"en": {}}}
	populateMainChatEventCopy(&item, map[string]string{"event_title": "Standup", "event_start": start})
	if got := item.Localized["en"].Headline; got != "Standup starts at 7:05 AM." {
		t.Fatalf("meeting card clock = %q, want the computer's 12-hour clock", got)
	}
}

func TestMainChatMeetingCardFoldsOnceItStarts(t *testing.T) {
	now := time.Date(2026, 10, 30, 14, 0, 0, 0, time.Local)
	card := mainChatProactive{Kind: "meeting_prep", State: "pending", Event: &mainChatProactiveEvent{Title: "Design review", Start: now.Add(10 * time.Minute).Format(time.RFC3339)}}
	if got := mainChatProactiveRowClass(card, now); got != MainChatRowWaiting {
		t.Fatalf("upcoming meeting row = %q, want waiting", got)
	}
	if got := mainChatProactiveRowClass(card, now.Add(10*time.Minute)); got != MainChatRowRoutine {
		t.Fatalf("started meeting row = %q, want routine (folded into updates)", got)
	}
}

func TestMainChatLeftOffCardFoldsAfterTheNextMorningBrief(t *testing.T) {
	leftAt := time.Date(2026, 10, 29, 22, 58, 0, 0, time.Local)
	card := mainChatProactive{Kind: "decision_due", Target: mainChatTarget{Kind: mainChatTargetThread, ID: "thread-1"},
		Place: &mainChatProactivePlace{App: "Isoform", At: leftAt.Format(time.RFC3339)}}
	if mainChatLeftOffFolded(card, leftAt.UnixMilli(), leftAt.Add(time.Hour)) {
		t.Fatal("a brief on the same day folded the left-off card")
	}
	if !mainChatLeftOffFolded(card, leftAt.UnixMilli(), time.Date(2026, 10, 30, 7, 0, 0, 0, time.Local)) {
		t.Fatal("the next morning brief did not fold the left-off card")
	}
	if mainChatLeftOffFolded(card, leftAt.UnixMilli(), time.Time{}) {
		t.Fatal("folded without a brief")
	}
	other := card
	other.Kind = "waiting_on_reply"
	if mainChatLeftOffFolded(other, leftAt.UnixMilli(), time.Date(2026, 10, 30, 7, 0, 0, 0, time.Local)) {
		t.Fatal("folded a card that is not a left-off card")
	}
}

func TestMainChatMeetingWithoutTitleHasNoSentence(t *testing.T) {
	item := mainChatProactive{Kind: "meeting_prep", Localized: map[string]mainChatProactiveCopy{"en": {Headline: "A meeting is about to start."}, "zh-CN": {}}}
	populateMainChatEventCopy(&item, map[string]string{"event_title": "", "event_start": "2026-10-26T14:05:00Z", "event_private": "true"})
	if got := item.Localized["en"].Headline; strings.Contains(got, "starts at") {
		t.Fatalf("untitled meeting got a meeting sentence: %q", got)
	}
}

func TestMainChatCopyParamsWordTheClockInTheComputersFormat(t *testing.T) {
	saved := computerUses24Hour
	t.Cleanup(func() { computerUses24Hour = saved })
	params := map[string]string{"slot": "morning", "time": "07:00"}
	if got := mainChatCopyParams(params, "en")["time"]; got != "7:00 AM" {
		t.Fatalf("12-hour English time = %q", got)
	}
	if got := mainChatCopyParams(params, "zh-CN")["time"]; got != "上午7:00" {
		t.Fatalf("12-hour Chinese time = %q", got)
	}
	computerUses24Hour = func() bool { return true }
	if got := mainChatCopyParams(params, "en")["time"]; got != "07:00" {
		t.Fatalf("24-hour time = %q", got)
	}
	if params["time"] != "07:00" {
		t.Fatalf("stored params changed: %q", params["time"])
	}
}
