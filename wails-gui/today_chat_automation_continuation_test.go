package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wails-gui/internal/pii"
	"wails-gui/storage"
)

func TestTodayChatContextDeliverablesResolveAutomationContinuationOrigin(t *testing.T) {
	for _, change := range []string{"allowed", "missing-job", "sensitive-app", "protected-session", "missing-handoff", "missing-job-id", "malformed-context", "unrelated-legacy-alias"} {
		t.Run(change, func(t *testing.T) {
			a, refs := todayDeliverablePrivacyFixture(t)
			ctx := context.Background()
			hidden, err := a.store.LoadSession(ctx, "work-chat")
			if err != nil {
				t.Fatal(err)
			}
			hidden.Hidden = true
			if err := a.store.SaveSession(ctx, *hidden); err != nil {
				t.Fatal(err)
			}
			// Continue in Chat creates a visible session with a new local ID;
			// the job retains its hidden execution session in target_config.
			const continuedID = "visible-automation-result"
			if err := a.store.SaveSession(ctx, storage.SessionRow{
				LocalID: continuedID, ProjectID: hidden.ProjectID, WorkspaceID: hidden.WorkspaceID,
				Label: "Scheduled: Confidential Atlas outline", AutomationResult: true,
			}); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(os.Getenv("HOME"), "outline.md"), []byte("Draft file content stays outside model context"), 0600); err != nil {
				t.Fatal(err)
			}
			row, err := a.store.GetChatDeliverable(ctx, "chat-output")
			if err != nil {
				t.Fatal(err)
			}
			row.SessionID = continuedID
			if err := a.store.SaveChatDeliverable(ctx, *row); err != nil {
				t.Fatal(err)
			}
			// Match stream-helpers.ts: the first user turn persists the
			// originating job ID beside raw output, which is never injected.
			metadata := `{"output":"Private scheduled draft payload","jobId":"job"}`
			switch change {
			case "missing-job", "unrelated-legacy-alias":
				if err := a.store.DeleteCronJob(ctx, "job"); err != nil {
					t.Fatal(err)
				}
				if change == "unrelated-legacy-alias" {
					if err := a.store.SaveCronJob(ctx, storage.CronJobRow{ID: "unrelated", Name: "Unrelated schedule", Enabled: true, ScheduleKind: "at", ScheduleConfig: `{}`, TargetKind: "agent", TargetConfig: `{"session_tag":"visible-automation-result"}`}); err != nil {
						t.Fatal(err)
					}
				}
			case "sensitive-app":
				previous := pii.GetUserSensitiveApps()
				t.Cleanup(func() { pii.SetUserSensitiveApps(previous) })
				pii.SetUserSensitiveApps(map[string]string{"com.apple.Notes": "Notes"})
			case "protected-session":
				if _, err := a.activity.store.db.Exec(`UPDATE activity_sessions SET triage_status = 'protected' WHERE id = 'source-session'`); err != nil {
					t.Fatal(err)
				}
			case "missing-handoff":
				if err := a.handoffStore.Delete("work"); err != nil {
					t.Fatal(err)
				}
			case "missing-job-id":
				metadata = `{"output":"Private scheduled draft payload","jobId":""}`
			case "malformed-context":
				metadata = `{"output":"Private scheduled draft payload","jobId":`
			}
			if _, err := a.store.AppendStreamItem(ctx, continuedID, storage.StreamItemRow{
				ID: "continue-question", Kind: "user", Content: "Polish this file for the review", AutomationContext: metadata,
			}); err != nil {
				t.Fatal(err)
			}
			for _, ref := range refs[deliverableSourceChat] {
				got, err := a.buildTodayChatContext(ctx, ref)
				if change == "allowed" {
					if err != nil || !strings.Contains(got.Text, "Confidential Atlas outline") {
						t.Errorf("allowed %s continuation unavailable: %v\n%s", ref.SourceKind, err, got.Text)
					}
					for _, forbidden := range []string{"Private scheduled draft payload", "Draft file content", "Polish this file", "Private draft body"} {
						if strings.Contains(got.Text, forbidden) {
							t.Errorf("%s continuation included raw payload %q", ref.SourceKind, forbidden)
						}
					}
				} else if !errors.Is(err, errTodayChatItemUnavailable) || got.Text != "" {
					t.Errorf("%s continuation bypassed %s provenance: %v\n%s", ref.SourceKind, change, err, got.Text)
				}
			}
		})
	}
}
