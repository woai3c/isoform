package main

import (
	"fmt"
	"net/http"

	terminalsvc "wails-gui/internal/terminal"
)

// Re-export terminal types so the Wails API shape remains unchanged.
type TerminalInfo = terminalsvc.Info
type TerminalRemoteEnv = terminalsvc.RemoteEnv

func isLoopbackRemoteAddr(remoteAddr string) bool {
	return terminalsvc.IsLoopbackRemoteAddr(remoteAddr)
}

func isAllowedTerminalOrigin(origin string) bool {
	return terminalsvc.IsAllowedTerminalOrigin(origin)
}

func allowTerminalWSRequest(r *http.Request) bool {
	return terminalsvc.AllowWSRequest(r)
}

// CreateTerminal starts a new terminal shell in the given directory.
// fgColor/bgColor are optional hex theme colors (e.g. "#c0caf5", "#0f0f14")
// used for responding to terminal color queries from vim/nvim.
// remoteEnv is optional — when provided, the terminal runs in the specified
// remote environment (WSL or SSH). Nil means local (default).
func (a *App) CreateTerminal(workDir string, fgColor string, bgColor string, remoteEnv *TerminalRemoteEnv) (*TerminalInfo, error) {
	if a.termMgr == nil {
		return nil, fmt.Errorf("terminal manager is not initialized")
	}
	var colors *terminalsvc.ThemeColors
	if fgColor != "" || bgColor != "" {
		colors = &terminalsvc.ThemeColors{Foreground: fgColor, Background: bgColor}
	}
	return a.termMgr.CreateTerminal(a.ctx, workDir, colors, remoteEnv)
}

// ResizeTerminal updates the PTY window size.
func (a *App) ResizeTerminal(id string, rows, cols uint16) error {
	if a.termMgr == nil {
		return fmt.Errorf("terminal manager is not initialized")
	}
	return a.termMgr.ResizeTerminal(id, rows, cols)
}

// CloseTerminal kills the terminal shell and cleans up.
func (a *App) CloseTerminal(id string) error {
	if a.termMgr == nil {
		return nil
	}
	a.termMgr.CloseTerminal(id)
	return nil
}

// ListTerminals returns info for all active terminals.
func (a *App) ListTerminals() []TerminalInfo {
	if a.termMgr == nil {
		return nil
	}
	return a.termMgr.ListTerminals()
}
