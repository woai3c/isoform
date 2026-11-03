package main

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// The stored brief owns Today's local signals. Existing section records are
// enriched in place; upcoming schedules carry structured metadata. Content
// never enters the opening narrative's model prompt. Activity summaries have
// no source provenance and belong only to the desktop Today view.
type mobileBriefTodayInput struct {
	Upcoming []mobileBriefCandidate
	Context  map[string]MobileBriefItemContext
	// Unavailable distinguishes a failed source read from a successful empty list.
	Unavailable map[string]bool
}

func mobileBriefSourceKey(kind, id string) string { return kind + "\x00" + id }

func appendMobileBriefSource(items *[]mobileBriefCandidate, candidate mobileBriefCandidate) {
	for _, item := range *items {
		if item.SourceKind == candidate.SourceKind && item.SourceID == candidate.SourceID {
			return
		}
	}
	*items = append(*items, candidate)
}

func mobileBriefPreview(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#"))
		if line != "" {
			return mobileBriefText(line, mobileBriefTextLimit)
		}
	}
	return ""
}

func collectMobileBriefToday(a *App, in *mobileBriefInput) {
	loc := in.Location
	if loc == nil {
		loc = time.Local
	}
	now := in.AsOf.In(loc)
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	today := &mobileBriefTodayInput{Context: map[string]MobileBriefItemContext{}, Unavailable: map[string]bool{}}
	in.Today = today
	collectMobileBriefNoticed(a, in)
	if in.Language == "" {
		// Today refreshes build their own input; attention lines follow the UI.
		in.Language = mobileBriefVoiceLang(a.currentUILanguageName())
	}
	isToday := func(at time.Time) bool { return !at.Before(dayStart) && !at.After(in.AsOf) }
	remember := func(kind, id string, detail MobileBriefItemContext) {
		today.Context[mobileBriefSourceKey(kind, id)] = detail
	}

	unnamed := pick(mobileBriefVoiceLang(a.currentUILanguage()), "Automation", "自动化")
	jobs := a.ListCronJobs()
	hidden := map[string]bool{}
	for _, job := range jobs {
		hidden[job.ID] = job.SurfaceKind == todayNewsSurfaceKind
		if hidden[job.ID] || !job.Enabled || job.NextRunAt <= 0 || job.NextRunAt > in.AsOf.Add(48*time.Hour).UnixMilli() {
			continue
		}
		today.Upcoming = append(today.Upcoming, mobileBriefCandidate{
			SourceKind: "schedule", SourceID: job.ID,
			Title:  mobileBriefFirstText(job.Name, job.Description, unnamed),
			Detail: job.Description, Status: "upcoming", Kind: "automation", At: mobileBriefMillis(job.NextRunAt),
		})
		remember("schedule", job.ID, MobileBriefItemContext{JobID: job.ID, ScheduleSpec: mobileBriefSchedule(job)})
	}
	// Include failures earlier today even when this brief's window starts later.
	if runs, err := a.GetCronRunsByDateRange(dayStart.UnixMilli(), in.AsOf.UnixMilli()); err == nil {
		for _, run := range runs {
			if hidden[run.JobID] || run.Status != "failed" || !isToday(mobileBriefMillis(run.StartedAt)) {
				continue
			}
			appendMobileBriefSource(&in.Schedules, mobileBriefCandidate{
				SourceKind: "schedule_run", SourceID: run.ID,
				Title: mobileBriefFirstText(run.JobName, unnamed), Status: run.Status,
				Kind: run.TriggerKind, At: mobileBriefMillis(run.StartedAt),
			})
			remember("schedule_run", run.ID, MobileBriefItemContext{
				Preview: mobileBriefFirstText(run.Error, run.ResultSummary), JobID: run.JobID,
			})
		}
	} else {
		today.Unavailable["schedule_run"] = true
		today.Unavailable["schedule"] = true
	}
	if a.handoffStore != nil {
		if items, err := a.handoffStore.List(); err == nil {
			for _, item := range items {
				if !mobileBriefWorkOpen(item.Status) || !a.mobileBriefHandoffReadable(context.Background(), item) {
					continue
				}
				// Work already in Moved must not appear a second time in Open loops.
				found := false
				for _, moved := range in.Moved {
					if moved.SourceKind == "history" && moved.SourceID == item.ID {
						found = true
						break
					}
				}
				if !found {
					appendMobileBriefSource(&in.OpenLoops, mobileBriefCandidate{
						SourceKind: "history", SourceID: item.ID, Title: item.Title,
						Detail: mobileBriefWorkDetail(item.Status), Status: string(item.Status),
						Kind: "one_time_work", At: mobileBriefMillis(item.UpdatedAt), OpenedAt: mobileBriefMillis(item.CreatedAt),
					})
				}
				remember("history", item.ID, MobileBriefItemContext{
					Preview: mobileBriefPreview(item.Body), BlockedReason: item.BlockedReason,
				})
			}
		} else {
			today.Unavailable["history"] = true
		}
	} else {
		today.Unavailable["history"] = true
	}
	allowMemory, privacyErr := a.memoryReadFilter()
	if memories, err := a.GetMemoryEntries(); err == nil {
		for _, item := range memories {
			updated := item.UpdatedAt
			if updated == 0 {
				updated = item.ModTime
			}
			at := mobileBriefMillis(updated)
			if !isToday(at) {
				continue
			}
			appendMobileBriefSource(&in.Learned, mobileBriefCandidate{
				SourceKind: "memory", SourceID: item.ID, Title: item.Title, Status: "updated", Kind: "memory", At: at,
				Apps: item.Apps, AppsShown: true, Protected: privacyErr != nil || !allowMemory(item.ID, item.Content),
			})
			source := ""
			if len(item.Apps) == 0 {
				source = item.Source
			}
			remember("memory", item.ID, MobileBriefItemContext{Preview: mobileBriefPreview(item.Content), SourceLabel: source})
		}
	} else {
		today.Unavailable["memory"] = true
	}
	if knowledge, err := a.GetKnowledgeEntries(); err == nil {
		for _, item := range knowledge {
			at := mobileBriefParseTime(item.Date)
			if !isToday(at) {
				continue
			}
			appendMobileBriefSource(&in.Learned, mobileBriefCandidate{
				SourceKind: "knowledge", SourceID: item.ID, Title: item.Title, Status: "updated", Kind: "knowledge", At: at,
				Apps: strings.Split(item.Apps, ","), AppsShown: true, Protected: privacyErr != nil || !allowMemory(item.ID, item.Content),
			})
			remember("knowledge", item.ID, MobileBriefItemContext{Preview: mobileBriefPreview(item.Content)})
		}
	} else {
		today.Unavailable["knowledge"] = true
	}
	// Deliverables aggregate handoffs and the same database. An unavailable
	// component must not erase its previously projected artifacts either.
	today.Unavailable["deliverable"] = today.Unavailable["history"] || today.Unavailable["schedule_run"]
	if items, err := a.ListDeliverables(); err == nil {
		for _, item := range items {
			at := mobileBriefMillis(item.UpdatedAt)
			if !isToday(at) {
				continue
			}
			appendMobileBriefSource(&in.Deliverables, mobileBriefCandidate{
				SourceKind: "deliverable", SourceID: item.ID, Title: item.Title,
				Status: item.Status, Kind: item.Source, At: at,
			})
			remember("deliverable", item.ID, MobileBriefItemContext{Preview: item.Summary, FileCount: len(item.Files)})
		}
	} else {
		today.Unavailable["deliverable"] = true
	}
}

// Attach extra context only to items that survived the brief's privacy filter,
// section caps and evidence budget. Reuse that same budget for upcoming work.
func applyMobileBriefToday(brief *MobileBriefV1, today *mobileBriefTodayInput) {
	if today == nil {
		return
	}
	refs := map[string]MobileBriefRef{}
	for _, ref := range brief.Evidence {
		refs[ref.Ref] = ref
	}
	for _, items := range [][]MobileBriefItem{brief.Sections.Moved, brief.Sections.OpenLoops, brief.Sections.Learned, brief.Sections.Deliverables, brief.Sections.Schedules, brief.Upcoming} {
		for i := range items {
			if len(items[i].Refs) == 0 {
				continue
			}
			ref := refs[items[i].Refs[0]]
			if extra, ok := today.Context[mobileBriefSourceKey(ref.SourceKind, ref.SourceID)]; ok {
				extra.Preview = mobileBriefText(extra.Preview, mobileBriefTextLimit)
				extra.Schedule = mobileBriefText(extra.Schedule, mobileBriefTextLimit)
				extra.SourceLabel = mobileBriefText(extra.SourceLabel, mobileBriefTextLimit)
				extra.JobID = mobileBriefText(extra.JobID, mobileBriefSourceIDLimit)
				extra.FileCount = mobileBriefNonNegative(extra.FileCount)
				extra.BlockedReason = mobileBriefText(extra.BlockedReason, mobileBriefKindLimit)
				items[i].Context = &extra
			}
		}
	}
	if len(brief.Upcoming) > 0 {
		brief.ColdStart = false
	}
}

// Reserve imminent actions before the other sections consume the shared budget.
func (b *mobileBriefBuilder) upcoming(today *mobileBriefTodayInput) []MobileBriefItem {
	if today == nil {
		return nil
	}
	upcoming := prepareMobileBriefCandidates(today.Upcoming, func(a, b mobileBriefCandidate) bool {
		if !a.At.Equal(b.At) {
			return a.At.Before(b.At)
		}
		return a.SourceID < b.SourceID
	})
	if len(upcoming) > 3 {
		upcoming = upcoming[:3]
		b.truncated = append(b.truncated, "upcoming")
	}
	return b.section("upcoming", upcoming)
}

func mobileBriefSchedule(job CronJobState) *MobileBriefScheduleSpec {
	// Read only known schedule fields; targets, prompts and credentials never
	// enter the brief's schedule metadata.
	var cfg struct {
		Expression string `json:"expr"`
		Timezone   string `json:"tz"`
		EveryMS    int64  `json:"every_ms"`
		AtMS       int64  `json:"at_ms"`
	}
	data, _ := json.Marshal(job.ScheduleConfig)
	_ = json.Unmarshal(data, &cfg)
	return &MobileBriefScheduleSpec{Kind: job.ScheduleKind, Expression: mobileBriefText(cfg.Expression, mobileBriefTextLimit), Timezone: mobileBriefText(cfg.Timezone, mobileBriefSourceIDLimit), EveryMS: cfg.EveryMS, AtMS: cfg.AtMS, NextRunAt: job.NextRunAt}
}
