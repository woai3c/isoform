//go:build !windows

package shellenv

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
)

// LoginShell resolves the user's shell for login-shell command execution.
func LoginShell() string {
	if shell := strings.TrimSpace(os.Getenv("SHELL")); shell != "" {
		if filepath.IsAbs(shell) {
			if info, err := os.Stat(shell); err == nil && !info.IsDir() {
				return shell
			}
		} else if resolved, err := exec.LookPath(shell); err == nil {
			return resolved
		}
	}

	if goruntime.GOOS == "darwin" {
		return "/bin/zsh"
	}
	return "/bin/sh"
}

// resolveUserPATH captures PATH from a login shell.
// Returns empty string if resolution fails.
func resolveUserPATH() string {
	const startMarker = "YANSU_PATH_BEGIN="
	const endMarker = "=YANSU_PATH_END"
	script := `printf 'YANSU_PATH_BEGIN=%s=YANSU_PATH_END' "$PATH"`
	candidates := shellValuesByMarkers(startMarker, endMarker, script)
	if len(candidates) == 0 {
		return ""
	}

	best := candidates[0]
	bestScore := pathScore(best)
	for _, candidate := range candidates[1:] {
		score := pathScore(candidate)
		if score > bestScore {
			best = candidate
			bestScore = score
		}
	}

	return best
}

func commandAvailableViaLoginShell(name string) bool {
	return resolveCommandPathViaLoginShell(name) != ""
}

// ShellQuote wraps a value in single quotes for safe shell interpolation.
func ShellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func resolveCommandPathViaLoginShell(name string) string {
	if strings.TrimSpace(name) == "" {
		return ""
	}
	const startMarker = "YANSU_CMD_BEGIN="
	const endMarker = "=YANSU_CMD_END"
	script := fmt.Sprintf(`resolved="$(whence -p %s 2>/dev/null || type -P %s 2>/dev/null || command -v %s 2>/dev/null || true)"; printf 'YANSU_CMD_BEGIN=%%s=YANSU_CMD_END' "$resolved"`,
		ShellQuote(name),
		ShellQuote(name),
		ShellQuote(name),
	)
	resolved := shellValueByMarkers(startMarker, endMarker, script)
	if !isExecutablePathValue(resolved) {
		return ""
	}
	resolved = ExpandUserPath(resolved)
	if !IsExecutableFile(resolved) {
		return ""
	}
	return resolved
}

func resolveEnvVarViaLoginShell(name string) string {
	if !isSafeEnvVarName(name) {
		return ""
	}
	const startMarker = "YANSU_ENV_BEGIN="
	const endMarker = "=YANSU_ENV_END"
	script := fmt.Sprintf(`if [ "${%s+x}" = x ]; then printf 'YANSU_ENV_BEGIN=%%s=YANSU_ENV_END' "$%s"; fi`,
		name,
		name,
	)
	return shellValueByMarkers(startMarker, endMarker, script)
}

// batchResolveEnvVarsViaLoginShell reads multiple env vars in a single login
// shell invocation, avoiding the overhead of spawning separate processes for
// each variable (critical when ~/.zshrc is slow, e.g. oh-my-zsh).
func batchResolveEnvVarsViaLoginShell(names []string) map[string]string {
	var safe []string
	for _, name := range names {
		if isSafeEnvVarName(name) {
			safe = append(safe, name)
		}
	}
	if len(safe) == 0 {
		return nil
	}

	// Build a single script that outputs all variables with unique markers.
	// Format per variable: YANSU_BV:<NAME>=<VALUE>=:YANSU_BV_END\n
	var sb strings.Builder
	for i, name := range safe {
		if i > 0 {
			sb.WriteString("; ")
		}
		fmt.Fprintf(&sb,
			`if [ "${%s+x}" = x ]; then printf 'YANSU_BV:%s=%%s=:YANSU_BV_END\n' "$%s"; fi`,
			name, name, name,
		)
	}
	script := sb.String()

	result := make(map[string]string, len(safe))
	shell := LoginShell()

	for _, shellArg := range []string{"-lc", "-lic"} {
		out, err := exec.Command(shell, shellArg, script).Output()
		if err != nil && len(out) == 0 {
			continue
		}
		output := string(out)
		for _, name := range safe {
			if result[name] != "" {
				continue // already resolved from a previous shell mode
			}
			startMarker := "YANSU_BV:" + name + "="
			endMarker := "=:YANSU_BV_END"

			start := strings.LastIndex(output, startMarker)
			if start < 0 {
				continue
			}
			start += len(startMarker)
			end := strings.Index(output[start:], endMarker)
			if end < 0 {
				continue
			}
			if v := strings.TrimSpace(output[start : start+end]); v != "" {
				result[name] = v
			}
		}
	}
	return result
}

func resolveCommandPathInCommonDirs(name string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	dirs := []string{
		filepath.Join(home, ".bun", "bin"),
		filepath.Join(home, ".local", "bin"),
		filepath.Join(home, ".npm", "bin"),
		filepath.Join(home, ".npm-global", "bin"),
		"/opt/homebrew/bin",
		"/usr/local/bin",
		"/usr/bin",
	}

	for _, dir := range dirs {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		candidate := filepath.Join(dir, name)
		if IsExecutableFile(candidate) {
			return candidate
		}
	}
	return ""
}

// IsExecutableFile checks whether the given path is an executable file.
func IsExecutableFile(pathValue string) bool {
	pathValue = ExpandUserPath(pathValue)
	if pathValue == "" {
		return false
	}
	info, err := os.Stat(pathValue)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode()&0o111 != 0
}

func shellValueByMarkers(startMarker, endMarker, script string) string {
	values := shellValuesByMarkers(startMarker, endMarker, script)
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func shellValuesByMarkers(startMarker, endMarker, script string) []string {
	shell := LoginShell()
	var values []string
	for _, shellArg := range []string{"-lc", "-lic"} {
		out, err := exec.Command(shell, shellArg, script).Output()
		if err != nil && len(out) == 0 {
			continue
		}

		if value := ParseMarkedValue(string(out), startMarker, endMarker); value != "" {
			values = append(values, value)
		}
	}
	return values
}

func pathScore(pathValue string) int {
	if pathValue == "" {
		return -1
	}

	entries := strings.Split(pathValue, ":")
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
		candidate := filepath.Join(dir, name)
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			continue
		}
		if info.Mode()&0o111 != 0 {
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
	return strings.HasPrefix(value, "/") || strings.HasPrefix(value, "~")
}

func pathContainsDir(pathValue, dir string) bool {
	if pathValue == "" || dir == "" {
		return false
	}
	target := filepath.Clean(dir)
	for _, entry := range strings.Split(pathValue, ":") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if filepath.Clean(entry) == target {
			return true
		}
	}
	return false
}

func ensureCommandDirsInPATH(names ...string) []string {
	pathValue := os.Getenv("PATH")
	if pathValue == "" {
		pathValue = "/usr/bin:/bin:/usr/sbin:/sbin"
	}

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
		pathValue += ":" + dir
		added = append(added, dir)
	}

	if len(added) > 0 {
		_ = os.Setenv("PATH", pathValue)
	}
	return added
}
