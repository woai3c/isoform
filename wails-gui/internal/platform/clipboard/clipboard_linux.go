//go:build linux

package clipboard

import (
	"strings"

	"wails-gui/internal/platform/cmdutil"
)

// Write copies text to the system clipboard using xclip.
func Write(text string) error {
	cmd := cmdutil.Command("xclip", "-selection", "clipboard")
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}
