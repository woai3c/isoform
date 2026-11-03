package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/engagement"
	"wails-gui/storage"
)

func TestMobileBriefPurposeWaitingRefKeepsCanonicalTitleBeyondCap(t *testing.T) {
	app := newMobileBriefPurposeTestApp(t)
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	var details []PurposeOpenLoopDetail
	for i := 0; i < 6; i++ {
		details = append(details, mobileBriefPurposeFixture(fmt.Sprintf("goal-%d", i), now))
	}
	details[5].Current.Title = "Ship the beta"
	details[5].Current.Counterparty = "Ana Lee"
	in := mobileBriefInput{AsOf: now, Location: time.UTC}
	collectMobileBriefPurposes(app, &in, details, nil)
	brief := buildMobileBriefV1(in)
	if len(brief.Sections.WorkingToward) != 5 || len(brief.Sections.WaitingOnYou) != 1 {
		t.Fatalf("unexpected sections: %+v", brief.Sections)
	}
	for _, item := range brief.Sections.WorkingToward {
		if item.Title == "Ship the beta" {
			t.Fatal("fixture must put the waiting purpose outside the working-toward cap")
		}
	}
	if brief.Sections.WaitingOnYou[0].Title != "Ana Lee" {
		t.Fatal("waiting card must still name the person")
	}
	for _, ref := range brief.Evidence {
		if ref.Ref == brief.Sections.WaitingOnYou[0].Refs[0] {
			if ref.SourceID != "goal-5" || ref.Label != "Ship the beta" {
				t.Fatalf("purpose identity was overwritten by the counterparty: %+v", ref)
			}
			return
		}
	}
	t.Fatal("waiting item has no purpose reference")
}

func TestMobileBriefNarrativeIncludesCommitmentWithoutChatReport(t *testing.T) {
	for _, state := range []string{storage.EngagementStatePending, storage.EngagementStateSent} {
		t.Run(state, func(t *testing.T) {
			app := newMobileBriefPurposeTestApp(t)
			now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
			detail := mobileBriefPurposeFixture("due", now)
			detail.OpenLoop.Kind = PurposeKindCommitment
			detail.Current.Title = "Send the release plan"
			detail.Current.DueKind, detail.Current.DueValue = PurposeDueDate, "2026-10-16"
			moment, _ := engagementCommitmentSlippingMoment(detail, now)
			_, err := app.store.CreateEngagementMoment(context.Background(), storage.EngagementMomentRow{
				ID: "moment", Kind: string(engagement.KindCommitmentSlipping), SubjectID: detail.OpenLoop.ID,
				DedupeKey: moment.DedupeKey, State: state, CreatedAt: now.Add(-time.Hour).UnixMilli(),
			})
			if err != nil {
				t.Fatal(err)
			}
			in := mobileBriefInput{AsOf: now, Location: time.UTC}
			collectMobileBriefPurposes(app, &in, []PurposeOpenLoopDetail{detail}, nil)
			brief := buildMobileBriefV1(in)
			if len(brief.Sections.Commitments) != 1 || brief.Sections.Commitments[0].MomentID != "moment" || brief.Sections.Commitments[0].ChatMessageID != "" {
				t.Fatalf("fixture did not retain the unreported moment: %+v", brief.Sections.Commitments)
			}
			// Due today and not reported in main chat: a pick of its own.
			found := false
			for _, p := range mobileBriefPicks(brief) {
				found = found || p.Tier == mobileBriefPickDue && p.Item.Title == "Send the release plan"
			}
			if !found {
				t.Fatalf("a pending or toast-only reminder disappeared from the picks: %v", briefPickRefs(mobileBriefPicks(brief)))
			}
		})
	}
}

func TestMobileBriefNarrativeKeepsProgressForReportedCommitment(t *testing.T) {
	app := newMobileBriefPurposeTestApp(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	detail := mobileBriefPurposeFixture("ship", now)
	detail.OpenLoop.Kind = PurposeKindCommitment
	detail.Current.Title = "Ship the companion"
	detail.Current.DueKind, detail.Current.DueValue = PurposeDueDate, "2026-10-16"
	moment, _ := engagementCommitmentSlippingMoment(detail, now)
	row, err := app.store.CreateEngagementMoment(ctx, storage.EngagementMomentRow{
		ID: "reported-moment", Kind: string(moment.Kind), SubjectID: moment.SubjectID, DedupeKey: moment.DedupeKey,
		State: storage.EngagementStatePending, CreatedAt: now.Add(-time.Hour).UnixMilli(),
	})
	if err != nil {
		t.Fatal(err)
	}
	main, err := app.ensureMainChat(ctx)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(mainChatReport{Source: mainChatSourceEngagement, Key: engagementReportKey(momentFromRow(row), now), Status: string(engagement.KindCommitmentSlipping)})
	if _, err := app.store.AppendStreamItem(ctx, main.LocalID, storage.StreamItemRow{
		ID: "reported-message", Kind: "system", Content: "RAW_CHAT_BODY", ToolName: mainChatReportTool, ToolInput: string(payload), CreatedAt: now.Add(-time.Hour).UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}
	session := seedMobileBriefPurposeRun(t, app, "release", 1, now.Add(-30*time.Minute), 1000, []string{"Editor"})
	linkMobileBriefPurposeSession(&detail, session, now.Add(-30*time.Minute))
	step := mobileBriefPurposeFixture("send", now)
	step.OpenLoop.Kind, step.Current.Title = PurposeKindCommitment, "Send the release plan"
	detail.Relations = []PurposeRelationDetail{{Relation: PurposeRelationRecord{SubjectID: "ship", ObjectID: "send", RelationType: PurposeRelationDependsOn}, Current: PurposeRelationEventRecord{EventType: PurposeRelationEventAsserted}}}
	in := mobileBriefInput{AsOf: now, Location: time.UTC}
	collectMobileBriefPurposes(app, &in, []PurposeOpenLoopDetail{detail, step}, nil)
	brief := buildMobileBriefV1(in)
	if len(brief.Sections.Commitments) != 1 || brief.Sections.Commitments[0].ChatMessageID != "reported-message" {
		t.Fatalf("missing existing chat reference: %+v", brief.Sections.Commitments)
	}
	if brief.Sections.WorkingToward[0].ChatMessageID != "" {
		t.Error("the commitment report must not mark the progress row as a duplicate")
	}
	// The reminder main chat reported is not said again; the goal that moved
	// still is, as progress.
	picks := mobileBriefPicks(brief)
	if len(picks) != 1 || picks[0].Tier != mobileBriefPickGoal || picks[0].Item.ChatMessageID != "" {
		t.Fatalf("reported commitment lost its independent progress: %v", briefPickRefs(picks))
	}
	prompt := mobileBriefTestPrompt(brief, "English")
	for _, excluded := range []string{"\ncommitments:", "| engagement_moment |", "| chat_message |", "RAW_CHAT_BODY", "Compared the release options"} {
		if strings.Contains(prompt, excluded) {
			t.Errorf("delivered reminder entered narrative: %q", excluded)
		}
	}
}

func TestMobileBriefPurposeDeterministicCopyUsesUILanguage(t *testing.T) {
	app := newMobileBriefPurposeTestApp(t)
	if err := app.store.SetUIState(context.Background(), uiLanguageStateKey, "zh-CN"); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	untouched := mobileBriefPurposeFixture("untouched", now)
	untouched.Current.Counterparty = "Ana Lee"
	due := mobileBriefPurposeFixture("due", now)
	due.OpenLoop.Kind = PurposeKindCommitment
	due.Current.DueKind, due.Current.DueValue = PurposeDueDate, "2026-10-16"
	touched := mobileBriefPurposeFixture("touched", now)
	touched.Current.Counterparty = "Bo"
	at := now.AddDate(0, 0, -22)
	if err := app.activity.store.SaveSession(ActivitySessionRow{ID: "old-session", StartTime: at.Add(-time.Minute).UnixMilli(), EndTime: at.UnixMilli(), AppNames: `["Editor"]`, TriageStatus: "done", CreatedAt: at.UnixMilli()}, nil, nil); err != nil {
		t.Fatal(err)
	}
	linkMobileBriefPurposeSession(&touched, "old-session", at)
	in := mobileBriefInput{AsOf: now, Location: time.UTC}
	collectMobileBriefPurposes(app, &in, []PurposeOpenLoopDetail{untouched, due, touched}, nil)
	brief := buildMobileBriefV1(in)
	raw, _ := json.Marshal(brief)
	for _, english := range []string{"No linked activity", "No recorded touch", "Recorded activity"} {
		if strings.Contains(string(raw), english) {
			t.Errorf("English deterministic copy in Chinese brief: %q", english)
		}
	}
	for _, translated := range []string{"尚无关联活动记录。", "这项承诺仍在你的计划中。", "近三周没有关联活动记录：", "没有联系记录：", "已记录的活动"} {
		if !strings.Contains(string(raw), translated) {
			t.Errorf("missing translated copy %q in %s", translated, raw)
		}
	}
}

func TestMobileBriefProtocolExampleMatchesEmptySectionEncoding(t *testing.T) {
	doc, err := os.ReadFile("../mobile-protocol/PROTOCOL.md")
	if err != nil {
		t.Fatal(err)
	}
	// Read the brief example, rather than duplicating its field list in a test.
	start := strings.Index(string(doc), `"briefId":`)
	if start < 0 {
		t.Fatal("protocol has no brief example")
	}
	section := strings.Index(string(doc)[start:], `"sections": {`) + start + len(`"sections": `)
	end := strings.Index(string(doc)[section:], "}") + section + 1
	var example, encoded map[string]json.RawMessage
	if err := json.Unmarshal(doc[section:end], &example); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(buildMobileBriefV1(mobileBriefInput{AsOf: time.Now(), Location: time.UTC}).Sections)
	if err := json.Unmarshal(raw, &encoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(example, encoded) {
		t.Fatalf("documented empty sections differ from wire encoding: example=%s wire=%s", doc[section:end], raw)
	}
}
