package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"wails-gui/internal/pii"
	"wails-gui/storage"
)

func recordPrivacyWithdrawals(t *testing.T) func() []privacyWithdrawal {
	t.Helper()
	var events []privacyWithdrawal
	unsubscribe := subscribePrivacyWithdrawals(func(event privacyWithdrawal) {
		events = append(events, event)
	})
	t.Cleanup(unsubscribe)
	return func() []privacyWithdrawal {
		return append([]privacyWithdrawal(nil), events...)
	}
}

func requirePrivacyWithdrawal(t *testing.T, events []privacyWithdrawal, want privacyWithdrawalScope) {
	t.Helper()
	if len(events) != 1 {
		t.Fatalf("withdrawal events=%+v, want exactly one", events)
	}
	if !reflect.DeepEqual(events[0].Scope, want) {
		t.Fatalf("withdrawal scope=%+v, want %+v", events[0].Scope, want)
	}
}

func TestPrivacyWithdrawalPublisherPolicyChanged(t *testing.T) {
	events := recordPrivacyWithdrawals(t)
	scope := privacyWithdrawalScope{Kind: privacyWithdrawalPolicyChange}
	publishPrivacyWithdrawal(scope)
	requirePrivacyWithdrawal(t, events(), scope)
}

func TestPrivacyWithdrawalOnObservationPause(t *testing.T) {
	events := recordPrivacyWithdrawals(t)
	now := time.Date(2026, time.October, 25, 12, 0, 0, 0, time.UTC)
	app := &App{audioScreenLocked: func() bool { return false }}
	app.closedEyes.now = func() time.Time { return now }
	if err := app.SetClosedEyesModeEnabled(true); err != nil {
		t.Fatal(err)
	}
	requirePrivacyWithdrawal(t, events(), privacyWithdrawalScope{Kind: privacyWithdrawalTimeRange, StartMs: now.UnixMilli()})
}

func TestPrivacyWithdrawalOnSensitiveApp(t *testing.T) {
	t.Setenv(topicsStateDirEnvVar, t.TempDir())
	previous := pii.GetUserSensitiveApps()
	t.Cleanup(func() { pii.SetUserSensitiveApps(previous) })
	events := recordPrivacyWithdrawals(t)
	app := &App{}
	if err := app.AddSensitiveApp("com.example.private", "Private"); err != nil {
		t.Fatal(err)
	}
	requirePrivacyWithdrawal(t, events(), privacyWithdrawalScope{Kind: privacyWithdrawalApp, ID: "com.example.private"})
}

func TestPrivacyWithdrawalOnVoiceInputApp(t *testing.T) {
	previous := GetUserVoiceInputApps()
	t.Cleanup(func() { SetUserVoiceInputApps(previous) })
	events := recordPrivacyWithdrawals(t)
	app := &App{}
	if err := app.AddVoiceInputApp("com.example.dictation", "Dictation"); err != nil {
		t.Fatal(err)
	}
	requirePrivacyWithdrawal(t, events(), privacyWithdrawalScope{Kind: privacyWithdrawalApp, ID: "com.example.dictation"})
}

func TestPrivacyWithdrawalOnProtectedSession(t *testing.T) {
	store := newTestStore(t)
	insertTestSession(t, store, "protected-session", "done", 1_000)
	events := recordPrivacyWithdrawals(t)
	if err := store.UpdateTriageStatus("protected-session", "protected"); err != nil {
		t.Fatal(err)
	}
	requirePrivacyWithdrawal(t, events(), privacyWithdrawalScope{Kind: privacyWithdrawalSession, ID: "protected-session"})
}

func TestPrivacyWithdrawalOnTimedPauseReplacement(t *testing.T) {
	app := newBindingsTestApp(t)
	now := time.Date(2026, time.October, 25, 12, 0, 0, 0, time.UTC)
	app.closedEyes.now = func() time.Time { return now }
	if err := app.PauseObservationFor(60); err != nil {
		t.Fatal(err)
	}
	events := recordPrivacyWithdrawals(t)
	if err := app.PauseObservationFor(120); err != nil {
		t.Fatal(err)
	}
	requirePrivacyWithdrawal(t, events(), privacyWithdrawalScope{Kind: privacyWithdrawalTimeRange, StartMs: now.UnixMilli(), EndMs: now.Add(2 * time.Hour).UnixMilli()})
}

func TestPrivacyWithdrawalOnSourceDeletion(t *testing.T) {
	app := newMobileBriefPurposeTestApp(t)
	if err := app.activity.store.InsertActivity(ActivityRow{ID: "deleted-source"}); err != nil {
		t.Fatal(err)
	}
	events := recordPrivacyWithdrawals(t)
	if err := app.DeleteActivity("deleted-source"); err != nil {
		t.Fatal(err)
	}
	requirePrivacyWithdrawal(t, events(), privacyWithdrawalScope{Kind: privacyWithdrawalSource, SourceKind: "activity", ID: "deleted-source"})
}

func TestPrivacyWithdrawalOnFileSourceDeletion(t *testing.T) {
	store := newTestStore(t)
	_, err := store.UpsertFileActivity([]FileActivityRow{{ID: "deleted-file", Name: "notes", Folder: "docs", ActiveAt: 1_000, Action: "opened", FirstSeenAt: 1_000}}, 1_000)
	if err != nil {
		t.Fatal(err)
	}
	events := recordPrivacyWithdrawals(t)
	if _, err := store.DeleteFileActivityIDs([]string{"deleted-file"}); err != nil {
		t.Fatal(err)
	}
	requirePrivacyWithdrawal(t, events(), privacyWithdrawalScope{Kind: privacyWithdrawalSource, SourceKind: "file", ID: "file:deleted-file"})
}

func TestPrivacyWithdrawalOnPrivateFolder(t *testing.T) {
	store := newTestStore(t)
	_, err := store.UpsertFileActivity([]FileActivityRow{{ID: "private-file", Name: "notes", Folder: "docs", DirHashes: " folder-hash ", ActiveAt: 1_000, Action: "opened", FirstSeenAt: 1_000}}, 1_000)
	if err != nil {
		t.Fatal(err)
	}
	events := recordPrivacyWithdrawals(t)
	if _, err := store.DeleteFileActivityUnder("folder-hash"); err != nil {
		t.Fatal(err)
	}
	requirePrivacyWithdrawal(t, events(), privacyWithdrawalScope{Kind: privacyWithdrawalSource, SourceKind: "file", ID: "file:private-file"})
}

func TestPrivacyWithdrawalOnMemoryDeletion(t *testing.T) {
	app, dir := newForgetTestApp(t)
	const id = "deleted-memory"
	if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte("# Private memory\n"), 0600); err != nil {
		t.Fatal(err)
	}
	events := recordPrivacyWithdrawals(t)
	if err := app.DeleteMemoryEntry(id); err != nil {
		t.Fatal(err)
	}
	requirePrivacyWithdrawal(t, events(), privacyWithdrawalScope{Kind: privacyWithdrawalSource, SourceKind: "memory", ID: id})
}

func TestPrivacyWithdrawalOnMemoryForget(t *testing.T) {
	app, dir := newForgetTestApp(t)
	const id = "forgotten-memory"
	if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte("# Private memory\n"), 0600); err != nil {
		t.Fatal(err)
	}
	preview, err := app.GetMemoryForgetPreview(id)
	if err != nil {
		t.Fatal(err)
	}
	events := recordPrivacyWithdrawals(t)
	if err := app.ForgetMemoryEntry(id, preview.ConfirmationToken); err != nil {
		t.Fatal(err)
	}
	requirePrivacyWithdrawal(t, events(), privacyWithdrawalScope{Kind: privacyWithdrawalSource, SourceKind: "memory", ID: id})
}

func TestPrivacyWithdrawalOnKnowledgeDeletion(t *testing.T) {
	app := newMobileBriefPurposeTestApp(t)
	dir, err := knowledgeDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "deleted-knowledge.md"), []byte("# Private knowledge\n"), 0600); err != nil {
		t.Fatal(err)
	}
	events := recordPrivacyWithdrawals(t)
	if err := app.DeleteKnowledgeEntry("deleted-knowledge"); err != nil {
		t.Fatal(err)
	}
	requirePrivacyWithdrawal(t, events(), privacyWithdrawalScope{Kind: privacyWithdrawalSource, SourceKind: "knowledge", ID: "deleted-knowledge"})
}

func TestPrivacyWithdrawalOnPurposeForget(t *testing.T) {
	app := newMobileBriefPurposeTestApp(t)
	goal, err := app.createUserPurpose(userPurposeCreate{Key: "privacy-withdrawal-goal", Origin: "test", Kind: PurposeKindGoal, Text: "Private goal"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	events := recordPrivacyWithdrawals(t)
	if _, err := app.moveUserPurpose(userPurposeTransition{Key: "privacy-withdrawal-forget", Origin: "test", OpenLoopID: goal.OpenLoop.ID, ExpectedEventID: goal.Current.ID, Action: userPurposeForget}, time.Now()); err != nil {
		t.Fatal(err)
	}
	requirePrivacyWithdrawal(t, events(), privacyWithdrawalScope{Kind: privacyWithdrawalSource, SourceKind: "purpose", ID: goal.OpenLoop.ID})
}

func TestPrivacyWithdrawalOnSessionDeletion(t *testing.T) {
	store := newTestStore(t)
	insertTestSession(t, store, "deleted-session", "done", 1_000)
	events := recordPrivacyWithdrawals(t)
	if err := store.DeleteSession("deleted-session"); err != nil {
		t.Fatal(err)
	}
	requirePrivacyWithdrawal(t, events(), privacyWithdrawalScope{Kind: privacyWithdrawalSession, ID: "deleted-session"})
}

func savePrivacyWithdrawalChat(t *testing.T, app *App, localID, projectID, workspaceID string) {
	t.Helper()
	if err := app.store.SaveProject(t.Context(), storage.ProjectRow{ID: projectID, Path: filepath.Join(t.TempDir(), projectID)}); err != nil {
		t.Fatal(err)
	}
	if workspaceID != "" {
		if err := app.store.SaveWorkspace(t.Context(), storage.WorkspaceRow{ID: workspaceID, ProjectID: projectID}); err != nil {
			t.Fatal(err)
		}
	}
	if err := app.store.SaveSession(t.Context(), storage.SessionRow{LocalID: localID, ProjectID: projectID, WorkspaceID: workspaceID}); err != nil {
		t.Fatal(err)
	}
}

func TestPrivacyWithdrawalOnChatSessionDeletion(t *testing.T) {
	app := newBindingsTestApp(t)
	savePrivacyWithdrawalChat(t, app, "deleted-chat", "project", "")
	events := recordPrivacyWithdrawals(t)
	if err := app.DeleteSessionData("deleted-chat"); err != nil {
		t.Fatal(err)
	}
	requirePrivacyWithdrawal(t, events(), privacyWithdrawalScope{Kind: privacyWithdrawalSession, ID: "deleted-chat"})
}

func TestPrivacyWithdrawalOnProjectDeletion(t *testing.T) {
	app := newBindingsTestApp(t)
	savePrivacyWithdrawalChat(t, app, "project-chat", "deleted-project", "")
	events := recordPrivacyWithdrawals(t)
	if err := app.DeleteProjectData("deleted-project"); err != nil {
		t.Fatal(err)
	}
	requirePrivacyWithdrawal(t, events(), privacyWithdrawalScope{Kind: privacyWithdrawalSession, ID: "project-chat"})
}

func TestPrivacyWithdrawalOnWorkspaceDeletion(t *testing.T) {
	app := newBindingsTestApp(t)
	savePrivacyWithdrawalChat(t, app, "workspace-chat", "project", "deleted-workspace")
	events := recordPrivacyWithdrawals(t)
	if err := app.DeleteWorkspaceData("deleted-workspace"); err != nil {
		t.Fatal(err)
	}
	requirePrivacyWithdrawal(t, events(), privacyWithdrawalScope{Kind: privacyWithdrawalSession, ID: "workspace-chat"})
}

func TestPrivacyWithdrawalOnPartialAccountSignOut(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	events := recordPrivacyWithdrawals(t)
	app := &App{}
	if err := app.DisconnectFeishu("lark"); err != nil {
		t.Fatal(err)
	}
	requirePrivacyWithdrawal(t, events(), privacyWithdrawalScope{Kind: privacyWithdrawalAccount, ID: "feishu:lark"})
}

func TestOrdinaryCaptureAndComposerMaintenanceDoNotPublishPrivacyWithdrawal(t *testing.T) {
	app := newMobileBriefPurposeTestApp(t)
	store := app.activity.store
	events := recordPrivacyWithdrawals(t)
	err := store.SaveSession(ActivitySessionRow{ID: "ordinary-capture", StartTime: 1_000, EndTime: 2_000}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InsertActivity(ActivityRow{ID: "composer-maintenance"}); err != nil {
		t.Fatal(err)
	}
	if err := app.deleteActivityWithTopicScrub(store, "composer-maintenance"); err != nil {
		t.Fatal(err)
	}
	if got := events(); len(got) != 0 {
		t.Fatalf("ordinary capture or composer maintenance published withdrawals: %+v", got)
	}
}
