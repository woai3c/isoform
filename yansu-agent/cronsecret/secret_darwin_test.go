//go:build darwin && cgo

package cronsecret

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"yansu-agent/cronsecret/keychaintest"
)

func keychainHome(t *testing.T) (string, string) {
	t.Helper()
	home := isolatedHome(t)
	return home, keychaintest.New(t, home)
}

func requireKeychain(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	if os.Getenv("CI") != "" && errors.Is(err, ErrUnavailable) {
		t.Skipf("CI Keychain unavailable: %v", err)
	}
	t.Fatal(err)
}

func TestKeychainStoreMigratesLegacyThenReadsWithoutFile(t *testing.T) {
	home, keychain := keychainHome(t)
	legacy := filepath.Join(home, "cron-secret")
	if err := os.WriteFile(legacy, []byte("legacy-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	requireKeychain(t, Store(home, "legacy-secret", WithKeychain(keychain)))
	got, err := Load(home, WithKeychain(keychain))
	requireKeychain(t, err)
	if got != "legacy-secret" {
		t.Fatal("legacy secret did not survive migration")
	}
	if _, err := os.Stat(legacy); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy credential still exists: %v", err)
	}
	got, err = Load(home, WithKeychain(keychain))
	if err != nil || got != "legacy-secret" {
		t.Fatalf("read without credential file failed: %v", err)
	}

}

func TestKeychainStoreRotatesAndRemovesStaleFile(t *testing.T) {
	home, keychain := keychainHome(t)
	requireKeychain(t, Store(home, "first-secret", WithKeychain(keychain)))
	legacy := filepath.Join(home, "cron-secret")
	if err := os.WriteFile(legacy, []byte("stale-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	requireKeychain(t, Store(home, "rotated-secret", WithKeychain(keychain)))
	got, err := Load(home, WithKeychain(keychain))
	if err != nil || got != "rotated-secret" {
		t.Fatalf("rotated Keychain credential not read: %v", err)
	}
	if _, err := os.Stat(legacy); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale credential still exists: %v", err)
	}
}

func TestKeychainAccountsAreIsolatedByDataDirectory(t *testing.T) {
	home, keychain := keychainHome(t)
	requireKeychain(t, Store(home, "isolated-secret", WithKeychain(keychain)))
	other := filepath.Join(home, "other")
	if _, err := Load(other, WithKeychain(keychain)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("different data directory accessed a credential: %v", err)
	}
}

func TestKeychainFailurePreservesLegacyCredential(t *testing.T) {
	home := isolatedHome(t)
	legacy := filepath.Join(home, "cron-secret")
	if err := os.WriteFile(legacy, []byte("unmigrated-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	missing := WithKeychain(filepath.Join(home, "missing.keychain"))
	if err := Store(home, "replacement-secret", missing); err == nil {
		t.Fatal("storing to a missing Keychain succeeded")
	}
	if _, err := Load(home, missing); err == nil {
		t.Fatal("loading from a missing Keychain bypassed the store")
	}
	data, err := os.ReadFile(legacy)
	if err != nil || string(data) != "unmigrated-secret" {
		t.Fatalf("failed Keychain migration changed the legacy credential: %v", err)
	}
}
