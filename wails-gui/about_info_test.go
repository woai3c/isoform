package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync/atomic"
	"testing"
)

func TestAboutReleaseNotesStayWithSelectedUpdate(t *testing.T) {
	setReleaseTestBuild(t, appFlavorIsoform, buildChannelRelease)
	t.Setenv(agentHomeEnv, t.TempDir())
	payload := []byte("verified archive with version-specific notes")
	checksum := fmt.Sprintf("%x", sha256.Sum256(payload))
	const notes = "Today with **topics**, the pinned main chat panel, one Search with commands."
	var newer atomic.Bool
	var checks atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/asset" {
			_, _ = w.Write(payload)
			return
		}
		checks.Add(1)
		version, body := "0.1.303", notes
		if newer.Load() {
			version, body = "0.1.304", "A different release with different notes."
		}
		key := runtime.GOOS + "-" + displayArch()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"version": version, "release_notes": body,
			"updater_assets":    map[string]string{key: server.URL + "/asset"},
			"updater_checksums": map[string]string{key: checksum},
		})
	}))
	defer server.Close()
	R2ManifestURL = server.URL
	app := &App{updater: &updater{}}
	if err := app.SetBackgroundUpdates(false); err != nil {
		t.Fatal(err)
	}
	if info := app.CheckForUpdates(); !info.Available || info.ReleaseNotes != notes {
		t.Fatalf("check: %+v", info)
	}
	waitBackgroundDownload(t, app)
	assertNotes := func(t *testing.T, app *App, status string) {
		t.Helper()
		info, err := app.GetAboutInfo()
		if err != nil {
			t.Fatal(err)
		}
		if info.Update.Status != status || info.Update.Version != "0.1.303" {
			t.Fatalf("shown update changed: %+v", info.Update)
		}
		// Assert the Wails contract so the guard also fails against the old DTO.
		raw, err := json.Marshal(info)
		if err != nil {
			t.Fatal(err)
		}
		var wire map[string]any
		if err := json.Unmarshal(raw, &wire); err != nil {
			t.Fatal(err)
		}
		if wire["releaseNotes"] != notes || wire["releaseNotesVersion"] != "0.1.303" {
			t.Errorf("shown version lost its notes: %s", raw)
		}
		wantURL := "https://github.com/" + desktopReleasesRepo + "/releases/tag/v0.1.303"
		if info.ReleaseNotesURL != wantURL {
			t.Errorf("notes URL = %q, want immutable %q", info.ReleaseNotesURL, wantURL)
		}
	}
	t.Run("available", func(t *testing.T) { assertNotes(t, app, "update-available") })
	app.broadcastUpdateStatus(UpdateStatus{Status: "downloading", Version: "0.1.303", Percent: 42})
	t.Run("downloading", func(t *testing.T) { assertNotes(t, app, "downloading") })
	if err := app.DownloadUpdate(); err != nil {
		t.Fatal(err)
	}
	newer.Store(true)
	// Prove upstream really has advanced, without altering the staged selection.
	if upstream := app.fetchLatestRelease(); upstream.Latest != "0.1.304" || upstream.ReleaseNotes == notes {
		t.Fatalf("upstream did not advance: %+v", upstream)
	}
	before := checks.Load()
	if selected := app.CheckForUpdates(); selected.Latest != "0.1.303" {
		t.Fatalf("recheck changed downloaded selection: %+v", selected)
	}
	waitBackgroundDownload(t, app)
	t.Run("downloaded with newer upstream", func(t *testing.T) { assertNotes(t, app, "update-downloaded") })
	next := &App{updater: &updater{}}
	if !next.restorePendingUpdate() {
		t.Fatal("could not restore staged archive")
	}
	t.Run("restored with newer upstream", func(t *testing.T) { assertNotes(t, next, "update-downloaded") })
	if checks.Load() != before {
		t.Fatal("About or a staged recheck fetched a different release")
	}
}

func TestAboutInstalledVersionHasOwnReleaseNotesLink(t *testing.T) {
	setReleaseTestBuild(t, appFlavorIsoform, buildChannelRelease)
	t.Setenv(agentHomeEnv, t.TempDir())
	info, err := (&App{}).GetAboutInfo()
	if err != nil {
		t.Fatal(err)
	}
	want := "https://github.com/" + desktopReleasesRepo + "/releases/tag/v" + Version
	if info.ReleaseNotesURL != want {
		t.Fatalf("installed version notes = %q, want %q", info.ReleaseNotesURL, want)
	}
}

func TestAboutInstalledReleaseNotesOnlyUseMatchingVersion(t *testing.T) {
	for _, version := range []string{"0.1.302", "0.1.301"} {
		t.Run(version, func(t *testing.T) {
			setReleaseTestBuild(t, appFlavorIsoform, buildChannelRelease)
			t.Setenv(agentHomeEnv, t.TempDir())
			const notes = "The summary returned by this release's manifest."
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]string{"version": version, "release_notes": notes})
			}))
			defer server.Close()
			R2ManifestURL = server.URL
			app := &App{updater: &updater{}}
			if update := app.checkForUpdates(); update.Available || update.Error != "" {
				t.Fatalf("unexpected update: %+v", update)
			}
			info, err := app.GetAboutInfo()
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(info)
			if err != nil {
				t.Fatal(err)
			}
			var wire map[string]any
			if err := json.Unmarshal(raw, &wire); err != nil {
				t.Fatal(err)
			}
			wantNotes := ""
			if version == Version {
				wantNotes = notes
			}
			if wire["releaseNotes"] != wantNotes || wire["releaseNotesVersion"] != Version {
				t.Fatalf("installed release received mismatched or missing notes: %s", raw)
			}
		})
	}
}
