package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

// A call's sources are declared only when the read gate can establish them
// from the recording. A recording without its sidecar (an older build's call)
// names none, so a transcript written for it could not open through the
// session row alone. The writer's allowance check already refuses such a
// recording before transcribing; the "no transcript" half guards that, and
// passed before this change too.
func TestAnUnestablishedRecordingNamesNoSourcesAndGetsNoTranscript(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app := mainChatTestApp(t)
	store := newLifecycleStore(t)
	app.activity = &activityMonitorState{store: store}
	engine := &recordingFixtureEngine{fakeLongForm: fakeLongForm{texts: map[int16]string{2000: "The launch date moves to Friday."}}}
	app.whisper = &whisperState{server: engine}
	w, err := newMeetingAudioWriter(store.AudioDir())
	if err != nil {
		t.Fatal(err)
	}
	w.SetMeeting("Zoom", "us.zoom.xos", "sidecar-less-call")
	w.WriteCapturedChunk(timedChunk(backgroundListeningSourceMicrophone, time.Now(), constSamples(16000, 2000)))
	session, meta, ok := app.saveCallRecordingLocked(w, 0, "meeting_ended")
	if !ok {
		t.Fatal("recording was not saved")
	}
	// The recording loses its sidecar, as one an older build made never had it.
	if err := os.Remove(meetingWAVMetaPath(store.AudioDir(), session.AudioPath)); err != nil {
		t.Fatal(err)
	}
	if ids := app.meetingRecordingSourceSessions(session); ids != nil {
		t.Fatalf("an unestablished recording names sources: %v", ids)
	}
	app.saveAutomaticMeetingTranscript(session, nil, meta.App, 0, meta.MeetingID)
	if engine.calls != 0 || transcriptFileExists(session.ID) {
		t.Fatal("an unestablished recording was transcribed")
	}
}

// A live transcript with no recording still declares its own session, and a
// recorded call with its sidecar is transcribed and declares its session.
func TestATranscriptOfAnEstablishedCallDeclaresItsSources(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app := mainChatTestApp(t)
	store := newLifecycleStore(t)
	app.activity = &activityMonitorState{store: store}
	live := ActivitySessionRow{ID: "live-call"}
	if ids := app.meetingRecordingSourceSessions(live); len(ids) != 1 || ids[0] != live.ID {
		t.Fatalf("a live call does not declare its own session: %v", ids)
	}
	// Dev Tools' synthetic calls have no activity store and keep their session.
	if ids := (&App{}).meetingRecordingSourceSessions(live); len(ids) != 1 || ids[0] != live.ID {
		t.Fatalf("a synthetic call does not keep its session: %v", ids)
	}
	app.whisper = &whisperState{server: &recordingFixtureEngine{fakeLongForm: fakeLongForm{texts: map[int16]string{2000: "The launch date moves to Friday."}}}}
	w, err := newMeetingAudioWriter(store.AudioDir())
	if err != nil {
		t.Fatal(err)
	}
	w.SetMeeting("Zoom", "us.zoom.xos", "established-call")
	w.WriteCapturedChunk(timedChunk(backgroundListeningSourceMicrophone, time.Now(), constSamples(16000, 2000)))
	session, meta, ok := app.saveCallRecordingLocked(w, 0, "meeting_ended")
	if !ok {
		t.Fatal("recording was not saved")
	}
	if ids := app.meetingRecordingSourceSessions(session); !stringSliceContains(ids, session.ID) {
		t.Fatalf("an established recording does not declare its session: %v", ids)
	}
	app.saveAutomaticMeetingTranscript(session, nil, meta.App, 0, meta.MeetingID)
	row, err := store.GetSessionByID(session.ID)
	if err != nil || row == nil || row.TranscriptPath == "" {
		t.Fatalf("an established call was not transcribed: %+v %v", row, err)
	}
	raw, err := os.ReadFile(row.TranscriptPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), transcriptActivitySourcesMarker) || !strings.Contains(string(raw), session.ID) {
		t.Fatalf("an established call's transcript does not declare its session:\n%s", raw)
	}
	if ids, err := store.transcriptSourceIDs(*row, ""); err != nil || !stringSliceContains(ids, session.ID) {
		t.Fatalf("an established call's transcript is not readable: %v %v", ids, err)
	}
}
