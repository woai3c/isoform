package main

// mediaPlaybackBundleIDs lists apps whose audio output is overwhelmingly
// non-user playback (music, video, podcasts). Two callers:
//
//  1. activity_triage.isLikelyNonUserSpeech — post-capture heuristic
//     for mic-side picked-up speaker audio. Used as a fallback on machines
//     where the CoreAudio system_audio tap returns silent PCM (e.g. macOS
//     26's tightened Process Tap privacy), which means the SpeechLikelyEcho
//     flag never fires.
//  2. background_audio_system_darwin SystemAudioRecorder_Start — OS-level
//     exclusion from the system audio process tap, so these apps' audio
//     never reaches Yansu's transcription pipeline at all.
//
// Deliberately conservative: only dedicated media apps. Browsers and chat
// apps (WeChat, Slack) are NOT here because they also carry legitimate
// meeting audio (Google Meet in Chrome, WeChat video calls, Slack huddles)
// — excluding the whole bundle would silently drop those. Browser/chat
// media playback is handled post-capture via window-title heuristics in
// activity_triage.isLikelyNonUserSpeech.
var mediaPlaybackBundleIDs = map[string]struct{}{
	// macOS bundle IDs
	"com.apple.QuickTimePlayerX": {},
	"com.apple.podcasts":         {}, // Apple Podcasts
	"com.apple.TV":               {}, // Apple TV
	"com.apple.Music":            {}, // Apple Music
	"tv.danmaku.bili.client":     {}, // Bilibili
	"com.bytedance.douyin.macos": {}, // 抖音
	"org.videolan.vlc":           {},
	"com.colliderli.iina":        {},
	"com.spotify.client":         {},
	"com.netease.163music":       {}, // 网易云音乐
	"com.tencent.QQMusicMac":     {},
	// Windows (lowercase exe basename — see monitor_windows.go FrontmostAppInfo).
	// Only the post-capture isLikelyNonUserSpeech heuristic uses this on
	// Windows; the CATap PID-exclusion path is macOS-only because classic
	// WASAPI loopback can't filter per-process anyway.
	"vlc.exe":             {},
	"wmplayer.exe":        {}, // Windows Media Player
	"itunes.exe":          {}, // iTunes
	"spotify.exe":         {},
	"cloudmusic.exe":      {}, // 网易云音乐 Windows
	"qqmusic.exe":         {}, // QQ Music Windows
	"qqplayer.exe":        {}, // QQ Player
	"potplayermini64.exe": {},
	"potplayer.exe":       {},
	"mpc-hc64.exe":        {}, // Media Player Classic
	"mpc-hc.exe":          {},
	"music.exe":           {}, // Microsoft Groove / generic
	"yymp3.exe":           {}, // 千千音乐
}

// isMediaPlaybackApp returns true when the given bundle ID is a known
// dedicated media-playback application.
func isMediaPlaybackApp(bundleID string) bool {
	if bundleID == "" {
		return false
	}
	_, ok := mediaPlaybackBundleIDs[bundleID]
	return ok
}
