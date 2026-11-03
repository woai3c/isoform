//go:build linux

package notify

import "wails-gui/internal/platform/cmdutil"

// Send displays a desktop notification using notify-send.
// If opts.IconPath is set, it is passed via the -i flag.
func Send(title, body string, opts *Options) error {
	args := []string{title, body}
	if opts != nil && opts.IconPath != "" {
		args = []string{"-i", opts.IconPath, title, body}
	}
	return cmdutil.Command("notify-send", args...).Run()
}
