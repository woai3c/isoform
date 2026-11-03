package tools

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"yansu-agent/agent/tools/guard"
)

type trustedYansuCLIRegistration struct {
	installed string
	bundled   string
	digest    [sha256.Size]byte
}

var trustedYansuCLI struct {
	sync.RWMutex
	registration trustedYansuCLIRegistration
}

// RegisterTrustedYansuCLI grants a narrow Seatbelt credential-read exception
// to the app-installed CLI only. Callers must supply the app's bundled source,
// never an agent-selected executable. The hardened CLI checks its kernel
// sandbox status and refuses general file-reading commands in a sandbox.
func RegisterTrustedYansuCLI(installedPath, bundleSource string) error {
	trustedYansuCLI.Lock()
	defer trustedYansuCLI.Unlock()
	trustedYansuCLI.registration = trustedYansuCLIRegistration{}
	installed, err := filepath.EvalSymlinks(installedPath)
	if err != nil || !filepath.IsAbs(installed) {
		return fmt.Errorf("resolve installed CLI: %v", err)
	}
	bundled, err := filepath.EvalSymlinks(bundleSource)
	if err != nil || !filepath.IsAbs(bundled) {
		return fmt.Errorf("resolve bundled CLI: %v", err)
	}
	installedDigest, err := trustedCLIHash(installed)
	if err != nil {
		return err
	}
	bundledDigest, err := trustedCLIHash(bundled)
	if err != nil || installedDigest != bundledDigest {
		return fmt.Errorf("installed CLI does not match bundled source")
	}
	for _, path := range []string{bundled, installed} {
		if err := verifyTrustedCLISignature(path); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	probe := exec.CommandContext(ctx, installed, "--isoform-sandbox-capability")
	probe.Env = guard.ScrubSensitiveEnv(os.Environ())
	output, err := probe.CombinedOutput()
	if err != nil || strings.TrimSpace(string(output)) != "isoform-local-api-v1" {
		return fmt.Errorf("bundled CLI lacks kernel sandbox command enforcement")
	}
	// Detect replacement while signatures and capability were being checked.
	current, err := trustedCLIHash(installed)
	if err != nil || current != installedDigest {
		return fmt.Errorf("installed CLI changed during verification")
	}
	trustedYansuCLI.registration = trustedYansuCLIRegistration{installed, bundled, installedDigest}
	return nil
}

func trustedCLIHash(path string) ([sha256.Size]byte, error) {
	var digest [sha256.Size]byte
	f, err := os.Open(path)
	if err != nil {
		return digest, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode()&0111 == 0 {
		return digest, fmt.Errorf("CLI must be a regular executable")
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return digest, err
	}
	copy(digest[:], h.Sum(nil))
	return digest, nil
}

func verifyTrustedCLISignature(path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if output, err := exec.CommandContext(ctx, "/usr/bin/codesign", "--verify", "--strict", path).CombinedOutput(); err != nil {
		return fmt.Errorf("verify bundled CLI signature: %w: %.256s", err, output)
	}
	output, err := exec.CommandContext(ctx, "/usr/bin/codesign", "-d", "--verbose=4", path).CombinedOutput()
	if err != nil || !regexp.MustCompile(`(?m)^CodeDirectory .* flags=0x[0-9a-fA-F]+\([^)]*\bruntime\b`).Match(output) {
		return fmt.Errorf("bundled CLI requires the hardened runtime")
	}
	// Runtime flags are insufficient if entitlements permit injection/debugging.
	entitlements, err := exec.CommandContext(ctx, "/usr/bin/codesign", "-d", "--entitlements", ":-", path).CombinedOutput()
	if err != nil {
		return fmt.Errorf("inspect bundled CLI entitlements: %w", err)
	}
	for _, entitlement := range []string{
		"com.apple.security.cs.allow-dyld-environment-variables",
		"com.apple.security.cs.disable-library-validation",
		"com.apple.security.get-task-allow",
		"com.apple.security.cs.allow-unsigned-executable-memory",
		"com.apple.security.cs.allow-jit",
	} {
		if strings.Contains(string(entitlements), entitlement) {
			return fmt.Errorf("bundled CLI has an unsafe entitlement: %s", entitlement)
		}
	}
	return nil
}

// trustedYansuCLIProfile rehashes only the registered executable, with no tree
// walk. The caller pins both files and their ancestors against sandbox writes.
func trustedYansuCLIProfile() (string, []string) {
	trustedYansuCLI.RLock()
	registration := trustedYansuCLI.registration
	trustedYansuCLI.RUnlock()
	if registration.installed == "" {
		return "", nil
	}
	digest, err := trustedCLIHash(registration.installed)
	if err != nil || digest != registration.digest {
		log.Printf("[bash-sandbox] bundled CLI changed; credential exception disabled")
		return "", nil
	}
	return "(process-path " + strconv.Quote(registration.installed) + ")", []string{registration.installed, registration.bundled}
}

// Pin the registration for in-process writers too: a previously started
// background shell retains its profile even after another tool call begins.
func trustedYansuCLIWriteHit(path string) bool {
	trustedYansuCLI.RLock()
	registration := trustedYansuCLI.registration
	trustedYansuCLI.RUnlock()
	if registration.installed == "" {
		return false
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return true
	}
	canonical, err := canonicalPathLoose(absolute)
	if err != nil {
		return true
	}
	for _, candidate := range []string{stripFirmlink(filepath.Clean(absolute)), canonical} {
		for _, pinned := range []string{registration.installed, registration.bundled} {
			if foldedWithin(pinned, candidate) {
				return true
			}
		}
	}
	return false
}
