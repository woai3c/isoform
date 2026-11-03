package main

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"wails-gui/internal/pii"
)

const wavHeaderSize = 44

// meetingAudioBytesPerSecond is the PCM rate of both recorders: 16 kHz,
// mono, 16-bit.
const meetingAudioBytesPerSecond = 32000

// meetingAudioGapTolerance is how far a track may fall behind a chunk's
// start before the gap is filled with silence. Chunks that the privacy gate
// held back, a recorder restart, or the microphone switched off mid-call
// leave such gaps; recorder jitter stays well below it.
const meetingAudioGapTolerance = time.Second

// meetingAudioMaxGapFill bounds one silence fill. Chunk times carry the
// monotonic clock, so a gap is real time without chunks (a screen lock, the
// gate holding chunks back) and is filled however long it is; the bound only
// guards against a broken clock. Fills are sparse, so a long one costs no
// disk until the WAV is written.
const meetingAudioMaxGapFill = 6 * time.Hour

// meetingAudioWriter records a call as two time-aligned tracks, the
// microphone and the system audio, and writes them as one WAV when the call
// ends: stereo (left the microphone, right the system audio) when both were
// captured, mono otherwise. The file name starts with the wall-clock time of
// its first sample, so the call can be transcribed again later with real
// timestamps and with the two sides apart.
type meetingAudioWriter struct {
	mu           sync.Mutex
	micFile      *os.File
	sysFile      *os.File
	audioDir     string
	relDir       string // date subdir, e.g. "2026-04-15"
	prefix       string // "<timestampMs>-<uuid8>"
	id           string // the uuid8 part of prefix
	micBytes     int64
	sysBytes     int64
	seenCaptures map[uint64]struct{}
	// startAt is the wall-clock time of byte 0 on both tracks, set by the
	// first timed chunk.
	startAt   time.Time
	finalRel  string // the WAV Finalize wrote, relative to audioDir
	finalized bool   // the complete WAV is durable; failed paths are never commit targets
	meta      meetingAudioMeta
	closed    bool
	aborted   bool // an intentional discard cannot accept late provenance
	writeErr  bool // set on first write error; suppresses further writes
}

// meetingAudioMeta is the sidecar ("<prefix>.json") that names the call a
// recording belongs to. While recording it lets a recording a crash left as
// raw tracks be finalized at the next start (recoverMeetingAudioTracks);
// once finished it stays beside the WAV, so recovery keys the notes on the
// meeting and deleting a meeting's notes finds all of its audio.
type meetingAudioMeta struct {
	App              string                   `json:"app,omitempty"`
	BundleID         string                   `json:"bundleId,omitempty"`
	MeetingID        string                   `json:"meetingId,omitempty"`
	PendingAudio     bool                     `json:"pendingAudio,omitempty"`
	PendingSources   []pendingRecordingSource `json:"pendingSources,omitempty"`
	CaptureSource    activitySourceProvenance `json:"captureSource,omitempty"`
	ActivitySessions []string                 `json:"activitySessions,omitempty"` // all activity rotations that contributed audio
	StartMs          int64                    `json:"startMs,omitempty"`
	MonoSource       string                   `json:"monoSource,omitempty"` // retained through compression; empty for stereo/unknown legacy audio
	// SessionID is the durable recording owner, reserved before finalization.
	// PendingSources distinguishes uncommitted identities from missing committed rows.
	SessionID string `json:"sessionId,omitempty"`
	// Protected: a protected app was in front during the call, so its
	// session follows the protected rule (no transcript, no notes).
	Protected bool `json:"protected,omitempty"`
	// TranscribedBy names the engine that transcribed the recording itself:
	// meetingTranscribedByAfterCall (Qwen3-ASR) or meetingTranscribedByLive.
	// Storage keeps the WAV until the transcript is final (see
	// storageRecordingFinal).
	TranscribedBy string `json:"transcribedBy,omitempty"`
	// DecodeFailed notes a turns rebuild that failed to read this recording.
	// The turns view does not decode the same unchanged recording again until
	// a day passes, so a legacy call whose audio cannot be decoded is not
	// retried on every load.
	DecodeFailed *meetingAudioDecodeFailure `json:"decodeFailed,omitempty"`
}

// Engines in meetingAudioMeta.TranscribedBy.
const (
	meetingTranscribedByAfterCall = "after-call"
	meetingTranscribedByLive      = "live"
)

// meetingWAVMetaPath is the sidecar of a finished recording, whether the
// recording is still the WAV or storage compressed it (<name>.opus): both
// share the base name, so the sidecar never moves.
func meetingWAVMetaPath(audioDir, relPath string) string {
	return filepath.Join(audioDir, strings.TrimSuffix(relPath, filepath.Ext(relPath))+".json")
}

// recordMeetingTranscribedBy notes in a finished recording's sidecar which
// engine transcribed it.
func recordMeetingTranscribedBy(audioDir, relPath, engine string) {
	updateMeetingWAVMeta(audioDir, relPath, func(meta *meetingAudioMeta) bool {
		meta.TranscribedBy = engine
		return true
	})
}

// updateMeetingWAVMeta changes a finished recording's sidecar under
// meetingSidecarMu and writes it atomically when change reports a change,
// and reports whether it wrote. It changes only a sidecar that is there and
// parses cleanly. One it cannot read is left as it is, because rewriting it
// from an empty record would drop Protected, CaptureSource and
// ActivitySessions. A missing one is not created, because the transcript
// read gate treats a recording without one as closed when its transcript
// names no sources, and a record holding one field would open it.
func updateMeetingWAVMeta(audioDir, relPath string, change func(*meetingAudioMeta) bool) bool {
	if relPath == "" {
		return false
	}
	path := meetingWAVMetaPath(audioDir, relPath)
	meetingSidecarMu.Lock()
	defer meetingSidecarMu.Unlock()
	meta, present, err := readMeetingAudioMetaFile(path)
	if err != nil {
		log.Printf("[meeting-audio] sidecar of %s is unreadable, left unchanged: %v", relPath, err)
		return false
	}
	if !present || !change(&meta) {
		return false
	}
	if err := writeMeetingAudioMetaFile(path, meta); err != nil {
		log.Printf("[meeting-audio] sidecar of %s: %v", relPath, err)
		return false
	}
	return true
}

// readMeetingWAVMeta returns the sidecar of a finished recording, or an
// empty one.
func readMeetingWAVMeta(audioDir, relPath string) meetingAudioMeta {
	var meta meetingAudioMeta
	if relPath == "" {
		return meta
	}
	if raw, err := os.ReadFile(meetingWAVMetaPath(audioDir, relPath)); err == nil {
		_ = json.Unmarshal(raw, &meta)
	}
	return meta
}

// meetingAudioDecodeFailure is the sidecar record of a turns rebuild that
// failed to read the recording: the file's size and modification time at the
// failure, and when it failed.
type meetingAudioDecodeFailure struct {
	Size     int64 `json:"size"`
	ModMs    int64 `json:"modMs"`
	FailedMs int64 `json:"failedMs"`
}

// meetingAudioDecodeRetryAfter is how long a failed rebuild suppresses
// decoding the same unchanged recording again.
const meetingAudioDecodeRetryAfter = 24 * time.Hour

// meetingAudioDecodeFailedRecently reports whether rebuilding from this
// recording already failed on the same file — same size and modification
// time — less than meetingAudioDecodeRetryAfter ago.
func meetingAudioDecodeFailedRecently(audioDir, relPath string, now time.Time) bool {
	failed := recordedMeetingAudioDecodeFailure(audioDir, relPath)
	if failed == nil {
		return false
	}
	info, err := os.Stat(filepath.Join(audioDir, relPath))
	if err != nil {
		return false
	}
	return info.Size() == failed.Size &&
		info.ModTime().UnixMilli() == failed.ModMs &&
		now.Sub(time.UnixMilli(failed.FailedMs)) < meetingAudioDecodeRetryAfter
}

// meetingDecodeFailures keeps, while Isoform runs, a failed rebuild of a
// recording whose sidecar is missing or unreadable, so that recording is not
// decoded on every load either; updateMeetingWAVMeta never creates a sidecar
// to hold it. meetingDecodeNoteMu keeps a note and a clear of the same
// recording from crossing between the sidecar and this map.
var (
	meetingDecodeFailures sync.Map // absolute recording path -> *meetingAudioDecodeFailure
	meetingDecodeNoteMu   sync.Mutex
)

// recordedMeetingAudioDecodeFailure is the failed rebuild noted for a
// recording: on its sidecar, or in memory when it has no readable one.
func recordedMeetingAudioDecodeFailure(audioDir, relPath string) *meetingAudioDecodeFailure {
	if failed := readMeetingWAVMeta(audioDir, relPath).DecodeFailed; failed != nil {
		return failed
	}
	if remembered, ok := meetingDecodeFailures.Load(filepath.Join(audioDir, relPath)); ok {
		return remembered.(*meetingAudioDecodeFailure)
	}
	return nil
}

// noteMeetingAudioDecodeFailed records a failed rebuild on the recording's
// sidecar, or in memory when it has no readable one, keyed to the file's
// current size and modification time.
func noteMeetingAudioDecodeFailed(audioDir, relPath string, now time.Time) {
	if relPath == "" {
		return
	}
	info, err := os.Stat(filepath.Join(audioDir, relPath))
	if err != nil {
		return
	}
	failure := &meetingAudioDecodeFailure{
		Size:     info.Size(),
		ModMs:    info.ModTime().UnixMilli(),
		FailedMs: now.UnixMilli(),
	}
	key := filepath.Join(audioDir, relPath)
	meetingDecodeNoteMu.Lock()
	defer meetingDecodeNoteMu.Unlock()
	if updateMeetingWAVMeta(audioDir, relPath, func(meta *meetingAudioMeta) bool {
		meta.DecodeFailed = failure
		return true
	}) {
		meetingDecodeFailures.Delete(key)
		return
	}
	meetingDecodeFailures.Store(key, failure)
}

// clearMeetingAudioDecodeFailed drops the failure record once a rebuild
// succeeds, so the sidecar no longer carries it.
func clearMeetingAudioDecodeFailed(audioDir, relPath string) {
	meetingDecodeNoteMu.Lock()
	defer meetingDecodeNoteMu.Unlock()
	meetingDecodeFailures.Delete(filepath.Join(audioDir, relPath))
	updateMeetingWAVMeta(audioDir, relPath, func(meta *meetingAudioMeta) bool {
		if meta.DecodeFailed == nil {
			return false
		}
		meta.DecodeFailed = nil
		return true
	})
}

// keepMeetingWAVMeta moves a recording's sidecar from its tracks to its
// finished WAV.
func keepMeetingWAVMeta(audioDir, relPath, trackMetaPath string, meta meetingAudioMeta) {
	if err := writeMeetingAudioMeta(meetingWAVMetaPath(audioDir, relPath), meta); err != nil {
		log.Printf("[meeting-audio] sidecar of %s: %v", relPath, err)
		return // the old journal still finds the completed WAV after a crash
	}
	if trackMetaPath != meetingWAVMetaPath(audioDir, relPath) {
		os.Remove(trackMetaPath)
	}
}

// removeMeetingRecording removes a finished recording and its sidecar.
func removeMeetingRecording(audioDir, relPath string) error {
	if relPath == "" {
		return nil
	}
	// The recording goes first: one that cannot be removed keeps the sidecar
	// that names its privacy.
	if err := os.Remove(filepath.Join(audioDir, relPath)); err != nil && !os.IsNotExist(err) {
		return err
	}
	meetingDecodeFailures.Delete(filepath.Join(audioDir, relPath))
	return removeMeetingSidecar(meetingWAVMetaPath(audioDir, relPath))
}

// removeMeetingSidecar removes a sidecar under meetingSidecarMu, so a field
// change that read it before the removal cannot write it back after.
func removeMeetingSidecar(path string) error {
	meetingSidecarMu.Lock()
	defer meetingSidecarMu.Unlock()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func newMeetingAudioWriter(audioDir string) (*meetingAudioWriter, error) {
	now := time.Now()
	dateDir := now.Format("2006-01-02")
	dir := filepath.Join(audioDir, dateDir)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create audio dir: %w", err)
	}

	id := generateShortID()
	prefix := fmt.Sprintf("%d-%s", now.UnixMilli(), id)

	micPath := filepath.Join(dir, prefix+".mic.pcm")
	sysPath := filepath.Join(dir, prefix+".sys.pcm")

	micFile, err := os.OpenFile(micPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return nil, fmt.Errorf("create mic temp file: %w", err)
	}
	sysFile, err := os.OpenFile(sysPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		micFile.Close()
		os.Remove(micPath)
		return nil, fmt.Errorf("create sys temp file: %w", err)
	}

	return &meetingAudioWriter{
		micFile:  micFile,
		sysFile:  sysFile,
		audioDir: audioDir,
		relDir:   dateDir,
		prefix:   prefix,
		id:       id,
	}, nil
}

func generateShortID() string {
	b := make([]byte, 4)
	_, _ = io.ReadFull(rand.Reader, b)
	return fmt.Sprintf("%x", b)
}

// SetMeeting records which call the recording belongs to.
func (w *meetingAudioWriter) SetMeeting(app, bundleID, meetingID string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.meta.App = app
	w.meta.BundleID = bundleID
	w.meta.MeetingID = meetingID
	w.writeMetaLocked()
}

func (w *meetingAudioWriter) metaPath() string {
	return filepath.Join(w.audioDir, w.relDir, w.prefix+".json")
}

func (w *meetingAudioWriter) writeMetaLocked() {
	if w.closed {
		return
	}
	if err := writeMeetingAudioMeta(w.metaPath(), w.meta); err != nil {
		log.Printf("[meeting-audio] write sidecar: %v", err)
	}
}

// NoteActivitySession records provenance before a rotation releases the monitor
// lock. A terminal extraction may finish before the rotation's database write;
// transcript recovery retries once every contributing row is available.
func (w *meetingAudioWriter) NoteActivitySession(id string) {
	if w == nil || id == "" {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	for _, existing := range w.meta.ActivitySessions {
		if existing == id {
			return
		}
	}
	w.meta.ActivitySessions = append(w.meta.ActivitySessions, id)
	w.writeMetaLocked()
}

// WriteChunk appends a chunk to its source's track without any timing.
func (w *meetingAudioWriter) WriteChunk(source backgroundListeningSource, wavData []byte) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed || w.writeErr || len(wavData) <= wavHeaderSize {
		return
	}
	w.writeLocked(source, wavData[wavHeaderSize:])
}

// WriteCapturedChunk places a chunk on its source's track at the time it was
// recorded: a track that fell behind (chunks the gate held back, a recorder
// restart) is filled with silence first, so the two tracks stay aligned with
// each other and with the wall clock.
func (w *meetingAudioWriter) WriteCapturedChunk(chunk capturedAudioChunk) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed || w.writeErr || len(chunk.WavData) <= wavHeaderSize {
		return
	}
	if chunkProtected(chunk) && !w.meta.Protected {
		w.meta.Protected = true
		w.writeMetaLocked()
	}
	apps := uniqueNonEmpty(append(append([]string{}, w.meta.CaptureSource.Apps...), chunk.CapturedApp.BundleID, chunk.CapturedApp.AppName), strings.TrimSpace)
	private := w.meta.CaptureSource.Private || chunk.PrivateWindowInFront || privateBrowserWindow(chunk.CapturedApp) || chunkProtected(chunk)
	if len(apps) != len(w.meta.CaptureSource.Apps) || private != w.meta.CaptureSource.Private {
		w.meta.CaptureSource.Apps, w.meta.CaptureSource.Private = apps, private
		w.writeMetaLocked()
	}
	pcm := chunk.WavData[wavHeaderSize:]
	pcm = pcm[:len(pcm)/2*2]
	if len(pcm) == 0 {
		return
	}
	// An absent identity is not proof of replay. Distinct native reads can be
	// delivered almost together after a stall; never trim their unique PCM.
	if chunk.CaptureID != 0 {
		if w.seenCaptures == nil {
			w.seenCaptures = map[uint64]struct{}{}
		}
		if _, seen := w.seenCaptures[chunk.CaptureID]; seen {
			return
		}
		w.seenCaptures[chunk.CaptureID] = struct{}{}
	}
	if !chunk.CapturedAt.IsZero() {
		// CapturedAt is when the recorder handed the chunk over, the end of
		// its audio.
		duration := time.Duration(len(pcm)) * time.Second / meetingAudioBytesPerSecond
		chunkStart := chunk.CapturedAt.Add(-duration)

		if w.startAt.IsZero() {
			w.startAt = chunkStart
			w.meta.StartMs = chunkStart.UnixMilli()
			w.writeMetaLocked()
		}
		// Microseconds keep the product far from int64 overflow.
		want := int64(recordingElapsed(w.startAt, chunkStart)/time.Microsecond) * meetingAudioBytesPerSecond / 1_000_000
		want &^= 1
		have := w.trackBytesLocked(chunk.Source)
		tolerance := int64(meetingAudioGapTolerance) * meetingAudioBytesPerSecond / int64(time.Second)
		maxFill := int64(meetingAudioMaxGapFill) * meetingAudioBytesPerSecond / int64(time.Second)
		if gap := want - have; gap > maxFill {
			log.Printf("[meeting-audio] %s track gap of %s not filled", chunk.Source, time.Duration(gap)*time.Second/meetingAudioBytesPerSecond)
		} else if gap >= tolerance {
			w.writeSilenceLocked(chunk.Source, gap)
		}
	}
	w.writeLocked(chunk.Source, pcm)
}

// meetingSleepThreshold: wall time running this far ahead of the monotonic
// clock between two chunks means the Mac slept in between.
const meetingSleepThreshold = 2 * time.Second

// recordingElapsed is the time from start to at on the recording's
// timeline. The monotonic clock is immune to clock changes, but on macOS it
// stops while the Mac sleeps, so a closed lid would vanish from the
// recording; when wall time ran ahead of it by more than a moment, the
// wall clock is used.
func recordingElapsed(start, at time.Time) time.Duration {
	return pickRecordingElapsed(at.Sub(start), at.Round(0).Sub(start.Round(0)))
}

// pickRecordingElapsed chooses between the monotonic and the wall-clock
// elapsed time (see recordingElapsed).
func pickRecordingElapsed(monotonic, wall time.Duration) time.Duration {
	if wall-monotonic > meetingSleepThreshold {
		return wall
	}
	return monotonic
}

func (w *meetingAudioWriter) trackBytesLocked(source backgroundListeningSource) int64 {
	switch source {
	case backgroundListeningSourceSystem:
		return w.sysBytes
	default:
		return w.micBytes
	}
}

// writeSilenceLocked skips n bytes on the track: the file gets a hole,
// which reads back as zeros (silence) once the next chunk is written.
func (w *meetingAudioWriter) writeSilenceLocked(source backgroundListeningSource, n int64) {
	if w.writeErr || n <= 0 {
		return
	}
	target := w.micFile
	if source == backgroundListeningSourceSystem {
		target = w.sysFile
	}
	if _, err := target.Seek(n, io.SeekCurrent); err != nil {
		log.Printf("[meeting-audio] gap fill error (source=%s): %v", source, err)
		w.writeErr = true
		return
	}
	switch source {
	case backgroundListeningSourceSystem:
		w.sysBytes += n
	default:
		w.micBytes += n
	}
}

// chunkProtected reports whether a protected app was in front while the
// chunk was recorded.
func chunkProtected(chunk capturedAudioChunk) bool {
	if chunk.ProtectedAppInFront || chunk.FocusUnavailable || chunk.CapturedApp.FocusUnavailable {
		return true
	}
	_, sensitive := pii.IsSensitiveApp(chunk.CapturedApp.BundleID)
	return sensitive
}

// meetingRecordingChunk is the chunk as the call recording keeps it: audio
// captured while a protected app was in front becomes silence of the same
// length, so it never reaches disk and the tracks stay aligned.
func meetingRecordingChunk(chunk capturedAudioChunk) capturedAudioChunk {
	if !chunkProtected(chunk) || len(chunk.WavData) <= wavHeaderSize {
		return chunk
	}
	silent := make([]byte, len(chunk.WavData))
	copy(silent[:wavHeaderSize], chunk.WavData[:wavHeaderSize])
	chunk.WavData = silent
	return chunk
}

func (w *meetingAudioWriter) writeLocked(source backgroundListeningSource, pcm []byte) {
	if w.writeErr {
		return
	}
	var target *os.File
	switch source {
	case backgroundListeningSourceSystem:
		target = w.sysFile
	default:
		target = w.micFile
	}

	n, err := target.Write(pcm)
	if err != nil {
		log.Printf("[meeting-audio] write error (source=%s): %v", source, err)
		w.writeErr = true
		return
	}

	switch source {
	case backgroundListeningSourceSystem:
		w.sysBytes += int64(n)
	default:
		w.micBytes += int64(n)
	}
}

func (w *meetingAudioWriter) Finalize() (relPath string, sizeBytes int64, err error) {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return "", 0, nil
	}
	w.closed = true
	micFile := w.micFile
	sysFile := w.sysFile
	w.micFile = nil
	w.sysFile = nil
	micBytes := w.micBytes
	sysBytes := w.sysBytes
	w.meta.MonoSource = meetingMonoSource(micBytes, sysBytes)
	startAt := w.startAt
	w.mu.Unlock()

	micPath := micFile.Name()
	sysPath := sysFile.Name()
	micFile.Close()
	sysFile.Close()

	name := w.prefix
	if !startAt.IsZero() {
		name = fmt.Sprintf("%d-%s", startAt.UnixMilli(), w.id)
	}
	w.mu.Lock()
	w.finalRel = filepath.Join(w.relDir, name+".wav")
	w.mu.Unlock()
	relPath, sizeBytes, err = finalizeMeetingTracks(w.audioDir, w.relDir, name, micPath, sysPath, micBytes, sysBytes)
	if err == nil {
		// Serialize the sidecar move with late rotation privacy updates and
		// commits. Never publish a snapshot taken before those updates.
		w.mu.Lock()
		w.finalized = relPath != ""
		if relPath == "" {
			os.Remove(w.metaPath())
		} else if metaErr := w.persistMetaLocked(); metaErr != nil {
			log.Printf("[meeting-audio] final sidecar: %v", metaErr)
		}
		w.mu.Unlock()
	}
	return relPath, sizeBytes, err
}

func meetingMonoSource(micBytes, sysBytes int64) string {
	if micBytes > 0 && sysBytes == 0 {
		return "microphone"
	}
	if sysBytes > 0 && micBytes == 0 {
		return "system_audio"
	}
	return ""
}

// KeepForRecovery preserves the reserved owner after a failed Finalize.
// Pending source proof distinguishes an uncommitted owner from a missing
// committed row, so recovery attaches the original recording identity.
func (w *meetingAudioWriter) KeepForRecovery(sessionID string) {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.meta.SessionID = sessionID
	if err := writeMeetingAudioMeta(w.metaPath(), w.meta); err != nil {
		log.Printf("[meeting-audio] sidecar for recovery: %v", err)
	}
}

// finalizeMeetingTracks writes the two raw tracks as one WAV (stereo when
// both have audio) and removes the tracks only once the WAV is complete and
// synced: a failed write (a full disk) keeps them for the next start.
func finalizeMeetingTracks(audioDir, relDir, name, micPath, sysPath string, micBytes, sysBytes int64) (string, int64, error) {
	removeTracks := func() {
		os.Remove(micPath)
		os.Remove(sysPath)
	}
	if micBytes == 0 && sysBytes == 0 {
		removeTracks()
		return "", 0, nil
	}

	outRelPath := filepath.Join(relDir, name+".wav")
	outPath := filepath.Join(audioDir, outRelPath)

	outFile, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return "", 0, fmt.Errorf("create output WAV: %w", err)
	}

	channels := 1
	if micBytes > 0 && sysBytes > 0 {
		channels = 2
	}

	// Write placeholder header — updated after the data.
	if err := writeWAVHeaderChannels(outFile, 0, channels); err != nil {
		outFile.Close()
		os.Remove(outPath)
		return "", 0, fmt.Errorf("write WAV header: %w", err)
	}

	var totalPCMBytes int64

	if channels == 2 {
		totalPCMBytes, err = streamInterleave(micPath, sysPath, outFile)
	} else if micBytes > 0 {
		totalPCMBytes, err = streamCopy(micPath, outFile)
	} else {
		totalPCMBytes, err = streamCopy(sysPath, outFile)
	}

	if err != nil {
		outFile.Close()
		os.Remove(outPath)
		return "", 0, fmt.Errorf("write audio data: %w", err)
	}

	if totalPCMBytes == 0 {
		outFile.Close()
		os.Remove(outPath)
		removeTracks()
		return "", 0, nil
	}

	// Seek back and update the WAV header with the correct data size.
	if _, err := outFile.Seek(0, io.SeekStart); err != nil {
		outFile.Close()
		return outRelPath, totalPCMBytes + wavHeaderSize, fmt.Errorf("seek for header update: %w", err)
	}
	if err := writeWAVHeaderChannels(outFile, uint32(totalPCMBytes), channels); err != nil {
		outFile.Close()
		return outRelPath, totalPCMBytes + wavHeaderSize, fmt.Errorf("update WAV header: %w", err)
	}

	if err := outFile.Sync(); err != nil {
		outFile.Close()
		return outRelPath, totalPCMBytes + wavHeaderSize, fmt.Errorf("sync WAV: %w", err)
	}
	outFile.Close()
	removeTracks()

	return outRelPath, totalPCMBytes + wavHeaderSize, nil
}

// meetingAudioStartFromPath reads the wall-clock start of a meeting WAV from
// its name ("<startMs>-<id>.wav"). Recordings written before the tracks were
// timed carry the writer's creation time there, close to their start.
func meetingAudioStartFromPath(relPath string) (time.Time, bool) {
	base := filepath.Base(relPath)
	dash := strings.IndexByte(base, '-')
	if dash <= 0 {
		return time.Time{}, false
	}
	ms, err := strconv.ParseInt(base[:dash], 10, 64)
	if err != nil || ms <= 0 {
		return time.Time{}, false
	}
	return time.UnixMilli(ms), true
}

func (w *meetingAudioWriter) Abort() {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return
	}
	w.closed = true
	w.aborted = true

	if w.micFile != nil {
		name := w.micFile.Name()
		w.micFile.Close()
		os.Remove(name)
		w.micFile = nil
	}
	if w.sysFile != nil {
		name := w.sysFile.Name()
		w.sysFile.Close()
		os.Remove(name)
		w.sysFile = nil
	}
	os.Remove(w.metaPath())
}

// RemoveFinalized removes only the WAV path owned by this writer. Finalize can
// return a relative path together with a late seek/header/sync error; callers
// use this to avoid leaving a partial automatic meeting artifact behind.
func (w *meetingAudioWriter) RemoveFinalized(relPath string) {
	if w == nil || relPath == "" {
		return
	}
	w.mu.Lock()
	expected := w.finalRel
	w.mu.Unlock()
	if expected == "" || filepath.Clean(relPath) != filepath.Clean(expected) {
		return
	}
	if err := removeMeetingRecording(w.audioDir, expected); err != nil {
		log.Printf("[meeting-audio] failed to remove finalized artifact %s: %v", relPath, err)
	}
}

func writeWAVHeader(w io.Writer, dataSize uint32) error {
	return writeWAVHeaderChannels(w, dataSize, 1)
}

// writeWAVHeaderChannels writes a 16 kHz, 16-bit PCM header for the given
// channel count.
func writeWAVHeaderChannels(w io.Writer, dataSize uint32, channels int) error {
	if channels < 1 {
		channels = 1
	}
	blockAlign := uint16(2 * channels)
	fileSize := 36 + dataSize
	header := [wavHeaderSize]byte{}
	copy(header[0:4], "RIFF")
	binary.LittleEndian.PutUint32(header[4:8], fileSize)
	copy(header[8:12], "WAVE")
	copy(header[12:16], "fmt ")
	binary.LittleEndian.PutUint32(header[16:20], 16)                       // fmt chunk size
	binary.LittleEndian.PutUint16(header[20:22], 1)                        // PCM
	binary.LittleEndian.PutUint16(header[22:24], uint16(channels))         // channels
	binary.LittleEndian.PutUint32(header[24:28], 16000)                    // sample rate
	binary.LittleEndian.PutUint32(header[28:32], 16000*uint32(blockAlign)) // byte rate
	binary.LittleEndian.PutUint16(header[32:34], blockAlign)               // block align
	binary.LittleEndian.PutUint16(header[34:36], 16)                       // bits per sample
	copy(header[36:40], "data")
	binary.LittleEndian.PutUint32(header[40:44], dataSize)
	_, err := w.Write(header[:])
	return err
}

const mixBufSamples = 4096

// streamInterleave writes the two tracks as one stereo stream, the
// microphone left and the system audio right. The shorter track is padded
// with silence.
func streamInterleave(micPath, sysPath string, out *os.File) (int64, error) {
	micF, err := os.Open(micPath)
	if err != nil {
		return 0, err
	}
	defer micF.Close()

	sysF, err := os.Open(sysPath)
	if err != nil {
		return 0, err
	}
	defer sysF.Close()

	micBuf := make([]byte, mixBufSamples*2)
	sysBuf := make([]byte, mixBufSamples*2)
	outBuf := make([]byte, mixBufSamples*4)
	var totalBytes int64

	for {
		micN, micErr := io.ReadFull(micF, micBuf)
		sysN, sysErr := io.ReadFull(sysF, sysBuf)

		// Round down to sample boundary.
		micN = (micN / 2) * 2
		sysN = (sysN / 2) * 2

		if micN == 0 && sysN == 0 {
			break
		}

		frames := micN
		if sysN > frames {
			frames = sysN
		}
		frames /= 2

		for i := 0; i < frames; i++ {
			var m, s uint16
			if i*2+1 < micN {
				m = binary.LittleEndian.Uint16(micBuf[i*2:])
			}
			if i*2+1 < sysN {
				s = binary.LittleEndian.Uint16(sysBuf[i*2:])
			}
			binary.LittleEndian.PutUint16(outBuf[i*4:], m)
			binary.LittleEndian.PutUint16(outBuf[i*4+2:], s)
		}

		n, err := out.Write(outBuf[:frames*4])
		if err != nil {
			return totalBytes, err
		}
		totalBytes += int64(n)

		if (micErr != nil && micErr != io.ErrUnexpectedEOF) &&
			(sysErr != nil && sysErr != io.ErrUnexpectedEOF) {
			break
		}
	}

	return totalBytes, nil
}

func streamCopy(srcPath string, out *os.File) (int64, error) {
	src, err := os.Open(srcPath)
	if err != nil {
		return 0, err
	}
	defer src.Close()
	return io.Copy(out, src)
}
