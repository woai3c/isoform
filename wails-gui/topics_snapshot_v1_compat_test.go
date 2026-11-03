package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"wails-gui/internal/pii"
	"wails-gui/storage"
)

const baseSchemaV1SnapshotJSON = `{
  "version": 1,
  "policyHash": [0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0],
  "at": "2026-10-25T00:00:00Z",
  "topics": [
    {
      "topic": {
        "id": "migrated-topic-a",
        "kind": "activity",
        "name": "Migrated topic A",
        "status": "active",
        "pinnedToSidebar": true
      }
    },
    {
      "topic": {
        "id": "referenced-topic-b",
        "kind": "activity",
        "name": "Referenced survivor B",
        "status": "active",
        "pinnedToSidebar": true
      },
      "refs": [
        {"kind": "app", "value": "com.example.survivor"},
        {"kind": "session", "value": "unrelated-survivor-session"}
      ]
    }
  ]
}`

type baseSchemaV1Fixture struct {
	app     *App
	service *topicsService
	dir     string
}

func newBaseSchemaV1Fixture(t *testing.T) baseSchemaV1Fixture {
	t.Helper()
	previousSensitiveApps := pii.GetUserSensitiveApps()
	t.Cleanup(func() { pii.SetUserSensitiveApps(previousSensitiveApps) })
	pii.SetUserSensitiveApps(map[string]string{})

	dir := t.TempDir()
	t.Setenv(topicsStateDirEnvVar, dir)
	if _, err := newTopicsStore(dir).update(func(file *topicsStateFile) {
		file.SidebarPins = map[string]bool{migratedTopicID: true, survivorTopicID: true}
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, topicSnapshotFileName), []byte(baseSchemaV1SnapshotJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	app := newPrivacyReadTestApp(t)
	service := app.topicsSvc()
	t.Cleanup(func() {
		if service.privacyWithdrawalUnsubscribe != nil {
			service.privacyWithdrawalUnsubscribe()
			service.privacyWithdrawalUnsubscribe = nil
		}
	})
	snapshot := service.snapshots.Current()
	if snapshot == nil || len(snapshot.Topics) != 2 ||
		snapshot.Topics[0].Topic.ID != migratedTopicID || snapshot.Topics[1].Topic.ID != survivorTopicID {
		t.Fatalf("base schema-v1 snapshot = %+v", snapshot)
	}
	return baseSchemaV1Fixture{app: app, service: service, dir: dir}
}

func TestTopicSnapshotV1LoadNormalizesUnreferencedEntries(t *testing.T) {
	fixture := newBaseSchemaV1Fixture(t)
	snapshot := fixture.service.snapshots.Current()
	if !snapshot.Topics[0].Unproven {
		t.Error("legacy unreferenced entry was not normalized as unproven")
	}
	if snapshot.Topics[1].Unproven {
		t.Error("referenced survivor was marked unproven")
	}
}

func TestTopicSnapshotV1AppWithdrawalRemovesUnreferencedEntry(t *testing.T) {
	fixture := newBaseSchemaV1Fixture(t)
	if err := fixture.app.AddSensitiveApp(migratedAppID, "Code"); err != nil {
		t.Fatal(err)
	}

	assertMigratedTopicReadPaths(t, fixture.app)
	assertMigratedSnapshotIDs(t, fixture.dir, false)
}

func TestTopicSnapshotV1SessionDeletionRemovesUnreferencedEntry(t *testing.T) {
	fixture := newBaseSchemaV1Fixture(t)
	main, err := fixture.app.ensureMainChat(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.app.store.SaveSession(t.Context(), storage.SessionRow{
		LocalID: migratedSessionID, ProjectID: main.ProjectID, WorkspaceID: main.WorkspaceID,
		Label: "Migrated source",
	}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.app.DeleteSessionData(migratedSessionID); err != nil {
		t.Fatal(err)
	}

	assertMigratedTopicReadPaths(t, fixture.app)
	assertMigratedSnapshotIDs(t, fixture.dir, false)
}

func TestTopicSnapshotV1PolicyMismatchIsHiddenBeforeStartupBuild(t *testing.T) {
	newBaseSchemaV1Fixture(t)
	pii.SetUserSensitiveApps(map[string]string{migratedAppID: "Code"})

	restartedApp := newPrivacyReadTestApp(t)
	service := restartedApp.topicsSvc()
	t.Cleanup(func() {
		if service.privacyWithdrawalUnsubscribe != nil {
			service.privacyWithdrawalUnsubscribe()
			service.privacyWithdrawalUnsubscribe = nil
		}
	})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	beforeSnapshot := make(chan struct{})
	service.registryBeforeSnapshot = func() {
		close(beforeSnapshot)
		<-ctx.Done()
	}
	buildDone := make(chan struct{})
	go func() {
		defer close(buildDone)
		_, _ = service.registryViews(ctx)
	}()
	select {
	case <-beforeSnapshot:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("registry build did not reach the pre-snapshot hold")
	}

	assertMigratedTopicReadPaths(t, restartedApp)

	cancel()
	select {
	case <-buildDone:
	case <-time.After(5 * time.Second):
		t.Fatal("held registry build did not stop after cancellation")
	}
}

func TestTopicSnapshotV1FirstBuildReplacesUnreferencedEntry(t *testing.T) {
	fixture := newBaseSchemaV1Fixture(t)
	published := fixture.service.snapshots.Current()
	candidate := &topicSnapshot{
		Version:    published.Version,
		PolicyHash: published.PolicyHash,
		At:         published.At,
		Topics: []topicSnapshotEntry{
			{
				Topic: published.Topics[0].Topic,
				Refs: []topicSourceReference{
					{Kind: topicRefApp, Value: migratedAppID},
					{Kind: topicRefSession, Value: migratedSessionID},
				},
			},
			published.Topics[1],
		},
	}
	if !fixture.service.commitBuiltSnapshot(candidate, true, false) {
		t.Fatal("first build did not replace the unreferenced entry")
	}

	current := fixture.service.snapshots.Current()
	if current == nil || len(current.Topics) != 2 || len(current.Topics[0].Refs) == 0 ||
		current.Topics[0].Unproven {
		t.Errorf("published snapshot did not replace the legacy entry: %+v", current)
	}
}
