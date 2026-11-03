//go:build windows

package terminal

import (
	"os"
	"os/exec"
)

// defaultShellCommand returns the shell and arguments for Windows.
// Prefers PowerShell 7 (pwsh) → PowerShell 5 → cmd.exe.
func defaultShellCommand() (string, []string) {
	if ps, _ := exec.LookPath("pwsh.exe"); ps != "" {
		return ps, []string{"-NoLogo"}
	}
	if ps, _ := exec.LookPath("powershell.exe"); ps != "" {
		return ps, []string{"-NoLogo"}
	}
	comspec := os.Getenv("COMSPEC")
	if comspec == "" {
		comspec = "cmd.exe"
	}
	return comspec, nil
}
