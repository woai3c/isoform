//go:build !bindings && darwin && (amd64 || arm64) && !ios

package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMeetingRecordingNativeASREndToEnd plays a scripted two-person call through the
// real pipeline: two macOS voices on the microphone and system-audio tracks
// (with the other side leaking into the microphone, as on speakers), cut
// into the recorder's 5-second chunks, written by the meeting audio writer
// with one system chunk held back by the gate, then transcribed by the real
// SenseVoice model. It logs the transcript the old chunk-by-chunk path
// produces next to the new one and checks the new one keeps every turn,
// in order, with the right side.
//
// It needs the speech models and macOS `say`:
//
//	YANSU_MEETING_E2E_MODELS=$HOME/.yansu-agent/sherpa [YANSU_MEETING_E2E_QWEN3=<qwen3 folder>] \
//	go test -run TestMeetingRecordingNativeASREndToEnd -count=1 -v .
func TestMeetingRecordingNativeASREndToEnd(t *testing.T) {
	models := os.Getenv("YANSU_MEETING_E2E_MODELS")
	if models == "" {
		t.Skip("set YANSU_MEETING_E2E_MODELS to a folder with sense-voice/ and silero_vad.onnx")
	}
	if _, err := exec.LookPath("say"); err != nil {
		t.Skip("needs macOS say")
	}
	transcriber, err := newSherpaTranscriber(sherpaPaths{
		root:       models,
		senseVoice: filepath.Join(models, "sense-voice", "model.int8.onnx"),
		tokens:     filepath.Join(models, "sense-voice", "tokens.txt"),
		silero:     filepath.Join(models, "silero_vad.onnx"),
	}, []string{"en", "zh", "yue"}, "auto")
	if err != nil {
		t.Fatal(err)
	}
	defer transcriber.Stop()
	// The after-call pass runs on Qwen3-ASR once its model is downloaded;
	// YANSU_MEETING_E2E_QWEN3=<model folder> checks the call with it.
	afterCall := transcriber
	if dir := os.Getenv("YANSU_MEETING_E2E_QWEN3"); dir != "" {
		afterCall, err = newQwen3ASRTranscriber(qwen3ASRPathsIn(dir), filepath.Join(models, "silero_vad.onnx"))
		if err != nil {
			t.Fatal(err)
		}
		defer afterCall.Stop()
	}

	type turn struct {
		me   bool
		text string
	}
	script := []turn{
		{true, "Thanks for joining. I want to walk through the recorder changes today."},
		{false, "Sure. Last week the transcripts came out in tiny pieces, and most of the call was missing."},
		{true, "Right. Now the whole call is recorded as one file, and we transcribe it again when the call ends."},
		{false, "Does that mean the timestamps follow the real pauses instead of five second chunks?"},
		{true, "Yes. The speech is cut where people stop talking, so words are no longer split in half."},
		{false, "Good. What about the notifications people complained about?"},
		{true, "They are gone. The notes go into memory quietly, and nothing pops up after a call."},
		{false, "Perfect. Let's ship it on Friday and check the first real meetings next week."},
	}

	dir := t.TempDir()
	second := meetingAudioSampleRate
	var mic, sys []int16
	pad := func(track []int16, n int) []int16 { return append(track, make([]int16, n)...) }
	mic = pad(mic, second)
	sys = pad(sys, second)
	for i, line := range script {
		voice := "Daniel"
		if line.me {
			voice = "Samantha"
		}
		speech := sayPCM(t, dir, fmt.Sprintf("turn-%d", i), voice, line.text)
		silence := make([]int16, len(speech))
		if line.me {
			mic = append(mic, speech...)
			sys = append(sys, silence...)
		} else {
			sys = append(sys, speech...)
			mic = append(mic, silence...)
		}
		gap := 800 * second / 1000
		if i == 3 {
			// A long quiet stretch mid-call: nobody talks for 12 seconds.
			gap = 12 * second
		}
		mic = pad(mic, gap)
		sys = pad(sys, gap)
	}
	// The other side leaks into the microphone through the speakers.
	for i := range mic {
		v := int32(mic[i]) + int32(sys[i])/4
		if v > 32767 {
			v = 32767
		}
		if v < -32768 {
			v = -32768
		}
		mic[i] = int16(v)
	}

	chunkSamples := int(backgroundAudioChunkDuration/time.Second) * second
	t0 := time.Date(2026, 10, 15, 14, 0, 0, 0, time.Local)
	writer, err := newMeetingAudioWriter(dir)
	if err != nil {
		t.Fatal(err)
	}
	var before []string
	heldBack := 0
	for start, index := 0, 0; start < len(mic); start, index = start+chunkSamples, index+1 {
		end := start + chunkSamples
		if end > len(mic) {
			end = len(mic)
		}
		capturedAt := t0.Add(time.Duration(end) * time.Second / time.Duration(second))
		micChunk := capturedAudioChunk{Source: backgroundListeningSourceMicrophone, WavData: realWAV(mic[start:end]), CapturedAt: capturedAt}
		sysChunk := capturedAudioChunk{Source: backgroundListeningSourceSystem, WavData: realWAV(sys[start:end]), CapturedAt: capturedAt}

		// What the live path made of this chunk: one line per chunk and side.
		for _, chunk := range []capturedAudioChunk{micChunk, sysChunk} {
			text, err := transcriber.TranscribeBackground(chunk.WavData, string(chunk.Source))
			if err != nil {
				t.Fatal(err)
			}
			if text = strings.TrimSpace(text); text != "" && !shouldDropBackgroundTranscript(text) {
				before = append(before, fmt.Sprintf("[%s] %s: %s", chunk.CapturedAt.Add(-backgroundAudioChunkDuration).Format("15:04:05"), speakerLabel(string(chunk.Source)), text))
			}
		}

		writer.WriteCapturedChunk(micChunk)
		if wavChunkRMS(sysChunk.WavData) < 1 && heldBack == 0 && index > 2 {
			// The privacy gate held a silent system chunk back.
			heldBack++
			continue
		}
		writer.WriteCapturedChunk(sysChunk)
	}
	if heldBack == 0 {
		t.Fatal("the script has no silent system chunk to hold back")
	}
	relPath, _, err := writer.Finalize()
	if err != nil {
		t.Fatal(err)
	}

	started := time.Now()
	utterances, err := transcribeMeetingRecording(afterCall, filepath.Join(dir, relPath))
	if err != nil {
		t.Fatal(err)
	}
	session := ActivitySessionRow{ID: "e2e", StartTime: t0.UnixMilli(), EndTime: t0.Add(time.Duration(len(mic)) * time.Second / time.Duration(second)).UnixMilli()}
	after, words := buildMeetingTranscriptMarkdown("Zoom", session, utterances, nil)
	t.Logf("call length %s, recording transcribed in %s", time.Duration(len(mic))*time.Second/time.Duration(second), time.Since(started).Round(time.Millisecond))
	t.Logf("BEFORE (live 5-second chunks, %d lines):\n%s", len(before), strings.Join(before, "\n"))
	t.Logf("AFTER (whole recording, %d words):\n%s", words, stripTranscriptHeader(after))

	paragraphs := buildTranscriptParagraphs(utterances)
	if len(paragraphs) != len(script) {
		t.Fatalf("paragraphs = %d, want one per turn (%d)", len(paragraphs), len(script))
	}
	for i, line := range script {
		wantSource := string(backgroundListeningSourceSystem)
		if line.me {
			wantSource = string(backgroundListeningSourceMicrophone)
		}
		if paragraphs[i].Source != wantSource {
			t.Errorf("turn %d is on %s, want %s: %q", i, paragraphs[i].Source, wantSource, paragraphs[i].Text)
		}
		if recall := transcriptTokenContainment(line.text, paragraphs[i].Text); recall < 0.8 {
			t.Errorf("turn %d kept %.0f%% of its words: %q", i, recall*100, paragraphs[i].Text)
		}
	}
}

// realWAV is makeWAV with a real header, for the speech engine.
func realWAV(samples []int16) []byte {
	wav := makeWAV(samples)
	var header bytes.Buffer
	_ = writeWAVHeader(&header, uint32(len(samples)*2))
	copy(wav, header.Bytes())
	return wav
}

// sayPCM speaks text with a macOS voice and returns 16 kHz mono samples.
func sayPCM(t *testing.T, dir, name, voice, text string) []int16 {
	t.Helper()
	path := filepath.Join(dir, name+".wav")
	cmd := exec.Command("say", "-v", voice, "-o", path, "--file-format=WAVE", "--data-format=LEI16@16000", text)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("say: %v %s", err, out)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	samples, rate, err := decodeWAVMonoFloat(raw)
	if err != nil || rate != meetingAudioSampleRate {
		t.Fatalf("say output: rate=%d err=%v", rate, err)
	}
	out := make([]int16, len(samples))
	for i, s := range samples {
		out[i] = int16(s * 32767)
	}
	return out
}
