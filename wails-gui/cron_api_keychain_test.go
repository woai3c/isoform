//go:build darwin && cgo

package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"yansu-agent/cronsecret"
	"yansu-agent/cronsecret/keychaintest"
)

func TestCronAPICredentialsFromKeychain(t *testing.T) {
	_, server, home := newCronMcpTestServer(t)
	dir := filepath.Join(home, configDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	keychain := keychaintest.New(t, home)
	opts := cronsecret.WithKeychain(keychain)
	legacy := filepath.Join(dir, "cron-secret")
	if err := os.WriteFile(legacy, []byte("retired-test-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeCronAPIFiles(server.Port(), server.Secret(), opts); err != nil {
		if os.Getenv("CI") != "" && errors.Is(err, cronsecret.ErrUnavailable) {
			t.Skipf("Keychain unavailable in CI: %v", err)
		}
		t.Fatal(err)
	}
	secret, err := cronsecret.Load(dir, opts)
	if err != nil {
		// Publication can now be pending while an unavailable Keychain is
		// retried in the background. This integration fixture requires access.
		if os.Getenv("CI") != "" && errors.Is(err, cronsecret.ErrUnavailable) {
			t.Skipf("Keychain unavailable in CI: %v", err)
		}
		t.Fatal(err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy plaintext secret remains: %v", err)
	}
	if secret != server.Secret() {
		t.Fatal("Keychain credential differs from the running server")
	}
	port, err := os.ReadFile(filepath.Join(dir, "cron-port"))
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%s/mcp", strings.TrimSpace(string(port))), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+secret)
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Keychain-authenticated request status = %d", resp.StatusCode)
	}
}
