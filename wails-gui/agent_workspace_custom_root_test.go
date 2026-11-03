package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"wails-gui/storage"
)

func setAgentWorkspaceTestFlavor(t *testing.T, flavor string) {
	t.Helper()
	t.Setenv(appFlavorEnv, flavor)
	previous := configDirName
	configDirName = configDirForFlavor(flavor)
	t.Cleanup(func() { configDirName = previous })
}

func TestAgentWorkspaceMigrationHonorsCustomDataRoot(t *testing.T) {
	home := shortGuardTestHome(t)
	setAgentWorkspaceTestFlavor(t, appFlavorIsoform)
	dataRoot := filepath.Join(home, "custom-data")
	t.Setenv(agentHomeEnv, dataRoot)
	const chatTag, jobID = "old-chat", "old-automation"
	legacyChat := filepath.Join(dataRoot, "chats", chatTag, "chat.txt")
	legacyReport := filepath.Join(dataRoot, "automations", jobID, "report.md")
	writeWorkspaceMigrationFile(t, legacyChat, "historical chat artifact")
	writeWorkspaceMigrationFile(t, legacyReport, "# Historical automation report\n")
	// The explicit root owns these artifacts; the flavor default is unrelated.
	defaultReport := filepath.Join(home, configDirName, "automations", jobID, "report.md")
	writeWorkspaceMigrationFile(t, defaultReport, "leave the default root alone")
	store, err := storage.New(filepath.Join(home, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.SaveCronJob(context.Background(), storage.CronJobRow{
		ID: jobID, Name: "Historical report", TargetKind: "agent", TargetConfig: `{}`,
	}); err != nil {
		t.Fatal(err)
	}
	app := &App{store: store}
	chatDir := app.getOrCreateSessionTempDir(chatTag)
	if body, err := os.ReadFile(filepath.Join(chatDir, "chat.txt")); err != nil || string(body) != "historical chat artifact" {
		t.Errorf("custom-root chat did not migrate: %q, %v", body, err)
	}
	// Exercise the exact resolver used by OpenAutomationDeliverable, then
	// open the real file without launching a GUI application.
	report, err := app.resolveAutomationDeliverablePath(jobID, "report.md")
	if err != nil {
		t.Fatalf("resolve historical automation deliverable: %v", err)
	}
	file, err := os.Open(report)
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(file)
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || string(body) != "# Historical automation report\n" {
		t.Fatalf("open migrated deliverable: %q, read=%v close=%v", body, readErr, closeErr)
	}
	for path, want := range map[string]string{
		legacyChat: "historical chat artifact", legacyReport: "# Historical automation report\n",
		defaultReport: "leave the default root alone",
	} {
		if body, err := os.ReadFile(path); err != nil || string(body) != want {
			t.Errorf("historical path %s changed: %q, %v", path, body, err)
		}
	}
}

func TestAgentWorkspaceMigrationRetriesUnreadableCustomSource(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission-denied source fixture requires Unix directory permissions")
	}
	home := shortGuardTestHome(t)
	setAgentWorkspaceTestFlavor(t, appFlavorIsoform)
	dataRoot := filepath.Join(home, "custom-data")
	t.Setenv(agentHomeEnv, dataRoot)
	legacy := filepath.Join(dataRoot, "chats", "retry-chat")
	writeWorkspaceMigrationFile(t, filepath.Join(legacy, "report.txt"), "retry after source is readable")
	if err := os.Chmod(legacy, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(legacy, 0o700) })
	if _, err := os.ReadDir(legacy); !os.IsPermission(err) {
		t.Skipf("host does not enforce unreadable fixture permissions: %v", err)
	}
	if _, err := prepareAgentWorkspace("chats", "retry-chat"); err == nil {
		t.Error("unreadable custom source was accepted as migrated")
	}
	destination, err := agentWorkspacePath("chats", "retry-chat")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(destination, agentWorkspaceMigrationMarker)); !os.IsNotExist(err) {
		t.Errorf("unreadable source was marked complete: %v", err)
	}
	if err := os.Chmod(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareAgentWorkspace("chats", "retry-chat"); err != nil {
		t.Fatal(err)
	}
	if body, err := os.ReadFile(filepath.Join(destination, "report.txt")); err != nil || string(body) != "retry after source is readable" {
		t.Fatalf("migration did not retry the recovered source: %q, %v", body, err)
	}
}

func TestAgentWorkspaceMigrationDoesNotCompleteForDanglingSource(t *testing.T) {
	for _, missingPart := range []string{"data-root", "chats", "tag"} {
		t.Run(missingPart, func(t *testing.T) {
			home := shortGuardTestHome(t)
			setAgentWorkspaceTestFlavor(t, appFlavorIsoform)
			dataRoot := filepath.Join(home, "custom-data")
			t.Setenv(agentHomeEnv, dataRoot)
			legacy := filepath.Join(dataRoot, "chats", "retry-chat")
			link := dataRoot
			switch missingPart {
			case "chats":
				link = filepath.Dir(legacy)
			case "tag":
				link = legacy
			}
			if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(home, "temporarily-unavailable"), link); err != nil {
				t.Fatal(err)
			}
			if _, err := prepareAgentWorkspace("chats", "retry-chat"); err == nil {
				t.Error("dangling source was accepted as permanently absent")
			}
			destination, err := agentWorkspacePath("chats", "retry-chat")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(filepath.Join(destination, agentWorkspaceMigrationMarker)); !os.IsNotExist(err) {
				t.Errorf("unresolved source was marked complete: %v", err)
			}
			if err := os.Remove(link); err != nil {
				t.Fatal(err)
			}
			writeWorkspaceMigrationFile(t, filepath.Join(legacy, "report.txt"), "recovered source")
			if _, err := prepareAgentWorkspace("chats", "retry-chat"); err != nil {
				t.Fatal(err)
			}
			if body, err := os.ReadFile(filepath.Join(destination, "report.txt")); err != nil || string(body) != "recovered source" {
				t.Fatalf("repaired source did not migrate: %q, %v", body, err)
			}
		})
	}
}
