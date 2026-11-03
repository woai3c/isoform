package main

import (
	"context"
	"fmt"
	"strings"

	"wails-gui/internal/identity"
	"wails-gui/internal/platform/activitymon"
)

// EventIdentityVerdictDTO is what the Activity page's debug overlay renders
// next to each captured event. The fields explain not just what the
// identity layer decided, but WHY — so the developer can immediately tell
// whether a mis-attribution is a bug in the classifier or downstream.
type EventIdentityVerdictDTO struct {
	Index       int      `json:"index"`       // position in the input slice, for the frontend to align with its own list
	ContentMode string   `json:"contentMode"` // performed | received | observed | ambient | unknown
	Reasoning   string   `json:"reasoning"`   // one-line explanation in plain English
	SpeechHints struct { // populated only for kind=speech
		Source         string `json:"source,omitempty"`
		LikelyEcho     bool   `json:"likelyEcho,omitempty"`
		NonUserSpeech  bool   `json:"nonUserSpeech"`
		MediaApp       bool   `json:"mediaApp,omitempty"`
		BrowserMedia   bool   `json:"browserMedia,omitempty"`
		MeetingContext bool   `json:"meetingContext,omitempty"`
	} `json:"speechHints"`
}

// ClassifyActivityEvents runs each event through the same EventContentMode
// classifier the triager uses, and returns a per-event verdict. The
// frontend invokes this when the user turns on the "Show identity verdicts"
// debug toggle on the Activity page; the verdicts are render-only, never
// persisted.
//
// inMeeting reflects the session-level meeting flag (sessionGroup.hasMeetingSessions)
// since speech classification routes system_audio differently when inside a meeting
// (received/talkmate vs. observed/media-bleed).
func (a *App) ClassifyActivityEvents(events []activitymon.ActivityEvent, inMeeting bool) []EventIdentityVerdictDTO {
	if len(events) == 0 {
		return []EventIdentityVerdictDTO{}
	}
	out := make([]EventIdentityVerdictDTO, len(events))
	for i, evt := range events {
		mode := EventContentMode(evt, inMeeting)
		v := EventIdentityVerdictDTO{
			Index:       i,
			ContentMode: string(mode),
			Reasoning:   reasoningForEvent(evt, mode, inMeeting),
		}
		if evt.Kind == activitymon.EventSpeech {
			v.SpeechHints.Source = evt.SpeechSource
			v.SpeechHints.LikelyEcho = evt.SpeechLikelyEcho
			v.SpeechHints.NonUserSpeech = isLikelyNonUserSpeech(evt)
			v.SpeechHints.MediaApp = isMediaPlaybackApp(evt.AppBundleID)
			v.SpeechHints.MeetingContext = inMeeting
			// Browser-media check matches the labeling in isLikelyNonUserSpeech.
			if evt.SpeechSource == "" || evt.SpeechSource == "microphone" {
				if isBrowserMediaTitle(evt.AppBundleID, evt.WindowTitle) {
					v.SpeechHints.BrowserMedia = true
				}
			}
		}
		out[i] = v
	}
	return out
}

// isBrowserMediaTitle is a debug-side mirror of the browser-media detection
// inside isLikelyNonUserSpeech (which is internally inlined). Keeping it
// here as a tiny helper means the speech-hint payload can flag the exact
// branch that triggered without re-running the full classifier.
func isBrowserMediaTitle(bundleID, windowTitle string) bool {
	if bundleID == "" || windowTitle == "" {
		return false
	}
	// Avoid importing meetingdetect just for the bundle check — match the
	// same lowercase contains semantics used by browserMediaPlaybackTitleHints.
	lower := strings.ToLower(bundleID)
	if !(strings.Contains(lower, "chrome") || strings.Contains(lower, "safari") ||
		strings.Contains(lower, "firefox") || strings.Contains(lower, "edge") ||
		strings.Contains(lower, "arc") || strings.Contains(lower, "brave")) {
		return false
	}
	for _, hint := range browserMediaPlaybackTitleHints {
		if strings.Contains(windowTitle, hint) {
			return true
		}
	}
	return false
}

func reasoningForEvent(evt activitymon.ActivityEvent, mode ContentMode, inMeeting bool) string {
	switch evt.Kind {
	case activitymon.EventClick:
		return fmt.Sprintf("Click in %s — interaction events are always performed by the user.", appLabelForReasoning(evt))
	case activitymon.EventKeyboard:
		return fmt.Sprintf("Keystroke in %s — keyboard input is always performed by the user.", appLabelForReasoning(evt))
	case activitymon.EventFocus:
		return fmt.Sprintf("Focus change in %s — focus events follow user navigation.", appLabelForReasoning(evt))
	case activitymon.EventAppSwitch:
		return fmt.Sprintf("App switched to %s — switches are user-initiated.", appLabelForReasoning(evt))
	case activitymon.EventSpeech:
		return reasoningForSpeechEvent(evt, mode, inMeeting)
	}
	return string(mode)
}

func reasoningForSpeechEvent(evt activitymon.ActivityEvent, mode ContentMode, inMeeting bool) string {
	app := appLabelForReasoning(evt)
	if evt.SpeechSource == "system_audio" {
		if inMeeting {
			return fmt.Sprintf("system_audio inside a meeting (%s) — remote participant speech, attributed to a Contact.", app)
		}
		return fmt.Sprintf("system_audio outside a meeting (%s) — speaker output / media bleed, attributed to ContentSubject.", app)
	}
	if isMediaPlaybackApp(evt.AppBundleID) {
		if inMeeting {
			return fmt.Sprintf("Microphone speech in a media app (%s) inside a meeting — treated as remote audio bleed.", app)
		}
		return fmt.Sprintf("Microphone speech in dedicated media app %s — treated as media playback, not the user.", app)
	}
	if isBrowserMediaTitle(evt.AppBundleID, evt.WindowTitle) {
		if inMeeting {
			return fmt.Sprintf("Microphone speech in browser with media title %q — treated as received in a meeting.", evt.WindowTitle)
		}
		return fmt.Sprintf("Microphone speech in browser with media title %q — treated as observed media.", evt.WindowTitle)
	}
	if evt.SpeechLikelyEcho {
		return fmt.Sprintf("Microphone speech with echo flag set (%s) — speaker output picked up by mic.", app)
	}
	if evt.AppBundleID == "com.tencent.xinWeChat" &&
		(strings.Contains(evt.WindowTitle, "图片和视频") || strings.Contains(evt.WindowTitle, "视频通话")) {
		return "Microphone speech inside WeChat image/video viewer or video call — treated as non-user audio."
	}
	switch mode {
	case ContentModePerformed:
		return fmt.Sprintf("Microphone speech in %s — treated as the user's own voice.", app)
	default:
		return fmt.Sprintf("Speech in %s (mode=%s).", app, mode)
	}
}

func appLabelForReasoning(evt activitymon.ActivityEvent) string {
	if evt.AppName != "" {
		return evt.AppName
	}
	if evt.AppBundleID != "" {
		return evt.AppBundleID
	}
	return "an unknown app"
}

// ChatSenderVerdictDTO is what the EventFramePopup's parsed-content panel
// shows next to every chat message sender. It mirrors what
// senderLabeler.formatSenderLabel would produce, but is queryable from the
// frontend on demand (the popup doesn't run the labeler, only the
// triager's render path does).
type ChatSenderVerdictDTO struct {
	RawSender         string  `json:"rawSender"`
	Resolved          bool    `json:"resolved"`
	Kind              string  `json:"kind"` // self | contact | anonymous | unknown
	PrincipalID       string  `json:"principalId,omitempty"`
	DisplayName       string  `json:"displayName,omitempty"`
	Relationship      string  `json:"relationship,omitempty"`
	AttributionMethod string  `json:"attributionMethod,omitempty"` // handle_id | display_handle | alias | unknown
	Confidence        float64 `json:"confidence"`
	Label             string  `json:"label"` // pre-formatted prompt-style label ([YOU] / [Contact: …] / …)
}

// ResolveChatSender resolves a single chat sender against the identity
// store and returns a debug verdict. Read-only: autoBucket is disabled so
// hovering the message in the popup never creates an Anonymous principal
// as a side effect. Empty/nil-store fall back to an "unknown" verdict
// rather than an error — this is a render-time debug helper.
func (a *App) ResolveChatSender(app, sender string) ChatSenderVerdictDTO {
	raw := strings.TrimSpace(sender)
	verdict := ChatSenderVerdictDTO{
		RawSender: raw,
		Kind:      string(identity.SubjectUnknown),
		Label:     fmt.Sprintf("[Unknown: %s]", raw),
	}
	if raw == "" {
		verdict.Label = "[Unknown: —]"
		return verdict
	}
	// Mirror what sender_labeler does in the real pipeline: strip
	// timestamps / role badges / message body bleed before resolving.
	normalized := identity.NormalizeSenderName(raw)
	if normalized == "" {
		normalized = raw
	}
	verdict.Label = fmt.Sprintf("[Unknown: %s]", normalized)
	store := a.store
	if store == nil {
		return verdict
	}
	r := identity.NewResolver(store, false)
	ref, err := r.ResolveSender(context.Background(), app, "", normalized)
	if err != nil || ref.Kind == identity.SubjectUnknown {
		return verdict
	}
	verdict.Resolved = true
	verdict.Kind = string(ref.Kind)
	verdict.PrincipalID = ref.PrincipalID
	verdict.DisplayName = ref.DisplayName
	verdict.Relationship = ref.Relationship
	verdict.AttributionMethod = string(ref.Method)
	verdict.Confidence = ref.Confidence
	verdict.Label = formatSenderLabel(ref, normalized)
	return verdict
}
