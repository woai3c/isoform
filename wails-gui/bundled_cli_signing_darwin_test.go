package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestDevBundledCLIResigningKeepsHardenedRuntime(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "iso-sign-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("HOME", home)

	script, err := os.ReadFile("scripts/sign-dev-build.sh")
	if err != nil {
		t.Fatal(err)
	}
	_, body, found := strings.Cut(string(script), "embed_dev_cli() {\n")
	if !found {
		t.Fatal("development CLI signing function is missing")
	}
	body, _, found = strings.Cut(body, "\n}\n")
	if !found {
		t.Fatal("development CLI signing function is incomplete")
	}

	// Exercise the actual signing function on a copied system executable.
	// No desktop application is built, signed, or launched by this fixture.
	source := filepath.Join(home, "source", yansuCLIBundleDirName, "bin", "yansu")
	if err := os.MkdirAll(filepath.Dir(source), 0700); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("/usr/bin/true")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, data, 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := "set -eu\nembed_dev_cli() {\n" + body + "\n}\nembed_dev_cli --sign -\n"
	cmd := exec.CommandContext(ctx, "/bin/bash", "--noprofile", "--norc", "-c", command)
	cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin", "WAILS_DIR=" + filepath.Join(home, "source"), "BUNDLE=" + filepath.Join(home, "fixture")}
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sign nested CLI: %v: %s", err, output)
	}
	target := filepath.Join(home, "fixture", "Contents", "Resources", yansuCLIBundleDirName, "bin", "yansu")
	if output, err := exec.CommandContext(ctx, "/usr/bin/codesign", "--verify", "--strict", target).CombinedOutput(); err != nil {
		t.Fatalf("verify nested CLI: %v: %s", err, output)
	}
	output, err := exec.CommandContext(ctx, "/usr/bin/codesign", "-d", "--verbose=4", target).CombinedOutput()
	if err != nil || !regexp.MustCompile(`(?m)^CodeDirectory .* flags=0x[0-9a-fA-F]+\([^)]*\bruntime\b`).Match(output) {
		t.Fatalf("nested CLI lost hardened runtime: %v: %s", err, output)
	}
}
