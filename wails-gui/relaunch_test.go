package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestRelaunchEnvCarriesOnlyLaunchSettings(t *testing.T) {
	t.Setenv("ISOFORM_RELAY_URL", "http://127.0.0.1:8791")
	t.Setenv("YANSU_BROWSER_BRIDGE", "1")
	t.Setenv("YANSU_PPROF", "1")
	env := relaunchEnv()
	for _, want := range []string{"ISOFORM_RELAY_URL=http://127.0.0.1:8791", "YANSU_BROWSER_BRIDGE=1"} {
		if !slices.Contains(env, want) {
			t.Errorf("relaunchEnv() = %q, want it to carry %s", env, want)
		}
	}
	if slices.Contains(env, "YANSU_PPROF=1") {
		t.Errorf("relaunchEnv() = %q, carries a setting that is not a launch setting", env)
	}
}

func TestRelaunchEnvPreservesMobileRelayURL(t *testing.T) {
	for _, tc := range []struct {
		name      string
		canonical string
		legacy    string
	}{
		{name: "legacy only", legacy: "https://legacy.example"},
		{name: "canonical only", canonical: "https://canonical.example"},
		{name: "both", canonical: "https://canonical.example", legacy: "https://legacy.example"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, key := range relaunchEnvKeys {
				t.Setenv(key, "")
			}
			t.Setenv("ISOFORM_RELAY_URL", tc.canonical)
			t.Setenv("YANSU_RELAY_URL", tc.legacy)
			t.Setenv("ISOFORM_MOBILE_REMOTE_ACCESS", "1")

			want := mobileRelayURLFromEnvironment()
			env := relaunchEnv()
			if !slices.Contains(env, "ISOFORM_MOBILE_REMOTE_ACCESS=1") {
				t.Fatalf("relaunchEnv() = %q, does not preserve the remote-access gate", env)
			}

			carried := make(map[string]string, len(env))
			for _, entry := range env {
				key, value, ok := strings.Cut(entry, "=")
				if !ok {
					t.Fatalf("relaunchEnv() contains malformed entry %q", entry)
				}
				carried[key] = value
			}
			for _, key := range relaunchEnvKeys {
				t.Setenv(key, "")
			}
			for key, value := range carried {
				t.Setenv(key, value)
			}
			if got := mobileRelayURLFromEnvironment(); got != want {
				t.Fatalf("mobileRelayURLFromEnvironment() after relaunch = %q, want %q", got, want)
			}
		})
	}
}

// A bundle that would be gone, or a different copy, once the app quits is
// never relaunched: the app would quit and not come back.
func TestRelaunchableBundleRefusesCopiesThatCannotReopen(t *testing.T) {
	bundle := filepath.Join(t.TempDir(), "Isoform.app")
	if err := os.Mkdir(bundle, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := relaunchableBundle(bundle); err != nil {
		t.Fatalf("relaunchableBundle(%q) = %v, want nil", bundle, err)
	}
	for _, path := range []string{
		"",
		"/private/var/folders/xy/T/AppTranslocation/1234/d/Isoform.app",
		"/Volumes/Isoform/Isoform.app",
		filepath.Join(t.TempDir(), "Missing.app"),
	} {
		if err := relaunchableBundle(path); err == nil {
			t.Errorf("relaunchableBundle(%q) = nil, want a refusal", path)
		}
	}
}

// Outside an app bundle (a test binary) RelaunchApp refuses and clears its
// guard, so a later tap can try again; while a reopen is under way a second
// call does nothing.
func TestRelaunchAppRunsOnce(t *testing.T) {
	a := &App{}
	for range 2 {
		if err := a.RelaunchApp(); err == nil {
			t.Fatal("RelaunchApp() outside an app bundle = nil, want an error")
		}
	}
	relaunchPending.Store(true)
	defer relaunchPending.Store(false)
	if err := a.RelaunchApp(); err != nil {
		t.Fatalf("RelaunchApp() while one is under way = %v, want nil", err)
	}
}
