//go:build windows

package shellenv

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// LoginShell returns the default command interpreter on Windows.
func LoginShell() string {
	if comspec := os.Getenv("COMSPEC"); comspec != "" {
		return comspec
	}
	if ps, _ := exec.LookPath("powershell.exe"); ps != "" {
		return ps
	}
	return "cmd.exe"
}

// resolveUserPATH returns the current PATH directly on Windows.
// Windows GUI apps inherit the full system+user PATH, so login-shell
// resolution is unnecessary.
func resolveUserPATH() string {
	return os.Getenv("PATH")
}

func commandAvailableViaLoginShell(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func resolveCommandPathViaLoginShell(name string) string {
	if strings.TrimSpace(name) == "" {
		return ""
	}
	resolved, err := exec.LookPath(name)
	if err != nil {
		return ""
	}
	return resolved
}

func resolveEnvVarViaLoginShell(name string) string {
	if !isSafeEnvVarName(name) {
		return ""
	}
	return os.Getenv(name)
}

func resolveCommandPathInCommonDirs(name string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	localAppData := os.Getenv("LOCALAPPDATA")
	programFiles := os.Getenv("ProgramFiles")

	var dirs []string
	if localAppData != "" {
		dirs = append(dirs, filepath.Join(localAppData, "Programs"))
	}
	if programFiles != "" {
		dirs = append(dirs, programFiles)
	}
	dirs = append(dirs,
		filepath.Join(home, ".bun", "bin"),
		filepath.Join(home, ".local", "bin"),
		filepath.Join(home, "AppData", "Roaming", "npm"),
	)

	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		for _, ext := range []string{".exe", ".cmd", ".bat", ".com"} {
			candidate := filepath.Join(dir, name+ext)
			if IsExecutableFile(candidate) {
				return candidate
			}
		}
		// Also try without extension (e.g. already has .exe).
		candidate := filepath.Join(dir, name)
		if IsExecutableFile(candidate) {
			return candidate
		}
	}
	return ""
}

// IsExecutableFile checks if a path exists and has an executable extension on Windows.
func IsExecutableFile(pathValue string) bool {
	pathValue = ExpandUserPath(pathValue)
	if pathValue == "" {
		return false
	}
	info, err := os.Stat(pathValue)
	if err != nil || info.IsDir() {
		return false
	}
	ext := strings.ToLower(filepath.Ext(pathValue))
	switch ext {
	case ".exe", ".cmd", ".bat", ".com", ".ps1":
		return true
	}
	return false
}

// shellValueByMarkers is a no-op on Windows (no login shell marker extraction needed).
func shellValueByMarkers(_, _, _ string) string {
	return ""
}

// shellValuesByMarkers is a no-op on Windows.
func shellValuesByMarkers(_, _, _ string) []string {
	return nil
}

// batchResolveEnvVarsViaLoginShell on Windows reads directly from the process
// environment (Windows GUI apps inherit the full system+user env).
func batchResolveEnvVarsViaLoginShell(names []string) map[string]string {
	result := make(map[string]string, len(names))
	for _, name := range names {
		if v := os.Getenv(name); v != "" {
			result[name] = v
		}
	}
	return result
}

func pathScore(pathValue string) int {
	if pathValue == "" {
		return -1
	}

	entries := strings.Split(pathValue, string(os.PathListSeparator))
	score := len(entries)

	for _, cmd := range []string{"claude", "codex", "node"} {
		if pathHasExecutable(entries, cmd) {
			score += 1000
		}
	}

	return score
}

func pathHasExecutable(entries []string, name string) bool {
	for _, dir := range entries {
		if dir == "" {
			continue
		}
		for _, ext := range []string{".exe", ".cmd", ".bat", ".com"} {
			candidate := filepath.Join(dir, name+ext)
			info, err := os.Stat(candidate)
			if err != nil || info.IsDir() {
				continue
			}
			return true
		}
	}
	return false
}

func isExecutablePathValue(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	if strings.HasPrefix(value, "alias ") {
		return false
	}
	if idx := strings.IndexByte(value, '\n'); idx >= 0 {
		value = strings.TrimSpace(value[:idx])
	}
	return filepath.IsAbs(value)
}

func pathContainsDir(pathValue, dir string) bool {
	if pathValue == "" || dir == "" {
		return false
	}
	target := filepath.Clean(strings.ToLower(dir))
	for _, entry := range strings.Split(pathValue, string(os.PathListSeparator)) {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if filepath.Clean(strings.ToLower(entry)) == target {
			return true
		}
	}
	return false
}

func ensureCommandDirsInPATH(names ...string) []string {
	pathValue := os.Getenv("PATH")

	added := make([]string, 0, len(names))
	for _, name := range names {
		resolved := ResolveCommandPath(name)
		if resolved == "" {
			continue
		}
		dir := filepath.Dir(resolved)
		if pathContainsDir(pathValue, dir) {
			continue
		}
		pathValue += string(os.PathListSeparator) + dir
		added = append(added, dir)
	}

	if len(added) > 0 {
		_ = os.Setenv("PATH", pathValue)
	}
	return added
}

// ShellQuote on Windows wraps a value in double quotes for safe interpolation.
func ShellQuote(value string) string {
	if value == "" {
		return `""`
	}
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}
