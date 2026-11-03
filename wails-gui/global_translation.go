package main

import (
	"fmt"
	"strings"
)

type GlobalTranslationRequest struct {
	// Target accepts: follow-ui, zh-CN/Chinese/中文, en/English, other/custom,
	// or a direct custom language name.
	Target string `json:"target"`
	// Other is used when Target is "other" or "custom".
	Other string `json:"other,omitempty"`
	// Language is an alias for Other so callers can send a clearer field name.
	Language string `json:"language,omitempty"`
	// IncludeHistory translates historical snapshots for each live entry after
	// translating the live file. Defaults to false to preserve existing callers.
	IncludeHistory bool `json:"includeHistory,omitempty"`
}

type GlobalTranslationKindSummary struct {
	Total      int `json:"total"`
	Translated int `json:"translated"`
	Failed     int `json:"failed"`
}

type GlobalTranslationBatchItem struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type GlobalTranslationBatchSummary struct {
	TargetLanguage string                       `json:"targetLanguage"`
	IncludeHistory bool                         `json:"includeHistory,omitempty"`
	Total          int                          `json:"total"`
	Translated     int                          `json:"translated"`
	Failed         int                          `json:"failed"`
	Memories       GlobalTranslationKindSummary `json:"memories"`
	Knowledge      GlobalTranslationKindSummary `json:"knowledge"`
	Items          []GlobalTranslationBatchItem `json:"items"`
}

type GlobalTranslationBatchStart struct {
	Scope          string `json:"scope"`
	TargetLanguage string `json:"targetLanguage"`
	IncludeHistory bool   `json:"includeHistory,omitempty"`
	Total          int    `json:"total"`
}

type GlobalTranslationBatchEvent struct {
	Scope   string                         `json:"scope"`
	Summary *GlobalTranslationBatchSummary `json:"summary,omitempty"`
	Error   string                         `json:"error,omitempty"`
}

func (a *App) StartTranslateAllCurrentMemoryEntries(req GlobalTranslationRequest) (GlobalTranslationBatchStart, error) {
	languageName, err := a.resolveGlobalTranslationLanguage(req)
	if err != nil {
		return GlobalTranslationBatchStart{}, err
	}

	memories, err := a.GetMemoryEntries()
	if err != nil {
		return GlobalTranslationBatchStart{}, err
	}

	start := GlobalTranslationBatchStart{
		Scope:          "memory",
		TargetLanguage: languageName,
		IncludeHistory: req.IncludeHistory,
		Total:          estimateGlobalTranslationTotal(len(memories), req.IncludeHistory),
	}
	go func() {
		summary := GlobalTranslationBatchSummary{
			TargetLanguage: languageName,
			IncludeHistory: req.IncludeHistory,
		}
		a.translateMemoryEntriesToLanguage(memories, languageName, req.IncludeHistory, &summary)
		a.emitBrowserEvent("global-translation:done", GlobalTranslationBatchEvent{
			Scope:   "memory",
			Summary: &summary,
		})
	}()

	return start, nil
}

func (a *App) StartTranslateAllCurrentKnowledgeEntries(req GlobalTranslationRequest) (GlobalTranslationBatchStart, error) {
	languageName, err := a.resolveGlobalTranslationLanguage(req)
	if err != nil {
		return GlobalTranslationBatchStart{}, err
	}

	knowledge, err := a.GetKnowledgeEntries()
	if err != nil {
		return GlobalTranslationBatchStart{}, err
	}

	start := GlobalTranslationBatchStart{
		Scope:          "knowledge",
		TargetLanguage: languageName,
		IncludeHistory: req.IncludeHistory,
		Total:          estimateGlobalTranslationTotal(len(knowledge), req.IncludeHistory),
	}
	go func() {
		summary := GlobalTranslationBatchSummary{
			TargetLanguage: languageName,
			IncludeHistory: req.IncludeHistory,
		}
		a.translateKnowledgeEntriesToLanguage(knowledge, languageName, req.IncludeHistory, &summary)
		a.emitBrowserEvent("global-translation:done", GlobalTranslationBatchEvent{
			Scope:   "knowledge",
			Summary: &summary,
		})
	}()

	return start, nil
}

func estimateGlobalTranslationTotal(liveCount int, includeHistory bool) int {
	if !includeHistory {
		return liveCount
	}
	return liveCount * 2
}

func (a *App) translateMemoryEntriesToLanguage(entries []MemoryEntry, languageName string, includeHistory bool, summary *GlobalTranslationBatchSummary) {
	for _, entry := range entries {
		summary.Total++
		summary.Memories.Total++
		item := GlobalTranslationBatchItem{
			Kind:  "memory",
			ID:    entry.ID,
			Title: entry.Title,
		}
		if _, err := a.translateMemoryEntryToLanguage(entry.ID, languageName); err != nil {
			item.Status = "failed"
			item.Error = err.Error()
			summary.Failed++
			summary.Memories.Failed++
		} else {
			item.Status = "translated"
			summary.Translated++
			summary.Memories.Translated++
		}
		summary.Items = append(summary.Items, item)
		if includeHistory {
			a.translateMemoryHistoryToLanguage(entry, languageName, summary)
		}
	}
}

func (a *App) translateMemoryHistoryToLanguage(entry MemoryEntry, languageName string, summary *GlobalTranslationBatchSummary) {
	versions, err := a.ListMemoryVersions(entry.ID)
	if err != nil {
		a.appendMemoryHistoryTranslationFailure(entry, err, summary)
		return
	}
	if len(versions) == 0 {
		return
	}
	summary.Total++
	summary.Memories.Total++
	item := GlobalTranslationBatchItem{
		Kind:  "memory-history",
		ID:    entry.ID,
		Title: entry.Title,
	}
	if _, err := a.translateAllMemoryVersionsToLanguage(entry.ID, languageName); err != nil {
		item.Status = "failed"
		item.Error = err.Error()
		summary.Failed++
		summary.Memories.Failed++
	} else {
		item.Status = "translated"
		summary.Translated++
		summary.Memories.Translated++
	}
	summary.Items = append(summary.Items, item)
}

func (a *App) appendMemoryHistoryTranslationFailure(entry MemoryEntry, err error, summary *GlobalTranslationBatchSummary) {
	summary.Total++
	summary.Memories.Total++
	summary.Failed++
	summary.Memories.Failed++
	summary.Items = append(summary.Items, GlobalTranslationBatchItem{
		Kind:   "memory-history",
		ID:     entry.ID,
		Title:  entry.Title,
		Status: "failed",
		Error:  err.Error(),
	})
}

func (a *App) translateKnowledgeEntriesToLanguage(entries []KnowledgeEntry, languageName string, includeHistory bool, summary *GlobalTranslationBatchSummary) {
	for _, entry := range entries {
		summary.Total++
		summary.Knowledge.Total++
		item := GlobalTranslationBatchItem{
			Kind:  "knowledge",
			ID:    entry.ID,
			Title: entry.Title,
		}
		if _, err := a.translateKnowledgeEntryToLanguage(entry.ID, languageName); err != nil {
			item.Status = "failed"
			item.Error = err.Error()
			summary.Failed++
			summary.Knowledge.Failed++
		} else {
			item.Status = "translated"
			summary.Translated++
			summary.Knowledge.Translated++
		}
		summary.Items = append(summary.Items, item)
		if includeHistory {
			a.translateKnowledgeHistoryToLanguage(entry, languageName, summary)
		}
	}
}

func (a *App) translateKnowledgeHistoryToLanguage(entry KnowledgeEntry, languageName string, summary *GlobalTranslationBatchSummary) {
	versions, err := a.ListKnowledgeVersions(entry.ID)
	if err != nil {
		a.appendKnowledgeHistoryTranslationFailure(entry, err, summary)
		return
	}
	if len(versions) == 0 {
		return
	}
	summary.Total++
	summary.Knowledge.Total++
	item := GlobalTranslationBatchItem{
		Kind:  "knowledge-history",
		ID:    entry.ID,
		Title: entry.Title,
	}
	if _, err := a.translateAllKnowledgeVersionsToLanguage(entry.ID, languageName); err != nil {
		item.Status = "failed"
		item.Error = err.Error()
		summary.Failed++
		summary.Knowledge.Failed++
	} else {
		item.Status = "translated"
		summary.Translated++
		summary.Knowledge.Translated++
	}
	summary.Items = append(summary.Items, item)
}

func (a *App) appendKnowledgeHistoryTranslationFailure(entry KnowledgeEntry, err error, summary *GlobalTranslationBatchSummary) {
	summary.Total++
	summary.Knowledge.Total++
	summary.Failed++
	summary.Knowledge.Failed++
	summary.Items = append(summary.Items, GlobalTranslationBatchItem{
		Kind:   "knowledge-history",
		ID:     entry.ID,
		Title:  entry.Title,
		Status: "failed",
		Error:  err.Error(),
	})
}

func (a *App) resolveGlobalTranslationLanguage(req GlobalTranslationRequest) (string, error) {
	target := strings.TrimSpace(req.Target)
	switch strings.ToLower(target) {
	case "", "follow-ui", "follow_ui", "ui", "current", "current-ui", "interface":
		return a.currentUILanguageName(), nil
	case "zh", "zh-cn", "zh_cn", "chinese", "simplified chinese", "中文", "简体中文":
		return "Simplified Chinese", nil
	case "en", "english":
		return "English", nil
	case "other", "custom":
		other := strings.TrimSpace(req.Other)
		if other == "" {
			other = strings.TrimSpace(req.Language)
		}
		return sanitizeGlobalTranslationLanguageName(other)
	default:
		return sanitizeGlobalTranslationLanguageName(target)
	}
}

func sanitizeGlobalTranslationLanguageName(languageName string) (string, error) {
	languageName = strings.Join(strings.Fields(strings.TrimSpace(languageName)), " ")
	if languageName == "" {
		return "", fmt.Errorf("target language is required")
	}
	if len(languageName) > 80 {
		return "", fmt.Errorf("target language is too long")
	}
	return languageName, nil
}
