package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func cronPolicySpellings(t *testing.T, path string) []string {
	t.Helper()
	paths := []string{path}
	if runtime.GOOS == "darwin" {
		paths = append(paths, firmlinkSpellings(t, path)...)
		lowerFirmlink := "/system/volumes/data" + path
		if _, err := os.Stat(lowerFirmlink); err != nil {
			t.Fatalf("lowercase firmlink unavailable for policy fixture: %v", err)
		}
		if upper := strings.ToUpper(path); upper != path {
			if _, err := os.Stat(upper); err == nil {
				paths = append(paths, upper)
			}
		}
	}
	return paths
}

func TestCronProjectDirAllowsAppWorkFolders(t *testing.T) {
	home := shortGuardTestHome(t)
	for _, flavor := range []string{appFlavorIsoform, appFlavorYansu} {
		t.Run(flavor, func(t *testing.T) {
			t.Setenv(appFlavorEnv, flavor)
			data := filepath.Join(home, "."+flavor+"-agent")
			for _, name := range []string{"scratch", "research", "workspaces", "skills", "skill-hub", "chat", "chats", "automations", "handoff", "handoffs", "deliverables", "run"} {
				path := filepath.Join(data, name, "project")
				if err := os.MkdirAll(path, 0o700); err != nil {
					t.Fatal(err)
				}
				for _, spelling := range cronPolicySpellings(t, path) {
					if err := validateCronProjectDir(spelling); err != nil {
						t.Errorf("safe work folder %q: %v", spelling, err)
					}
				}
			}
			for _, name := range []string{"", "memory", "knowledge", "mobile", "activity", "screenshots", "audio", "transcripts", "scratch/credentials", "workspaces/cache.db", "bin/project", "opencli/project", "bun/project", "git/project"} {
				path := filepath.Join(data, filepath.FromSlash(name))
				if err := os.MkdirAll(path, 0o700); err != nil {
					t.Fatal(err)
				}
				for _, spelling := range cronPolicySpellings(t, path) {
					if err := validateCronProjectDir(spelling); err == nil {
						t.Errorf("private project folder %q was accepted", spelling)
					}
				}
			}
			alias := filepath.Join(data, "scratch", "private-alias")
			if err := os.Symlink(filepath.Join(data, "memory"), alias); err != nil {
				t.Fatal(err)
			}
			if err := validateCronProjectDir(alias); err == nil {
				t.Fatal("private symlink target accepted as a scratch project")
			}
		})
	}
}

func TestUploadsAllowAppReportsButRefuseAppSecrets(t *testing.T) {
	home := shortGuardTestHome(t)
	for _, flavor := range []string{appFlavorIsoform, appFlavorYansu} {
		t.Run(flavor, func(t *testing.T) {
			t.Setenv(appFlavorEnv, flavor)
			data := filepath.Join(home, "."+flavor+"-agent")
			for _, name := range []string{"scratch", "research", "workspaces", "skills", "bin", "chats", "automations"} {
				work := filepath.Join(data, name, "project")
				if err := os.MkdirAll(work, 0o700); err != nil {
					t.Fatal(err)
				}
				report, secret := filepath.Join(work, "report.md"), filepath.Join(work, "cron-secret")
				for path, content := range map[string]string{report: "report", secret: "PRIVATE-MARKER"} {
					if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				for _, path := range cronPolicySpellings(t, report) {
					files, skipped := readDeliverableFiles([]string{path}, work, work, touchedBy(t, report))
					if len(files) != 1 || len(skipped) != 0 || string(files[0].Data) != "report" {
						t.Errorf("safe report %q: files=%v skipped=%v", path, files, skipped)
					}
				}
				for _, path := range cronPolicySpellings(t, secret) {
					files, skipped := readDeliverableFiles([]string{path}, work, work, touchedBy(t, secret))
					if len(files) != 0 || len(skipped) != 1 {
						t.Errorf("private secret %q: files=%v skipped=%v", path, files, skipped)
					}
				}
			}
		})
	}
}
