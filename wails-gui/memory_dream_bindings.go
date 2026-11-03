package main

import (
	"context"
	"fmt"
)

// Wails bindings for Memory Dream. These get auto-generated into the
// frontend's `wailsjs/go/main/App.{ts,js}` and consumed by the memory
// page's Dream settings drawer.

// GetMemoryDreamStatus returns the current cycle status (running flag,
// last run row, next expected run timestamp, archive count). Frontend
// polls this when the Dream drawer is open.
func (a *App) GetMemoryDreamStatus() DreamStatus {
	if a.memoryDream == nil {
		return DreamStatus{}
	}
	return a.memoryDream.Status()
}

// GetMemoryDreamSettings returns the user's current Dream settings. If
// no settings have been saved yet, DefaultDreamSettings is returned (the
// service falls back to defaults internally, so behavior matches).
func (a *App) GetMemoryDreamSettings() DreamSettings {
	if a.memoryDream == nil {
		return DefaultDreamSettings
	}
	return a.memoryDream.loadSettings()
}

// SaveMemoryDreamSettings persists user-supplied Dream settings. Returns
// the *clamped* settings the service will actually use, so the frontend
// can re-render with corrected values (e.g. an out-of-range threshold
// snapped into [0.7, 0.999]).
func (a *App) SaveMemoryDreamSettings(cfg DreamSettings) (DreamSettings, error) {
	if a.memoryDream == nil {
		return cfg, fmt.Errorf("dream service not initialized")
	}
	if err := a.memoryDream.SaveSettings(cfg); err != nil {
		return cfg, err
	}
	return a.memoryDream.loadSettings(), nil
}

// PreviewMemoryDream runs the layered cycle in dry-run mode: it returns
// every archive/synthesis proposal but writes nothing. Layer 3 still hits
// Haiku so previews cost tokens — the report includes the run id and
// counters so the UI can display "would archive N, would synthesize M".
func (a *App) PreviewMemoryDream() (DreamPreview, error) {
	if a.memoryDream == nil {
		return DreamPreview{}, fmt.Errorf("dream service not initialized")
	}
	p, err := a.memoryDream.RunCycle(context.Background(), "manual", true)
	if err != nil {
		return DreamPreview{}, err
	}
	if p == nil {
		return DreamPreview{}, nil
	}
	return *p, nil
}

// RunMemoryDream commits a full destructive cycle. Frontend should
// always show the preview report first and require an explicit confirm
// before invoking this — there is no undo prompt past the archive layer
// (the archive itself is the undo, exposed via ListArchivedMemories and
// RestoreArchivedMemory).
func (a *App) RunMemoryDream() (DreamRun, error) {
	if a.memoryDream == nil {
		return DreamRun{}, fmt.Errorf("dream service not initialized")
	}
	p, err := a.memoryDream.RunCycle(context.Background(), "manual", false)
	if err != nil {
		return DreamRun{}, err
	}
	if p == nil {
		return DreamRun{}, nil
	}
	return p.Run, nil
}

// CancelMemoryDream signals any in-flight cycle to abort. Returns
// immediately; the cycle finishes its current layer and exits.
func (a *App) CancelMemoryDream() {
	if a.memoryDream == nil {
		return
	}
	a.memoryDream.Stop()
	// Restart the scheduler so future daily ticks still fire — Stop()
	// tears down both the scheduler and the in-flight cycle. We only
	// wanted to abort the cycle; relaunch the scheduler thread.
	a.memoryDream.Start(a.ctx)
}

// GetMemoryDreamRuns returns the recent run history (most recent first).
// `limit` ≤ 0 means "all".
func (a *App) GetMemoryDreamRuns(limit int) []DreamRun {
	if a.memoryDream == nil {
		return nil
	}
	return a.memoryDream.RecentRuns(limit)
}

// ListArchivedMemories returns every archive entry, newest first. `limit`
// ≤ 0 means "all". The UI uses this for the archive drawer and the per-run
// "Undo" affordance.
func (a *App) ListArchivedMemories(limit int) ([]ArchivedMemory, error) {
	return listArchivedMemories(limit)
}

// RestoreArchivedMemory moves a single archive entry back to the live
// store. Returns the restored memory id. Errors when a live memory with
// the same id already exists (the user must delete it first).
func (a *App) RestoreArchivedMemory(archiveID string) (string, error) {
	return a.restoreArchivedMemory(archiveID)
}

// UndoMemoryDreamRun restores every archive entry produced by the given
// run id. Returns the count restored. Individual restore failures are
// logged but don't abort the bulk operation — partial undo is better
// than no undo.
func (a *App) UndoMemoryDreamRun(runID string) (int, error) {
	return a.undoDreamRun(runID)
}
