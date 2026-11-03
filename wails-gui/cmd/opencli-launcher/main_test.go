package main

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestLauncherUsesBundledRuntimeWithoutInstallOrUpdateChecks(t *testing.T) {
	for _, layout := range []string{"bundle", "installed"} {
		t.Run(layout, func(t *testing.T) {
			base := t.TempDir()
			exe := filepath.Join(base, "opencli-bundle", "bin", "opencli")
			entry, candidates := runtimePaths(exe)
			bun := candidates[0]
			if layout == "installed" {
				bun = candidates[1]
			}
			for _, path := range []string{entry, bun} {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("fixture"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			cmd, err := command(exe, []string{"list", "-f", "json"}, []string{"PATH=", "CI=0", "KEPT=value"})
			if err != nil {
				t.Fatal(err)
			}
			if cmd.Path != bun || !reflect.DeepEqual(cmd.Args[1:], []string{"--no-install", entry, "list", "-f", "json"}) {
				t.Fatalf("unexpected command: %v", cmd.Args)
			}
			if !reflect.DeepEqual(cmd.Env, []string{"PATH=", "KEPT=value", "CI=1"}) {
				t.Fatalf("env=%v", cmd.Env)
			}
			if runtime.GOOS == "windows" && cmd.SysProcAttr == nil {
				t.Fatal("launcher must hide the Bun console")
			}
			if runtime.GOOS == "windows" && !strings.HasSuffix(cmd.Path, ".exe") {
				t.Fatal(cmd.Path)
			}
		})
	}
}

func TestLauncherMissingBundleIsNotReady(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "opencli-bundle", "bin", "opencli")
	if _, err := command(exe, nil, nil); err == nil || !strings.Contains(err.Error(), "not ready") {
		t.Fatalf("err=%v", err)
	}
	entry, _ := runtimePaths(exe)
	if err := os.MkdirAll(filepath.Dir(entry), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entry, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := command(exe, nil, nil); err == nil || !strings.Contains(err.Error(), "runtime missing") {
		t.Fatalf("err=%v", err)
	}
}
