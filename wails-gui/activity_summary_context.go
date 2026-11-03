package main

import (
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"wails-gui/internal/platform/activitymon"
)

// ──────────────────────────────────────────────────────────────────────────────
// Per-source context loaders for the daily summary.
//
// These pull rich per-day data (transcripts, memories, knowledge,
// messenger conversations, OCR text, click highlights, speech) and format
// each into a string section ready to inject into the summary prompt.
//
// All loaders are best-effort: failures return an empty string and log; they
// never block summary generation.
// ──────────────────────────────────────────────────────────────────────────────

const (
	// Per-section content caps. Daily summary runs once per day so we can
	// afford a fairly large prompt; modern context windows are huge.
	maxTranscriptHeadBytes = 3000
	maxTranscriptTailBytes = 1000
	maxMessengerMessages   = 20
	maxOCRSamples          = 100
	maxClickHighlights     = 50
	maxSpeechSnippets      = 50
	maxOCRSnippetBytes     = 200
)

// loadDailyMeetings returns a markdown section describing each meeting whose
// session falls within the date range, including head + tail of its transcript.
func (a *App) loadDailyMeetings(sessions []ActivitySessionRow) string {
	if a.activity == nil || a.activity.store == nil {
		return ""
	}
	var meetings []ActivitySessionRow
	for _, s := range a.filterActivityModelSessions(a.activity.store, sessions) {
		if s.IsMeeting && s.TranscriptPath != "" {
			meetings = append(meetings, s)
		}
	}
	if len(meetings) == 0 {
		return ""
	}

	var b strings.Builder
	for _, m := range meetings {
		text, _, err := a.readMeetingTranscript(m)
		if err != nil {
			continue
		}
		if b.Len() == 0 {
			b.WriteString("\n## Meetings\n")
		}
		b.WriteString(formatDailyMeetingContext(m, text))
	}
	return redactActivityModelText(b.String())
}

func formatDailyMeetingContext(m ActivitySessionRow, text string) string {
	var b strings.Builder
	startTime := time.UnixMilli(m.StartTime)
	durationSec := (m.EndTime - m.StartTime) / 1000
	fmt.Fprintf(&b, "\n### %s — %s (%dm %ds)\n", m.MeetingApp, startTime.Format("15:04"), durationSec/60, durationSec%60)
	text = redactActivityModelText(text)
	if len(text) <= maxTranscriptHeadBytes+maxTranscriptTailBytes {
		b.WriteString(text)
	} else {
		b.WriteString(text[:maxTranscriptHeadBytes])
		b.WriteString("\n... (transcript middle omitted) ...\n")
		b.WriteString(text[len(text)-maxTranscriptTailBytes:])
	}
	b.WriteString("\n")
	return b.String()
}

// loadDailyMemories returns memories whose file mtime falls within the day.
func (a *App) loadDailyMemories(startMs, endMs int64) string {
	if a.IsClosedEyesModeEnabled() {
		return ""
	}
	entries, err := a.GetMemoryEntries()
	if err != nil {
		log.Printf("[summary] load memories failed: %v", err)
		return ""
	}
	var matched []MemoryEntry
	for _, e := range entries {
		if e.ModTime >= startMs && e.ModTime < endMs && a.memorySourceAllowsRead(e.Content) {
			matched = append(matched, e)
		}
	}
	if len(matched) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("\n## Memories captured today\n")
	for _, m := range matched {
		fmt.Fprintf(&b, "\n### %s\n%s\n", m.Title, m.Content)
	}
	return redactActivityModelText(b.String())
}

// loadDailyKnowledge returns knowledge entries whose Date metadata matches.
func (a *App) loadDailyKnowledge(dateStr string) string {
	if a.IsClosedEyesModeEnabled() {
		return ""
	}
	entries, err := a.GetKnowledgeEntries()
	if err != nil {
		log.Printf("[summary] load knowledge failed: %v", err)
		return ""
	}
	var matched []KnowledgeEntry
	for _, e := range entries {
		// Date metadata is "YYYY-MM-DD HH:MM" or "YYYY-MM-DD"; match prefix.
		if strings.HasPrefix(e.Date, dateStr) && a.memorySourceAllowsRead(e.Content) {
			matched = append(matched, e)
		}
	}
	if len(matched) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("\n## Knowledge learned today\n")
	for _, k := range matched {
		fmt.Fprintf(&b, "\n### %s\n%s\n", k.Title, k.Content)
	}
	return redactActivityModelText(b.String())
}

// loadDailyOCRSamples returns OCR snippets from the day's sessions with time
// and app context. Dedupes near-duplicates (same first 80 chars) at query time
// since the OCR worker now stores every frame.
func (a *App) loadDailyOCRSamples(sessions []ActivitySessionRow) string {
	if a.activity == nil || a.activity.store == nil {
		return ""
	}
	type ocrSample struct {
		timestamp int64
		text      string
	}
	seen := make(map[string]bool)
	var samples []ocrSample
	for _, s := range a.filterActivityModelSessions(a.activity.store, sessions) {
		if len(samples) >= maxOCRSamples {
			break
		}
		frames, err := a.activity.store.GetOCRFramesBySession(s.ID)
		if err != nil {
			continue
		}
		for _, f := range frames {
			if len(samples) >= maxOCRSamples {
				break
			}
			text := strings.TrimSpace(redactActivityModelText(f.Text))
			if len(text) < 20 || len(text) > 500 {
				continue
			}
			// Dedup by first 80 chars across the whole day.
			key := text
			if len(key) > 80 {
				key = key[:80]
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			if len(text) > maxOCRSnippetBytes {
				text = text[:maxOCRSnippetBytes] + "..."
			}
			ts := s.StartTime + int64(f.FrameTime*1000)
			samples = append(samples, ocrSample{ts, text})
		}
	}
	if len(samples) == 0 {
		return ""
	}
	// Sort chronologically so the AI can follow the day's narrative.
	sort.Slice(samples, func(i, j int) bool { return samples[i].timestamp < samples[j].timestamp })

	var b strings.Builder
	b.WriteString("\n## On-screen content (chronological)\n")
	for _, s := range samples {
		t := time.Unix(s.timestamp/1000, 0).Format("15:04")
		fmt.Fprintf(&b, "- [%s] %s\n", t, s.text)
	}
	return redactActivityModelText(b.String())
}

// loadDailyClickHighlights returns deduped labeled click actions from the day.
func (a *App) loadDailyClickHighlights(sessions []ActivitySessionRow) string {
	if a.activity == nil || a.activity.store == nil {
		return ""
	}
	type clickKey struct {
		app, role, label string
	}
	seen := make(map[clickKey]int)
	for _, s := range a.filterActivityModelSessions(a.activity.store, sessions) {
		events, err := a.activity.store.GetSessionEvents(s.ID)
		if err != nil {
			continue
		}
		for _, evt := range events {
			if evt.Kind != activitymon.EventClick || activitySourceAppIsSensitive(evt.AppBundleID, evt.AppName) {
				continue
			}
			label := strings.TrimSpace(evt.ClickLabel)
			role := strings.TrimSpace(evt.ClickRole)
			if label == "" && role == "" {
				continue
			}
			seen[clickKey{evt.AppName, role, label}]++
		}
	}
	if len(seen) == 0 {
		return ""
	}
	type ranked struct {
		key   clickKey
		count int
	}
	all := make([]ranked, 0, len(seen))
	for k, c := range seen {
		all = append(all, ranked{k, c})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].count > all[j].count })
	if len(all) > maxClickHighlights {
		all = all[:maxClickHighlights]
	}

	var b strings.Builder
	b.WriteString("\n## Notable actions taken (clicks)\n")
	for _, r := range all {
		label := r.key.label
		if label == "" {
			label = r.key.role
		}
		if r.count > 1 {
			fmt.Fprintf(&b, "- %s in %s (×%d)\n", label, r.key.app, r.count)
		} else {
			fmt.Fprintf(&b, "- %s in %s\n", label, r.key.app)
		}
	}
	return redactActivityModelText(b.String())
}

// loadDailySpeech returns speech snippets from non-meeting sessions.
// Meeting speech is already covered via transcripts.
func (a *App) loadDailySpeech(sessions []ActivitySessionRow) string {
	if a.activity == nil || a.activity.store == nil {
		return ""
	}
	var snippets []string
	for _, s := range a.filterActivityModelSessions(a.activity.store, sessions) {
		if s.IsMeeting {
			continue
		}
		if len(snippets) >= maxSpeechSnippets {
			break
		}
		events, err := a.activity.store.GetSessionEvents(s.ID)
		if err != nil {
			continue
		}
		for _, evt := range events {
			if evt.Kind != activitymon.EventSpeech || activitySourceAppIsSensitive(evt.AppBundleID, evt.AppName) {
				continue
			}
			text := strings.TrimSpace(redactActivityModelText(evt.SpeechText))
			if text == "" {
				continue
			}
			snippets = append(snippets, text)
			if len(snippets) >= maxSpeechSnippets {
				break
			}
		}
	}
	if len(snippets) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("\n## Speech captured (outside meetings)\n")
	for _, s := range snippets {
		fmt.Fprintf(&b, "- %q\n", s)
	}
	return redactActivityModelText(b.String())
}
