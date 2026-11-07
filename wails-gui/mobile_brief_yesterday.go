package main

import (
	"context"
	"encoding/json"
	"sort"
	"time"
)

// mobileBriefYesterdayActivity freezes the allocation and source identities,
// not a claim that any unobserved work did or did not happen. The same readable
// projection as Your week must be used before the snapshot reaches a model.
func (a *App) mobileBriefYesterdayActivity(ctx context.Context, asOf time.Time, loc *time.Location) *yourWeekSnapshot {
	return a.mobileBriefYesterdayActivityFor(ctx, asOf, asOf, loc)
}

// mobileBriefYesterdayActivityFor is the day before day, the brief's day. The
// window never runs past madeAt, the time the brief is made, so an early brief
// claims nothing after it was made.
func (a *App) mobileBriefYesterdayActivityFor(ctx context.Context, madeAt, day time.Time, loc *time.Location) *yourWeekSnapshot {
	if a == nil || a.activity == nil || a.activity.store == nil || a.IsClosedEyesModeEnabled() || madeAt.IsZero() || day.IsZero() {
		return nil
	}
	if loc == nil {
		loc = time.Local
	}
	local := day.In(loc)
	end := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	start := end.AddDate(0, 0, -1)
	if end.After(madeAt) {
		end = madeAt
	}
	spent, _, err := a.activityMomentTimeMs(ctx, start.UnixMilli(), end.UnixMilli())
	if err != nil || ctx.Err() != nil {
		return nil
	}
	snap := &yourWeekSnapshot{Version: 1, StartDate: start.Format("2006-01-02"), CutoffMs: end.UnixMilli(), HasActivityProvenance: true}
	var ids []string
	for id, ms := range spent {
		if id != "" && ms > 0 {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		snap.Activities = append(snap.Activities, yourWeekSnapshotActivity{ID: id, DayMs: []int64{spent[id]}})
	}
	snap.Topics = a.yourWeekTopicMinutes(spent)
	return snap
}

// bindMobileBriefYesterday returns only currently readable topic totals and
// freezes their source identities. A later source withdrawal must invalidate
// any synthesis that consumed those totals, even when it did not cite them.
func (a *App) bindMobileBriefYesterday(ctx context.Context, brief MobileBriefV1, binding *mobileBriefModelBinding, snapshot *yourWeekSnapshot) ([]yourWeekTopic, bool) {
	if binding == nil || snapshot == nil || len(snapshot.Activities) == 0 {
		return nil, false
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return nil, false
	}
	var frozen yourWeekSnapshot
	if json.Unmarshal(data, &frozen) != nil {
		return nil, false
	}
	topics, ok := a.mobileBriefYesterdayReadable(ctx, brief, &frozen)
	if !ok {
		return nil, false
	}
	binding.YesterdayActivity, binding.YesterdayDigest = &frozen, mobileBriefModelDigest(topics)
	return topics, binding.YesterdayDigest != ""
}

func (a *App) mobileBriefYesterdayBindingReadable(ctx context.Context, brief MobileBriefV1, binding *mobileBriefModelBinding) bool {
	if binding == nil || binding.YesterdayDigest == "" {
		return false
	}
	topics, ok := a.mobileBriefYesterdayReadable(ctx, brief, binding.YesterdayActivity)
	return ok && mobileBriefModelDigest(topics) == binding.YesterdayDigest
}

func (a *App) mobileBriefYesterdayReadable(ctx context.Context, brief MobileBriefV1, snapshot *yourWeekSnapshot) ([]yourWeekTopic, bool) {
	if a == nil || a.IsClosedEyesModeEnabled() && !pausedBriefReadAllowed(ctx) || a.activity == nil || a.activity.store == nil || snapshot == nil || !snapshot.HasActivityProvenance || ctx.Err() != nil {
		return nil, false
	}
	loc := mobileBriefNarrativeLocation(brief)
	start, err := time.ParseInLocation("2006-01-02", snapshot.StartDate, loc)
	local := mobileBriefAsOfTime(brief).In(loc)
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	if err != nil || !start.Equal(today.AddDate(0, 0, -1)) || snapshot.CutoffMs != today.UnixMilli() || len(snapshot.Activities) == 0 {
		return nil, false
	}
	readable := a.yourWeekReadable(ctx, *snapshot, start, mobileBriefAsOfTime(brief))
	if readable.SeenDays < 0 || ctx.Err() != nil {
		return nil, false
	}
	return readable.Topics, true
}
