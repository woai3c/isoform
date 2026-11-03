package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/asr"
	"wails-gui/internal/platform/activitymon"
)

func speechEvent(ms int64, source, text string) activitymon.ActivityEvent {
	return activitymon.ActivityEvent{Kind: activitymon.EventSpeech, Timestamp: ms, SpeechSource: source, SpeechText: text}
}

func transcriptLines(markdown string) []string {
	var lines []string
	for _, line := range strings.Split(stripTranscriptHeader(markdown), "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func TestBuildTranscriptMarkdownMergesFragmentsIntoTurns(t *testing.T) {
	base := time.Date(2026, 10, 15, 14, 0, 0, 0, time.Local).UnixMilli()
	session := ActivitySessionRow{ID: "s-turns", StartTime: base, EndTime: base + 60_000}
	events := []activitymon.ActivityEvent{
		speechEvent(base, "microphone", "So the plan for"),
		speechEvent(base+5_000, "microphone", "this week is to"),
		speechEvent(base+10_000, "microphone", "ship the recorder."),
		// Out of order on purpose: transcription finishes out of order.
		speechEvent(base+25_000, "system_audio", "and the notifications?"),
		speechEvent(base+20_000, "system_audio", "Sounds good,"),
		speechEvent(base+40_000, "microphone", "Those go away."),
	}

	body, words := buildTranscriptMarkdown("Zoom", session, events)
	lines := transcriptLines(body)
	want := []string{
		"[14:00:00] Me: So the plan for this week is to ship the recorder.",
		"[14:00:20] Other: Sounds good, and the notifications?",
		"[14:00:40] Me: Those go away.",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("turns:\n%s\nwant:\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
	if words != 19 {
		t.Fatalf("words = %d, want 19", words)
	}
}

func TestBuildTranscriptMarkdownDropsLabelsWhenOnlyOneSideSpoke(t *testing.T) {
	base := time.Date(2026, 10, 15, 14, 0, 0, 0, time.Local).UnixMilli()
	session := ActivitySessionRow{ID: "s-solo", StartTime: base, EndTime: base + 60_000}
	events := []activitymon.ActivityEvent{
		speechEvent(base, "microphone", "One idea is"),
		speechEvent(base+5_000, "microphone", "text messages."),
		speechEvent(base+30_000, "microphone", "Another is telemetry."),
	}
	body, _ := buildTranscriptMarkdown("Zoom", session, events)
	lines := transcriptLines(body)
	want := []string{
		"[14:00:00] One idea is text messages.",
		"[14:00:30] Another is telemetry.",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("lines:\n%s", strings.Join(lines, "\n"))
	}
}

func TestBuildTranscriptParagraphsBreaksLongMonologues(t *testing.T) {
	start := time.Date(2026, 10, 15, 14, 0, 0, 0, time.Local)
	var utterances []meetingUtterance
	for i := 0; i < 30; i++ {
		at := start.Add(time.Duration(i) * 5 * time.Second)
		utterances = append(utterances, meetingUtterance{At: at, End: at.Add(5 * time.Second), Source: "microphone", Text: "word"})
	}
	paragraphs := buildTranscriptParagraphs(utterances)
	if len(paragraphs) != 2 {
		t.Fatalf("a 150-second monologue = %d paragraphs, want 2", len(paragraphs))
	}
	if got := paragraphs[1].At.Sub(start); got != meetingParagraphSpan {
		t.Fatalf("second paragraph starts at %s, want %s", got, meetingParagraphSpan)
	}
}

func TestJoinTranscriptTextKeepsCJKTogether(t *testing.T) {
	if got := joinTranscriptText("新一版的严肃也要来了。", "第一件事"); got != "新一版的严肃也要来了。第一件事" {
		t.Fatalf("CJK join = %q", got)
	}
	if got := joinTranscriptText("we learn", "a lot"); got != "we learn a lot" {
		t.Fatalf("latin join = %q", got)
	}
	if got := joinTranscriptText("我觉得", "app of apps"); got != "我觉得 app of apps" {
		t.Fatalf("mixed join = %q", got)
	}
}

func TestDropMicrophoneEchoRemovesSpeakerLeak(t *testing.T) {
	start := time.Date(2026, 10, 15, 14, 0, 0, 0, time.Local)
	at := func(sec int) time.Time { return start.Add(time.Duration(sec) * time.Second) }
	system := []meetingUtterance{{At: at(10), End: at(14), Source: "system_audio", Text: "Can you send the deck by Friday?"}}
	mic := []meetingUtterance{
		{At: at(10), End: at(14), Source: "microphone", Text: "can you send the deck by friday"},
		{At: at(15), End: at(17), Source: "microphone", Text: "Yes, I will send it tonight."},
		{At: at(40), End: at(42), Source: "microphone", Text: "Can you send the deck by Friday?"},
	}
	kept := dropMicrophoneEcho(mic, system)
	if len(kept) != 2 || kept[0].Text != "Yes, I will send it tonight." || !kept[1].At.Equal(at(40)) {
		t.Fatalf("kept = %+v", kept)
	}
	if got := transcriptTokenContainment("我们一起做", "我们一起做严肃"); got != 1 {
		t.Fatalf("CJK containment = %v, want 1", got)
	}
}

func TestDropMicrophoneEchoKeepsShortReplies(t *testing.T) {
	start := time.Date(2026, 10, 15, 14, 0, 0, 0, time.Local)
	at := func(sec int) time.Time { return start.Add(time.Duration(sec) * time.Second) }
	system := []meetingUtterance{{At: at(10), End: at(14), Source: "system_audio", Text: "Yes, we can ship it Friday."}}
	mic := []meetingUtterance{
		{At: at(11), End: at(12), Source: "microphone", Text: "Yes."},
		{At: at(12), End: at(13), Source: "microphone", Text: "我们可以"},
		{At: at(12), End: at(14), Source: "microphone", Text: "ship it Friday then"},
	}
	kept := dropMicrophoneEcho(mic, system)
	if len(kept) != 3 {
		t.Fatalf("short replies and partial overlaps must stay: %+v", kept)
	}
}

func TestDropMicrophoneEchoUsesOnlyTheWordsAroundTheReply(t *testing.T) {
	start := time.Date(2026, 10, 15, 14, 0, 0, 0, time.Local)
	at := func(sec int) time.Time { return start.Add(time.Duration(sec) * time.Second) }
	// One long system segment: half a minute of the other side talking.
	system := []meetingUtterance{{At: at(0), End: at(30), Source: "system_audio",
		Text: "good morning everyone thanks for joining let's go through the weekly numbers first then the launch plan " +
			"and finally the hiring update I think we should send it soon so the team has time to review it properly"}}
	mic := []meetingUtterance{
		// The user's own reply at the start, made of words the other side
		// says only at the end.
		{At: at(1), End: at(3), Source: "microphone", Text: "I think we should send it soon"},
		// The speakers leaking the end of the segment into the microphone.
		{At: at(18), End: at(27), Source: "microphone", Text: "I think we should send it soon so the team has time"},
	}
	kept := dropMicrophoneEcho(mic, system)
	if len(kept) != 1 || !kept[0].At.Equal(at(1)) {
		t.Fatalf("only the leak where those words were said is echo: %+v", kept)
	}
}

func timedChunk(source backgroundListeningSource, end time.Time, samples []int16) capturedAudioChunk {
	return capturedAudioChunk{CaptureID: nextAudioCaptureIdentity(), Source: source, WavData: makeWAV(samples), CapturedAt: end}
}

func constSamples(n int, v int16) []int16 {
	out := make([]int16, n)
	for i := range out {
		out[i] = v
	}
	return out
}

func TestMeetingAudioWriterAlignsTracksAndWritesStereo(t *testing.T) {
	dir := t.TempDir()
	writer, err := newMeetingAudioWriter(dir)
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 10, 15, 14, 0, 0, 0, time.Local)
	second := meetingAudioSampleRate
	// Microphone: seconds 0-2 and 2-4. System audio: seconds 0-2, then
	// nothing until 6-8 (the gate held two chunks back).
	writer.WriteCapturedChunk(timedChunk(backgroundListeningSourceMicrophone, t0.Add(2*time.Second), constSamples(2*second, 100)))
	writer.WriteCapturedChunk(timedChunk(backgroundListeningSourceSystem, t0.Add(2*time.Second), constSamples(2*second, 200)))
	writer.WriteCapturedChunk(timedChunk(backgroundListeningSourceMicrophone, t0.Add(4*time.Second), constSamples(2*second, 101)))
	writer.WriteCapturedChunk(timedChunk(backgroundListeningSourceSystem, t0.Add(8*time.Second), constSamples(2*second, 201)))

	relPath, _, err := writer.Finalize()
	if err != nil || relPath == "" {
		t.Fatalf("Finalize: %q %v", relPath, err)
	}
	started, ok := meetingAudioStartFromPath(relPath)
	if !ok || !started.Equal(t0) {
		t.Fatalf("start from %q = %v, want %v", relPath, started, t0)
	}
	path := filepath.Join(dir, relPath)
	info, err := readMeetingWAVInfo(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.channels != 2 || info.dataSize != int64(8*second*4) {
		t.Fatalf("info = %+v, want stereo 8s", info)
	}

	read := func(channel int) []float32 {
		r, err := openMeetingWAVChannel(path, info, channel)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		var all []float32
		for {
			block, err := r.ReadSamples()
			all = append(all, block...)
			if errors.Is(err, io.EOF) {
				return all
			}
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	value := func(samples []float32, sec int) int16 {
		return int16(samples[sec*second+second/2] * 32768)
	}
	mic, sys := read(0), read(1)
	if len(mic) != 8*second || len(sys) != 8*second {
		t.Fatalf("channel lengths %d/%d", len(mic), len(sys))
	}
	if value(mic, 1) != 100 || value(mic, 3) != 101 || value(mic, 5) != 0 {
		t.Fatalf("mic track misplaced: %d %d %d", value(mic, 1), value(mic, 3), value(mic, 5))
	}
	if value(sys, 1) != 200 || value(sys, 3) != 0 || value(sys, 5) != 0 || value(sys, 7) != 201 {
		t.Fatalf("system track not aligned: %d %d %d %d", value(sys, 1), value(sys, 3), value(sys, 5), value(sys, 7))
	}
	writer.RemoveFinalized(relPath)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("RemoveFinalized left %s", path)
	}
}

func TestMeetingAudioWriterIgnoresJitter(t *testing.T) {
	dir := t.TempDir()
	writer, err := newMeetingAudioWriter(dir)
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 10, 15, 14, 0, 0, 0, time.Local)
	second := meetingAudioSampleRate
	writer.WriteCapturedChunk(timedChunk(backgroundListeningSourceMicrophone, t0.Add(5*time.Second), constSamples(5*second, 1)))
	// Handed over 300 ms late: no silence goes in.
	writer.WriteCapturedChunk(timedChunk(backgroundListeningSourceMicrophone, t0.Add(10300*time.Millisecond), constSamples(5*second, 1)))
	relPath, size, err := writer.Finalize()
	if err != nil {
		t.Fatal(err)
	}
	if size != int64(wavHeaderSize+10*second*2) {
		t.Fatalf("mono size = %d, want %d (%s)", size, wavHeaderSize+10*second*2, relPath)
	}
}

// fakeLongForm reports one segment per run of non-zero samples, with the
// run's first sample value as its text.
type fakeLongForm struct {
	texts map[int16]string
}

func (f fakeLongForm) TranscribeLongForm(r asr.SampleReader, source string) ([]asr.Segment, error) {
	var segments []asr.Segment
	var pos int
	inRun := false
	var runStart int
	var runValue int16
	for {
		block, err := r.ReadSamples()
		for _, s := range block {
			v := int16(s * 32768)
			if v != 0 && !inRun {
				inRun, runStart, runValue = true, pos, v
			} else if v == 0 && inRun {
				inRun = false
				segments = append(segments, asr.Segment{Start: offsetOf(runStart), End: offsetOf(pos), Text: f.texts[runValue]})
			}
			pos++
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	if inRun {
		segments = append(segments, asr.Segment{Start: offsetOf(runStart), End: offsetOf(pos), Text: f.texts[runValue]})
	}
	return segments, nil
}

func offsetOf(sample int) time.Duration {
	return time.Duration(sample) * time.Second / meetingAudioSampleRate
}

func writeStereoMeetingWAV(t *testing.T, dir string, start time.Time, mic, sys []int16) string {
	t.Helper()
	path := filepath.Join(dir, "2026-10-15", fmt.Sprintf("%d-abcd1234.wav", start.UnixMilli()))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := writeWAVHeaderChannels(f, uint32(len(mic)*4), 2); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	for i := range mic {
		binary.LittleEndian.PutUint16(buf[0:], uint16(mic[i]))
		binary.LittleEndian.PutUint16(buf[2:], uint16(sys[i]))
		if _, err := f.Write(buf); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestTranscribeMeetingRecordingLabelsSidesAndDropsEcho(t *testing.T) {
	dir := t.TempDir()
	start := time.Date(2026, 10, 15, 14, 0, 0, 0, time.Local)
	second := meetingAudioSampleRate
	mic := make([]int16, 12*second)
	sys := make([]int16, 12*second)
	fill := func(track []int16, from, to int, v int16) {
		for i := from * second; i < to*second; i++ {
			track[i] = v
		}
	}
	fill(mic, 1, 3, 1)  // the user
	fill(sys, 4, 7, 2)  // the other side
	fill(mic, 4, 7, 3)  // the other side again, through the speakers
	fill(mic, 9, 11, 4) // the user answers
	path := writeStereoMeetingWAV(t, dir, start, mic, sys)

	transcriber := fakeLongForm{texts: map[int16]string{
		1: "Shall we start with the recorder?",
		2: "Yes, the recorder first.",
		3: "yes the recorder first",
		4: "Great.",
	}}
	utterances, err := transcribeMeetingRecording(transcriber, path)
	if err != nil {
		t.Fatal(err)
	}
	session := ActivitySessionRow{ID: "s-audio", StartTime: start.UnixMilli(), EndTime: start.Add(12 * time.Second).UnixMilli()}
	body, _ := buildMeetingTranscriptMarkdown("Zoom", session, utterances, nil)
	lines := transcriptLines(body)
	want := []string{
		"[14:00:01] Me: Shall we start with the recorder?",
		"[14:00:04] Other: Yes, the recorder first.",
		"[14:00:09] Me: Great.",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("transcript:\n%s\nwant:\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
}

func TestWholeCallAudioKeepsOpeningBeforeFinalActivitySlice(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app := mainChatTestApp(t)
	store := newLifecycleStore(t)
	app.activity = &activityMonitorState{store: store}
	app.whisper = &whisperState{server: &recordingFixtureEngine{fakeLongForm: fakeLongForm{texts: map[int16]string{2000: "Opening before rotation.", 3000: "Closing after rotation."}}}}
	start := time.Date(2026, 10, 15, 14, 0, 0, 0, time.Local)
	mic, sys := make([]int16, 42*meetingAudioSampleRate), make([]int16, 42*meetingAudioSampleRate)
	copy(mic[meetingAudioSampleRate:], constSamples(meetingAudioSampleRate, 2000))
	copy(sys[40*meetingAudioSampleRate:], constSamples(meetingAudioSampleRate, 3000))
	path := writeStereoMeetingWAV(t, store.AudioDir(), start, mic, sys)
	rel, err := filepath.Rel(store.AudioDir(), path)
	if err != nil {
		t.Fatal(err)
	}
	got, complete := app.meetingAudioUtterances(ActivitySessionRow{ID: "whole-call-opening", AudioPath: rel, StartTime: start.Add(30 * time.Second).UnixMilli(), EndTime: start.Add(42 * time.Second).UnixMilli()})
	if !complete || len(got) != 2 || got[0].Text != "Opening before rotation." || got[1].Text != "Closing after rotation." {
		t.Fatalf("full recording was clipped to final activity slice: %+v complete=%v", got, complete)
	}
}

func TestAfterCallTranscriberWithoutSpeechEngine(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	transcriber, release := (&App{}).afterCallTranscriber()
	defer release()
	if transcriber != nil {
		t.Fatalf("no speech engine, got %T", transcriber)
	}
}

func TestCallSpeechEventsLeavesDictationOutOfTheCall(t *testing.T) {
	speech := func(text string, inCall bool, dictatedTo string) activitymon.ActivityEvent {
		return activitymon.ActivityEvent{Kind: activitymon.EventSpeech, SpeechText: text, SpeechInCall: inCall, SpeechDictationApp: dictatedTo}
	}
	events := []activitymon.ActivityEvent{
		speech("Let's start the review.", true, ""),
		speech("draft a reply to Sam", false, "Claude"),
		{Kind: activitymon.EventClick},
	}
	got := callSpeechEvents(events)
	if len(got) != 2 || got[0].SpeechText != "Let's start the review." || got[1].Kind != activitymon.EventClick {
		t.Fatalf("only the call's speech stays: %+v", got)
	}
	// A row saved before calls were told apart: all its speech is the call's.
	legacy := []activitymon.ActivityEvent{speech("Hello everyone.", false, ""), speech("Next item.", false, "")}
	if got := callSpeechEvents(legacy); len(got) != 2 {
		t.Fatalf("legacy speech is the call's: %+v", got)
	}
}
