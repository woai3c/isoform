//go:build darwin

// Package keychaintest provides a real, isolated macOS Keychain for integration
// tests. It never changes the user's default Keychain or persistent search list.
package keychaintest

import (
	"context"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var fixtures sync.Map

// New creates an unlocked Keychain below the provided temporary HOME. The
// subprocess starts with that HOME so Security preferences also stay there.
func New(t *testing.T, home string) string {
	t.Helper()
	if home == "" || home == "/" {
		t.Fatal("isolated Keychain requires a temporary home")
	}
	path := filepath.Join(home, "fixture.keychain")
	run := func(args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "/usr/bin/security", args...)
		cmd.Env = append(os.Environ(), "HOME="+home)
		return cmd.CombinedOutput()
	}
	// This password protects only disposable test data, never a real credential.
	if output, err := run("create-keychain", "-p", "fixture-only!", path); err != nil {
		if os.Getenv("CI") != "" && keychainUnavailable(err) {
			t.Skipf("CI Keychain unavailable: %v: %s", err, output)
		}
		t.Fatalf("create isolated Keychain: %v: %s", err, output)
	}
	fixtures.Store(path, home)
	t.Cleanup(func() {
		fixtures.Delete(path)
		if output, err := run("delete-keychain", path); err != nil {
			t.Errorf("delete isolated Keychain: %v: %s", err, output)
		}
	})
	return path
}

// GrantCLI models explicit approval in a disposable modern Keychain. Ad-hoc
// executables have separate cdhash partitions even when both are in the item's
// trusted-application ACL. Changing that partition requires the Keychain's
// password; production code cannot silently grant it. This helper uses only
// the known fixture password and never changes the trusted-application ACL.
func GrantCLI(t *testing.T, keychain, account, cli string) {
	t.Helper()
	home, ok := fixtures.Load(keychain)
	if !ok || account == "" {
		t.Fatal("CLI partition approval requires an account in a Keychain created by keychaintest.New")
	}
	creator, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	partitions := "cdhash:" + codeHash(t, creator) + ",cdhash:" + codeHash(t, cli)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/security", "set-generic-password-partition-list",
		"-a", account, "-S", partitions, "-k", "fixture-only!", keychain)
	cmd.Env = append(os.Environ(), "HOME="+home.(string))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("approve exact CLI partition in disposable Keychain: %v: %s", err, output)
	}
}

func codeHash(t *testing.T, executable string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "/usr/bin/codesign", "-d", "--verbose=4", executable).CombinedOutput()
	if err != nil {
		t.Fatalf("read fixture executable signature: %v: %s", err, output)
	}
	for _, line := range strings.Split(string(output), "\n") {
		if !strings.HasPrefix(line, "CDHash=") {
			continue
		}
		hash := strings.TrimPrefix(line, "CDHash=")
		if decoded, err := hex.DecodeString(hash); err == nil && len(decoded) == 20 {
			return strings.ToLower(hash)
		}
	}
	t.Fatal("fixture executable has no valid CDHash")
	return ""
}

func keychainUnavailable(err error) bool {
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		return false
	}
	// security returns the low byte of OSStatus. Skip unavailable service,
	// missing default Keychain, failed authentication or prohibited UI only.
	switch exit.ExitCode() {
	case 29, 36, 37, 51, 53:
		return true
	default:
		return false
	}
}
