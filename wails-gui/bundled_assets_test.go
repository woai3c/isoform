package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func isolatedYansuCLIHome(t *testing.T) string {
	t.Helper()
	home, err := os.MkdirTemp("/tmp", "ycli-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	// os.UserHomeDir and os.UserConfigDir consult different environment
	// variables across platforms. Keep both installation and discovery inside
	// this test's home, including macOS's Library/Application Support path.
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("AppData", filepath.Join(home, "config"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv(agentHomeEnv, filepath.Join(home, "selected-data-root"))
	return home
}

func TestBundledYansuCLIStartupIgnoresWorkingDirectory(t *testing.T) {
	for _, existingInstall := range []bool{false, true} {
		name := "absent-install"
		if existingInstall {
			name = "existing-install"
		}
		t.Run(name, func(t *testing.T) {
			home := isolatedYansuCLIHome(t)
			project := filepath.Join(home, "untrusted-project")
			payloadPath := filepath.Join(project, yansuCLIBundleDirName, "bin", bundledYansuCLIExecutableName())
			if err := os.MkdirAll(filepath.Dir(payloadPath), 0o700); err != nil {
				t.Fatal(err)
			}
			// Executable permissions make this discoverable by the former CWD
			// lookup. These are inert bytes and are never executed by the test.
			if err := os.WriteFile(payloadPath, []byte("untrusted working-directory payload"), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Chdir(project)
			safeBin := filepath.Join(home, "safe-bin")
			if err := os.Mkdir(safeBin, 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", safeBin)
			dataRoot, err := flavorDataDir()
			if err != nil {
				t.Fatal(err)
			}
			destPath := filepath.Join(dataRoot, yansuCLIInstallRoot, bundledYansuCLIExecutableName())
			// Obsolete install locations must not satisfy the selected flavor's
			// MCP registration or discovery fallback.
			for _, obsoleteRoot := range []string{home, filepath.Join(home, ".yansu-agent")} {
				obsoletePath := filepath.Join(obsoleteRoot, yansuCLIInstallRoot, bundledYansuCLIExecutableName())
				if err := os.MkdirAll(filepath.Dir(obsoletePath), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(obsoletePath, []byte("unselected installed CLI"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			const installedBytes = "existing installed CLI"
			if existingInstall {
				if err := os.MkdirAll(filepath.Dir(destPath), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(destPath, []byte(installedBytes), 0o755); err != nil {
					t.Fatal(err)
				}
			}

			if got := findBundledYansuCLI(); got != "" {
				t.Fatalf("found a bundle outside the test executable: %s", got)
			}
			if path, err := ensureBundledYansuCLI(); err == nil || path != "" {
				t.Fatalf("installed a working-directory bundle: path=%q err=%v", path, err)
			}
			// Exercise the same entry point used at startup, including PATH
			// publication and install.json after the install attempt.
			ensureBundledYansuAssets()
			if got := os.Getenv("PATH"); got != safeBin {
				t.Fatalf("untrusted bundle changed PATH: %s", got)
			}
			wantInstalled := ""
			if existingInstall {
				wantInstalled = destPath
				data, err := os.ReadFile(destPath)
				if err != nil || string(data) != installedBytes {
					t.Fatalf("existing installed CLI changed: %q, %v", data, err)
				}
			} else if _, err := os.Stat(destPath); !os.IsNotExist(err) {
				t.Fatalf("working-directory payload reached installed path: %v", err)
			}
			if got := mcpReadCLIPath(); got != wantInstalled {
				t.Fatalf("MCP command path = %q, want %q", got, wantInstalled)
			}

			configDir, err := os.UserConfigDir()
			if err != nil {
				t.Fatal(err)
			}
			discoveryPath := filepath.Join(configDir, installDiscoveryDirName(), installDiscoveryFileName)
			data, err := os.ReadFile(discoveryPath)
			if err != nil {
				t.Fatal(err)
			}
			var discovery installDiscovery
			if err := json.Unmarshal(data, &discovery); err != nil {
				t.Fatal(err)
			}
			if discovery.CLI != "" || discovery.CLIInstalled != wantInstalled {
				t.Fatalf("unsafe discovery paths: cli=%q installed=%q", discovery.CLI, discovery.CLIInstalled)
			}
			for _, path := range []string{discovery.CLI, discovery.CLIInstalled} {
				if strings.HasPrefix(path, project+string(os.PathSeparator)) {
					t.Fatalf("install.json published the working-directory payload: %s", path)
				}
			}
		})
	}
}
