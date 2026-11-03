//go:build windows

package clipboard

import (
	"strings"

	"wails-gui/internal/platform/cmdutil"
)

// Write copies text to the system clipboard using clip.exe.
func Write(text string) error {
	cmd := cmdutil.Command("cmd", "/c", "clip")
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}
