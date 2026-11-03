package apphome

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDirHonorsOverrideAndFlavor(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, tc := range []struct {
		name, override, flavor, defaultName, want string
	}{
		{"release", "", "", "", filepath.Join(home, ".yansu-agent")},
		{"isoform tool", "", " ISOFORM ", "", filepath.Join(home, ".isoform-agent")},
		{"build flavor", "", "yansu", ".isoform-agent", filepath.Join(home, ".isoform-agent")},
		{"override", " " + filepath.Join(home, "isolated") + " ", "yansu", ".yansu-agent", filepath.Join(home, "isolated")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("YANSU_AGENT_HOME", tc.override)
			t.Setenv("YANSU_APP_FLAVOR", tc.flavor)
			got, err := Dir(tc.defaultName)
			if err != nil || got != tc.want {
				t.Fatalf("Dir = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		t.Fatalf("resolving paths must not create data: %v, %v", entries, err)
	}
}

func TestDirOverrideWorksWithoutUserHome(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	override := t.TempDir()
	t.Setenv("YANSU_AGENT_HOME", override)
	if got, err := Dir(".isoform-agent"); err != nil || got != override {
		t.Fatalf("Dir = %q, %v; want %q", got, err, override)
	}
	t.Setenv("YANSU_AGENT_HOME", "")
	if _, err := Dir(".isoform-agent"); err == nil {
		t.Fatal("missing both homes must fail")
	}
}

func TestDirRejectsRelativeOverridesAndHomes(t *testing.T) {
	for _, override := range []string{"relative", "~/.isoform-agent", "../data"} {
		t.Run(override, func(t *testing.T) {
			t.Setenv("YANSU_AGENT_HOME", override)
			if got, err := Dir(".isoform-agent"); err == nil || got != "" {
				t.Fatalf("invalid override resolved to %q, err=%v", got, err)
			}
		})
	}
	t.Setenv("YANSU_AGENT_HOME", "")
	t.Setenv("HOME", "relative-home")
	t.Setenv("USERPROFILE", "relative-home")
	if got, err := Dir(".isoform-agent"); err == nil || got != "" {
		t.Fatalf("relative user home resolved to %q, err=%v", got, err)
	}
}

func TestIsoformRejectsPublishedHomeAndAliases(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	legacy := filepath.Join(home, ".yansu-agent")
	if err := os.Mkdir(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, override := range []string{legacy, filepath.Join(legacy, "nested")} {
		t.Setenv("YANSU_AGENT_HOME", override)
		if got, err := Dir(".isoform-agent"); err == nil || got != "" {
			t.Errorf("published home accepted: %q, err=%v", got, err)
		}
	}
	alias := filepath.Join(home, "alias")
	if err := os.Symlink(legacy, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	t.Setenv("YANSU_AGENT_HOME", filepath.Join(alias, "new-child"))
	if got, err := Dir(".isoform-agent"); err == nil || got != "" {
		t.Errorf("published home alias accepted: %q, err=%v", got, err)
	}
	t.Setenv("YANSU_AGENT_HOME", "")
	if err := os.Symlink(legacy, filepath.Join(home, ".isoform-agent")); err != nil {
		t.Fatal(err)
	}
	if got, err := Dir(".isoform-agent"); err == nil || got != "" {
		t.Errorf("published default-home alias accepted: %q, err=%v", got, err)
	}
}

func TestDeveloperDirIgnoresInheritedReleaseFlavor(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("YANSU_APP_FLAVOR", "yansu")
	t.Setenv("YANSU_AGENT_HOME", "")
	if got, err := DeveloperDir(); err != nil || got != filepath.Join(home, ".isoform-agent") {
		t.Fatalf("developer root = %q, err=%v", got, err)
	}
	override := t.TempDir()
	t.Setenv("YANSU_AGENT_HOME", override)
	if got, err := DeveloperDir(); err != nil || got != override {
		t.Fatalf("developer override = %q, err=%v", got, err)
	}
}
