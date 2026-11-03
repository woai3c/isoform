package main

import (
	"os"
	"path/filepath"
	"testing"
)

func dreamArchiveCountTestDir(t *testing.T) string {
	t.Helper()
	home, err := os.MkdirTemp("/tmp", "dream-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("HOME", home)
	dir, err := memoryArchiveDir()
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestCountArchivedMemoriesMatchesMarkdownListing(t *testing.T) {
	dir := dreamArchiveCountTestDir(t)
	for _, name := range []string{"first.md", "second.md", "ignore.txt", "ignore.MD"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("# Archived: note\n\nbody\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "nested.md"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "nested.md", "inside.md"), []byte("nested"), 0600); err != nil {
		t.Fatal(err)
	}
	count, err := countArchivedMemories()
	if err != nil {
		t.Fatal(err)
	}
	listed, err := listArchivedMemories(0)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 || count != len(listed) {
		t.Fatalf("count = %d, listed = %d; want two top-level markdown files", count, len(listed))
	}
}

func TestCountArchivedMemoriesFollowsOnlyRegularFileLinks(t *testing.T) {
	dir := dreamArchiveCountTestDir(t)
	target := filepath.Join(filepath.Dir(dir), "target.txt")
	if err := os.WriteFile(target, []byte("# Archived: linked\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for name, destination := range map[string]string{
		"valid.md":     target,
		"directory.md": filepath.Dir(dir),
		"missing.md":   filepath.Join(dir, "absent"),
	} {
		if err := os.Symlink(destination, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	count, err := countArchivedMemories()
	if err != nil {
		t.Fatal(err)
	}
	listed, err := listArchivedMemories(0)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 || count != len(listed) {
		t.Fatalf("count = %d, listed = %d; want only the regular-file link", count, len(listed))
	}
}

func TestMemoryDreamStatusSkipsUnreadableArchivesWithoutLoadingBodies(t *testing.T) {
	dir := dreamArchiveCountTestDir(t)
	path := filepath.Join(dir, "unreadable.md")
	if err := os.WriteFile(path, []byte("preserved archive contents"), 0000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0600) })
	if _, err := os.ReadFile(path); err == nil {
		t.Skip("current user can read a mode-000 file")
	}

	if err := os.Symlink(path, filepath.Join(dir, "unreadable-link.md")); err != nil {
		t.Fatal(err)
	}

	service := NewMemoryDreamService(&App{})
	service.running = true
	service.currentRun = &DreamRun{ID: "active-run", Status: "running"}
	service.history = []DreamRun{{ID: "previous-run", Status: "completed"}}
	status := service.Status()
	if status.ArchiveCount != 0 {
		t.Fatalf("archive count = %d; want unreadable file and symlink excluded", status.ArchiveCount)
	}
	if !status.Running || status.CurrentRunID != "active-run" || status.LastRun == nil || status.LastRun.ID != "previous-run" {
		t.Fatalf("status lost lifecycle fields: %+v", status)
	}
	listed, err := listArchivedMemories(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != status.ArchiveCount {
		t.Fatalf("count = %d, listed = %d; unreadable entries must match", status.ArchiveCount, len(listed))
	}

	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	status = service.Status()
	listed, err = listArchivedMemories(0)
	if err != nil {
		t.Fatal(err)
	}
	if status.ArchiveCount != 2 || status.ArchiveCount != len(listed) {
		t.Fatalf("count = %d, listed = %d; restored readability must count file and symlink", status.ArchiveCount, len(listed))
	}
}
