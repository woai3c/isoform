package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"wails-gui/storage"
)

func TestTodayChatContextUpcomingScheduleUsesCurrentDisplayMetadataOnly(t *testing.T) {
	a := newTodayContextTestApp(t, true)
	now := time.Now()
	job := storage.CronJobRow{
		ID: "upcoming-job", Name: "Atlas review", Description: "Subject: confidential schedule instructions", Enabled: true,
		ScheduleKind: "cron", ScheduleConfig: `{"expr":"0 9 * * *"}`,
		TargetKind: "agent", TargetConfig: `{"prompt":"private execution draft"}`,
		NextRunAt: now.Add(time.Hour).UnixMilli(), LastError: "private execution error",
	}
	if err := a.store.SaveCronJob(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	in := mobileBriefInput{Slot: "morning", WindowStart: now.Add(-time.Hour), AsOf: now.Add(time.Minute), Location: time.Local}
	collectMobileBriefToday(a, &in)
	metadata := in.Today.Context[mobileBriefSourceKey("schedule", job.ID)]
	metadata.Preview = "Subject: confidential preview draft"
	in.Today.Context[mobileBriefSourceKey("schedule", job.ID)] = metadata
	brief := buildMobileBriefV1(in)
	if len(brief.Upcoming) != 1 || !strings.Contains(brief.Upcoming[0].Detail, "confidential schedule instructions") || brief.Upcoming[0].Context == nil {
		t.Fatalf("fixture did not retain display-only upcoming metadata: %+v", brief.Upcoming)
	}
	brief.Upcoming[0].Title = "Superseded private card title"
	dir, err := mobileBriefStateDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := newMobileBriefStore(dir).save(brief, now); err != nil {
		t.Fatal(err)
	}
	ref := TodayChatContextRef{SourceKind: "brief_item", SourceID: brief.Upcoming[0].ID, BriefID: brief.BriefID}
	got, err := a.buildTodayChatContext(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"confidential schedule instructions", "confidential preview draft", "private execution draft", "private execution error", "Superseded private card title"} {
		if strings.Contains(got.Text, forbidden) {
			t.Errorf("upcoming card injected display-only content %q: %s", forbidden, got.Text)
		}
	}
	if !strings.Contains(got.Text, job.Name) || !strings.Contains(got.Text, "enabled: true") || !strings.Contains(got.Text, "<evidence>") {
		t.Fatalf("current schedule context or receipt missing: %s", got.Text)
	}
	item, _, err := a.loadTodayChatContextItem(context.Background(), ref)
	if err != nil || item.Label != job.Name || len(item.PriorLabels) != 1 || item.PriorLabels[0] != brief.Upcoming[0].Title {
		t.Fatalf("composer label refresh lost cached-title identity: %+v, %v", item, err)
	}
}
