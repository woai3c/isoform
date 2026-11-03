package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
	"wails-gui/storage"
)

// Uses verified on-device models and synthetic Flite voices; no capture device,
// app lifecycle or network operation. Portable unit tests run without assets.
func TestCallNativeModels(t *testing.T) {
	dir := os.Getenv("ISOFORM_CALL_TEST_MODELS")
	if dir == "" {
		t.Skip("set ISOFORM_CALL_TEST_MODELS to verified segmentation.onnx and embedding.onnx")
	}
	d, err := newCallDiarizer(sherpaPaths{segmentation: filepath.Join(dir, "segmentation.onnx"), speakerEmbd: filepath.Join(dir, "embedding.onnx")})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	path := filepath.Join("testdata", "call_recording", "two_remote_voices.wav")
	info, err := readMeetingWAVInfo(path)
	if err != nil {
		t.Fatal(err)
	}
	r, err := openMeetingWAVChannel(path, info, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var samples []float32
	for {
		block, err := r.ReadSamples()
		samples = append(samples, block...)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	spans, err := d.Process(samples)
	if err != nil {
		t.Fatal(err)
	}
	voices := map[int]bool{}
	for _, span := range spans {
		voices[span.Speaker] = true
	}
	if len(voices) != 2 {
		t.Fatalf("synthetic system voices = %d, spans=%+v", len(voices), spans)
	}
	if len(spans) < 2 {
		t.Fatal("no speaker boundaries")
	}
	for _, span := range spans {
		if len(span.Embedding) == 0 {
			t.Fatal("clean synthetic voice has no embedding")
		}
	}
	// Exercise the actual Rename binding's sample extraction, activity-DB
	// provenance and a second call's matcher with these real local models.
	priorPaths := callSpeakerModelPaths
	callSpeakerModelPaths = func() sherpaPaths {
		return sherpaPaths{segmentation: filepath.Join(dir, "segmentation.onnx"), speakerEmbd: filepath.Join(dir, "embedding.onnx")}
	}
	t.Cleanup(func() { callSpeakerModelPaths = priorPaths })
	app := newMeetingNotesTestApp(t)
	stubMeetingNotesLearn(t)
	start := time.Now().Add(-time.Hour).Truncate(time.Second)
	u := []meetingUtterance{{At: start.Add(2 * time.Second), End: start.Add(4 * time.Second), Source: "microphone", Text: "I will send it.", SpeakerID: "you"}}
	for _, span := range spans {
		u = append(u, meetingUtterance{At: start.Add(span.Start), End: start.Add(span.End), Source: "system_audio", Text: "Synthetic remote speech.", SpeakerID: fmt.Sprintf("speaker-%d", span.Speaker+2), Embedding: span.Embedding})
	}
	first := saveSyntheticCall(t, app, "native-first", "native-one", u)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	rel := fmt.Sprintf("%d-native01.wav", start.UnixMilli())
	if err := os.WriteFile(filepath.Join(app.activity.store.AudioDir(), rel), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := app.activity.store.db.Exec(`UPDATE activity_sessions SET audio_path=? WHERE id=?`, rel, first.ID); err != nil {
		t.Fatal(err)
	}
	if err := writeMeetingAudioMeta(meetingWAVMetaPath(app.activity.store.AudioDir(), rel), meetingAudioMeta{App: "Zoom", ActivitySessions: []string{first.ID}}); err != nil {
		t.Fatal(err)
	}
	view, err := app.GetMeetingCallTurns(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	id := ""
	for _, turn := range view.Turns {
		if turn.Source == "system_audio" {
			id = turn.SpeakerID
			break
		}
	}
	if err := app.RenameMeetingSpeaker("native-one", id, "Lin Chen"); err != nil {
		t.Fatal(err)
	}
	refs, err := app.activity.store.callSignatures()
	if err != nil || len(refs) != 1 || refs[0].Key != "native-one" {
		t.Fatalf("native Rename did not teach a local signature: %d %v", len(refs), err)
	}
	second := saveSyntheticCall(t, app, "native-second", "native-two", u)
	secondView, err := app.GetMeetingCallTurns(second.ID)
	if err != nil {
		t.Fatal(err)
	}
	matched, unknown := 0, 0
	for _, turn := range secondView.Turns {
		if turn.Source == "system_audio" {
			if turn.Name == "Lin Chen" {
				matched++
			} else {
				unknown++
			}
		}
	}
	if matched == 0 || unknown == 0 {
		t.Fatalf("native voice matching confused the two voices: matched=%d unknown=%d", matched, unknown)
	}
	// The same action also clears old explicit enrollments and their cache.
	self, err := app.EnsureSelfProfile("Test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.store.CreateVoiceEmbedding(bindingsCtx, storage.VoiceEmbeddingRow{PrincipalID: self.ID, Embedding: []float32{1, 0}}); err != nil {
		t.Fatal(err)
	}
	if err := app.ForgetVoices(); err != nil {
		t.Fatal(err)
	}
	refs, _ = app.activity.store.callSignatures()
	if len(refs) != 0 {
		t.Fatal("native signatures survived Forget")
	}
	enrolled, err := app.ListVoiceSamples(self.ID)
	if err != nil || len(enrolled) != 0 {
		t.Fatal("explicit voice enrollment survived Forget")
	}
}
