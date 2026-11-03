package meetingdetect

import "testing"

func TestBrowserMeetingForURL(t *testing.T) {
	tests := []struct {
		url, want string
	}{
		{"https://meet.google.com/abc-defg-hij", "Google Meet"},
		{"https://meet.google.com/abc-defg-hij?authuser=1", "Google Meet"},
		{"https://meet.google.com/landing", ""},
		{"https://meet.google.com/", ""},
		{"https://app.zoom.us/wc/81234567890/join?fromPWA=1", "Zoom"},
		{"https://us02web.zoom.us/j/81234567890?pwd=x", "Zoom"},
		{"https://zoom.us/pricing", ""},
		{"https://teams.microsoft.com/l/meetup-join/19%3ameeting_x/0", "Microsoft Teams"},
		{"https://teams.live.com/meet/9312345", "Microsoft Teams"},
		{"https://teams.microsoft.com/v2/#/conversations/19:abc", ""},
		{"https://teams.microsoft.com/v2/#/calendarv2", ""},
		{"https://meet.jit.si/standup", "Jitsi Meet"},
		{"https://whereby.com/my-room", "Whereby"},
		{"https://whereby.com/pricing", ""},
		{"https://whereby.com/login", ""},
		// Chats named after a meeting are no call.
		{"https://chatgpt.com/c/68f0-zoom-meeting-prep", ""},
		{"https://claude.ai/chat/abc", ""},
		{"https://evil.example/meet.google.com/abc-defg-hij", ""},
		{"https://meet.google.com.evil.example/abc-defg-hij", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := BrowserMeetingForURL(tt.url); got != tt.want {
			t.Errorf("BrowserMeetingForURL(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}
