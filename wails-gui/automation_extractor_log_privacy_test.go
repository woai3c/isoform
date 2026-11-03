package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"wails-gui/storage"
)

func TestAutomationExtractionRejectionLogsKeepContentPrivate(t *testing.T) {
	for _, flag := range []string{"unset", "0", "1"} {
		for _, site := range []string{"missing_fields", "placeholder", "missing_schedule", "invalid_schedule", "unavailable_store", "duplicate_cron"} {
			t.Run(site+"/debug="+flag, func(t *testing.T) {
				setContentLogDebugFlag(t, flag)
				output, _ := isolateActivityPrivacyLogs(t)
				const title = "private-extraction-title-amber"
				const detail = "PRIVATE_EXTRACTOR_DETAIL"
				spec := initiativeAutomationSpec{name: title, description: "private task", prompt: "Run the private task", scheduleKind: "manual", scheduleConfig: json.RawMessage(`{}`)}
				app := &App{}
				secrets := []string{title}
				prefix := "[initiative] skipping"
				switch site {
				case "missing_fields":
					spec.prompt = ""
				case "placeholder":
					spec.prompt = "Run [" + detail + "]"
					secrets = append(secrets, detail)
				case "missing_schedule":
					spec.scheduleKind, spec.scheduleConfig = detail, nil
					secrets = append(secrets, detail)
				case "invalid_schedule":
					spec.scheduleKind, spec.scheduleConfig = "cron", json.RawMessage(`{"expr":"`+detail+`"}`)
					secrets = append(secrets, detail)
				case "unavailable_store":
					prefix = "[initiative] cannot shadow"
				case "duplicate_cron":
					app = newActivityTranslationTestApp(t, &activityMarkdownTranslationFakeRunner{})
					app.activity = &activityMonitorState{store: newTestStore(t)}
					target, err := json.Marshal(map[string]string{"prompt": spec.prompt, "project_dir": ""})
					if err != nil {
						t.Fatal(err)
					}
					job := storage.CronJobRow{ID: "existing-job", Name: detail, ScheduleKind: "manual", ScheduleConfig: `{}`, TargetKind: "agent", TargetConfig: string(target), Fingerprint: computeAutomationFingerprint(spec.description, "agent", string(target))}
					if err := app.store.SaveCronJob(context.Background(), job); err != nil {
						t.Fatal(err)
					}
					secrets = append(secrets, detail)
				}
				result := app.createShadowInitiativeFromTriage(context.Background(), spec, "wechat", "source-test", "", "")
				if result.Status != "skipped" && result.Status != "error" {
					t.Fatalf("expected rejection, got %+v", result)
				}
				line := contentLogLine(t, output.String(), prefix)
				assertActivityLogContentVisibility(t, line, flag == "1", secrets...)
				if !strings.Contains(line, "_bytes=") {
					t.Fatalf("missing safe diagnostic size: %s", line)
				}
				// Redaction applies only to diagnostics, not to the caller's result.
				if result.Reason == "" {
					t.Fatal("rejection lost its canonical reason")
				}
			})
		}
	}
}

func TestAutomationExtractionPersistenceLogsKeepTitlesPrivate(t *testing.T) {
	for _, flag := range []string{"unset", "0", "1"} {
		for _, site := range []string{"duplicate_active", "add_error", "duplicate_observation", "promoted", "observed_repeat", "save_failure", "shadowed_initial", "observed_initial"} {
			t.Run(site+"/debug="+flag, func(t *testing.T) {
				setContentLogDebugFlag(t, flag)
				output, _ := isolateActivityPrivacyLogs(t)
				store := newTestStore(t)
				app := &App{activity: &activityMonitorState{store: store}}
				const title = "private-persistence-title-amber"
				const detail = "private-persistence-detail-amber"
				item := newInitiativeFixture("candidate", "candidate-source", InitiativeStatusObserved)
				item.Title, item.Confidence, item.Fingerprint = title, 0.1, "private-log-fingerprint"
				prefix, wantStatus, immediate := "[initiative] observed", "observed", false
				secrets := []string{title}
				if site != "save_failure" && site != "shadowed_initial" && site != "observed_initial" {
					existing := item
					existing.ID, existing.SourceID = "existing", "existing-source"
					if site == "duplicate_active" {
						existing.Status, existing.Title = InitiativeStatusShadowed, detail
					}
					if err := store.SaveInitiative(existing); err != nil {
						t.Fatal(err)
					}
				}
				switch site {
				case "duplicate_active":
					prefix, wantStatus = "[initiative] skipping", "skipped"
					secrets = append(secrets, detail)
				case "add_error":
					prefix, wantStatus = "[initiative] failed to observe", "error"
					if _, err := store.db.Exec(`CREATE TRIGGER privacy_observe_failure BEFORE INSERT ON initiative_observations BEGIN SELECT RAISE(ABORT, 'database is locked: private-persistence-detail-amber'); END`); err != nil {
						t.Fatal(err)
					}
					secrets = append(secrets, detail)
				case "duplicate_observation":
					item.SourceID = "existing-source"
					prefix, wantStatus = "[initiative] skipping", "skipped"
				case "promoted":
					prefix, wantStatus, immediate = "[initiative] promoted", "shadowed", true
				case "save_failure":
					prefix, wantStatus = "[initiative] failed to save", "error"
					if _, err := store.db.Exec(`CREATE TRIGGER privacy_save_failure BEFORE INSERT ON initiatives BEGIN SELECT RAISE(ABORT, 'database is locked: private-persistence-detail-amber'); END`); err != nil {
						t.Fatal(err)
					}
					secrets = append(secrets, detail)
				case "shadowed_initial":
					item.Status = InitiativeStatusShadowed
					prefix, wantStatus, immediate = "[initiative] shadowed", "shadowed", true
				}
				result := app.persistShadowInitiative(item, "manual", immediate)
				if result.Status != wantStatus {
					t.Fatalf("status=%s, want %s: %+v", result.Status, wantStatus, result)
				}
				line := contentLogLine(t, output.String(), prefix)
				assertActivityLogContentVisibility(t, line, flag == "1", secrets...)
				if !strings.Contains(line, "title_bytes=") {
					t.Fatalf("missing title size: %s", line)
				}
				if wantStatus == "error" && !strings.Contains(line, "error_kind=locked") {
					t.Fatalf("missing actionable error kind: %s", line)
				}
				if wantStatus == "observed" || wantStatus == "shadowed" {
					saved, err := store.GetInitiative(result.InitiativeID)
					if err != nil || saved.Title != title {
						t.Fatalf("logging changed canonical title: saved=%+v error=%v", saved, err)
					}
				}
			})
		}
	}
}

func TestAutomationDedupFailureLogsKeepErrorsPrivate(t *testing.T) {
	for _, flag := range []string{"unset", "0", "1"} {
		t.Run("debug="+flag, func(t *testing.T) {
			setContentLogDebugFlag(t, flag)
			output, _ := isolateActivityPrivacyLogs(t)
			app := newActivityTranslationTestApp(t, &activityMarkdownTranslationFakeRunner{})
			if err := app.store.Close(); err != nil {
				t.Fatal(err)
			}
			app.buildAutomationDedupSection("")
			line := contentLogLine(t, output.String(), "[initiative] failed to load cron jobs for dedup")
			assertActivityLogContentVisibility(t, line, flag == "1", "database is closed")
			if !strings.Contains(line, "error_bytes=") || !strings.Contains(line, "error_kind=") {
				t.Fatalf("missing error metadata: %s", line)
			}
		})
	}
}
