package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"yansu-agent/cronsecret"
)

// cronCLIKeychainOptions trusts only the CLI shipped inside this app bundle.
// Discovery through PATH, the working directory or the user's install folder
// must never grant another executable access to the scheduler credential.
func cronCLIKeychainOptions() []cronsecret.Option {
	if runtime.GOOS != "darwin" {
		return nil
	}
	executable, err := os.Executable()
	if err != nil {
		log.Printf("[cron-api] cannot locate bundled CLI: %v", err)
		return nil
	}
	cli, err := bundledCronCLIForExecutable(executable)
	if err != nil {
		log.Printf("[cron-api] bundled CLI will not receive Keychain access: %v", err)
		return nil
	}
	if cli == "" {
		return nil
	}
	// The credential store also verifies the running app's sealed code and the
	// CLI signature before including this exact executable in the access list.
	return []cronsecret.Option{cronsecret.WithTrustedCLI(cli)}
}

func bundledCronCLIForExecutable(executable string) (string, error) {
	executable, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return "", err
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return "", err
	}
	macOSDir := filepath.Dir(executable)
	contentsDir := filepath.Dir(macOSDir)
	bundleDir := filepath.Dir(contentsDir)
	if filepath.Base(macOSDir) != "MacOS" || filepath.Base(contentsDir) != "Contents" || !strings.HasSuffix(bundleDir, ".app") {
		return "", nil
	}
	candidate := filepath.Join(contentsDir, "Resources", yansuCLIBundleDirName, "bin", "yansu")
	info, err := os.Lstat(candidate)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return "", fmt.Errorf("bundled CLI is not a regular executable")
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", err
	}
	if resolved != candidate {
		return "", fmt.Errorf("bundled CLI path contains a symlink")
	}
	return candidate, nil
}
