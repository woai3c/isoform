package main

import (
	"fmt"
	"time"

	"wails-gui/internal/handoff"
)

// An item earns a place under "What needs your attention" only with grounding:
// a due date, a person waiting, Isoform's own work waiting on the user, or a
// recorded state. The tiers order that list (docs/today-page.md); zero means
// the item does not need the user and carries no reason line.
const (
	mobileBriefAttentionOverdue = 1 + iota
	mobileBriefAttentionDueToday
	// A follow-up the user added from a moment in Activity.
	mobileBriefAttentionFollowUp
	mobileBriefAttentionPersonWaiting
	mobileBriefAttentionOwnWork
	mobileBriefAttentionDueSoon
	mobileBriefAttentionUnfinished
	mobileBriefAttentionStalled
)

const (
	// mobileBriefDueSoonDays bounds "due within the week".
	mobileBriefDueSoonDays = 6
	// mobileBriefStalledDays is how long recorded progress on an active
	// Purpose must have stopped before it needs the user.
	mobileBriefStalledDays = 3
)

// mobileBriefCivilDays is the number of calendar days from ref to at in loc,
// so a deadline at 23:30 yesterday is one day back however few hours passed.
func mobileBriefCivilDays(at, ref time.Time, loc *time.Location) int {
	if loc == nil {
		loc = time.Local
	}
	a, r := at.In(loc), ref.In(loc)
	day := func(t time.Time) time.Time { return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC) }
	return int(day(a).Sub(day(r)).Hours() / 24)
}

// mobileBriefSameWeek reports whether at falls in ref's Monday-to-Sunday week.
func mobileBriefSameWeek(at, ref time.Time, loc *time.Location) bool {
	if loc == nil {
		loc = time.Local
	}
	offset := (int(ref.In(loc).Weekday()) + 6) % 7
	days := mobileBriefCivilDays(at, ref, loc)
	return days >= -offset && days < 7-offset
}

// mobileBriefDayWords names a calendar day the way a person would, relative to
// the brief's day: "today", "yesterday", "tomorrow", "Thursday" within this
// week, "last Thursday" in the previous one, and the date from a week away.
func mobileBriefDayWords(at, ref time.Time, loc *time.Location, lang string) string {
	if at.IsZero() || ref.IsZero() {
		return ""
	}
	if loc == nil {
		loc = time.Local
	}
	local := at.In(loc)
	days := mobileBriefCivilDays(at, ref, loc)
	zhWeekday := []string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}[local.Weekday()]
	switch days {
	case 0:
		return pick(lang, "today", "今天")
	case -1:
		return pick(lang, "yesterday", "昨天")
	case 1:
		return pick(lang, "tomorrow", "明天")
	}
	if days >= -mobileBriefDueSoonDays && days <= mobileBriefDueSoonDays {
		if mobileBriefSameWeek(at, ref, loc) {
			return pick(lang, local.Weekday().String(), zhWeekday)
		}
		if days < 0 {
			return pick(lang, "last "+local.Weekday().String(), "上"+zhWeekday)
		}
		return pick(lang, local.Weekday().String(), "下"+zhWeekday)
	}
	if local.Year() != ref.In(loc).Year() {
		return pick(lang, local.Format("January 2, 2006"), fmt.Sprintf("%d年%d月%d日", local.Year(), local.Month(), local.Day()))
	}
	return pick(lang, local.Format("January 2"), fmt.Sprintf("%d月%d日", local.Month(), local.Day()))
}

// mobileBriefCarriedOver reports an item first recorded before the brief's day.
func mobileBriefCarriedOver(since, asOf time.Time, loc *time.Location) bool {
	return !since.IsZero() && !asOf.IsZero() && mobileBriefCivilDays(since, asOf, loc) < 0
}

// mobileBriefSinceWords is the age of a carried-over item ("since last
// Thursday", "从上周四起"), empty for an item first seen today.
func mobileBriefSinceWords(since, asOf time.Time, loc *time.Location, lang string) string {
	if !mobileBriefCarriedOver(since, asOf, loc) {
		return ""
	}
	day := mobileBriefDayWords(since, asOf, loc, lang)
	return pick(lang, "since "+day, "从"+day+"起")
}

// mobileBriefDueWords states a deadline relative to the brief's day: "was due
// yesterday", "is due today", "is due Thursday" (zh: 已于昨天到期, 今天到期).
func mobileBriefDueWords(due, asOf time.Time, loc *time.Location, lang string) string {
	if due.IsZero() {
		return ""
	}
	day := mobileBriefDayWords(due, asOf, loc, lang)
	if mobileBriefCivilDays(due, asOf, loc) < 0 {
		return pick(lang, "was due "+day, "已于"+day+"到期")
	}
	return pick(lang, "is due "+day, day+"到期")
}

// mobileBriefAttention computes the reason line and tier from the grounding
// the candidate carries. It never guesses: a title with nothing behind it,
// or a row whose evidence is gone, returns no reason.
func mobileBriefAttention(item mobileBriefCandidate, asOf time.Time, loc *time.Location, lang string) (string, int) {
	// Only an anchored item can need the user (mobile_brief_anchor.go).
	if item.Kind == "noticed" || item.EvidenceUnavailable || asOf.IsZero() || mobileBriefCandidateAnchor(item) == "" {
		return "", 0
	}
	since := mobileBriefSinceWords(item.OpenedAt, asOf, loc, lang)
	dueDays := mobileBriefCivilDays(item.DueAt, asOf, loc)
	dueDay := mobileBriefDayWords(item.DueAt, asOf, loc, lang)
	if !item.DueAt.IsZero() && dueDays < 0 {
		return pick(lang, "Was due "+dueDay, "已于"+dueDay+"到期"), mobileBriefAttentionOverdue
	}
	if !item.DueAt.IsZero() && dueDays == 0 {
		return pick(lang, "Due today", "今天到期"), mobileBriefAttentionDueToday
	}
	if item.FollowUp && item.SourceKind == "purpose" && (item.Status == PurposeStateActive || item.Status == PurposeStateWaiting) {
		if added := mobileBriefDayWords(item.OpenedAt, asOf, loc, lang); added != "" {
			return pick(lang, "Follow-up you added "+added, "你"+added+"添加的跟进"), mobileBriefAttentionFollowUp
		}
		return pick(lang, "Follow-up you added", "你添加的跟进"), mobileBriefAttentionFollowUp
	}
	if item.Kind == "waiting_on_reply" {
		if since == "" {
			return pick(lang, "Waiting on your reply", "在等你回复"), mobileBriefAttentionPersonWaiting
		}
		return pick(lang, "Waiting on your reply "+since, since+"一直在等你回复"), mobileBriefAttentionPersonWaiting
	}
	if item.Status == "no_recent_touch" {
		last := item.LastTouch
		if last.IsZero() {
			last = item.OpenedAt
		}
		if last.IsZero() {
			return pick(lang, "No touch recorded in three weeks", "近三周没有联系记录"), mobileBriefAttentionPersonWaiting
		}
		day := mobileBriefDayWords(last, asOf, loc, lang)
		return pick(lang, "No touch recorded since "+day, day+"以来没有联系记录"), mobileBriefAttentionPersonWaiting
	}
	if item.Kind == "one_time_work" {
		switch handoff.Status(item.Status) {
		case handoff.StatusBlocked:
			return pick(lang, "Blocked, waiting for you", "已暂停，等你处理"), mobileBriefAttentionOwnWork
		case handoff.StatusNeedsReview:
			return pick(lang, "Ready for your review", "等你审阅"), mobileBriefAttentionOwnWork
		}
	}
	if !item.DueAt.IsZero() && dueDays <= mobileBriefDueSoonDays {
		return pick(lang, "Due "+dueDay, dueDay+"到期"), mobileBriefAttentionDueSoon
	}
	if item.Kind == mobileBriefThreadOutcomeSource && threadOutcomeUnfinished(item.Outcome) &&
		!(item.Outcome.Kind == "assistant" && item.Outcome.UserActed == "observed") {
		if reason := mobileBriefOutcomeReason(item.Outcome.State, since, lang); reason != "" {
			return reason, mobileBriefAttentionUnfinished
		}
	}
	if item.SourceKind == "purpose" && item.Status == PurposeStateActive && !item.LastTouch.IsZero() &&
		mobileBriefCivilDays(item.LastTouch, asOf, loc) <= -mobileBriefStalledDays {
		day := mobileBriefDayWords(item.LastTouch, asOf, loc, lang)
		return pick(lang, "No progress recorded since "+day, day+"以来没有进展记录"), mobileBriefAttentionStalled
	}
	return "", 0
}

// mobileBriefOutcomeReason says where an observed thread stopped, with its
// age when it was first seen before today. Wording states the recorded last
// state and never claims that something did not happen.
func mobileBriefOutcomeReason(state, since, lang string) string {
	// Each state reads on its own and with an age: "Still a draft",
	// "Still a draft since yesterday".
	var en, enSince, zh string
	switch state {
	case "cart_left":
		en, enSince, zh = "Still in the cart", "Still in the cart", "还在购物车里"
	case "checkout_started":
		en, enSince, zh = "Stopped at checkout", "At checkout", "停在结账页"
	case "draft":
		en, enSince, zh = "Still a draft", "Still a draft", "还是草稿"
	case "answered":
		en, enSince, zh = "Answer ready", "Answer ready", "已有答案"
	case "incomplete":
		en, enSince, zh = "Stopped partway", "Open", "做到一半"
	default:
		return ""
	}
	if since == "" {
		return pick(lang, en, zh)
	}
	return pick(lang, enSince+" "+since, since+zh)
}

// mobileBriefWhen says when an upcoming calendar item starts, relative to the
// brief's day: "at 9am" today, "tomorrow at 9am", "Thursday at 9am" (zh:
// 上午9点, 明天上午9点). An all-day event names only its day.
func mobileBriefWhen(item MobileBriefItem, reference time.Time, loc *time.Location, lang string) string {
	at := mobileBriefParseTime(item.At)
	if at.IsZero() || reference.IsZero() {
		return ""
	}
	if loc == nil {
		loc = time.Local
	}
	local := at.In(loc)
	day := mobileBriefDayWords(at, reference, loc, lang)
	if end := mobileBriefParseTime(item.EndAt); !end.IsZero() && end.Sub(at) >= 24*time.Hour && local.Hour() == 0 && local.Minute() == 0 {
		return day
	}
	clock := briefShortClock(local.Hour(), local.Minute(), lang)
	if mobileBriefCivilDays(at, reference, loc) == 0 {
		return pick(lang, "at "+clock, clock)
	}
	return pick(lang, day+" at "+clock, day+clock)
}
