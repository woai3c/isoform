//go:build updateqa

package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestUpdateQAHookUsesRealCheckAndDownloadBindings(t *testing.T) {
	payload := []byte("local update payload")
	sum := fmt.Sprintf("%x", sha256.Sum256(payload))
	var feed *httptest.Server
	feed = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/archive" {
			_, _ = w.Write(payload)
			return
		}
		_ = json.NewEncoder(w).Encode(r2Manifest{Version: "0.2.0", UpdaterAssets: map[string]string{runtime.GOOS + "-" + displayArch(): feed.URL + "/archive"}, UpdaterChecksums: map[string]string{runtime.GOOS + "-" + displayArch(): sum}})
	}))
	defer feed.Close()
	oldFeed, oldVersion := R2ManifestURL, Version
	R2ManifestURL, Version = feed.URL, "0.1.301"
	t.Cleanup(func() { R2ManifestURL, Version = oldFeed, oldVersion })
	t.Setenv(agentHomeEnv, t.TempDir())
	t.Setenv("TMPDIR", canonicalUpdateTempDir(t))
	app := &App{updater: &updater{}}
	handler := updateQAHandler(app, "test-token")
	send := func(method, path, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		result := httptest.NewRecorder()
		handler.ServeHTTP(result, req)
		return result
	}
	if result := send("POST", "/check", "wrong"); result.Code != http.StatusUnauthorized {
		t.Fatal(result.Code)
	}
	if app.GetUpdateStatus().Status != "" {
		t.Fatal("unauthenticated request invoked binding")
	}
	if result := send("GET", "/check", "test-token"); result.Code != http.StatusMethodNotAllowed {
		t.Fatal(result.Code)
	}
	var status struct {
		TempDir string `json:"temp_dir"`
	}
	if result := send("GET", "/status", "test-token"); json.Unmarshal(result.Body.Bytes(), &status) != nil || status.TempDir != os.Getenv("TMPDIR") {
		t.Fatalf("status did not report inherited temporary directory: %s", result.Body.String())
	}
	result := send("POST", "/check", "test-token")
	var info UpdateInfo
	if err := json.Unmarshal(result.Body.Bytes(), &info); err != nil || !info.Available || info.Latest != "0.2.0" {
		t.Fatalf("check binding: %s %v", result.Body.String(), err)
	}
	result = send("POST", "/download", "test-token")
	if result.Code != http.StatusOK || app.GetUpdateStatus().Status != "update-downloaded" {
		t.Fatalf("download binding: %s", result.Body.String())
	}
	if got, err := os.ReadFile(filepath.Join(os.Getenv(agentHomeEnv), "updates", platformAssetName())); err != nil || string(got) != string(payload) {
		t.Fatalf("actual download: %s %v", got, err)
	}
}
