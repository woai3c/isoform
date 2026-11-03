package terminal

import (
	"wails-gui/internal/wsl"
)

// resolveShellCommand determines the shell, arguments, and effective working directory
// based on the remote environment configuration.
//
// For local (env == nil): uses platform default shell, workDir as-is.
// For WSL: uses wsl.exe with the specified distro, converts workDir to Linux path.
// For SSH: not yet supported at the PTY level (SSH uses a different I/O path).
//
// Returns (shell, args, effectiveWorkDir). For WSL, the workDir is passed via --cd
// to wsl.exe, so effectiveWorkDir is empty (wsl.exe handles it internally).
func resolveShellCommand(workDir string, env *RemoteEnv) (shell string, args []string, effectiveWorkDir string) {
	if env == nil || env.Type == "" || env.Type == "local" {
		shell, args = defaultShellCommand()
		effectiveWorkDir = workDir
		return
	}

	switch env.Type {
	case "wsl":
		if !wsl.Available() {
			// Fallback to local if WSL is not available.
			shell, args = defaultShellCommand()
			effectiveWorkDir = workDir
			return
		}
		// Convert workDir to Linux path for WSL.
		linuxWorkDir := workDir
		if workDir != "" {
			linuxWorkDir = wsl.ToLinuxPath(workDir)
		}
		shell, args = wsl.ShellCommand(env.Distro, linuxWorkDir)
		// wsl.exe --cd handles the working directory internally;
		// do not set cmd.Dir (it would be a Windows path).
		effectiveWorkDir = ""
		return

	case "ssh":
		// SSH terminals use a different I/O path (sshPTY, not local PTY).
		// This will be handled at a higher level. For now, fallback to local.
		shell, args = defaultShellCommand()
		effectiveWorkDir = workDir
		return

	default:
		shell, args = defaultShellCommand()
		effectiveWorkDir = workDir
		return
	}
}
