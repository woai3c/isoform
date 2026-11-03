package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"wails-gui/internal/platform/cmdutil"
)

// Call recordings are kept compressed. The recorder writes 16 kHz PCM WAV
// (115 MB per hour mono, 230 MB stereo) because the after-call transcription
// reads it. Once a call's transcript is final (recordingFinal), the storage
// layer re-encodes the WAV as Opus (24 kbit/s per channel, the speech
// profile) through the bundled ffmpeg: about 11 MB per hour per channel,
// 10-14x smaller. A recording without a final transcript stays WAV however
// old it is, because the call pipeline may still transcribe it. Stereo
// recordings (left the microphone, right the computer) are encoded as two
// independent channels so each side can still be transcribed on its own.
// The row then points at the .opus file with the same base name, so the
// start time in the name still reads.
//
// Anything that needs PCM again (a later re-transcription) calls
// decodeMeetingAudioToWAV, which returns the WAV itself or a temporary
// decoded copy.
const (
	meetingAudioOpusExt               = ".opus"
	meetingAudioOpusBitratePerChannel = 24000
	// meetingAudioCompressedFactor estimates Opus bytes from WAV bytes:
	// 24 kbit/s against 256 kbit/s of 16 kHz PCM per channel is 0.094.
	meetingAudioCompressedFactor = 0.12
	// meetingAudioEncodeTimeout bounds one ffmpeg run on top of the
	// recording's own length (encoding runs far faster than real time).
	meetingAudioEncodeTimeout = 5 * time.Minute
)

// recordingTranscriptFinal is the default finality rule: the call has a
// transcript.
func recordingTranscriptFinal(m audioMedia) bool {
	return m.Transcribed
}

// audioCompressible reports whether the storage layer may compress a
// recording now: a WAV whose transcript is final.
func (r *StorageRotator) audioCompressible(m audioMedia) bool {
	return strings.EqualFold(filepath.Ext(m.RelPath), ".wav") && r.recordingFinal(m) && !r.audioSkip[m.RelPath]
}

// audioProjectedSize is the size a recording will take once compressed
// (its size when it will not be: not compressible, or no ffmpeg).
func (r *StorageRotator) audioProjectedSize(m audioMedia) int64 {
	if r.audioCompressible(m) && r.ffmpegPath() != "" {
		return int64(float64(m.Size) * meetingAudioCompressedFactor)
	}
	return m.Size
}

// wavFormat is the part of a WAV header the encoder needs.
type wavFormat struct {
	channels   int
	sampleRate int
	byteRate   int
	dataBytes  int64
	dataOffset int64 // where the samples start
}

// readWAVFormat reads the fmt and data chunk sizes of a RIFF/WAVE file.
func readWAVFormat(path string) (wavFormat, error) {
	f, err := os.Open(path)
	if err != nil {
		return wavFormat{}, err
	}
	defer f.Close()
	var riff [12]byte
	if _, err := io.ReadFull(f, riff[:]); err != nil {
		return wavFormat{}, fmt.Errorf("read wav header: %w", err)
	}
	if string(riff[0:4]) != "RIFF" || string(riff[8:12]) != "WAVE" {
		return wavFormat{}, fmt.Errorf("not a wav file")
	}
	var wf wavFormat
	for {
		var hdr [8]byte
		if _, err := io.ReadFull(f, hdr[:]); err != nil {
			if wf.channels > 0 {
				return wf, nil
			}
			return wavFormat{}, fmt.Errorf("read wav chunk: %w", err)
		}
		size := int64(binary.LittleEndian.Uint32(hdr[4:8]))
		switch string(hdr[0:4]) {
		case "fmt ":
			var fmtChunk [16]byte
			if size < 16 {
				return wavFormat{}, fmt.Errorf("short fmt chunk")
			}
			if _, err := io.ReadFull(f, fmtChunk[:]); err != nil {
				return wavFormat{}, fmt.Errorf("read fmt chunk: %w", err)
			}
			wf.channels = int(binary.LittleEndian.Uint16(fmtChunk[2:4]))
			wf.sampleRate = int(binary.LittleEndian.Uint32(fmtChunk[4:8]))
			wf.byteRate = int(binary.LittleEndian.Uint32(fmtChunk[8:12]))
			if _, err := f.Seek(size-16+size%2, io.SeekCurrent); err != nil {
				return wavFormat{}, err
			}
		case "data":
			wf.dataBytes = size
			if pos, err := f.Seek(0, io.SeekCurrent); err == nil {
				wf.dataOffset = pos
			}
			if wf.channels > 0 {
				return wf, nil
			}
			if _, err := f.Seek(size+size%2, io.SeekCurrent); err != nil {
				return wavFormat{}, err
			}
		default:
			if _, err := f.Seek(size+size%2, io.SeekCurrent); err != nil {
				return wavFormat{}, err
			}
		}
	}
}

// compressMeetingAudio encodes wavPath as Ogg Opus next to it (same base
// name, .opus) and returns the new file's path and size. The WAV is left in
// place; the caller removes it once the row points at the new file.
func compressMeetingAudio(ffmpegPath, wavPath string) (string, int64, error) {
	wf, err := readWAVFormat(wavPath)
	if err != nil {
		return "", 0, err
	}
	channels := max(1, wf.channels)
	outPath := strings.TrimSuffix(wavPath, filepath.Ext(wavPath)) + meetingAudioOpusExt
	tmpPath := outPath + ".tmp"
	args := []string{"-nostdin", "-v", "error", "-y", "-i", wavPath,
		"-c:a", "libopus", "-application", "voip", "-vbr", "on",
		"-b:a", fmt.Sprintf("%d", meetingAudioOpusBitratePerChannel*channels)}
	if channels > 1 {
		// Discrete channels: no stereo coupling between the microphone and
		// the computer side.
		args = append(args, "-mapping_family", "255")
	}
	args = append(args, "-f", "ogg", tmpPath)
	// The file length is the truth (a writer may leave the header's data size
	// short); the encoded audio must be that long.
	wavInfo, err := os.Stat(wavPath)
	if err != nil {
		return "", 0, err
	}
	want := 0.0
	if wf.byteRate > 0 {
		want = float64(wavInfo.Size()-wf.dataOffset) / float64(wf.byteRate)
	}
	timeout := meetingAudioEncodeTimeout + time.Duration(want*float64(time.Second))
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := lowPriorityCommand(ctx, ffmpegPath, args...).CombinedOutput()
	if err != nil {
		_ = os.Remove(tmpPath)
		return "", 0, fmt.Errorf("ffmpeg opus encode: %w: %s", err, strings.TrimSpace(string(out)))
	}
	info, err := os.Stat(tmpPath)
	if err != nil || info.Size() == 0 {
		_ = os.Remove(tmpPath)
		return "", 0, fmt.Errorf("ffmpeg wrote no audio")
	}
	// Read the result back once before trusting it with the only copy, and
	// check it holds the whole recording.
	got, err := decodedAudioSeconds(ctx, ffmpegPath, tmpPath)
	if err != nil {
		_ = os.Remove(tmpPath)
		return "", 0, err
	}
	if want > 0 && math.Abs(got-want) > math.Max(1, want*0.01) {
		_ = os.Remove(tmpPath)
		return "", 0, fmt.Errorf("compressed audio is %.1f s, the recording %.1f s", got, want)
	}
	if err := os.Rename(tmpPath, outPath); err != nil {
		_ = os.Remove(tmpPath)
		return "", 0, err
	}
	return outPath, info.Size(), nil
}

// ffmpegTimeRE matches the progress time ffmpeg prints while decoding.
var ffmpegTimeRE = regexp.MustCompile(`time=(\d+):(\d{2}):(\d{2}(?:\.\d+)?)`)

// decodedAudioSeconds decodes path to nothing and returns its length.
func decodedAudioSeconds(ctx context.Context, ffmpegPath, path string) (float64, error) {
	out, err := lowPriorityCommand(ctx, ffmpegPath, "-nostdin", "-hide_banner", "-i", path, "-f", "null", "-").CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("compressed audio does not decode: %w: %s", err, strings.TrimSpace(string(out)))
	}
	matches := ffmpegTimeRE.FindAllStringSubmatch(string(out), -1)
	if len(matches) == 0 {
		return 0, fmt.Errorf("compressed audio length unreadable")
	}
	last := matches[len(matches)-1]
	h, _ := strconv.Atoi(last[1])
	m, _ := strconv.Atoi(last[2])
	sec, _ := strconv.ParseFloat(last[3], 64)
	return float64(h*3600+m*60) + sec, nil
}

// decodeMeetingAudioToWAV returns a 16-bit PCM WAV of a call recording: the
// file itself when it is a WAV, otherwise a decoded copy in a temporary
// folder with the same base name (so the start time in the name still
// reads), channels kept. cleanup removes the copy and is never nil.
func decodeMeetingAudioToWAV(ffmpegPath, path string) (string, func(), error) {
	noop := func() {}
	if strings.EqualFold(filepath.Ext(path), ".wav") {
		return path, noop, nil
	}
	if ffmpegPath == "" {
		return "", noop, fmt.Errorf("ffmpeg is not installed")
	}
	dir, err := os.MkdirTemp("", "isoform-call-audio-")
	if err != nil {
		return "", noop, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)) + ".wav"
	wavPath := filepath.Join(dir, base)
	out, err := cmdutil.Command(ffmpegPath, "-nostdin", "-v", "error", "-y", "-i", path,
		"-ar", "16000", "-c:a", "pcm_s16le", "-bitexact", "-map_metadata", "-1", "-f", "wav", wavPath).CombinedOutput()
	if err != nil {
		cleanup()
		return "", noop, fmt.Errorf("ffmpeg decode: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return wavPath, cleanup, nil
}

// compressCallAudio compresses the WAV recordings audioIsCompressible
// allows, oldest first, until deadline passes or stop (when set) says so.
// Returns how many were compressed and the bytes saved. Without ffmpeg it
// does nothing.
func (r *StorageRotator) compressCallAudio(deadline time.Time, stop func() bool, progress func(done int)) (compressed int, saved int64) {
	ffmpeg := r.ffmpegPath()
	if ffmpeg == "" {
		return 0, 0
	}
	now := time.Now()
	sessions, err := r.store.sessionsWithAudio(now.UnixMilli())
	if err != nil {
		log.Printf("[activity-rotator] list call audio: %v", err)
		return 0, 0
	}
	done := 0
	for _, m := range sessions {
		if !time.Now().Before(deadline) || (stop != nil && stop()) {
			return
		}
		if !r.audioCompressible(m) {
			continue
		}
		n, ok := r.compressOneRecording(ffmpeg, m)
		if ok {
			compressed++
			saved += n
		}
		done++
		if progress != nil {
			progress(done)
		}
	}
	return
}

// compressOneRecording compresses one call recording and points its row at
// the result. Returns the bytes saved and whether it was compressed.
func (r *StorageRotator) compressOneRecording(ffmpeg string, m audioMedia) (int64, bool) {
	wavPath := filepath.Join(r.store.AudioDir(), m.RelPath)
	info, err := os.Stat(wavPath)
	if errors.Is(err, os.ErrNotExist) {
		// The row names a file that is gone: nothing to keep.
		if _, err := r.store.stripSessionAudio(m.SessionID, m.RelPath); err != nil {
			log.Printf("[activity-rotator] clear missing call audio %s: %v", m.RelPath, err)
		}
		return 0, false
	}
	if err != nil {
		r.audioSkip[m.RelPath] = true
		return 0, false
	}
	outPath, size, err := compressMeetingAudio(ffmpeg, wavPath)
	if err != nil {
		log.Printf("[activity-rotator] compress call audio %s: %v", m.RelPath, err)
		r.audioSkip[m.RelPath] = true
		return 0, false
	}
	newRel := strings.TrimSuffix(m.RelPath, filepath.Ext(m.RelPath)) + meetingAudioOpusExt
	changed, err := r.store.replaceSessionAudio(m.SessionID, m.RelPath, newRel, size)
	if err != nil || !changed {
		// The call pipeline changed the row meanwhile; keep its file.
		_ = os.Remove(outPath)
		if err != nil {
			log.Printf("[activity-rotator] point session %s at compressed audio: %v", m.SessionID, err)
		}
		r.audioSkip[m.RelPath] = true
		return 0, false
	}
	if err := os.Remove(wavPath); err != nil && !os.IsNotExist(err) {
		log.Printf("[activity-rotator] remove compressed call audio WAV %s: %v", m.RelPath, err)
	}
	return info.Size() - size, true
}
