package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"wails-gui/internal/platform/recentfiles"
)

// Deliverable titles and summaries can identify a private document too. Check
// every attached path before exporting any part of their parent metadata.
func (a *App) deliverableFolderReadFilter(ctx context.Context) func(DeliverableEntry) bool {
	if a == nil || a.store == nil {
		return func(DeliverableEntry) bool { return false }
	}
	raw, err := a.store.GetUIState(ctx, filesPrivateFoldersKey)
	var folders []string
	if err != nil || (raw != "" && json.Unmarshal([]byte(raw), &folders) != nil) {
		return func(DeliverableEntry) bool { return false }
	}
	home, homeErr := os.UserHomeDir()
	if homeErr != nil {
		return func(DeliverableEntry) bool { return false }
	}
	canonical := func(path string) (string, error) {
		// Resolve existing parents as well as files, so a missing output below a
		// symlink cannot turn a private folder into a public metadata source.
		rest := ""
		for {
			resolved, err := filepath.EvalSymlinks(path)
			if err == nil {
				return recentfiles.Canonical(filepath.Join(resolved, rest), home), nil
			}
			if !os.IsNotExist(err) {
				return "", err
			}
			// A dangling link is an existing alias with an unknown target
			// identity, not an ordinary not-yet-written public output.
			info, statErr := os.Lstat(path)
			if statErr == nil && info.Mode()&os.ModeSymlink != 0 {
				return "", errors.New("unresolvable deliverable alias")
			}
			if statErr != nil && !os.IsNotExist(statErr) {
				return "", statErr
			}
			parent := filepath.Dir(path)
			if parent == path {
				return "", errors.New("deliverable ancestor is unavailable")
			}
			rest = filepath.Join(filepath.Base(path), rest)
			path = parent
		}
	}
	for i, folder := range folders {
		if !filepath.IsAbs(folder) {
			return func(DeliverableEntry) bool { return false }
		}
		resolved, err := canonical(folder)
		if err != nil {
			return func(DeliverableEntry) bool { return false }
		}
		folders[i] = resolved
	}
	return func(item DeliverableEntry) bool {
		if ctx.Err() != nil || a.IsClosedEyesModeEnabled() {
			return false
		}
		for _, file := range item.Files {
			if !filepath.IsAbs(file.Path) {
				return false
			}
			path, err := canonical(file.Path)
			if err != nil {
				return false
			}
			for _, folder := range folders {
				if recentfiles.UnderFolder(path, folder) {
					return false
				}
			}
		}
		return true
	}
}
