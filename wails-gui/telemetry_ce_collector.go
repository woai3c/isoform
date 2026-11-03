package main

import (
	"regexp"
	"wails-gui/internal/pii"
	"yansu-agent/agent/native"
)

// buildCEContextCollector returns a ContextEngineCollector closure
// suitable for agent.TaskInput.ContextEngineCollector. It captures
// the App instance so the collector can read userMemory + knowledge
// counters from the live store on every step, and inspects the
// current telemetry tier each call so a setting flip mid-run takes
// effect on the next LLM step (no need to restart the loop).
//
// Returning nil from buildCEContextCollector means "don't wire CE
// telemetry at all" — used by callers that always-suppress (cron
// jobs, ephemeral probes, etc.).
//
// This is the wails-gui side of the Phase 1.5 context engine
// snapshot plumbing — see yansu-agent/agent/native/context_engine.go
// for the snapshot type and tmp/yansu-monitoring-impl-plan.md §4.4
// + §11 for the field definitions and privacy posture.
func (a *App) buildCEContextCollector(systemPromptTemplate string) native.ContextEngineCollector {
	// Cache memory + knowledge counts once per chat run (closure
	// lifetime = one TaskInput = one Run() = one user chat turn or
	// agent task). The underlying GetMemoryEntries() / GetKnowledgeEntries()
	// both walk on-disk markdown directories — typical sizes are
	// 10-500 files, so each call is a few hundred microseconds, but
	// the collector fires on every LLM step (could be 5-15 per turn)
	// and runs synchronously on the loop goroutine before the next
	// model call. Caching turns N disk walks into 1.
	//
	// Staleness trade-off: if the user adds a memory mid-chat from a
	// separate UI surface, the counts won't update until the next
	// run. That's acceptable — the counts are coarse signals for the
	// pattern checker, not real-time state. The closure is invoked
	// only from the loop goroutine, so no synchronisation is needed
	// for the cached values.
	var (
		memoryCounts     map[string]int
		knowledgeCount   = -1
		structuralCached bool
	)

	return func(req native.ContextEngineRequest) *native.ContextEngineSnapshot {
		tier := currentTelemetryTier()
		if tier == TelemetryTierOff {
			return nil
		}

		if !structuralCached {
			memoryCounts = a.collectMemoryCounts()
			knowledgeCount = a.collectKnowledgeFileCount()
			structuralCached = true
		}

		snap := &native.ContextEngineSnapshot{
			Tier:               tier,
			KnowledgeFileCount: knowledgeCount,
			UserMemoryCounts:   memoryCounts,
		}

		// Per-step L1 fields — these come from req (zero cost, no
		// disk I/O). The cached counts above (user_memory_counts,
		// knowledge_file_count) only refresh on a new run; the
		// per-call fields below reflect this exact step.
		if req.SystemText != "" {
			snap.SystemPromptChars = len(req.SystemText)
		}
		if len(req.Messages) > 0 {
			byRole := make(map[string]int, 3)
			for _, m := range req.Messages {
				byRole[m.Role]++
			}
			snap.MessagesByRole = byRole
		}
		if len(req.Tools) > 0 {
			names := make([]string, len(req.Tools))
			for i, t := range req.Tools {
				names[i] = t.Name
			}
			snap.Tools = names
		}

		// L2: tool schemas + template name. After the PR #1895
		// follow-up that dropped the Basic tier, "not off" implies
		// detailed and the wails-gui side always populates L2 when
		// a snapshot is returned. The native applySnapshotToProps
		// still tier-gates L2 emission as defence in depth (a
		// historical event written with tier="basic" or a future
		// re-introduction of intermediate tiers stays correct).
		// ToolSchemas only on step 0 to dedupe.
		snap.SystemPromptTemplate = systemPromptTemplate
		if req.StepIndex == 0 && len(req.Tools) > 0 {
			schemas := make(map[string]any, len(req.Tools))
			for _, t := range req.Tools {
				schemas[t.Name] = t.InputSchema
			}
			snap.ToolSchemas = schemas
		}

		return snap
	}
}

// collectMemoryCounts buckets the user's memory entries by
// Dimension (persona / identity / context / preference / etc.) for
// the L1 ce_user_memory_counts field. Returns nil when no store is
// configured or it's empty — the loop drops nil maps so dashboards
// don't see noisy {} cells for fresh-install users.
func (a *App) collectMemoryCounts() map[string]int {
	entries, err := a.GetMemoryEntries()
	if err != nil || len(entries) == 0 {
		return nil
	}
	counts := make(map[string]int, 4)
	for _, e := range entries {
		dim := e.Dimension
		if dim == "" {
			dim = "uncategorized"
		}
		counts[dim]++
	}
	return counts
}

// collectKnowledgeFileCount returns the user's knowledge-base file
// count for the L1 ce_knowledge_file_count field. -1 sentinel means
// "couldn't read" so the loop suppresses the field rather than
// reporting a misleading 0 (which would be indistinguishable from
// "user has no KB files").
func (a *App) collectKnowledgeFileCount() int {
	entries, err := a.GetKnowledgeEntries()
	if err != nil {
		return -1
	}
	return len(entries)
}

// buildCERedactor returns the compatibility redactor wired into
// LoopConfig.Redactor. The loop no longer calls it for upstream error bodies:
// those are never exported to telemetry, even after redaction.
// It runs the shared pii.RedactText first (covers email / phone / CC /
// CVV / OTP / password / addresses / government_id / license_plate /
// api_key / session_id / device_id) and then layers two extra
// telemetry-only patterns from the Phase 1.5 plan's "5 categories"
// list that pii didn't already cover: IPv4 addresses and OS-style
// absolute file paths.
//
// Keeping the IP + path patterns here rather than in
// internal/pii/regex.go avoids changing screenshot AX-text masking
// (the pii consumer): error bodies are tolerant of aggressive path
// matching, but AX text would lose legitimate filename references.
func buildCERedactor() func(string) string {
	return func(s string) string {
		redacted, _ := pii.RedactText(s)
		redacted = ipv4RedactRe.ReplaceAllString(redacted, "[REDACTED:ip]")
		redacted = absPathRedactRe.ReplaceAllString(redacted, "[REDACTED:path]")
		return redacted
	}
}

// ipv4RedactRe matches dotted-quad IPv4 addresses. Each octet is
// 0-255; \b at start avoids matching version strings like "1.2.3.456"
// or the "0" inside "10.0.0.50".
var ipv4RedactRe = regexp.MustCompile(`\b(?:(?:25[0-5]|2[0-4]\d|1\d{2}|[1-9]?\d)\.){3}(?:25[0-5]|2[0-4]\d|1\d{2}|[1-9]?\d)\b`)

// absPathRedactRe matches OS-style absolute paths likely to appear in
// upstream error bodies (proxy errors echoing local file references,
// "no such file or directory" messages, stack traces). Covers:
//   - POSIX:    /Users/... /home/... /var/... /tmp/... /opt/...
//   - Windows:  C:\Users\... D:\proj\... etc. (drive letter + backslash)
//
// The POSIX prefix list is intentionally limited so we don't match
// "/v1/messages" (Anthropic API path) or "/admin/desktop-logs" (our
// own URL routes), which would over-redact useful diagnostics.
var absPathRedactRe = regexp.MustCompile(`(?:[A-Za-z]:\\[^\s"'<>|*?]+|/(?:Users|home|var|tmp|opt|root|etc|mnt|private)/[^\s"'<>|*?]+)`)
