package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCronCLITrustOnlyUsesAppBundleResource(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	write := func(path string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("fixture"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	executable := filepath.Join(home, "Isoform.app", "Contents", "MacOS", "Isoform")
	write(executable)
	// Neither the existing install folder nor a working-directory bundle is
	// an identity the desktop is allowed to trust implicitly.
	write(filepath.Join(home, configDirName, yansuCLIInstallRoot, "yansu"))
	write(filepath.Join(home, yansuCLIBundleDirName, "bin", "yansu"))
	if got, err := bundledCronCLIForExecutable(executable); err != nil || got != "" {
		t.Fatalf("unbundled CLI was trusted: %q, %v", got, err)
	}
	cli := filepath.Join(home, "Isoform.app", "Contents", "Resources", yansuCLIBundleDirName, "bin", "yansu")
	write(cli)
	want, err := filepath.EvalSymlinks(cli)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := bundledCronCLIForExecutable(executable); err != nil || got != want {
		t.Fatalf("bundled CLI = %q, %v; want %q", got, err, want)
	}
	plain := filepath.Join(home, "Isoform")
	write(plain)
	if got, err := bundledCronCLIForExecutable(plain); err != nil || got != "" {
		t.Fatalf("unpackaged app gained CLI trust: %q, %v", got, err)
	}
	if err := os.Remove(cli); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(executable, cli); err != nil {
		t.Fatal(err)
	}
	if got, err := bundledCronCLIForExecutable(executable); err == nil || got != "" {
		t.Fatalf("symlink received CLI trust: %q, %v", got, err)
	}
}
