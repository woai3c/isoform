package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDeliverableFolderGateFailsClosedOnUnresolvableAliases(t *testing.T) {
	app := newTodayContextTestApp(t, true)
	root := t.TempDir()
	private := filepath.Join(root, "private")
	public := filepath.Join(root, "public")
	for _, dir := range []string{private, public} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := app.saveFilesPrivateFolders([]string{private}); err != nil {
		t.Fatal(err)
	}
	aliases := map[string]string{"broken": filepath.Join(private, "missing.md"), "loop": filepath.Join(public, "loop"), "private-alias": private}
	for name, target := range aliases {
		if err := os.Symlink(target, filepath.Join(public, name)); err != nil {
			t.Skipf("symlink fixtures are unavailable on this platform: %v", err)
		}
	}
	allowed := app.deliverableFolderReadFilter(context.Background())
	for _, name := range []string{"broken", "loop", filepath.Join("private-alias", "missing.md")} {
		if allowed(DeliverableEntry{Files: []DeliverableFileRef{{Path: filepath.Join(public, name)}}}) {
			t.Errorf("unverifiable or private alias %q authorized metadata", name)
		}
	}
	if !allowed(DeliverableEntry{Files: []DeliverableFileRef{{Path: filepath.Join(public, "future-report.md")}}}) {
		t.Fatal("ordinary public missing output was withheld")
	}
}
