package meetingdetect

import (
	"strings"
	"testing"

	"wails-gui/internal/browsers"
)

func TestAlwaysMeetingApps(t *testing.T) {
	tests := []struct {
		bundleID string
		want     string
	}{
		// macOS bundle IDs.
		{"us.zoom.xos", "Zoom"},
		{"com.microsoft.teams2", "Microsoft Teams"},
		{"com.microsoft.teams", "Microsoft Teams"},
		{"com.cisco.webex.meetings", "WebEx"},
		{"com.apple.FaceTime", "FaceTime"},
		{"com.ringcentral.app", "RingCentral"},
		{"com.amazon.chime", "Amazon Chime"},
		{"com.skype.skype", "Skype"},
		{"com.tencent.tencentmeeting", "Tencent Meeting"},
		// Windows exe basenames (lowercase, as returned by
		// activitymon.FrontmostAppBundleID on Windows).
		{"zoom.exe", "Zoom"},
		{"teams.exe", "Microsoft Teams"},
		{"ms-teams.exe", "Microsoft Teams"},
		{"webex.exe", "WebEx"},
		{"skype.exe", "Skype"},
		{"wemeetapp.exe", "Tencent Meeting"},
	}
	for _, tt := range tests {
		r := IsMeetingApp(tt.bundleID, "")
		if !r.IsMeeting {
			t.Errorf("IsMeetingApp(%q, %q): want meeting, got not meeting", tt.bundleID, "")
		}
		if r.MeetingName != tt.want {
			t.Errorf("IsMeetingApp(%q, %q): want name %q, got %q", tt.bundleID, "", tt.want, r.MeetingName)
		}
	}
}

func TestConditionalApps(t *testing.T) {
	tests := []struct {
		bundleID    string
		windowTitle string
		wantMeeting bool
		wantName    string
	}{
		{"com.tinyspeck.slackmacgap", "Slack - #general", false, ""},
		{"com.tinyspeck.slackmacgap", "Slack - Huddle with team", true, "Slack"},
		{"com.slack.Slack", "Slack | Call", true, "Slack"},
		{"com.hnc.Discord", "Discord - Server", false, ""},
		{"com.hnc.Discord", "Discord - Voice Channel", true, "Discord"},
		{"net.whatsapp.WhatsApp", "WhatsApp", false, ""},
		{"net.whatsapp.WhatsApp", "WhatsApp - Video Call", true, "WhatsApp"},
		{"com.tencent.xinWeChat", "WeChat", false, ""},
		{"com.tencent.xinWeChat", "WeChat - Video Call", true, "WeChat"},
		// Chinese-locale WeChat call titles — without these the
		// activity-triager ambient-audio heuristic mis-labels the
		// user's own voice during a Chinese WeChat call as playback
		// echo, because the session never gets classified as a meeting
		// in the first place.
		{"com.tencent.xinWeChat", "视频通话", true, "WeChat"},
		{"com.tencent.xinWeChat", "语音通话", true, "WeChat"},
		{"com.tencent.xinWeChat", "群语音", true, "WeChat"},
		{"com.tencent.xinWeChat", "群视频", true, "WeChat"},
		{"com.tencent.xinWeChat", "聊天 - 朋友", false, ""},
		{"com.tencent.xinWeChat", "图片和视频", false, ""},
		{"com.tdesktop.telegram", "Telegram - Chat", false, ""},
		{"com.tdesktop.telegram", "Telegram - Group Call", true, "Telegram"},
		// Windows exe basenames — same conditional logic must apply.
		{"slack.exe", "Slack | general", false, ""},
		{"slack.exe", "Slack | Huddle in #general", true, "Slack"},
		{"discord.exe", "Discord", false, ""},
		{"discord.exe", "Discord — Voice Connected", true, "Discord"},
		{"wechat.exe", "WeChat", false, ""},
		{"wechat.exe", "视频通话", true, "WeChat"},
		{"telegram.exe", "Telegram - Chat", false, ""},
		{"telegram.exe", "Telegram - Group Call", true, "Telegram"},
	}
	for _, tt := range tests {
		r := IsMeetingApp(tt.bundleID, tt.windowTitle)
		if r.IsMeeting != tt.wantMeeting {
			t.Errorf("IsMeetingApp(%q, %q): want meeting=%v, got %v", tt.bundleID, tt.windowTitle, tt.wantMeeting, r.IsMeeting)
		}
		if r.MeetingName != tt.wantName {
			t.Errorf("IsMeetingApp(%q, %q): want name %q, got %q", tt.bundleID, tt.windowTitle, tt.wantName, r.MeetingName)
		}
	}
}

func TestBrowserMeetings(t *testing.T) {
	tests := []struct {
		bundleID    string
		windowTitle string
		wantMeeting bool
		wantName    string
	}{
		{"com.google.Chrome", "Gmail - Inbox", false, ""},
		{"com.google.Chrome", "meet.google.com/abc-defg-hij", true, "Google Meet"},
		{"com.apple.Safari", "meet.google.com/abc", true, "Google Meet"},
		{"com.apple.Safari", "Meet - dsy-gsyq-qky", true, "Google Meet"},
		{"com.google.Chrome", "Meet - abc-defg-hij", true, "Google Meet"},
		{"org.mozilla.firefox", "Zoom Meeting", true, "Zoom"},
		// A chat page titled "Microsoft Teams" (a ChatGPT conversation) is
		// no Teams page; Teams sets "<page> | Microsoft Teams".
		{"com.microsoft.edgemac", "Microsoft Teams", false, ""},
		{"com.google.Chrome", "Microsoft Teams", false, ""},
		{"com.microsoft.edgemac", "Standup | Microsoft Teams", true, "Microsoft Teams"},
		{"com.google.Chrome", "Notes from my Zoom Meeting", false, ""},
		{"com.google.Chrome", "How to Meet - new people", false, ""},
		{"company.thebrowser.Browser", "Jitsi Meet - standup", true, "Jitsi Meet"},
		{"com.google.Chrome", "whereby.com/my-room", true, "Whereby"},
		// Windows browser exes — same title-pattern logic.
		{"chrome.exe", "Stack Overflow - Google Chrome", false, ""},
		{"chrome.exe", "Meet - abc-defg-hij - Google Chrome", true, "Google Meet"},
		{"msedge.exe", "Microsoft Teams - Profile 1 - Microsoft​ Edge", false, ""},
		{"msedge.exe", "Standup | Microsoft Teams - Profile 1 - Microsoft​ Edge", true, "Microsoft Teams"},
		{"firefox.exe", "Zoom Meeting", true, "Zoom"},
		{"brave.exe", "whereby.com/my-room - Brave", true, "Whereby"},
	}
	for _, tt := range tests {
		r := IsMeetingApp(tt.bundleID, tt.windowTitle)
		if r.IsMeeting != tt.wantMeeting {
			t.Errorf("IsMeetingApp(%q, %q): want meeting=%v, got %v", tt.bundleID, tt.windowTitle, tt.wantMeeting, r.IsMeeting)
		}
		if tt.wantMeeting && r.MeetingName != tt.wantName {
			t.Errorf("IsMeetingApp(%q, %q): want name %q, got %q", tt.bundleID, tt.windowTitle, tt.wantName, r.MeetingName)
		}
	}
}

func TestIsMeetingEvent(t *testing.T) {
	tests := []struct {
		name        string
		bundleID    string
		windowTitle string
		wantMeeting bool
		wantName    string
		wantBundle  bool // expect BundleID populated
	}{
		// Tier 1: always-meeting native apps — require title hint
		{"zoom active", "us.zoom.xos", "Zoom Meeting", true, "Zoom", true},
		{"zoom controls", "us.zoom.xos", "Meeting Controls", true, "Zoom", true},
		{"zoom share screen", "us.zoom.xos", "Share screen window", true, "Zoom", true},
		{"zoom share toolbar", "us.zoom.xos", "share toolbar", true, "Zoom", true},
		{"zoom idle launcher", "us.zoom.xos", "Zoom Workplace", false, "", false},
		{"zoom empty title", "us.zoom.xos", "", false, "", false},
		{"teams active", "com.microsoft.teams2", "Meeting in Progress", true, "Microsoft Teams", true},
		{"teams sharing", "com.microsoft.teams2", "Sharing", true, "Microsoft Teams", true},
		{"teams idle", "com.microsoft.teams2", "Microsoft Teams", false, "", false},
		{"facetime active", "com.apple.FaceTime", "FaceTime", true, "FaceTime", true},
		{"facetime empty", "com.apple.FaceTime", "", false, "", false},
		{"webex meeting", "com.cisco.webex.meetings", "Meeting Room", true, "WebEx", true},
		{"tencent meeting cn", "com.tencent.tencentmeeting", "会议室", true, "Tencent Meeting", true},

		// Tier 2: conditional apps — same as IsMeetingApp
		{"slack huddle", "com.tinyspeck.slackmacgap", "Slack - Huddle with team", true, "Slack", true},
		{"slack chat", "com.tinyspeck.slackmacgap", "Slack - #general", false, "", false},
		{"discord voice", "com.hnc.Discord", "Discord - Voice Channel", true, "Discord", true},
		{"discord chat", "com.hnc.Discord", "Discord - Server", false, "", false},
		{"whatsapp call", "net.whatsapp.WhatsApp", "WhatsApp - Video Call", true, "WhatsApp", true},

		// Tier 3: browser meetings — same as IsMeetingApp
		{"meet in chrome", "com.google.Chrome", "Meet - abc-defg-hij", true, "Google Meet", true},
		{"meet.google.com", "com.google.Chrome", "meet.google.com/abc", true, "Google Meet", true},
		{"teams web", "com.microsoft.edgemac", "Standup | Microsoft Teams", true, "Microsoft Teams", true},
		{"chat titled teams", "com.google.Chrome", "Microsoft Teams", false, "", false},
		{"random tab", "com.google.Chrome", "Stack Overflow", false, "", false},

		// Unknown apps
		{"finder", "com.apple.finder", "Downloads", false, "", false},
		{"empty", "", "", false, "", false},

		// Windows: tier 1 — always-meeting native apps via exe basename.
		// Same strict rule applies: title hint required, even though
		// IsMeetingApp would accept the exe alone (these are the bug-fix
		// regression cases — previously every Windows bundleID fell through
		// to "no match" because the table only listed macOS bundle IDs).
		{"win zoom meeting", "zoom.exe", "Zoom Meeting - Standup", true, "Zoom", true},
		{"win zoom controls", "zoom.exe", "Meeting Controls", true, "Zoom", true},
		{"win zoom idle", "zoom.exe", "Zoom Workplace", false, "", false},
		// zh-CN Zoom client: active meeting title is "Zoom 会议", idle
		// launcher is "Zoom 工作区". Verified on a real Windows Zoom
		// install where the English strict path missed the active title.
		{"win zoom meeting zh", "zoom.exe", "Zoom 会议", true, "Zoom", true},
		{"win zoom idle zh", "zoom.exe", "Zoom 工作区", false, "", false},
		{"win teams classic", "teams.exe", "Meeting in Standup | Microsoft Teams", true, "Microsoft Teams", true},
		{"win teams new", "ms-teams.exe", "Meeting in Standup | Microsoft Teams", true, "Microsoft Teams", true},
		{"win teams idle", "ms-teams.exe", "Microsoft Teams", false, "", false},
		{"win webex", "webex.exe", "Standup Meeting", true, "WebEx", true},
		{"win tencent meeting cn", "wemeetapp.exe", "腾讯会议", true, "Tencent Meeting", true},

		// Windows: tier 2 — conditional comms apps via exe basename.
		{"win slack huddle", "slack.exe", "Slack | Huddle in #standup", true, "Slack", true},
		{"win slack channel", "slack.exe", "Slack | #standup", false, "", false},
		{"win discord voice", "discord.exe", "Discord — Voice Connected", true, "Discord", true},
		{"win wechat call cn", "wechat.exe", "视频通话", true, "WeChat", true},

		// Windows: tier 3 — browsers via exe basename.
		{"win meet in chrome", "chrome.exe", "Meet - abc-defg-hij - Google Chrome", true, "Google Meet", true},
		{"win teams in edge", "msedge.exe", "Standup | Microsoft Teams - Microsoft Edge", true, "Microsoft Teams", true},
		{"win random chrome tab", "chrome.exe", "Stack Overflow - Google Chrome", false, "", false},

		// Windows: non-meeting apps must still be rejected.
		{"win explorer", "explorer.exe", "Documents", false, "", false},
		{"win notepad", "notepad.exe", "Untitled - Notepad", false, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := IsMeetingEvent(tt.bundleID, tt.windowTitle)
			if r.IsMeeting != tt.wantMeeting {
				t.Errorf("IsMeetingEvent(%q, %q): want meeting=%v, got %v", tt.bundleID, tt.windowTitle, tt.wantMeeting, r.IsMeeting)
			}
			if tt.wantMeeting && r.MeetingName != tt.wantName {
				t.Errorf("IsMeetingEvent(%q, %q): want name %q, got %q", tt.bundleID, tt.windowTitle, tt.wantName, r.MeetingName)
			}
			if tt.wantBundle && r.BundleID != tt.bundleID {
				t.Errorf("IsMeetingEvent(%q, %q): want BundleID=%q, got %q", tt.bundleID, tt.windowTitle, tt.bundleID, r.BundleID)
			}
		})
	}
}

func TestTranscriptionSupportedMeetingEvent(t *testing.T) {
	tests := []struct {
		name        string
		bundleID    string
		windowTitle string
		wantMeeting bool
		wantName    string
	}{
		{"zoom active", "us.zoom.xos", "Zoom Meeting", true, "Zoom"},
		{"zoom idle", "us.zoom.xos", "Zoom Workplace", false, ""},
		{"slack huddle", "com.tinyspeck.slackmacgap", "Slack - Huddle with team", true, "Slack"},
		{"slack channel", "com.tinyspeck.slackmacgap", "Slack - #general", false, ""},
		{"google meet chrome", "com.google.Chrome", "Meet - abc-defg-hij", true, "Google Meet"},
		{"google meet safari", "com.apple.Safari", "meet.google.com/abc", true, "Google Meet"},
		{"teams native", "com.microsoft.teams2", "Meeting in Progress", true, "Microsoft Teams"},
		{"teams web", "com.microsoft.edgemac", "Standup | Microsoft Teams", true, "Microsoft Teams"},
		{"chat titled teams", "com.google.Chrome", "Microsoft Teams", false, ""},
		{"discord excluded", "com.hnc.Discord", "Discord - Voice Channel", false, ""},
		{"jitsi excluded", "company.thebrowser.Browser", "Jitsi Meet - standup", false, ""},
		// Windows: the original bug fixed by this test file's parent change
		// — none of these would match before the per-platform identifier
		// tables were added, so transcription never fired on Windows.
		{"win zoom active", "zoom.exe", "Zoom Meeting - Standup", true, "Zoom"},
		{"win zoom idle", "zoom.exe", "Zoom Workplace", false, ""},
		{"win teams classic native", "teams.exe", "Meeting in Standup | Microsoft Teams", true, "Microsoft Teams"},
		{"win teams new native", "ms-teams.exe", "Meeting in Standup | Microsoft Teams", true, "Microsoft Teams"},
		{"win slack huddle", "slack.exe", "Slack | Huddle in #standup", true, "Slack"},
		{"win slack channel", "slack.exe", "Slack | #general", false, ""},
		{"win google meet chrome", "chrome.exe", "Meet - abc-defg-hij - Google Chrome", true, "Google Meet"},
		{"win teams in edge", "msedge.exe", "Standup | Microsoft Teams - Microsoft Edge", true, "Microsoft Teams"},
		{"win discord excluded", "discord.exe", "Discord — Voice Connected", false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := IsTranscriptionSupportedMeetingEvent(tt.bundleID, tt.windowTitle)
			if r.IsMeeting != tt.wantMeeting {
				t.Fatalf("IsTranscriptionSupportedMeetingEvent(%q, %q): meeting=%v, want %v", tt.bundleID, tt.windowTitle, r.IsMeeting, tt.wantMeeting)
			}
			if r.MeetingName != tt.wantName {
				t.Fatalf("IsTranscriptionSupportedMeetingEvent(%q, %q): name=%q, want %q", tt.bundleID, tt.windowTitle, r.MeetingName, tt.wantName)
			}
		})
	}
}

func TestTranscriptionSupportedMeetingName(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"Zoom", true},
		{"Slack", true},
		{"Google Meet", true},
		{"Microsoft Teams", true},
		{"Discord", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := IsTranscriptionSupportedMeetingName(tt.name); got != tt.want {
			t.Fatalf("IsTranscriptionSupportedMeetingName(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestTranscriptionSupportedNativeMeetingForBundle(t *testing.T) {
	tests := []struct {
		bundleID    string
		wantMeeting bool
		wantName    string
	}{
		{"us.zoom.xos", true, "Zoom"},
		{"com.microsoft.teams2", true, "Microsoft Teams"},
		{"com.microsoft.teams", true, "Microsoft Teams"},
		{"com.cisco.webex", false, ""},
		{"com.tinyspeck.slackmacgap", false, ""},
		{"com.google.Chrome", false, ""},
		// Windows: bundle-only lookup must accept the exe-basename
		// identifier the Windows activity monitor produces.
		{"zoom.exe", true, "Zoom"},
		{"teams.exe", true, "Microsoft Teams"},
		{"ms-teams.exe", true, "Microsoft Teams"},
		{"webex.exe", false, ""},  // WebEx is not in the transcription allowlist.
		{"slack.exe", false, ""},  // Slack is conditional, not always-meeting.
		{"chrome.exe", false, ""}, // Browser, not a native meeting app.
		{"notepad.exe", false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.bundleID, func(t *testing.T) {
			got := TranscriptionSupportedNativeMeetingForBundle(tt.bundleID)
			if got.IsMeeting != tt.wantMeeting {
				t.Fatalf("TranscriptionSupportedNativeMeetingForBundle(%q).IsMeeting = %v, want %v", tt.bundleID, got.IsMeeting, tt.wantMeeting)
			}
			if got.MeetingName != tt.wantName {
				t.Fatalf("TranscriptionSupportedNativeMeetingForBundle(%q).MeetingName = %q, want %q", tt.bundleID, got.MeetingName, tt.wantName)
			}
		})
	}
}

func TestNonMeetingApps(t *testing.T) {
	tests := []struct {
		bundleID    string
		windowTitle string
	}{
		{"com.apple.finder", "Downloads"},
		{"com.apple.Terminal", "bash"},
		{"com.google.Chrome", "Stack Overflow"},
		{"com.microsoft.VSCode", "main.go"},
		{"", ""},
		// Windows: non-meeting exes that share substrings with meeting
		// apps must not false-match.
		{"explorer.exe", "Documents"},
		{"notepad.exe", "Untitled - Notepad"},
		{"code.exe", "main.go - Visual Studio Code"},
		{"steam.exe", "Steam"}, // shares "team" substring with teams.exe — guard against prefix bleed.
	}
	for _, tt := range tests {
		r := IsMeetingApp(tt.bundleID, tt.windowTitle)
		if r.IsMeeting {
			t.Errorf("IsMeetingApp(%q, %q): want not meeting, got meeting=%q", tt.bundleID, tt.windowTitle, r.MeetingName)
		}
	}
}

// TestWindowsExePrefixesAreLowercase asserts that every ".exe" prefix
// across the three meeting-app tables is lowercase. The Windows-side
// caller (activitymon.FrontmostAppBundleID on Windows) lowercases what
// it returns from QueryFullProcessImageNameW, so an uppercase entry
// would silently fail to match the real bundleID — exactly the class
// of bug this PR fixed. A literal "Zoom.exe" added by a future
// contributor must trip this test, not ship a regression.
func TestWindowsExePrefixesAreLowercase(t *testing.T) {
	check := func(table, name string, prefixes []string) {
		t.Helper()
		for _, p := range prefixes {
			if !strings.HasSuffix(p, ".exe") {
				continue
			}
			if p != strings.ToLower(p) {
				t.Errorf("%s[%q] prefix %q is not lowercase; Windows callers lowercase bundleID so this entry can never match", table, name, p)
			}
		}
	}
	for _, app := range alwaysMeetingApps {
		check("alwaysMeetingApps", app.name, app.prefixes)
	}
	for _, app := range conditionalMeetingApps {
		check("conditionalMeetingApps", app.name, app.prefixes)
	}
	for _, exe := range browsers.WindowsExeNames() {
		if exe != strings.ToLower(exe) {
			t.Errorf("browser exe %q is not lowercase; Windows callers lowercase bundleID so this entry can never match", exe)
		}
	}
}
