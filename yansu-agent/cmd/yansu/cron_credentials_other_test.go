//go:build !darwin

package main

import (
	"os"
	"path/filepath"
	"testing"

	"yansu-agent/cronsecret"
)

func TestCronStoredCredentialsKeepTheirResolvedEndpoint(t *testing.T) {
	home := cronCredentialHome(t)
	t.Setenv("YANSU_CRON_SECRET", "")
	t.Setenv("YANSU_CRON_PORT", "12345")
	writeCronPort(t, home, "43210")
	if err := cronsecret.Store(home, "fake-local-secret"); err != nil {
		t.Fatal(err)
	}
	port, secret, err := loadCronCredentials()
	if err != nil || port != 43210 || secret != "fake-local-secret" {
		t.Fatalf("environment redirected stored secret: port=%d err=%v", port, err)
	}
	t.Setenv("YANSU_CRON_SECRET", "caller-provided-secret")
	port, secret, err = loadCronCredentials()
	if err != nil || port != 43210 || secret != "caller-provided-secret" {
		t.Fatalf("explicit secret bypassed the published port: port=%d err=%v", port, err)
	}
	t.Setenv("YANSU_CRON_SECRET", "")
	t.Run("directory alias keeps the published endpoint", func(t *testing.T) {
		alias := filepath.Join(home, "alias")
		if err := os.Symlink(home, alias); err != nil {
			t.Skipf("directory symlinks unavailable: %v", err)
		}
		t.Setenv("YANSU_AGENT_HOME", alias)
		port, secret, err := loadCronCredentials()
		if err != nil || port != 43210 || secret != "fake-local-secret" {
			t.Fatalf("directory alias changed the credential endpoint: port=%d err=%v", port, err)
		}
	})
	t.Run("secret alias cannot borrow another endpoint", func(t *testing.T) {
		other := filepath.Join(home, "other")
		if err := os.Mkdir(other, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(home, "cron-secret"), filepath.Join(other, "cron-secret")); err != nil {
			t.Skipf("file symlinks unavailable: %v", err)
		}
		writeCronPort(t, other, "12345")
		t.Setenv("YANSU_AGENT_HOME", other)
		if _, _, err := loadCronCredentials(); err == nil {
			t.Fatal("another data directory borrowed the stored credential through a file symlink")
		}
	})
}
