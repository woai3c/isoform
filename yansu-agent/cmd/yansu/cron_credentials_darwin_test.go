//go:build darwin && cgo

package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"yansu-agent/cronsecret"
	"yansu-agent/cronsecret/keychaintest"
)

func TestLoadCronCredentialsReadsKeychain(t *testing.T) {
	home := cronCredentialHome(t)
	writeCronPort(t, home, "12347")
	t.Setenv("YANSU_CRON_PORT", "12347")
	t.Setenv("YANSU_CRON_SECRET", "")
	keychain := keychaintest.New(t, home)
	opts := cronsecret.WithKeychain(keychain)
	if err := cronsecret.Store(home, "cli-keychain-secret", opts); err != nil {
		if os.Getenv("CI") != "" && errors.Is(err, cronsecret.ErrUnavailable) {
			t.Skipf("CI Keychain unavailable: %v", err)
		}
		t.Fatal(err)
	}
	port, secret, err := loadCronCredentials(opts)
	if err != nil || port != 12347 || secret != "cli-keychain-secret" {
		t.Fatalf("CLI did not load Keychain credential: port=%d err=%v", port, err)
	}
	if _, err := os.Stat(filepath.Join(home, "cron-secret")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("macOS credential leaked to a file: %v", err)
	}
}

func TestLoadCronCredentialsLeavesMigrationToDesktop(t *testing.T) {
	home := cronCredentialHome(t)
	writeCronPort(t, home, "12347")
	t.Setenv("YANSU_CRON_PORT", "12347")
	t.Setenv("YANSU_CRON_SECRET", "")
	keychain := keychaintest.New(t, home)
	legacy := filepath.Join(home, "cron-secret")
	if err := os.WriteFile(legacy, []byte("legacy-cli-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := loadCronCredentials(cronsecret.WithKeychain(keychain))
	if err == nil || !strings.Contains(err.Error(), "start the desktop app") {
		t.Fatalf("expected desktop migration guidance, got %v", err)
	}
	data, err := os.ReadFile(legacy)
	if err != nil || string(data) != "legacy-cli-secret" {
		t.Fatalf("CLI modified legacy credential: %v", err)
	}
}

func TestCronStoredCredentialsKeepTheirResolvedEndpoint(t *testing.T) {
	home := cronCredentialHome(t)
	t.Setenv("YANSU_CRON_SECRET", "")
	t.Setenv("YANSU_CRON_PORT", "12345")
	writeCronPort(t, home, "43210")
	opts := cronsecret.WithKeychain(keychaintest.New(t, home))
	if err := cronsecret.Store(home, "fake-local-secret", opts); err != nil {
		if os.Getenv("CI") != "" && errors.Is(err, cronsecret.ErrUnavailable) {
			t.Skipf("CI Keychain unavailable: %v", err)
		}
		t.Fatal(err)
	}
	port, secret, err := loadCronCredentials(opts)
	if err != nil || port != 43210 || secret != "fake-local-secret" {
		t.Fatalf("environment redirected stored secret: port=%d err=%v", port, err)
	}
	// A directory alias selects the same canonical Keychain account and its
	// published endpoint. There is no cron-secret file to resolve on macOS.
	alias := filepath.Join(home, "alias")
	if err := os.Symlink(home, alias); err != nil {
		t.Fatal(err)
	}
	t.Setenv("YANSU_AGENT_HOME", alias)
	port, secret, err = loadCronCredentials(opts)
	if err != nil || port != 43210 || secret != "fake-local-secret" {
		t.Fatalf("directory alias changed the credential endpoint: port=%d err=%v", port, err)
	}
	t.Setenv("YANSU_CRON_SECRET", "caller-provided-secret")
	port, secret, err = loadCronCredentials(opts)
	if err != nil || port != 43210 || secret != "caller-provided-secret" {
		t.Fatalf("explicit secret bypassed the published port: port=%d err=%v", port, err)
	}
	t.Setenv("YANSU_CRON_SECRET", "")
	other := filepath.Join(home, "other")
	if err := os.Mkdir(other, 0o700); err != nil {
		t.Fatal(err)
	}
	writeCronPort(t, other, "12345")
	t.Setenv("YANSU_AGENT_HOME", other)
	if _, _, err := loadCronCredentials(opts); err == nil {
		t.Fatal("another data directory borrowed the stored Keychain credential")
	}
}

func TestCronRequestDoesNotForwardCredentialsOnRedirect(t *testing.T) {
	home := cronCredentialHome(t)
	t.Setenv("YANSU_CRON_SECRET", "")
	opts := cronsecret.WithKeychain(keychaintest.New(t, home))
	if err := cronsecret.Store(home, "fake-local-secret", opts); err != nil {
		if os.Getenv("CI") != "" && errors.Is(err, cronsecret.ErrUnavailable) {
			t.Skipf("CI Keychain unavailable: %v", err)
		}
		t.Fatal(err)
	}
	var redirected, published atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirected.Add(1)
	}))
	defer target.Close()
	targetURL, _ := url.Parse(target.URL)
	t.Setenv("YANSU_CRON_PORT", targetURL.Port())
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		published.Add(1)
		if r.Header.Get("Authorization") != "Bearer fake-local-secret" {
			t.Error("published endpoint did not receive its Keychain credential")
		}
		http.Redirect(w, r, target.URL+"/capture", http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	sourceURL, _ := url.Parse(source.URL)
	writeCronPort(t, home, sourceURL.Port())
	resp, err := doCronRequest(http.MethodGet, "/cron/jobs", nil, "", opts)
	if resp != nil {
		resp.Body.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "redirects are not allowed") {
		t.Fatalf("expected redirect refusal, got %v", err)
	}
	if published.Load() != 1 || redirected.Load() != 0 {
		t.Fatalf("credential endpoint changed: published=%d redirected=%d", published.Load(), redirected.Load())
	}
}
