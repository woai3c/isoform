package daemon

import (
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"yansu-agent/internal/config"
)

const (
	launchdPlistDir = "Library/LaunchAgents"
	systemdDir      = ".config/systemd/user"
)

// InstallService registers the yansu daemon as a user-level system service.
// mode controls the operation mode ("direct" or "relay"); empty defaults to "direct".
// force allows reinstalling even if the service is already installed.
func InstallService(yansuBinaryPath, mode string, force bool) error {
	switch runtime.GOOS {
	case "darwin":
		return installLaunchd(yansuBinaryPath, mode, force)
	case "linux":
		return installSystemd(yansuBinaryPath, mode, force)
	default:
		return fmt.Errorf("automatic service installation is not supported on %s\n\nManual setup: run `yansu daemon start` in a persistent terminal session (e.g. tmux or screen)", runtime.GOOS)
	}
}

// UninstallService removes the yansu daemon from system services.
func UninstallService() error {
	switch runtime.GOOS {
	case "darwin":
		return uninstallLaunchd()
	case "linux":
		return uninstallSystemd()
	default:
		return fmt.Errorf("service management not supported on %s", runtime.GOOS)
	}
}

// ServiceStatus prints the current service status.
func ServiceStatus() error {
	switch runtime.GOOS {
	case "darwin":
		return launchdStatus()
	case "linux":
		return systemdStatus()
	default:
		return fmt.Errorf("service management not supported on %s", runtime.GOOS)
	}
}

// StopService stops the running daemon without uninstalling it.
func StopService() error {
	switch runtime.GOOS {
	case "darwin":
		return stopLaunchd()
	case "linux":
		return stopSystemd()
	default:
		return fmt.Errorf("service management not supported on %s", runtime.GOOS)
	}
}

// RestartService stops and restarts the daemon.
func RestartService() error {
	switch runtime.GOOS {
	case "darwin":
		return restartLaunchd()
	case "linux":
		return restartSystemd()
	default:
		return fmt.Errorf("service management not supported on %s", runtime.GOOS)
	}
}

// ============================================================================
// macOS launchd
// ============================================================================

func launchdPlistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(home) {
		return "", fmt.Errorf("service user home must be an absolute path: %q", home)
	}
	root, err := config.ConfigDir()
	if err != nil {
		return "", err
	}
	label, _ := daemonIdentity(root, home)
	return filepath.Join(home, launchdPlistDir, label+".plist"), nil
}

func installLaunchd(yansuBinaryPath, mode string, force bool) error {
	plistPath, err := launchdPlistPath()
	if err != nil {
		return err
	}

	logDir, err := ensureLogDir()
	if err != nil {
		return err
	}

	// Re-install guard: handle all os.Stat outcomes explicitly
	_, statErr := os.Stat(plistPath)
	if statErr != nil && !os.IsNotExist(statErr) {
		return fmt.Errorf("failed to check existing service installation: %w", statErr)
	}
	if statErr == nil {
		// File exists — handle reinstall
		if !force {
			return fmt.Errorf("service is already installed — use --force to reinstall\n  Plist: %s", plistPath)
		}
		exec.Command("launchctl", "unload", plistPath).Run() //nolint:errcheck
	}

	root, err := config.ConfigDir()
	if err != nil {
		return err
	}
	launchdLabel := strings.TrimSuffix(filepath.Base(plistPath), ".plist")
	plist := launchdServiceContents(yansuBinaryPath, mode, root, launchdLabel)

	if err := os.MkdirAll(filepath.Dir(plistPath), 0755); err != nil {
		return fmt.Errorf("failed to create LaunchAgents directory: %w", err)
	}
	if err := os.WriteFile(plistPath, []byte(plist), 0644); err != nil {
		return fmt.Errorf("failed to write plist: %w", err)
	}

	// Load the service
	if err := exec.Command("launchctl", "load", plistPath).Run(); err != nil {
		return fmt.Errorf("launchctl load failed: %w\nPlist written to: %s", err, plistPath)
	}

	fmt.Printf("✓ Service installed and started (launchd)\n")
	fmt.Printf("  Plist: %s\n", plistPath)
	fmt.Printf("  Logs:  %s/daemon.log\n", logDir)
	fmt.Println()
	fmt.Println("The daemon will start automatically on login.")
	fmt.Printf("  Stop:    launchctl unload %s\n", plistPath)
	fmt.Printf("  Logs:    tail -f %s/daemon.log\n", logDir)
	fmt.Println()

	// Post-install health check
	fmt.Print("Waiting for daemon to start.")
	for range 4 {
		time.Sleep(500 * time.Millisecond)
		fmt.Print(".")
	}
	fmt.Println()
	if _, err := exec.Command("launchctl", "list", launchdLabel).Output(); err == nil {
		fmt.Println("✓ Daemon is running")
	} else {
		fmt.Printf("⚠ Daemon may not have started yet — check logs: tail -f %s/daemon.log\n", logDir)
	}
	return nil
}

func uninstallLaunchd() error {
	plistPath, err := launchdPlistPath()
	if err != nil {
		return err
	}
	// Unload (ignore error if not loaded)
	exec.Command("launchctl", "unload", plistPath).Run() //nolint:errcheck
	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove plist: %w", err)
	}
	fmt.Println("✓ Service uninstalled")
	return nil
}

func launchdStatus() error {
	plistPath, err := launchdPlistPath()
	if err != nil {
		return err
	}
	_, statErr := os.Stat(plistPath)
	installed := statErr == nil

	launchdLabel := strings.TrimSuffix(filepath.Base(plistPath), ".plist")
	out, err := exec.Command("launchctl", "list", launchdLabel).Output()
	if err != nil {
		if !installed {
			fmt.Println("Status: not installed")
			fmt.Println("  Run `yansu service install` to set up the daemon as a system service.")
		} else {
			fmt.Println("Status: stopped (installed but not loaded)")
			fmt.Printf("  Plist: %s\n", plistPath)
			fmt.Println("  Run `yansu service restart` to start it.")
		}
		return nil
	}

	raw := string(out)
	pid := parseLaunchdField(raw, "PID")
	exitStatus := parseLaunchdField(raw, "LastExitStatus")

	if pid != "" && pid != "0" {
		fmt.Printf("Status: running (PID %s)\n", pid)
	} else {
		if exitStatus != "" && exitStatus != "0" {
			fmt.Printf("Status: stopped (last exit code: %s)\n", exitStatus)
		} else {
			fmt.Println("Status: stopped")
		}
	}
	fmt.Printf("  Plist: %s\n", plistPath)
	logDir, _ := ensureLogDir()
	if logDir != "" {
		fmt.Printf("  Logs:  %s/daemon.log\n", logDir)
	}
	return nil
}

// parseLaunchdField extracts a field value from launchctl list output.
// The format is: "Key" = Value; where Value may be a number or quoted string.
func parseLaunchdField(raw, key string) string {
	needle := fmt.Sprintf(`"%s" = `, key)
	_, after, ok := strings.Cut(raw, needle)
	if !ok {
		return ""
	}
	val, _, ok := strings.Cut(after, ";")
	if !ok {
		return ""
	}
	val = strings.TrimSpace(val)
	if len(val) >= 2 && val[0] == '"' && val[len(val)-1] == '"' {
		val = val[1 : len(val)-1]
	}
	return val
}

func stopLaunchd() error {
	plistPath, err := launchdPlistPath()
	if err != nil {
		return err
	}
	if err := exec.Command("launchctl", "unload", plistPath).Run(); err != nil {
		return fmt.Errorf("launchctl unload failed: %w", err)
	}
	fmt.Println("✓ Service stopped")
	return nil
}

func restartLaunchd() error {
	plistPath, err := launchdPlistPath()
	if err != nil {
		return err
	}
	exec.Command("launchctl", "unload", plistPath).Run() //nolint:errcheck
	if err := exec.Command("launchctl", "load", plistPath).Run(); err != nil {
		return fmt.Errorf("launchctl load failed: %w", err)
	}
	fmt.Println("✓ Service restarted")
	return nil
}

// ============================================================================
// Linux systemd
// ============================================================================

func systemdServicePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(home) {
		return "", fmt.Errorf("service user home must be an absolute path: %q", home)
	}
	root, err := config.ConfigDir()
	if err != nil {
		return "", err
	}
	_, unit := daemonIdentity(root, home)
	return filepath.Join(home, systemdDir, unit), nil
}

func installSystemd(yansuBinaryPath, mode string, force bool) error {
	servicePath, err := systemdServicePath()
	if err != nil {
		return err
	}
	systemdUnit := filepath.Base(servicePath)

	logDir, err := ensureLogDir()
	if err != nil {
		return err
	}

	// Re-install guard: handle all os.Stat outcomes explicitly
	_, statErr := os.Stat(servicePath)
	if statErr != nil && !os.IsNotExist(statErr) {
		return fmt.Errorf("failed to check existing service installation: %w", statErr)
	}
	if statErr == nil {
		// File exists — handle reinstall
		if !force {
			return fmt.Errorf("service is already installed — use --force to reinstall\n  Unit: %s", servicePath)
		}
		exec.Command("systemctl", "--user", "stop", systemdUnit).Run() //nolint:errcheck
	}

	root, err := config.ConfigDir()
	if err != nil {
		return err
	}
	unit := systemdServiceContents(yansuBinaryPath, mode, root)

	if err := os.MkdirAll(filepath.Dir(servicePath), 0755); err != nil {
		return fmt.Errorf("failed to create systemd user directory: %w", err)
	}
	if err := os.WriteFile(servicePath, []byte(unit), 0644); err != nil {
		return fmt.Errorf("failed to write service file: %w", err)
	}

	// Reload and enable
	exec.Command("systemctl", "--user", "daemon-reload").Run() //nolint:errcheck
	if err := exec.Command("systemctl", "--user", "enable", "--now", systemdUnit).Run(); err != nil {
		return fmt.Errorf("systemctl enable failed: %w\nService file written to: %s", err, servicePath)
	}

	fmt.Printf("✓ Service installed and started (systemd)\n")
	fmt.Printf("  Unit:  %s\n", servicePath)
	fmt.Printf("  Logs:  %s/daemon.log\n", logDir)
	fmt.Println()
	fmt.Println("The daemon will start automatically on login.")
	fmt.Printf("  Stop:  systemctl --user stop %s\n", systemdUnit)
	fmt.Printf("  Logs:  tail -f %s/daemon.log\n", logDir)
	fmt.Println()

	// Post-install health check
	fmt.Print("Waiting for daemon to start.")
	for range 4 {
		time.Sleep(500 * time.Millisecond)
		fmt.Print(".")
	}
	fmt.Println()
	out, _ := exec.Command("systemctl", "--user", "is-active", systemdUnit).Output()
	if strings.TrimSpace(string(out)) == "active" {
		fmt.Println("✓ Daemon is running")
	} else {
		fmt.Printf("⚠ Daemon may not have started yet — check logs: tail -f %s/daemon.log\n", logDir)
	}
	return nil
}

func uninstallSystemd() error {
	servicePath, err := systemdServicePath()
	if err != nil {
		return err
	}
	systemdUnit := filepath.Base(servicePath)
	exec.Command("systemctl", "--user", "disable", "--now", systemdUnit).Run() //nolint:errcheck
	if err := os.Remove(servicePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove service file: %w", err)
	}
	exec.Command("systemctl", "--user", "daemon-reload").Run() //nolint:errcheck
	fmt.Println("✓ Service uninstalled")
	return nil
}

func systemdStatus() error {
	servicePath, err := systemdServicePath()
	if err != nil {
		return err
	}
	systemdUnit := filepath.Base(servicePath)
	out, _ := exec.Command("systemctl", "--user", "status", systemdUnit).Output()
	fmt.Printf("%s\n", strings.TrimSpace(string(out)))
	return nil
}

func stopSystemd() error {
	servicePath, err := systemdServicePath()
	if err != nil {
		return err
	}
	systemdUnit := filepath.Base(servicePath)
	if err := exec.Command("systemctl", "--user", "stop", systemdUnit).Run(); err != nil {
		return fmt.Errorf("systemctl stop failed: %w", err)
	}
	fmt.Println("✓ Service stopped")
	return nil
}

func restartSystemd() error {
	servicePath, err := systemdServicePath()
	if err != nil {
		return err
	}
	systemdUnit := filepath.Base(servicePath)
	if err := exec.Command("systemctl", "--user", "restart", systemdUnit).Run(); err != nil {
		return fmt.Errorf("systemctl restart failed: %w", err)
	}
	fmt.Println("✓ Service restarted")
	return nil
}

// ============================================================================
// Shared helpers
// ============================================================================

// xmlEscape returns s with XML special characters escaped for safe plist injection.
func xmlEscape(s string) string {
	var b strings.Builder
	xml.EscapeText(&b, []byte(s)) //nolint:errcheck
	return b.String()
}

// extraEnvPlistEntries returns additional plist <key>/<string> pairs for
// environment variables that the daemon needs but launchd doesn't inherit.
// Always persists the resolved data folder, including an inferred bundled root.
func extraEnvPlistEntries(root string) string {
	var sb strings.Builder
	for _, entry := range daemonEnvironment(root) {
		key, value, _ := strings.Cut(entry, "=")
		fmt.Fprintf(&sb, "\n        <key>%s</key>\n        <string>%s</string>", key, xmlEscape(value))
	}
	return sb.String()
}

// daemonEnvPath builds a PATH string for the service environment.
// It resolves the actual locations of known executor CLIs (claude, codex) at
// install time using LookPath, so launchd/systemd find them even though
// service processes don't inherit the user's shell PATH.
func daemonEnvPath() string {
	base := "/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin"
	home, err := os.UserHomeDir()
	if err != nil {
		return base
	}

	// Candidate dirs: resolved actual locations first, then well-known fallbacks.
	seen := map[string]bool{}
	var extra []string

	addDir := func(dir string) {
		if dir != "" && !seen[dir] {
			seen[dir] = true
			extra = append(extra, dir)
		}
	}

	// Resolve real install locations of executor CLIs at install time.
	for _, cli := range []string{"claude", "codex"} {
		if p, err := exec.LookPath(cli); err == nil {
			addDir(filepath.Dir(p))
		}
	}

	// Well-known fallback dirs (in case LookPath misses them).
	addDir(filepath.Join(home, ".claude", "bin"))
	addDir(filepath.Join(home, ".codex", "bin"))
	addDir(filepath.Join(home, ".local", "bin"))
	addDir(filepath.Join(home, "go", "bin"))

	if len(extra) == 0 {
		return base
	}
	return strings.Join(extra, ":") + ":" + base
}

func ensureLogDir() (string, error) {
	base, err := config.ConfigDir()
	if err != nil {
		return "", err
	}
	logDir := filepath.Join(base, "logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create log directory: %w", err)
	}
	return logDir, nil
}
