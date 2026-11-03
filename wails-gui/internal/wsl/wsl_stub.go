//go:build !windows

package wsl

// Distro represents an installed WSL distribution.
type Distro struct {
	Name      string `json:"name"`
	IsDefault bool   `json:"is_default"`
	State     string `json:"state"`
	Version   int    `json:"version"`
}

// Available always returns false on non-Windows platforms.
func Available() bool { return false }

// ListDistros returns nil on non-Windows platforms.
func ListDistros() ([]Distro, error) { return nil, nil }

// DefaultDistro returns an empty string on non-Windows platforms.
func DefaultDistro() (string, error) { return "", nil }

// ToLinuxPath returns the path as-is on non-Windows platforms.
func ToLinuxPath(path string) string { return path }

// ToWindowsPath returns the path as-is on non-Windows platforms.
func ToWindowsPath(_, path string) string { return path }

// IsWSLPath always returns false on non-Windows platforms.
func IsWSLPath(_ string) bool { return false }

// ShellCommand is not available on non-Windows platforms.
func ShellCommand(_, _ string) (string, []string) { return "", nil }

// ResolveJSRuntime is not available on non-Windows platforms.
func ResolveJSRuntime(_ string) (string, error) { return "", nil }
