package main

import (
	"bytes"
	"fmt"
	"os/exec"
	"runtime"
)

// OpenExternalURL opens the given URL with the OS default handler.
// Works for http/https, mailto, tel, and other registered schemes.
// This is more reliable than wails runtime.BrowserOpenURL for mailto:
// on macOS, where NSWorkspace may silently drop non-http URLs inside
// the WKWebView context.
func (a *App) OpenExternalURL(url string) error {
	if url == "" {
		return fmt.Errorf("empty url")
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		// Absolute path to avoid PATH issues when launched via Finder/launchd.
		cmd = exec.Command("/usr/bin/open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("open %q: %w (stderr: %s)", url, err, stderr.String())
	}
	return nil
}
