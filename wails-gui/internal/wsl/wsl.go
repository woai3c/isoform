//go:build windows

package wsl

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf16"

	"wails-gui/internal/platform/cmdutil"
)

// Distro represents an installed WSL distribution.
type Distro struct {
	Name      string `json:"name"`
	IsDefault bool   `json:"is_default"`
	State     string `json:"state"`   // "Running", "Stopped"
	Version   int    `json:"version"` // 1 or 2
}

// Available returns true if WSL is available on this system.
func Available() bool {
	_, err := exec.LookPath("wsl.exe")
	return err == nil
}

// ListDistros returns installed WSL distributions by parsing
// the output of wsl.exe --list --verbose.
func ListDistros() ([]Distro, error) {
	cmd := cmdutil.Command("wsl.exe", "--list", "--verbose")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("wsl --list --verbose: %w", err)
	}

	text := decodeUTF16(out)
	return parseListVerbose(text)
}

// DefaultDistro returns the name of the default WSL distribution.
func DefaultDistro() (string, error) {
	distros, err := ListDistros()
	if err != nil {
		return "", err
	}
	for _, d := range distros {
		if d.IsDefault {
			return d.Name, nil
		}
	}
	if len(distros) > 0 {
		return distros[0].Name, nil
	}
	return "", fmt.Errorf("no WSL distributions installed")
}

// ToLinuxPath converts a Windows path to a WSL-accessible path.
// Example: "D:\project" -> "/mnt/d/project"
// Paths already in Linux format (starting with /) are returned as-is.
func ToLinuxPath(windowsPath string) string {
	if strings.HasPrefix(windowsPath, "/") {
		return windowsPath
	}

	// Normalize backslashes.
	p := filepath.ToSlash(windowsPath)

	// Handle drive letter: "D:/foo" -> "/mnt/d/foo"
	if len(p) >= 2 && p[1] == ':' {
		drive := strings.ToLower(string(p[0]))
		rest := ""
		if len(p) > 2 {
			rest = p[2:]
		}
		// Ensure rest starts with /
		if !strings.HasPrefix(rest, "/") {
			rest = "/" + rest
		}
		return "/mnt/" + drive + rest
	}

	// UNC WSL path: //wsl$/Ubuntu/home/... -> /home/...
	// (after ToSlash, \\wsl$\Ubuntu\home becomes //wsl$/Ubuntu/home)
	for _, prefix := range []string{"//wsl$/", "//wsl.localhost/"} {
		if strings.HasPrefix(strings.ToLower(p), prefix) {
			rest := p[len(prefix):]
			// Skip distro name: "Ubuntu/home/..." -> "/home/..."
			if idx := strings.Index(rest, "/"); idx >= 0 {
				return rest[idx:]
			}
			return "/"
		}
	}

	return p
}

// ToWindowsPath converts a WSL Linux path to a Windows UNC path.
// Example: "/home/user/project" in distro "Ubuntu" -> "\\wsl$\Ubuntu\home\user\project"
func ToWindowsPath(distro, linuxPath string) string {
	if distro == "" {
		distro = "Ubuntu"
	}
	// Normalize: ensure leading /
	if !strings.HasPrefix(linuxPath, "/") {
		linuxPath = "/" + linuxPath
	}
	return `\\wsl$\` + distro + filepath.FromSlash(linuxPath)
}

// IsWSLPath returns true if the path is inside a WSL filesystem.
func IsWSLPath(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(path))
	return strings.HasPrefix(lower, "//wsl$/") ||
		strings.HasPrefix(lower, "//wsl.localhost/") ||
		strings.HasPrefix(strings.ToLower(path), `\\wsl$\`) ||
		strings.HasPrefix(strings.ToLower(path), `\\wsl.localhost\`)
}

// decodeUTF16 decodes UTF-16LE bytes (with optional BOM) to a Go string.
// wsl.exe --list --verbose outputs UTF-16LE on Windows.
func decodeUTF16(b []byte) string {
	// Strip BOM if present.
	if len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE {
		b = b[2:]
	}

	// Ensure even length.
	if len(b)%2 != 0 {
		b = b[:len(b)-1]
	}

	u16 := make([]uint16, len(b)/2)
	for i := range u16 {
		u16[i] = binary.LittleEndian.Uint16(b[i*2:])
	}

	return string(utf16.Decode(u16))
}

// parseListVerbose parses the text output of wsl.exe --list --verbose.
// Example output:
//
//	  NAME            STATE           VERSION
//	* Ubuntu-22.04    Running         2
//	  Debian          Stopped         2
func parseListVerbose(text string) ([]Distro, error) {
	lines := strings.Split(text, "\n")
	var distros []Distro

	for _, line := range lines {
		line = strings.TrimRight(line, "\r\n\t ")
		if line == "" {
			continue
		}

		// Skip header line (contains "NAME" and "STATE").
		upper := strings.ToUpper(line)
		if strings.Contains(upper, "NAME") && strings.Contains(upper, "STATE") {
			continue
		}

		// Check for default marker (*).
		isDefault := false
		trimmed := line
		if strings.HasPrefix(strings.TrimLeft(line, " "), "*") {
			isDefault = true
			trimmed = strings.TrimLeft(line, " ")
			trimmed = strings.TrimPrefix(trimmed, "*")
		}

		// Split remaining fields by whitespace.
		fields := strings.Fields(trimmed)
		if len(fields) < 3 {
			continue
		}

		name := fields[0]
		state := fields[1]
		version := 0
		if fields[2] == "1" {
			version = 1
		} else if fields[2] == "2" {
			version = 2
		}

		// Normalize state display.
		switch strings.ToLower(state) {
		case "running":
			state = "Running"
		case "stopped":
			state = "Stopped"
		}

		distros = append(distros, Distro{
			Name:      name,
			IsDefault: isDefault,
			State:     state,
			Version:   version,
		})
	}

	return distros, nil
}

// ShellCommand returns the command to launch a login shell inside a WSL distro.
// distro: distribution name (empty string = default distro).
// workDir: working directory in Linux path format (empty = WSL default).
func ShellCommand(distro, workDir string) (shell string, args []string) {
	shell = "wsl.exe"
	if distro != "" {
		args = append(args, "-d", distro)
	}
	if workDir != "" {
		args = append(args, "--cd", workDir)
	}
	args = append(args, "--", "bash", "-l")
	return shell, args
}

// ResolveJSRuntime probes the WSL distro for a JavaScript runtime (node or bun).
// Returns the path inside WSL (e.g. "/usr/bin/node").
func ResolveJSRuntime(distro string) (string, error) {
	for _, runtime := range []string{"node", "bun"} {
		args := []string{}
		if distro != "" {
			args = append(args, "-d", distro)
		}
		args = append(args, "--", "which", runtime)

		cmd := cmdutil.Command("wsl.exe", args...)
		out, err := cmd.Output()
		if err == nil {
			result := strings.TrimSpace(string(bytes.TrimRight(out, "\r\n")))
			if result != "" {
				return result, nil
			}
		}
	}
	return "", fmt.Errorf("no JS runtime (node or bun) found in WSL distro %q", distro)
}
