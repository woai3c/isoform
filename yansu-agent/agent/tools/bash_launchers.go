package tools

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"yansu-agent/agent/authority"
)

const mandatoryLauncherApproval = "mandatory_launcher_approval"

var bashLauncherNames = []string{
	"open", "launchctl", "osascript", "automator", "shortcuts", "qlmanage", "crontab", "at",
	"ssh", "scp", "sftp", "sshpass", "mosh", "mosh-client", "autossh",
	"docker", "docker-compose", "podman", "podman-compose", "nerdctl", "colima", "lima", "limactl", "orbctl", "container",
}

// LaunchServices, launchd, Apple events and remote shells can start work
// outside the calling shell's sandbox. Their approval is independent of the
// optional risky-command prompt; a scoped grant cannot authorize a launcher.
func (s *bashScan) launcherApprovalAction(command string) *authority.Action {
	paths := bashLauncherPaths(s.workDir)
	plain, single := plainWords(command)
	for _, c := range s.cmds {
		if len(c.argv) == 0 {
			continue
		}
		name, ok := bashLauncherName(c.argv[0], s.workDir, paths)
		singleLauncher := single && len(plain) == len(c.argv) && len(plain) > 0 && plain[0] == c.argv[0].text
		if !ok || (singleLauncher && launcherReadOnly(name, c.argv[1:])) {
			continue
		}
		action := &authority.Action{
			Capability: "process.unsandboxed.launch", ToolName: "Bash", Operation: name,
			Effect: authority.EffectPrivileged, NoGrant: true,
			ApprovalPrefix: "authority:bash.launcher:" + commandDigest(command),
			ApprovalReason: fmt.Sprintf("Allow Isoform to run %s? It can start a process outside the shell sandbox.", name),
			Metadata:       map[string]string{mandatoryLauncherApproval: "true"},
		}
		switch name {
		case "osascript":
			action.Capability = "computer.apple_event.execute"
		case "ssh", "scp", "sftp", "sshpass", "mosh", "mosh-client", "autossh":
			action.Capability, action.Effect = "remote.shell.execute", authority.EffectExternalWrite
		case "launchctl", "crontab", "at":
			action.Capability, action.Effect = "os.schedule.write", authority.EffectExternalWrite
		}
		return action
	}
	return nil
}

func isBashLauncher(name string) bool {
	for _, launcher := range bashLauncherNames {
		if name == launcher {
			return true
		}
	}
	return false
}

func launcherReadOnly(name string, args []shWord) bool {
	for _, arg := range args {
		if !arg.literal() {
			return false
		}
	}
	switch name {
	case "open":
		if len(args) > 0 && args[0].text == "--" {
			args = args[1:]
		}
		if len(args) == 0 {
			return false
		}
		for _, arg := range args {
			parsed, err := url.Parse(arg.text)
			if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
				return false
			}
		}
		return true
	case "launchctl":
		if len(args) == 0 {
			return true
		}
		switch args[0].text {
		case "list", "print", "print-disabled", "print-cache", "version", "help", "blame",
			"procinfo", "dumpstate", "managerpid", "manageruid", "managername", "getenv":
			return true
		default:
			return false
		}
	default:
		return false
	}
}

func bashLauncherApproved(call ToolCall) bool {
	if call.Authority == nil || !call.Authority.Approved || call.Authority.Context.Unattended {
		return false
	}
	command, _ := call.Input["command"].(string)
	if analyzeBash(command, call.WorkDir).mixedApprovalAction(command) != nil {
		return false
	}
	mode := call.Authority.Context.Mode
	return mode == "" || mode == authority.ExecutionModeNormal
}

// bashLauncherExecutions returns the known launcher names this exact call may
// execute. The sandbox denies every other launcher, including dynamic calls
// hidden inside interpreters. Read-only launchctl and literal HTTP(S) open
// calls do not need an approval; unsafe calls need explicit attended consent.
func bashLauncherExecutions(call ToolCall) map[string]bool {
	command, _ := call.Input["command"].(string)
	allowed := map[string]bool{}
	approved := bashLauncherApproved(call)
	plain, single := plainWords(command)
	paths := bashLauncherPaths(call.WorkDir)
	for _, c := range analyzeBash(command, call.WorkDir).cmds {
		if len(c.argv) == 0 {
			continue
		}
		name, ok := bashLauncherName(c.argv[0], call.WorkDir, paths)
		// A read-only command must not enable the same executable for a
		// second, hidden launch in an interpreter or shell construct.
		singleLauncher := single && len(plain) == len(c.argv) && len(plain) > 0 && plain[0] == c.argv[0].text
		if ok && (approved || (singleLauncher && launcherReadOnly(name, c.argv[1:]))) {
			allowed[name] = true
			if approved {
				// These wrappers necessarily invoke the SSH transport. Other
				// launcher families retain their independent exec denials.
				switch name {
				case "scp", "sftp", "autossh", "mosh":
					allowed["ssh"] = true
				}
				if name == "mosh" {
					allowed["mosh-client"] = true
				}
			}
		}
	}
	return allowed
}

// Resolve a bounded set of executable names, never a filesystem inventory.
// Include every PATH entry: a later shell assignment can change which entry
// wins. Kernel exec checks see symlink targets, including renamed targets.
func bashLauncherPaths(workDir string) map[string][]string {
	dirs := []string{"/bin", "/usr/bin", "/usr/local/bin", "/opt/homebrew/bin", "/opt/local/bin",
		"/Applications/Docker.app/Contents/Resources/bin", "/Applications/OrbStack.app/Contents/MacOS/bin"}
	dirs = append(dirs, filepath.SplitList(os.Getenv("PATH"))...)
	paths := make(map[string][]string, len(bashLauncherNames))
	for _, name := range bashLauncherNames {
		seen := map[string]bool{}
		for _, dir := range dirs {
			if !filepath.IsAbs(dir) {
				dir = filepath.Join(workDir, dir)
			}
			candidate := filepath.Join(dir, name)
			if seen[foldPath(candidate)] {
				continue
			}
			seen[foldPath(candidate)] = true
			resolved, err := filepath.EvalSymlinks(candidate)
			if err != nil {
				continue
			}
			resolved = stripFirmlink(filepath.Clean(resolved))
			paths[name] = append(paths[name], candidate)
			if foldPath(resolved) != foldPath(candidate) {
				paths[name] = append(paths[name], resolved)
			}
		}
	}
	return paths
}

func bashLauncherName(word shWord, workDir string, paths map[string][]string) (string, bool) {
	name, hasDir, ok := commandName(word)
	if !ok || isBashLauncher(name) {
		return name, ok && isBashLauncher(name)
	}
	if !hasDir {
		// Bare aliases are resolved by the same PATH search used for launchers.
		for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
			candidate := filepath.Join(dir, word.text)
			if !filepath.IsAbs(candidate) {
				candidate = filepath.Join(workDir, candidate)
			}
			if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() && info.Mode()&0111 != 0 {
				word.text = candidate
				hasDir = true
				break
			}
		}
	}
	if !hasDir {
		return "", false
	}
	path := word.text
	if !filepath.IsAbs(path) {
		path = filepath.Join(workDir, path)
	}
	resolved, err := canonicalPathLoose(path)
	if err != nil {
		return "", false
	}
	if name := strings.ToLower(filepath.Base(resolved)); isBashLauncher(name) {
		return name, true
	}
	for _, name := range bashLauncherNames {
		for _, known := range paths[name] {
			if foldPath(resolved) == foldPath(known) {
				return name, true
			}
		}
	}
	return "", false
}

func enforceBashLauncherApproval(ctx context.Context, call ToolCall) error {
	if err := enforceMixedBashActions(ctx, call); err != nil {
		return err
	}
	command, _ := call.Input["command"].(string)
	action := analyzeBash(command, call.WorkDir).launcherApprovalAction(command)
	if action == nil {
		return nil
	}
	run := authority.Context{}
	if call.Authority != nil {
		run = call.Authority.Context
	}
	evaluation := authority.NewPolicy(nil).Evaluate(ctx, run, *action)
	ApplyBashApprovalPreference(&evaluation, false)
	if evaluation.Decision == authority.DecisionRequireApproval && bashLauncherApproved(call) {
		return nil
	}
	return &AuthorityError{Evaluation: evaluation}
}
