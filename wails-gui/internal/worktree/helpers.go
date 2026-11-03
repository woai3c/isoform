package worktree

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"wails-gui/internal/platform/cmdutil"
)

var multiDash = regexp.MustCompile(`-{2,}`)

const (
	// GitCommandTimeoutDefault is the default timeout for regular git operations.
	GitCommandTimeoutDefault = 90 * time.Second
	// GitCommandTimeoutLong is used for heavier git operations.
	GitCommandTimeoutLong = 5 * time.Minute
)

// SanitizeBranchName converts a human-readable label into a valid git branch suffix.
// Keeps letters (including Unicode), digits, and hyphens. Truncates to 50 chars.
func SanitizeBranchName(label string) string {
	var b strings.Builder
	for _, r := range label {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	s := b.String()
	s = multiDash.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 50 {
		s = s[:50]
		s = strings.TrimRight(s, "-")
	}
	if s == "" {
		return "unnamed"
	}
	return s
}

// GitCommandTimeout returns a timeout tuned by git subcommand.
func GitCommandTimeout(args []string) time.Duration {
	if len(args) == 0 {
		return GitCommandTimeoutDefault
	}

	switch args[0] {
	case "merge", "worktree", "fetch", "pull", "checkout", "commit":
		return GitCommandTimeoutLong
	default:
		return GitCommandTimeoutDefault
	}
}

// GitCommandTimeoutFromEnv resolves timeout from YANSU_GIT_TIMEOUT_MS if present.
func GitCommandTimeoutFromEnv(fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv("YANSU_GIT_TIMEOUT_MS"))
	if raw == "" {
		return fallback
	}

	ms, err := strconv.Atoi(raw)
	if err != nil || ms <= 0 {
		return fallback
	}
	return time.Duration(ms) * time.Millisecond
}

// RunGit executes a git command in the given directory and returns stdout.
func RunGit(dir string, args ...string) (string, error) {
	timeout := GitCommandTimeoutFromEnv(GitCommandTimeout(args))
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := cmdutil.GitCommandContext(ctx, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	switch ctx.Err() {
	case context.DeadlineExceeded:
		return string(out), fmt.Errorf("git %s timed out after %s", strings.Join(args, " "), timeout)
	}
	if err != nil {
		return string(out), fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out)), nil
}

// IsPathWithinRoot returns true when pathValue is strictly within root.
func IsPathWithinRoot(pathValue, root string) bool {
	if strings.TrimSpace(pathValue) == "" || strings.TrimSpace(root) == "" {
		return false
	}

	absPath, err := filepath.Abs(pathValue)
	if err != nil {
		return false
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}

	absPath = filepath.Clean(absPath)
	absRoot = filepath.Clean(absRoot)
	if absPath == absRoot {
		return false
	}

	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return false
	}
	if rel == "." || rel == "" {
		return false
	}
	return !strings.HasPrefix(rel, "..")
}

// CanSafelyRemovePath returns true when pathValue is within managed roots.
func CanSafelyRemovePath(pathValue string, managedRoots []string) bool {
	pathValue = strings.TrimSpace(pathValue)
	if pathValue == "" {
		return false
	}

	absPath, err := filepath.Abs(pathValue)
	if err != nil {
		return false
	}
	absPath = filepath.Clean(absPath)
	if absPath == string(filepath.Separator) {
		return false
	}

	for _, root := range managedRoots {
		if IsPathWithinRoot(absPath, root) {
			return true
		}
	}
	return false
}

// RemovePathSafe removes pathValue only when it's inside managed roots.
func RemovePathSafe(pathValue string, managedRoots []string) error {
	if !CanSafelyRemovePath(pathValue, managedRoots) {
		return fmt.Errorf("refuse to remove unmanaged path: %q", pathValue)
	}
	absPath, err := filepath.Abs(pathValue)
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}
	return os.RemoveAll(absPath)
}

const maxDedupAttempts = 100

// UniqueBranchName returns candidate if the git branch does not exist yet.
// Otherwise it appends -2, -3, … until a free name is found.
func UniqueBranchName(projectPath, candidate string) string {
	if !gitRefExists(projectPath, candidate) {
		return candidate
	}
	for i := 2; i <= maxDedupAttempts; i++ {
		trial := fmt.Sprintf("%s-%d", candidate, i)
		if !gitRefExists(projectPath, trial) {
			return trial
		}
	}
	// Exhausted attempts — return with timestamp suffix as last resort.
	return fmt.Sprintf("%s-%d", candidate, time.Now().UnixMilli())
}

// UniqueDirPath returns candidate if the directory does not exist yet.
// Otherwise it appends -2, -3, … until a free path is found.
func UniqueDirPath(candidate string) string {
	if _, err := os.Stat(candidate); os.IsNotExist(err) {
		return candidate
	}
	for i := 2; i <= maxDedupAttempts; i++ {
		trial := fmt.Sprintf("%s-%d", candidate, i)
		if _, err := os.Stat(trial); os.IsNotExist(err) {
			return trial
		}
	}
	return fmt.Sprintf("%s-%d", candidate, time.Now().UnixMilli())
}

// TranslatePrompt is the prompt fed to the translator when the user's workspace
// label contains non-ASCII characters. Exported so callers that wire a
// translator (typically backed by a Haiku call) can reuse the exact phrasing.
const TranslatePrompt = "Translate the following text to a short English phrase for a git branch name. " +
	"Reply with ONLY the translated phrase, nothing else. Use lowercase words separated by hyphens. Maximum 5 words.\n\n%s"

// TranslateWith runs `text` through the provided translator when it contains
// non-ASCII characters. The translator is expected to return a short English
// phrase; any error or empty response falls back to the original text.
//
// Passing a nil translator is equivalent to a no-op: callers that don't wire a
// runner simply keep the original (possibly non-ASCII) label and let
// SanitizeBranchName coerce it into something git-safe.
func TranslateWith(text string, translator func(prompt string) (string, error)) string {
	hasNonASCII := false
	for _, r := range text {
		if r > 127 {
			hasNonASCII = true
			break
		}
	}
	if !hasNonASCII || translator == nil {
		return text
	}

	out, err := translator(fmt.Sprintf(TranslatePrompt, text))
	if err != nil {
		log.Printf("[translate] translator failed: %v", err)
		return text
	}
	result := strings.TrimSpace(out)
	if result == "" {
		return text
	}
	return result
}
