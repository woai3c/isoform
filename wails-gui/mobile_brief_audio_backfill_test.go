package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newAudioBackfillFixture(t *testing.T) (*mobileBriefService, MobileBriefV1, *atomic.Int32, *fakeMobileBriefClock) {
	t.Helper()
	clock := &fakeMobileBriefClock{now: mobileBriefTestDay(8, 0)}
	svc, gathers := newTestMobileBriefService(t, clock)
	now := clock.Now()
	brief := buildMobileBriefV1(mobileBriefInput{
		Slot: mobileBriefSlotMorning, AsOf: now, WindowStart: now.Add(-time.Hour), Location: time.UTC,
		Moved: []mobileBriefCandidate{{SourceKind: "history", SourceID: "h-morning", Title: "Shipped the plan", At: now.Add(-time.Hour)}},
	})
	if len(brief.Evidence) != 1 {
		t.Fatalf("fixture evidence = %+v, want one readable source", brief.Evidence)
	}
	brief.Narrative = []MobileBriefPara{{Text: "You shipped the plan. What do you want to start with?", Refs: []string{brief.Evidence[0].Ref}}}
	brief.NarrativeStatus = "ok"
	if err := svc.store.save(brief, now); err != nil {
		t.Fatal(err)
	}
	svc.app.mobileBriefSvc = svc
	return svc, brief, gathers, clock
}

func TestMobileBriefAudioBackfillOnStartOnce(t *testing.T) {
	svc, brief, gathers, _ := newAudioBackfillFixture(t)
	t.Cleanup(svc.Stop)
	orphan := []byte("old recording whose words cannot be proven")
	if err := os.WriteFile(filepath.Join(svc.store.dir, brief.BriefID+mobileBriefAudioExtM4A), orphan, 0600); err != nil {
		t.Fatal(err)
	}
	var syntheses atomic.Int32
	spoken := make(chan string, 4)
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	speaker := fakeSpeaker("en", testWAV(1), nil)
	speaker.synthesize = func(ctx context.Context, _, text string) ([]byte, error) {
		syntheses.Add(1)
		spoken <- text
		select {
		case <-release:
			return testWAV(1), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	svc.speaker = speaker
	audioStored := make(chan struct{})
	var publishOnce sync.Once
	svc.publish = func(MobileBriefV1, bool) error {
		publishOnce.Do(func() { close(audioStored) })
		return nil
	}

	startReturned := make(chan struct{})
	go func() {
		svc.Start(context.Background())
		close(startReturned)
	}()
	select {
	case <-startReturned:
	case <-time.After(5 * time.Second):
		t.Fatal("service start waited for the voice")
	}
	var said string
	select {
	case said = <-spoken:
	case <-time.After(5 * time.Second):
		t.Fatal("today's silent brief was not synthesized")
	}
	// The fake voice is still blocked. A repeated Start is an idempotent service
	// call, not another backfill.
	svc.Start(context.Background())
	unblock()
	select {
	case <-audioStored:
	case <-time.After(5 * time.Second):
		t.Fatal("backfill did not attach the recording")
	}
	svc.Stop()

	if got := syntheses.Load(); got != 1 {
		t.Fatalf("synthesis ran %d times, want once", got)
	}
	if got := gathers.Load(); got != 0 {
		t.Fatalf("backfill generated %d new briefs", got)
	}
	stored, ok := svc.store.load(brief.BriefID)
	if !ok || stored.Audio == nil || stored.Audio.Status != mobileBriefAudioStatusOK {
		t.Fatalf("stored audio = %+v, %v", stored.Audio, ok)
	}
	if want := mobileBriefSpokenText(stored, "en"); said != want {
		t.Fatalf("voice read %q, want the current brief text %q", said, want)
	}
	if want := mobileBriefAudioBinding(stored, "en"); stored.Audio.binding != want {
		t.Fatal("the recording is not bound to the text it speaks")
	}
	if want := fmt.Sprintf("%x", sha256.Sum256(testWAV(1))); stored.Audio.SHA256 != want {
		t.Fatal("the stored recording did not come from the fresh fake synthesis")
	}
	if delivered := svc.app.mobileBriefForDelivery(stored); delivered.Audio == nil {
		t.Fatal("the newly bound recording is not readable")
	}
}

func TestMobileBriefAudioBackfillSkipsBoundRecording(t *testing.T) {
	svc, brief, _, _ := newAudioBackfillFixture(t)
	brief.Audio = &MobileBriefAudio{
		Status: mobileBriefAudioStatusOK, Bytes: 1,
		binding: mobileBriefAudioBinding(brief, "en"),
	}
	if err := svc.store.save(brief, svc.now()); err != nil {
		t.Fatal(err)
	}
	var syntheses atomic.Int32
	speaker := fakeSpeaker("en", testWAV(1), nil)
	speaker.synthesize = func(context.Context, string, string) ([]byte, error) {
		syntheses.Add(1)
		return testWAV(1), nil
	}
	svc.speaker = speaker

	svc.backfillTodayAudio(context.Background())
	if got := syntheses.Load(); got != 0 {
		t.Fatalf("a brief with bound audio synthesized %d times", got)
	}
}

func TestMobileBriefAudioBackfillSkipsIneligibleBrief(t *testing.T) {
	for _, tc := range []struct {
		name    string
		prepare func(*testing.T, *mobileBriefService, MobileBriefV1, *fakeMobileBriefClock, *mobileBriefSpeaker)
	}{
		{"withdrawn source", func(t *testing.T, svc *mobileBriefService, _ MobileBriefV1, _ *fakeMobileBriefClock, _ *mobileBriefSpeaker) {
			t.Helper()
			if err := svc.app.handoffStore.Delete("h-morning"); err != nil {
				t.Fatal(err)
			}
		}},
		{"Closed Eyes", func(_ *testing.T, svc *mobileBriefService, _ MobileBriefV1, _ *fakeMobileBriefClock, _ *mobileBriefSpeaker) {
			svc.app.closedEyes.enabled.Store(true)
		}},
		{"narrative not ok", func(t *testing.T, svc *mobileBriefService, brief MobileBriefV1, _ *fakeMobileBriefClock, _ *mobileBriefSpeaker) {
			t.Helper()
			brief.NarrativeStatus = "unavailable"
			if err := svc.store.save(brief, svc.now()); err != nil {
				t.Fatal(err)
			}
		}},
		{"not today", func(_ *testing.T, _ *mobileBriefService, _ MobileBriefV1, clock *fakeMobileBriefClock, _ *mobileBriefSpeaker) {
			clock.Advance(24 * time.Hour)
		}},
		{"audio not wired", func(_ *testing.T, _ *mobileBriefService, _ MobileBriefV1, _ *fakeMobileBriefClock, speaker *mobileBriefSpeaker) {
			speaker.voiceReady = nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, brief, _, clock := newAudioBackfillFixture(t)
			var syntheses atomic.Int32
			speaker := fakeSpeaker("en", testWAV(1), nil)
			speaker.synthesize = func(context.Context, string, string) ([]byte, error) {
				syntheses.Add(1)
				return testWAV(1), nil
			}
			tc.prepare(t, svc, brief, clock, &speaker)
			svc.speaker = speaker

			svc.backfillTodayAudio(context.Background())
			if got := syntheses.Load(); got != 0 {
				t.Fatalf("ineligible brief synthesized %d times", got)
			}
			if stored, _ := svc.store.load(brief.BriefID); stored.Audio != nil {
				t.Fatalf("ineligible brief gained audio: %+v", stored.Audio)
			}
		})
	}
}

func TestMobileBriefAudioBackfillDoesNotBindOrphanAudio(t *testing.T) {
	svc, brief, _, _ := newAudioBackfillFixture(t)
	orphan := []byte("old recording whose words cannot be proven")
	path := filepath.Join(svc.store.dir, brief.BriefID+mobileBriefAudioExtM4A)
	if err := os.WriteFile(path, orphan, 0600); err != nil {
		t.Fatal(err)
	}
	var syntheses atomic.Int32
	speaker := fakeSpeaker("en", testWAV(1), nil)
	speaker.voiceReady = func(string) bool { return false }
	speaker.synthesize = func(context.Context, string, string) ([]byte, error) {
		syntheses.Add(1)
		return testWAV(1), nil
	}
	svc.speaker = speaker

	svc.backfillTodayAudio(context.Background())
	if got := syntheses.Load(); got != 0 {
		t.Fatalf("missing voice caused %d syntheses", got)
	}
	stored, _ := svc.store.load(brief.BriefID)
	if stored.Audio != nil {
		t.Fatalf("the orphan file was bound to the brief: %+v", stored.Audio)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != string(orphan) {
		t.Fatalf("the unproven orphan file changed: %v", err)
	}
}

func TestMobileBriefAudioBackfillDoesNotReplaceNewerBrief(t *testing.T) {
	svc, brief, _, _ := newAudioBackfillFixture(t)
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	speaker := fakeSpeaker("en", testWAV(1), nil)
	speaker.synthesize = func(context.Context, string, string) ([]byte, error) {
		close(started)
		<-release
		return testWAV(1), nil
	}
	svc.speaker = speaker
	var published atomic.Int32
	svc.publish = func(MobileBriefV1, bool) error {
		published.Add(1)
		return nil
	}
	done := make(chan struct{})
	go func() {
		svc.backfillTodayAudio(context.Background())
		close(done)
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("backfill did not reach the fake voice")
	}

	newer := brief
	newer.BriefID = "2026-10-09-evening"
	newer.Slot = mobileBriefSlotEvening
	newer.AsOf = mobileBriefTestDay(18, 0).Format(time.RFC3339)
	if err := svc.store.save(newer, mobileBriefTestDay(18, 0)); err != nil {
		t.Fatal(err)
	}
	unblock()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("backfill did not finish")
	}
	latest, ok := svc.store.latest()
	if !ok || latest.BriefID != newer.BriefID {
		t.Fatalf("backfill made %q latest again, want %q", latest.BriefID, newer.BriefID)
	}
	if old, _ := svc.store.load(brief.BriefID); old.Audio != nil {
		t.Fatalf("the superseded brief gained audio: %+v", old.Audio)
	}
	if got := published.Load(); got != 0 {
		t.Fatalf("the superseded brief was published %d times", got)
	}
}

func TestMobileBriefAudioBackfillDoesNotPublishSupersededBrief(t *testing.T) {
	svc, brief, _, _ := newAudioBackfillFixture(t)
	svc.speaker = fakeSpeaker("en", testWAV(1), nil)
	var published atomic.Int32
	svc.publish = func(MobileBriefV1, bool) error {
		published.Add(1)
		return nil
	}
	svc.deliveryMu.Lock()
	var unlockOnce sync.Once
	unlockDelivery := func() { unlockOnce.Do(func() { svc.deliveryMu.Unlock() }) }
	t.Cleanup(unlockDelivery)
	done := make(chan struct{})
	go func() {
		svc.backfillTodayAudio(context.Background())
		close(done)
	}()
	mobileTestWaitFor(t, "backfill to wait for publication", func() bool {
		stored, ok := svc.store.load(brief.BriefID)
		return ok && stored.Audio != nil
	})

	newer := brief
	newer.BriefID = "2026-10-09-evening"
	newer.Slot = mobileBriefSlotEvening
	newer.AsOf = mobileBriefTestDay(18, 0).Format(time.RFC3339)
	if err := svc.store.save(newer, mobileBriefTestDay(18, 0)); err != nil {
		t.Fatal(err)
	}
	unlockDelivery()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("backfill publication did not finish")
	}
	latest, ok := svc.store.latest()
	if !ok || latest.BriefID != newer.BriefID {
		t.Fatalf("backfill made %q latest again, want %q", latest.BriefID, newer.BriefID)
	}
	if got := published.Load(); got != 0 {
		t.Fatalf("the superseded brief was published %d times", got)
	}
}
