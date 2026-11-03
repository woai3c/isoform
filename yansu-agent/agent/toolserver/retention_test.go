//go:build linux || darwin

package toolserver

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"yansu-agent/agent/gate"
	"yansu-agent/agent/native"
	"yansu-agent/agent/tools"
)

func retentionRoot(t *testing.T) string {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "iso-ret-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	// Short enough for a unix socket (macOS allows 103 bytes) and resolved, as
	// authority grants compare resolved paths (/tmp is /private/tmp on macOS).
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func completedRunFixture(t *testing.T, root, name string, completed time.Time) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "ledger.jsonl")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, completed, completed); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(dir, completed, completed); err != nil {
		t.Fatal(err)
	}
	return dir
}

func retentionServer(t *testing.T, root string) *Server {
	t.Helper()
	s, err := New(context.Background(), Config{RunDir: root, Gate: &gate.Gate{Config: &native.LoopConfig{Registry: tools.NewRegistry(), NoTools: true}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestNewPrunesExpiredCompletedLedger(t *testing.T) {
	root := retentionRoot(t)
	old := completedRunFixture(t, root, "assistant-expired", time.Now().Add(-31*24*time.Hour))
	_ = retentionServer(t, root)
	if _, err := os.Lstat(old); !os.IsNotExist(err) {
		t.Fatalf("expired completed run directory was not pruned: %v", err)
	}
}

func TestRunLeasePublicationCannotRacePruner(t *testing.T) {
	root := retentionRoot(t)
	dir := filepath.Join(root, "assistant-starting")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	rootFile, err := openRunRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer rootFile.Close()
	// Before a lease file is created, the empty startup directory is ignored.
	if run, err := inspectRetainedRun(rootFile, filepath.Base(dir)); err != nil || run != nil {
		if run != nil {
			run.close()
		}
		t.Fatalf("empty startup directory became a prune candidate: %v", err)
	}
	var inspected *openedRun
	lease, err := lockRunDirectoryBeforeLock(dir, func() {
		var inspectErr error
		inspected, inspectErr = inspectRetainedRun(rootFile, filepath.Base(dir))
		if inspectErr != nil {
			t.Errorf("inspect startup lease: %v", inspectErr)
		}
	})
	if inspected != nil {
		defer inspected.close()
	}
	if err != nil {
		t.Fatalf("pruner acquired the creator's unpublished lease: %v", err)
	}
	defer lease.close()
	if inspected != nil {
		t.Fatal("partially initialized run exposed an unlocked lease")
	}
	if run, err := inspectRetainedRun(rootFile, filepath.Base(dir)); err != nil || run != nil {
		if run != nil {
			run.close()
		}
		t.Fatalf("published active lease became a prune candidate: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "run.lock" {
		t.Fatalf("lease publication left temporary files: %+v %v", entries, err)
	}
}

func TestRunLeasePublicationDoesNotReplaceExistingLease(t *testing.T) {
	dir := retentionRoot(t)
	first, err := lockRunDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer first.close()
	before, err := os.Stat(filepath.Join(dir, "run.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if second, err := lockRunDirectory(dir); err == nil {
		_ = second.close()
		t.Fatal("second publication replaced a live lease")
	}
	after, err := os.Stat(filepath.Join(dir, "run.lock"))
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("published lease changed: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "run.lock" {
		t.Fatalf("failed publication left private files: %+v %v", entries, err)
	}
}

func TestLedgerRetentionCountAndInspectionWindow(t *testing.T) {
	now := time.Date(2026, time.October, 16, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name  string
		limit int
		ages  []time.Duration
		kept  []bool
	}{
		{"newest completed runs", 2, []time.Duration{time.Hour, 48 * time.Hour, 72 * time.Hour, 96 * time.Hour}, []bool{true, true, false, false}},
		{"inspection window exceeds count target", 1, []time.Duration{time.Hour, 2 * time.Hour, 48 * time.Hour}, []bool{true, true, false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := retentionRoot(t)
			var dirs []string
			for i, age := range tc.ages {
				dirs = append(dirs, completedRunFixture(t, root, "assistant-"+string(rune('a'+i)), now.Add(-age)))
			}
			policy := ledgerRetention{maxAge: 30 * 24 * time.Hour, inspectionWindow: 24 * time.Hour, maxCompleted: tc.limit}
			if err := pruneRunDirectories(root, now, policy); err != nil {
				t.Fatal(err)
			}
			for i, dir := range dirs {
				_, err := os.Lstat(dir)
				if (err == nil) != tc.kept[i] {
					t.Fatalf("run %d kept=%v want=%v err=%v", i, err == nil, tc.kept[i], err)
				}
			}
		})
	}
}

func TestLedgerRetentionNeverRemovesActiveRun(t *testing.T) {
	root := retentionRoot(t)
	s := retentionServer(t, root)
	// Even a run that has remained active past every age limit stays leased.
	if err := pruneRunDirectories(root, time.Now().Add(40*24*time.Hour), defaultLedgerRetention); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.TicketPath()); err != nil {
		t.Fatalf("active run was pruned: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	// Closing keeps the ledger available for the routing finalizer's Snapshot.
	_ = retentionServer(t, root)
	if _, _, err := s.Snapshot(); err != nil {
		t.Fatalf("closed attempt lost its inspection window: %v", err)
	}
}

func TestLedgerRetentionPrunesCrashedRunAfterLeaseRelease(t *testing.T) {
	root := retentionRoot(t)
	old := time.Now().Add(-31 * 24 * time.Hour)
	dir := completedRunFixture(t, root, "assistant-crashed", old)
	lease, err := lockRunDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	// A process crash closes the OS lease without running Server.Close.
	if err := lease.file.Close(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"token", "ticket.json", "claude-mcp.json"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("obsolete"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(dir, "tools.sock"), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if err := os.Chtimes(filepath.Join(dir, entry.Name()), old, old); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chtimes(dir, old, old); err != nil {
		t.Fatal(err)
	}
	if err := pruneRunDirectories(root, time.Now(), defaultLedgerRetention); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Fatalf("crashed run was not pruned: %v", err)
	}
}

func TestLedgerRetentionPreservesSymlinksAndUnknownContents(t *testing.T) {
	root := retentionRoot(t)
	old := time.Now().Add(-31 * 24 * time.Hour)
	outside := completedRunFixture(t, retentionRoot(t), "unrelated", old)
	directoryLink := filepath.Join(root, "assistant-directory-link")
	if err := os.Symlink(outside, directoryLink); err != nil {
		t.Fatal(err)
	}
	ledgerLink := completedRunFixture(t, root, "assistant-ledger-link", old)
	if err := os.Remove(filepath.Join(ledgerLink, "ledger.jsonl")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "ledger.jsonl"), filepath.Join(ledgerLink, "ledger.jsonl")); err != nil {
		t.Fatal(err)
	}
	unknown := completedRunFixture(t, root, "assistant-unknown", old)
	if err := os.WriteFile(filepath.Join(unknown, "keep.txt"), []byte("user file"), 0600); err != nil {
		t.Fatal(err)
	}
	legacyActive := completedRunFixture(t, root, "assistant-legacy-active", old)
	if err := os.WriteFile(filepath.Join(legacyActive, "ticket.json"), []byte("old release may still be running"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{ledgerLink, unknown, legacyActive} {
		if err := os.Chtimes(dir, old, old); err != nil {
			t.Fatal(err)
		}
	}
	// Zero age/count targets ensure every safe candidate is considered; these
	// directories must be excluded because of their contents, not their age.
	if err := pruneRunDirectories(root, time.Now(), ledgerRetention{}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{directoryLink, filepath.Join(outside, "ledger.jsonl"), filepath.Join(ledgerLink, "ledger.jsonl"), filepath.Join(unknown, "keep.txt"), filepath.Join(legacyActive, "ticket.json")} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("unsafe retention removed %s: %v", path, err)
		}
	}
}
