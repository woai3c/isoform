package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/handoff"
	"wails-gui/internal/pii"
	"wails-gui/storage"
)

func todayDeliverablePrivacyFixture(t *testing.T) (*App, map[string][]TodayChatContextRef) {
	t.Helper()
	a := newTodayContextTestApp(t, true)
	ctx := context.Background()
	now := time.Now()
	if err := a.activity.store.SaveSession(ActivitySessionRow{ID: "source-session", AppNames: `["Notes"]`, StartTime: now.Add(-time.Minute).UnixMilli(), EndTime: now.UnixMilli()}, nil, nil); err != nil {
		t.Fatal(err)
	}
	project, workspace, err := a.standaloneChatProjectAndWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.store.SaveSession(ctx, storage.SessionRow{LocalID: "work-chat", ProjectID: project, WorkspaceID: workspace, Label: handoffSessionLabelPrefix + "Confidential Atlas outline"}); err != nil {
		t.Fatal(err)
	}
	a.handoffStore, err = handoff.NewStore(filepath.Join(os.Getenv("HOME"), "handoffs"))
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(os.Getenv("HOME"), "outline.md")
	if _, err := a.handoffStore.Save(handoff.Entry{ID: "work", Title: "Confidential Atlas outline", Status: handoff.StatusCompleted, Source: handoff.SourceTriage, SourceID: "source-session", AppName: "Notes", ChatSessionID: "work-chat", KeyFiles: []string{file}, Body: "Private draft body"}); err != nil {
		t.Fatal(err)
	}
	files, _ := json.Marshal([]string{file})
	if err := a.store.SaveChatDeliverable(ctx, storage.ChatDeliverableRow{ID: "chat-output", SessionID: "work-chat", Title: "Confidential Atlas outline", Summary: "Private draft body", Files: string(files)}); err != nil {
		t.Fatal(err)
	}
	if err := a.store.SaveCronJob(ctx, storage.CronJobRow{ID: "job", Name: "Confidential Atlas outline", Enabled: true, ScheduleKind: "at", ScheduleConfig: `{}`, TargetKind: "agent", TargetConfig: `{"handoff_id":"work","session_tag":"work-chat"}`}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.AppendStreamItem(ctx, "work-chat", storage.StreamItemRow{ID: "output", Kind: "result", Content: "<deliverables>\n" + file + "\n</deliverables>"}); err != nil {
		t.Fatal(err)
	}
	if err := a.store.InsertCronRun(ctx, storage.CronRunRow{ID: "run", JobID: "job", Status: "ok", StartedAt: now.UnixMilli(), FinishedAt: now.UnixMilli(), SessionTag: "work-chat", ItemSortStart: 0}); err != nil {
		t.Fatal(err)
	}
	in := mobileBriefInput{Slot: "morning", WindowStart: now.Add(-time.Hour), AsOf: now.Add(time.Minute)}
	collectMobileBriefDeliverables(a, &in)
	brief := buildMobileBriefV1(in)
	if len(brief.Sections.Deliverables) != 3 {
		t.Fatalf("fixture needs all three stored deliverable sources: %+v", brief.Sections.Deliverables)
	}
	dir, err := mobileBriefStateDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := newMobileBriefStore(dir).save(brief, now); err != nil {
		t.Fatal(err)
	}
	refs := map[string][]TodayChatContextRef{}
	for _, item := range brief.Sections.Deliverables {
		for _, evidence := range brief.Evidence {
			if len(item.Refs) == 1 && evidence.Ref == item.Refs[0] {
				refs[item.Kind] = []TodayChatContextRef{
					{SourceKind: "deliverable", SourceID: evidence.SourceID},
					{SourceKind: "brief_item", SourceID: item.ID, BriefID: brief.BriefID},
				}
			}
		}
	}
	return a, refs
}

func TestTodayChatContextDeliverablesRecheckHandoffProvenance(t *testing.T) {
	for _, source := range []string{deliverableSourceHandoff, deliverableSourceChat, deliverableSourceAutomation} {
		for _, change := range []string{"allowed", "sensitive-app", "protected-session", "missing-source", "missing-handoff"} {
			t.Run(source+"/"+change, func(t *testing.T) {
				a, refs := todayDeliverablePrivacyFixture(t)
				if len(refs[source]) != 2 {
					t.Fatalf("missing direct and brief references for %s: %+v", source, refs)
				}
				for _, ref := range refs[source] {
					got, err := a.buildTodayChatContext(context.Background(), ref)
					if err != nil || !strings.Contains(got.Text, "Confidential Atlas outline") || strings.Contains(got.Text, "Private draft body") {
						t.Fatalf("allowed %s reference did not resolve safely: %v\n%s", ref.SourceKind, err, got.Text)
					}
				}
				switch change {
				case "allowed":
					return
				case "sensitive-app":
					previous := pii.GetUserSensitiveApps()
					t.Cleanup(func() { pii.SetUserSensitiveApps(previous) })
					pii.SetUserSensitiveApps(map[string]string{"com.apple.Notes": "Notes"})
				case "protected-session":
					if _, err := a.activity.store.db.Exec(`UPDATE activity_sessions SET triage_status = 'protected' WHERE id = 'source-session'`); err != nil {
						t.Fatal(err)
					}
				case "missing-source":
					entry, err := a.handoffStore.Get("work")
					if err != nil {
						t.Fatal(err)
					}
					entry.SourceID = "unavailable-source"
					if _, err := a.handoffStore.Save(entry); err != nil {
						t.Fatal(err)
					}
				case "missing-handoff":
					if err := a.handoffStore.Delete("work"); err != nil {
						t.Fatal(err)
					}
				}
				for _, ref := range refs[source] {
					got, err := a.buildTodayChatContext(context.Background(), ref)
					if !errors.Is(err, errTodayChatItemUnavailable) || got.Text != "" {
						t.Errorf("%s reference exposed excluded backing after %s: %v\n%s", ref.SourceKind, change, err, got.Text)
					}
				}
			})
		}
	}
}

func TestTodayChatContextDeliverablesRequireCurrentSessionAndJob(t *testing.T) {
	for _, source := range []string{deliverableSourceChat, deliverableSourceAutomation} {
		t.Run(source, func(t *testing.T) {
			a, refs := todayDeliverablePrivacyFixture(t)
			switch source {
			case deliverableSourceChat:
				if err := a.store.DeleteSession(context.Background(), "work-chat"); err != nil {
					t.Fatal(err)
				}
			case deliverableSourceAutomation:
				if err := a.store.DeleteCronJob(context.Background(), "job"); err != nil {
					t.Fatal(err)
				}
			}
			for _, ref := range refs[source] {
				got, err := a.buildTodayChatContext(context.Background(), ref)
				if !errors.Is(err, errTodayChatItemUnavailable) || got.Text != "" {
					t.Errorf("%s reference survived missing %s backing: %v\n%s", ref.SourceKind, source, err, got.Text)
				}
			}
		})
	}
}

func TestTodayChatContextDeliverablesValidateOtherDerivedSources(t *testing.T) {
	for _, test := range []struct {
		name, source, meeting, jobSource string
		protected, orphan, cycle, allow  bool
	}{
		{name: "plain-chat", source: deliverableSourceChat, allow: true},
		{name: "allowed-meeting", source: deliverableSourceChat, meeting: "source-session", allow: true},
		{name: "protected-meeting", source: deliverableSourceChat, meeting: "source-session", protected: true},
		{name: "missing-meeting", source: deliverableSourceChat, meeting: "missing"},
		{name: "plain-automation", source: deliverableSourceAutomation, allow: true},
		{name: "orphan-automation-chat", source: deliverableSourceChat, orphan: true},
		{name: "protected-job-source", source: deliverableSourceAutomation, jobSource: "source-session", protected: true},
		{name: "cyclic-job-source", source: deliverableSourceAutomation, jobSource: "work-chat", cycle: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			a, refs := todayDeliverablePrivacyFixture(t)
			ctx := context.Background()
			if err := a.handoffStore.Delete("work"); err != nil {
				t.Fatal(err)
			}
			session, err := a.store.LoadSession(ctx, "work-chat")
			if err != nil {
				t.Fatal(err)
			}
			session.Label, session.MeetingID = "Atlas review", test.meeting
			if test.source == deliverableSourceAutomation || test.orphan || test.cycle {
				session.Label = cronSessionLabel("job")
			}
			if err := a.store.SaveSession(ctx, *session); err != nil {
				t.Fatal(err)
			}
			config := `{"session_tag":"work-chat"}`
			if test.orphan {
				config = `{}`
			}
			if err := a.store.UpdateCronJob(ctx, "job", map[string]any{"target_config": config, "source_session_id": test.jobSource}); err != nil {
				t.Fatal(err)
			}
			if test.protected {
				if _, err := a.activity.store.db.Exec(`UPDATE activity_sessions SET triage_status = 'protected' WHERE id = 'source-session'`); err != nil {
					t.Fatal(err)
				}
			}
			for _, ref := range refs[test.source] {
				got, err := a.buildTodayChatContext(ctx, ref)
				if test.allow {
					if err != nil || got.Text == "" {
						t.Errorf("allowed %s backing unavailable: %v", ref.SourceKind, err)
					}
				} else if !errors.Is(err, errTodayChatItemUnavailable) || got.Text != "" {
					t.Errorf("%s reference bypassed derived source validation: %v\n%s", ref.SourceKind, err, got.Text)
				}
			}
		})
	}
}

func TestTodayChatContextDeliverablesDoNotConfuseActivityAndChatSources(t *testing.T) {
	for _, change := range []string{"allowed-activity", "allowed-chat", "protected-activity", "sensitive-activity", "unavailable-activity-store"} {
		t.Run(change, func(t *testing.T) {
			a, refs := todayDeliverablePrivacyFixture(t)
			ctx := context.Background()
			session, err := a.store.LoadSession(ctx, "work-chat")
			if err != nil {
				t.Fatal(err)
			}
			if err := a.store.SaveSession(ctx, storage.SessionRow{LocalID: "source-session", ProjectID: session.ProjectID, WorkspaceID: session.WorkspaceID, Label: "Independent chat"}); err != nil {
				t.Fatal(err)
			}
			// Remove handoff provenance so the job's ambiguous source ID is
			// the only remaining Activity gate for this automation result.
			if err := a.handoffStore.Delete("work"); err != nil {
				t.Fatal(err)
			}
			session.Label = "Atlas review"
			if err := a.store.SaveSession(ctx, *session); err != nil {
				t.Fatal(err)
			}
			if err := a.store.UpdateCronJob(ctx, "job", map[string]any{"target_config": `{"session_tag":"work-chat"}`, "source_session_id": "source-session"}); err != nil {
				t.Fatal(err)
			}
			switch change {
			case "allowed-chat":
				if _, err := a.activity.store.db.Exec(`DELETE FROM activity_sessions WHERE id = 'source-session'`); err != nil {
					t.Fatal(err)
				}
			case "protected-activity":
				if _, err := a.activity.store.db.Exec(`UPDATE activity_sessions SET triage_status = 'protected' WHERE id = 'source-session'`); err != nil {
					t.Fatal(err)
				}
			case "sensitive-activity":
				previous := pii.GetUserSensitiveApps()
				t.Cleanup(func() { pii.SetUserSensitiveApps(previous) })
				pii.SetUserSensitiveApps(map[string]string{"com.apple.Notes": "Notes"})
			case "unavailable-activity-store":
				a.activity = nil
			}
			for _, ref := range refs[deliverableSourceAutomation] {
				got, err := a.buildTodayChatContext(ctx, ref)
				if strings.HasPrefix(change, "allowed-") {
					if err != nil || !strings.Contains(got.Text, "Confidential Atlas outline") {
						t.Errorf("allowed %s source unavailable: %v\n%s", ref.SourceKind, err, got.Text)
					}
				} else if !errors.Is(err, errTodayChatItemUnavailable) || got.Text != "" {
					t.Errorf("%s reference fell through excluded Activity to same-ID chat: %v\n%s", ref.SourceKind, err, got.Text)
				}
			}
		})
	}
}

func TestTodayChatContextDeliverablesRecognizeRenamedHandoffSessions(t *testing.T) {
	a, refs := todayDeliverablePrivacyFixture(t)
	ctx := context.Background()
	entry, err := a.handoffStore.Get("work")
	if err != nil {
		t.Fatal(err)
	}
	sessionID, err := a.createHandoffExecutionSession(entry)
	if err != nil {
		t.Fatal(err)
	}
	entry.ChatSessionID = sessionID
	if _, err := a.handoffStore.Save(entry); err != nil {
		t.Fatal(err)
	}
	row, err := a.store.GetChatDeliverable(ctx, "chat-output")
	if err != nil {
		t.Fatal(err)
	}
	row.SessionID = sessionID
	if err := a.store.SaveChatDeliverable(ctx, *row); err != nil {
		t.Fatal(err)
	}
	session, err := a.store.LoadSession(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	session.Label = "Renamed completed work"
	if err := a.store.SaveSession(ctx, *session); err != nil {
		t.Fatal(err)
	}
	for _, ref := range refs[deliverableSourceChat] {
		if got, err := a.buildTodayChatContext(ctx, ref); err != nil || got.Text == "" {
			t.Fatalf("renamed session with allowed backing unavailable: %v", err)
		}
	}
	if err := a.handoffStore.Delete("work"); err != nil {
		t.Fatal(err)
	}
	for _, ref := range refs[deliverableSourceChat] {
		got, err := a.buildTodayChatContext(ctx, ref)
		if !errors.Is(err, errTodayChatItemUnavailable) || got.Text != "" {
			t.Errorf("%s reference forgot renamed session's Handoff group provenance: %v\n%s", ref.SourceKind, err, got.Text)
		}
	}
}
