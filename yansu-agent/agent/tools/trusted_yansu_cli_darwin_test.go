//go:build darwin && cgo

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"yansu-agent/cronsecret"
	"yansu-agent/cronsecret/keychaintest"
	"yansu-agent/runtoken"
)

func TestBashTrustedBundledCLIUsesLocalAPIWithoutExposingCredentials(t *testing.T) {
	// Reuse the build cache without inheriting the real HOME into the CLI.
	cacheOutput, err := exec.Command("go", "env", "GOCACHE", "GOMODCACHE").Output()
	if err != nil {
		t.Fatal(err)
	}
	caches := strings.Fields(string(cacheOutput))
	if len(caches) != 2 {
		t.Fatalf("cannot resolve Go caches: %s", cacheOutput)
	}
	home, project := sandboxFixture(t)
	t.Setenv("YANSU_AGENT_HOME", filepath.Join(home, ".isoform-agent"))
	t.Setenv("YANSU_CRON_PORT", "")
	t.Setenv("YANSU_CRON_SECRET", "")
	// sandboxFixture's text sentinel is not a valid login Keychain. Native
	// Security.framework probes must not discover it as a damaged database.
	if err := os.Remove(filepath.Join(home, "Library", "Keychains", "login.keychain-db")); err != nil {
		t.Fatal(err)
	}
	// Keep the native fixture inside the same protected subtree as a real
	// login Keychain, without changing any default Keychain or search list.
	keychain := keychaintest.New(t, filepath.Join(home, "Library", "Keychains"))
	trustedYansuCLI.Lock()
	previous := trustedYansuCLI.registration
	trustedYansuCLI.registration = trustedYansuCLIRegistration{}
	trustedYansuCLI.Unlock()
	t.Cleanup(func() {
		trustedYansuCLI.Lock()
		trustedYansuCLI.registration = previous
		trustedYansuCLI.Unlock()
	})
	bundled := filepath.Join(home, "bundle", "yansu")
	if err := os.MkdirAll(filepath.Dir(bundled), 0700); err != nil {
		t.Fatal(err)
	}
	// Select only this disposable Keychain in the test binary. The overlay
	// retains the actual CLI credential loader, native Security.framework
	// calls, sandbox command gate and HTTP client without a production bypass.
	mainPath, err := filepath.Abs(filepath.Join("..", "..", "cmd", "yansu", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	mainSource, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	const credentialLoader = "func loadCronCredentials(opts ...cronsecret.Option) (int, string, error) {"
	if strings.Count(string(mainSource), credentialLoader) != 1 {
		t.Fatal("cannot locate the actual CLI credential loader for the Keychain fixture")
	}
	overlaySource := filepath.Join(home, "cli-main.go")
	fixtureSource := strings.Replace(string(mainSource), credentialLoader, credentialLoader+"\nopts = append(opts, cronsecret.WithKeychain("+strconv.Quote(keychain)+"))", 1)
	if err := os.WriteFile(overlaySource, []byte(fixtureSource), 0600); err != nil {
		t.Fatal(err)
	}
	overlay, err := json.Marshal(map[string]any{"Replace": map[string]string{mainPath: overlaySource}})
	if err != nil {
		t.Fatal(err)
	}
	overlayPath := filepath.Join(home, "cli-overlay.json")
	if err := os.WriteFile(overlayPath, overlay, 0600); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-p", "1", "-overlay", overlayPath, "-o", bundled, "./cmd/yansu")
	build.Dir = filepath.Join("..", "..")
	build.Env = append(os.Environ(), "GOCACHE="+caches[0], "GOMODCACHE="+caches[1], "CGO_ENABLED=1")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build actual CLI: %v: %s", err, output)
	}
	installed := filepath.Join(home, ".yansu-agent", "bin", "yansu")
	copyCLI := func() {
		t.Helper()
		data, err := os.ReadFile(bundled)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(installed), 0700); err != nil {
			t.Fatal(err)
		}
		// Replace the inode as the app installer does, so validation of the
		// earlier unsigned copy cannot leave stale code-identity cache state.
		temporary := installed + ".next"
		if err := os.WriteFile(temporary, data, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(temporary, installed); err != nil {
			t.Fatal(err)
		}
	}
	copyCLI()
	if err := RegisterTrustedYansuCLI(installed, bundled); err == nil {
		t.Fatal("non-hardened Go signature was trusted")
	}
	if output, err := exec.Command("/usr/bin/codesign", "--force", "--options", "runtime", "--sign", "-", "--identifier", "com.isoform.cronsecret-test.yansu", bundled).CombinedOutput(); err != nil {
		t.Fatalf("sign CLI: %v: %s", err, output)
	}
	copyCLI()
	if err := RegisterTrustedYansuCLI(installed, bundled); err != nil {
		t.Fatalf("register verified bundled CLI: %v", err)
	}
	dataDir := filepath.Join(home, ".isoform-agent")
	if err := cronsecret.Store(dataDir, sandboxSecret, cronsecret.WithKeychain(keychain), cronsecret.WithTrustedCLI(bundled)); err != nil {
		if os.Getenv("CI") != "" && errors.Is(err, cronsecret.ErrUnavailable) {
			t.Skipf("CI Keychain unavailable: %v", err)
		}
		t.Fatalf("store native CLI credential: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "cron-secret")); !os.IsNotExist(err) {
		t.Fatalf("Keychain migration left a plaintext credential: %v", err)
	}
	// Ad-hoc fixtures have distinct code-hash partitions, unlike release
	// binaries signed by the same team. Authorize only these test identities.
	keychaintest.GrantCLI(t, keychain, dataDir, bundled)
	// A retired file keeps the raw-read probes meaningful. It deliberately
	// holds the wrong API credential, so success requires the native Keychain.
	sandboxWrite(t, filepath.Join(dataDir, "cron-secret"), sandboxSecret+"_RETIRED")
	if filter, paths := trustedYansuCLIProfile(); filter == "" || len(paths) != 2 {
		t.Fatalf("trusted profile registration missing: %q %v", filter, paths)
	}
	for _, path := range []string{installed, bundled, filepath.Dir(installed), strings.ToUpper(installed)} {
		if !trustedYansuCLIWriteHit(path) {
			t.Fatalf("registered binary or ancestor was not pinned: %s", path)
		}
	}
	if _, err := NewWriteTool(nil).Execute(context.Background(), ToolCall{
		WorkDir: project,
		Input:   map[string]any{"path": installed, "content": "changed"},
	}); err == nil {
		t.Fatal("in-process Write accepted a registered executable")
	}
	var authorized atomic.Int32
	const scopedToken = "run-disposable-fixture"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+sandboxSecret && r.Header.Get("Authorization") != "Bearer "+scopedToken {
			http.Error(w, "bad credential", http.StatusUnauthorized)
			return
		}
		authorized.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/mcp" {
			w.Write([]byte(`[{"id":"probe","name":"trusted-cli-ok","health":"healthy","env":{"KEY":` + strconv.Quote(sandboxSecret) + `},"args":[` + strconv.Quote(sandboxSecret) + `],"last_error":` + strconv.Quote(sandboxSecret) + `}]`))
			return
		}
		w.Write([]byte(`{"local_api":"trusted-cli-ok"}`))
	}))
	defer server.Close()
	var stolen atomic.Int32
	attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stolen.Add(1)
	}))
	defer attacker.Close()
	sandboxWrite(t, filepath.Join(home, ".isoform-agent", "cron-port"), strings.TrimPrefix(server.URL, "http://127.0.0.1:"))
	attackerPort := strings.TrimPrefix(attacker.URL, "http://127.0.0.1:")
	// First prove the overlay, canonical account and native ACL work without
	// Seatbelt, so a subsequent failure isolates the sandbox boundary.
	for index, executable := range []string{bundled, installed} {
		controlCtx, cancelControl := context.WithTimeout(context.Background(), 15*time.Second)
		control := exec.CommandContext(controlCtx, executable, "cron", "list")
		control.Dir = project
		control.Env = os.Environ()
		controlOutput, controlErr := control.CombinedOutput()
		cancelControl()
		if controlErr != nil || !strings.Contains(string(controlOutput), "trusted-cli-ok") || strings.Contains(string(controlOutput), sandboxSecret) || authorized.Load() != int32(index+1) {
			t.Fatalf("unsandboxed signed CLI %s could not use its native Keychain credential: %v: %s", executable, controlErr, controlOutput)
		}
	}
	cli := strconv.Quote(installed)
	port, err := strconv.Atoi(strings.TrimPrefix(server.URL, "http://127.0.0.1:"))
	if err != nil {
		t.Fatal(err)
	}
	runCtx := runtoken.WithCredential(context.Background(), runtoken.Credential{Token: scopedToken, Port: port})
	profile, err := bashSeatbeltProfile(runCtx, ToolCall{WorkDir: project})
	if err != nil || strings.Contains(profile, "(allow mach-lookup") {
		t.Fatal("scoped run retained Keychain exception")
	}
	run := func(command string) ToolResult {
		t.Helper()
		result, err := NewBashTool().Execute(runCtx, ToolCall{WorkDir: project, Input: map[string]any{"command": command, "timeout": 15000}})
		if err != nil {
			t.Fatalf("execute %s: %v", command, err)
		}
		if strings.Contains(result.Content, sandboxSecret) {
			t.Fatalf("CLI leaked credential for %s: %s", command, result.Content)
		}
		return result
	}
	failed := func(result ToolResult) bool {
		return result.IsError || strings.Contains(result.Content, "(exit code: ")
	}
	for _, command := range []string{"cron list", "activity list", "memory list", "skill list", "mcp sources", "mcp list"} {
		result := run(cli + " " + command)
		if failed(result) || !strings.Contains(result.Content, "trusted-cli-ok") {
			t.Fatalf("local API failed for %s: %s", command, result.Content)
		}
	}
	// The Keychain ACL follows the signature, but the sandbox exception must
	// remain bound to the exact registered path, including for an intact copy.
	unregistered := filepath.Join(home, "unregistered-yansu")
	unregisteredBytes, err := os.ReadFile(bundled)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unregistered, unregisteredBytes, 0700); err != nil {
		t.Fatal(err)
	}
	beforeCopy := authorized.Load()
	if result := run("env -u YANSU_RUN_TOKEN -u YANSU_RUN_SCOPED " + strconv.Quote(unregistered) + " cron list"); !failed(result) || authorized.Load() != beforeCopy {
		t.Fatalf("unregistered signed copy used the native Keychain exception: %s", result.Content)
	}
	if result := run("env -u YANSU_RUN_TOKEN -u YANSU_RUN_SCOPED " + cli + " cron list"); !failed(result) || authorized.Load() != beforeCopy {
		t.Fatalf("clearing scoped env regained master credential: %s", result.Content)
	}
	// An injected library would inherit the CLI's process-path exception.
	// Its constructor must never run, independently of file-read denials.
	dylibSource := filepath.Join(home, "inject.c")
	dylib := filepath.Join(home, "inject.dylib")
	sandboxWrite(t, dylibSource, `
#include <unistd.h>
__attribute__((constructor)) static void capture(void) {
    const char marker[] = "UNSAFE_CLI_INJECTION";
    write(1, marker, sizeof(marker) - 1);
}
`)
	if output, err := exec.Command("/usr/bin/cc", "-dynamiclib", "-o", dylib, dylibSource).CombinedOutput(); err != nil {
		t.Fatalf("compile injection probe: %v: %s", err, output)
	}
	if output, err := exec.Command("/usr/bin/codesign", "--force", "--sign", "-", dylib).CombinedOutput(); err != nil {
		t.Fatalf("sign injection probe: %v: %s", err, output)
	}
	if result := run("DYLD_INSERT_LIBRARIES=" + strconv.Quote(dylib) + " " + cli + " cron list"); failed(result) || !strings.Contains(result.Content, "trusted-cli-ok") || strings.Contains(result.Content, "UNSAFE_CLI_INJECTION") {
		t.Fatalf("hardened CLI did not ignore injection: %s", result.Content)
	}
	for _, command := range []string{
		`cat "$YANSU_AGENT_HOME/cron-secret"`,
		`p="$YANSU_AGENT_HOME"; cat "$p/cron-"secret`,
		`p=$(printf '%s' "$HOME"); cat "$p/Library/Keychains"/fixture*`,
		`p="$YANSU_AGENT_HOME"; printf hijacked > "$p/cron-"port`,
		`p=` + cli + `; cp /bin/cat "$p"`,
	} {
		if result := run(command); !failed(result) {
			t.Fatalf("protected read/write succeeded: %s: %s", command, result.Content)
		}
	}
	result := run("YANSU_CRON_PORT=" + attackerPort + " " + cli + " cron list")
	if failed(result) || !strings.Contains(result.Content, "trusted-cli-ok") {
		t.Fatalf("stored endpoint was not retained: %s", result.Content)
	}
	alias := filepath.Join(home, "alias")
	sandboxWrite(t, filepath.Join(alias, "cron-port"), attackerPort)
	if err := os.Symlink(filepath.Join(home, ".isoform-agent", "cron-secret"), filepath.Join(alias, "cron-secret")); err != nil {
		t.Fatal(err)
	}
	result = run("YANSU_AGENT_HOME=" + strconv.Quote(alias) + " " + cli + " cron list")
	if failed(result) || !strings.Contains(result.Content, "trusted-cli-ok") || stolen.Load() != 0 {
		t.Fatalf("data-directory override redirected the scoped credential: %s", result.Content)
	}
	accountAlias := filepath.Join(home, "account-alias")
	if err := os.Symlink(dataDir, accountAlias); err != nil {
		t.Fatal(err)
	}
	result = run("YANSU_AGENT_HOME=" + strconv.Quote(accountAlias) + " " + cli + " cron list")
	if failed(result) || !strings.Contains(result.Content, "trusted-cli-ok") {
		t.Fatalf("same-directory alias did not retain Keychain account and endpoint: %s", result.Content)
	}
	// A raw token alias plus attacker-configured backend used to make status
	// a credential-reading deputy. Kernel sandbox detection blocks it first.
	if err := os.Symlink(filepath.Join(home, ".isoform-agent", "cron-secret"), filepath.Join(alias, "token")); err != nil {
		t.Fatal(err)
	}
	sandboxWrite(t, filepath.Join(alias, "config.json"), `{"api_base_url":`+strconv.Quote(attacker.URL)+`}`)
	for _, command := range []string{"status", "knowledge list", "hook install", "push"} {
		result = run("YANSU_AGENT_HOME=" + strconv.Quote(alias) + " " + cli + " " + command)
		if !failed(result) || !strings.Contains(result.Content, "sandbox allows only local API") {
			t.Fatalf("general CLI command was not refused before reads: %s: %s", command, result.Content)
		}
	}
	for _, command := range []string{
		`mcp install --source manual --id probe --command /bin/sh --args=-c,echo`,
		`mcp install --source manual --id probe --command /bin/sh --no-auto-start`,
		`mcp install --source local --ref cursor:probe`,
		`mcp enable probe`, `mcp restart probe`, `mcp disable probe`, `mcp stop probe`, `mcp uninstall probe`,
		`mcp registry refresh`, `mcp discover`, `mcp tail probe`,
		`skill install --source local --yes .`, `skill enable probe`, `skill disable probe`,
		`skill domains probe --chat true`, `skill uninstall probe`,
	} {
		before := authorized.Load()
		result = run(cli + " " + command)
		if !failed(result) || !strings.Contains(result.Content, "sandbox allows only local API") || authorized.Load() != before {
			t.Fatalf("unsafe API command reached desktop: %s: %s", command, result.Content)
		}
	}
	if stolen.Load() != 0 || authorized.Load() != 12 {
		t.Fatalf("unexpected API routing: trusted=%d attacker=%d", authorized.Load(), stolen.Load())
	}
	// A change outside the sandbox revokes the next profile's exception.
	if err := os.WriteFile(installed, []byte("#!/bin/sh\ncat \"$YANSU_AGENT_HOME/cron-secret\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if filter, _ := trustedYansuCLIProfile(); filter != "" {
		t.Fatal("changed registered executable retained trust")
	}
	if err := RegisterTrustedYansuCLI(installed, bundled); err == nil {
		t.Fatal("different installed executable registered")
	}
	if result = run(cli + " cron list"); !failed(result) {
		t.Fatalf("replacement credential reader succeeded: %s", result.Content)
	}
	// Even a valid hardened signature cannot opt out of library validation.
	entitlements := filepath.Join(home, "weak.plist")
	sandboxWrite(t, entitlements, `<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>com.apple.security.cs.allow-dyld-environment-variables</key><true/></dict></plist>`)
	if output, err := exec.Command("/usr/bin/codesign", "--force", "--options", "runtime", "--entitlements", entitlements, "--sign", "-", bundled).CombinedOutput(); err != nil {
		t.Fatalf("sign entitlement probe: %v: %s", err, output)
	}
	copyCLI()
	if err := RegisterTrustedYansuCLI(installed, bundled); err == nil || !strings.Contains(err.Error(), "unsafe entitlement") {
		t.Fatalf("unsafe runtime entitlement registered: %v", err)
	}
}
