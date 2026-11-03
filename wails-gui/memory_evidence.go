package main

import (
	"encoding/json"
	"strings"

	"wails-gui/internal/pii"
)

// MemoryEvidence is a bounded projection without source identifiers.
type MemoryEvidence struct {
	Day     int64    `json:"day"`
	Apps    []string `json:"apps"`
	Snippet string   `json:"snippet"`
}

// GetMemoryEvidence reads existing observations through current memory and
// activity privacy gates, rather than the personal timeline's broader access.
func (a *App) GetMemoryEvidence(id string) ([]MemoryEvidence, error) {
	entry, err := a.GetMemoryEntry(id)
	if err != nil {
		return nil, err
	}
	allow, err := a.memoryReadFilter()
	if err != nil {
		return nil, err
	}
	if !allow(id, entry.Content) || a.activity == nil || a.activity.store == nil {
		return nil, nil
	}
	sessions := memoryEvidenceSessionIDs(entry.Content)
	rows := make([]MemoryEvidence, 0)
	for index, sessionID := range sessions {
		if index >= 40 {
			break
		}
		if !a.activityDependencyAllowsModelRead(sessionID, entry.Apps) {
			continue
		}
		session, readErr := a.activity.store.GetSessionByID(sessionID)
		if readErr != nil {
			continue
		}
		var apps []string
		if json.Unmarshal([]byte(session.AppNames), &apps) != nil {
			continue
		}
		snapshots, readErr := a.activity.store.GetSessionSnapshots(sessionID)
		if readErr != nil {
			return nil, readErr
		}
		snippet := ""
		for _, snapshot := range snapshots {
			parsed := parseSnapshotContent(snapshot)
			if activityEvidenceSecureSnapshot(parsed) {
				continue
			}
			body := cleanBody(parsed, snapshot, "")
			if strings.TrimSpace(body) == "" {
				body = strings.TrimSpace(snapshot.AXText)
			}
			if body == "" {
				// Resolve only the frame whose privacy metadata was checked above.
				ocr, err := a.activity.store.GetExactOCRTextBySnapshot(sessionID, snapshot.ID)
				if err != nil {
					return nil, err
				}
				body = cleanBody(parsed, snapshot, ocr)
			}
			if strings.TrimSpace(body) == "" {
				continue
			}
			snippet, _ = pii.RedactText(body)
			snippet = strings.Join(strings.Fields(snippet), " ")
			chars := []rune(snippet)
			if len(chars) > 200 {
				snippet = string(chars[:200])
			}
			break
		}
		if !a.activityDependencyAllowsModelRead(sessionID, entry.Apps) {
			continue
		}
		rows = append(rows, MemoryEvidence{Day: session.StartTime, Apps: apps, Snippet: snippet})
	}
	if !allow(id, entry.Content) {
		return nil, nil
	}
	return rows, nil
}

// memoryEvidenceSessionIDs returns supporting links, never comparison-input
// privacy dependencies. Old version-2 activity writers flattened both into
// activity_sessions; without an explicit evidence list those links are ambiguous.
func memoryEvidenceSessionIDs(content string) []string {
	doc, ok := parseOKFDocument(content)
	if ok {
		if _, present := doc.Frontmatter["evidence_sessions"]; present {
			return uniqueNonEmpty(okfStringSlice(doc.Frontmatter, "evidence_sessions"), strings.TrimSpace)
		}
	}
	fm := parseMemoryFrontmatter(content)
	return memoryEvidenceSessionIDsFromParts(content, doc, ok, fm)
}

func memoryEvidenceSessionIDsFromDocument(document parsedMemoryEntryDocument) []string {
	return memoryEvidenceSessionIDsFromParts(document.content, document.doc, document.ok, document.frontmatter)
}

func memoryEvidenceSessionIDsFromParts(content string, doc okfDocument, ok bool, fm memoryFrontmatter) []string {
	if ok {
		if _, present := doc.Frontmatter["evidence_sessions"]; present {
			return uniqueNonEmpty(okfStringSlice(doc.Frontmatter, "evidence_sessions"), strings.TrimSpace)
		}
		if fm.ReadProvenance != "" {
			provenance := memoryReadProvenanceFromDocument(content, fm, ok)
			for _, kind := range provenance.Kinds {
				if provenance.Version >= 2 && kind == "activity" {
					return nil
				}
			}
		}
	}
	return uniqueNonEmpty(fm.ActivitySessionIDs, strings.TrimSpace)
}
