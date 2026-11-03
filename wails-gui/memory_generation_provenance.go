package main

import (
	"context"
	"errors"
	"log"
	"reflect"
	"sort"
	"strings"
)

// Keep comparison dependencies below the MCP reader's 1,000-session work
// budget and 256 KiB file limit, leaving room for the current source. Writers
// retain every dependency even when their current source exceeds this budget;
// such entries are excluded as whole inputs on the next generation.
const (
	memoryGenerationDependencyLimit = 256
	memoryGenerationProvenanceBytes = 32 << 10
)

func loadMemoryGenerationContext(ctx context.Context, app *App) (string, *memoryReadProvenance) {
	return loadMemoryGenerationContextForQuery(ctx, app, "", 100_000)
}

func loadMemoryGenerationContextWithBudget(ctx context.Context, app *App, maxBytes int) (string, *memoryReadProvenance) {
	return loadMemoryGenerationContextForQuery(ctx, app, "", maxBytes)
}

// Rank bounded, privacy-checked snapshots by the current source before prompt
// admission. Every admitted title/point keeps the exact snapshot's provenance;
// neither a text limit nor a lineage limit can truncate those dependencies.
func loadMemoryGenerationContextForQuery(ctx context.Context, app *App, query string, maxBytes int) (string, *memoryReadProvenance) {
	if maxBytes <= 0 {
		return "", nil
	}
	maxBytes = min(maxBytes, 100_000)
	privacy, err := app.mcpReadPrivacy(ctx)
	if err != nil {
		logMemoryGenerationPrivacyFailure(err)
		return "", nil
	}
	entries, err := privacy.memoryEntries()
	if err != nil {
		log.Printf("[memory] dedup context unavailable: memory snapshots could not be read")
		return "", nil
	}
	type candidate struct {
		entry MemoryEntry
		text  string
		score int
	}
	candidates := make([]candidate, 0, len(entries))
	tokens := dedupQueryTokens(query)
	for _, entry := range entries {
		fullContent := "# " + entry.Title + "\n" + stripMemoryFrontmatter(entry.Content)
		// Rank every point before compact rendering drops later bullets.
		score := dedupTokenOverlapScore(tokens, fullContent)
		entryText := formatDedupEntry(entry.ID, fullContent)
		candidates = append(candidates, candidate{entry, entryText, score})
	}
	// Rank internal snapshots before spending the privacy work budget so
	// unrelated newer sources cannot starve a relevant older source. Nothing
	// is rendered until that snapshot passes every gate below. Stable ties
	// retain memoryEntries' newest-first fallback.
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].score > candidates[j].score })
	var text strings.Builder
	var provenance *memoryReadProvenance
	for _, candidate := range candidates {
		if ctx.Err() != nil {
			log.Printf("[memory] dedup context unavailable: request canceled")
			return "", nil
		}
		if text.Len()+len(candidate.text) > maxBytes {
			continue
		}
		entry := candidate.entry
		if !privacy.memoryAllowed(entry) {
			continue
		}
		source := privacy.memoryProvenance[entry.ID]
		if !memoryReadPromptSafe(source) {
			continue
		}
		source.Sessions = append(append([]string(nil), source.Sessions...), entry.ActivitySessionIDs...)
		source.Apps = append(append([]string(nil), source.Apps...), entry.Apps...)
		combined := mergeMemoryReadProvenance(source)
		if provenance != nil {
			combined = mergeMemoryReadProvenance(*provenance, combined)
		}
		if !memoryGenerationProvenanceFits(combined) {
			continue
		}
		text.WriteString(candidate.text)
		provenance = &combined
	}
	current, err := app.mcpReadPrivacy(ctx)
	if err != nil {
		logMemoryGenerationPrivacyFailure(err)
		return "", nil
	}
	if !reflect.DeepEqual(current.hidden, privacy.hidden) || !reflect.DeepEqual(current.ignored, privacy.ignored) || !reflect.DeepEqual(current.pauses, privacy.pauses) {
		log.Printf("[memory] dedup context unavailable: privacy settings changed during read")
		return "", nil
	}
	return text.String(), provenance
}

func memoryGenerationProvenanceFits(p memoryReadProvenance) bool {
	return len(p.Sessions)+len(p.Apps) <= memoryGenerationDependencyLimit && len(memoryReadProvenanceJSON(p)) <= memoryGenerationProvenanceBytes
}

func logMemoryGenerationPrivacyFailure(err error) {
	if errors.Is(err, errMCPReadPaused) {
		log.Printf("[memory] dedup context unavailable: observation is paused")
		return
	}
	log.Printf("[memory] dedup context unavailable: privacy settings could not be read")
}

const memoryGenerationSourceBoundary = `
## Initiative boundary

Predicted actions, rationales, confidence, and proposed execution plans are unverified hypotheses, not durable facts or permission to execute. Extract memories only from the supplied source evidence. Attribute statements to the actual speaker; only explicit user confirmation can establish a prediction as a fact about the user, and it does not grant authority to act.
`

type memoryGenerationPromptContext struct {
	existing    string
	corrections string
	provenance  *memoryReadProvenance
}

func loadMemoryGenerationPromptContext(ctx context.Context, app *App) memoryGenerationPromptContext {
	return loadMemoryGenerationPromptContextForQuery(ctx, app, "")
}

func loadMemoryGenerationPromptContextForQuery(ctx context.Context, app *App, query string) memoryGenerationPromptContext {
	existing, provenance := loadMemoryGenerationContextForQuery(ctx, app, query, 100_000)
	result := memoryGenerationPromptContext{existing: existing, provenance: provenance}
	if dir, err := memoryDir(); err == nil {
		if corrections, err := LoadRecentCorrections(dir, 50); err != nil {
			log.Printf("[memory] load corrections for prompt failed: %s", activityPrivateLogField("error", err.Error()))
		} else {
			result.corrections = FormatCorrectionsPromptBlock(corrections)
		}
	}
	return result
}

// These fields are never decoded from model JSON. Only this code-owned prompt
// assembly records every admitted comparison source under the compatibility policy;
// correction and initiative guidance contain no observed or user-authored prose.
func (p memoryGenerationPromptContext) apply(response *mcMemoryGenerationResponse) {
	response.inputProvenance = p.provenance
	response.inputPromptSafe = true
}

func memoryGenerationCurrentSource(response *mcMemoryGenerationResponse, source memoryReadProvenance) memoryReadProvenance {
	source.PromptSafe = response != nil && response.inputPromptSafe && !source.Withheld
	return source
}

func memoryGenerationProvenance(response *mcMemoryGenerationResponse, sources ...memoryReadProvenance) memoryReadProvenance {
	if response != nil && response.inputProvenance != nil {
		sources = append(sources, *response.inputProvenance)
	}
	return mergeMemoryReadProvenance(sources...)
}

// An update response can already contain points from its selected target.
// Preserve the existing create fallback when that target cannot be revalidated,
// but do not export its merged points under only the current source's lineage.
func memoryGenerationUnverifiedUpdate(response *mcMemoryGenerationResponse) *mcMemoryGenerationResponse {
	if response == nil {
		return nil
	}
	copy := *response
	withheld := memoryGenerationProvenance(response, memoryReadProvenance{Version: 2, Withheld: true})
	copy.inputProvenance = &withheld
	return &copy
}
