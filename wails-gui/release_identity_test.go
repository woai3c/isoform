package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
)

// The release ships as Isoform: its update archives are named
// Isoform-<version>-<os>-<arch>, and both update paths must find them.
func TestUpdaterAssetNamesAreIsoform(t *testing.T) {
	name := platformAssetName()
	if !strings.HasPrefix(name, "Isoform-") || !strings.HasSuffix(name, platformAssetSuffix()) {
		t.Fatalf("staging name %q must be Isoform-<os>-<arch> with suffix %q", name, platformAssetSuffix())
	}
	released := "Isoform-0.1.303" + platformAssetSuffix()
	if !strings.HasSuffix(released, platformAssetSuffix()) {
		t.Fatalf("versioned release asset %q does not match the fallback suffix", released)
	}
	if _, ok := parseUpdateVersion("0.1.303"); !ok {
		t.Fatal("RC2 version must parse")
	}
}

func setReleaseTestBuild(t *testing.T, flavor, channel string) {
	t.Helper()
	previousFlavor, previousChannel, previousVersion, previousFeed := buildFlavor, buildChannel, Version, R2ManifestURL
	t.Cleanup(func() {
		buildFlavor, buildChannel, Version, R2ManifestURL = previousFlavor, previousChannel, previousVersion, previousFeed
	})
	t.Setenv(appFlavorEnv, "")
	buildFlavor, buildChannel, Version = flavor, channel, "0.1.302"
}

// releaseFeed serves a version.json shaped like desktop_release.py output.
func releaseFeed(t *testing.T, requests *int) *httptest.Server {
	t.Helper()
	platform := runtime.GOOS + "-" + displayArch()
	asset := "Isoform-0.1.303" + platformAssetSuffix()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"version":           "0.1.303",
			"release_notes":     "RC2",
			"updater_assets":    map[string]string{platform: "https://github.com/example/isoform-releases/releases/download/v0.1.303/" + asset},
			"updater_checksums": map[string]string{platform: strings.Repeat("a", 64)},
		})
	}))
	t.Cleanup(server.Close)
	return server
}

func TestIsoformReleaseBuildFindsTheIsoformUpdate(t *testing.T) {
	setReleaseTestBuild(t, appFlavorIsoform, buildChannelRelease)
	requests := 0
	R2ManifestURL = releaseFeed(t, &requests).URL
	if !isReleaseChannel() || productName() != "Isoform" {
		t.Fatalf("release build identity: release=%v name=%q", isReleaseChannel(), productName())
	}
	app := &App{updater: &updater{}}
	info := app.fetchLatestRelease()
	if !info.Available || info.Error != "" || info.Latest != "0.1.303" {
		t.Fatalf("release build did not offer RC2: %+v", info)
	}
	if !strings.HasSuffix(info.DownloadURL, "/Isoform-0.1.303"+platformAssetSuffix()) {
		t.Fatalf("download URL %q is not the Isoform archive", info.DownloadURL)
	}
	if requests != 1 {
		t.Fatalf("feed requests = %d, want 1", requests)
	}
}

func TestIsoformDevelopmentBuildNeverChecksForUpdates(t *testing.T) {
	setReleaseTestBuild(t, appFlavorIsoform, "")
	requests := 0
	R2ManifestURL = releaseFeed(t, &requests).URL
	app := &App{updater: &updater{}}
	if info := app.fetchLatestRelease(); info.Available || info.Error != "" {
		t.Fatalf("development build offered an update: %+v", info)
	}
	if requests != 0 {
		t.Fatalf("development build contacted the release feed %d times", requests)
	}
}
