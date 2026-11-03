package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"wails-gui/storage"
)

func TestUpdateVersionStableChannel(t *testing.T) {
	for _, tt := range []struct {
		remote, local string
		want          bool
	}{
		{"0.2.0", "0.1.301", true}, {"v0.2.0", "v0.1.301", true},
		{"0.1.301", "0.2.0", false}, {"0.2.0", "0.2.0", false},
		{"0.1.302", "0.1.301", true}, {"1.0.0", "0.999.999", true},
		{"0.2.0-rc1", "0.1.301", false}, {"0.2.bad", "0.1.301", false},
		{"0.2", "0.1.301", false}, {"0.02.0", "0.1.301", false},
		{"0.2.0.1", "0.1.301", false}, {"0.2.0", "garbage", false},
	} {
		t.Run(tt.remote+"_"+tt.local, func(t *testing.T) {
			if got := isNewer(tt.remote, tt.local); got != tt.want {
				t.Fatalf("isNewer = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestUpdateFeedDerivedFromOneRepository(t *testing.T) {
	oldRepo, oldFeed := desktopReleasesRepo, R2ManifestURL
	t.Cleanup(func() { desktopReleasesRepo, R2ManifestURL = oldRepo, oldFeed })
	desktopReleasesRepo, R2ManifestURL = "example/releases", ""
	if got := updateFeedURL(); got != "https://github.com/example/releases/releases/latest/download/version.json" {
		t.Fatal(got)
	}
	R2ManifestURL = "https://example.test/version.json"
	if got := updateFeedURL(); got != R2ManifestURL {
		t.Fatal(got)
	}
}

func TestUpdateFeedRejectsMissingOrMalformedChecksum(t *testing.T) {
	for _, checksum := range []string{"", "bad", strings.Repeat("z", 64), strings.Repeat("a", 62)} {
		t.Run(checksum, func(t *testing.T) {
			app := appWithR2Manifest(t, map[string]any{"version": "0.2.0", "updater_assets": map[string]string{runtime.GOOS + "-" + displayArch(): "https://example.test/archive"}, "updater_checksums": map[string]string{runtime.GOOS + "-" + displayArch(): checksum}})
			if info := app.fetchFromR2(); info.Available || info.Error == "" {
				t.Fatalf("unverifiable feed offered update: %+v", info)
			}
		})
	}
}

func TestUpdateDownloadAndInstallReverifyBytes(t *testing.T) {
	payload := []byte("a downloaded zip or app archive")
	checksum := fmt.Sprintf("%x", sha256.Sum256(payload))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(payload) }))
	defer server.Close()
	app := &App{updater: &updater{expectedChecksum: checksum, targetVersion: "0.2.0"}}
	staging := filepath.Join(t.TempDir(), "update.zip")
	if err := app.downloadAttempt(server.Client(), server.URL, staging, 1); err != nil {
		t.Fatal(err)
	}
	if app.updater.downloadedChecksum != checksum || app.GetUpdateStatus().Status != "update-downloaded" {
		t.Fatal("download did not retain verified selection")
	}
	// The install must use the downloaded digest, even if a selected feed changes.
	app.updater.expectedChecksum = strings.Repeat("f", 64)
	if err := os.WriteFile(staging, []byte("tampered after download"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := app.QuitAndInstall(); err == nil || !strings.Contains(err.Error(), "expected") {
		t.Fatalf("tampered install: %v", err)
	}
	if app.GetUpdateStatus().Status != "error" || app.updater.stagingPath != "" {
		t.Fatal("failed install did not report and reset staging")
	}
	if _, err := os.Stat(staging); !os.IsNotExist(err) {
		t.Fatal("failed staging not discarded")
	}
}

func TestUpdateBundleRequiresIdentityNotarizationAndVersion(t *testing.T) {
	for fail := -1; fail < 5; fail++ {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			var calls []string
			run := func(name string, args ...string) ([]byte, error) {
				calls = append(calls, name+" "+strings.Join(args, " "))
				if len(calls)-1 == fail {
					return nil, errors.New("verification rejected")
				}
				switch len(calls) {
				case 2:
					return []byte("designated => identifier com.wails.Yansu and anchor apple generic and certificate leaf[subject.OU] = TEAM\n"), nil
				case 4:
					return []byte("accepted\nsource=Notarized Developer ID\n"), nil
				case 5:
					return []byte("0.2.0\n"), nil
				}
				return nil, nil
			}
			err := verifyUpdateBundleWithTools("/old.app", "/new.app", "0.2.0", run, func(string) (int, error) { return 0, nil })
			if fail >= 0 {
				if err == nil || len(calls) != fail+1 {
					t.Fatalf("verification did not stop: %v %v", err, calls)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for i, want := range []string{"1.2.840.113635.100.6.1.13", "-d -r- /old.app", "--deep --strict -R=identifier com.wails.Yansu", "--assess --type execute --verbose=2 /new.app", "CFBundleShortVersionString"} {
				if !strings.Contains(calls[i], want) {
					t.Fatalf("missing verification %q: %v", want, calls)
				}
			}
		})
	}
	for _, output := range []string{"", "0.3.0"} {
		run := func(name string, args ...string) ([]byte, error) {
			if strings.Contains(strings.Join(args, " "), "-r-") {
				return []byte("designated => identity"), nil
			}
			if name == "/usr/sbin/spctl" {
				return []byte("source=Notarized Developer ID"), nil
			}
			return []byte(output), nil
		}
		if err := verifyUpdateBundleWithTools("old", "new", "0.2.0", run, func(string) (int, error) { return 0, nil }); err == nil {
			t.Fatal("wrong signed version accepted")
		}
	}
}

func updateTestArchive(t *testing.T, headers []*tar.Header) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tr := tar.NewWriter(gz)
	for _, h := range headers {
		if err := tr.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			if _, err := tr.Write([]byte(strings.Repeat("x", int(h.Size)))); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tr.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func canonicalUpdateTempDir(t *testing.T) string {
	t.Helper()
	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestUpdateArchiveRefusesEscapesAndMultipleApps(t *testing.T) {
	for name, headers := range map[string][]*tar.Header{
		"traversal":      {{Name: "../escape", Typeflag: tar.TypeReg, Mode: 0600, Size: 1}},
		"absolute":       {{Name: "/escape", Typeflag: tar.TypeReg, Mode: 0600, Size: 1}},
		"outside":        {{Name: "Other.txt", Typeflag: tar.TypeReg, Size: 1}},
		"multiple":       {{Name: "A.app/", Typeflag: tar.TypeDir}, {Name: "B.app/", Typeflag: tar.TypeDir}},
		"symlink":        {{Name: "A.app/link", Typeflag: tar.TypeSymlink, Linkname: "../../escape"}},
		"symlink-parent": {{Name: "A.app/link", Typeflag: tar.TypeSymlink, Linkname: "dir"}, {Name: "A.app/link/child", Typeflag: tar.TypeReg, Size: 1}},
		"hardlink":       {{Name: "A.app/file", Typeflag: tar.TypeLink, Linkname: "/escape"}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := extractAppFromTarGz(bytes.NewReader(updateTestArchive(t, headers)), t.TempDir()); err == nil {
				t.Fatal("unsafe update extracted")
			}
		})
	}
	good := updateTestArchive(t, []*tar.Header{{Name: "A.app/Contents/file", Typeflag: tar.TypeReg, Mode: 0755, Size: 3}, {Name: "A.app/link", Typeflag: tar.TypeSymlink, Linkname: "Contents/file"}})
	if _, err := extractAppFromTarGz(bytes.NewReader(good), canonicalUpdateTempDir(t)); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateSignatureFailureLeavesRunningBundleAndDataIntact(t *testing.T) {
	root := t.TempDir()
	bundle, home := filepath.Join(root, "Yansu.app"), filepath.Join(root, "data")
	mustWrite(t, bundle, "Contents/MacOS/Yansu", "old")
	mustWrite(t, home, "keep.db", "user data")
	archive := filepath.Join(root, "update.tar.gz")
	if err := os.WriteFile(archive, updateTestArchive(t, []*tar.Header{{Name: "Yansu.app/Contents/MacOS/Yansu", Typeflag: tar.TypeReg, Mode: 0755, Size: 3}}), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := prepareBundleUpdate(archive, bundle, "0.2.0", home, func(string, string, string) error { return errors.New("bad signature or notarization") })
	if err == nil {
		t.Fatal("untrusted bundle accepted")
	}
	if readFile(t, bundle, "Contents/MacOS/Yansu") != "old" || readFile(t, home, "keep.db") != "user data" {
		t.Fatal("working state changed")
	}
}

func testBundleTransaction(t *testing.T) *bundleUpdate {
	t.Helper()
	parent := canonicalUpdateTempDir(t)
	root := filepath.Join(parent, ".yansu-transaction-test")
	bundle := filepath.Join(parent, "Yansu.app")
	candidate := filepath.Join(root, "next.app")
	mustWrite(t, bundle, "version", "old")
	mustWrite(t, candidate, "version", "new")
	home := filepath.Join(parent, "data")
	mustWrite(t, home, "keep.db", "keep")
	return &bundleUpdate{Root: root, Bundle: bundle, Candidate: candidate, Backup: filepath.Join(root, "previous.app"), Version: "0.2.0", DataHome: home}
}

func TestUpdateHelperStartAndSwapFailuresRestoreRunningBundle(t *testing.T) {
	for _, mode := range []string{"swap", "helper"} {
		t.Run(mode, func(t *testing.T) {
			txn := testBundleTransaction(t)
			if mode == "swap" {
				_ = os.RemoveAll(txn.Candidate)
			}
			err := txn.swapAndStart(func(u *bundleUpdate) error {
				if readFile(t, u.Backup, "version") != "old" {
					t.Fatal("backup deleted before helper launch")
				}
				return errors.New("cannot start helper")
			})
			if err == nil || readFile(t, txn.Bundle, "version") != "old" || readFile(t, txn.DataHome, "keep.db") != "keep" {
				t.Fatalf("failed transaction changed working state: %v", err)
			}
		})
	}
}

func TestUpdateChildProcess(t *testing.T) {
	if os.Getenv("UPDATE_TEST_CHILD") != "1" {
		return
	}
	if os.Getenv("UPDATE_TEST_EARLY_EXIT") == "1" {
		os.Exit(0)
	}
	if os.Getenv("UPDATE_TEST_MODEL_URL") != "" {
		state := newStorageStartupState()
		var store *storage.Store
		var downloadErr error
		state.start(openStartupStorage, func(opened *storage.Store) {
			store = opened
		}, func() {
			downloadErr = downloadFile(os.Getenv("UPDATE_TEST_MODEL_URL"), filepath.Join(os.Getenv(agentHomeEnv), "model"), "test model", nil)
		})
		<-state.done
		if store == nil || downloadErr != nil {
			os.Exit(3)
		}
		_ = store.Close()
		os.Exit(0)
	}
	if os.Getenv("UPDATE_TEST_NO_PROGRESS") == "1" {
		time.Sleep(10 * time.Second)
		os.Exit(4)
	}
	var ack updateStartup
	_ = json.Unmarshal([]byte(os.Getenv("UPDATE_TEST_ACK")), &ack)
	ack.PID = os.Getpid()
	if os.Getenv("UPDATE_TEST_WRONG_PID") == "1" {
		ack.PID++
	}
	if err := writeUpdateJSON(os.Getenv("UPDATE_TEST_PATH"), ack); err != nil {
		os.Exit(2)
	}
	time.Sleep(2 * time.Second)
	os.Exit(0)
}

func TestUpdateRelaunchAcknowledgementAndRollback(t *testing.T) {
	for _, mode := range []string{"success", "launch-failure", "early-exit", "no-progress", "wrong-version", "wrong-home", "wrong-bundle", "wrong-pid"} {
		t.Run(mode, func(t *testing.T) {
			txn := testBundleTransaction(t)
			if err := txn.swapAndStart(func(*bundleUpdate) error { return nil }); err != nil {
				t.Fatal(err)
			}
			reopened := false
			launch := func(u *bundleUpdate, path string) (*exec.Cmd, error) {
				if path == "" {
					reopened = true
					if readFile(t, u.Bundle, "version") != "old" {
						t.Fatal("rollback did not precede reopening")
					}
					return nil, nil
				}
				if mode == "launch-failure" {
					return nil, errors.New("launch rejected")
				}
				ack := updateStartup{Version: u.Version, DataHome: u.DataHome, Bundle: u.Bundle}
				if mode == "wrong-version" {
					ack.Version = "0.1.301"
				}
				if mode == "wrong-home" {
					ack.DataHome += "-other"
				}
				if mode == "wrong-bundle" {
					ack.Bundle += "-other"
				}
				data, _ := json.Marshal(ack)
				cmd := exec.Command(os.Args[0], "-test.run=^TestUpdateChildProcess$")
				cmd.Env = append(os.Environ(), "UPDATE_TEST_CHILD=1", "UPDATE_TEST_ACK="+string(data), "UPDATE_TEST_PATH="+path)
				if mode == "early-exit" {
					cmd.Env = append(cmd.Env, "UPDATE_TEST_EARLY_EXIT=1")
				}
				if mode == "wrong-pid" {
					cmd.Env = append(cmd.Env, "UPDATE_TEST_WRONG_PID=1")
				}
				if mode == "no-progress" {
					cmd.Env = append(cmd.Env, "UPDATE_TEST_NO_PROGRESS=1")
				}
				if err := cmd.Start(); err != nil {
					return nil, err
				}
				t.Cleanup(func() { _ = cmd.Process.Kill() })
				return cmd, nil
			}
			err := txn.relaunch(launch, 500*time.Millisecond)
			if mode == "success" {
				if err != nil || reopened || readFile(t, txn.Bundle, "version") != "new" {
					t.Fatalf("successful relaunch: %v", err)
				}
				if _, err := os.Stat(txn.Backup); !os.IsNotExist(err) {
					t.Fatal("backup not cleaned after startup acknowledgement")
				}
			} else {
				if err == nil || !reopened || readFile(t, txn.Bundle, "version") != "old" {
					t.Fatalf("failed relaunch: %v", err)
				}
				app := &App{updater: &updater{}}
				t.Setenv(agentHomeEnv, txn.DataHome)
				app.reportUpdateRecovery()
				if app.GetUpdateStatus().Status != "error" {
					t.Fatal("restored app has no user-visible error")
				}
			}
			if readFile(t, txn.DataHome, "keep.db") != "keep" {
				t.Fatal("update changed data home")
			}
		})
	}
}

func TestUpdateStartupRequiresReadyStorage(t *testing.T) {
	for _, mode := range []string{"ready", "failed", "stopped"} {
		t.Run(mode, func(t *testing.T) {
			home := canonicalUpdateTempDir(t)
			ackPath := filepath.Join(home, "started.json")
			t.Setenv(agentHomeEnv, home)
			t.Setenv(updateAckEnv, ackPath)
			state := newStorageStartupState()
			opening, allowOpen := make(chan struct{}), make(chan struct{})
			var published *storage.Store
			services := false
			state.start(func() (*storage.Store, error) {
				close(opening)
				<-allowOpen
				if mode == "failed" {
					return nil, errors.New("mandatory store unavailable")
				}
				return openStartupStorage()
			}, func(store *storage.Store) {
				if _, err := os.Stat(ackPath); !os.IsNotExist(err) {
					t.Error("acknowledged before mandatory storage was published")
				}
				published = store
			}, func() {
				services = true
				if published == nil || state.snapshot().Code != "" {
					t.Error("acknowledged before storage publication/readiness")
				}
				if _, err := os.Stat(ackPath); err != nil {
					t.Error("optional services started without core acknowledgement")
				}
				if err := published.SetUIState(t.Context(), "update-startup", "ready"); err != nil {
					t.Error(err)
				}
			})
			awaitStorageStartup(t, opening)
			if _, err := os.Stat(ackPath); !os.IsNotExist(err) {
				t.Error("pending mandatory storage acknowledged an update")
			}
			if mode == "stopped" {
				state.stop()
			}
			close(allowOpen)
			awaitStorageStartup(t, state.done)
			if published != nil {
				defer published.Close()
			}
			data, err := os.ReadFile(ackPath)
			if mode != "ready" {
				if services || !os.IsNotExist(err) {
					t.Fatal("failed/stopped mandatory storage acknowledged an update")
				}
				return
			}
			var ack updateStartup
			if err != nil || json.Unmarshal(data, &ack) != nil || !services ||
				ack.Version != Version || ack.DataHome != home || ack.PID != os.Getpid() || ack.Bundle != findAppBundle() {
				t.Fatalf("invalid core acknowledgement: %s, %v", data, err)
			}
		})
	}
}

func TestUpdateOptionalModelDownloadOutlivesStartupWatchdog(t *testing.T) {
	txn := testBundleTransaction(t)
	txn.Version = Version
	txn.Executable = "startup-test"
	// A test executable in a fixture bundle exercises real identity discovery
	// and mandatory storage startup, without starting Wails or optional services.
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(exe)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	path := filepath.Join(txn.Candidate, "Contents", "MacOS", txn.Executable)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	target, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0700)
	if err != nil {
		t.Fatal(err)
	}
	_, copyErr := io.Copy(target, source)
	if err := errors.Join(copyErr, target.Close()); err != nil {
		t.Fatal(err)
	}
	started, finish := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "12")
		_, _ = w.Write([]byte("slow "))
		w.(http.Flusher).Flush()
		close(started)
		<-finish
		_, _ = w.Write([]byte("model!!"))
	}))
	defer server.Close()
	defer close(finish)
	if err := txn.swapAndStart(func(*bundleUpdate) error { return nil }); err != nil {
		t.Fatal(err)
	}
	launch := func(u *bundleUpdate, ack string) (*exec.Cmd, error) {
		if ack == "" {
			t.Error("healthy optional model download triggered rollback")
			return nil, nil
		}
		cmd := exec.Command(filepath.Join(u.Bundle, "Contents", "MacOS", u.Executable), "-test.run=^TestUpdateChildProcess$")
		cmd.Env = append(os.Environ(), "UPDATE_TEST_CHILD=1", "UPDATE_TEST_MODEL_URL="+server.URL,
			agentHomeEnv+"="+u.DataHome, updateAckEnv+"="+ack)
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		t.Cleanup(func() { _ = cmd.Process.Kill() })
		return cmd, nil
	}
	const watchdog = 2 * time.Second
	if err := txn.relaunch(launch, watchdog); err != nil {
		t.Fatal(err)
	}
	awaitStorageStartup(t, started)
	// The download is held open beyond the entire watchdog, not just until
	// acknowledgement. It must still publish its complete model afterwards.
	time.Sleep(watchdog + time.Second)
	if _, err := os.Stat(filepath.Join(txn.DataHome, "model")); !os.IsNotExist(err) {
		t.Fatal("model unexpectedly completed before server released it")
	}
	finish <- struct{}{}
	deadline := time.After(5 * time.Second)
	for {
		data, err := os.ReadFile(filepath.Join(txn.DataHome, "model"))
		if err == nil && string(data) == "slow model!!" {
			break
		}
		select {
		case <-deadline:
			t.Fatal("healthy model download did not complete after the watchdog")
		case <-time.After(20 * time.Millisecond):
		}
	}
	if readFile(t, txn.Bundle, "version") != "new" || readFile(t, txn.DataHome, "keep.db") != "keep" {
		t.Fatal("healthy update changed bundle or data")
	}
	if _, err := os.Stat(txn.Root); !os.IsNotExist(err) {
		t.Fatal("core-ready update retained its transaction")
	}
}

func TestUpdateRepeatedCheckPreservesVerifiedDownload(t *testing.T) {
	setReleaseTestBuild(t, appFlavorIsoform, buildChannelRelease)
	t.Setenv(agentHomeEnv, t.TempDir())
	payload := []byte("verified archive")
	checksum := fmt.Sprintf("%x", sha256.Sum256(payload))
	asset := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(payload) }))
	defer asset.Close()
	key := runtime.GOOS + "-" + displayArch()
	app := appWithR2Manifest(t, map[string]any{"version": "0.2.0", "release_notes": "keep notes", "updater_assets": map[string]string{key: asset.URL}, "updater_checksums": map[string]string{key: checksum}})
	if err := app.SetBackgroundUpdates(false); err != nil {
		t.Fatal(err)
	}
	info := app.CheckForUpdates()
	if !info.Available {
		t.Fatalf("check: %+v", info)
	}
	path := filepath.Join(t.TempDir(), "archive")
	if err := app.downloadAttempt(asset.Client(), info.DownloadURL, path, 1); err != nil {
		t.Fatal(err)
	}
	R2ManifestURL = "http://127.0.0.1:1/must-not-fetch"
	if repeated := app.CheckForUpdates(); repeated.ReleaseNotes != "keep notes" || !repeated.Available {
		t.Fatalf("hydrate: %+v", repeated)
	}
	if app.GetUpdateStatus().Status != "update-downloaded" || app.updater.stagingPath != path {
		t.Fatal("release-note hydration discarded the verified update")
	}
	if err := verifySHA256(path, app.updater.downloadedChecksum); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateRecoveryMessageSurvivesBackgroundCheck(t *testing.T) {
	home := t.TempDir()
	t.Setenv(agentHomeEnv, home)
	t.Setenv(appFlavorEnv, appFlavorIsoform)
	if err := os.MkdirAll(filepath.Join(home, "updates"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeUpdateJSON(filepath.Join(home, "updates", "last-error.json"), "relaunch failed"); err != nil {
		t.Fatal(err)
	}
	app := &App{updater: &updater{}}
	app.reportUpdateRecovery()
	app.autoCheckForUpdates()
	if got := app.GetUpdateStatus(); got.Status != "error" || got.Detail != "relaunch failed" {
		t.Fatalf("rollback message lost: %+v", got)
	}
	app.CheckForUpdates()
	if app.updater.recoveryError {
		t.Fatal("manual retry did not clear recovery state")
	}
}

func TestUpdateRefusesADataHomeInsideTheBundle(t *testing.T) {
	root := canonicalUpdateTempDir(t)
	bundle := filepath.Join(root, "Yansu.app")
	mustWrite(t, bundle, "data/keep.db", "keep")
	home := filepath.Join(bundle, "data")
	if _, err := prepareBundleUpdate("unused", bundle, "0.2.0", home, func(string, string, string) error { return nil }); err == nil || !strings.Contains(err.Error(), "data home") {
		t.Fatalf("unsafe data home: %v", err)
	}
	if readFile(t, home, "keep.db") != "keep" {
		t.Fatal("data changed")
	}
}

func TestUpdateNotarizationCannotBeReplacedByOrdinaryAcceptance(t *testing.T) {
	for _, mode := range []string{"notarized", "ordinary", "quarantine-failure"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			run := func(name string, args ...string) ([]byte, error) {
				calls++
				if strings.Contains(strings.Join(args, " "), "-r-") {
					return []byte("designated => identity"), nil
				}
				if name == "/usr/sbin/spctl" {
					if mode == "ordinary" {
						return []byte("accepted\nsource=Developer ID"), nil
					}
					return []byte("accepted\nsource=Notarized Developer ID"), nil
				}
				return []byte("0.2.0"), nil
			}
			strip := func(path string) (int, error) {
				if calls != 3 || path != "new" {
					t.Fatal("quarantine changed before signature verification")
				}
				if mode == "quarantine-failure" {
					return 0, errors.New("cannot clear quarantine")
				}
				return 0, nil
			}
			err := verifyUpdateBundleWithTools("old", "new", "0.2.0", run, strip)
			if (err == nil) != (mode == "notarized") {
				t.Fatalf("%s: %v", mode, err)
			}
			if mode == "quarantine-failure" && calls != 3 {
				t.Fatal("continued after quarantine failure")
			}
		})
	}
}
