package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"wails-gui/storage"
)

type EntryUses = storage.EntryUses
type EntryUseSurface = storage.EntryUseSurface

type contextUseEntry struct {
	Kind  string
	ID    string
	epoch uint64
}

type contextUseCapture struct {
	entries []contextUseEntry
}
type contextUseCaptureKey struct{}
type contextUseRefKey struct{}

// Snapshot before collecting source text, never after publication or a model call.
func (a *App) contextUseEpoch() uint64 {
	if a == nil {
		return 0
	}
	return a.store.ContextUseEpoch()
}

func (a *App) recordContextUses(kind string, ids []string, surface, ref string) {
	if a != nil && a.store != nil {
		a.store.RecordContextUses(kind, ids, surface, ref)
	}
}

func (a *App) recordContextEntries(entries []contextUseEntry, surface, ref string) {
	if a == nil || a.store == nil {
		return
	}
	uses := make([]storage.ContextUse, 0, len(entries))
	for _, entry := range entries {
		uses = append(uses, storage.ContextUse{Kind: entry.Kind, ID: entry.ID, Surface: surface, Ref: ref, Epoch: entry.epoch})
	}
	a.store.RecordCapturedContextUseBatch(uses)
}

func memoryResultUseEntries(results []MemorySearchResult, epoch uint64) []contextUseEntry {
	var entries []contextUseEntry
	for _, result := range results {
		kind := string(MemorySource(result.Source))
		if kind != "memory" && kind != "knowledge" {
			continue
		}
		entries = append(entries, contextUseEntry{kind, strings.TrimSuffix(filepath.Base(result.Path), filepath.Ext(result.Path)), epoch})
	}
	return entries
}

func (a *App) recordMemorySelectionUses(ctx context.Context, results []MemorySearchResult, epoch uint64) {
	entries := memoryResultUseEntries(results, epoch)
	if capture, ok := ctx.Value(contextUseCaptureKey{}).(*contextUseCapture); ok {
		capture.entries = append(capture.entries, entries...)
		return
	}
	ref, _ := ctx.Value(contextUseRefKey{}).(string)
	a.recordContextEntries(entries, "chat", ref)
}

func (a *App) recordBriefContextUses(brief MobileBriefV1, epoch uint64) {
	cited := map[string]bool{}
	for _, item := range brief.Sections.Learned {
		for _, ref := range item.Refs {
			cited[ref] = true
		}
	}
	var entries []contextUseEntry
	for _, ref := range brief.Evidence {
		if cited[ref.Ref] && (ref.SourceKind == "memory" || ref.SourceKind == "knowledge") {
			entries = append(entries, contextUseEntry{ref.SourceKind, ref.SourceID, epoch})
		}
	}
	a.recordContextEntries(entries, "brief", brief.BriefID)
}

func (a *App) entryUsesReadable(kind, id string) (bool, error) {
	if (kind != "memory" && kind != "knowledge") || !storage.ContextUseID(id) {
		return false, fmt.Errorf("invalid entry identity")
	}
	if a.IsClosedEyesModeEnabled() {
		return false, nil
	}
	var content string
	switch kind {
	case "memory":
		entry, err := a.GetMemoryEntry(id)
		if err != nil {
			return false, err
		}
		content = entry.Content
	case "knowledge":
		entry, err := a.GetKnowledgeEntry(id)
		if err != nil {
			return false, err
		}
		content = entry.Content
	}
	allow, err := a.memoryReadFilter()
	if err != nil {
		return false, err
	}
	return allow(id, content), nil
}

// GetEntryUses returns identifier-only use statistics through current source gates.
func (a *App) GetEntryUses(kind string, id string) (EntryUses, error) {
	empty := EntryUses{Surfaces: []EntryUseSurface{}}
	readable, err := a.entryUsesReadable(kind, id)
	if err != nil || !readable || a.store == nil {
		return empty, err
	}
	return a.store.EntryUses(context.Background(), kind, id)
}

// GetEntryUseCounts batches reuse counts for memory and knowledge list views.
func (a *App) GetEntryUseCounts(kind string, ids []string) (map[string]int, error) {
	if kind != "memory" && kind != "knowledge" || len(ids) > 10000 {
		return nil, fmt.Errorf("invalid entry list")
	}
	counts := map[string]int{}
	var readableIDs []string
	for _, id := range ids {
		readable, err := a.entryUsesReadable(kind, id)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		counts[id] = 0
		if readable {
			readableIDs = append(readableIDs, id)
		}
	}
	if a.store == nil {
		return counts, nil
	}
	used, err := a.store.EntryUseCounts(context.Background(), kind, readableIDs)
	for id, count := range used {
		counts[id] = count
	}
	return counts, err
}
