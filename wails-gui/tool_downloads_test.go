package main

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestToolDownloadRequiresUnchangedUnexpiredSingleUsePlan(t *testing.T) {
	t.Setenv("PATH", os.Getenv("PATH"))
	var requests atomic.Int32
	archive := createTestZip(t, map[string]string{"cmd/git.exe": "git executable", "mingw64/libexec/git-core/helper.exe": "helper"})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = w.Write(archive)
	}))
	defer server.Close()
	sum := sha256.Sum256(archive)
	entry := ToolDownloadEntry{ID: "windows-git", DisplayName: "Git", Version: "test", SizeBytes: int64(len(archive)), Source: server.URL, Licence: "GPL-2.0", LicenceURL: server.URL + "/COPYING", SHA256: hex.EncodeToString(sum[:])}
	var plans toolDownloadPlans
	folder := filepath.Join(t.TempDir(), "git")
	plan, err := plans.plan(entry, folder)
	if err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 0 {
		t.Fatal("planning accessed network")
	}
	if _, err := os.Stat(folder); !os.IsNotExist(err) {
		t.Fatalf("planning created folder: %v", err)
	}
	if err := plans.startGit(ToolDownloadPlan{}); err == nil {
		t.Fatal("unconfirmed download accepted")
	}
	var other toolDownloadPlans
	if err := other.startGit(plan); err == nil {
		t.Fatal("plan from another App accepted")
	}
	for _, mutate := range []func(*ToolDownloadPlan){
		func(p *ToolDownloadPlan) { p.Folder += "-other" },
		func(p *ToolDownloadPlan) { p.Entry.ID = "other" },
		func(p *ToolDownloadPlan) { p.Entry.DisplayName = "other" },
		func(p *ToolDownloadPlan) { p.Entry.Version = "other" },
		func(p *ToolDownloadPlan) { p.Entry.Source += "/other" },
		func(p *ToolDownloadPlan) { p.Entry.SizeBytes++ },
		func(p *ToolDownloadPlan) { p.Entry.SHA256 = "other" },
		func(p *ToolDownloadPlan) { p.Entry.Licence = "other" },
		func(p *ToolDownloadPlan) { p.Entry.LicenceURL = "other" },
		func(p *ToolDownloadPlan) { p.Token = "other" },
		func(p *ToolDownloadPlan) { p.ExpiresAt++ },
	} {
		changed := plan
		mutate(&changed)
		if err := plans.startGit(changed); err == nil {
			t.Fatal("modified plan accepted")
		}
	}
	expired := plan
	expired.ExpiresAt = time.Now().Add(-time.Minute).UnixMilli()
	plans.pending[expired.Token] = expired
	if err := plans.startGit(expired); err == nil {
		t.Fatal("expired plan accepted")
	}
	plans.pending[plan.Token] = plan
	if requests.Load() != 0 {
		t.Fatal("unconfirmed or invalid plan accessed network")
	}
	oldGit := cachedResolvedGitPath()
	t.Cleanup(func() { setResolvedGitPath(oldGit) })
	if err := plans.startGit(plan); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Fatalf("requests=%d", requests.Load())
	}
	assertFileContent(t, filepath.Join(folder, "bin", "git.exe"), "git executable")
	assertFileContent(t, filepath.Join(folder, "mingw64", "libexec", "git-core", "helper.exe"), "helper")
	if err := plans.startGit(plan); err == nil {
		t.Fatal("replayed plan accepted")
	}
	if requests.Load() != 1 {
		t.Fatal("replayed plan accessed network")
	}
}

func TestToolDownloadRejectsWrongHashOrSizeBeforeInstalling(t *testing.T) {
	for _, problem := range []string{"size", "hash"} {
		t.Run(problem, func(t *testing.T) {
			payload := createTestZip(t, map[string]string{"cmd/git.exe": "bad"})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(payload) }))
			defer server.Close()
			sum := sha256.Sum256(payload)
			e := ToolDownloadEntry{ID: "windows-git", Source: server.URL, SizeBytes: int64(len(payload)), SHA256: hex.EncodeToString(sum[:])}
			if problem == "size" {
				e.SizeBytes--
			} else {
				e.SHA256 = strings.Repeat("0", 64)
			}
			var plans toolDownloadPlans
			folder := filepath.Join(t.TempDir(), "git")
			plan, err := plans.plan(e, folder)
			if err != nil {
				t.Fatal(err)
			}
			if err := plans.startGit(plan); err == nil {
				t.Fatal("corrupt download accepted")
			}
			if _, err := os.Stat(folder); !os.IsNotExist(err) {
				t.Fatalf("corrupt download installed: %v", err)
			}
		})
	}
}

// This is a backend retry contract for the future shared-sheet integration;
// it does not exercise or prove user confirmation in the UI.
func TestToolDownloadFailureRequiresFreshPlanBeforeRetry(t *testing.T) {
	t.Setenv("PATH", os.Getenv("PATH"))
	oldGit := cachedResolvedGitPath()
	t.Cleanup(func() { setResolvedGitPath(oldGit) })
	payload := createTestZip(t, map[string]string{"cmd/git.exe": "git executable"})
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write(payload)
	}))
	defer server.Close()
	sum := sha256.Sum256(payload)
	entry := ToolDownloadEntry{ID: "windows-git", Source: server.URL, SizeBytes: int64(len(payload)), SHA256: hex.EncodeToString(sum[:])}
	var plans toolDownloadPlans
	folder := filepath.Join(t.TempDir(), "git")
	first, err := plans.plan(entry, folder)
	if err != nil {
		t.Fatal(err)
	}
	if err := plans.startGit(first); err == nil {
		t.Fatal("failed download reported success")
	}
	if _, err := os.Stat(folder); !os.IsNotExist(err) {
		t.Fatalf("failed download installed: %v", err)
	}
	if err := plans.startGit(first); err == nil {
		t.Fatal("failed plan was reusable")
	}
	if requests.Load() != 1 {
		t.Fatal("reusing failed plan accessed network")
	}
	retry, err := plans.plan(entry, folder)
	if err != nil {
		t.Fatal(err)
	}
	if retry.Token == first.Token || retry.Entry != first.Entry || retry.Folder != first.Folder {
		t.Fatal("re-plan did not issue a fresh token for the same manifest and destination")
	}
	if requests.Load() != 1 {
		t.Fatal("re-planning accessed network")
	}
	if err := plans.startGit(retry); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 2 {
		t.Fatalf("requests=%d", requests.Load())
	}
	assertFileContent(t, filepath.Join(folder, "bin", "git.exe"), "git executable")
}

func TestGitReadinessPrefersLocalCandidatesAndNeverFetches(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	oldLookPath := gitLookPath
	oldResolved := cachedResolvedGitPath()
	t.Cleanup(func() {
		gitLookPath = oldLookPath
		setResolvedGitPath(oldResolved)
	})

	// A system git on PATH resolves locally; recovery is never offered for it.
	gitLookPath = func(string) (string, error) { return "/usr/bin/git", nil }
	setResolvedGitPath("")
	st := gitReadiness()
	if !st.Available || st.Path != "/usr/bin/git" || st.Recoverable {
		t.Fatalf("available git = %+v", st)
	}

	// No local candidate: recoverable only on the Windows platforms the
	// shipped MinGit manifest pins. Detection performs no network request.
	gitLookPath = func(string) (string, error) { return "", os.ErrNotExist }
	setResolvedGitPath("")
	st = gitReadiness()
	if st.Available {
		t.Fatalf("missing git reported available: %+v", st)
	}
	wantRecoverable := runtime.GOOS == "windows" && (runtime.GOARCH == "amd64" || runtime.GOARCH == "arm64")
	if st.Recoverable != wantRecoverable {
		t.Fatalf("missing git recoverable = %v, want %v", st.Recoverable, wantRecoverable)
	}
}

func TestGitDownloadManifestPinsBothWindowsArchitectures(t *testing.T) {
	for _, platform := range []string{"windows/amd64", "windows/arm64"} {
		e, err := gitDownloadEntry(platform)
		if err != nil {
			t.Fatal(err)
		}
		hash, err := hex.DecodeString(e.SHA256)
		if err != nil || len(hash) != 32 || e.SizeBytes <= 0 || e.Licence == "" || e.LicenceURL == "" || !strings.Contains(e.Source, "/v"+minGitRelease+"/") {
			t.Fatalf("manifest=%+v err=%v", e, err)
		}
	}
	if _, err := gitDownloadEntry("darwin/arm64"); err == nil {
		t.Fatal("non-Windows fallback accepted")
	}
	app := &App{}
	if _, err := app.PlanToolDownload("bun"); err == nil {
		t.Fatal("bundled runtime download accepted")
	}
	if err := app.StartToolDownload(ToolDownloadPlan{}); err == nil {
		t.Fatal("App allowed unconfirmed download")
	}
}
