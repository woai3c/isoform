package main

import (
	"encoding/json"

	"wails-gui/internal/pii"
)

// MarshalJSON keeps internal source bytes intact while summaries rendered from
// Markdown use the same neutral mask as title and search read models. Persisted
// documents and provenance checks continue to use the original Content field.
func (entry MemoryEntry) MarshalJSON() ([]byte, error) {
	type wire MemoryEntry
	out := wire(entry)
	out.Title = pii.DisplayText(out.Title)
	out.Content = pii.DisplayRedactions(out.Content)
	// Lists need titles/content only. Never build or emit per-entry links
	// there; single-entry delivery explicitly opts into the existing gate.
	out.ActivitySessionIDs = []string{}
	if entry.displayApp != nil {
		out.ActivitySessionIDs = entry.displayApp.memoryEvidenceSessionLinks(entry.ID, entry.Content)
	}
	return json.Marshal(out)
}

// MarshalJSON also covers legacy knowledge descriptions rendered from Markdown.
func (entry KnowledgeEntry) MarshalJSON() ([]byte, error) {
	type wire KnowledgeEntry
	out := wire(entry)
	out.Title = pii.DisplayText(out.Title)
	out.Content = pii.DisplayRedactions(out.Content)
	return json.Marshal(out)
}

// The detail page can fall back to these links when readable evidence is empty.
// Resolve lazily at serialization so internal recall/correction code retains its
// metadata, and ordinary internal list reads do not build evidence per entry.
func (a *App) memoryEvidenceSessionLinks(id, content string) []string {
	ids := []string{}
	if len(memoryEvidenceSessionIDs(content)) == 0 {
		return ids
	}
	rows, err := a.GetMemoryEvidenceReadable(id)
	if err != nil {
		return ids
	}
	current, err := a.GetMemoryEntry(id)
	if err != nil || current.Content != content || !a.memorySourceAllowsRead(current.Content) {
		return ids
	}
	for _, row := range rows {
		ids = append(ids, row.ActivitySessionID)
	}
	return ids
}
