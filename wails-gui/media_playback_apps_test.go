package main

import "testing"

func TestIsMediaPlaybackApp(t *testing.T) {
	cases := []struct {
		name     string
		bundleID string
		want     bool
	}{
		{"known media app", "com.spotify.client", true},
		{"another known media app", "org.videolan.vlc", true},
		{"empty bundle id", "", false},
		{"unknown app", "com.example.notamediaapp", false},
		// Browsers / chat apps are intentionally excluded from this list
		// because they also carry meeting audio. See media_playback_apps.go.
		{"browser is not a media app", "com.google.Chrome", false},
		{"wechat is not a media app", "com.tencent.xinWeChat", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isMediaPlaybackApp(c.bundleID); got != c.want {
				t.Errorf("isMediaPlaybackApp(%q) = %v, want %v", c.bundleID, got, c.want)
			}
		})
	}
}
