package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoginItemBundlePath(t *testing.T) {
	for _, tt := range []struct {
		name      string
		flavor    string
		running   string
		installed string
		want      string
	}{
		{"isoform installed", appFlavorIsoform, "Isoform.app", "Isoform.app", "Isoform.app"},
		{"yansu installed", appFlavorYansu, "Yansu.app", "Yansu.app", "Yansu.app"},
		{"private installed", appFlavorYansu, "Yansu Private.app", "Yansu Private.app", "Yansu Private.app"},
		{"isoform ignores yansu", appFlavorIsoform, "Isoform.app", "Yansu.app", ""},
		{"yansu ignores isoform", appFlavorYansu, "Yansu.app", "Isoform.app", ""},
		{"private ignores release", appFlavorYansu, "Yansu Private.app", "Yansu.app", ""},
		{"no installed bundle", appFlavorIsoform, "Isoform.app", "", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home := loginItemTestHome(t)
			running := filepath.Join(home, "worktree", "build", "bin", tt.running)
			if tt.installed != "" {
				bundle := filepath.Join(home, "Applications", tt.installed)
				if err := os.MkdirAll(filepath.Join(bundle, "Contents"), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(bundle, "Contents", "Info.plist"), []byte(`<plist><dict><key>YansuLoginItemProtocolVersion</key><integer>1</integer></dict></plist>`), 0644); err != nil {
					t.Fatal(err)
				}
			}
			want := running
			if tt.want != "" {
				want = filepath.Join(home, "Applications", tt.want)
			}
			if got := loginItemBundlePath(home, tt.flavor, running); got != want {
				t.Fatalf("loginItemBundlePath = %q, want %q", got, want)
			}
		})
	}
}

func TestLoginItemBundlePathRejectsFile(t *testing.T) {
	home := loginItemTestHome(t)
	if err := os.Mkdir(filepath.Join(home, "Applications"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "Applications", "Isoform.app"), nil, 0644); err != nil {
		t.Fatal(err)
	}
	const running = "/worktree/Isoform.app"
	if got := loginItemBundlePath(home, appFlavorIsoform, running); got != running {
		t.Fatalf("regular file selected as a bundle: %q", got)
	}
	if got := loginItemBundlePath("", appFlavorIsoform, running); got != running {
		t.Fatalf("missing home did not keep running bundle: %q", got)
	}
}

func TestLoginItemBundlePathResolvesSymlink(t *testing.T) {
	home := loginItemTestHome(t)
	running := filepath.Join(home, "worktree", "Isoform.app")
	if err := os.MkdirAll(filepath.Join(running, "Contents"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(running, "Contents", "Info.plist"), []byte(`<plist><dict><key>YansuLoginItemProtocolVersion</key><integer>1</integer></dict></plist>`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(home, "Applications"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(running, filepath.Join(home, "Applications", "Isoform.app")); err != nil {
		t.Fatal(err)
	}
	if got := loginItemBundlePath(home, appFlavorIsoform, running); got != running {
		t.Fatalf("symlink would recurse into running bundle: %q", got)
	}
}

func TestLoginItemCommandAction(t *testing.T) {
	for _, tt := range []struct {
		name      string
		args      []string
		action    string
		requested bool
	}{
		{"normal launch", nil, "", false},
		{"other argument", []string{"--background"}, "", false},
		{"register", []string{loginItemCommandFlag, "register"}, "register", true},
		{"unregister", []string{loginItemCommandFlag, "unregister"}, "unregister", true},
		{"status", []string{loginItemCommandFlag, "status"}, "status", true},
		{"missing action", []string{loginItemCommandFlag}, "", true},
		{"invalid action", []string{loginItemCommandFlag, "launch"}, "", true},
		{"extra argument", []string{loginItemCommandFlag, "register", "extra"}, "", true},
		{"not first argument", []string{"--background", loginItemCommandFlag, "register"}, "", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			action, requested := loginItemCommandAction(tt.args)
			if action != tt.action || requested != tt.requested {
				t.Fatalf("got (%q, %v), want (%q, %v)", action, requested, tt.action, tt.requested)
			}
		})
	}
}

// macOS may expose its temporary folder through /var -> /private/var.
func loginItemTestHome(t *testing.T) string {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return home
}

func TestLoginItemBundleRequiresProtocol(t *testing.T) {
	for _, tt := range []struct {
		name       string
		plist      string
		compatible bool
	}{
		{"missing capability", `<plist><dict><key>CFBundleName</key><string>Isoform</string></dict></plist>`, false},
		{"current protocol", `<plist><dict><key>YansuLoginItemProtocolVersion</key><integer>1</integer></dict></plist>`, true},
		{"future protocol", `<plist><dict><key>YansuLoginItemProtocolVersion</key><integer>2</integer></dict></plist>`, false},
		{"duplicate capability", `<plist><dict><key>YansuLoginItemProtocolVersion</key><integer>1</integer><key>YansuLoginItemProtocolVersion</key><integer>2</integer></dict></plist>`, false},
		{"wrong type", `<plist><dict><key>YansuLoginItemProtocolVersion</key><string>1</string></dict></plist>`, false},
		{"nested capability", `<plist><dict><key>Other</key><dict><key>YansuLoginItemProtocolVersion</key><integer>1</integer></dict></dict></plist>`, false},
		{"malformed plist", `<plist><dict><key>YansuLoginItemProtocolVersion</key><integer>1</integer>`, false},
		{"no plist", "", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home := loginItemTestHome(t)
			installed := filepath.Join(home, "Applications", "Isoform.app")
			if err := os.MkdirAll(filepath.Join(installed, "Contents"), 0755); err != nil {
				t.Fatal(err)
			}
			if tt.plist != "" {
				if err := os.WriteFile(filepath.Join(installed, "Contents", "Info.plist"), []byte(tt.plist), 0644); err != nil {
					t.Fatal(err)
				}
			}
			running := filepath.Join(home, "worktree", "Isoform.app")
			want := running
			if tt.compatible {
				want = installed
			}
			if got := loginItemBundlePath(home, appFlavorIsoform, running); got != want {
				t.Fatalf("selected %q, want %q; an incompatible installed binary must not be launched", got, want)
			}
		})
	}
}
