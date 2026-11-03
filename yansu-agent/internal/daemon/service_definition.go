package daemon

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"yansu-agent/internal/config"
)

// daemonIdentity keeps the published service stable while giving each other
// data folder its own service. Management commands use the same selection.
func daemonIdentity(root, home string) (label, unit string) {
	canonical := func(path string) string {
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			return resolved
		}
		return filepath.Clean(path)
	}
	root = canonical(root)
	switch root {
	case canonical(filepath.Join(home, config.ConfigDirName)):
		return "ai.yansu.daemon", "yansu-daemon.service"
	case canonical(filepath.Join(home, ".isoform-agent")):
		return "ai.isoform.daemon", "isoform-daemon.service"
	default:
		digest := sha256.Sum256([]byte(root))
		suffix := fmt.Sprintf("%x", digest[:8])
		return "ai.isoform.daemon." + suffix, "isoform-daemon-" + suffix + ".service"
	}
}

func daemonEnvironment(root string) []string {
	entries := []string{config.AgentHomeEnv + "=" + root}
	if value := os.Getenv(config.EnvBaseURL); value != "" {
		entries = append(entries, config.EnvBaseURL+"="+value)
	}
	return entries
}

func launchdServiceContents(binary, mode, root, label string) string {
	modeArgs := ""
	if mode != "" && mode != "direct" {
		modeArgs = fmt.Sprintf("\n        <string>--mode</string>\n        <string>%s</string>", xmlEscape(mode))
	}
	logPath := xmlEscape(filepath.Join(root, "logs", "daemon.log"))
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>%s</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
        <string>daemon</string>
        <string>start</string>%s
    </array>
    <key>EnvironmentVariables</key>
    <dict>
        <key>PATH</key>
        <string>%s</string>%s
    </dict>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>%s</string>
    <key>StandardErrorPath</key>
    <string>%s</string>
</dict>
</plist>
`, xmlEscape(label), xmlEscape(binary), modeArgs, xmlEscape(daemonEnvPath()), extraEnvPlistEntries(root), logPath, logPath)
}

// systemdQuote protects whitespace, escapes and literal percent characters from
// unit-file tokenization and specifier expansion.
func systemdQuote(value string) string {
	return strconv.Quote(strings.ReplaceAll(value, "%", "%%"))
}

func systemdServiceContents(binary, mode, root string) string {
	modeArg := ""
	if mode != "" && mode != "direct" {
		modeArg = " " + systemdQuote("--mode="+mode)
	}
	var environment strings.Builder
	for _, entry := range append([]string{"PATH=" + daemonEnvPath()}, daemonEnvironment(root)...) {
		fmt.Fprintf(&environment, "Environment=%s\n", systemdQuote(entry))
	}
	logPath := systemdQuote("append:" + filepath.Join(root, "logs", "daemon.log"))
	return fmt.Sprintf(`[Unit]
Description=Isoform Agent Daemon
After=network.target

[Service]
ExecStart=%s daemon start%s
Restart=always
RestartSec=5
%sStandardOutput=%s
StandardError=%s

[Install]
WantedBy=default.target
`, systemdQuote(binary), modeArg, environment.String(), logPath, logPath)
}
