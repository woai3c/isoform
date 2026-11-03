package main

import (
	"context"
	"errors"
	"strings"
	"time"
)

const (
	memoryContextDebugVersion       = "memory-context-debug-v1"
	memoryContextDebugPreviewChars  = 320
	memoryContextDebugDetailBudget  = 128_000
	memoryContextDebugSearchTimeout = 10 * time.Second
)

type MemoryContextDebugCounts struct {
	Discovered         int `json:"discovered"`
	CorrectedExcluded  int `json:"correctedExcluded"`
	AfterCorrection    int `json:"afterCorrection"`
	BelowRelevance     int `json:"belowRelevance"`
	Empty              int `json:"empty"`
	Relevant           int `json:"relevant"`
	Selected           int `json:"selected"`
	SelectedMemory     int `json:"selectedMemory"`
	SelectedKnowledge  int `json:"selectedKnowledge"`
	SelectedOther      int `json:"selectedOther"`
	SelectedChars      int `json:"selectedChars"`
	Truncated          int `json:"truncated"`
	BudgetOmitted      int `json:"budgetOmitted"`
	DetailItemsOmitted int `json:"detailItemsOmitted"`
}

type MemoryContextDebugItem struct {
	Path      string  `json:"path"`
	Source    string  `json:"source"`
	StartLine int     `json:"startLine"`
	EndLine   int     `json:"endLine"`
	Score     float64 `json:"score"`
	Preview   string  `json:"preview"`
}

type MemoryContextDebugSnapshot struct {
	Version             string                   `json:"version"`
	Query               string                   `json:"query"`
	MinScore            float64                  `json:"minScore"`
	CharacterBudget     int                      `json:"characterBudget"`
	DetailPayloadBudget int                      `json:"detailPayloadBudget"`
	Counts              MemoryContextDebugCounts `json:"counts"`
	Selected            []MemoryContextDebugItem `json:"selected"`
	CountsReconcile     bool                     `json:"countsReconcile"`
	ReadOnly            bool                     `json:"readOnly"`
	GeneratedAt         int64                    `json:"generatedAt"`
	DurationMs          int64                    `json:"durationMs"`
}

func init() {
	browserRPCAllowlist["GetMemoryContextDebugSnapshot"] = true
}

// GetMemoryContextDebugSnapshot runs the exact production Memory/Knowledge
// discovery and prompt-budget selection without sending a message or mutating
// product state.
func (a *App) GetMemoryContextDebugSnapshot(
	query string,
) (MemoryContextDebugSnapshot, error) {
	if a == nil {
		return MemoryContextDebugSnapshot{}, errors.New("app is unavailable")
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return MemoryContextDebugSnapshot{}, errors.New("message text is required")
	}
	startedAt := time.Now()
	ctx, cancel := context.WithTimeout(
		context.Background(),
		memoryContextDebugSearchTimeout,
	)
	defer cancel()
	resolution, err := a.resolveMemoryContext(ctx, query)
	if err != nil {
		return MemoryContextDebugSnapshot{}, err
	}
	selection := resolution.Selection
	counts := MemoryContextDebugCounts{
		Discovered:        resolution.DiscoveredCount,
		CorrectedExcluded: resolution.CorrectedExclusions,
		AfterCorrection:   selection.CandidateCount,
		BelowRelevance:    selection.BelowScoreCount,
		Empty:             selection.EmptyCount,
		Relevant: selection.SelectedCount +
			selection.BudgetOmittedCount,
		Selected:      selection.SelectedCount,
		SelectedChars: selection.SelectedChars,
		Truncated:     selection.TruncatedCount,
		BudgetOmitted: selection.BudgetOmittedCount,
	}
	items := make([]MemoryContextDebugItem, 0, len(selection.Results))
	detailChars := 0
	for _, result := range selection.Results {
		switch MemorySource(result.Source) {
		case MemorySourceMemory:
			counts.SelectedMemory++
		case MemorySourceKnowledge:
			counts.SelectedKnowledge++
		default:
			counts.SelectedOther++
		}
		preview := strings.TrimSpace(result.Snippet)
		if len(preview) > memoryContextDebugPreviewChars {
			preview = strings.TrimSpace(truncateAtRuneBoundary(
				preview,
				memoryContextDebugPreviewChars,
			)) + "..."
		}
		itemCost := len(result.Path) + len(result.Source) + len(preview) + 64
		if detailChars+itemCost > memoryContextDebugDetailBudget {
			counts.DetailItemsOmitted++
			continue
		}
		detailChars += itemCost
		items = append(items, MemoryContextDebugItem{
			Path:      result.Path,
			Source:    result.Source,
			StartLine: result.StartLine,
			EndLine:   result.EndLine,
			Score:     result.Score,
			Preview:   preview,
		})
	}
	countsReconcile := counts.Discovered-counts.CorrectedExcluded ==
		counts.Selected+counts.Empty+counts.BelowRelevance+
			counts.BudgetOmitted
	return MemoryContextDebugSnapshot{
		Version:             memoryContextDebugVersion,
		Query:               query,
		MinScore:            memoryInjectMinScore,
		CharacterBudget:     memoryInjectContextBudgetChars,
		DetailPayloadBudget: memoryContextDebugDetailBudget,
		Counts:              counts,
		Selected:            items,
		CountsReconcile:     countsReconcile,
		ReadOnly:            true,
		GeneratedAt:         time.Now().UnixMilli(),
		DurationMs:          time.Since(startedAt).Milliseconds(),
	}, nil
}
