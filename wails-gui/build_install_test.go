package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildWailsShellSyntax(t *testing.T) {
	if output, err := exec.Command("bash", "-n", "scripts/build-wails.sh").CombinedOutput(); err != nil {
		t.Fatalf("bash -n: %v\n%s", err, output)
	}
}

func TestBuildWailsInstallDryRun(t *testing.T) {
	script, err := os.ReadFile("scripts/build-wails.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name    string
		args    []string
		bundle  string
		install bool
		failure string
	}{
		{"isoform", []string{"--flavor", "isoform", "--install"}, "Isoform.app", true, ""},
		{"release", nil, "Isoform.app", false, ""},
		{"release refuses install", []string{"--install"}, "", false, "--install is for development builds"},
		{"yansu retired", []string{"--flavor", "yansu"}, "", false, "--flavor yansu is retired"},
		{"mac platform", []string{"--flavor", "isoform", "--platform", "darwin/arm64", "--install"}, "Isoform.app", true, ""},
		{"build only", []string{"--flavor", "isoform"}, "Isoform.app", false, ""},
		{"linux install", []string{"--flavor", "isoform", "--platform", "linux/amd64", "--install"}, "", false, "requires a macOS app bundle"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// A clean checkout with spaces exercises quoting and proves dry-run
			// needs neither bundles nor build tools and creates no build output.
			root := filepath.Join(t.TempDir(), "work tree")
			scripts := filepath.Join(root, "scripts")
			if err := os.MkdirAll(scripts, 0755); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(scripts, "build-wails.sh")
			if err := os.WriteFile(path, script, 0755); err != nil {
				t.Fatal(err)
			}
			args := append([]string{path, "--dry-run"}, tt.args...)
			output, runErr := exec.Command("bash", args...).CombinedOutput()
			plan := string(output)
			if tt.failure != "" {
				if runErr == nil || !strings.Contains(plan, tt.failure) {
					t.Fatalf("expected error %q, got %v: %s", tt.failure, runErr, plan)
				}
			} else {
				if runErr != nil {
					t.Fatalf("dry-run: %v\n%s", runErr, plan)
				}
				if !strings.Contains(plan, "/build/bin/"+tt.bundle) {
					t.Fatalf("missing build bundle %q: %s", tt.bundle, plan)
				}
				if tt.install {
					for _, want := range []string{"rsync -aE --checksum --delete", "/Applications/" + tt.bundle, "codesign --verify --deep --strict"} {
						if !strings.Contains(plan, want) {
							t.Errorf("missing install step %q: %s", want, plan)
						}
					}
				} else if strings.Contains(plan, "rsync") {
					t.Fatalf("build without --install planned a copy: %s", plan)
				}
			}
			entries, err := os.ReadDir(root)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 || entries[0].Name() != "scripts" {
				t.Fatalf("dry-run created files in clean checkout: %v", entries)
			}
		})
	}
}

func TestBuildWailsInstallProtectsExistingDestination(t *testing.T) {
	script, err := os.ReadFile("scripts/build-wails.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name    string
		exists  bool
		marker  string
		failure bool
	}{
		{"unmanaged app", true, "", true},
		{"invalid marker", true, "another installer", true},
		{"retired yansu marker", true, "isoform-build-install-v1:Yansu.app", true},
		{"managed install", true, "isoform-build-install-v1:Isoform.app", false},
		{"fresh install", false, "", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "scripts", "build-wails.sh")
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, script, 0755); err != nil {
				t.Fatal(err)
			}
			home := filepath.Join(root, "test home")
			installed := filepath.Join(home, "Applications", "Isoform.app")
			marker := filepath.Join(installed, "Contents", "Resources", ".isoform-build-install")
			if tt.exists {
				if err := os.MkdirAll(filepath.Dir(marker), 0755); err != nil {
					t.Fatal(err)
				}
				if tt.marker != "" {
					if err := os.WriteFile(marker, []byte(tt.marker+"\n"), 0644); err != nil {
						t.Fatal(err)
					}
				}
			}
			cmd := exec.Command("bash", path, "--flavor", "isoform", "--install", "--dry-run")
			cmd.Env = append(os.Environ(), "HOME="+home)
			output, runErr := cmd.CombinedOutput()
			if tt.failure {
				if runErr == nil || !strings.Contains(string(output), "unmanaged app") {
					t.Fatalf("expected unmanaged app refusal, got %v: %s", runErr, output)
				}
				if strings.Contains(string(output), "rsync") {
					t.Fatalf("refusal still planned a destructive copy: %s", output)
				}
			} else if runErr != nil {
				t.Fatalf("expected permitted install, got %v: %s", runErr, output)
			}
			if tt.marker != "" {
				unchanged, err := os.ReadFile(marker)
				if err != nil || string(unchanged) != tt.marker+"\n" {
					t.Fatalf("dry-run changed existing marker: %q, %v", unchanged, err)
				}
			}
		})
	}
}
