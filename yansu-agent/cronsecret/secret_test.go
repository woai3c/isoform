package cronsecret

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func isolatedHome(t *testing.T) string {
	t.Helper()
	home, err := os.MkdirTemp("/tmp", "ic-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	t.Setenv("HOME", home)
	return home
}

func TestCanonicalDirectoryResolvesExistingParents(t *testing.T) {
	home := isolatedHome(t)
	realDir := filepath.Join(home, "real")
	if err := os.Mkdir(realDir, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, "link")
	if err := os.Symlink(realDir, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	want, err := canonicalDir(filepath.Join(realDir, "missing"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := canonicalDir(filepath.Join(link, "missing"))
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("canonical directories differ: %q != %q", got, want)
	}
}

func TestStoreRejectsEmptyCredentialWithoutDeletingLegacy(t *testing.T) {
	home := isolatedHome(t)
	path := filepath.Join(home, "cron-secret")
	if err := os.WriteFile(path, []byte("previous-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Store(home, "  "); err == nil {
		t.Fatal("empty credential was accepted")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "previous-secret" {
		t.Fatalf("legacy credential changed after failed save: %v", err)
	}
}

func TestEmptyDirectoryRejected(t *testing.T) {
	if _, err := Load(""); err == nil {
		t.Fatal("Load accepted an empty directory")
	}
	if err := Store("", "secret"); err == nil {
		t.Fatal("Store accepted an empty directory")
	}
}

func TestReadLegacyRejectsCredentialLeafAlias(t *testing.T) {
	home := isolatedHome(t)
	trusted, alias := filepath.Join(home, "trusted"), filepath.Join(home, "alias")
	for _, dir := range []string{trusted, alias} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	source := filepath.Join(trusted, "cron-secret")
	if err := os.WriteFile(source, []byte("fixture-only-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(source, filepath.Join(alias, "cron-secret")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if secret, err := readLegacy(alias); err == nil || secret != "" {
		t.Fatal("credential alias borrowed another directory's secret")
	}
	if secret, err := readLegacy(trusted); err != nil || secret != "fixture-only-secret" {
		t.Fatalf("regular file credential became unavailable: %v", err)
	}
}

func TestVerifiedKeychainStorePreservesLegacyUntilReadBack(t *testing.T) {
	unavailable := fmt.Errorf("%w: Keychain status -25308", ErrUnavailable)
	for _, tt := range []struct {
		name     string
		writeErr error
		readErr  error
		stored   string
		wantErr  bool
	}{
		{name: "locked write", writeErr: unavailable, wantErr: true},
		{name: "locked read back", readErr: unavailable, wantErr: true},
		{name: "mismatched read back", stored: "previous-secret", wantErr: true},
		{name: "verified read back", stored: "replacement-secret"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			legacy := filepath.Join(dir, "cron-secret")
			if err := os.WriteFile(legacy, []byte("previous-secret"), 0o600); err != nil {
				t.Fatal(err)
			}
			assertLegacy := func() {
				t.Helper()
				data, err := os.ReadFile(legacy)
				if err != nil || string(data) != "previous-secret" {
					t.Fatalf("unverified write changed legacy credential: %v", err)
				}
			}
			wrote := false
			err := storeVerifiedKeychain(dir, "replacement-secret", func() error {
				assertLegacy()
				wrote = true
				return tt.writeErr
			}, func() (string, error) {
				if !wrote || tt.writeErr != nil {
					t.Fatal("read back before a successful write")
				}
				assertLegacy()
				return tt.stored, tt.readErr
			})
			if (err != nil) != tt.wantErr {
				t.Fatalf("store error = %v, want error = %v", err, tt.wantErr)
			}
			if tt.writeErr != nil || tt.readErr != nil {
				if !errors.Is(err, ErrUnavailable) {
					t.Fatalf("store lost unavailable status: %v", err)
				}
			}
			if tt.wantErr {
				assertLegacy()
			} else if _, err := os.Stat(legacy); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("verified migration retained legacy credential: %v", err)
			}
		})
	}
}

func TestVerifiedKeychainStoreRemovesLegacyAfterRecovery(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "cron-secret")
	if err := os.WriteFile(legacy, []byte("previous-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 3; attempt++ {
		err := storeVerifiedKeychain(dir, "replacement-secret", func() error {
			if attempt < 2 {
				return fmt.Errorf("%w: Keychain status -25308", ErrUnavailable)
			}
			return nil
		}, func() (string, error) {
			return "replacement-secret", nil
		})
		if attempt < 2 {
			if !errors.Is(err, ErrUnavailable) {
				t.Fatalf("locked attempt %d error = %v", attempt, err)
			}
			data, err := os.ReadFile(legacy)
			if err != nil || string(data) != "previous-secret" {
				t.Fatalf("locked attempt %d changed legacy credential: %v", attempt, err)
			}
		} else if err != nil {
			t.Fatalf("unlocked store failed: %v", err)
		}
	}
	if _, err := os.Stat(legacy); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("verified recovery retained legacy credential: %v", err)
	}
}

func TestVerifiedKeychainStoreKeepsWorkingWhenLegacyRemovalFails(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "cron-secret")
	// A nonempty directory makes removal fail even when tests run as root.
	if err := os.Mkdir(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	const oldSecret = "retained-legacy-secret"
	const newSecret = "verified-keychain-secret"
	retained := filepath.Join(legacy, "credential")
	if err := os.WriteFile(retained, []byte(oldSecret), 0o600); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	previousOutput := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previousOutput) })
	wrote, verified := false, false
	err := storeVerifiedKeychain(dir, newSecret, func() error {
		wrote = true
		return nil
	}, func() (string, error) {
		if !wrote {
			t.Fatal("read back before writing the credential")
		}
		verified = true
		return newSecret, nil
	})
	if err != nil {
		t.Errorf("verified Keychain storage failed because legacy removal failed: %v", err)
	}
	if !wrote || !verified {
		t.Error("credential must be written and read back before accepting cleanup failure")
	}
	if data, err := os.ReadFile(retained); err != nil || string(data) != oldSecret {
		t.Errorf("failed cleanup changed legacy data: %v", err)
	}
	message := logs.String()
	if !strings.Contains(message, "Keychain credential verified") || !strings.Contains(message, "could not remove legacy cron-secret") {
		t.Errorf("missing useful legacy cleanup diagnostic: %q", message)
	}
	if strings.Contains(message, oldSecret) || strings.Contains(message, newSecret) {
		t.Error("legacy cleanup diagnostic included a credential")
	}
}
