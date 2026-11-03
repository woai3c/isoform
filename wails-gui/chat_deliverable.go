package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/skratchdot/open-golang/open"
)

// chatDeliverablesDirective is injected into the prompt for interactive UI
// chat turns (composePrompt with an empty platform). It teaches the agent to
// declare any files it produced via a <deliverables> block so the frontend
// can surface them as clickable "open" chips below the reply — the same
// contract scheduled automations already use (see composeCronPrompt).
//
// Two deliberate differences from the cron variant:
//   - Declaration is OPTIONAL: interactive chat is conversational, so we do
//     not force a substantive final message or a delivery target.
//   - The "final deliverables only, no intermediate scripts/logs" rule is
//     emphasized so generated helper files (e.g. a create-pptx.js used to
//     build the real .pptx) are NOT surfaced as noise chips.
const chatDeliverablesDirective = `<deliverables-protocol>
If you create or modify any files the user would want to keep (documents, spreadsheets, presentations, PDFs, images, exports, archives, etc.), save them under your current working directory — never /tmp or $TMPDIR, which the user cannot browse — and declare them at the very end of your final reply:
<deliverables>
/absolute/path/to/file1.pptx | One-sentence description of what this file is
/absolute/path/to/file2.csv | Raw data behind the slides, for further analysis
</deliverables>
Rules:
- List ONLY the final deliverables the user actually wanted. Do NOT list intermediate scripts, build helpers, logs, package manifests, or temporary files — surfacing those as openable files is confusing noise.
- Use absolute paths, one file per line. After each path, append " | " followed by ONE short sentence (in the same language as the conversation) saying what the file is, so the user can tell files apart at a glance. Do not restate the filename or extension, and do not repeat a sentence you already wrote in your reply — the description is shown right next to it, so describe what is INSIDE the file (scope, contents, format) instead.
- If you produced no files, omit the block entirely. Do not emit an empty block.
</deliverables-protocol>

`

// OpenChatDeliverable opens a file an interactive chat agent declared via its
// <deliverables> block, in the system's default application.
//
// Safety model mirrors OpenAutomationDeliverable:
//   - Path must resolve inside the session's temp work dir or the user's home
//     directory (validateDeliverablePath's allow-list always appends home).
//   - Extension must be on the openable whitelist (isOpenableDeliverableExt),
//     so a stray shell script or executable cannot be one-click run.
//
// tag is the session localId (the same value passed to SendPrompt), used to
// resolve the standalone-chat temp dir where generated files land when the
// session has no pinned project directory.
func (a *App) OpenChatDeliverable(tag string, path string) error {
	absPath, err := a.resolveDeliverablePath(tag, path)
	if err != nil {
		return err
	}
	if !isOpenableDeliverableExt(absPath) {
		return fmt.Errorf("file type is not openable from this UI")
	}
	return open.Start(absPath)
}

// DeliverableOpenWithApp describes one application that can open a deliverable,
// for the "Open with" sub-menu. Icon is a base64 PNG data URL (may be empty).
type DeliverableOpenWithApp struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Icon      string `json:"icon"`
	IsDefault bool   `json:"isDefault"`
}

// DeliverableInfo is the lightweight metadata shown on a deliverable chip
// (subtitle: type + size). Ext is the uppercase extension without the dot.
type DeliverableInfo struct {
	Name      string `json:"name"`
	Ext       string `json:"ext"`
	SizeBytes int64  `json:"sizeBytes"`
}

// resolveDeliverablePath turns a declared deliverable path (absolute, or
// relative to the session temp dir) into a validated absolute path, applying
// the same existence + containment checks OpenChatDeliverable used. It does
// NOT apply the openable-extension whitelist — callers that hand the file to
// an app (open / open-with) must add that check; reveal/info do not need it.
func (a *App) resolveDeliverablePath(tag string, path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path is required")
	}

	// peekSessionTempDir is side-effect-free: if the session never created a
	// temp dir (e.g. a project-pinned chat), it returns "" and the home
	// directory in validateDeliverablePath's allow-list still covers files
	// the agent wrote under the user's project.
	sessionTempDir := a.peekSessionTempDir(tag)

	// Declared deliverables should be absolute (the directive demands it), but
	// resolve relative paths against the session temp dir as a fallback so a
	// well-behaved-but-relative declaration still resolves.
	absPath := path
	if !filepath.IsAbs(path) {
		if sessionTempDir == "" {
			return "", fmt.Errorf("cannot resolve relative path without session directory")
		}
		absPath = filepath.Join(sessionTempDir, path)
	}

	// Existence first so a missing file reports "does not exist" rather than
	// the misleading "outside allowed directories" that EvalSymlinks would
	// trigger for a nonexistent path (same ordering as OpenAutomationDeliverable).
	if _, err := os.Stat(absPath); err != nil {
		return "", fmt.Errorf("file does not exist: %w", err)
	}

	// Current standalone chats normally use a stable directory under home, but
	// older sessions and stable-directory failures may use an os.MkdirTemp
	// fallback. The in-memory tag->dir cache is lost on restart, so allow the OS
	// temp dir as a fallback root for those historical files. The
	// openable-extension whitelist applied by callers still blocks executables.
	if !validateDeliverablePath(absPath, sessionTempDir, os.TempDir()) {
		return "", fmt.Errorf("path is outside the allowed directories for this chat")
	}

	return absPath, nil
}

// ChatDeliverableInfo returns the type + size metadata for a deliverable chip
// subtitle. Returns an error if the file is missing or outside the allow-list.
func (a *App) ChatDeliverableInfo(tag string, path string) (*DeliverableInfo, error) {
	absPath, err := a.resolveDeliverablePath(tag, path)
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(absPath)
	if err != nil {
		return nil, fmt.Errorf("stat deliverable: %w", err)
	}
	ext := strings.TrimPrefix(strings.ToUpper(filepath.Ext(absPath)), ".")
	return &DeliverableInfo{
		Name:      filepath.Base(absPath),
		Ext:       ext,
		SizeBytes: fi.Size(),
	}, nil
}

// ListChatDeliverableOpenWithApps returns the applications that can open the
// deliverable, for the "Open with" sub-menu. macOS-only; other platforms
// return an empty list (the frontend then hides the sub-menu).
func (a *App) ListChatDeliverableOpenWithApps(tag string, path string) ([]DeliverableOpenWithApp, error) {
	absPath, err := a.resolveDeliverablePath(tag, path)
	if err != nil {
		return nil, err
	}
	if !isOpenableDeliverableExt(absPath) {
		return nil, fmt.Errorf("file type is not openable from this UI")
	}
	return listOpenWithApps(absPath), nil
}

// OpenChatDeliverableWithApp opens the deliverable using a specific application
// bundle (appPath, as returned by ListChatDeliverableOpenWithApps).
func (a *App) OpenChatDeliverableWithApp(tag string, path string, appPath string) error {
	absPath, err := a.resolveDeliverablePath(tag, path)
	if err != nil {
		return err
	}
	if !isOpenableDeliverableExt(absPath) {
		return fmt.Errorf("file type is not openable from this UI")
	}
	return openWithApp(absPath, appPath)
}

// RevealChatDeliverable selects the deliverable in the system file manager.
// No extension whitelist: revealing a file never executes it.
func (a *App) RevealChatDeliverable(tag string, path string) error {
	absPath, err := a.resolveDeliverablePath(tag, path)
	if err != nil {
		return err
	}
	return revealInFileManager(absPath)
}
