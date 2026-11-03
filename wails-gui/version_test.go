package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDesktopUpdateFeedUsesReleasesRepository(t *testing.T) {
	if desktopReleasesRepo != "yubozhao/isoform-releases" {
		t.Fatalf("desktopReleasesRepo = %q, want this repository's release channel", desktopReleasesRepo)
	}
	feed, err := url.Parse(updateFeedURL())
	if err != nil {
		t.Fatal(err)
	}
	if feed.Scheme != "https" || feed.Host != "github.com" || feed.Path != "/"+desktopReleasesRepo+"/releases/latest/download/version.json" {
		t.Fatalf("update feed = %q, want latest release version.json in %s", updateFeedURL(), desktopReleasesRepo)
	}
}

func TestNewAppInitializesUpdaterStatus(t *testing.T) {
	app := NewApp()

	status := app.GetUpdateStatus()
	if status.Status != "idle" {
		t.Fatalf("expected idle updater status, got %q", status.Status)
	}
}

func TestGetUpdateStatusNilUpdaterReturnsIdle(t *testing.T) {
	app := &App{}

	status := app.GetUpdateStatus()
	if status.Status != "idle" {
		t.Fatalf("expected idle updater status, got %q", status.Status)
	}
}

func TestFetchFromR2PrefersUpdaterAssetsOverLegacyAssets(t *testing.T) {
	platformKey := runtime.GOOS + "-" + displayArch()
	app := appWithR2Manifest(t, map[string]any{
		"version":       "0.2.0",
		"release_notes": "test release",
		"assets": map[string]string{
			platformKey: "https://example.test/legacy.gz",
		},
		"checksums": map[string]string{
			platformKey: strings.Repeat("b", 64),
		},
		"updater_assets": map[string]string{
			platformKey: "https://example.test/archive",
		},
		"updater_checksums": map[string]string{
			platformKey: strings.Repeat("a", 64),
		},
	})

	info := app.fetchFromR2()
	if info.Error != "" {
		t.Fatalf("fetchFromR2() error = %q", info.Error)
	}
	if info.DownloadURL != "https://example.test/archive" {
		t.Fatalf("DownloadURL = %q, want update asset", info.DownloadURL)
	}
	if app.updater.expectedChecksum != strings.Repeat("a", 64) {
		t.Fatalf("expectedChecksum = %q, want update checksum", app.updater.expectedChecksum)
	}
}

func TestFetchFromR2FallsBackToAssetsWhenUpdaterAssetsAbsent(t *testing.T) {
	platformKey := runtime.GOOS + "-" + displayArch()
	app := appWithR2Manifest(t, map[string]any{
		"version":       "0.2.0",
		"release_notes": "test release",
		"assets": map[string]string{
			platformKey: "https://example.test/archive",
		},
		"checksums": map[string]string{
			platformKey: strings.Repeat("a", 64),
		},
	})

	info := app.fetchFromR2()
	if info.Error != "" {
		t.Fatalf("fetchFromR2() error = %q", info.Error)
	}
	if info.DownloadURL != "https://example.test/archive" {
		t.Fatalf("DownloadURL = %q, want legacy assets fallback", info.DownloadURL)
	}
	if app.updater.expectedChecksum != strings.Repeat("a", 64) {
		t.Fatalf("expectedChecksum = %q, want fallback checksum", app.updater.expectedChecksum)
	}
}

func TestFetchFromR2DoesNotFallbackToLegacyAssetsWhenUpdaterAssetsPresent(t *testing.T) {
	platformKey := runtime.GOOS + "-" + displayArch()
	otherPlatform := "windows-x64"
	if platformKey == otherPlatform {
		otherPlatform = "linux-x64"
	}

	app := appWithR2Manifest(t, map[string]any{
		"version":       "0.2.0",
		"release_notes": "test release",
		"assets": map[string]string{
			platformKey: "https://example.test/legacy.gz",
		},
		"updater_assets": map[string]string{
			otherPlatform: "https://example.test/other-archive",
		},
	})

	info := app.fetchFromR2()
	if !strings.Contains(info.Error, "No R2 update asset") {
		t.Fatalf("fetchFromR2() error = %q, want missing update asset", info.Error)
	}
}

func appWithR2Manifest(t *testing.T, manifest map[string]any) *App {
	t.Helper()

	oldManifestURL := R2ManifestURL
	oldVersion := Version

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(manifest); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
		default:
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
		}
	}))

	R2ManifestURL = server.URL
	Version = "0.1.0"
	t.Cleanup(func() {
		server.Close()
		R2ManifestURL = oldManifestURL
		Version = oldVersion
	})

	return &App{updater: &updater{status: UpdateStatus{Status: "idle"}}}
}

// mustWrite writes content to dir/rel, creating parents.
func mustWrite(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func readFile(t *testing.T, dir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

func TestReplaceDirFileWiseReplacesAndQuarantines(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")   // new git-bundle
	dst := filepath.Join(root, "dst")   // installed git-bundle
	aside := filepath.Join(root, "old") // quarantine/<name>

	// Old install has two files; the new bundle updates one, adds a nested one,
	// and drops "stale.txt" (present in dst, absent from src).
	mustWrite(t, dst, "bin/git.exe", "old-git")
	mustWrite(t, dst, "stale.txt", "removed-in-new-version")
	mustWrite(t, src, "bin/git.exe", "new-git")
	mustWrite(t, src, "share/template.txt", "brand-new")

	var undo []func()
	if err := replaceDirFileWise(src, dst, aside, &undo); err != nil {
		t.Fatalf("replaceDirFileWise: %v", err)
	}

	if got := readFile(t, dst, "bin/git.exe"); got != "new-git" {
		t.Errorf("git.exe = %q, want new-git", got)
	}
	if got := readFile(t, dst, "share/template.txt"); got != "brand-new" {
		t.Errorf("new nested file = %q, want brand-new", got)
	}
	// The replaced file's previous content is preserved in the quarantine.
	if got := readFile(t, aside, "bin/git.exe"); got != "old-git" {
		t.Errorf("quarantined git.exe = %q, want old-git", got)
	}
	// Files absent from the new bundle are quarantined, leaving dst an exact
	// mirror of the new bundle (no stale leftovers).
	if _, err := os.Stat(filepath.Join(dst, "stale.txt")); !os.IsNotExist(err) {
		t.Errorf("stale.txt should be removed from install dir, stat err = %v", err)
	}
	if got := readFile(t, aside, "stale.txt"); got != "removed-in-new-version" {
		t.Errorf("quarantined stale.txt = %q, want removed-in-new-version", got)
	}
}

func TestReplaceDirFileWiseUndoRestoresOriginal(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	dst := filepath.Join(root, "dst")
	aside := filepath.Join(root, "old")

	mustWrite(t, dst, "bin/git.exe", "old-git")
	mustWrite(t, src, "bin/git.exe", "new-git")
	mustWrite(t, src, "share/new.txt", "added")

	var undo []func()
	if err := replaceDirFileWise(src, dst, aside, &undo); err != nil {
		t.Fatalf("replaceDirFileWise: %v", err)
	}

	// Simulate a later top-level entry failing: replay undo in reverse.
	for i := len(undo) - 1; i >= 0; i-- {
		undo[i]()
	}

	if got := readFile(t, dst, "bin/git.exe"); got != "old-git" {
		t.Errorf("after rollback git.exe = %q, want old-git", got)
	}
	if _, err := os.Stat(filepath.Join(dst, "share", "new.txt")); !os.IsNotExist(err) {
		t.Errorf("added file should be removed on rollback, stat err = %v", err)
	}
}

func TestCleanLeakedUpdateRemnantsSweepsQuarantineDirs(t *testing.T) {
	dir := t.TempDir()
	// New-style quarantine dir, legacy sibling, extract staging, and a keeper.
	quarantine, err := os.MkdirTemp(dir, ".yansu-old-*")
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, quarantine, "bin/git.exe", "locked")
	mustWrite(t, dir, "git-bundle.yansu-old/inner", "legacy")
	if err := os.MkdirAll(filepath.Join(dir, ".yansu-update-abc"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, dir, "Yansu.exe", "keep me")

	cleanLeakedUpdateRemnants(dir)

	for _, gone := range []string{quarantine, filepath.Join(dir, "git-bundle.yansu-old"), filepath.Join(dir, ".yansu-update-abc")} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("expected %s removed, stat err = %v", gone, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "Yansu.exe")); err != nil {
		t.Errorf("Yansu.exe should survive cleanup: %v", err)
	}
}

// The Isoform dev flavor never reads the release feed: the feed only carries
// Yansu builds, and installing one would replace the dev app.
func TestFetchLatestReleaseSkipsTheIsoformFlavor(t *testing.T) {
	t.Setenv(appFlavorEnv, appFlavorIsoform)
	app := &App{}
	info := app.fetchLatestRelease()
	if info.Available || info.Error != "" {
		t.Fatalf("isoform flavor must report no update, got %+v", info)
	}
}
