package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// A finished recording's sidecar names what keeps its call private:
// Protected, CaptureSource and ActivitySessions. Recording the engine that
// transcribed it and noting a failed decode, at the same time and while it
// is read, must never drop them or let a reader see a half-written file.
func TestMeetingSidecarKeepsPrivacyFieldsUnderConcurrentWrites(t *testing.T) {
	dir := t.TempDir()
	rel := filepath.Join("2026-10-20", "call.wav")
	if err := os.MkdirAll(filepath.Join(dir, "2026-10-20"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, rel), []byte("not a wav"), 0o600); err != nil {
		t.Fatal(err)
	}
	private := meetingAudioMeta{
		MeetingID:        "meeting-1",
		Protected:        true,
		CaptureSource:    activitySourceProvenance{SessionID: "session-1", Private: true, Apps: []string{"com.example.vault"}},
		ActivitySessions: []string{"session-1", "session-2"},
	}
	if err := writeMeetingAudioMeta(meetingWAVMetaPath(dir, rel), private); err != nil {
		t.Fatal(err)
	}

	var writers, readers sync.WaitGroup
	done := make(chan struct{})
	failures := make(chan string, 1)
	fail := func(message string) {
		select {
		case failures <- message:
		default:
		}
	}
	for range 4 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				raw, err := os.ReadFile(meetingWAVMetaPath(dir, rel))
				if err != nil {
					fail("the sidecar went missing: " + err.Error())
					return
				}
				var meta meetingAudioMeta
				if err := json.Unmarshal(raw, &meta); err != nil {
					fail("a reader saw a half-written sidecar: " + string(raw))
					return
				}
				if !meta.Protected || !meta.CaptureSource.Private || len(meta.ActivitySessions) != 2 {
					fail("a reader saw the privacy fields dropped: " + string(raw))
					return
				}
			}
		}()
	}
	for i := range 200 {
		writers.Add(4)
		go func() {
			defer writers.Done()
			engine := meetingTranscribedByAfterCall
			if i%2 == 1 {
				engine = meetingTranscribedByLive
			}
			recordMeetingTranscribedBy(dir, rel, engine)
		}()
		go func() {
			defer writers.Done()
			noteMeetingAudioDecodeFailed(dir, rel, time.Now())
		}()
		// The recording writer and crash recovery write whole records.
		go func() {
			defer writers.Done()
			if err := writeMeetingAudioMeta(meetingWAVMetaPath(dir, rel), private); err != nil {
				fail("a whole record failed: " + err.Error())
			}
		}()
		go func() {
			defer writers.Done()
			clearMeetingAudioDecodeFailed(dir, rel)
		}()
	}
	writers.Wait()
	close(done)
	readers.Wait()
	select {
	case message := <-failures:
		t.Fatal(message)
	default:
	}
	got := readMeetingWAVMeta(dir, rel)
	if !got.Protected || !got.CaptureSource.Private || len(got.CaptureSource.Apps) != 1 ||
		len(got.ActivitySessions) != 2 || got.MeetingID != "meeting-1" {
		t.Fatalf("the privacy fields were dropped: %+v", got)
	}
	if got.TranscribedBy == "" {
		t.Fatalf("a whole record dropped the engine: %+v", got)
	}
}

// A sidecar that cannot be parsed is left exactly as it is: rewriting it from
// an empty record would drop the fields that keep a protected call closed.
func TestMeetingSidecarUpdatesLeaveAnUnreadableSidecarAlone(t *testing.T) {
	dir := t.TempDir()
	rel := "call.wav"
	if err := os.WriteFile(filepath.Join(dir, rel), []byte("not a wav"), 0o600); err != nil {
		t.Fatal(err)
	}
	corrupt := []byte(`{"meetingId":"meeting-1","protected":true,"captureSou`)
	path := meetingWAVMetaPath(dir, rel)
	if err := os.WriteFile(path, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	recordMeetingTranscribedBy(dir, rel, meetingTranscribedByAfterCall)
	noteMeetingAudioDecodeFailed(dir, rel, time.Now())
	if raw, err := os.ReadFile(path); err != nil || !bytes.Equal(raw, corrupt) {
		t.Fatalf("an unreadable sidecar was rewritten: %q (%v)", raw, err)
	}
	// The failure is still remembered, so the recording is not decoded on
	// every load.
	if !meetingAudioDecodeFailedRecently(dir, rel, time.Now()) {
		t.Fatal("a failed decode beside an unreadable sidecar was forgotten")
	}
	clearMeetingAudioDecodeFailed(dir, rel)
	if raw, err := os.ReadFile(path); err != nil || !bytes.Equal(raw, corrupt) {
		t.Fatalf("an unreadable sidecar was rewritten: %q (%v)", raw, err)
	}
}

// A recording without a sidecar gets none. The read gate keeps a transcript
// that names no sources closed while its recording has no sidecar, and a
// sidecar holding only the engine or a decode note would open it.
func TestMeetingSidecarIsNeverCreatedSoALegacyTranscriptStaysClosed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store := newTestStore(t)
	rel := "legacy.wav"
	if err := os.WriteFile(filepath.Join(store.AudioDir(), rel), []byte("not a wav"), 0o600); err != nil {
		t.Fatal(err)
	}
	session := ActivitySessionRow{ID: "legacy-session", AudioPath: rel}
	transcript := "# Call\n- **Audio**: activity/audio/" + rel + "\n\n## Transcript\nhello"
	if _, err := store.transcriptSourceIDs(session, transcript); err == nil {
		t.Fatal("a legacy transcript without a sidecar is readable before any update")
	}
	noteMeetingAudioDecodeFailed(store.AudioDir(), rel, time.Now())
	recordMeetingTranscribedBy(store.AudioDir(), rel, meetingTranscribedByAfterCall)
	if _, err := os.Stat(meetingWAVMetaPath(store.AudioDir(), rel)); !os.IsNotExist(err) {
		t.Fatalf("an update created a sidecar: %v", err)
	}
	if ids, err := store.transcriptSourceIDs(session, transcript); err == nil {
		t.Fatalf("an update opened a legacy transcript: %v", ids)
	}
	if !meetingAudioDecodeFailedRecently(store.AudioDir(), rel, time.Now()) {
		t.Fatal("the failed decode of a recording without a sidecar was forgotten")
	}
}

// Removing a recording never leaves it without its privacy fields: a
// recording that cannot be removed keeps its sidecar, and a field change that
// read the sidecar before a removal cannot write it back after.
func TestMeetingSidecarRemovalLeavesNoPartialSidecar(t *testing.T) {
	dir := t.TempDir()
	private := meetingAudioMeta{MeetingID: "meeting-1", Protected: true, ActivitySessions: []string{"session-1"}}

	// The recording cannot be removed: here it is a folder that is not empty.
	stuck := "stuck.wav"
	if err := os.MkdirAll(filepath.Join(dir, stuck, "inner"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeMeetingAudioMeta(meetingWAVMetaPath(dir, stuck), private); err != nil {
		t.Fatal(err)
	}
	if err := removeMeetingRecording(dir, stuck); err == nil {
		t.Fatal("removing a folder that is not empty succeeded")
	}
	noteMeetingAudioDecodeFailed(dir, stuck, time.Now())
	recordMeetingTranscribedBy(dir, stuck, meetingTranscribedByLive)
	if got := readMeetingWAVMeta(dir, stuck); !got.Protected || len(got.ActivitySessions) != 1 || got.MeetingID != "meeting-1" {
		t.Fatalf("a recording that could not be removed lost its privacy fields: %+v", got)
	}

	// A change holds the sidecar it read while the removal starts: the
	// removal waits for it, and the sidecar is gone afterwards.
	rel := "call.wav"
	if err := os.WriteFile(filepath.Join(dir, rel), []byte("not a wav"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeMeetingAudioMeta(meetingWAVMetaPath(dir, rel), private); err != nil {
		t.Fatal(err)
	}
	read, release := make(chan struct{}), make(chan struct{})
	changed := make(chan bool)
	go func() {
		changed <- updateMeetingWAVMeta(dir, rel, func(meta *meetingAudioMeta) bool {
			close(read)
			<-release
			meta.TranscribedBy = meetingTranscribedByLive
			return true
		})
	}()
	<-read
	removed := make(chan error)
	go func() { removed <- removeMeetingRecording(dir, rel) }()
	select {
	case err := <-removed:
		t.Fatalf("the removal did not wait for the change that read the sidecar: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if _, err := os.Stat(meetingWAVMetaPath(dir, rel)); err != nil {
		t.Fatalf("the sidecar went before the change finished: %v", err)
	}
	close(release)
	if !<-changed {
		t.Fatal("the change was not written")
	}
	if err := <-removed; err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(meetingWAVMetaPath(dir, rel)); !os.IsNotExist(err) {
		t.Fatalf("a sidecar outlived its recording: %+v", readMeetingWAVMeta(dir, rel))
	}
}

// A whole record written by the recording writer or crash recovery keeps the
// transcribing engine and a decode note another writer put on the sidecar.
func TestMeetingSidecarWholeRecordKeepsTheEngineAndDecodeNote(t *testing.T) {
	dir := t.TempDir()
	rel := "call.wav"
	if err := os.WriteFile(filepath.Join(dir, rel), []byte("not a wav"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := meetingWAVMetaPath(dir, rel)
	if err := writeMeetingAudioMeta(path, meetingAudioMeta{MeetingID: "meeting-1"}); err != nil {
		t.Fatal(err)
	}
	recordMeetingTranscribedBy(dir, rel, meetingTranscribedByAfterCall)
	noteMeetingAudioDecodeFailed(dir, rel, time.Now())
	if err := writeMeetingAudioMeta(path, meetingAudioMeta{MeetingID: "meeting-1", Protected: true}); err != nil {
		t.Fatal(err)
	}
	got := readMeetingWAVMeta(dir, rel)
	if !got.Protected || got.TranscribedBy != meetingTranscribedByAfterCall || got.DecodeFailed == nil {
		t.Fatalf("a whole record dropped another writer's field: %+v", got)
	}
	if err := writeMeetingAudioMeta(path, meetingAudioMeta{MeetingID: "meeting-1", Protected: true, TranscribedBy: meetingTranscribedByLive}); err != nil {
		t.Fatal(err)
	}
	if got := readMeetingWAVMeta(dir, rel); got.TranscribedBy != meetingTranscribedByLive {
		t.Fatalf("a whole record's own engine did not win: %+v", got)
	}
	// A cleared decode note stays cleared through the next whole record.
	clearMeetingAudioDecodeFailed(dir, rel)
	if err := writeMeetingAudioMeta(path, meetingAudioMeta{MeetingID: "meeting-1", Protected: true}); err != nil {
		t.Fatal(err)
	}
	if got := readMeetingWAVMeta(dir, rel); got.DecodeFailed != nil || got.TranscribedBy != meetingTranscribedByLive {
		t.Fatalf("a whole record brought back a cleared note or dropped the engine: %+v", got)
	}
}

// Every field of a sidecar but the engine and the decode note establishes the
// recording, so a field added later cannot be missed by the read gate's
// "not established" rule.
func TestMeetingAudioMetaEstablishedNamesEveryField(t *testing.T) {
	notes := map[string]bool{"TranscribedBy": true, "DecodeFailed": true}
	set := func(field reflect.Value) {
		switch field.Kind() {
		case reflect.String:
			field.SetString("x")
		case reflect.Bool:
			field.SetBool(true)
		case reflect.Int, reflect.Int64:
			field.SetInt(1)
		case reflect.Slice:
			field.Set(reflect.MakeSlice(field.Type(), 1, 1))
		case reflect.Pointer:
			field.Set(reflect.New(field.Type().Elem()))
		default:
			t.Fatalf("no test value for a %s field", field.Kind())
		}
	}
	if meetingAudioMetaEstablished(meetingAudioMeta{TranscribedBy: "live", DecodeFailed: &meetingAudioDecodeFailure{}}) {
		t.Fatal("the engine and the decode note establish a recording")
	}
	metaType := reflect.TypeOf(meetingAudioMeta{})
	for i := range metaType.NumField() {
		name := metaType.Field(i).Name
		if notes[name] {
			continue
		}
		if name == "CaptureSource" {
			sourceType := reflect.TypeOf(activitySourceProvenance{})
			for j := range sourceType.NumField() {
				var meta meetingAudioMeta
				set(reflect.ValueOf(&meta.CaptureSource).Elem().Field(j))
				if !meetingAudioMetaEstablished(meta) {
					t.Errorf("CaptureSource.%s does not establish the recording", sourceType.Field(j).Name)
				}
			}
			continue
		}
		var meta meetingAudioMeta
		set(reflect.ValueOf(&meta).Elem().Field(i))
		if !meetingAudioMetaEstablished(meta) {
			t.Errorf("%s does not establish the recording", name)
		}
	}
}

// Sidecars that older builds created for a recording that had none hold only
// the transcribing engine or a decode note. They name nothing about the
// recording, so the read gate treats them as missing: a transcript that names
// no sources stays closed, and one that names its sources stays readable.
func TestMeetingSidecarHoldingOnlyNotesDoesNotOpenALegacyTranscript(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store := newTestStore(t)
	rel := "legacy.wav"
	if err := os.WriteFile(filepath.Join(store.AudioDir(), rel), []byte("not a wav"), 0o600); err != nil {
		t.Fatal(err)
	}
	session := ActivitySessionRow{ID: "legacy-session", AudioPath: rel}
	undeclared := "# Call\n- **Audio**: activity/audio/" + rel + "\n\n## Transcript\nhello"
	declared := withTranscriptActivitySources("# Call\n- **Audio**: activity/audio/"+rel+"\n- **Session**: x\n\n## Transcript\nhello", []string{session.ID})
	for name, raw := range map[string]string{
		"engine only":      `{"captureSource":{"sessionId":"","startTime":0,"endTime":0},"transcribedBy":"after-call"}`,
		"decode note only": `{"captureSource":{"sessionId":"","startTime":0,"endTime":0},"decodeFailed":{"size":9,"modMs":1,"failedMs":2}}`,
		"both notes":       `{"transcribedBy":"live","decodeFailed":{"size":9,"modMs":1,"failedMs":2}}`,
		"empty":            `{}`,
	} {
		if err := os.WriteFile(meetingWAVMetaPath(store.AudioDir(), rel), []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		if ids, err := store.transcriptSourceIDs(session, undeclared); err == nil || !strings.Contains(err.Error(), "recording dependencies unavailable") {
			t.Errorf("%s: a legacy transcript opened: %v %v", name, ids, err)
		}
		if ids, err := store.transcriptSourceIDs(session, declared); err != nil || !stringSliceContains(ids, session.ID) {
			t.Errorf("%s: a transcript that names its sources closed: %v %v", name, ids, err)
		}
	}
	// A sidecar the recording writer made still establishes the recording.
	if err := writeMeetingAudioMeta(meetingWAVMetaPath(store.AudioDir(), rel), meetingAudioMeta{MeetingID: "meeting-1", TranscribedBy: meetingTranscribedByLive}); err != nil {
		t.Fatal(err)
	}
	if ids, err := store.transcriptSourceIDs(session, undeclared); err != nil || !stringSliceContains(ids, session.ID) {
		t.Fatalf("a recording's own sidecar no longer establishes it: %v %v", ids, err)
	}
}
