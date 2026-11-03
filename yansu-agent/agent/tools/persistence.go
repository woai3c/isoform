package tools

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"yansu-agent/agent/authority"
)

const mandatoryPersistenceApproval = "mandatory_persistence_approval"

var persistenceHomeDirectories = []string{
	"Library/LaunchAgents", "Library/LaunchDaemons",
	"Library/Application Support/com.apple.backgroundtaskmanagementagent",
	".config/fish",
}

var persistenceHomeFiles = []string{
	".zshrc", ".zprofile", ".zshenv", ".zlogin", ".zlogout",
	".bashrc", ".bash_profile", ".bash_login", ".profile",
	".ssh/authorized_keys", ".ssh/authorized_keys2", ".ssh/rc", ".ssh/config",
	".gitconfig", ".config/git/config",
}

var persistenceSpoolDirectories = []string{"/private/var/at", "/private/var/spool/cron", "/private/var/spool/at"}

func persistencePath(path, workDir string) bool {
	home := canonicalHome("")
	if home == "" || !filepath.IsAbs(path) {
		return false
	}
	canonical, err := canonicalPathLoose(path)
	if err != nil {
		return false
	}
	for _, candidate := range []string{stripFirmlink(filepath.Clean(path)), canonical} {
		for _, relative := range persistenceHomeDirectories {
			if credentialPathWithin(candidate, filepath.Join(home, relative)) {
				return true
			}
		}
		for _, relative := range persistenceHomeFiles {
			if credentialPathEqual(candidate, filepath.Join(home, relative)) {
				return true
			}
		}
		for _, dir := range persistenceSpoolDirectories {
			if credentialPathWithin(candidate, dir) {
				return true
			}
		}
		if credentialPathEqual(candidate, "/etc/crontab") {
			return true
		}
		if externalGitPersistencePath(candidate, workDir) {
			return true
		}
	}
	return false
}

func externalGitPersistencePath(path, workDir string) bool {
	if workDir != "" {
		if root, err := canonicalPathLoose(workDir); err == nil && foldedWithin(path, root) {
			return false
		}
	}
	parts := strings.Split(filepath.ToSlash(foldPath(path)), "/")
	for i, part := range parts {
		if !strings.HasSuffix(part, ".git") || i+1 >= len(parts) {
			continue
		}
		// External repo config can set core.hooksPath/core.fsmonitor, which
		// runs code on a later Git invocation just like a hook file.
		if parts[i+1] == "hooks" || (parts[i+1] == "config" && i+2 == len(parts)) {
			return true
		}
	}
	return false
}

func (s *bashScan) persistenceWritePaths() []string {
	var paths []string
	seen := map[string]bool{}
	words := append([]shWord(nil), s.writes...)
	for _, command := range s.cmds {
		if len(command.argv) == 0 {
			continue
		}
		name, _, ok := commandName(command.argv[0])
		if !ok || name != "git" {
			continue
		}
		sub, index, verdict := gitSubcommand(command.argv)
		if verdict != nil || sub != "config" {
			continue
		}
		rest := command.argv[index+1:]
		hits, operands := scanOptions(rest, "f", setOf("--file", "--blob", "--type", "--default"))
		if s.gitSubcommandVerdict(command.argv, sub, rest, hits, operands, 0).kind == bashLocal {
			continue
		}
		words = append(words, optValues(hits, "-f", "--file")...)
		if optSeen(hits, "--global") {
			words = append(words, shWord{text: filepath.Join(s.home, ".gitconfig")}, shWord{text: filepath.Join(s.home, ".config/git/config")})
		} else {
			for _, base := range s.gitBases(command.argv[1:index]) {
				words = append(words, shWord{text: filepath.Join(base, ".git/config")})
			}
		}
	}
	for _, link := range s.links {
		words = append(words, link.path)
	}
	for _, word := range words {
		for _, path := range s.pathCandidates(word) {
			if persistencePath(path, s.workDir) && !seen[path] {
				seen[path] = true
				paths = append(paths, path)
			}
		}
	}
	return paths
}

func persistenceAction(tool, operation, digest string) authority.Action {
	return authority.Action{
		Capability: "os.persistence.write", ToolName: tool, Operation: operation,
		Effect: authority.EffectPrivileged, NoGrant: true,
		ApprovalPrefix: "authority:persistence:" + digest,
		ApprovalReason: "Allow Isoform to change startup or persistence configuration? It can run code outside the shell sandbox later.",
		Metadata:       map[string]string{mandatoryPersistenceApproval: "true"},
	}
}

func (s *bashScan) persistenceApprovalAction(command string) *authority.Action {
	if paths := s.persistenceWritePaths(); len(paths) > 0 {
		action := persistenceAction("Bash", paths[0], commandDigest(command))
		return &action
	}
	return nil
}

func persistenceFileAction(tool string, input map[string]any, workDir string) *authority.Action {
	switch tool {
	case "Write", "Edit", "NotebookEdit":
	default:
		return nil
	}
	path, _ := input["path"].(string)
	if !persistencePath(path, workDir) {
		return nil
	}
	action := persistenceAction(tool, path, commandDigest(fmt.Sprintf("%s:%s:%v", tool, path, input)))
	return &action
}

func enforcePersistenceApproval(ctx context.Context, tool string, call ToolCall) error {
	var action *authority.Action
	if tool == "Bash" {
		if err := enforceMixedBashActions(ctx, call); err != nil {
			return err
		}
		command, _ := call.Input["command"].(string)
		action = analyzeBash(command, call.WorkDir).persistenceApprovalAction(command)
	} else {
		action = persistenceFileAction(tool, call.Input, call.WorkDir)
	}
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
