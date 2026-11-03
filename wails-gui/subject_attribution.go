package main

import (
	"fmt"
	"strings"

	"wails-gui/internal/platform/activitymon"
)

// ContentMode classifies the relationship between a captured signal and the
// local user. It is the "who is the subject of this content" axis the
// downstream prompts and write-gates must respect.
//
//   - performed: the user themselves produced this signal (clicked, typed,
//     spoke into the mic in their own voice).
//   - received:  a known counterparty produced this signal AND it was
//     directed at the user (remote meeting audio, incoming chat messages).
//   - observed:  the signal is media or third-party content the user is
//     consuming, not authoring (videos, music, social feeds).
//   - ambient:   the source is unclear or unattributable (background noise,
//     room speech without a known speaker, untyped focus events on idle apps).
//   - unknown:   no classification possible from the available signals.
//
// The single most important invariant: observed/ambient content must NEVER
// become a first-person memory point about the user. Whether that is enforced
// by the prompt, by the post-generation gate, or by both, must not depend on
// the model's judgement alone.
type ContentMode string

const (
	ContentModePerformed ContentMode = "performed"
	ContentModeReceived  ContentMode = "received"
	ContentModeObserved  ContentMode = "observed"
	ContentModeAmbient   ContentMode = "ambient"
	ContentModeUnknown   ContentMode = "unknown"
)

// EventContentMode classifies a single ActivityEvent. inMeeting is the
// session-level meeting flag from sessionGroup.hasMeetingSessions().
//
// Interaction events (keyboard, click, focus, app_switch) are always
// performed — the OS only delivers those when the user physically did
// something. Speech is the only ambiguous kind and reuses the existing
// isLikelyNonUserSpeech heuristic so this stays a single source of truth.
func EventContentMode(evt activitymon.ActivityEvent, inMeeting bool) ContentMode {
	switch evt.Kind {
	case activitymon.EventClick, activitymon.EventKeyboard, activitymon.EventFocus, activitymon.EventAppSwitch:
		return ContentModePerformed
	case activitymon.EventSpeech:
		if evt.SpeechSource == "system_audio" {
			if inMeeting {
				return ContentModeReceived
			}
			return ContentModeObserved
		}
		if isLikelyNonUserSpeech(evt) {
			if inMeeting {
				return ContentModeReceived
			}
			return ContentModeObserved
		}
		return ContentModePerformed
	}
	return ContentModeUnknown
}

// SessionContentProfile is the per-group aggregate used by prompts and the
// memory write-gate to decide whether a session can produce user-attributed
// memory at all.
type SessionContentProfile struct {
	PerformedCount int
	ReceivedCount  int
	ObservedCount  int
	AmbientCount   int
	UnknownCount   int
	Total          int

	// Dominant is the highest-count mode. Ties break toward the more
	// conservative interpretation (observed > received > ambient >
	// performed) so that mixed sessions err away from user-attribution.
	Dominant ContentMode

	// HasMediaPlaybackApp is true when at least one event came from an app
	// in mediaPlaybackBundleIDs. The bundle-level signal is needed in
	// addition to event-level mode because OCR samples (which are not
	// ActivityEvents) inherit attribution from the surrounding session.
	HasMediaPlaybackApp bool

	// PrimaryAppName is the app contributing the most events. Used only
	// in the prompt directive text for human-readable framing.
	PrimaryAppName string

	// ChatSenders is populated by the parsed-content renderer after it
	// resolves each chat message's sender via the identity resolver.
	// Zero-value means no parsed chat content was seen in this group.
	ChatSenders ChatSenderStats
}

// ChatSenderStats aggregates chat messages by resolved sender kind. The
// counts let the memory pipeline reason about whether a session's parsed
// content is dominated by the user's own messages, by other people, or by
// unidentified senders. Counts are per-message, not per-conversation.
type ChatSenderStats struct {
	SelfMsgCount      int
	ContactMsgCount   int
	AnonymousMsgCount int
	UnknownMsgCount   int
}

// Total returns the aggregate of all sender counts.
func (s ChatSenderStats) Total() int {
	return s.SelfMsgCount + s.ContactMsgCount + s.AnonymousMsgCount + s.UnknownMsgCount
}

// NonSelfTotal returns the count of messages that are NOT attributable to
// the local user.
func (s ChatSenderStats) NonSelfTotal() int {
	return s.ContactMsgCount + s.AnonymousMsgCount + s.UnknownMsgCount
}

// ChatHeavilyNonSelf reports whether the session's chat content is so
// dominated by other people that user-attributed memory writes should be
// blocked. Threshold: at least 3 non-self messages AND non-self count is
// strictly greater than 2x self messages.
//
// The 3-message floor (was 4 originally) catches the common "burst of
// 3 messages from the same contact, no reply from user yet" pattern
// that earlier slipped past the gate. Three full messages from one
// counterparty is already enough evidence that the session content is
// not the user's own — promoting any of it to a user_insight or
// deep_user_model would be wrong.
func (s ChatSenderStats) ChatHeavilyNonSelf() bool {
	return s.NonSelfTotal() >= 3 && s.NonSelfTotal() > 2*s.SelfMsgCount
}

// ProfileGroup computes a SessionContentProfile from the events of a session
// group. The group is consulted for the meeting flag and primary app name.
func ProfileGroup(group sessionGroup, events []activitymon.ActivityEvent) SessionContentProfile {
	p := SessionContentProfile{}
	inMeeting := group.hasMeetingSessions()

	appCounts := map[string]int{}
	for _, evt := range events {
		p.Total++
		switch EventContentMode(evt, inMeeting) {
		case ContentModePerformed:
			p.PerformedCount++
		case ContentModeReceived:
			p.ReceivedCount++
		case ContentModeObserved:
			p.ObservedCount++
		case ContentModeAmbient:
			p.AmbientCount++
		default:
			p.UnknownCount++
		}
		if isMediaPlaybackApp(evt.AppBundleID) {
			p.HasMediaPlaybackApp = true
		}
		if evt.AppName != "" {
			appCounts[evt.AppName]++
		}
	}

	var bestName string
	bestCount := -1
	for name, n := range appCounts {
		if n > bestCount {
			bestCount = n
			bestName = name
		}
	}
	p.PrimaryAppName = bestName

	p.Dominant = pickDominant(p)
	return p
}

func pickDominant(p SessionContentProfile) ContentMode {
	if p.Total == 0 {
		return ContentModeUnknown
	}
	candidates := []struct {
		mode  ContentMode
		count int
	}{
		{ContentModeObserved, p.ObservedCount},
		{ContentModeReceived, p.ReceivedCount},
		{ContentModeAmbient, p.AmbientCount},
		{ContentModePerformed, p.PerformedCount},
	}
	best := candidates[0]
	for _, c := range candidates[1:] {
		if c.count > best.count {
			best = c
		}
	}
	if best.count == 0 {
		return ContentModeUnknown
	}
	return best.mode
}

// ProfileSummaryBlock returns a human-readable header block describing the
// profile, intended to be prepended to the activity summary string fed into
// the AI. It is structured so the model can rely on these counts rather than
// re-derive them from event labels.
func (p SessionContentProfile) ProfileSummaryBlock() string {
	if p.Total == 0 && p.ChatSenders.Total() == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Session subject profile\n")
	if p.Total > 0 {
		fmt.Fprintf(&b, "- Dominant content mode: %s\n", p.Dominant)
		fmt.Fprintf(&b, "- Event mix: performed=%d received=%d observed=%d ambient=%d unknown=%d (total=%d)\n",
			p.PerformedCount, p.ReceivedCount, p.ObservedCount, p.AmbientCount, p.UnknownCount, p.Total)
	}
	if p.HasMediaPlaybackApp {
		b.WriteString("- A dedicated media-playback app was active in this session.\n")
	}
	if p.PrimaryAppName != "" {
		fmt.Fprintf(&b, "- Primary app by event count: %s\n", p.PrimaryAppName)
	}
	if cs := p.ChatSenders; cs.Total() > 0 {
		fmt.Fprintf(&b, "- Chat messages by resolved sender: self=%d contact=%d anonymous=%d unknown=%d (total=%d)\n",
			cs.SelfMsgCount, cs.ContactMsgCount, cs.AnonymousMsgCount, cs.UnknownMsgCount, cs.Total())
		if cs.ChatHeavilyNonSelf() {
			b.WriteString("- WARNING: chat content is dominated by messages from other people. Statements/preferences/opinions in those messages must not be attributed to the local user.\n")
		}
	}
	b.WriteString("\n")
	return b.String()
}

// PerspectiveInstruction returns the prompt instruction block to insert into
// the memory generation prompt INSTEAD of firstPersonPerspectiveInstruction.
// It is profile-aware: observed/ambient/received sessions get strong
// "do not attribute to the user" framing instead of the default
// first-person rewrite directive.
func (p SessionContentProfile) PerspectiveInstruction(userName string) string {
	base := p.baseInstruction(userName)
	if p.ChatSenders.ChatHeavilyNonSelf() {
		return base + "\n\n" + chatHeavilyNonSelfAddendum(userName, p.ChatSenders)
	}
	return base
}

func (p SessionContentProfile) baseInstruction(userName string) string {
	switch p.Dominant {
	case ContentModeObserved:
		return observedPerspectiveInstruction(userName, p)
	case ContentModeAmbient:
		return ambientPerspectiveInstruction(userName)
	case ContentModeReceived:
		return receivedPerspectiveInstruction(userName)
	case ContentModePerformed, ContentModeUnknown:
		fallthrough
	default:
		// Fall back to the existing first-person directive for sessions
		// dominated by user-performed activity. The base directive
		// already handles the "self vs others" distinction at the
		// sentence level; the profile gate only adds extra guardrails
		// when the dominant signal is non-self.
		return firstPersonPerspectiveInstruction(userName)
	}
}

func chatHeavilyNonSelfAddendum(userName string, cs ChatSenderStats) string {
	return fmt.Sprintf(`CHAT SENDER NOTICE: The parsed conversation content in this session contains %d messages from other people (Contact/Anonymous/Unknown) versus %d messages from %s. Each rendered chat message is now prefixed with a sender tag:

- [YOU] — written by %s themselves. These are the ONLY chat messages that can produce memory points about %s.
- [Contact: <name>] / [Anonymous: <handle>] / [Unknown: <handle>] — written by other people. Their statements, preferences, opinions, and self-descriptions are theirs, not %s's. NEVER write a memory like "I X" based on what a non-[YOU] sender said.

If you choose to record a memory about something a non-[YOU] sender said, write it explicitly in the form "<contact name> mentioned X" / "Friend X said Y" and tag the dimension as "topic_highlight". NEVER use the dimensions "user_insight", "deep_user_model", or "agentic_memory" for content sourced from non-[YOU] chat messages.`,
		cs.NonSelfTotal(), cs.SelfMsgCount, userName, userName, userName, userName)
}

func observedPerspectiveInstruction(userName string, p SessionContentProfile) string {
	app := p.PrimaryAppName
	if app == "" {
		app = "media or content apps"
	}
	return fmt.Sprintf(`The user's name is **%s**. THIS SESSION WAS DOMINATED BY CONTENT THE USER WAS CONSUMING (primary app: %s; observed events: %d of %d). Any people, opinions, preferences, or statements visible in the captured content are NOT %s and must never be written as memory points about %s.

Hard rules for this session:
- DO NOT write any memory point of the form "I X", "I like X", "I want X", "I am X" derived from speech, OCR, or parsed content captured here. The user was watching/listening, not authoring.
- DO NOT promote a topic, claim, or preference shown in the content into a user habit, opinion, or stable trait.
- The only memory worth writing here, if any, is about the act of consumption itself, framed in past tense: "Watched a video about X", "Read an article on Y", "Listened to a podcast about Z". Choose the "topic_highlight" dimension for such points; do NOT use "user_insight" or "deep_user_model".
- If nothing of substance was actively performed by %s in this session, return action="skip" with an empty memoryPoints array. Skipping is the correct default for passive media consumption.`,
		userName, app, p.ObservedCount, p.Total, userName, userName, userName)
}

func ambientPerspectiveInstruction(userName string) string {
	return fmt.Sprintf(`The user's name is **%s**. THIS SESSION'S SIGNAL IS PREDOMINANTLY AMBIENT — background noise, unattributed speech, or short focus events without sustained user activity. There is no reliable evidence about %s's own preferences, decisions, or actions here.

Hard rules for this session:
- The default response is action="skip" with an empty memoryPoints array.
- NEVER infer a user preference, opinion, or trait from ambient speech or window titles.
- Write a memory only if a sustained user-performed signal (typed text, deliberate clicks, mic speech clearly tied to a non-media app) is unambiguously present. When in doubt, skip.`,
		userName, userName)
}

func receivedPerspectiveInstruction(userName string) string {
	return fmt.Sprintf(`The user's name is **%s**. MUCH OF THIS SESSION CONSISTS OF MESSAGES OR AUDIO PRODUCED BY OTHER PEOPLE (remote meeting participants, incoming chat messages). Those words are NOT %s's words.

Hard rules for this session:
- Treat every statement, preference, opinion, or claim made by other senders or remote speakers as third-party — never as %s's own.
- Only %s's own outgoing replies, decisions, or actions are memory material. If you cannot clearly identify %s as the author of a statement, skip it.
- It is correct and expected for many such sessions to return action="skip" with an empty memoryPoints array.`,
		userName, userName, userName, userName, userName)
}

// MemoryGateAction is the decision returned by ValidateMemoryDimension.
type MemoryGateAction string

const (
	MemoryGateAllow             MemoryGateAction = "allow"
	MemoryGateCoerceToHighlight MemoryGateAction = "coerce_to_topic_highlight"
	MemoryGateSkip              MemoryGateAction = "skip"
)

// ValidateMemoryDimension is the post-generation hard gate. The model may
// still return a user_insight or deep_user_model memory from an observed
// session despite the prompt; this function refuses to let those reach disk.
//
// Decisions:
//   - chat content heavily non-self + user_insight/deep_user_model/agentic_memory ->
//     coerce_to_topic_highlight (most common bug class: "user likes chestnuts"
//     extracted from a friend's chat message)
//   - observed dominant + user_insight/deep_user_model/agentic_memory ->
//     coerce_to_topic_highlight (allow the consumption-fact framing through,
//     but never as a stable trait)
//   - ambient dominant -> skip outright (no reliable signal at all)
//   - received dominant + user_insight/deep_user_model -> skip (cannot
//     attribute another speaker's statement to the user as a stable trait)
//   - everything else -> allow
//
// reason is a short string suitable for logTriage and pipeline run telemetry.
// ConnectorAttributionProfile is the MemoryGenerator (Slack /
// Discord / Feishu / Telegram direct connector) analogue of
// SessionContentProfile.ChatSenders — it counts [SELF] vs [OTHER]
// lines in the message summary that gets fed to the LLM. Drives a
// post-generation hard gate (ValidateConnectorMemoryDimension)
// parallel to the one the activity path already has, so the
// connector path stops being single-layer prompt-only defense.
type ConnectorAttributionProfile struct {
	SelfMsgCount  int
	OtherMsgCount int
}

func (p ConnectorAttributionProfile) Total() int { return p.SelfMsgCount + p.OtherMsgCount }

// HeavilyOther mirrors ChatSenderStats.ChatHeavilyNonSelf: ≥3
// other messages AND non-self > 2× self. Three-message bursts from
// a single counterparty are the common pattern this catches; below
// that we accept the signal is too noisy to gate on.
func (p ConnectorAttributionProfile) HeavilyOther() bool {
	if p.OtherMsgCount < 3 {
		return false
	}
	return p.OtherMsgCount > p.SelfMsgCount*2
}

// NoSelf is the strong case: the conversation has [OTHER] messages
// but zero [SELF] messages. Nothing the user said is in scope.
func (p ConnectorAttributionProfile) NoSelf() bool {
	return p.SelfMsgCount == 0 && p.OtherMsgCount > 0
}

// ParseConnectorAttributionProfile reads the connector summary
// (one [SELF]/[OTHER]-tagged line per message, the same shape
// formatConnectorMessageLine emits) and returns the counts. Lines
// without a [SELF]/[OTHER] prefix are ignored.
func ParseConnectorAttributionProfile(summary string) ConnectorAttributionProfile {
	var p ConnectorAttributionProfile
	for _, line := range strings.Split(summary, "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		switch {
		case strings.HasPrefix(trimmed, "[SELF]"):
			p.SelfMsgCount++
		case strings.HasPrefix(trimmed, "[OTHER]"):
			p.OtherMsgCount++
		}
	}
	return p
}

// ValidateConnectorMemoryDimension is the MemoryGenerator post-
// generation hard gate. The prompt already tells the model
// "[OTHER] lines do NOT justify personal memory about the user",
// but the prompt alone is single-layer defense — this enforces.
//
// Decisions:
//   - dimension is NOT one of user_insight / deep_user_model /
//     agentic_memory → allow unconditionally (topic_highlight etc.
//     are safe regardless of attribution because they don't claim
//     anything stable about the user)
//   - NoSelf (purely [OTHER]) + a personal-trait dimension → skip
//     outright; there's literally no self-attributable signal
//   - HeavilyOther + a personal-trait dimension → coerce to
//     topic_highlight; keep the content as observed context, drop
//     the trait framing
//   - everything else → allow
func ValidateConnectorMemoryDimension(profile ConnectorAttributionProfile, dimension string) (MemoryGateAction, string) {
	dim := strings.ToLower(strings.TrimSpace(dimension))
	switch dim {
	case "user_insight", "deep_user_model", "agentic_memory":
		// fall through to gating
	default:
		return MemoryGateAllow, ""
	}
	if profile.NoSelf() {
		return MemoryGateSkip, fmt.Sprintf(
			"connector conversation has zero [SELF] lines (other=%d): refused dimension=%q",
			profile.OtherMsgCount, dim)
	}
	if profile.HeavilyOther() {
		return MemoryGateCoerceToHighlight, fmt.Sprintf(
			"connector conversation dominated by [OTHER] (self=%d other=%d): refused dimension=%q, coerced to topic_highlight",
			profile.SelfMsgCount, profile.OtherMsgCount, dim)
	}
	return MemoryGateAllow, ""
}

func ValidateMemoryDimension(profile SessionContentProfile, dimension string) (MemoryGateAction, string) {
	dim := strings.ToLower(strings.TrimSpace(dimension))
	if profile.ChatSenders.ChatHeavilyNonSelf() {
		switch dim {
		case "user_insight", "deep_user_model", "agentic_memory":
			return MemoryGateCoerceToHighlight, fmt.Sprintf(
				"chat content dominated by non-self senders (self=%d non-self=%d): refused dimension=%q, coerced to topic_highlight",
				profile.ChatSenders.SelfMsgCount, profile.ChatSenders.NonSelfTotal(), dim)
		}
	}
	// Media-app present override: a session that touched any known
	// media-playback app (YouTube / Bilibili / Spotify / Podcasts /
	// Music / 抖音 etc.) is presumed to be passive consumption even
	// when per-event Dominant comes back as performed (clicking play,
	// scrolling the video timeline). Personal-trait dimensions
	// extracted from passive consumption are the "user likes whatever
	// the YouTuber likes" bug. Coerce them to topic_highlight instead
	// of skipping outright — the user MAY have done something
	// noteworthy in the session, but it shouldn't shape their identity.
	if profile.HasMediaPlaybackApp {
		switch dim {
		case "user_insight", "deep_user_model", "agentic_memory":
			return MemoryGateCoerceToHighlight, fmt.Sprintf(
				"media playback app present in session: refused dimension=%q, coerced to topic_highlight", dim)
		}
	}
	switch profile.Dominant {
	case ContentModeAmbient:
		return MemoryGateSkip, "ambient-dominant session: no user-attributable signal"
	case ContentModeObserved:
		switch dim {
		case "user_insight", "deep_user_model", "agentic_memory":
			return MemoryGateCoerceToHighlight, fmt.Sprintf("observed-dominant session: refused dimension=%q, coerced to topic_highlight", dim)
		}
		return MemoryGateAllow, ""
	case ContentModeReceived:
		switch dim {
		case "deep_user_model":
			return MemoryGateSkip, "received-dominant session: deep_user_model would attribute third-party content to user"
		case "user_insight", "agentic_memory":
			// Previously allowed; lone received-mode signal can still
			// legitimately produce a non-trait highlight, but extracting
			// a personal insight or agentic_memory from third-party
			// statements has the same shape of mis-attribution risk as
			// deep_user_model, just slightly weaker. Coerce rather than
			// skip so the content fact is preserved.
			return MemoryGateCoerceToHighlight, fmt.Sprintf(
				"received-dominant session: refused dimension=%q, coerced to topic_highlight", dim)
		}
		return MemoryGateAllow, ""
	}
	return MemoryGateAllow, ""
}
