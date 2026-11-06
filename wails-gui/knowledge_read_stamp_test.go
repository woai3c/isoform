package main

import (
	"context"
	"crypto/sha256"
	"hash"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A snapshot folder's presence follows its contents: adding or removing a
// snapshot changes the folder's modification time, and the answer follows.
func TestKnowledgeEntryHasHistoryFollowsSnapshotFolder(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, knowledgeHistoryDirName, "entry-1")
	if knowledgeEntryHasHistory(dir, "entry-1") {
		t.Fatal("no history folder should mean no history")
	}
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	base := time.Now().Add(-time.Hour).Truncate(time.Second)
	set := func(at time.Time) {
		if err := os.Chtimes(folder, at, at); err != nil {
			t.Fatal(err)
		}
	}
	set(base)
	if knowledgeEntryHasHistory(dir, "entry-1") {
		t.Fatal("an empty history folder should not count as history")
	}
	snapshot := filepath.Join(folder, "snap.md")
	if err := os.WriteFile(snapshot, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	set(base.Add(time.Minute))
	if !knowledgeEntryHasHistory(dir, "entry-1") {
		t.Fatal("a new snapshot was not seen")
	}
	if err := os.Remove(snapshot); err != nil {
		t.Fatal(err)
	}
	set(base.Add(2 * time.Minute))
	if knowledgeEntryHasHistory(dir, "entry-1") {
		t.Fatal("a removed snapshot still counted as history")
	}
}

// An in-place edit that keeps the file size changes its modification time,
// so the admission digest is recomputed from the new bytes, not reused.
func TestAdmissionFileDigestFollowsSameSizeEdit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "note.md")
	if err := os.WriteFile(path, []byte("alpha"), 0o600); err != nil {
		t.Fatal(err)
	}
	base := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(path, base, base); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	digestOf := func() []byte {
		t.Helper()
		var h hash.Hash = sha256.New()
		if err := todayChatAdmissionFile(ctx, h, path, path); err != nil {
			t.Fatal(err)
		}
		return h.Sum(nil)
	}
	first := digestOf()
	if again := digestOf(); string(again) != string(first) {
		t.Fatal("an unchanged file changed its digest between reads")
	}
	if err := os.WriteFile(path, []byte("omega"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, base.Add(time.Minute), base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if edited := digestOf(); string(edited) == string(first) {
		t.Fatal("a same-size edit kept the old digest")
	}
}
