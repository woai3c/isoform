package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"

	"wails-gui/internal/meetingdetect"
	"wails-gui/internal/pii"
	"wails-gui/internal/platform/activitymon"
	"wails-gui/storage"
)

// transcriptDir returns ~/.yansu-agent/transcripts/, creating it if needed.
func transcriptDir() (string, error) {
	dataRoot, err := flavorDataDir()
	if err != nil {
		return "", fmt.Errorf("resolve home: %w", err)
	}
	dir := filepath.Join(dataRoot, "transcripts")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create transcript dir: %w", err)
	}
	return dir, nil
}

// meetingSpeechEventsForRecordingEpoch keeps automatic meeting evidence tied to the
// preference generation that admitted it, and to speech captured in the call.
// The event remains available to the ordinary activity pipeline, but an
// off/on cycle permanently invalidates its meeting classification.
func meetingSpeechEventsForRecordingEpoch(events []activitymon.ActivityEvent, epoch uint64) []activitymon.ActivityEvent {
	var filtered []activitymon.ActivityEvent
	for _, evt := range events {
		if evt.Kind == activitymon.EventSpeech && evt.SpeechInCall && evt.MeetingRecordingEpoch == epoch {
			filtered = append(filtered, evt)
		}
	}
	return filtered
}

// detectMeetingInEvents scans activity events for speech events whose app or
// captured meeting context matches a transcript-supported meeting platform.
// Returns the meeting name and whether this is a meeting session.
func detectMeetingInEvents(events []activitymon.ActivityEvent) (meetingName string, isMeeting bool) {
	for _, evt := range events {
		// Only speech captured during a call counts: speech outside calls
		// (dictation into Claude while Zoom is in front, say) is activity.
		if evt.Kind != activitymon.EventSpeech || evt.SpeechText == "" || !evt.SpeechInCall {
			continue
		}
		// A call window's title, never a browser page's (a chat can be
		// titled "Zoom Meeting prep").
		if !meetingdetect.IsBrowserBundle(evt.AppBundleID) {
			if result := meetingdetect.IsTranscriptionSupportedMeetingEvent(evt.AppBundleID, evt.WindowTitle); result.IsMeeting {
				return result.MeetingName, true
			}
		}
		// Delayed ASR events may be tagged with the captured meeting app while
		// keeping the original foreground window title (for example Notes or
		// iTerm while a Meet/Slack call continues in the background). In that
		// case the app name is the reliable meeting-context field.
		if meetingdetect.IsTranscriptionSupportedMeetingName(evt.AppName) {
			return evt.AppName, true
		}
	}
	return "", false
}

const (
	meetingCandidateMinSpeechEvents = 2
	meetingCandidateMinWords        = 8
	meetingCandidateSingleMinWords  = 20
)

type meetingCandidateStats struct {
	app    string
	events int
	words  int
	first  int64
	last   int64
}

func detectMeetingCandidateInEvents(events []activitymon.ActivityEvent) (meetingName string, isMeeting bool) {
	statsByApp := map[string]*meetingCandidateStats{}
	totalCandidateEvents := 0

	for _, evt := range events {
		if evt.Kind != activitymon.EventSpeech || strings.TrimSpace(evt.SpeechText) == "" || !evt.MeetingCandidate {
			continue
		}
		appName := strings.TrimSpace(evt.MeetingCandidateApp)
		if !meetingdetect.IsTranscriptionSupportedMeetingName(appName) {
			continue
		}

		totalCandidateEvents++
		stats := statsByApp[appName]
		if stats == nil {
			stats = &meetingCandidateStats{app: appName, first: evt.Timestamp, last: evt.Timestamp}
			statsByApp[appName] = stats
		}
		stats.events++
		stats.words += len(strings.Fields(evt.SpeechText))
		if evt.Timestamp < stats.first {
			stats.first = evt.Timestamp
		}
		if evt.Timestamp > stats.last {
			stats.last = evt.Timestamp
		}
	}

	var best *meetingCandidateStats
	for _, stats := range statsByApp {
		if best == nil || stats.events > best.events || (stats.events == best.events && stats.words > best.words) {
			best = stats
		}
	}
	if best == nil {
		return "", false
	}
	if best.events*2 < totalCandidateEvents {
		return "", false
	}
	if best.events >= meetingCandidateMinSpeechEvents && best.words >= meetingCandidateMinWords {
		return best.app, true
	}
	if best.words >= meetingCandidateSingleMinWords {
		return best.app, true
	}
	return "", false
}

// buildTranscriptMarkdown formats speech events into a timestamped markdown
// transcript. Returns the markdown string and the word count.
func buildTranscriptMarkdown(meetingApp string, session ActivitySessionRow, events []activitymon.ActivityEvent) (string, int) {
	return buildTranscriptMarkdownWithEvent(meetingApp, session, events, nil)
}

// buildTranscriptMarkdownWithEvent is buildTranscriptMarkdown with the
// calendar event behind the meeting, when one matched: the header then names
// the event and its attendees, after the app line.
func buildTranscriptMarkdownWithEvent(meetingApp string, session ActivitySessionRow, events []activitymon.ActivityEvent, header *transcriptEventHeader) (string, int) {
	return buildMeetingTranscriptMarkdown(meetingApp, session, speechEventUtterances(events), header)
}

// buildMeetingTranscriptMarkdown writes the transcript file: the header,
// then one "[HH:MM:SS] Me: text" line per paragraph (meeting_transcript_audio.go
// merges the call's speech into paragraphs and labels them only when both
// sides spoke).
func buildMeetingTranscriptMarkdown(meetingApp string, session ActivitySessionRow, utterances []meetingUtterance, header *transcriptEventHeader) (string, int) {
	startTime := time.Unix(session.StartTime/1000, 0)
	endTime := time.Unix(session.EndTime/1000, 0)
	durationSec := int(endTime.Sub(startTime).Seconds())
	durationStr := fmt.Sprintf("%dm %ds", durationSec/60, durationSec%60)

	var b strings.Builder
	title := meetingApp
	if header != nil && header.Title != "" {
		title = header.Title
	}
	fmt.Fprintf(&b, "# Meeting Transcript: %s\n\n", title)
	fmt.Fprintf(&b, "- **Date**: %s\n", startTime.Format("2006-01-02 15:04"))
	fmt.Fprintf(&b, "- **Duration**: %s\n", durationStr)
	fmt.Fprintf(&b, "- **App**: %s\n", meetingApp)
	if header != nil {
		if header.Title != "" {
			fmt.Fprintf(&b, "- **Event**: %s (%s)\n", header.Title, header.Time)
		}
		if header.Attendees != "" {
			fmt.Fprintf(&b, "- **Attendees**: %s\n", header.Attendees)
		}
		if header.Place != "" {
			fmt.Fprintf(&b, "- **Place**: %s\n", header.Place)
		}
	}
	fmt.Fprintf(&b, "- **Session**: %s\n\n", session.ID)
	b.WriteString("## Transcript\n\n")

	wordCount := writeTranscriptParagraphs(&b, buildTranscriptParagraphs(utterances))
	return b.String(), wordCount
}

// speakerLabel maps an ActivityEvent.SpeechSource to a human-readable
// speaker name for the transcript. Microphone audio is the local user
// ("Me"); system audio is whatever the Mac was playing — meeting peers
// on a Zoom/Teams/Meet call ("Other"). Empty source → empty label so
// callers keep the unprefixed format for speech whose side is unknown.
func speakerLabel(source string) string {
	switch source {
	case "microphone":
		return "Me"
	case "system_audio":
		return "Other"
	default:
		return ""
	}
}

// transcriptDedupWindowMs is how far back we look for a near-duplicate.
// Mic + system-audio recorders both produce 5-second chunks and fan into
// one stream, so the same utterance can appear in two transcripts ~1-3s
// apart. 6s is enough to catch the pair without falsely collapsing
// genuine repetition a few seconds later.
const transcriptDedupWindowMs = int64(6000)

// transcriptDedupWordOverlap is the fraction of shared words above which
// two nearby transcripts are considered duplicates. Tuned against the
// common pattern where the mic version is fragmented and the system
// version is cleaner — they share most of the content.
const transcriptDedupWordOverlap = 0.7

// dedupSpeechEvents collapses consecutive near-duplicate speech events
// coming from the combined mic + system-audio pipeline. For each pair
// within the dedup window with high word overlap, keeps the "better"
// version — longer text wins, ties broken by punctuation density.
//
// Non-speech events and blank-text events are filtered out entirely,
// so callers can iterate the result without re-checking Kind/Text.
func dedupSpeechEvents(events []activitymon.ActivityEvent) []activitymon.ActivityEvent {
	out := make([]activitymon.ActivityEvent, 0, len(events))
	for _, evt := range events {
		if evt.Kind != activitymon.EventSpeech {
			continue
		}
		text := strings.TrimSpace(evt.SpeechText)
		if text == "" {
			continue
		}
		evt.SpeechText = text

		// Try to collapse with the most recent emitted event if it's
		// within the dedup window and textually similar.
		if n := len(out); n > 0 {
			prev := out[n-1]
			if evt.Timestamp-prev.Timestamp <= transcriptDedupWindowMs &&
				wordOverlapRatio(prev.SpeechText, evt.SpeechText) >= transcriptDedupWordOverlap {
				if preferTranscript(evt.SpeechText, prev.SpeechText) {
					out[n-1] = evt
				}
				continue
			}
		}
		out = append(out, evt)
	}
	return out
}

// wordOverlapRatio returns |A ∩ B| / max(|A|, |B|) on normalized word
// sets. Range [0, 1]. 1.0 means one is a superset of the other; 0.0
// means no shared words.
func wordOverlapRatio(a, b string) float64 {
	wa := normalizedWordSet(a)
	wb := normalizedWordSet(b)
	if len(wa) == 0 || len(wb) == 0 {
		return 0
	}
	small, large := wa, wb
	if len(wa) > len(wb) {
		small, large = wb, wa
	}
	shared := 0
	for w := range small {
		if large[w] {
			shared++
		}
	}
	return float64(shared) / float64(len(large))
}

// normalizedWordSet lowercases, strips leading/trailing non-letter and
// non-digit runes, and splits on whitespace into a set for overlap
// comparison. Uses unicode.IsLetter/IsDigit so words in non-English
// scripts (CJK, Cyrillic, accented Latin like "Über"/"café", etc.)
// survive normalization instead of collapsing to the empty string.
func normalizedWordSet(s string) map[string]bool {
	fields := strings.Fields(strings.ToLower(s))
	out := make(map[string]bool, len(fields))
	for _, f := range fields {
		trimmed := strings.TrimFunc(f, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r)
		})
		if trimmed != "" {
			out[trimmed] = true
		}
	}
	return out
}

// preferTranscript reports whether candidate is a better transcript
// than incumbent for the same utterance. Longer wins first; on near
// ties, higher punctuation density wins (indicates whisper produced a
// cleaner, finalized version vs a mid-stream fragment).
func preferTranscript(candidate, incumbent string) bool {
	lc, li := len(candidate), len(incumbent)
	if lc > li+4 {
		return true
	}
	if li > lc+4 {
		return false
	}
	return punctuationScore(candidate) > punctuationScore(incumbent)
}

func punctuationScore(s string) int {
	score := 0
	for _, r := range s {
		switch r {
		case '.', ',', ';', ':', '?', '!', '\'', '"', '-':
			score++
		}
	}
	return score
}

const (
	// transcriptCleanupMaxWords is the longest transcript sent through the
	// AI cleanup.
	transcriptCleanupMaxWords = 3000
	transcriptCleanupTimeout  = 60 * time.Second
	// transcriptCleanupMinKeptTenths: the cleanup must keep at least 9/10 of
	// the words.
	transcriptCleanupMinKeptTenths = 9
)

// cleanTranscriptWithAI sends the raw transcript through a lightweight AI
// model to remove post-meeting noise, meaningless fragments, and obvious
// transcription errors. Falls back to the original on any failure.
func (a *App) cleanTranscriptWithAI(rawMarkdown, meetingApp string, wordCount int) (string, int) {
	if a.IsClosedEyesModeEnabled() || activitySourceAppIsSensitive(meetingApp, meetingApp) {
		return rawMarkdown, wordCount
	}
	const delimiter = "## Transcript\n\n"
	idx := strings.Index(rawMarkdown, delimiter)
	if idx < 0 {
		return rawMarkdown, wordCount
	}
	header := rawMarkdown[:idx+len(delimiter)]
	body := rawMarkdown[idx+len(delimiter):]
	if strings.TrimSpace(body) == "" {
		return rawMarkdown, wordCount
	}
	// A long call would not come back inside the timeout; its raw
	// transcript is already in paragraphs.
	if wordCount > transcriptCleanupMaxWords {
		log.Printf("[transcript] AI cleanup skipped: %d words", wordCount)
		return rawMarkdown, wordCount
	}

	prompt := fmt.Sprintf(`You are cleaning up a raw meeting transcript from automatic speech recognition.
The meeting was on %s.

Rules:
- Each line is one paragraph of one speaker. Keep EVERY line and keep each paragraph whole; do not shorten, merge, or split lines
- Fix obvious transcription errors if the correct word is unambiguous from context
- Remove only lines that are pure noise after the meeting clearly ended (mic testing after everyone left)
- Preserve the exact [HH:MM:SS] timestamp format for each line
- Preserve the speaker prefix (e.g. "Me:" or "Other:") that appears immediately after the timestamp on lines that have one — do NOT add, change, or remove the speaker label
- Do NOT add, invent, or hallucinate any content that was not in the original
- Do NOT summarize -- return the full cleaned transcript line by line
- Do NOT add any commentary, headers, or explanation -- output ONLY the cleaned transcript lines

Raw transcript:
%s

Cleaned transcript:`, meetingApp, body)

	prompt, _ = pii.RedactText(prompt)
	cleaned, err := a.runEphemeralHaiku(prompt, transcriptCleanupTimeout)
	if err != nil {
		log.Printf("[transcript] AI cleanup failed, using raw transcript: %s", activityPrivateLogField("error", err.Error()))
		return rawMarkdown, wordCount
	}
	cleaned = strings.TrimSpace(stripCodeFences(cleaned))
	if cleaned == "" {
		log.Printf("[transcript] AI cleanup returned empty, using raw transcript")
		return rawMarkdown, wordCount
	}

	// Recalculate word count from cleaned body. Strip the timestamp and
	// the optional speaker prefix so the count reflects only the spoken
	// words — otherwise every line would count "Me:" / "Other:" as a
	// word, inflating the meeting word total.
	newWordCount := 0
	for _, line := range strings.Split(cleaned, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		newWordCount += len(strings.Fields(stripTranscriptLinePrefix(line)))
	}
	if newWordCount == 0 {
		log.Printf("[transcript] AI cleanup produced zero words, using raw transcript")
		return rawMarkdown, wordCount
	}
	// The cleanup may fix words, not drop speech: a result that lost more
	// than a tenth of the words cut content, so the raw transcript stays.
	if newWordCount*10 < wordCount*transcriptCleanupMinKeptTenths {
		log.Printf("[transcript] AI cleanup dropped too much (%d -> %d words), using raw transcript", wordCount, newWordCount)
		return rawMarkdown, wordCount
	}

	log.Printf("[transcript] AI cleanup: %d -> %d words", wordCount, newWordCount)
	return header + cleaned + "\n", newWordCount
}

// saveAutomaticMeetingTranscript runs when a meeting ends; meetingID is the
// activity monitor's id for the call (empty for older paths), captured before
// the monitor clears it.
func (a *App) saveAutomaticMeetingTranscript(session ActivitySessionRow, events []activitymon.ActivityEvent, meetingApp string, epoch uint64, meetingID string) {
	a.saveMeetingTranscriptWithAutomaticEpoch(session, events, meetingApp, &epoch, meetingID)
}

func (a *App) saveMeetingTranscriptWithAutomaticEpoch(session ActivitySessionRow, events []activitymon.ActivityEvent, meetingApp string, automaticEpoch *uint64, meetingID string) {
	if _, busy := a.meetingTranscriptActive.LoadOrStore(session.ID, true); busy {
		return
	}
	defer a.meetingTranscriptActive.Delete(session.ID)
	if !a.meetingTranscriptAllowed(session, automaticEpoch) {
		return
	}
	dir, err := transcriptDir()
	if err != nil {
		log.Printf("[transcript] failed to resolve dir: %v", err)
		return
	}

	// The calendar event behind this meeting, when one overlaps the session
	// and its link matches the app: the header names it and its attendees.
	transcriptSession := session
	if session.AudioPath != "" {
		if start, ok := meetingAudioStartFromPath(session.AudioPath); ok && start.UnixMilli() < transcriptSession.StartTime {
			transcriptSession.StartTime = start.UnixMilli()
		}
	}
	calendarEvent := a.calendarEventForSession(transcriptSession, meetingApp)
	// The whole call from its recording when there is one, the live speech
	// otherwise.
	utterances, complete := a.meetingTranscriptUtterances(session, events)
	if !complete {
		return
	}
	markdown, wordCount := buildMeetingTranscriptMarkdown(meetingApp, transcriptSession, utterances, transcriptHeaderForEvent(calendarEvent, time.Local))
	markdown = withTranscriptMeetingID(markdown, meetingID)
	// Only established sources are declared; the allowance check below
	// refuses this transcript when the gate cannot establish them.
	if sources := a.meetingRecordingSourceSessions(session); len(sources) > 0 {
		markdown = withTranscriptActivitySources(markdown, sources)
	}
	if session.AudioPath != "" {
		markdown = strings.Replace(markdown, "- **Session**: ", "- **Audio**: activity/audio/"+filepath.ToSlash(session.AudioPath)+"\n- **Session**: ", 1)
	}
	// Transcribing a whole call takes minutes: check again before the
	// transcript goes to a model.
	if !a.meetingTranscriptAllowed(session, automaticEpoch) {
		return
	}
	// Keep recognition verbatim. AI cleanup could remove up to ten percent
	// of a call, reorder lines or repeat them; readability is a later step.
	if wordCount == 0 && session.AudioPath == "" {
		markTranscriptAttempted(session.ID)
		log.Printf("[transcript] skipping empty transcript for session %s", session.ID)
		return
	}

	// Automatic transcript cleanup may take tens of seconds. Fence only the
	// durable writes, each time revalidating the exact capture generation so
	// an opt-out (including off/on) cannot create a late file, meeting row
	// update or note; the follow-up pass (a model call of up to 45 s) runs
	// outside the fence so speech admission and call starts never wait on
	// it. Manual Dev Tools simulation passes no epoch and remains
	// intentionally available regardless of this automatic-recording setting.
	fenced := func(write func()) bool {
		if automaticEpoch != nil {
			a.meetingRecordingMu.Lock()
			defer a.meetingRecordingMu.Unlock()
			if !a.automaticMeetingRecordingEpochIsCurrent(*automaticEpoch) {
				log.Printf("[transcript] dropping stale automatic transcript for session %s", session.ID)
				return false
			}
		}
		write()
		return true
	}

	// Save .md file.
	startTime := time.Unix(session.StartTime/1000, 0)
	fileName := fmt.Sprintf("%s-%s-%s.md",
		startTime.Format("2006-01-02"),
		strings.ToLower(strings.ReplaceAll(meetingApp, " ", "-")),
		session.ID[:8],
	)
	filePath := filepath.Join(dir, fileName)
	saved := false
	if !fenced(func() {
		if err := os.WriteFile(filePath, []byte(markdown), 0o600); err != nil {
			log.Printf("[transcript] failed to write %s: %v", filePath, err)
			return
		}
		saved = true
		log.Printf("[transcript] saved %s (%d words)", filePath, wordCount)

		// Update session row with transcript path.
		if a.activity != nil && a.activity.store != nil {
			if err := a.activity.store.UpdateMeetingStatus(session.ID, meetingApp, filePath); err != nil {
				log.Printf("[transcript] failed to update meeting status for %s: %v", session.ID, err)
			}
		}
		a.attachCalendarEventToSession(session, calendarEvent)
	}) || !saved {
		return
	}

	// Decisions and follow-ups. The user's follow-ups become commitments.
	// Multi-source call inference needs a durable input envelope for the Purpose
	// ledger. The complete archive is saved now; do not attach a full-call
	// inference solely to its final activity slice.
	var outline *meetingFollowupExtraction
	if wordCount > 0 && len(a.meetingRecordingSourceSessions(session)) == 1 && a.meetingTranscriptAllowed(session, automaticEpoch) {
		if extraction, err := a.extractMeetingFollowups(context.Background(), session, markdown, calendarEvent, fenced); err != nil {
			log.Printf("[meeting-followup] skipped for session %s: %s", session.ID, activityPrivateLogField("error", err.Error()))
		} else {
			outline = &extraction
		}
	}

	// The notes join the meeting on Activity and Isoform learns from them,
	// silently: recording a call and writing it up never notifies (no toast,
	// push, text or main-chat line).
	title := ""
	if calendarEvent != nil {
		title = calendarEvent.Title
	}
	callKey := ""
	fenced(func() {
		if key, err := a.recordMeetingNotes(transcriptSession, meetingApp, wordCount, meetingID, title, outline); err != nil {
			log.Printf("[meeting-notes] failed to save the notes of %s: %v", session.ID, err)
		} else if a.activity != nil && a.activity.store != nil {
			callKey = key
		}
	})
	if callKey != "" {
		if err := a.saveMeetingCallTurns(callKey, session, utterances, calendarEvent, automaticEpoch); err != nil {
			log.Printf("[meeting-notes] call turns not saved for %s", session.ID)
		}
	}
}

// meetingTranscriptAllowed reports whether a call may still be written up
// and sent to a model: automatic recording is still on in the generation
// that captured it, observation is not paused, and the session never touched
// a protected app.
func (a *App) meetingTranscriptAllowed(session ActivitySessionRow, automaticEpoch *uint64) bool {
	if automaticEpoch != nil && !a.automaticMeetingRecordingEpochIsCurrent(*automaticEpoch) {
		log.Printf("[transcript] recording was turned off; dropping the transcript of session %s", session.ID)
		return false
	}
	if a.IsClosedEyesModeEnabled() {
		log.Printf("[transcript] observation is paused; dropping the transcript of session %s", session.ID)
		return false
	}
	if session.TriageStatus == "protected" {
		return false
	}
	if session.AudioPath != "" && a.activity != nil && a.activity.store != nil && meetingRecordingTriageStatus(readMeetingWAVMeta(a.activity.store.AudioDir(), session.AudioPath)) == "protected" {
		return false
	}
	// Real captures carry persisted event provenance, which must still be
	// eligible under the current settings before either transcript model pass.
	if a.activity != nil && a.activity.store != nil {
		if !a.activitySessionAllowsModelRead(a.activity.store, session) {
			return false
		}
		if session.AudioPath != "" || session.TranscriptPath != "" {
			policy, err := a.activityReadPrivacyPolicy()
			if err != nil {
				return false
			}
			if _, err := a.transcriptSourcesWithPolicy(a.activity.store, session, "", policy); err != nil {
				return false
			}
		}
		return true
	}
	// Dev Tools' synthetic transcript path has no activity store. Its supplied
	// app metadata still observes the same sensitive-app policy.
	var apps []string
	if session.AppNames != "" && json.Unmarshal([]byte(session.AppNames), &apps) != nil {
		return false
	}
	for _, app := range append(apps, session.MeetingApp) {
		if activitySourceAppIsSensitive(app, app) {
			return false
		}
	}
	return true
}

// withTranscriptMeetingID names the meeting in the transcript header, so a
// recovery after a crash publishes the notes under the same key as the call
// itself (a rejoined call shares one key).
func withTranscriptMeetingID(markdown, meetingID string) string {
	meetingID = strings.TrimSpace(meetingID)
	const marker = "- **Session**: "
	if meetingID == "" || !strings.Contains(markdown, marker) {
		return markdown
	}
	return strings.Replace(markdown, marker, "- **Meeting**: "+meetingID+"\n"+marker, 1)
}

// parseTranscriptMeetingID reads the meeting id withTranscriptMeetingID wrote.
func parseTranscriptMeetingID(markdown string) string {
	header := markdown
	if idx := strings.Index(markdown, "## Transcript"); idx >= 0 {
		header = markdown[:idx]
	}
	for _, line := range strings.Split(header, "\n") {
		if rest, ok := strings.CutPrefix(line, "- **Meeting**: "); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

// stripTranscriptHeader removes the YAML-ish metadata block and
// "## Transcript" heading that buildTranscriptMarkdown prepends, so
// the frontend can render the body cleanly under its own overview
// card without a duplicate header.
func stripTranscriptHeader(s string) string {
	const marker = "## Transcript\n\n"
	if i := strings.Index(s, marker); i >= 0 {
		return strings.TrimSpace(s[i+len(marker):]) + "\n"
	}
	return s
}

// stripTranscriptLinePrefix removes the leading "[HH:MM:SS] " timestamp
// and the optional "Me: " / "Other: " speaker prefix from a transcript
// line, returning just the spoken text. Used by word-count and meeting-
// summary code so prefixes don't inflate counts or pollute prose.
func stripTranscriptLinePrefix(line string) string {
	line = strings.TrimSpace(line)
	if i := strings.Index(line, "] "); i >= 0 && strings.HasPrefix(line, "[") {
		line = strings.TrimSpace(line[i+2:])
	}
	for _, label := range []string{"Me:", "Other:"} {
		if strings.HasPrefix(line, label) {
			return strings.TrimSpace(line[len(label):])
		}
	}
	return line
}

// formatTranscriptMessage builds the markdown body used as the first
// assistant message of a transcript chat session. The header mirrors the
// overview card that used to sit above the dedicated TranscriptView so
// readers still see the meeting app, date, duration, and word count at
// a glance while the body flows naturally into follow-up chat turns.
// The AI cleanup step often returns lines separated by a single '\n',
// which markdown renders as a single paragraph — join them with blank
// lines so each timestamped line stands on its own row.
func formatTranscriptMessage(meetingApp, markdown string, wordCount int, startTime time.Time, durationStr string) string {
	body := stripTranscriptHeader(markdown)
	var lines []string
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lines = append(lines, line)
	}
	header := fmt.Sprintf(
		"**%s call** · %s · %s · %d words\n\n",
		meetingApp,
		startTime.Format("Monday, January 2 at 3:04 PM"),
		durationStr,
		wordCount,
	)
	return header + strings.Join(lines, "\n\n") + "\n"
}

// persistTranscriptFirstMessage ensures the transcript body sits as the
// first assistant stream item on a chat session. Idempotent: a new item
// is inserted when the session is empty, and an existing solo first
// message is updated in place if it was persisted with an older format
// (e.g. timestamped lines collapsed into one paragraph). Sessions with
// user follow-ups are left alone so mid-stream rewrites never rewrite
// user-authored history.
func (a *App) persistTranscriptFirstMessage(ctx context.Context, chatSessionID, meetingApp, markdown string, wordCount int, startTime time.Time, durationStr string) {
	if a.store == nil {
		return
	}
	session, err := a.store.LoadSession(ctx, chatSessionID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return
		}
		log.Printf("[transcript] load chat session failed for %s: %v", chatSessionID, err)
		return
	}
	if session == nil {
		return
	}
	count, err := a.store.CountStreamItems(ctx, chatSessionID, 0)
	if err != nil {
		log.Printf("[transcript] count stream items failed for %s: %v", chatSessionID, err)
		return
	}
	content := formatTranscriptMessage(meetingApp, markdown, wordCount, startTime, durationStr)
	if count == 0 {
		if _, err := a.store.AppendStreamItem(ctx, chatSessionID, storage.StreamItemRow{
			ID:      uuid.New().String(),
			Kind:    "assistant",
			Content: content,
		}); err != nil {
			log.Printf("[transcript] persist first message failed for %s: %v", chatSessionID, err)
		} else {
			a.mainChatRowsChanged(chatSessionID)
		}
		return
	}
	if count != 1 {
		return
	}
	items, err := a.store.LoadStreamItems(ctx, chatSessionID)
	if err != nil || len(items) != 1 || items[0].Kind != "assistant" {
		return
	}
	if items[0].Content == content {
		return
	}
	if err := a.store.UpdateStreamItemContent(ctx, items[0].ID, content); err != nil {
		log.Printf("[transcript] repair first message failed for %s: %v", chatSessionID, err)
	}
}

// EnsureTranscriptChatMessage lazy-backfills the transcript body as the
// first assistant message for a single chat session when it's missing.
// Returns true when the session's first assistant message was inserted
// or rewritten so the frontend knows to refresh its in-memory items.
func (a *App) EnsureTranscriptChatMessage(chatSessionID string) (bool, error) {
	if a.store == nil || a.activity == nil || a.activity.store == nil {
		return false, nil
	}
	ctx := context.Background()
	session, err := a.activity.store.GetSessionByID(chatSessionID)
	if err != nil || session == nil || session.TranscriptPath == "" {
		return false, nil
	}
	rawText, _, err := a.readMeetingTranscript(*session)
	raw := []byte(rawText)
	if err != nil {
		return false, nil
	}
	startTime := time.Unix(session.StartTime/1000, 0)
	endTime := time.Unix(session.EndTime/1000, 0)
	durationSec := int(endTime.Sub(startTime).Seconds())
	if durationSec < 0 {
		durationSec = 0
	}
	durationStr := fmt.Sprintf("%dm %ds", durationSec/60, durationSec%60)
	wordCount := 0
	for _, line := range strings.Split(stripTranscriptHeader(string(raw)), "\n") {
		wordCount += len(strings.Fields(stripTranscriptLinePrefix(line)))
	}
	// Snapshot the solo item (if any) before the repair so we can report
	// whether content actually changed — the frontend only reloads when
	// the backend tells it something changed.
	var beforeID, beforeContent string
	if items, err := a.store.LoadStreamItems(ctx, chatSessionID); err == nil && len(items) == 1 {
		beforeID = items[0].ID
		beforeContent = items[0].Content
	}
	a.persistTranscriptFirstMessage(ctx, chatSessionID, session.MeetingApp, string(raw), wordCount, startTime, durationStr)
	afterItems, err := a.store.LoadStreamItems(ctx, chatSessionID)
	if err != nil || len(afterItems) == 0 {
		return false, err
	}
	if beforeID == "" {
		return true, nil
	}
	return afterItems[0].Content != beforeContent, nil
}

// recoverOrphanTranscripts scans the transcripts directory for .md files
// whose meeting notes were never saved (the app quit between the transcript
// file and the notes). For each orphan it saves the notes with the meeting
// on Activity and patches the activity row's transcript_path. It skips
// transcripts whose part is in the notes already, whose meeting the user
// deleted (the publish marker), or that an older build turned into a
// `Transcript:` chat session.
func (a *App) recoverOrphanTranscripts(ctx context.Context) {
	if a.store == nil {
		return
	}
	dir, err := transcriptDir()
	if err != nil {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	// Orphans are created only during abnormal shutdown, so limit the
	// scan to files from the last 48 hours. The filename starts with
	// YYYY-MM-DD, letting us filter without reading file contents.
	cutoff := time.Now().AddDate(0, 0, -2).Format("2006-01-02")

	recovered := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		// Only consider files matching the YYYY-MM-DD-... naming convention.
		if len(e.Name()) < 10 || e.Name()[4] != '-' || e.Name()[7] != '-' {
			continue
		}
		if e.Name() < cutoff {
			continue
		}
		filePath := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}
		content := string(raw)

		sessionID, meetingApp, dateStr, durationStr := parseTranscriptMetadata(content)
		if sessionID == "" || len(sessionID) < 8 {
			continue
		}
		// Notes exist, were published once and then deleted, or a legacy
		// chat session holds the transcript: nothing to recover. Notes are
		// keyed by the meeting id the header names; older files fall back to
		// the activity session.
		meetingID := parseTranscriptMeetingID(content)
		notesKey := sessionID
		if meetingID != "" {
			notesKey = meetingID
		}
		// A later slice of a call whose notes exist is recovered into them
		// unless it is there already; notes the user deleted stay deleted.
		if a.activity == nil || a.activity.store == nil {
			continue
		}
		if _, known, err := a.activity.store.meetingNotesKeyForSession(sessionID); err != nil || known {
			continue
		}
		if _, exists, err := a.activity.store.meetingNotesByKey(notesKey); err != nil || (!exists && meetingNotesPublished(notesKey)) {
			continue
		}
		if sess, err := a.store.LoadSession(ctx, sessionID); err == nil && sess != nil {
			continue
		}

		if meetingApp == "" || dateStr == "" {
			continue
		}

		startTime, err := time.ParseInLocation("2006-01-02 15:04", dateStr, time.Local)
		if err != nil {
			continue
		}

		wordCount := 0
		for _, line := range strings.Split(stripTranscriptHeader(content), "\n") {
			wordCount += len(strings.Fields(stripTranscriptLinePrefix(line)))
		}
		if wordCount == 0 && !strings.Contains(content, "- **Audio**: ") {
			continue
		}

		// Patch activity row's transcript_path if it was lost during shutdown.
		if a.activity != nil && a.activity.store != nil {
			if row, gErr := a.activity.store.GetSessionByID(sessionID); gErr == nil && row != nil && row.TranscriptPath == "" {
				_ = a.activity.store.UpdateMeetingStatus(sessionID, meetingApp, filePath)
			}
		}

		// Write the notes with the meeting on Activity. The duration is
		// reconstructed from the header when the file has one.
		endTime := startTime
		if d, dErr := parseTranscriptDuration(durationStr); dErr == nil {
			endTime = startTime.Add(d)
		}
		row := ActivitySessionRow{
			ID:             sessionID,
			StartTime:      startTime.UnixMilli(),
			EndTime:        endTime.UnixMilli(),
			IsMeeting:      true,
			MeetingApp:     meetingApp,
			TranscriptPath: filePath,
		}
		if a.activity != nil && a.activity.store != nil {
			if stored, err := a.activity.store.GetSessionByID(sessionID); err == nil && stored != nil {
				row.AudioPath, row.TriageStatus, row.AppNames = stored.AudioPath, stored.TriageStatus, stored.AppNames
				// The header keeps minutes; the row keeps the exact span.
				row.StartTime, row.EndTime = stored.StartTime, stored.EndTime
			}
		}
		if !a.meetingTranscriptAllowed(row, nil) {
			continue
		}
		if _, err := a.recordMeetingNotes(row, meetingApp, wordCount, meetingID, "", nil); err != nil {
			log.Printf("[transcript] orphan recovery: notes for %s were not written: %v", sessionID, err)
			continue
		}
		recovered++
		log.Printf("[transcript] orphan recovery: published meeting notes for %s", sessionID)
	}
	if recovered > 0 {
		log.Printf("[transcript] orphan recovery: %d meeting note(s) restored", recovered)
	}
}

// parseTranscriptDuration reads the "12m 34s" form buildTranscriptMarkdown
// writes into the header.
func parseTranscriptDuration(s string) (time.Duration, error) {
	var minutes, seconds int
	if _, err := fmt.Sscanf(strings.TrimSpace(s), "%dm %ds", &minutes, &seconds); err != nil {
		return 0, err
	}
	return time.Duration(minutes)*time.Minute + time.Duration(seconds)*time.Second, nil
}

// parseTranscriptMetadata extracts session, app, date, and duration from the
// markdown header (before "## Transcript") in a single pass.
func parseTranscriptMetadata(markdown string) (session, app, date, duration string) {
	header := markdown
	if idx := strings.Index(markdown, "## Transcript"); idx >= 0 {
		header = markdown[:idx]
	}
	for _, line := range strings.Split(header, "\n") {
		switch {
		case strings.HasPrefix(line, "- **Session**: "):
			session = strings.TrimSpace(line[len("- **Session**: "):])
		case strings.HasPrefix(line, "- **App**: "):
			app = strings.TrimSpace(line[len("- **App**: "):])
		case strings.HasPrefix(line, "- **Date**: "):
			date = strings.TrimSpace(line[len("- **Date**: "):])
		case strings.HasPrefix(line, "- **Duration**: "):
			duration = strings.TrimSpace(line[len("- **Duration**: "):])
		}
	}
	return
}

// backfillTranscriptMessages walks all activity sessions with a saved
// transcript and ensures their chat session carries the transcript body
// as the first assistant message. Runs once at app startup to migrate
// sessions created before transcripts were chat-persisted.
func (a *App) backfillTranscriptMessages(ctx context.Context) {
	if a.activity == nil || a.activity.store == nil || a.store == nil {
		return
	}
	sessions, err := a.activity.store.GetSessionsByAge()
	if err != nil {
		log.Printf("[transcript] backfill: load sessions failed: %v", err)
		return
	}
	for _, s := range sessions {
		if !s.IsMeeting || s.TranscriptPath == "" {
			continue
		}
		rawText, _, err := a.readMeetingTranscript(s)
		raw := []byte(rawText)
		if err != nil {
			continue
		}
		startTime := time.Unix(s.StartTime/1000, 0)
		endTime := time.Unix(s.EndTime/1000, 0)
		durationSec := int(endTime.Sub(startTime).Seconds())
		if durationSec < 0 {
			durationSec = 0
		}
		durationStr := fmt.Sprintf("%dm %ds", durationSec/60, durationSec%60)
		wordCount := 0
		for _, line := range strings.Split(stripTranscriptHeader(string(raw)), "\n") {
			wordCount += len(strings.Fields(stripTranscriptLinePrefix(line)))
		}
		a.persistTranscriptFirstMessage(ctx, s.ID, s.MeetingApp, string(raw), wordCount, startTime, durationStr)
	}
}

// GetMeetingTranscript returns the full transcript markdown for a session.
func (a *App) GetMeetingTranscript(sessionID string) (string, error) {
	if a.activity == nil || a.activity.store == nil {
		return "", fmt.Errorf("activity store not initialized")
	}
	session, err := a.activity.store.GetSessionByID(sessionID)
	if err != nil {
		return "", err
	}
	if session == nil || session.TranscriptPath == "" {
		return "", fmt.Errorf("no transcript for session %s", sessionID)
	}
	text, _, err := a.readMeetingTranscript(*session)
	if err != nil {
		return "", fmt.Errorf("read transcript: %w", err)
	}
	return text, nil
}
