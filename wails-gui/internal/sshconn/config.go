package sshconn

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// Config holds SSH connection configuration for a single host.
type Config struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	User        string `json:"user"`
	AuthType    string `json:"auth_type"` // "key", "password"
	KeyPath     string `json:"key_path"`
	Password    string `json:"password"`
	ExecMode    string `json:"exec_mode"`    // "subscription", "own_plan"
	CLITool     string `json:"cli_tool"`     // "claude", "codex" (only for own_plan mode)
	AutoConnect bool   `json:"auto_connect"` // connect automatically on app startup
}

// ParseUserSSHConfig reads ~/.ssh/config and returns discovered hosts.
// Wildcard hosts (*) are skipped.
func ParseUserSSHConfig() ([]Config, error) {
	path := sshConfigPath()
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open ssh config: %w", err)
	}
	defer f.Close()

	var configs []Config
	var current *Config

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip comments and empty lines.
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, value := parseDirective(line)
		if key == "" {
			continue
		}

		switch key {
		case "host":
			// Flush previous host block.
			if current != nil {
				configs = appendConfig(configs, current)
			}

			// Skip wildcard patterns.
			if strings.Contains(value, "*") || strings.Contains(value, "?") {
				current = nil
				continue
			}

			current = &Config{
				Name:     value,
				Host:     value,
				Port:     22,
				AuthType: "key",
			}

		case "hostname":
			if current != nil {
				current.Host = value
			}

		case "port":
			if current != nil {
				if p, err := strconv.Atoi(value); err == nil {
					current.Port = p
				}
			}

		case "user":
			if current != nil {
				current.User = value
			}

		case "identityfile":
			if current != nil {
				current.KeyPath = expandPath(value)
				current.AuthType = "key"
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read ssh config: %w", err)
	}

	// Flush last block.
	if current != nil {
		configs = appendConfig(configs, current)
	}

	return configs, nil
}

// appendConfig adds a config only if it has a valid host.
func appendConfig(configs []Config, c *Config) []Config {
	if c.Host == "" {
		return configs
	}
	if c.ID == "" {
		c.ID = c.Name
	}
	return append(configs, *c)
}

// sshConfigPath returns the platform-appropriate SSH config path.
func sshConfigPath() string {
	if runtime.GOOS == "windows" {
		if profile := os.Getenv("USERPROFILE"); profile != "" {
			return filepath.Join(profile, ".ssh", "config")
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join("~", ".ssh", "config")
	}
	return filepath.Join(home, ".ssh", "config")
}

// parseDirective splits a line into key/value. Supports both "Key Value"
// and "Key=Value" formats. The key is lowercased.
func parseDirective(line string) (string, string) {
	// Handle Key=Value format.
	if idx := strings.IndexByte(line, '='); idx >= 0 {
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		return strings.ToLower(key), val
	}

	// Handle Key Value format (split on first whitespace).
	parts := strings.SplitN(line, " ", 2)
	if len(parts) < 2 {
		parts = strings.SplitN(line, "\t", 2)
	}
	if len(parts) < 2 {
		return strings.ToLower(parts[0]), ""
	}
	return strings.ToLower(strings.TrimSpace(parts[0])), strings.TrimSpace(parts[1])
}

// expandPath replaces ~ with the home directory.
func expandPath(p string) string {
	if !strings.HasPrefix(p, "~") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, p[1:])
}
