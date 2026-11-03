// Package meetingdetect identifies meeting and voice/video call applications
// by app identifier (macOS bundle ID / Windows exe basename) and window title
// patterns. Used by the background audio pipeline to tag speech events with
// meeting context and trigger transcript saving.
//
// The same matching logic runs on macOS and Windows; each meeting app entry
// lists every per-platform identifier we know about (e.g. "us.zoom.xos" on
// macOS, "zoom.exe" on Windows). Callers pass whatever stable per-process
// identifier their platform exposes — macOS bundle ID from NSRunningApplication
// or the lowercase exe basename from activitymon.FrontmostAppBundleID on
// Windows — and matching works uniformly.
package meetingdetect

import (
	"strings"

	"wails-gui/internal/browsers"
)

// Result describes whether the frontmost app is a meeting/call app.
type Result struct {
	IsMeeting   bool
	MeetingName string // human-readable platform name (e.g. "Zoom", "Google Meet")
	// BundleID of the matched meeting app. Populated by IsMeetingAppActive
	// so callers can tag speech events with the correct app without a
	// second lookup. Empty for frontmost/browser matches.
	BundleID string
	AppName  string
}

// IsTranscriptionSupportedMeetingName reports whether Yansu should produce
// meeting transcripts for this platform. Meeting detection intentionally
// remains broader than transcription so other activity heuristics can still
// recognize calls without recording/transcribing them.
func IsTranscriptionSupportedMeetingName(name string) bool {
	switch strings.TrimSpace(name) {
	case "Zoom", "Slack", "Google Meet", "Microsoft Teams":
		return true
	default:
		return false
	}
}

func IsTranscriptionSupportedMeeting(result Result) bool {
	return result.IsMeeting && IsTranscriptionSupportedMeetingName(result.MeetingName)
}

// IsTranscriptionSupportedMeetingEvent is the strict, active-window check used
// before sending background audio into ASR. It avoids treating idle launchers
// such as "Zoom Workplace" as transcription targets.
func IsTranscriptionSupportedMeetingEvent(bundleID, windowTitle string) Result {
	result := IsMeetingEvent(bundleID, windowTitle)
	if IsTranscriptionSupportedMeeting(result) {
		return result
	}
	return Result{}
}

// TranscriptionSupportedNativeMeetingForBundle returns a supported native
// meeting app by bundle ID without asserting an active meeting window. This is
// used only by conservative fallback logic that also requires audio/mic
// evidence before candidate transcription is allowed.
func TranscriptionSupportedNativeMeetingForBundle(bundleID string) Result {
	for _, app := range alwaysMeetingApps {
		if !IsTranscriptionSupportedMeetingName(app.name) {
			continue
		}
		if hasAnyPrefix(bundleID, app.prefixes) {
			return Result{IsMeeting: true, MeetingName: app.name, BundleID: bundleID}
		}
	}
	return Result{}
}

// hasAnyPrefix reports whether s starts with any of the given prefixes.
// Empty prefixes are skipped so a stray "" in a table doesn't match
// everything.
func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if p == "" {
			continue
		}
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// alwaysMeetingApps are native video-conferencing apps. Being frontmost is
// treated as an active meeting (foreground detection). For background
// detection, callers must also verify an active-meeting window title hint
// via IsMeetingAppActive — these apps are often left running idle.
//
// Each entry lists every per-platform identifier we know about: macOS
// bundle IDs (e.g. "us.zoom.xos") and lowercase Windows exe basenames
// (e.g. "zoom.exe"). Matching uses strings.HasPrefix against whichever
// identifier the caller's platform produces.
var alwaysMeetingApps = []struct {
	prefixes []string
	name     string
	// activeMeetingTitleHints are substrings that appear in a window title
	// ONLY when an active call/meeting is in progress. "" means the app
	// doesn't have a distinct idle-vs-call title, so we can't distinguish —
	// in that case background detection is skipped.
	activeMeetingTitleHints []string
	// idleMaxWindows: when > 0, if the app has more visible windows than
	// this threshold, consider it in an active meeting regardless of
	// title matching. Idle meeting apps typically show 1-2 windows
	// (launcher/main); an active call creates additional windows for
	// controls, sharing toolbar, participants, etc. This structural
	// heuristic avoids false MEETING_ENDED when window titles change
	// during screen sharing, view toggles, or toolbar animations.
	idleMaxWindows int
}{
	// Zoom: idle shows 1 window ("Zoom Workplace" in English, "Zoom 工作区"
	// in zh-CN). Active meeting shows 3+: meeting view, controls bar, and
	// during screen sharing: share toolbar, "Share screen window" picker,
	// notification overlays. Title hints cover the common titles;
	// window-count covers the rest. "Zoom 会议" is the literal zh-CN
	// active-meeting title — added so Chinese-locale users hit the strict
	// IsMeetingEvent path without depending on the idleMaxWindows fallback.
	{[]string{"us.zoom.xos", "zoom.exe"}, "Zoom", []string{"Zoom Meeting", "Zoom 会议", "Meeting Controls", "Share screen", "share toolbar", "screen sharing"}, 2},
	// Microsoft Teams: classic Teams ships as "Teams.exe", new Teams (Teams 2.x)
	// ships as "ms-teams.exe". Both surface the same "Meeting in <name>" /
	// "Sharing" titles during an active meeting.
	{[]string{"com.microsoft.teams2", "com.microsoft.teams", "ms-teams.exe", "teams.exe"}, "Microsoft Teams", []string{"Meeting in", "Sharing"}, 2},
	// WebEx Windows: only webex.exe owns user-visible windows; webexhost.exe
	// / webexmta.exe are background helpers that never have focus, so they
	// don't add real coverage here.
	{[]string{"com.cisco.webex", "webex.exe"}, "WebEx", []string{"Meeting"}, 0},
	{[]string{"com.apple.FaceTime"}, "FaceTime", []string{"FaceTime"}, 0}, // FaceTime window only exists during a call; macOS-only.
	{[]string{"com.ringcentral", "ringcentral.exe"}, "RingCentral", []string{"Meeting"}, 0},
	// Amazon Chime ships as "Amazon Chime.exe" on Windows — the space in
	// the basename is intentional, not a typo. FrontmostAppBundleID
	// lowercases what it returns from QueryFullProcessImageNameW, so the
	// prefix we match against has to include the space and be lowercase.
	{[]string{"com.amazon.chime", "amazon chime.exe"}, "Amazon Chime", []string{"Meeting"}, 0},
	{[]string{"com.skype.skype", "skype.exe"}, "Skype", []string{"Call with"}, 0},
	// Tencent Meeting (腾讯会议) / VooV Meeting (international). The Windows
	// exe is wemeetapp.exe (the underlying "WeMeet" project name).
	{[]string{"com.tencent.tencentmeeting", "wemeetapp.exe"}, "Tencent Meeting", []string{"会议", "Meeting"}, 0},
}

// conditionalMeetingApps are messaging/communication apps that only count as
// a meeting when the window title contains a call/voice indicator.
var conditionalMeetingApps = []struct {
	prefixes  []string
	name      string
	titleHint []string // any of these substrings in the window title = meeting
}{
	{[]string{"com.tinyspeck.slackmacgap", "com.slack.Slack", "slack.exe"}, "Slack", []string{"Huddle", "huddle", "Call", "call"}},
	{[]string{"com.hnc.Discord", "com.discordapp.Discord", "discord.exe"}, "Discord", []string{"Voice", "voice", "Call", "call"}},
	{[]string{"net.whatsapp.WhatsApp", "whatsapp.exe"}, "WhatsApp", []string{"Call", "call", "Video", "video"}},
	// WeChat localizes its in-call window titles. The macOS Chinese build
	// uses 视频通话 / 语音通话 / 群语音 / 群视频; the English build uses
	// "Video Call" / "Voice Call". Without the Chinese hints, Chinese
	// users' WeChat calls never get classified as meetings, which lets
	// the activity-triager's ambient-audio heuristic mis-label the
	// user's own voice as playback echo.
	{[]string{"com.tencent.xinWeChat", "com.tencent.wechat", "wechat.exe"}, "WeChat", []string{"Call", "call", "Video", "video", "视频通话", "语音通话", "群语音", "群视频"}},
	{[]string{"com.tdesktop.telegram", "ru.keepcoder.Telegram", "telegram.exe"}, "Telegram", []string{"Call", "call", "Group Call"}},
	{[]string{"org.whispersystems.signal-desktop", "signal.exe"}, "Signal", []string{"Call", "call"}},
	{[]string{"jp.naver.line", "com.nhnent.LINE", "line.exe"}, "LINE", []string{"Call", "call", "Video", "video"}},
	{[]string{"com.kakao.KakaoTalk", "kakaotalk.exe"}, "KakaoTalk", []string{"Call", "call", "Video", "video"}},
	{[]string{"com.viber", "viber.exe"}, "Viber", []string{"Call", "call"}},
}

// browserMeetingPatterns match window titles when a browser is frontmost.
// Each entry maps title substrings (hints) or title beginnings (prefixes) to
// a meeting name. A chat page can carry any words in its title (a ChatGPT
// conversation named "Microsoft Teams"), so only the page titles the
// meeting sites set count: "Meet - abc-defg-hij", "Zoom Meeting",
// "<meeting> | Microsoft Teams".
var browserMeetingPatterns = []struct {
	hints    []string
	prefixes []string
	name     string
}{
	// Order matters: more specific patterns first to avoid false matches
	// (e.g., "Jitsi Meet" before "Meet -").
	{[]string{"Jitsi Meet"}, nil, "Jitsi Meet"},
	{[]string{"zoom.us/j/", "zoom.us/wc/"}, []string{"Zoom Meeting"}, "Zoom"},
	{[]string{"| Microsoft Teams"}, nil, "Microsoft Teams"},
	{[]string{"whereby.com"}, nil, "Whereby"},
	// "Meet - " is the page title format Google Meet uses (e.g. "Meet - abc-defg-hij").
	{[]string{"meet.google.com"}, []string{"Meet - "}, "Google Meet"},
}

// browserMeetingForTitle returns the meeting name a browser window title
// names, or "".
func browserMeetingForTitle(windowTitle string) string {
	title := strings.TrimSpace(windowTitle)
	for _, pattern := range browserMeetingPatterns {
		for _, hint := range pattern.hints {
			if strings.Contains(title, hint) {
				return pattern.name
			}
		}
		for _, prefix := range pattern.prefixes {
			if strings.HasPrefix(title, prefix) {
				return pattern.name
			}
		}
	}
	return ""
}

// IsMeetingEvent checks whether a single activity event (click, app_switch,
// focus) indicates an active meeting. Stricter than IsMeetingApp: requires
// title-hint confirmation for ALL tiers, including always-meeting native
// apps. This prevents false positives when the user clicks on an idle
// launcher like "Zoom Workplace". Designed for the event-driven meeting
// detection path in handleActivityEvent.
func IsMeetingEvent(bundleID, windowTitle string) Result {
	// 1. Always-meeting native apps — require BOTH bundle match AND title hint.
	for _, app := range alwaysMeetingApps {
		if !hasAnyPrefix(bundleID, app.prefixes) {
			continue
		}
		for _, hint := range app.activeMeetingTitleHints {
			if hint != "" && strings.Contains(windowTitle, hint) {
				return Result{IsMeeting: true, MeetingName: app.name, BundleID: bundleID}
			}
		}
		return Result{}
	}

	// 2. Conditional apps — same logic as IsMeetingApp (already strict).
	for _, app := range conditionalMeetingApps {
		if !hasAnyPrefix(bundleID, app.prefixes) {
			continue
		}
		for _, hint := range app.titleHint {
			if strings.Contains(windowTitle, hint) {
				return Result{IsMeeting: true, MeetingName: app.name, BundleID: bundleID}
			}
		}
		return Result{}
	}

	// 3. Browser-based meetings — same logic as IsMeetingApp (already strict).
	if browsers.IsBrowser(bundleID) {
		if name := browserMeetingForTitle(windowTitle); name != "" {
			return Result{IsMeeting: true, MeetingName: name, BundleID: bundleID}
		}
		return Result{}
	}

	return Result{}
}

// IsMeetingApp checks whether the given bundle ID and window title indicate
// a meeting or voice/video call is active. Thread-safe (no shared state).
func IsMeetingApp(bundleID, windowTitle string) Result {
	// 1. Always-meeting native apps (bundle ID prefix match).
	for _, app := range alwaysMeetingApps {
		if hasAnyPrefix(bundleID, app.prefixes) {
			return Result{IsMeeting: true, MeetingName: app.name}
		}
	}

	// 2. Conditional apps (bundle ID + window title hint).
	for _, app := range conditionalMeetingApps {
		if !hasAnyPrefix(bundleID, app.prefixes) {
			continue
		}
		for _, hint := range app.titleHint {
			if strings.Contains(windowTitle, hint) {
				return Result{IsMeeting: true, MeetingName: app.name}
			}
		}
		// App matches but no call indicator in title — not a meeting.
		return Result{}
	}

	// 3. Browser-based meetings (bundle ID is a browser + title pattern).
	if browsers.IsBrowser(bundleID) {
		if name := browserMeetingForTitle(windowTitle); name != "" {
			return Result{IsMeeting: true, MeetingName: name}
		}
	}

	return Result{}
}

// VisibleWindow is a minimal representation of a visible on-screen window,
// matching the fields needed for active-meeting detection. Callers provide
// this from their platform-specific window-enumeration API so meetingdetect
// stays platform-agnostic.
type VisibleWindow struct {
	BundleID string
	AppName  string
	Title    string
}

// IsKnownMeetingBundle returns true if the bundle ID matches any registered
// always-meeting app, regardless of whether a meeting is currently active.
// Callers use this to narrow the window enumeration scope before calling
// IsMeetingAppActive.
func IsKnownMeetingBundle(bundleID string) bool {
	for _, meeting := range alwaysMeetingApps {
		if hasAnyPrefix(bundleID, meeting.prefixes) {
			return true
		}
	}
	return false
}

// IsBrowserBundle returns true if the bundle ID is a known browser. Callers
// use this to identify browser windows whose titles may indicate a web-based
// meeting (Google Meet, Teams web, Jitsi, Whereby, etc.).
func IsBrowserBundle(bundleID string) bool {
	return browsers.IsBrowser(bundleID)
}

// IsMeetingAppActive checks visible windows for an active meeting using
// two strategies:
//  1. Title hints: at least one window title contains an activeMeetingTitleHint.
//  2. Window count: if the app has more windows than idleMaxWindows, it's
//     likely in an active meeting. Idle apps typically show 1-2 windows
//     (launcher); an active call adds control bars, sharing toolbars, etc.
//
// For browser-based meetings (Google Meet, etc.), the caller still needs
// IsMeetingApp with the frontmost window title.
func IsMeetingAppActive(windows []VisibleWindow) Result {
	// Group windows by meeting app for the count heuristic.
	type appMatch struct {
		meeting  int // index into alwaysMeetingApps
		bundleID string
		appName  string
		count    int
		titleHit bool
	}
	matches := map[int]*appMatch{}

	for _, win := range windows {
		for i, meeting := range alwaysMeetingApps {
			if !hasAnyPrefix(win.BundleID, meeting.prefixes) {
				continue
			}
			m, ok := matches[i]
			if !ok {
				m = &appMatch{meeting: i, bundleID: win.BundleID, appName: win.AppName}
				matches[i] = m
			}
			m.count++
			for _, hint := range meeting.activeMeetingTitleHints {
				if hint != "" && strings.Contains(win.Title, hint) {
					m.titleHit = true
				}
			}
		}
	}

	for _, m := range matches {
		meeting := alwaysMeetingApps[m.meeting]
		if m.titleHit {
			return Result{
				IsMeeting:   true,
				MeetingName: meeting.name,
				BundleID:    m.bundleID,
				AppName:     m.appName,
			}
		}
		if meeting.idleMaxWindows > 0 && m.count > meeting.idleMaxWindows {
			return Result{
				IsMeeting:   true,
				MeetingName: meeting.name,
				BundleID:    m.bundleID,
				AppName:     m.appName,
			}
		}
	}
	return Result{}
}
