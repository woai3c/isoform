package main

import (
	"bytes"
	"log"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"wails-gui/internal/platform/activitymon"
)

func TestMeetingBrowserNativeProbeForbiddenInTests(t *testing.T) {
	defer func() {
		if got := recover(); got == nil || !strings.Contains(got.(string), "native meeting browser probe") {
			t.Fatalf("registry must reject a missing browser stub before native code: %v", got)
		}
	}()
	meetingProbes.browserURL("com.google.Chrome")
}

func TestMeetingBrowserProbeCapturedBeforeRestore(t *testing.T) {
	// With one P, the child cannot execute until this goroutine yields. Restore
	// the registry before the child first runs, reproducing the dangerous order.
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	saved := meetingProbes.browserURL
	t.Cleanup(func() {
		browserMeetings.pending.Wait()
		meetingProbes.browserURL = saved
	})
	meetingProbes.browserURL = func(string) activitymon.BrowserURLRead {
		return activitymon.BrowserURLRead{URL: "https://meet.google.com/captured"}
	}
	answer := startBrowserURLProbe("com.google.Chrome")
	meetingProbes.browserURL = saved
	select {
	case got := <-answer:
		if got.URL != "https://meet.google.com/captured" {
			t.Fatalf("child used a restored probe: %q", got.URL)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("captured probe did not finish")
	}
}

func TestMeetingBrowserTimeoutWaitsBeforeRestoringProbe(t *testing.T) {
	saved := meetingProbes.browserURL
	release := make(chan struct{})
	started := make(chan struct{})
	noteStarted := sync.OnceFunc(func() { close(started) })
	releaseProbe := sync.OnceFunc(func() { close(release) })
	t.Cleanup(func() {
		releaseProbe()
		browserMeetings.pending.Wait()
		meetingProbes.browserURL = saved
	})
	meetingProbes.browserURL = func(string) activitymon.BrowserURLRead {
		noteStarted()
		<-release
		return activitymon.BrowserURLRead{URL: "https://meet.google.com/abc-defg-hij"}
	}
	front := activitymon.AppInfo{BundleID: "com.google.Chrome", WindowTitle: t.Name()}
	if got := browserMeetingInFront(front, time.Now()); got != "" {
		t.Fatalf("timed-out lookup became meeting evidence: %q", got)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("late worker did not start")
	}
	releaseProbe()
	browserMeetings.pending.Wait()
	if got := browserMeetingInFront(front, time.Now()); got != "" {
		t.Fatalf("late result overwrote timed-out evidence: %q", got)
	}
	// A fresh key uses the same successful stub as a positive control.
	front.WindowTitle += " new tab"
	if got := browserMeetingInFront(front, time.Now()); got != "Google Meet" {
		t.Fatalf("fresh lookup did not recognize the control meeting: %q", got)
	}
}

func TestMeetingBrowserAccepts850msAnswerAndCachesFourSeconds(t *testing.T) {
	saved := meetingProbes.browserURL
	calls := 0
	t.Cleanup(func() { browserMeetings.pending.Wait(); meetingProbes.browserURL = saved })
	meetingProbes.browserURL = func(string) activitymon.BrowserURLRead {
		calls++
		time.Sleep(850 * time.Millisecond)
		return activitymon.BrowserURLRead{URL: "https://meet.google.com/abc-defg-hij"}
	}
	front := activitymon.AppInfo{BundleID: "com.google.Chrome", WindowTitle: t.Name()}
	now := time.Now()
	for _, elapsed := range []time.Duration{0, 3*time.Second + 999*time.Millisecond, 4 * time.Second} {
		if got := browserMeetingInFront(front, now.Add(elapsed)); got != "Google Meet" {
			t.Fatalf("850 ms answer was dropped at %s: %q", elapsed, got)
		}
		browserMeetings.pending.Wait()
	}
	if calls != 2 {
		t.Fatalf("four-second cache issued %d probes, want 2", calls)
	}
}

func TestMeetingBrowserNativeStatusPreservesDiagnostic(t *testing.T) {
	for _, tc := range []struct {
		name    string
		result  activitymon.BrowserURLRead
		message string
	}{
		{"timeout", activitymon.BrowserURLRead{URL: "https://meet.google.com/abc-defg-hij", TimedOut: true}, "did not give its tab's address in 1s"},
		{"pending", activitymon.BrowserURLRead{URL: "https://meet.google.com/abc-defg-hij", InFlight: true}, "browser lookup is still in flight"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			saved := meetingProbes.browserURL
			t.Cleanup(func() { browserMeetings.pending.Wait(); meetingProbes.browserURL = saved })
			meetingProbes.browserURL = func(string) activitymon.BrowserURLRead { return tc.result }
			previous := log.Writer()
			var output bytes.Buffer
			log.SetOutput(&output)
			defer log.SetOutput(previous)
			front := activitymon.AppInfo{BundleID: "com.google.Chrome", WindowTitle: t.Name()}
			if got := browserMeetingInFront(front, time.Now()); got != "" {
				t.Fatalf("unavailable probe became evidence: %q", got)
			}
			browserMeetings.pending.Wait()
			log.SetOutput(previous)
			if !strings.Contains(output.String(), tc.message) {
				t.Fatalf("native status lost diagnostic: %s", output.String())
			}
		})
	}
}

func TestMeetingBrowserFocusUnavailableKeepsAudioExcludedUntilObservedSafe(t *testing.T) {
	for _, privateFirst := range []bool{true, false} {
		name := "unknown startup"
		if privateFirst {
			name = "private then timeout"
		}
		t.Run(name, func(t *testing.T) {
			h := newCallHarness(t)
			h.holders = holding("com.google.Chrome.helper")
			if privateFirst {
				h.front = activitymon.AppInfo{BundleID: "com.google.Chrome", WindowTitle: "Private - Incognito"}
				h.run(5*time.Second, nil)
			}
			h.front = activitymon.AppInfo{FocusUnavailable: true}
			h.run(30*time.Second, nil)
			uncertain := h.app.annotateBackgroundAudioChunk(capturedAudioChunk{
				Source: backgroundListeningSourceMicrophone, CapturedAt: h.now,
				Duration:    5 * time.Second,
				CapturedApp: activitymon.AppInfo{BundleID: "com.google.Chrome", WindowTitle: "ChatGPT"},
			})
			if !uncertain.FocusUnavailable || h.app.shouldTranscribeBackgroundAudioChunk(uncertain) {
				t.Fatal("a successful end snapshot cleared unavailable focus inside the audio chunk")
			}
			if !chunkProtected(uncertain) {
				t.Fatal("unavailable focus bypassed the recording privacy gate")
			}
			if h.engine.seenBlobs() != 0 {
				t.Fatal("unavailable focus sent microphone audio to ASR")
			}
			if privateFirst {
				_, _, private := h.app.captureSpans.during(h.now.Add(-5*time.Second).UnixMilli(), h.now.UnixMilli())
				if !private {
					t.Fatal("a focus timeout ended the private exclusion")
				}
			}
			// A later successful public observation is the only release.
			h.front = activitymon.AppInfo{BundleID: "com.google.Chrome", AppName: "Google Chrome", WindowTitle: "ChatGPT"}
			h.url = "https://chatgpt.com/"
			h.run(30*time.Second, nil)
			h.requireNoCall("public dictation after focus recovery")
			h.requireOnlyDictation(h.finish())
		})
	}
}
