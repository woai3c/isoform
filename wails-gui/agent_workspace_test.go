package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	agenttools "yansu-agent/agent/tools"
)

func shortGuardTestHome(t *testing.T) string {
	t.Helper()
	base := os.TempDir()
	if runtime.GOOS == "darwin" {
		base = "/tmp"
	}
	home, err := os.MkdirTemp(base, "iso-g5-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	home, err = filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("YANSU_AGENT_HOME", "")
	return home
}

func TestAgentWorkspacesStayOutsidePrivateData(t *testing.T) {
	home := shortGuardTestHome(t)
	t.Setenv("YANSU_AGENT_HOME", "")
	for _, flavor := range []string{appFlavorIsoform, appFlavorYansu} {
		t.Run(flavor, func(t *testing.T) {
			setAgentWorkspaceTestFlavor(t, flavor)
			app := &App{}
			chat := app.getOrCreateSessionTempDir("chat-1")
			automation, err := app.defaultAutomationWorkDir("job-1")
			if err != nil {
				t.Fatal(err)
			}
			for kind, dir := range map[string]string{"chats": chat, "automations": automation} {
				id := "chat-1"
				if kind == "automations" {
					id = "job-1"
				}
				want := filepath.Join(home, ".isoform-workspaces", flavor, kind, id)
				if dir != want {
					t.Fatalf("%s workspace = %q, want %q", kind, dir, want)
				}
				if err := validateCronProjectDir(dir); err != nil {
					t.Fatalf("workspace is private: %v", err)
				}
				report := filepath.Join(dir, "report.txt")
				call := agenttools.ToolCall{WorkDir: dir, Input: map[string]any{"path": report, "content": "workspace-report"}}
				if _, err := agenttools.NewWriteTool(nil).Execute(context.Background(), call); err != nil {
					t.Fatalf("write in workspace: %v", err)
				}
				result, err := agenttools.NewBashTool().Execute(context.Background(), agenttools.ToolCall{
					WorkDir: dir, Input: map[string]any{"command": "cat report.txt"},
				})
				if err != nil || result.Metadata["exit_code"] != 0 || result.Content != "workspace-report" {
					t.Fatalf("Bash in workspace: result=%+v err=%v", result, err)
				}
				files, skipped := readDeliverableFiles([]string{report}, dir, dir, touchedBy(t, report))
				if len(files) != 1 || len(skipped) != 0 {
					t.Fatalf("workspace upload: files=%v skipped=%v", files, skipped)
				}
			}
		})
	}
}

func TestAgentWorkspacesFailClosedWithoutHome(t *testing.T) {
	fallbackRoot := shortGuardTestHome(t)
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	t.Setenv("TMPDIR", fallbackRoot)
	t.Setenv("TMP", fallbackRoot)
	t.Setenv("TEMP", fallbackRoot)
	app := &App{}
	if _, err := app.chatSessionWorkDir("chat-1"); err == nil {
		t.Fatal("chat workspace accepted unknown HOME")
	}
	if _, err := app.defaultAutomationWorkDir("job-1"); err == nil {
		t.Fatal("automation workspace accepted unknown HOME")
	}
	dir := app.getOrCreateSessionTempDir("chat-1")
	if dir == "" || filepath.Dir(dir) != fallbackRoot {
		t.Fatalf("unknown HOME fallback workspace = %q, want directory under %q", dir, fallbackRoot)
	}
	if again := app.getOrCreateSessionTempDir("chat-1"); again != dir {
		t.Fatalf("fallback not reused: got %q, want %q", again, dir)
	}
	app.cleanupFallbackSessionDirsOnShutdown()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("fallback not cleaned: %v", err)
	}
}

func TestAgentWorkspaceMigrationDoesNotBlockAppMutex(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		t.Run(fmt.Sprintf("shutdown=%v", shutdown), func(t *testing.T) {
			home := shortGuardTestHome(t)
			setAgentWorkspaceTestFlavor(t, appFlavorIsoform)
			const tag = "migration-concurrency"
			writeWorkspaceMigrationFile(t, filepath.Join(home, ".isoform-agent", "chats", tag, "report.txt"), "legacy report")
			app := &App{}
			workspace, err := app.chatSessionWorkDir(tag)
			if err != nil {
				t.Fatal(err)
			}
			// Pause real migration before the copy; directory creation shows the
			// getter has reached prepareAgentWorkspace, without timing a large file.
			agentWorkspaceMigrationMu.Lock()
			var release sync.Once
			unlock := func() { release.Do(agentWorkspaceMigrationMu.Unlock) }
			var workers sync.WaitGroup
			defer func() {
				unlock()
				workers.Wait()
			}()
			created := make(chan string, 1)
			workers.Add(1)
			go func() {
				defer workers.Done()
				created <- app.getOrCreateSessionTempDir(tag)
			}()
			deadline := time.Now().Add(30 * time.Second)
			for {
				if info, err := os.Stat(workspace); err == nil && info.IsDir() {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("workspace creation did not reach the paused migration")
				}
				time.Sleep(time.Millisecond)
			}
			responsive := make(chan struct{})
			workers.Add(1)
			go func() {
				defer workers.Done()
				app.mu.Lock()
				app.pendingTraceIDs = map[string]string{tag: "unrelated operation"}
				app.mu.Unlock()
				close(responsive)
			}()
			select {
			case <-responsive:
			case <-time.After(30 * time.Second):
				t.Fatal("workspace migration held the app-wide mutex")
			}
			cleanupStarted, cleaned := make(chan struct{}), make(chan struct{})
			workers.Add(1)
			go func() {
				defer workers.Done()
				close(cleanupStarted)
				if shutdown {
					app.cleanupFallbackSessionDirsOnShutdown()
				} else {
					app.CleanupSessionTempDir(tag)
				}
				close(cleaned)
			}()
			waitForSessionWorkspaceContention(t, app, tag, cleanupStarted, cleaned)
			unlock()
			select {
			case got := <-created:
				if got != workspace {
					t.Fatalf("workspace = %q, want %q", got, workspace)
				}
			case <-time.After(30 * time.Second):
				t.Fatal("workspace migration did not complete")
			}
			select {
			case <-cleaned:
			case <-time.After(30 * time.Second):
				t.Fatal("workspace cleanup did not complete")
			}
			app.mu.Lock()
			cached := app.sessionTempDirs[tag]
			_, fallback := app.sessionFallbackDirs[tag]
			app.mu.Unlock()
			if cached != "" || fallback {
				t.Fatalf("creation republished a cache entry after cleanup: %q, fallback=%v", cached, fallback)
			}
			if shutdown {
				if body, err := os.ReadFile(filepath.Join(workspace, "report.txt")); err != nil || string(body) != "legacy report" {
					t.Fatalf("shutdown removed stable migrated report: %q, %v", body, err)
				}
			} else if _, err := os.Stat(workspace); !os.IsNotExist(err) {
				t.Fatalf("session deletion left migrated workspace behind: %v", err)
			}
		})
	}
}

func TestAgentWorkspacesRefusePrivateSymlinkAndTraversal(t *testing.T) {
	home := shortGuardTestHome(t)
	private := filepath.Join(home, ".isoform-agent")
	if err := os.MkdirAll(private, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(private, filepath.Join(home, ".isoform-workspaces")); err != nil {
		t.Fatal(err)
	}
	app := &App{}
	if _, err := app.chatSessionWorkDir("chat-1"); err == nil {
		t.Fatal("chat workspace followed a private-folder symlink")
	}
	if _, err := app.defaultAutomationWorkDir("job-1"); err == nil {
		t.Fatal("automation workspace followed a private-folder symlink")
	}
	for _, id := range []string{"", ".", "..", "../../.ssh", `..\..\.ssh`} {
		if _, err := app.defaultAutomationWorkDir(id); err == nil {
			t.Fatalf("accepted invalid workspace ID %q", id)
		}
	}
}

func TestAgentWorkspacesMoveHistoricalArtifactsOnce(t *testing.T) {
	home := shortGuardTestHome(t)
	for _, flavor := range []string{appFlavorIsoform, appFlavorYansu} {
		for _, kind := range []string{"chats", "automations"} {
			t.Run(flavor+"/"+kind, func(t *testing.T) {
				setAgentWorkspaceTestFlavor(t, flavor)
				legacy := filepath.Join(home, configDirForFlavor(flavor), kind, "session-1")
				oldReport := filepath.Join(legacy, "old-report.txt")
				writeWorkspaceMigrationFile(t, oldReport, "historical report")
				writeWorkspaceMigrationFile(t, filepath.Join(legacy, ".yansu-attachments", "input.txt"), "attachment")
				app := &App{}
				var workspace string
				var err error
				if kind == "chats" {
					workspace = app.getOrCreateSessionTempDir("session-1")
				} else {
					workspace, err = app.defaultAutomationWorkDir("session-1")
				}
				if err != nil || workspace == "" {
					t.Fatalf("new workspace missing: %v", err)
				}
				report := filepath.Join(workspace, "old-report.txt")
				for path, want := range map[string]string{report: "historical report", oldReport: "historical report", filepath.Join(workspace, ".yansu-attachments", "input.txt"): "attachment"} {
					if body, err := os.ReadFile(path); err != nil || string(body) != want {
						t.Fatalf("read %q: %q %v", path, body, err)
					}
				}
				if info, err := os.Lstat(oldReport); runtime.GOOS != "windows" && (err != nil || info.Mode()&os.ModeSymlink == 0) {
					t.Fatalf("legacy bytes not moved to compatibility link: %v", err)
				}
				if path, err := app.resolveDeliverablePath("session-1", oldReport); err != nil || path != oldReport {
					t.Fatalf("native UI lost historical absolute path: %q %v", path, err)
				}
				files, skipped := readDeliverableFiles([]string{oldReport}, workspace, workspace, touchedBy(t, report))
				if len(files) != 1 || len(skipped) != 0 || string(files[0].Data) != "historical report" {
					t.Fatalf("legacy upload path: files=%v skipped=%v", files, skipped)
				}
				writeWorkspaceMigrationFile(t, filepath.Join(legacy, "late.txt"), "not part of original migration")
				if _, err := prepareAgentWorkspace(kind, "session-1"); err != nil {
					t.Fatal(err)
				}
				if _, err := os.Stat(filepath.Join(workspace, "late.txt")); !os.IsNotExist(err) {
					t.Fatalf("completed migration ran again: %v", err)
				}
			})
		}
	}
}

func writeWorkspaceMigrationFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAgentWorkspacesMigrationLeavesPrivateDataInPlace(t *testing.T) {
	home := shortGuardTestHome(t)
	for _, flavor := range []string{appFlavorIsoform, appFlavorYansu} {
		for _, kind := range []string{"chats", "automations"} {
			t.Run(flavor+"/"+kind, func(t *testing.T) {
				setAgentWorkspaceTestFlavor(t, flavor)
				legacy := filepath.Join(home, configDirForFlavor(flavor), kind, "private-fixture")
				private := []string{
					"state.db", "nested/state.db-wal", "nested/cron-secret", "memory.json", "mcp-hub/state.json", "credentials.json", "cron-port",
					"activity/snapshots/frame.png", "activity/segments/recording.mp4", "audio/recording.wav",
					"memory/facts.md", "knowledge/reference.md", "SOUL.md",
				}
				for _, relative := range private {
					writeWorkspaceMigrationFile(t, filepath.Join(legacy, relative), "private fixture")
				}
				writeWorkspaceMigrationFile(t, filepath.Join(legacy, "reports", "report.txt"), "normal report")
				workspace, err := prepareAgentWorkspace(kind, "private-fixture")
				if err != nil {
					t.Fatal(err)
				}
				for _, relative := range private {
					old := filepath.Join(legacy, relative)
					if info, err := os.Lstat(old); err != nil || !info.Mode().IsRegular() {
						t.Errorf("private source replaced or removed at %s: %v", relative, err)
					}
					if body, err := os.ReadFile(old); err != nil || string(body) != "private fixture" {
						t.Errorf("private source changed at %s: %q, %v", relative, body, err)
					}
					if _, err := os.Lstat(filepath.Join(workspace, relative)); !os.IsNotExist(err) {
						t.Errorf("private source moved outside protected data root at %s: %v", relative, err)
					}
				}
				if body, err := os.ReadFile(filepath.Join(workspace, "reports", "report.txt")); err != nil || string(body) != "normal report" {
					t.Fatalf("ordinary report did not migrate: %q, %v", body, err)
				}
			})
		}
	}
}

func TestAgentWorkspacesMigrationPreservesConflictsAndSkipsAliases(t *testing.T) {
	home := shortGuardTestHome(t)
	setAgentWorkspaceTestFlavor(t, appFlavorIsoform)
	legacy := filepath.Join(home, ".isoform-agent", "chats", "chat-1")
	workspace, err := agentWorkspacePath("chats", "chat-1")
	if err != nil {
		t.Fatal(err)
	}
	writeWorkspaceMigrationFile(t, filepath.Join(legacy, "report.txt"), "legacy")
	writeWorkspaceMigrationFile(t, filepath.Join(workspace, "report.txt"), "current")
	writeWorkspaceMigrationFile(t, filepath.Join(legacy, "nested", "keep.txt"), "moved")
	writeWorkspaceMigrationFile(t, filepath.Join(workspace, "nested", "existing.txt"), "existing")
	writeWorkspaceMigrationFile(t, filepath.Join(legacy, "directory-conflict", "old.txt"), "legacy dir")
	writeWorkspaceMigrationFile(t, filepath.Join(workspace, "directory-conflict"), "current file")
	secret := filepath.Join(home, ".ssh", "id_test")
	writeWorkspaceMigrationFile(t, secret, "private")
	if err := os.Symlink(secret, filepath.Join(legacy, "secret-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(secret, filepath.Join(legacy, "secret-hardlink")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(secret), filepath.Join(workspace, "external")); err != nil {
		t.Fatal(err)
	}
	writeWorkspaceMigrationFile(t, filepath.Join(legacy, "external", "report.txt"), "must stay")
	if _, err := prepareAgentWorkspace("chats", "chat-1"); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{
		filepath.Join(legacy, "report.txt"): "legacy", filepath.Join(workspace, "report.txt"): "current",
		filepath.Join(workspace, "nested", "keep.txt"): "moved", filepath.Join(workspace, "nested", "existing.txt"): "existing",
		filepath.Join(legacy, "directory-conflict", "old.txt"): "legacy dir", filepath.Join(workspace, "directory-conflict"): "current file",
		filepath.Join(legacy, "external", "report.txt"): "must stay", secret: "private",
	} {
		if body, err := os.ReadFile(path); err != nil || string(body) != want {
			t.Errorf("read %q: %q %v, want %q", path, body, err, want)
		}
	}
	for _, path := range []string{filepath.Join(workspace, "secret-link"), filepath.Join(workspace, "secret-hardlink"), filepath.Join(filepath.Dir(secret), "report.txt")} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Errorf("migration followed a secret alias at %s: %v", path, err)
		}
	}
}

func TestAgentWorkspacesMigrationDoesNotFollowLegacyRootAliases(t *testing.T) {
	for _, aliasParent := range []bool{false, true} {
		t.Run(fmt.Sprint(aliasParent), func(t *testing.T) {
			home := shortGuardTestHome(t)
			setAgentWorkspaceTestFlavor(t, appFlavorIsoform)
			private := filepath.Join(home, ".ssh")
			writeWorkspaceMigrationFile(t, filepath.Join(private, "id_test"), "private")
			parent := filepath.Join(home, ".isoform-agent", "chats")
			if err := os.MkdirAll(parent, 0o700); err != nil {
				t.Fatal(err)
			}
			link, target := filepath.Join(parent, "chat-1"), private
			if aliasParent {
				if err := os.Remove(parent); err != nil {
					t.Fatal(err)
				}
				writeWorkspaceMigrationFile(t, filepath.Join(private, "chat-1", "id_test"), "private")
				link = parent
			}
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			if _, err := prepareAgentWorkspace("chats", "chat-1"); err == nil {
				t.Fatal("migration accepted an aliased legacy root")
			}
			workspace, err := agentWorkspacePath("chats", "chat-1")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(filepath.Join(workspace, agentWorkspaceMigrationMarker)); !os.IsNotExist(err) {
				t.Fatalf("migration marked an unread legacy root complete: %v", err)
			}
			if _, err := os.Stat(filepath.Join(workspace, "id_test")); !os.IsNotExist(err) {
				t.Fatalf("migration imported a private legacy-root alias: %v", err)
			}
		})
	}
}
