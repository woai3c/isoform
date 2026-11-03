package tools

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestBundledWritesAllowOrdinaryHomeOperations(t *testing.T) {
	home, project := hardBlockHome(t)
	writeFile(t, filepath.Join(project, "report.pdf"), "report")
	writeFile(t, filepath.Join(home, "Downloads", "a.pdf"), "download")
	writeFile(t, filepath.Join(home, "old.log"), "log")
	for _, command := range []string{
		"cp report.pdf ~/",
		"mv ~/Downloads/a.pdf ~/",
		"rm ~/*.log",
		"cp report.pdf " + strconv.Quote(home+"/"),
		"mv " + strconv.Quote(filepath.Join(home, "Downloads", "a.pdf")) + " " + strconv.Quote(home+"/"),
		"rm " + strconv.Quote(home) + "/*.log",
	} {
		t.Run(command, func(t *testing.T) {
			if err := CheckProtectedPaths("Bash", map[string]any{"command": command}, project); err != nil {
				t.Errorf("ordinary home operation was blocked: %v", err)
			}
		})
	}
	// Check the conventional macOS spelling without creating or modifying
	// anything under /Users. The real filesystem fixture stays in temp HOME.
	for _, fakeHome := range []string{home, "/Users/isoform-guard-fixture"} {
		roots := []string{filepath.Join(fakeHome, ".isoform-agent"), filepath.Join(fakeHome, ".yansu-agent")}
		for _, candidate := range []string{fakeHome, filepath.Dir(fakeHome), filepath.Join(fakeHome, "report.pdf")} {
			if bundledWriteHit(candidate, roots) {
				t.Errorf("non-data path %q was treated as a bundled tool", candidate)
			}
		}
	}
}

func TestBundledWritesRetainDataRootAndResolvedTargetProtection(t *testing.T) {
	home, project := hardBlockHome(t)
	for _, flavor := range []string{".isoform-agent", ".yansu-agent"} {
		root := filepath.Join(home, flavor)
		for _, folder := range bundledToolFolders {
			bundle := filepath.Join(root, folder)
			writeFile(t, filepath.Join(bundle, "tool"), "original")
			for _, path := range []string{root, bundle, filepath.Join(bundle, "tool"), filepath.Join(bundle, "new-tool")} {
				for _, command := range []string{"rm -rf " + strconv.Quote(path), "mv " + strconv.Quote(path) + " moved"} {
					var blocked *BundledToolWriteError
					if err := CheckProtectedPaths("Bash", map[string]any{"command": command}, project); !errors.As(err, &blocked) {
						t.Errorf("protected mutation %s: %v, want bundled tool refusal", command, err)
					}
				}
				if !bundledWriteHit(path, []string{root}) {
					t.Errorf("bundled path or its data root became writable: %s", path)
				}
			}
		}
	}
	root := filepath.Join(home, "custom-data")
	target := filepath.Join(home, "relocated-tools", "bin")
	writeFile(t, filepath.Join(target, "tool"), "original")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "bin")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{root, filepath.Join(root, "bin"), target, filepath.Join(target, "tool")} {
		if !bundledWriteHit(path, []string{root}) {
			t.Errorf("relocated bundle or its data root became writable: %s", path)
		}
	}
	if bundledWriteHit(filepath.Dir(target), []string{root}) {
		t.Error("ordinary destination beside a relocated bundle was blocked")
	}
}

func TestHomeCleanupGlobsRetainProtectedPaths(t *testing.T) {
	home, project := hardBlockHome(t)
	for _, command := range []string{
		"rm -rf ~/.isoform-agent/*",
		"rm -rf ~/.yansu-agent/*",
		"rm -rf ~/.*",
		"rm -rf ~/.iso*",
		"rm -rf ~/.yan*",
		"rm -rf " + strconv.Quote(home) + "/.*",
		"rm -rf ~/.{isoform,yansu}-agent/*",
		"rm -rf ~/.ssh/*",
		"cat ~/.ssh/id_*",
		"cat ~/.isoform-agent/$(printf cron-secret)",
	} {
		t.Run(command, func(t *testing.T) {
			if err := CheckProtectedPaths("Bash", map[string]any{"command": command}, project); err == nil {
				t.Error("glob or dynamic path reaching protected data was allowed")
			}
		})
	}
	// A configured data root can itself match an ordinary-looking glob.
	t.Setenv("YANSU_AGENT_HOME", filepath.Join(home, "custom.log"))
	if err := CheckProtectedPaths("Bash", map[string]any{"command": "rm -rf ~/*.log"}, project); err == nil {
		t.Error("glob matching a configured data root was allowed")
	}
}
