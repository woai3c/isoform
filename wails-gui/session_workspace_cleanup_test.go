package main

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestSessionWorkspaceCleanupDoesNotBlockOtherTags(t *testing.T) {
	for _, mode := range []string{"delete", "shutdown"} {
		t.Run(mode, func(t *testing.T) {
			home := shortGuardTestHome(t)
			t.Setenv(appFlavorEnv, appFlavorIsoform)
			const tag = "cleanup-target"
			app := &App{}
			oldDir := app.getOrCreateSessionTempDir(tag)
			if mode == "shutdown" {
				oldDir = filepath.Join(home, "fallback")
				app.sessionTempDirs[tag] = oldDir
				app.sessionFallbackDirs = map[string]struct{}{tag: {}}
			}
			writeWorkspaceMigrationFile(t, filepath.Join(oldDir, "old-report.txt"), "old report")
			removing := make(chan struct{})
			release := make(chan struct{})
			var started, released sync.Once
			app.sessionWorkDirRemoveAll = func(dir string) error {
				if dir == oldDir {
					started.Do(func() { close(removing) })
					<-release
				}
				return os.RemoveAll(dir)
			}
			var workers sync.WaitGroup
			defer func() {
				released.Do(func() { close(release) })
				workers.Wait()
			}()
			cleaned := make(chan struct{})
			workers.Add(1)
			go func() {
				defer workers.Done()
				if mode == "shutdown" {
					app.cleanupFallbackSessionDirsOnShutdown()
				} else {
					app.CleanupSessionTempDir(tag)
				}
				close(cleaned)
			}()
			select {
			case <-removing:
			case <-time.After(30 * time.Second):
				t.Fatal("cleanup did not reach directory removal")
			}
			sameTag, otherTag := make(chan string, 1), make(chan string, 1)
			recreationStarted, recreationFinished := make(chan struct{}), make(chan struct{})
			workers.Add(2)
			go func() {
				defer workers.Done()
				close(recreationStarted)
				sameTag <- app.getOrCreateSessionTempDir(tag)
				close(recreationFinished)
			}()
			go func() {
				defer workers.Done()
				otherTag <- app.getOrCreateSessionTempDir("unrelated-chat")
			}()
			var otherDir string
			select {
			case otherDir = <-otherTag:
				if otherDir == "" {
					t.Fatal("unrelated chat did not get a workspace")
				}
			case <-time.After(30 * time.Second):
				t.Fatal("directory removal blocked an unrelated new chat")
			}
			waitForSessionWorkspaceContention(t, app, tag, recreationStarted, recreationFinished)
			released.Do(func() { close(release) })
			select {
			case <-cleaned:
			case <-time.After(30 * time.Second):
				t.Fatal("cleanup did not finish after directory removal resumed")
			}
			var recreated string
			select {
			case recreated = <-sameTag:
			case <-time.After(30 * time.Second):
				t.Fatal("same-tag recreation did not resume after cleanup")
			}
			if recreated == "" {
				t.Fatal("same-tag recreation returned an empty workspace")
			}
			if _, err := os.Stat(filepath.Join(oldDir, "old-report.txt")); !os.IsNotExist(err) {
				t.Fatalf("old workspace contents survived cleanup: %v", err)
			}
			for tag, dir := range map[string]string{tag: recreated, "unrelated-chat": otherDir} {
				if info, err := os.Stat(dir); err != nil || !info.IsDir() {
					t.Errorf("new workspace %s was removed: %v", tag, err)
				}
				app.mu.Lock()
				cached := app.sessionTempDirs[tag]
				app.mu.Unlock()
				if cached != dir {
					t.Errorf("new workspace %s lost its cache entry: got %q, want %q", tag, cached, dir)
				}
			}
		})
	}
}

// Prove the contender entered the per-tag lock operation while the holder is
// explicitly paused. A short absence of completion does not prove contention:
// the goroutine may simply not have been scheduled yet.
func waitForSessionWorkspaceContention(t *testing.T, app *App, tag string, started, finished <-chan struct{}) {
	t.Helper()
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	select {
	case <-started:
	case <-deadline.C:
		t.Fatal("workspace contender was not scheduled")
	}
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		app.sessionWorkDirMu.Lock()
		entry := app.sessionWorkDirLocks[sanitizeSessionTag(tag)]
		contending := entry != nil && entry.refCount >= 2
		app.sessionWorkDirMu.Unlock()
		select {
		case <-finished:
			t.Fatal("workspace operation finished while the same-tag holder was paused")
		default:
		}
		if contending {
			return
		}
		select {
		case <-finished:
			t.Fatal("workspace operation skipped same-tag exclusion")
		case <-deadline.C:
			t.Fatal("workspace operation never reached same-tag contention")
		case <-tick.C:
		}
	}
}
