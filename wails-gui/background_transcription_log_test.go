package main

import (
	"bytes"
	"log"
	"strings"
	"testing"
	"time"
)

func TestBackgroundTranscriptLogsArePrivateByDefault(t *testing.T) {
	for _, tc := range []struct {
		name      string
		text      string
		abandoned bool
		marker    string
	}{
		{name: "accepted", text: "The private project password is amber meadow", marker: "accepted transcript"},
		{name: "dropped", text: "Thanks for watching", marker: "dropped likely non-speech transcript"},
		{name: "abandoned", text: "The private project password is amber meadow", abandoned: true, marker: "dropping post-timeout transcript"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, flag := range []string{"", "0", "true", "1"} {
				t.Run("debug="+flag, func(t *testing.T) {
					t.Setenv(backgroundTranscriptDebugEnv, flag)
					var output bytes.Buffer
					previous := log.Writer()
					log.SetOutput(&output)
					t.Cleanup(func() { log.SetOutput(previous) })
					called := false
					scheduler := newBackgroundTranscriptionScheduler(newFakeBackgroundTranscriber(tc.text), func(text string, _ backgroundListeningSource, _ speechCaptureContext) {
						called = true
						if text != tc.text {
							t.Fatalf("delivered transcript changed: %q", text)
						}
					})
					scheduler.abandoned.Store(tc.abandoned)
					scheduler.processBatch([]scheduledAudioChunk{{source: backgroundListeningSourceMicrophone, wavData: makeTestWAV(16000, 160), duration: time.Second}})
					got := output.String()
					if !strings.Contains(got, tc.marker) || !strings.Contains(got, "chars=") || !strings.Contains(got, "words=") {
						t.Fatalf("missing transcript metadata: %s", got)
					}
					wantText := flag == "1"
					if strings.Contains(got, tc.text) != wantText || strings.Contains(got, "text=") != wantText {
						t.Fatalf("debug=%q: unexpected transcript visibility: %s", flag, got)
					}
					if called != (tc.name == "accepted") {
						t.Fatalf("onSpeech called=%v for %s", called, tc.name)
					}
				})
			}
		})
	}
}

func TestBackgroundTranscriptDebugLogIsBounded(t *testing.T) {
	t.Setenv(backgroundTranscriptDebugEnv, "1")
	text := strings.Repeat("private", 100)
	if got := backgroundTranscriptDebugLogSuffix(text); strings.Contains(got, text) || len(got) > 150 {
		t.Fatalf("debug transcript was not bounded: %d bytes", len(got))
	}
}
