//go:build darwin

package clipboard

import (
	"strings"

	"wails-gui/internal/platform/cmdutil"
)

// Write copies text to the system clipboard using pbcopy. The change it
// makes is remembered, so the watcher never takes it for the user's copy.
func Write(text string) error {
	cmd := cmdutil.Command("pbcopy")
	cmd.Stdin = strings.NewReader(text)
	if err := cmd.Run(); err != nil {
		return err
	}
	NoteOwnWrite()
	return nil
}
