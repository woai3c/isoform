package tools

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

// Persistence rules are separate from hard blocks: explicit attended consent
// removes only the matching rule for this invocation. We never append broad
// allow rules that could override credential or bundled-executable protection.
func bashPersistenceSeatbeltRules(call ToolCall) (string, error) {
	home := canonicalHome("")
	if home == "" {
		return "", fmt.Errorf("cannot resolve persistence HOME")
	}
	var b strings.Builder
	rawFilter := func(path, suffix string) string {
		pattern := "^(" + seatbeltFoldRegex("/System/Volumes/Data") + ")?" + seatbeltFoldRegex(stripFirmlink(filepath.Clean(path))) + suffix
		return "(regex " + strconv.Quote(pattern) + ")"
	}
	pathFilter := func(path, suffix string) (string, error) {
		resolved, err := canonicalPathLoose(path)
		if err != nil {
			return "", err
		}
		raw := rawFilter(path, suffix)
		real := rawFilter(resolved, suffix)
		if raw == real {
			return raw, nil
		}
		return "(require-any " + raw + " " + real + ")", nil
	}
	var approved []string
	command, _ := call.Input["command"].(string)
	if bashLauncherApproved(call) {
		for _, path := range analyzeBash(command, call.WorkDir).persistenceWritePaths() {
			filter, err := pathFilter(path, "(/|$)")
			if err != nil {
				return "", err
			}
			approved = append(approved, filter)
		}
	}
	excludeApproved := func(filter string) string {
		if len(approved) == 0 {
			return filter
		}
		return "(require-all " + filter + " (require-not (require-any " + strings.Join(approved, " ") + ")))"
	}
	pinned := map[string]bool{}
	denyPath := func(path, suffix string) error {
		filter, err := pathFilter(path, suffix)
		if err != nil {
			return err
		}
		fmt.Fprintf(&b, "(deny file-write* %s)\n", excludeApproved(filter))
		resolved, err := canonicalPathLoose(path)
		if err != nil {
			return err
		}
		// Ancestor rename would otherwise remove the protected path from
		// its filter, including when a startup file is a symlink elsewhere.
		for _, spelling := range []string{stripFirmlink(filepath.Clean(path)), resolved} {
			for parent := filepath.Dir(spelling); parent != "/" && parent != "."; parent = filepath.Dir(parent) {
				if pinned[parent] {
					continue
				}
				pinned[parent] = true
				fmt.Fprintf(&b, "(deny file-write-unlink %s)\n", rawFilter(parent, "$"))
			}
		}
		return nil
	}
	for _, relative := range persistenceHomeDirectories {
		if err := denyPath(filepath.Join(home, relative), "(/|$)"); err != nil {
			return "", err
		}
	}
	for _, relative := range persistenceHomeFiles {
		if err := denyPath(filepath.Join(home, relative), "$"); err != nil {
			return "", err
		}
	}
	launchers := bashLauncherExecutions(call)
	if !bashLauncherApproved(call) || (!launchers["crontab"] && !launchers["at"]) {
		for _, path := range persistenceSpoolDirectories {
			if err := denyPath(path, "(/|$)"); err != nil {
				return "", err
			}
		}
		if err := denyPath("/etc/crontab", "$"); err != nil {
			return "", err
		}
	}
	outsideWork := func(filter string) (string, error) {
		if call.WorkDir == "" {
			return filter, nil
		}
		work, err := pathFilter(call.WorkDir, "(/|$)")
		if err != nil {
			return "", err
		}
		return "(require-all " + filter + " (require-not " + work + "))", nil
	}
	// Match normal repositories and bare *.git repositories without walking
	// HOME. Custom hooks outside these conventional paths are not discovered.
	hooks := "(regex " + strconv.Quote("/[^/]*"+seatbeltFoldRegex(".git")+"/("+seatbeltFoldRegex("hooks")+"(/|$)|"+seatbeltFoldRegex("config")+"$)") + ")"
	hooks, err := outsideWork(hooks)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(&b, "(deny file-write* %s)\n", excludeApproved(hooks))
	gitDir := "(regex " + strconv.Quote("/[^/]*"+seatbeltFoldRegex(".git")+"$") + ")"
	gitDir, err = outsideWork(gitDir)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(&b, "(deny file-write-unlink %s)\n", gitDir)
	return b.String(), nil
}
