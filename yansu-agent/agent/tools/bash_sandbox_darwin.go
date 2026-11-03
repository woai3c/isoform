package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"yansu-agent/agent/tools/guard"
	"yansu-agent/runtoken"
)

// bashInvocation checks the exact generated profile before any user command
// runs. A command failure is never retried outside the sandbox: it may already
// have had side effects. Background shells and their descendants inherit the
// same kernel policy as foreground shells.
func (t *BashTool) bashInvocation(ctx context.Context, call ToolCall, command string) (bashInvocation, error) {
	shell, args := shellInvocation(command)
	profile, err := bashSeatbeltProfile(ctx, call)
	if err == nil {
		profile += bashLauncherSeatbeltRules(call)
		var persistence string
		persistence, err = bashPersistenceSeatbeltRules(call)
		profile += persistence
	}
	if err != nil {
		// An invalid HOME/policy is not an unavailable OS sandbox.
		return bashInvocation{}, fmt.Errorf("Bash refused: cannot construct macOS sandbox: %w", err)
	}
	executable := t.sandboxExecutable
	if executable == "" {
		executable = "/usr/bin/sandbox-exec"
	}
	if err == nil {
		probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		probe := exec.CommandContext(probeCtx, executable, "-p", profile, "/usr/bin/true")
		probe.Env = guard.ScrubSensitiveEnv(os.Environ())
		if output, probeErr := probe.CombinedOutput(); probeErr != nil {
			err = fmt.Errorf("sandbox-exec initialization: %w: %.512s", probeErr, output)
		}
	}
	if err != nil {
		if ctx.Err() != nil {
			return bashInvocation{}, ctx.Err()
		}
		return bashInvocation{}, fmt.Errorf("Bash refused: macOS sandbox unavailable: %w", err)
	}
	return bashInvocation{executable: executable, args: append([]string{"-p", profile, shell}, args...)}, nil
}

// Seatbelt does not expose an argv predicate for process-exec. Keep the
// friendly lexical security(1) refusal and block the keychain services at the
// Mach boundary, including dynamically named/copied clients. This intentionally
// prevents credential helpers from retrieving keychain passwords as well,
// except the verified bundled CLI with its own restricted command surface.
func bashSeatbeltProfile(ctx context.Context, calls ...ToolCall) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) {
		return "", fmt.Errorf("cannot resolve HOME for the Bash sandbox")
	}
	rawHome := home
	home, err = canonicalPathLoose(home)
	if err != nil {
		return "", fmt.Errorf("resolve sandbox HOME: %w", err)
	}
	var b strings.Builder
	b.WriteString("(version 1)\n(allow default)\n")
	filters := map[string]bool{}
	deny := func(filter string) {
		if filter == "" || filters[filter] {
			return
		}
		filters[filter] = true
		fmt.Fprintf(&b, "(deny file-read* file-write* %s)\n", filter)
	}
	pathRegex := func(path, suffix string) string {
		// Kernel vnode paths use real paths, but unlinking a symlink acts on
		// its original entry. Protect both names so a link cannot be replaced
		// with a newly writable tree after this profile has been generated.
		resolved, resolveErr := canonicalPathLoose(path)
		if resolveErr != nil {
			err = resolveErr
			return ""
		}
		var matchers []string
		seen := map[string]bool{}
		for _, candidate := range []string{stripFirmlink(filepath.Clean(path)), resolved} {
			if seen[foldPath(candidate)] {
				continue
			}
			seen[foldPath(candidate)] = true
			pattern := "^(" + seatbeltFoldRegex("/System/Volumes/Data") + ")?" + seatbeltFoldRegex(candidate) + suffix
			matchers = append(matchers, "(regex "+strconv.Quote(pattern)+")")
			// Pin ancestors on both sides of a link without denying normal
			// writes to their other children. Include the protected entry.
			for ancestor := candidate; ancestor != "/" && ancestor != "."; ancestor = filepath.Dir(ancestor) {
				pin := "(regex " + strconv.Quote("^("+seatbeltFoldRegex("/System/Volumes/Data")+")?"+seatbeltFoldRegex(ancestor)+"$") + ")"
				if !filters["pin:"+pin] {
					filters["pin:"+pin] = true
					fmt.Fprintf(&b, "(deny file-write-unlink %s)\n", pin)
				}
			}
		}
		if len(matchers) == 1 {
			return matchers[0]
		}
		return "(require-any " + strings.Join(matchers, " ") + ")"
	}
	var call ToolCall
	if len(calls) > 0 {
		call = calls[0]
	}
	// Last matching Seatbelt rules win. Limit HOME writes first, then open
	// only working space and tool caches. All hard denies below come last so
	// neither a workspace symlink nor a cache exception opens private data.
	fmt.Fprintf(&b, "(deny file-write* file-write-unlink %s)\n", pathRegex(home, "(/|$)"))
	writable := []string{call.WorkDir, os.TempDir(), "/tmp", "/var/tmp"}
	for _, relative := range []string{
		"Library/Caches/go-build", ".cache/go-build", "go/pkg/mod", "go/pkg/sumdb",
		".npm", ".cache/pnpm", "Library/Caches/pnpm", "Library/pnpm/store", ".local/share/pnpm/store",
		".bun/install/cache", ".cache/pip", "Library/Caches/pip", ".cache/uv",
		".cargo/registry", ".cargo/git", ".gradle/caches",
	} {
		writable = append(writable, filepath.Join(home, relative))
	}
	// Retain the existing, exact per-command persistence consent. These grants
	// precede the immutable credential/bundle rules and never grant HOME itself.
	if bashLauncherApproved(call) {
		command, _ := call.Input["command"].(string)
		writable = append(writable, analyzeBash(command, call.WorkDir).persistenceWritePaths()...)
	}
	for _, path := range writable {
		if !filepath.IsAbs(path) {
			continue
		}
		resolved, resolveErr := canonicalPathLoose(path)
		if resolveErr != nil {
			return "", fmt.Errorf("resolve sandbox writable path: %w", resolveErr)
		}
		// Temp/work roots can be ancestors of HOME (including test homes under
		// /tmp). Never let such an exception erase the whole HOME restriction.
		filter := pathRegex(path, "(/|$)")
		if foldedWithin(home, resolved) {
			filter = "(require-all " + filter + " (require-not " + pathRegex(home, "(/|$)") + "))"
		}
		fmt.Fprintf(&b, "(allow file-write* file-write-unlink %s)\n", filter)
		// A fresh cache may need its parent directories created. Grant only
		// directory creation at those exact names, never their other children.
		for parent := filepath.Dir(resolved); foldedWithin(parent, home) && !strings.EqualFold(parent, home); parent = filepath.Dir(parent) {
			fmt.Fprintf(&b, "(allow file-write-create (require-all %s (vnode-type DIRECTORY)))\n", pathRegex(parent, "$"))
		}
	}
	// A path policy cannot follow a directory after it is renamed elsewhere.
	// Pin directory entries without denying safe file contents/executables.
	// This static rule replaces the former per-file ancestor inventory.
	pinDirectories := func(root string) {
		fmt.Fprintf(&b, "(deny file-write-unlink (require-all %s (vnode-type DIRECTORY)))\n", pathRegex(root, "(/|$)"))
	}
	trustedCLI, trustedPaths := trustedYansuCLIProfile()
	for _, path := range trustedPaths {
		fmt.Fprintf(&b, "(deny file-write* %s)\n", pathRegex(path, "$"))
	}
	// protectedDataRoots returns canonical paths. Preserve the conventional
	// root entries too when the entire data folder has been relocated by a
	// symlink; pinning the entry adds no restriction on safe file contents.
	for _, root := range []string{filepath.Join(rawHome, ".isoform-agent"), filepath.Join(rawHome, ".yansu-agent"), strings.TrimSpace(os.Getenv("YANSU_AGENT_HOME"))} {
		if filepath.IsAbs(root) {
			pathRegex(root, "$")
		}
	}
	for _, root := range protectedDataRoots() {
		pinDirectories(root)
		// Bundled runtimes execute later in the desktop process. Keep them
		// readable/executable, but prevent replacement from any agent shell.
		for _, relative := range bundledToolFolders {
			fmt.Fprintf(&b, "(deny file-write* %s)\n", pathRegex(filepath.Join(root, relative), "(/|$)"))
		}
		for _, pattern := range dataPrivateBasenames {
			deny("(require-all " + pathRegex(root, "/") + " (regex " + strconv.Quote("/("+seatbeltPattern(pattern)+")$") + "))")
		}
		// Pair the CLI's port with its protected credential; shells must not
		// redirect the genuine CLI's Authorization header to another listener.
		fmt.Fprintf(&b, "(deny file-write* %s)\n", pathRegex(filepath.Join(root, "cron-port"), "$"))
		deny(pathRegex(root, "/"+seatbeltPattern(dataPrivatePath)))
		deny(pathRegex(root, "/"+seatbeltPattern(dataMediaPath)))
		deny(pathRegex(root, "/("+seatbeltPattern(dataMemoryPath)+")"))
		// Fixed application-owned paths can be resolved individually without
		// discovering any directory entries. This also protects relocated
		// secrets/media whose conventional path is a symlink.
		for _, relative := range []string{"cron-secret", "ai-provider.json", "memory.json", "mcp-hub/state.json", "external-mcp.json", "daemon.json", "composio.json", "token", "browser-bridge-token", "slack/credentials.json", "discord/credentials.json", "telegram/credentials.json", "feishu/credentials.json", "lark/credentials.json", "mobile/hub-identity.json", "mobile/relay.json", "mobile/devices.json", "yansu-gui.log", "yansu-gui.log.old", "yansu-gui.log.old.2", "yansu-gui.stderr.log", "yansu-gui.stderr.log.old", "yansu-gui.stderr.log.old.2", "triage.log", "triage.log.old", "triage.log.old.2"} {
			deny(pathRegex(filepath.Join(root, relative), "$"))
		}
		for _, relative := range []string{"activity", "activity/snapshots", "activity/segments", "activity/events", "activity/screenshots", "activity/audio", "activity/transcripts", "screenshots", "audio", "transcripts", "memory", "knowledge", "slack-scan/conversations", "mobile/briefs"} {
			deny(pathRegex(filepath.Join(root, relative), "(/|$)"))
		}
	}
	for _, dir := range []string{"Library/Keychains", ".gnupg"} {
		deny(pathRegex(filepath.Join(home, dir), "(/|$)"))
	}
	for _, file := range []string{".aws/credentials", ".config/gh/hosts.yml", ".netrc", ".docker/config.json", ".kube/config", ".git-credentials"} {
		deny(pathRegex(filepath.Join(home, file), "$"))
	}
	// Deny private-key contents while permitting directory enumeration and
	// public keys/config/known_hosts. Regexes cover keys created after launch.
	ssh := filepath.Join(home, ".ssh")
	pinDirectories(ssh)
	public := "(regex " + strconv.Quote("/("+seatbeltFoldRegex("config")+"|"+seatbeltFoldRegex("authorized_keys")+"2?|"+seatbeltFoldRegex("rc")+"|"+seatbeltFoldRegex("known_hosts")+"[^/]*|[^/]+"+seatbeltFoldRegex(".pub")+")$") + ")"
	deny("(require-all " + pathRegex(ssh, "/.+") + " (require-not " + public + "))")
	for _, dir := range []string{"Library/Application Support", "Library/Containers", ".mozilla", ".config", "Library/Cookies", "Library/Safari"} {
		// Include database journals/WALs as they can contain the same secrets.
		var secretNames []string
		for name := range browserSecretNames {
			secretNames = append(secretNames, name)
		}
		sort.Strings(secretNames)
		root := filepath.Join(home, dir)
		pinDirectories(root)
		for _, name := range secretNames {
			deny(pathRegex(root, "/(.*/)?"+seatbeltFoldRegex(name)+"(-[^/]*)?$"))
		}
	}

	b.WriteString("(deny mach-lookup\n (global-name \"com.apple.SecurityServer\")\n (global-name \"com.apple.securityd.xpc\")\n (global-name \"com.apple.securityd\")\n (global-name \"com.apple.securityd.systemkeychain\"))\n")
	_, scopedRun := runtoken.FromContext(ctx)
	if trustedCLI != "" && !scopedRun {
		// Security.framework reads the encrypted store while retrieving the
		// CLI's ACL-protected item. Only the verified, command-restricted CLI
		// gets this exception; writes and retired plaintext secrets stay denied.
		fmt.Fprintf(&b, "(allow file-read* (require-all %s %s))\n", pathRegex(filepath.Join(home, "Library/Keychains"), "(/|$)"), trustedCLI)
		fmt.Fprintf(&b, "(allow mach-lookup (require-all %s (require-any (global-name \"com.apple.SecurityServer\") (global-name \"com.apple.securityd.xpc\") (global-name \"com.apple.securityd\") (global-name \"com.apple.securityd.systemkeychain\"))))\n", trustedCLI)
	}
	if err != nil {
		return "", fmt.Errorf("resolve sandbox protected path: %w", err)
	}
	return b.String(), nil
}

func seatbeltFoldRegex(value string) string {
	var b strings.Builder
	for _, c := range value {
		switch true {
		case c >= 'a' && c <= 'z':
			fmt.Fprintf(&b, "[%c%c]", c, c-'a'+'A')
		case c >= 'A' && c <= 'Z':
			fmt.Fprintf(&b, "[%c%c]", c+'a'-'A', c)
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	return b.String()
}

// seatbeltPattern folds literal ASCII letters in our fixed regex syntax.
// Escape pairs are kept intact (notably \.); this is not a user-input parser.
func seatbeltPattern(pattern string) string {
	var b strings.Builder
	escaped := false
	for _, c := range pattern {
		if escaped {
			b.WriteRune(c)
			escaped = false
			continue
		}
		if c == '\\' {
			b.WriteRune(c)
			escaped = true
			continue
		}
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			b.WriteString(seatbeltFoldRegex(string(c)))
		} else {
			b.WriteRune(c)
		}
	}
	return b.String()
}

func bashLauncherSeatbeltRules(call ToolCall) string {
	allowed := bashLauncherExecutions(call)
	var b strings.Builder
	paths := bashLauncherPaths(call.WorkDir)
	deniedPaths := map[string]bool{}
	for _, name := range bashLauncherNames {
		if allowed[name] {
			continue
		}
		// Match dynamically named launchers anywhere, then separately match
		// known symlink targets whose kernel path may have a different name.
		fmt.Fprintf(&b, "(deny process-exec (regex %s))\n", strconv.Quote("/"+seatbeltFoldRegex(name)+"(\\.(exe|cmd|bat|com))?$"))
		for _, path := range paths[name] {
			pattern := "^(" + seatbeltFoldRegex("/System/Volumes/Data") + ")?" + seatbeltFoldRegex(path) + "$"
			if !deniedPaths[pattern] {
				deniedPaths[pattern] = true
				fmt.Fprintf(&b, "(deny process-exec (regex %s))\n", strconv.Quote(pattern))
			}
		}
	}
	b.WriteString(bashContainerSocketRules(call, allowed))
	// launchd treats job creation specially even with allow-default. Grant it
	// explicitly only for the attended launcher command the user approved.
	if bashLauncherApproved(call) && (allowed["launchctl"] || allowed["automator"] || allowed["shortcuts"] || allowed["osascript"]) {
		b.WriteString("(allow job-creation)\n")
	} else {
		b.WriteString("(deny job-creation)\n")
	}
	if !allowed["open"] {
		b.WriteString("(deny lsopen)\n")
	}
	if !bashLauncherApproved(call) || !(allowed["osascript"] || allowed["automator"] || allowed["shortcuts"] || allowed["open"]) {
		b.WriteString("(deny appleevent-send)\n")
	}
	return b.String()
}

// Containers accept work over daemon IPC, so denying only the CLI executable
// is insufficient: an interpreter can speak the same Unix socket protocol.
// These static locations cover the common macOS engines without walking any
// data directory or contacting a running engine.
func bashContainerSocketRules(call ToolCall, allowed map[string]bool) string {
	if bashLauncherApproved(call) {
		for _, name := range []string{"docker", "docker-compose", "podman", "podman-compose", "nerdctl", "colima", "lima", "limactl", "orbctl", "container"} {
			if allowed[name] {
				return ""
			}
		}
	}
	home, _ := os.UserHomeDir()
	var b strings.Builder
	seen := map[string]bool{}
	deny := func(path, suffix string) {
		resolved, err := canonicalPathLoose(path)
		if err != nil || !filepath.IsAbs(resolved) {
			return
		}
		for _, candidate := range []string{stripFirmlink(filepath.Clean(path)), resolved} {
			pattern := "^(" + seatbeltFoldRegex("/System/Volumes/Data") + ")?" + seatbeltFoldRegex(candidate) + suffix
			if !seen[pattern] {
				seen[pattern] = true
				fmt.Fprintf(&b, "(deny network-outbound file-read* file-write* (regex %s))\n", strconv.Quote(pattern))
			}
			if suffix != "$" {
				fmt.Fprintf(&b, "(deny file-write-unlink (require-all (regex %s) (vnode-type DIRECTORY)))\n", strconv.Quote("^("+seatbeltFoldRegex("/System/Volumes/Data")+")?"+seatbeltFoldRegex(candidate)+"(/|$)"))
			}
			for ancestor := filepath.Dir(candidate); ancestor != "/" && ancestor != "."; ancestor = filepath.Dir(ancestor) {
				pin := "^(" + seatbeltFoldRegex("/System/Volumes/Data") + ")?" + seatbeltFoldRegex(ancestor) + "$"
				if !seen["pin:"+pin] {
					seen["pin:"+pin] = true
					fmt.Fprintf(&b, "(deny file-write-unlink (regex %s))\n", strconv.Quote(pin))
				}
			}
		}
	}
	for _, path := range []string{"/var/run/docker.sock", "/var/run/podman/podman.sock", "/var/run/containerd/containerd.sock", "/var/run/buildkit/buildkitd.sock"} {
		deny(path, "$")
	}
	for _, relative := range []string{".docker", ".colima", ".lima", ".orbstack", ".local/share/containers", ".config/containers",
		"Library/Containers/com.docker.docker/Data", "Library/Group Containers/group.com.docker",
		"Library/Application Support/OrbStack", "Library/Application Support/io.podman-desktop", "Library/Application Support/com.apple.container"} {
		deny(filepath.Join(home, relative), "/.*\\.sock$")
	}
	// Respect explicit host configuration without printing endpoint values.
	for _, variable := range []string{"DOCKER_HOST", "CONTAINER_HOST", "CONTAINERD_ADDRESS", "BUILDKIT_HOST", "PODMAN_HOST"} {
		value := strings.TrimSpace(os.Getenv(variable))
		value = strings.TrimPrefix(strings.TrimPrefix(value, "unix://"), "unix:")
		if filepath.IsAbs(value) {
			deny(value, "$")
		}
	}
	return b.String()
}
