package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"wails-gui/internal/platform/cmdutil"
	"wails-gui/storage"
)

// ── Session rewind ──────────────────────────────────────────────────────────

// RewindSession truncates a session's conversation history from a given point.
// It removes stream items from SQLite (sort_order >= sortOrder). Assistant
// transcripts are never read or rewritten: controlled turns start fresh from
// Isoform's retained history. The legacy arguments remain for Wails callers.
//
// The operation is structured for atomicity: files are restored first (reversible),
// and the DB is only modified after file restoration succeeds. If file restoration
// fails, the DB is left untouched so the user can retry.
func (a *App) RewindSession(sessionLocalID, claudeSessionID, projectPath string, sortOrder int, userMessageContent string) error {
	if a.store == nil {
		return fmt.Errorf("store not initialized")
	}

	log.Printf("[rewind] ════════════════════════════════════════════════════════")
	log.Printf("[rewind] starting: session=%s claudeSession=%s sortOrder=%d projectPath=%s",
		sessionLocalID, claudeSessionID, sortOrder, projectPath)

	ctx := context.Background()

	// Log all existing git snapshots for this session (diagnostic context).
	if allSnaps, err := a.store.ListGitSnapshots(ctx, sessionLocalID); err == nil {
		log.Printf("[rewind] all git snapshots for session (%d total):", len(allSnaps))
		for _, s := range allSnaps {
			log.Printf("[rewind]   sortOrder=%d commitSHA=%s headSHA=%s workDir=%s",
				s.SortOrder, s.StashSHA, s.HeadSHA, s.WorkDir)
		}
	}

	// Log how many stream items will be deleted.
	if itemCount, err := a.store.CountStreamItems(ctx, sessionLocalID, sortOrder); err == nil {
		log.Printf("[rewind] stream_items to delete (sortOrder>=%d): %d", sortOrder, itemCount)
	}

	// 1-2. Restore files BEFORE touching the DB.
	// Always try git snapshot first (works for ALL models — captures complete
	// worktree state including Bash-modified files). Fall back to per-file
	// checkpoints only if no git snapshot exists (e.g. non-git directories).
	restoredFromGit := false
	snapshot, err := a.store.LoadGitSnapshotAtOrBefore(ctx, sessionLocalID, sortOrder)
	if err != nil {
		log.Printf("[rewind] no git snapshot at or before sort_order %d: %v", sortOrder, err)
	} else {
		log.Printf("[rewind] selected git snapshot: id=%d sortOrder=%d commitSHA=%q headSHA=%q workDir=%s",
			snapshot.ID, snapshot.SortOrder, snapshot.StashSHA, snapshot.HeadSHA, snapshot.WorkDir)
		if err := restoreGitSnapshot(snapshot); err != nil {
			log.Printf("[rewind] git snapshot restore FAILED: %v", err)
		} else {
			log.Printf("[rewind] git snapshot restore succeeded")
			restoredFromGit = true
		}
	}

	if !restoredFromGit {
		// Fallback: restore from per-file checkpoint backups (for non-git directories).
		checkpointRows, err := a.store.LoadCheckpointFilesFrom(ctx, sessionLocalID, sortOrder)
		if err != nil {
			return fmt.Errorf("load checkpoint files: %w", err)
		}
		log.Printf("[rewind] fallback to per-file checkpoints: found %d file(s) for sortOrder>=%d", len(checkpointRows), sortOrder)
		for _, row := range checkpointRows {
			log.Printf("[rewind]   checkpoint: sortOrder=%d file=%s existed=%v", row.SortOrder, row.FilePath, row.Existed)
		}
		if err := a.restoreCheckpointFiles(sessionLocalID, checkpointRows); err != nil {
			return fmt.Errorf("restore checkpoint files: %w", err)
		}
		a.cleanupCheckpointDirs(sessionLocalID, checkpointRows)
	}

	// 3. Delete stream items, checkpoint records, and git snapshots from the DB.
	// First collect snapshot SHAs for ref cleanup.
	snapshotSHAs, _ := a.store.LoadGitSnapshotSHAs(ctx, sessionLocalID, sortOrder)
	log.Printf("[rewind] cleaning up DB records with sortOrder>=%d (snapshot refs to remove: %d)", sortOrder, len(snapshotSHAs))

	if err := a.store.DeleteStreamItemsFrom(ctx, sessionLocalID, sortOrder); err != nil {
		return fmt.Errorf("delete stream items: %w", err)
	}
	if err := a.store.DeleteCheckpointFilesFrom(ctx, sessionLocalID, sortOrder); err != nil {
		log.Printf("[rewind] delete checkpoint records failed: %v", err)
	}
	if err := a.store.DeleteGitSnapshotsFrom(ctx, sessionLocalID, sortOrder); err != nil {
		log.Printf("[rewind] delete git snapshots failed: %v", err)
	}

	// Clean up git refs for deleted snapshots (allow GC to reclaim objects).
	if snapshot != nil {
		for _, sha := range snapshotSHAs {
			deleteCheckpointRef(snapshot.WorkDir, sha)
		}
	}

	// Log remaining snapshots after cleanup.
	if remaining, err := a.store.ListGitSnapshots(ctx, sessionLocalID); err == nil {
		log.Printf("[rewind] remaining git snapshots after cleanup: %d", len(remaining))
	}

	log.Printf("[rewind] completed successfully")
	log.Printf("[rewind] ════════════════════════════════════════════════════════")
	return nil
}

// ── Git snapshot helpers ─────────────────────────────────────────────────────
//
// Git snapshots are the PRIMARY checkpoint mechanism for all sessions. They
// capture the COMPLETE worktree state (tracked + untracked files) as an orphan
// commit using git plumbing commands (write-tree + commit-tree). This approach
// is superior to `git stash create` because:
//
//   - It captures the ABSOLUTE state, not a diff from HEAD.
//   - It works even when the AI has committed changes during a turn
//     (stash would see a clean worktree and capture nothing).
//   - The orphan commit's tree represents the full filesystem snapshot,
//     so restore is a simple `git checkout <commit> -- .`.
//
// A git ref (refs/yansu-checkpoint/<sha>) anchors each commit to prevent GC.
// Per-file backups are only used as a fallback for non-git directories.

const checkpointRefPrefix = "refs/yansu-checkpoint/"

// isGitRepo returns true if workDir is inside a git repository.
func isGitRepo(workDir string) bool {
	cmd := cmdutil.GitCommand("rev-parse", "--git-dir")
	cmd.Dir = workDir
	return cmd.Run() == nil
}

// gitOutput runs a git command and returns trimmed stdout.
func gitOutput(workDir string, args ...string) (string, error) {
	cmd := cmdutil.GitCommand(args...)
	cmd.Dir = workDir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// gitRun runs a git command and returns combined output on error.
func gitRun(workDir string, args ...string) error {
	cmd := cmdutil.GitCommand(args...)
	cmd.Dir = workDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %w\n%s", strings.Join(args, " "), err, out)
	}
	return nil
}

// createGitSnapshot captures the complete worktree state as an orphan commit.
//
// Approach: use a TEMPORARY index file so the user's real index is never touched.
//  1. Create temp index, read-tree HEAD into it
//  2. git add -A using temp index (stages all working tree changes + untracked)
//  3. git write-tree → tree SHA (full worktree snapshot)
//  4. git commit-tree → orphan commit SHA
//  5. git update-ref to prevent GC
//  6. Clean up temp index
func createGitSnapshot(workDir string) (commitSHA, headSHA, untrackedFiles string, err error) {
	// Record HEAD.
	headSHA, err = gitOutput(workDir, "rev-parse", "HEAD")
	if err != nil {
		return "", "", "", fmt.Errorf("rev-parse HEAD: %w", err)
	}
	log.Printf("[git-snapshot] creating snapshot: workDir=%s HEAD=%s", workDir, headSHA)

	// List untracked files (for diagnostic logging + backward compat).
	if out, e := gitOutput(workDir, "ls-files", "--others", "--exclude-standard"); e == nil {
		untrackedFiles = out
	}
	log.Printf("[git-snapshot] pre-snapshot: HEAD=%s untracked=%d",
		headSHA, countLines(untrackedFiles))

	// Create a temporary index file to avoid touching the user's index.
	tmpIndex, err := os.CreateTemp("", "yansu-checkpoint-idx-*")
	if err != nil {
		return "", headSHA, untrackedFiles, fmt.Errorf("create temp index: %w", err)
	}
	tmpIndexPath := tmpIndex.Name()
	tmpIndex.Close()
	defer os.Remove(tmpIndexPath)

	env := append(os.Environ(), "GIT_INDEX_FILE="+tmpIndexPath)

	// Initialize temp index from HEAD.
	readTreeCmd := cmdutil.GitCommand("read-tree", "HEAD")
	readTreeCmd.Dir = workDir
	readTreeCmd.Env = env
	if out, e := readTreeCmd.CombinedOutput(); e != nil {
		return "", headSHA, untrackedFiles, fmt.Errorf("read-tree HEAD: %w\n%s", e, out)
	}

	// Stage ALL working tree changes (tracked modifications + untracked files) into temp index.
	addCmd := cmdutil.GitCommand("add", "-A")
	addCmd.Dir = workDir
	addCmd.Env = env
	if out, e := addCmd.CombinedOutput(); e != nil {
		log.Printf("[git-snapshot] git add -A failed (non-fatal, trying without untracked): %v\n%s", e, out)
		// Fallback: add only tracked file changes (skip untracked that may have path issues on Windows).
		addCmd2 := cmdutil.GitCommand("add", "-u")
		addCmd2.Dir = workDir
		addCmd2.Env = env
		if out2, e2 := addCmd2.CombinedOutput(); e2 != nil {
			return "", headSHA, untrackedFiles, fmt.Errorf("git add -u: %w\n%s", e2, out2)
		}
	}

	// Create tree object from temp index.
	writeTreeCmd := cmdutil.GitCommand("write-tree")
	writeTreeCmd.Dir = workDir
	writeTreeCmd.Env = env
	treeOut, err := writeTreeCmd.Output()
	if err != nil {
		return "", headSHA, untrackedFiles, fmt.Errorf("write-tree: %w", err)
	}
	treeSHA := strings.TrimSpace(string(treeOut))

	// Create orphan commit from the tree (no parent — standalone snapshot).
	commitOut, err := gitOutput(workDir, "commit-tree", treeSHA, "-m", "yansu-checkpoint")
	if err != nil {
		return "", headSHA, untrackedFiles, fmt.Errorf("commit-tree: %w", err)
	}
	commitSHA = commitOut

	// Create a ref to prevent garbage collection of the commit.
	if err := gitRun(workDir, "update-ref", checkpointRefPrefix+commitSHA, commitSHA); err != nil {
		log.Printf("[git-snapshot] update-ref failed (commit may be GC'd): %v", err)
	}

	log.Printf("[git-snapshot] created: commitSHA=%s treeSHA=%s HEAD=%s", commitSHA, treeSHA, headSHA)
	return commitSHA, headSHA, untrackedFiles, nil
}

// deleteCheckpointRef removes the GC-prevention ref for a checkpoint commit.
func deleteCheckpointRef(workDir, commitSHA string) {
	if commitSHA == "" || workDir == "" {
		return
	}
	if err := gitRun(workDir, "update-ref", "-d", checkpointRefPrefix+commitSHA); err != nil {
		// Non-fatal: ref might not exist (old stash-based snapshots).
		log.Printf("[git-snapshot] delete ref %s%s: %v", checkpointRefPrefix, commitSHA, err)
	}
}

// restoreGitSnapshot restores the worktree to a previously captured snapshot.
//
// The snapshot commit's tree contains the COMPLETE worktree state at checkpoint
// time. Restoration:
//  1. git checkout <commit> -- .   (restore all files from the tree)
//  2. Delete files not in the tree (created after the snapshot)
//  3. If HEAD moved since snapshot, reset HEAD to the snapshot's HEAD
func restoreGitSnapshot(snapshot *storage.GitSnapshotRow) error {
	workDir := snapshot.WorkDir
	commitSHA := snapshot.StashSHA // field reused: stores checkpoint commit SHA

	currentHEAD, _ := gitOutput(workDir, "rev-parse", "HEAD")
	log.Printf("[git-snapshot] ── restore begin ──")
	log.Printf("[git-snapshot] target: sortOrder=%d commitSHA=%q snapshotHEAD=%q currentHEAD=%s",
		snapshot.SortOrder, commitSHA, snapshot.HeadSHA, currentHEAD)

	if commitSHA == "" {
		// Empty commitSHA means the snapshot was from a clean worktree (legacy stash-based
		// snapshot or first turn). Reset tracked files to HEAD.
		log.Printf("[git-snapshot] commitSHA is empty → restoring tracked files to HEAD")

		// Save the user's index state so we can restore it after checkout.
		savedIdxTree, _ := gitOutput(workDir, "write-tree")

		if err := gitRun(workDir, "checkout", "HEAD", "--", "."); err != nil {
			return fmt.Errorf("git checkout HEAD: %w", err)
		}

		// Restore the user's original index (checkout overwrites it).
		if savedIdxTree != "" {
			if err := gitRun(workDir, "read-tree", savedIdxTree); err != nil {
				log.Printf("[git-snapshot] read-tree restore failed (non-fatal): %v", err)
			}
		}

		log.Printf("[git-snapshot] ── restore end (clean snapshot) ──")
		return nil
	}

	// Verify the commit object still exists (might have been garbage-collected).
	objType, err := gitOutput(workDir, "cat-file", "-t", commitSHA)
	if err != nil {
		return fmt.Errorf("checkpoint commit %s not found (garbage collected?): %w", commitSHA, err)
	}
	log.Printf("[git-snapshot] commit %s verified: type=%s", commitSHA, objType)

	// Save the user's index as a tree object BEFORE any modifications.
	// We restore it after the checkout so the staging area is completely untouched.
	savedIndexTree, writeTreeErr := gitOutput(workDir, "write-tree")
	if writeTreeErr != nil {
		log.Printf("[git-snapshot] write-tree failed (index will not be preserved): %v", writeTreeErr)
	} else {
		log.Printf("[git-snapshot] saved index tree: %s", savedIndexTree)
	}

	// Step 1: Checkout all files from the snapshot tree.
	// This restores every file that existed at checkpoint time.
	// NOTE: git checkout also overwrites the index, but we restore it in step 4.
	if err := gitRun(workDir, "checkout", commitSHA, "--", "."); err != nil {
		return fmt.Errorf("git checkout %s -- .: %w", commitSHA, err)
	}
	log.Printf("[git-snapshot] checked out all files from %s", commitSHA)

	// Step 2: Remove files that exist now but did NOT exist at checkpoint time.
	// git ls-tree lists all files in the checkpoint tree.
	treeFilesStr, err := gitOutput(workDir, "ls-tree", "-r", "--name-only", commitSHA)
	if err != nil {
		log.Printf("[git-snapshot] ls-tree failed (skipping cleanup): %v", err)
	} else {
		treeFiles := makeSet(treeFilesStr)

		// Find tracked files not in the checkpoint tree → delete them.
		if trackedStr, e := gitOutput(workDir, "ls-files"); e == nil {
			for _, f := range splitLines(trackedStr) {
				if !treeFiles[f] {
					absPath := filepath.Join(workDir, f)
					if err := os.Remove(absPath); err != nil && !os.IsNotExist(err) {
						log.Printf("[git-snapshot] remove tracked file %s failed: %v", f, err)
					} else {
						log.Printf("[git-snapshot] removed extra tracked file: %s", f)
					}
				}
			}
		}

		// Find untracked files not in the checkpoint tree → delete them.
		if untrackedStr, e := gitOutput(workDir, "ls-files", "--others", "--exclude-standard"); e == nil {
			for _, f := range splitLines(untrackedStr) {
				if !treeFiles[f] {
					absPath := filepath.Join(workDir, f)
					if err := os.Remove(absPath); err != nil && !os.IsNotExist(err) {
						log.Printf("[git-snapshot] remove untracked file %s failed: %v", f, err)
					} else {
						log.Printf("[git-snapshot] removed extra untracked file: %s", f)
					}
				}
			}
		}
	}

	// Step 3: Restore the user's original index from the saved tree.
	// This undoes the index changes caused by "git checkout <commit> -- ."
	// so the user's staging area is completely preserved.
	if writeTreeErr == nil {
		if err := gitRun(workDir, "read-tree", savedIndexTree); err != nil {
			log.Printf("[git-snapshot] read-tree restore failed (non-fatal): %v", err)
		} else {
			log.Printf("[git-snapshot] restored user's index from %s", savedIndexTree)
		}
	}

	// Step 4: If the AI committed during the session (HEAD moved), reset HEAD
	// back to where it was at snapshot time. Use --soft to ONLY move HEAD
	// without touching the index or working tree.
	if snapshot.HeadSHA != "" && currentHEAD != snapshot.HeadSHA {
		log.Printf("[git-snapshot] HEAD moved: %s → %s, resetting to snapshot HEAD", snapshot.HeadSHA, currentHEAD)
		if err := gitRun(workDir, "reset", "--soft", snapshot.HeadSHA); err != nil {
			log.Printf("[git-snapshot] HEAD reset failed (non-fatal, files are already restored): %v", err)
		} else {
			log.Printf("[git-snapshot] HEAD reset to %s (soft)", snapshot.HeadSHA)
		}
	}

	// Log final state.
	finalHEAD, _ := gitOutput(workDir, "rev-parse", "--short", "HEAD")
	log.Printf("[git-snapshot] post-restore HEAD=%s", finalHEAD)
	if statusStr, e := gitOutput(workDir, "status", "--short"); e == nil {
		if statusStr == "" {
			statusStr = "(clean)"
		} else {
			lines := strings.Split(statusStr, "\n")
			if len(lines) > 15 {
				statusStr = strings.Join(lines[:15], "\n") + fmt.Sprintf("\n... and %d more", len(lines)-15)
			}
		}
		log.Printf("[git-snapshot] post-restore status:\n%s", statusStr)
	}

	log.Printf("[git-snapshot] ── restore end ──")
	return nil
}

// ── String helpers ──────────────────────────────────────────────────────────

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	var result []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			result = append(result, line)
		}
	}
	return result
}

func makeSet(newlineSeparated string) map[string]bool {
	m := make(map[string]bool)
	for _, f := range splitLines(newlineSeparated) {
		m[f] = true
	}
	return m
}

func countLines(s string) int {
	if strings.TrimSpace(s) == "" {
		return 0
	}
	return len(splitLines(s))
}

// ── File checkpoint helpers ──────────────────────────────────────────────────

// isFileModifyTool returns true for tool names that modify files on disk.
func isFileModifyTool(name string) bool {
	switch name {
	case "Write", "write_file", "Edit", "edit_file":
		return true
	default:
		return false
	}
}

// extractFilePath extracts the file_path field from a tool_use input.
func extractFilePath(toolInput any) string {
	m, ok := toolInput.(map[string]any)
	if !ok {
		data, err := json.Marshal(toolInput)
		if err != nil {
			return ""
		}
		if err := json.Unmarshal(data, &m); err != nil {
			return ""
		}
	}
	if path, ok := m["file_path"].(string); ok && path != "" {
		return path
	}
	if path, ok := m["path"].(string); ok && path != "" {
		return path
	}
	return ""
}

// maybeBackupFile backs up a file before it is modified by a Write/Edit tool.
func (a *App) maybeBackupFile(sessionLocalID string, sortOrder int, toolName string, toolInput any, backedUp map[string]bool) {
	if !isFileModifyTool(toolName) {
		return
	}
	filePath := extractFilePath(toolInput)
	if filePath == "" {
		log.Printf("[checkpoint] extractFilePath returned empty for tool=%s inputType=%T", toolName, toolInput)
		return
	}
	if backedUp[filePath] {
		return
	}
	log.Printf("[checkpoint] backing up file: %s (tool=%s sortOrder=%d)", filePath, toolName, sortOrder)

	dataRoot, err := flavorDataDir()
	if err != nil {
		log.Printf("[checkpoint] cannot determine home dir: %v", err)
		return
	}

	checkpointDir := filepath.Join(dataRoot, "checkpoints", sessionLocalID, fmt.Sprintf("%d", sortOrder))
	if err := os.MkdirAll(checkpointDir, 0700); err != nil {
		log.Printf("[checkpoint] create dir failed: %v", err)
		return
	}

	encoded := base64.URLEncoding.EncodeToString([]byte(filePath))
	backupPath := filepath.Join(checkpointDir, encoded)

	existed := true
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			existed = false
		} else {
			log.Printf("[checkpoint] read file %s failed: %v", filePath, err)
			return
		}
	}

	if existed {
		if err := os.WriteFile(backupPath, data, 0644); err != nil {
			log.Printf("[checkpoint] write backup failed: %v", err)
			return
		}
	}

	if a.store != nil {
		if err := a.store.SaveCheckpointFile(context.Background(), storage.CheckpointFileRow{
			SessionLocalID: sessionLocalID,
			SortOrder:      sortOrder,
			FilePath:       filePath,
			BackupPath:     backupPath,
			Existed:        existed,
			CreatedAt:      time.Now().UnixMilli(),
		}); err != nil {
			log.Printf("[checkpoint] save db record failed: %v", err)
		}
	}

	backedUp[filePath] = true
}

// restoreCheckpointFiles restores backed-up files from the given checkpoint rows.
func (a *App) restoreCheckpointFiles(sessionLocalID string, rows []storage.CheckpointFileRow) error {
	if len(rows) == 0 {
		return nil
	}

	earliest := make(map[string]storage.CheckpointFileRow)
	for _, row := range rows {
		if _, ok := earliest[row.FilePath]; !ok {
			earliest[row.FilePath] = row
		}
	}
	log.Printf("[checkpoint] restoring %d unique files from %d records", len(earliest), len(rows))

	for _, row := range earliest {
		switch {
		case row.Existed:
			data, err := os.ReadFile(row.BackupPath)
			if err != nil {
				return fmt.Errorf("read backup %s: %w", row.BackupPath, err)
			}
			if err := os.WriteFile(row.FilePath, data, 0644); err != nil {
				return fmt.Errorf("restore file %s: %w", row.FilePath, err)
			}
			log.Printf("[checkpoint] restored %s (%d bytes)", row.FilePath, len(data))
		default:
			if err := os.Remove(row.FilePath); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove file %s: %w", row.FilePath, err)
			}
			log.Printf("[checkpoint] removed %s (did not exist before)", row.FilePath)
		}
	}
	return nil
}

// cleanupCheckpointDirs removes checkpoint directories on disk for the given rows.
func (a *App) cleanupCheckpointDirs(sessionLocalID string, rows []storage.CheckpointFileRow) {
	if len(rows) == 0 {
		return
	}

	dataRoot, err := flavorDataDir()
	if err != nil {
		return
	}
	sessionCheckpointDir := filepath.Join(dataRoot, "checkpoints", sessionLocalID)

	sortOrders := make(map[int]bool)
	for _, row := range rows {
		sortOrders[row.SortOrder] = true
	}
	for so := range sortOrders {
		dir := filepath.Join(sessionCheckpointDir, fmt.Sprintf("%d", so))
		os.RemoveAll(dir)
	}
}

// cleanupCheckpointDisk removes checkpoint directories for the given session IDs.
func (a *App) cleanupCheckpointDisk(sessionLocalIDs []string) {
	dataRoot, err := flavorDataDir()
	if err != nil {
		return
	}
	for _, id := range sessionLocalIDs {
		dir := filepath.Join(dataRoot, "checkpoints", id)
		os.RemoveAll(dir)
	}
}
