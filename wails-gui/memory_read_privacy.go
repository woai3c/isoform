package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"wails-gui/internal/pii"
)

// filterMemorySearchReads uses full source provenance, because a matching
// chunk can be far below its file's Apps and Activity Session metadata.
func (a *App) filterMemorySearchReads(store *MemoryStore, results []MemorySearchResult) ([]MemorySearchResult, error) {
	allowRead, err := a.memoryReadFilter()
	if err != nil {
		return nil, err
	}
	return cachedMemorySearchReadFilter(store, allowRead)(results)
}

func cachedMemorySearchReadFilter(store *MemoryStore, allowRead func(string, string) bool) func([]MemorySearchResult) ([]MemorySearchResult, error) {
	allowedPaths := map[string]bool{}
	return func(results []MemorySearchResult) ([]MemorySearchResult, error) {
		if allowRead == nil {
			return nil, nil
		}
		filtered := make([]MemorySearchResult, 0, len(results))
		for _, result := range results {
			path := store.resolveAbsPath(result.Path, MemorySource(result.Source))
			allowed, seen := allowedPaths[path]
			if !seen {
				content, err := os.ReadFile(path)
				id := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
				allowed = err == nil && allowRead(id, string(content))
				allowedPaths[path] = allowed
			}
			if !allowed {
				continue
			}
			result.Snippet, _ = pii.RedactText(result.Snippet)
			filtered = append(filtered, result)
		}
		return filtered, nil
	}
}

// Build one current read policy per operation. The local Memory page retains
// its direct access; model and CLI readers must authorize each full source.
func (a *App) memoryReadFilter() (func(string, string) bool, error) {
	return a.memoryReadFilterContext(context.Background())
}

func (a *App) memoryReadFilterContext(ctx context.Context) (func(string, string) bool, error) {
	if a == nil || a.IsClosedEyesModeEnabled() && !pausedBriefReadAllowed(ctx) {
		return func(string, string) bool { return false }, nil
	}
	var suppressed map[string]struct{}
	if a.activity != nil && a.activity.store != nil {
		var err error
		suppressed, err = a.activity.store.ListCorrectedAgencyLearningTargetIDs()
		if err != nil {
			return nil, fmt.Errorf("read memory corrections: %w", err)
		}
	}
	return func(id, content string) bool {
		if _, corrected := suppressed[id]; corrected {
			return false
		}
		return a.memorySourceAllowsReadContext(ctx, content)
	}, nil
}

func (a *App) memoryEntriesForExternalRead() ([]MemoryEntry, error) {
	allowRead, err := a.memoryReadFilter()
	if err != nil {
		return nil, err
	}
	entries, err := a.GetMemoryEntries()
	if err != nil {
		return nil, err
	}
	out := make([]MemoryEntry, 0, len(entries))
	for _, entry := range entries {
		if allowRead(entry.ID, entry.Content) && memoryInUse(entry.Content) {
			out = append(out, redactMemoryEntryForExternalRead(entry))
		}
	}
	return out, nil
}

func (a *App) memoryEntryForExternalRead(id string) (MemoryEntry, error) {
	allowRead, err := a.memoryReadFilter()
	if err != nil {
		return MemoryEntry{}, err
	}
	entry, err := a.GetMemoryEntry(id)
	if err != nil {
		return MemoryEntry{}, err
	}
	if !allowRead(entry.ID, entry.Content) || !memoryInUse(entry.Content) {
		return MemoryEntry{}, os.ErrNotExist
	}
	return redactMemoryEntryForExternalRead(entry), nil
}

func redactMemoryEntryForExternalRead(entry MemoryEntry) MemoryEntry {
	entry.Title, _ = pii.RedactText(entry.Title)
	entry.Content, _ = pii.RedactText(entry.Content)
	entry.Apps = append([]string(nil), entry.Apps...)
	for i := range entry.Apps {
		entry.Apps[i], _ = pii.RedactText(entry.Apps[i])
	}
	return entry
}

func (a *App) memorySourceAllowsRead(content string) bool {
	return a.memorySourceAllowsReadContext(context.Background(), content)
}

func (a *App) memorySourceAllowsReadContext(ctx context.Context, content string) bool {
	if a == nil || a.IsClosedEyesModeEnabled() && !pausedBriefReadAllowed(ctx) {
		return false
	}
	return a.memorySourceAllowsReadWithDependency(content, func(id string, apps []string) bool {
		return a.activityDependencyAllowsModelReadWithCacheContext(ctx, id, apps, nil)
	})
}

// Shared source checks also serve the owner-only stored portrait reader.
func (a *App) memorySourceAllowsReadWithDependency(content string, allowsDependency func(string, []string) bool) bool {
	fm := parseMemoryFrontmatter(content)
	apps, sessions := fm.Apps, fm.ActivitySessionIDs
	if fm.ReadProvenance != "" {
		// Writers flatten dependencies when updating or dreaming. Validate
		// explicit envelopes here, but retain v1 conversation dependencies:
		// MCP excludes their ambiguous connector lineage from sharing while
		// internal recall still needs their concrete app/session privacy gates.
		var provenance memoryReadProvenance
		if _, yamlHeader := parseOKFDocument(content); !yamlHeader || json.Unmarshal([]byte(fm.ReadProvenance), &provenance) != nil || (provenance.Version != 1 && provenance.Version != 2) || provenance.ReadBlocked {
			return false
		}
		apps = compactStrings(append(apps, provenance.Apps...))
		sessions = compactStrings(append(sessions, provenance.Sessions...))
	}
	for _, name := range apps {
		if activitySourceAppIsSensitive(name, name) {
			return false
		}
	}
	for _, id := range sessions {
		if a.activity == nil || a.activity.store == nil {
			return false
		}
		if !allowsDependency(id, apps) {
			return false
		}
	}
	return true
}
