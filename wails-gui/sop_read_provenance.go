package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"wails-gui/internal/sop"
)

// An author request being manual does not make its observed evidence manual.
// Generated bundles always retain this code-owned proof alongside SourceIDs.
// A source-free handwritten SourceManual bundle has no generated proof.
type sopReadProvenance struct {
	Version     int      `json:"version"`
	Apps        []string `json:"apps,omitempty"`
	ReadBlocked bool     `json:"readBlocked,omitempty"`
}

func sopReadProvenanceJSON(proof sopReadProvenance) string {
	data, _ := json.Marshal(proof)
	return string(data)
}

func parseSOPReadProvenance(raw string) (sopReadProvenance, bool) {
	if raw == "" {
		return sopReadProvenance{}, true
	}
	var proof sopReadProvenance
	if json.Unmarshal([]byte(raw), &proof) != nil || proof.Version != 1 {
		return sopReadProvenance{Version: 1, ReadBlocked: true}, false
	}
	return proof, !proof.ReadBlocked
}

func mergeSOPReadProvenance(first, second string) string {
	if first == "" {
		return second
	}
	if second == "" {
		return first
	}
	left, leftOK := parseSOPReadProvenance(first)
	right, rightOK := parseSOPReadProvenance(second)
	return sopReadProvenanceJSON(sopReadProvenance{
		Version: 1, Apps: uniqueNonEmpty(append(left.Apps, right.Apps...), strings.TrimSpace),
		ReadBlocked: !leftOK || !rightOK,
	})
}

type sopSeedMemorySource struct {
	Source  MemorySource
	ID      string
	Content string
}

type sopSeedSources struct {
	SOPs        []sop.Entry
	Sessions    []string
	Apps        []string
	Memories    []sopSeedMemorySource
	ReadBlocked bool
}

func appendSOPSeedEvidence(builder *strings.Builder, text string, sources *sopSeedSources, captured sopSeedSources) bool {
	before := builder.Len()
	more := appendDedupWithinBudget(builder, redactActivityModelText(text), sopSeedMaxBytes)
	if builder.Len() > before {
		sources.Sessions = append(sources.Sessions, captured.Sessions...)
		sources.Apps = append(sources.Apps, captured.Apps...)
	}
	return more
}

func (sources *sopSeedSources) captureMemory(source MemorySource) func(string, string) {
	return func(id, content string) {
		sources.Memories = append(sources.Memories, sopSeedMemorySource{Source: source, ID: id, Content: content})
		proof := memoryReadProvenanceFromContent(content)
		sources.Sessions = append(sources.Sessions, proof.Sessions...)
		sources.Apps = append(sources.Apps, proof.Apps...)
		// Preserve restrictions even when an old note cannot prove where its
		// observed text originated. Never invent a session ID to authorize it.
		sources.ReadBlocked = sources.ReadBlocked || proof.ReadBlocked ||
			(len(proof.Sessions) == 0 && (len(proof.Apps) > 0 || proof.Withheld))
	}
}

func (sources *sopSeedSources) captureSOPTarget(entry sop.Entry) {
	sources.Sessions = append(sources.Sessions, entry.SourceIDs...)
	proof, valid := parseSOPReadProvenance(entry.ReadProvenance)
	sources.Apps = append(sources.Apps, proof.Apps...)
	sources.ReadBlocked = sources.ReadBlocked || !valid
}

func (sources sopSeedSources) readProvenance() sopReadProvenance {
	return sopReadProvenance{
		Version: 1, Apps: uniqueNonEmpty(sources.Apps, strings.TrimSpace),
		ReadBlocked: sources.ReadBlocked || len(uniqueNonEmpty(sources.Sessions, strings.TrimSpace)) == 0,
	}
}

func (a *App) sopSeedSourcesAllowRead(sources sopSeedSources) bool {
	if a == nil || a.IsClosedEyesModeEnabled() || !a.sopModelEntriesAllowRead(sources.SOPs) {
		return false
	}
	for _, app := range sources.Apps {
		if activitySourceAppIsSensitive(app, app) {
			return false
		}
	}
	if !a.sopSessionIDsAllowRead(sources.Sessions, sources.Apps) {
		return false
	}
	if len(sources.Memories) == 0 {
		return true
	}
	allowRead, err := a.memoryReadFilter()
	if err != nil {
		return false
	}
	for _, captured := range sources.Memories {
		if !allowRead(captured.ID, captured.Content) {
			return false
		}
		var dir string
		switch captured.Source {
		case MemorySourceMemory:
			dir, err = memoryDir()
		case MemorySourceKnowledge:
			dir, err = knowledgeDir()
		default:
			return false
		}
		if err != nil {
			return false
		}
		current, err := os.ReadFile(filepath.Join(dir, captured.ID+".md"))
		if err != nil || !allowRead(captured.ID, string(current)) {
			return false
		}
	}
	return true
}

func (a *App) sopSessionIDsAllowRead(ids []string, storedApps ...[]string) bool {
	var apps []string
	for _, values := range storedApps {
		apps = append(apps, values...)
	}
	if a == nil || a.IsClosedEyesModeEnabled() {
		return false
	}
	for _, id := range uniqueNonEmpty(ids, strings.TrimSpace) {
		if a.activity == nil || a.activity.store == nil {
			return false
		}
		if !a.activityDependencyAllowsModelRead(id, apps) {
			return false
		}
	}
	return true
}

// Capture the runs which actually contributed to a rendered bucket. The time
// window alone cannot describe a bucket spanning older source sessions.
func (a *App) captureSOPActivitySources(activity ActivityRow, sources *sopSeedSources) bool {
	if a == nil || a.activity == nil || a.activity.store == nil {
		return false
	}
	runIDs := composeParseStrings(activity.RunIDs)
	if len(runIDs) == 0 {
		return false
	}
	var ids, apps []string
	for _, id := range runIDs {
		run, err := a.activity.store.GetTimelineRunByID(id)
		if err != nil || run == nil || !a.activityTimelineRunAllowsModelRead(a.activity.store, *run) {
			return false
		}
		ids = append(ids, run.SessionIDs...)
		apps = append(apps, run.AppName, run.AppKey)
	}
	for app := range composeParseInt64Map(activity.ActiveMsByApp) {
		apps = append(apps, app)
	}
	sources.Sessions = append(sources.Sessions, ids...)
	sources.Apps = append(sources.Apps, apps...)
	return true
}
