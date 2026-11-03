package main

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"time"

	"yansu-agent/agent"
)

// extractedEvent is one semantic event as emitted by the extraction LLM. Its
// SourceIndices reference observations in the ObservationSequence fed to the
// prompt (0-based, many-to-many). See docs/activity-event-model-p1-design.md §5.
type extractedEvent struct {
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Category    string   `json:"category"`
	Entities    []Entity `json:"entities"`
	// Keywords are user-facing display tags (3-5), distinct from Entities.
	// Entities are the high-recall stitching signal (regex + LLM, never capped);
	// Keywords are a curated, precision-first label set shown on the event card.
	// May be empty (older rows, no-LLM fallback) — derived deterministically from
	// Entities/Title in that case. See deriveDisplayKeywords.
	Keywords               []string `json:"keywords"`
	SourceIndices          []int    `json:"source_indices"`
	ContinuationOfPrevious bool     `json:"continuation_of_previous"`
	TaskWorthy             bool     `json:"task_worthy"`
}

type eventExtractionResponse struct {
	Events []extractedEvent `json:"events"`
	// DroppedIndices / DropReasons are populated only by the coherence VERIFIER
	// (not plain extraction): the verifier must account for every input observation
	// by placing it in an event OR declaring it dropped here, so an UNdeclared index
	// (a silent omission, not an intentional noise drop) can be detected. See
	// verifyCoverageComplete.
	DroppedIndices []int             `json:"dropped_indices,omitempty"`
	DropReasons    map[string]string `json:"drop_reasons,omitempty"`
}

// eventExtractionInstruction mirrors AirJelly's activity-record-expert contract
// and carries forward the conservative Source-attribution prior from
// buildGroupSummaryBody (activity_triage.go): temporal adjacency alone never
// justifies a merge.
const eventExtractionInstruction = `You are an activity-record expert. The input below is a chronological sequence of STRUCTURED observations already parsed from desktop activity (NOT raw pixels). Each observation has an index, a real source app, optional extracted entities, and clean body text.

Group the observations into semantic EVENTS. One event = one coherent piece of work that may span multiple apps and may jump in time.

## What counts as an EVENT (read this FIRST)
An event is a MEANINGFUL thing the user worked on or accomplished — a unit of work or a real conversation — NOT a mechanical action. The following are NEVER events; never emit them:
- Opening, launching, or switching to an app ("Open WeChat", "Switch to Chrome", "Focus terminal")
- Glancing at a notification/badge, or looking at the activity/timeline/summary view itself
- A momentary focus with no substantive content
Aggressively MERGE consecutive observations of the same underlying task into ONE event (e.g. dozens of terminal frames while setting up a tool = one "Set up X" event; scrolling a long chat = one "Discuss Y with Z" event). A single isolated observation almost never deserves its own event — prefer DROPPING it over emitting a trivial action. Aim for FEWER, larger, meaningful events — a thoughtful work log, not a keystroke log.

## Source attribution (CRITICAL)
Each observation marks an INDEPENDENT source. Material from different apps may be UNRELATED even when adjacent in time. Only group observations into the same event when their content EXPLICITLY references the same entity (same repo / project / PR / channel / person). Temporal adjacency ALONE is NEVER a reason to merge. When unsure, keep observations in separate events — under-merging is acceptable, over-merging is not.

## Fields per event
- title: [verb] + [project/module] + [specific content], roughly 20-40 chars. Name real projects, repos, PRs, files, or people when visible.
- description: 1-3 concise sentences describing what the user actually did.
- category: exactly one of:
  - build: creating output (coding, design, writing, configuration, deployment)
  - connect: interpersonal communication (chat, email, meeting, code review)
  - explore: information acquisition (browsing, searching, reading docs, learning)
  - sustain: maintenance / operations / monitoring / routine tasks
- entities: [{type,name}] the real entities this event concerns. Types: repo, project, pr, channel, person — AND topic for a coherent subject or interest that ties activity together across time and apps (e.g. {"type":"topic","name":"cold brew coffee"}, {"type":"topic","name":"tennis gear"}). ALWAYS emit a topic entity when the event is about a recognizable subject or interest, so the same interest pursued later (other apps, hours apart) links to it. Entities are an internal matching signal — be generous; do NOT worry about how many there are.
- keywords: 3-5 concise core identifiers shown to the user as tags. Keep ONLY keywords that uniquely locate this event. Priority: proper nouns (project name, file name) > core tech names (framework, library). Exclude: generic words ("coding", "browsing", "type safety"); near-synonym duplicates (keep only one of "schema validation" / "data validation"). A key project/file name may repeat one already in the title (these tags double as click-to-filter handles, so that overlap is useful) — but don't waste a slot on a filler word the title already conveys. Unlike entities, keywords are a curated, precision-first DISPLAY label set — favor what a person recognizes at a glance over coverage, and never exceed 5.
- source_indices: 0-based observation indices this event is built from. The same observation may appear in multiple events. Every event must reference at least one observation.
- continuation_of_previous: true only if this event continues the immediately previous event's same work (a stitching hint).
- task_worthy: true for goal-oriented work (coding, debugging, writing, configuring, deploying, reviewing PRs, technical discussion) OR substantive research / learning on a coherent topic — even via videos, forums, or search (e.g. researching cold brew coffee, comparing tennis gear, studying a framework). false for: opening or switching apps, glancing at notifications, momentary actions, AIMLESS social-media scrolling or idle entertainment with NO coherent thread, or checking your own activity logs. The test is INTENT: a real thread of interest/work = true; mindless flipping = false.

` + activitySecondPersonRule

const eventExtractionJSONInstruction = `Respond with ONLY a JSON object (no markdown, no code fences):
{ "events": [ { "title": "...", "description": "...", "category": "build|connect|explore|sustain", "entities": [{"type":"repo|project|pr|channel|person|topic","name":"..."}], "keywords": ["...", "..."], "source_indices": [0, 1], "continuation_of_previous": false, "task_worthy": true } ] }

CRITICAL: Do NOT ask any questions. Respond immediately with the JSON object.`

const (
	eventBodyMaxRunes        = 600
	eventMeetingBodyMaxRunes = 2000
	// semanticExtractTimeout bounds a single extraction LLM call so a stuck call
	// cannot hold evaluate()'s runMu and starve subsequent triage ticks (which
	// would busy-skip). Mirrors triagerTriageTimeout (3 min).
	semanticExtractTimeout = 3 * time.Minute
)

// buildEventExtractionPrompt assembles the extraction prompt, reusing the
// triage prompt style (Builder + `---`-wrapped payload + JSON-only tail).
// identityContext (may be "") is the "who is who" block from
// buildEventIdentityContext, inserted between the instruction and the
// observations so the extractor can tell the local user apart from other people.
func buildEventExtractionPrompt(seq ObservationSequence, languageInstruction, identityContext string) string {
	var b strings.Builder
	b.WriteString(languageInstruction)
	b.WriteString("\n\n")
	b.WriteString(eventExtractionInstruction)
	b.WriteString("\n\n")
	if identityContext != "" {
		b.WriteString(identityContext)
	}
	b.WriteString("## Observations\n---\n")
	b.WriteString(renderObservations(seq))
	b.WriteString("---\n\n")
	b.WriteString(eventExtractionJSONInstruction)
	return b.String()
}

// coherenceVerifyInstruction is the split-biased contract for the coherence
// verifier. Unlike extraction (which groups a whole timeline), this is handed the
// observations of ONE event that a deterministic gate could NOT prove cohere
// (no shared repo/pr). The job is to decide, by CONTENT, whether they are truly
// one piece of work or an over-merge of unrelated activity — and to RE-EMIT the
// real events. The bias is toward SPLITTING: keep together only what shares the
// same specific subject/work.
const coherenceVerifyInstruction = `You are an activity-record expert auditing ONE candidate event for internal coherence. The observations below were tentatively grouped into a single event, but they may include UNRELATED activity that was merged only because it happened around the same time. Each observation has an index, a real source app, optional entities, and clean body text.

Your job: decide whether these observations are truly ONE piece of work, and re-emit the REAL event(s).

## Rules
- An event = ONE coherent piece of work / one real conversation / one coherent subject of interest, which may span multiple apps.
- SPLIT observations into separate events when their CONTENT is about different work/subjects (e.g. coding one project vs. watching an unrelated video vs. a chat on a different topic). Being adjacent in time is NEVER a reason to keep them together.
- KEEP observations in one event only when their content is clearly the same specific subject/work (same project, repo, PR, person-and-topic, or the same coherent research subject) — even across different apps.
- DROP pure noise: a momentary glance, a notification, an app switch, an empty/contentless frame, or looking at the activity/timeline view itself. Emit no event for those.
- When unsure whether two observations are the same work, SPLIT them (under-merging is acceptable here; over-merging is the bug we are fixing).

## Fields per event
- title: [verb] + [project/module] + [specific content], roughly 20-40 chars, reflecting ONLY that event's own content. Name real projects, repos, PRs, files, or people when visible.
- description: 1-3 concise sentences on what the user actually did.
- category: exactly one of: build, connect, explore, sustain.
- entities: [{type,name}] the real entities this event concerns (repo, project, pr, channel, person, topic).
- source_indices: 0-based observation indices this event is built from. Every observation that belongs to real work must appear in exactly one event; noise observations appear in none.
- continuation_of_previous: always false here.
- task_worthy: true for goal-oriented work or substantive research; false for idle/mechanical activity.

## Account for EVERY observation (REQUIRED)
Every observation index MUST be accounted for EXACTLY ONCE — either inside one event's source_indices OR listed in "dropped_indices" (with a short reason in "drop_reasons"). Put an index in dropped_indices when it is pure noise (a glance, notification, app switch, empty frame). Do NOT leave any index out of both: an omitted index is treated as an invalid response.

` + activitySecondPersonRule

const coherenceVerifyJSONInstruction = `Respond with ONLY a JSON object (no markdown, no code fences):
{ "events": [ { "title": "...", "description": "...", "category": "build|connect|explore|sustain", "entities": [{"type":"repo|project|pr|channel|person|topic","name":"..."}], "source_indices": [0, 1], "continuation_of_previous": false, "task_worthy": true } ], "dropped_indices": [2], "drop_reasons": {"2": "notification glance"} }

EVERY input observation index must appear EXACTLY ONCE across all source_indices and dropped_indices combined. CRITICAL: Do NOT ask any questions. Respond immediately with the JSON object.`

// buildCoherenceVerifyPrompt assembles the verifier prompt (split-biased
// instruction + the candidate event's observations + JSON-only tail that requires
// full index coverage via dropped_indices).
func buildCoherenceVerifyPrompt(seq ObservationSequence, languageInstruction, identityContext string) string {
	var b strings.Builder
	b.WriteString(languageInstruction)
	b.WriteString("\n\n")
	b.WriteString(coherenceVerifyInstruction)
	b.WriteString("\n\n")
	if identityContext != "" {
		b.WriteString(identityContext)
	}
	b.WriteString("## Observations\n---\n")
	b.WriteString(renderObservations(seq))
	b.WriteString("---\n\n")
	b.WriteString(coherenceVerifyJSONInstruction)
	return b.String()
}

// renderObservations serializes the sequence into the indexed, app-tagged form
// the prompt consumes ("[i] [HH:MM:SS] [app=X] {entities} / title / body").
func renderObservations(seq ObservationSequence) string {
	var b strings.Builder
	for _, o := range seq.Observations {
		fmt.Fprintf(&b, "[%d] [%s] [app=%s]", o.Index, formatClock(o.StartMs), o.RealApp)
		if o.Source == "meeting_transcript" {
			b.WriteString(" [meeting transcript]")
		}
		if len(o.Entities) > 0 {
			fmt.Fprintf(&b, " {entities: %s}", renderEntities(o.Entities))
		}
		b.WriteByte('\n')
		if o.Title != "" {
			fmt.Fprintf(&b, "  title: %s\n", compactText(o.Title, 160))
		}
		bodyMax := eventBodyMaxRunes
		if o.Source == "meeting_transcript" {
			bodyMax = eventMeetingBodyMaxRunes
		}
		// Conversation apps stream oldest-first → keep the tail (latest messages =
		// what the user is looking at), not the head (oldest scrolled-in history).
		body := compactText(o.Body, bodyMax)
		if isConversationApp(o.RealApp) {
			// Per-day event split is a hard rule, so drop pre-today messages first,
			// then keep the tail (latest of what remains).
			body = compactTextTail(dropPreTodayConversation(o.Body, o.StartMs), bodyMax)
		}
		if body != "" {
			fmt.Fprintf(&b, "  body: %s\n", body)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func renderEntities(ents []Entity) string {
	parts := make([]string, 0, len(ents))
	for _, e := range ents {
		parts = append(parts, e.Type+":"+e.Name)
	}
	return strings.Join(parts, ", ")
}

func formatClock(ms int64) string {
	if ms <= 0 {
		return "--:--:--"
	}
	return time.UnixMilli(ms).Format("15:04:05")
}

// compactText collapses whitespace and truncates on a rune boundary (keeps HEAD).
func compactText(s string, maxRunes int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > maxRunes {
		return string(r[:maxRunes]) + "…"
	}
	return s
}

// compactTextTail is compactText but keeps the TAIL. Conversation apps stream
// messages oldest-first, so the latest (current) messages sit at the end;
// truncating from the head would feed the extractor the oldest scrolled-in
// history (e.g. a weeks-old BYOK thread) and misattribute it as current activity.
func compactTextTail(s string, maxRunes int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > maxRunes {
		return "…" + string(r[len(r)-maxRunes:])
	}
	return s
}

// conversationApps stream messages oldest-first (Discord/Slack/Telegram/WeChat),
// so their observation bodies are truncated keeping the tail (latest) not head.
var conversationApps = map[string]bool{
	"Discord":      true,
	"Slack":        true,
	"Telegram":     true,
	"WeChat":       true,
	"tencent_chat": true,
	"Lark":         true,
	"Feishu":       true,
}

func isConversationApp(realApp string) bool {
	return conversationApps[realApp]
}

var (
	// convoStampRe matches a message timestamp "[...]:" (group 1 = the stamp).
	convoStampRe = regexp.MustCompile(`\[([^\]]+)\]:`)
	// Absolute-date forms each chat app prints on non-today messages. Observed in
	// the wild (activity.db sampling):
	//   Discord: "2026/5/21" (Y/M/D) and "5月21日" (M月D日)
	//   Feishu:  "5月29日" / "04月25日" (M月D日, no year)
	//   Slack:   "6/5/26" (M/D/YY) and "Mar 30th" / "May 11th" (Mon Dth, no year)
	// Tried in priority order in parseStampDate so Y/M/D wins before M/D/YY can
	// mis-grab a 2-digit tail out of a 4-digit year.
	convoDateYMDRe     = regexp.MustCompile(`(\d{4})\s*[/年]\s*(\d{1,2})\s*[/月]\s*(\d{1,2})`)
	convoDateMDYRe     = regexp.MustCompile(`\b(\d{1,2})/(\d{1,2})/(\d{2,4})\b`)
	convoDateCJKMDRe   = regexp.MustCompile(`(\d{1,2})\s*月\s*(\d{1,2})\s*日`)
	convoDateEnMonthRe = regexp.MustCompile(`(?i)\b(jan|feb|mar|apr|may|jun|jul|aug|sep|oct|nov|dec)[a-z]*\.?\s+(\d{1,2})(?:st|nd|rd|th)?\b`)
)

var enMonthIndex = map[string]int{
	"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6,
	"jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12,
}

// stampDate is a parsed calendar date from a conversation timestamp. month/day are
// always set when ok; year is set only for formats that carry it (hasYear).
type stampDate struct {
	year, month, day int
	hasYear          bool
}

// parseStampDate extracts an absolute calendar date from a chat timestamp string,
// covering every format observed across Discord/Slack/Feishu (see convoDate*Re).
// Returns ok=false for bare-time stamps ("11:16") that carry no date.
func parseStampDate(stamp string) (stampDate, bool) {
	if m := convoDateYMDRe.FindStringSubmatch(stamp); m != nil {
		y, _ := strconv.Atoi(m[1])
		mo, _ := strconv.Atoi(m[2])
		d, _ := strconv.Atoi(m[3])
		return stampDate{year: y, month: mo, day: d, hasYear: true}, true
	}
	if m := convoDateMDYRe.FindStringSubmatch(stamp); m != nil {
		mo, _ := strconv.Atoi(m[1])
		d, _ := strconv.Atoi(m[2])
		y, _ := strconv.Atoi(m[3])
		if y < 100 {
			y += 2000
		}
		return stampDate{year: y, month: mo, day: d, hasYear: true}, true
	}
	if m := convoDateCJKMDRe.FindStringSubmatch(stamp); m != nil {
		mo, _ := strconv.Atoi(m[1])
		d, _ := strconv.Atoi(m[2])
		return stampDate{month: mo, day: d}, true
	}
	if m := convoDateEnMonthRe.FindStringSubmatch(stamp); m != nil {
		if mo := enMonthIndex[strings.ToLower(m[1])]; mo > 0 {
			d, _ := strconv.Atoi(m[2])
			return stampDate{month: mo, day: d}, true
		}
	}
	return stampDate{}, false
}

// isTodayStamp reports whether a conversation message timestamp belongs to ref's
// day. Older messages carry an absolute date ("2026/5/21", "5月29日", "6/5/26",
// "Mar 30th") or a relative day ("昨天", "Yesterday"); today's messages use a bare
// time ("11:16") or an explicit "今天"/"Today", and so default to true.
func isTodayStamp(stamp string, ref time.Time) bool {
	lower := strings.ToLower(stamp)
	if strings.Contains(stamp, "昨天") || strings.Contains(stamp, "前天") ||
		strings.Contains(lower, "yesterday") {
		return false
	}
	if strings.Contains(stamp, "今天") || strings.Contains(lower, "today") {
		return true
	}
	d, ok := parseStampDate(stamp)
	if !ok {
		return true // bare time → today
	}
	if d.month != int(ref.Month()) || d.day != ref.Day() {
		return false
	}
	return !d.hasYear || d.year == ref.Year()
}

// dropPreTodayConversation trims a conversation body to today's messages,
// aligning with the per-day event split (events never span days). It finds the
// FIRST message stamped as today and drops everything before it (so a pre-today
// message's own body is dropped too, not just its date marker). Falls back to the
// full body when no today message is found (e.g. browsing an all-historical
// thread), so tail-truncation still surfaces its latest part.
func dropPreTodayConversation(body string, snapshotMs int64) string {
	if snapshotMs <= 0 {
		return body
	}
	ref := time.UnixMilli(snapshotMs)
	for _, s := range convoStampRe.FindAllStringSubmatchIndex(body, -1) {
		if !isTodayStamp(body[s[2]:s[3]], ref) {
			continue
		}
		// First today message: cut back to its sender (the token before "[").
		cut := s[0]
		head := strings.TrimRight(body[:cut], " ")
		if sp := strings.LastIndexAny(head, " \n"); sp >= 0 {
			cut = sp + 1
		} else {
			cut = 0
		}
		if cut <= 0 {
			return body // first message is already today
		}
		return strings.TrimSpace(body[cut:])
	}
	return body // no today message → fall back
}

// extractSemanticEvents runs the extraction LLM over a preprocessed sequence,
// reusing the runner.ExecuteTask + lightTaskModel + best-effort-JSON path that
// triage uses (activity_triage.go:3078).
func (c *ActivityTriager) extractSemanticEvents(ctx context.Context, seq ObservationSequence) ([]extractedEvent, error) {
	if len(seq.Observations) == 0 {
		return nil, nil
	}
	languageInstruction := responseLanguageInstruction(c.app.currentUILanguageName())
	identityContext := buildEventIdentityContext(ctx, c.identityStore())
	prompt := buildEventExtractionPrompt(seq, languageInstruction, identityContext)
	resp, err := c.runEventExtractionPrompt(ctx, prompt)
	if err != nil {
		return nil, err
	}
	cleaned := sanitizeExtractedEvents(resp.Events, len(seq.Observations))
	log.Printf("[semantic-events] extracted %d events from %d observations", len(cleaned), len(seq.Observations))
	return cleaned, nil
}

// errVerifyIncomplete signals that the verifier failed to account for every input
// observation (some index is neither in an event nor in dropped_indices) — its
// output cannot be trusted to partition, so applyCoherence falls back to a
// deterministic split rather than silently losing the undeclared observations.
var errVerifyIncomplete = fmt.Errorf("coherence verifier left observations unaccounted for")

// coherenceVerify repartitions a single over-merged event's observations by
// CONTENT: it returns the events those observations should actually form, so
// applyCoherence can replace one incoherent event with its true pieces. The
// verifier must account for EVERY input observation (event or dropped_indices);
// an incomplete response returns errVerifyIncomplete. Indices are sub-sequence
// local (applyCoherence remaps them). See coherenceClusters for when it fires.
func (c *ActivityTriager) coherenceVerify(ctx context.Context, sub ObservationSequence) ([]extractedEvent, error) {
	if len(sub.Observations) == 0 {
		return nil, nil
	}
	languageInstruction := responseLanguageInstruction(c.app.currentUILanguageName())
	identityContext := buildEventIdentityContext(ctx, c.identityStore())
	prompt := buildCoherenceVerifyPrompt(sub, languageInstruction, identityContext)
	resp, err := c.runEventExtractionPrompt(ctx, prompt)
	if err != nil {
		return nil, err
	}
	// Completeness is checked on RAW events (a verifier-declared index counts even
	// if sanitize later drops that event as meta-noise) + dropped_indices.
	if !verifyCoverageComplete(resp.Events, resp.DroppedIndices, len(sub.Observations)) {
		return nil, errVerifyIncomplete
	}
	cleaned := sanitizeExtractedEvents(resp.Events, len(sub.Observations))
	log.Printf("[semantic-events] coherence verify repartitioned %d observations into %d events (%d dropped)", len(sub.Observations), len(cleaned), len(resp.DroppedIndices))
	return cleaned, nil
}

// verifyCoverageComplete reports whether every input index [0,obsCount) is
// accounted for — present in some event's source_indices OR in dropped_indices.
// Out-of-range indices are ignored (sanitize clamps them anyway).
func verifyCoverageComplete(events []extractedEvent, dropped []int, obsCount int) bool {
	accounted := make([]bool, obsCount)
	mark := func(idx int) {
		if idx >= 0 && idx < obsCount {
			accounted[idx] = true
		}
	}
	for _, ev := range events {
		for _, idx := range ev.SourceIndices {
			mark(idx)
		}
	}
	for _, idx := range dropped {
		mark(idx)
	}
	for _, ok := range accounted {
		if !ok {
			return false
		}
	}
	return true
}

// runEventExtractionPrompt runs an extraction/verify prompt over the light model
// (reusing triage's ExecuteTask + best-effort-JSON path) and returns the raw
// parsed response (callers sanitize / inspect dropped_indices as needed).
func (c *ActivityTriager) runEventExtractionPrompt(ctx context.Context, prompt string) (eventExtractionResponse, error) {
	// Independent timeout: a stuck call must not hold evaluate()'s runMu and block
	// later triage ticks. Mirrors the main triage call's bounded ctx.
	ctx, cancel := context.WithTimeout(ctx, semanticExtractTimeout)
	defer cancel()

	c.app.mu.Lock()
	runner := c.app.runner
	c.app.mu.Unlock()
	if runner == nil {
		return eventExtractionResponse{}, fmt.Errorf("runner not initialized")
	}

	var result strings.Builder
	_, err := runner.ExecuteTask(ctx, &agent.TaskInput{
		Priority:          agent.PriorityBackground,
		AutoApproveTools:  true,
		Prompt:            prompt,
		Model:             c.app.lightTaskModel(),
		PlanMode:          true,
		Ephemeral:         true,
		NoTools:           true,
		NoSystem:          true,
		SuppressTelemetry: true,
	}, func(event *agent.StreamEvent) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if event.Type == "assistant" {
			result.WriteString(event.Content)
		}
		return nil
	})
	if err != nil {
		return eventExtractionResponse{}, fmt.Errorf("execute event extraction: %w", err)
	}

	raw := stripCodeFences(strings.TrimSpace(result.String()))
	var resp eventExtractionResponse
	if err := unmarshalJSONBestEffort(raw, &resp); err != nil {
		return eventExtractionResponse{}, fmt.Errorf("parse event extraction response: %w", err)
	}
	return resp, nil
}

// sanitizeExtractedEvents drops malformed events and clamps source_indices to
// the valid observation range, so a hallucinated index can never panic the
// stitcher's source-ref assembly.
func sanitizeExtractedEvents(events []extractedEvent, obsCount int) []extractedEvent {
	out := events[:0]
	for _, ev := range events {
		if strings.TrimSpace(ev.Title) == "" {
			continue
		}
		if isMetaNoiseEvent(ev) {
			continue // drop mechanical actions / looking at our own activity surface
		}
		valid := ev.SourceIndices[:0]
		for _, idx := range ev.SourceIndices {
			if idx >= 0 && idx < obsCount {
				valid = append(valid, idx)
			}
		}
		ev.SourceIndices = valid
		if len(ev.SourceIndices) == 0 {
			continue
		}
		ev.Category = normalizeCategory(ev.Category)
		out = append(out, ev)
	}
	return out
}

// isMetaNoiseEvent drops events the prompt is supposed to suppress but the LLM
// sometimes still emits: pure mechanical actions (opening/switching/glancing) and
// looking at our OWN activity surface (timeline/feed/notifications/summary).
func isMetaNoiseEvent(ev extractedEvent) bool {
	t := strings.ToLower(strings.TrimSpace(ev.Title))
	for _, p := range []string{"open ", "switch to ", "glance at ", "launch ", "focus "} {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	blob := t + " " + strings.ToLower(ev.Description)
	for _, m := range []string{"activity timeline", "activity feed", "activity notification", "activity summary"} {
		if strings.Contains(blob, m) {
			return true
		}
	}
	// Self-admitted empty: the LLM sometimes emits an event whose OWN description
	// says nothing of substance happened (idle glances at a chat/tab list). Those
	// are noise, not events — drop them.
	for _, m := range []string{"no substantive", "no coherent", "no real conversation", "no focused discussion", "just app titles", "just app chrome"} {
		if strings.Contains(blob, m) {
			return true
		}
	}
	// Empty browser new-tab pages carry no activity.
	if strings.HasPrefix(t, "browse ") && strings.Contains(t, "new tab") {
		return true
	}
	return false
}

func normalizeCategory(c string) string {
	switch strings.ToLower(strings.TrimSpace(c)) {
	case "build", "connect", "explore", "sustain":
		return strings.ToLower(strings.TrimSpace(c))
	default:
		return "sustain"
	}
}
