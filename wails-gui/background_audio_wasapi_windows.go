//go:build windows

package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"log"
	"math"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/moutend/go-wca/pkg/wca"
)

const (
	// wasapiTargetSampleRate is the rate sherpa-onnx ASR expects (16 kHz
	// mono int16). Mirrors CORE_AUDIO_INPUT_TARGET_RATE on macOS.
	wasapiTargetSampleRate = 16000

	// wasapiRingSeconds buffers up to 60 s of converted samples between
	// the WASAPI capture goroutine and ReadChunk. Matches the macOS ring
	// size so memory footprint is comparable across platforms.
	wasapiRingSeconds  = 60
	wasapiRingCapacity = wasapiTargetSampleRate * wasapiRingSeconds

	// wasapiPollInterval drives the timer-driven capture loop. WASAPI
	// guarantees a packet at most every device period (~10 ms on default
	// engines) so polling slightly faster than the device period avoids
	// burning CPU while still draining promptly. Event-driven mode is
	// avoided for loopback because the render-side event trick is fragile
	// when render is silent — for 5 s ASR chunks a 10 ms timer is fine.
	wasapiPollInterval = 10 * time.Millisecond

	// wasapiBufferDurationHNs requests a 200 ms WASAPI engine buffer.
	// REFERENCE_TIME is in 100-ns units, so 200 ms = 200 * 10_000.
	// Generous enough that brief GC pauses in the capture goroutine
	// don't drop frames, small enough to keep latency bounded.
	wasapiBufferDurationHNs = 200 * 10_000

	// AUDCLNT_BUFFERFLAGS_SILENT marks a returned packet whose payload is
	// undefined and should be treated as silence (the WASAPI engine sets
	// this on loopback when nothing is playing). Defined in audioclient.h.
	audclntBufferFlagsSilent = 0x1

	// wasapiHealthLogInterval bounds how often the watchdog goroutine
	// reports session liveness to the file log. Short enough that a stuck
	// loopback shows up within one or two ASR chunk windows; long enough
	// that a healthy capture doesn't spam the log.
	wasapiHealthLogInterval = 30 * time.Second

	// wasapiCaptureErrorLogThrottle caps how often we surface a given
	// per-packet WASAPI failure (GetNextPacketSize / GetBuffer /
	// ReleaseBuffer) to the file log. The capture loop polls every 10 ms,
	// so without this we'd write thousands of identical errors per second
	// while a device is invalidated.
	wasapiCaptureErrorLogThrottle = 5 * time.Second
)

// PCM sample-format tags from mmreg.h. Only the ones we actually decode.
const (
	wavFormatPCM        = 0x0001
	wavFormatIEEEFloat  = 0x0003
	wavFormatExtensible = 0xFFFE
)

// wasapiSession is one capture session — either microphone input
// (loopback=false, dataFlow=ECapture) or render-endpoint loopback
// (loopback=true, dataFlow=ERender + AUDCLNT_STREAMFLAGS_LOOPBACK). The
// capture goroutine runs on its own OS thread (COM apartment requirement),
// mixes the native mix-format into 16k mono int16, and drops samples into
// a ring buffer that ReadChunk drains.
type wasapiSession struct {
	source   backgroundListeningSource
	loopback bool

	stopCh   chan struct{}
	doneCh   chan struct{}
	startErr chan error

	running atomic.Bool

	// Native mix-format detected after Initialize. Read only by the
	// capture goroutine after Start returns, so no lock needed.
	nativeRate     uint32
	nativeChannels uint16
	nativeBits     uint16
	nativeFormat   int // wavFormat* constant

	// Resampler state — fractional source-frame index advanced each
	// destination sample. Owned by the capture goroutine.
	resamplePos float64

	// Ring buffer of converted int16 samples (mono, 16 kHz).
	ringMu       sync.Mutex
	ring         []int16
	ringReadIdx  uint64
	ringWriteIdx uint64
	ringOverflow atomic.Bool

	// Diagnostic counters for the health-log watchdog. Written by the
	// capture goroutine, read by the watchdog goroutine — atomic so we
	// don't have to take ringMu just to peek liveness.
	packetsTotal  atomic.Int64
	packetsSilent atomic.Int64
	framesTotal   atomic.Int64

	// Device identity captured at session start. Logged once at start and
	// repeated in every health-log line so support reports can correlate
	// "no audio" with the actual endpoint we bound to.
	deviceFriendlyName string
	deviceID           string
}

func newWASAPISession(source backgroundListeningSource, loopback bool) *wasapiSession {
	return &wasapiSession{source: source, loopback: loopback}
}

// wasapiTargetWAVEFORMATEX returns a freshly-allocated WAVEFORMATEX
// describing our target capture format (16 kHz, mono, 16-bit signed PCM).
// Used with AUDCLNT_STREAMFLAGS_AUTOCONVERTPCM so the WASAPI engine does
// the format conversion with its built-in anti-aliasing resampler instead
// of our naive Go-side linear interpolation.
func wasapiTargetWAVEFORMATEX() *wca.WAVEFORMATEX {
	return &wca.WAVEFORMATEX{
		WFormatTag:      wavFormatPCM,
		NChannels:       1,
		NSamplesPerSec:  wasapiTargetSampleRate,
		NAvgBytesPerSec: wasapiTargetSampleRate * 2, // 16k samples/s * 2 bytes/sample
		NBlockAlign:     2,                          // 1 channel * 2 bytes/sample
		WBitsPerSample:  16,
		CbSize:          0,
	}
}

// Start spawns the OS-thread-locked capture goroutine and waits until it
// has either successfully entered the capture loop or reported an init
// error. Returns the init error if any; otherwise the session is running
// and ReadChunk will block on incoming samples.
func (s *wasapiSession) Start() error {
	s.stopCh = make(chan struct{})
	s.doneCh = make(chan struct{})
	s.startErr = make(chan error, 1)
	s.ring = make([]int16, wasapiRingCapacity)
	s.ringReadIdx = 0
	s.ringWriteIdx = 0
	s.ringOverflow.Store(false)
	s.resamplePos = 0

	go s.run()

	if err := <-s.startErr; err != nil {
		<-s.doneCh // wait for goroutine to fully exit so we don't leak
		return err
	}
	return nil
}

// Stop signals the capture goroutine to exit and waits for it. Safe to
// call multiple times; a no-op once stopped.
func (s *wasapiSession) Stop() {
	if !s.running.CompareAndSwap(true, false) {
		return
	}
	close(s.stopCh)
	<-s.doneCh
}

// run owns the COM apartment and all WASAPI interface lifetimes.
// CoInitializeEx + WASAPI require every call to happen on the same OS
// thread, hence runtime.LockOSThread for the goroutine's lifetime.
func (s *wasapiSession) run() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(s.doneCh)

	if err := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED); err != nil {
		s.startErr <- fmt.Errorf("CoInitializeEx: %w", err)
		return
	}
	defer ole.CoUninitialize()

	var enumerator *wca.IMMDeviceEnumerator
	if err := wca.CoCreateInstance(wca.CLSID_MMDeviceEnumerator, 0, wca.CLSCTX_ALL, wca.IID_IMMDeviceEnumerator, &enumerator); err != nil {
		s.startErr <- fmt.Errorf("CoCreateInstance(MMDeviceEnumerator): %w", err)
		return
	}
	defer enumerator.Release()

	dataFlow := uint32(wca.ECapture)
	if s.loopback {
		dataFlow = uint32(wca.ERender)
	}
	var device *wca.IMMDevice
	if err := enumerator.GetDefaultAudioEndpoint(dataFlow, wca.EConsole, &device); err != nil {
		s.startErr <- fmt.Errorf("GetDefaultAudioEndpoint(%s): %w", s.source, err)
		return
	}
	defer device.Release()

	// Capture device identity so the start + health logs can report which
	// physical endpoint we actually bound to. Best-effort: a failure here
	// only loses log detail, not capture functionality.
	s.deviceFriendlyName, s.deviceID = wasapiDeviceIdentity(device)

	var ac *wca.IAudioClient
	if err := device.Activate(wca.IID_IAudioClient, wca.CLSCTX_ALL, nil, &ac); err != nil {
		s.startErr <- fmt.Errorf("IMMDevice.Activate(IAudioClient): %w", err)
		return
	}
	defer ac.Release()

	// Format negotiation. Loopback is special — WASAPI rejects anything
	// other than the engine's mix format, so we must take whatever the
	// render endpoint is currently rendering and resample in Go. For mic
	// capture, prefer letting WASAPI's engine deliver 16 kHz mono 16-bit
	// PCM directly via AUDCLNT_STREAMFLAGS_AUTOCONVERTPCM — its built-in
	// resampler has proper anti-alias filtering, which our Go linear-
	// interp resampler does not. The Go resampler was producing audible
	// aliasing on 48 kHz Windows mics, which in turn corrupted Chinese
	// tone recognition (SenseVoice would fall back to Pinyin/Latin
	// transliteration on borderline-quality input).
	var wfx *wca.WAVEFORMATEX
	if err := ac.GetMixFormat(&wfx); err != nil {
		s.startErr <- fmt.Errorf("GetMixFormat: %w", err)
		return
	}
	// wfx is COM-allocated; free it once we've copied what we need.
	defer ole.CoTaskMemFree(uintptr(unsafe.Pointer(wfx)))

	bufferDuration := wca.REFERENCE_TIME(wasapiBufferDurationHNs)
	usedAutoConvert := false

	if !s.loopback {
		// Try the WASAPI-resampled path first. AUDCLNT_STREAMFLAGS_AUTOCONVERTPCM
		// tells the engine to insert its own resampler between the device and
		// our client buffer, delivering whatever PCM format we request.
		desired := wasapiTargetWAVEFORMATEX()
		flags := uint32(wca.AUDCLNT_STREAMFLAGS_AUTOCONVERTPCM | wca.AUDCLNT_STREAMFLAGS_SRC_DEFAULT_QUALITY)
		if err := ac.Initialize(wca.AUDCLNT_SHAREMODE_SHARED, flags, bufferDuration, 0, desired, nil); err == nil {
			s.nativeRate = desired.NSamplesPerSec
			s.nativeChannels = desired.NChannels
			s.nativeBits = desired.WBitsPerSample
			s.nativeFormat = wavFormatPCM
			usedAutoConvert = true
		} else {
			// Some virtual / pro audio devices reject AUTOCONVERTPCM.
			// Fall back to the mix-format path with Go-side resampling so
			// capture still works (even if quality is a bit lower).
			recordAudioCaptureEvent(AudioCaptureEvent{
				Source: string(s.source), Kind: "config_changed",
				Message: fmt.Sprintf("AUTOCONVERTPCM rejected (%v); falling back to mix-format + Go resampler", err),
			})
			// Re-Activate the client so we can call Initialize again; a
			// failed Initialize leaves the client in an unusable state.
			ac.Release()
			if err := device.Activate(wca.IID_IAudioClient, wca.CLSCTX_ALL, nil, &ac); err != nil {
				s.startErr <- fmt.Errorf("re-Activate after AUTOCONVERTPCM fallback: %w", err)
				return
			}
		}
	}

	if !usedAutoConvert {
		// Mix-format path: take whatever the engine has, resample in Go.
		// This is mandatory for loopback and the fallback for mic.
		s.nativeRate = wfx.NSamplesPerSec
		s.nativeChannels = wfx.NChannels
		s.nativeBits = wfx.WBitsPerSample
		s.nativeFormat = decodeWASAPIFormat(wfx)
		if s.nativeFormat == 0 {
			s.startErr <- fmt.Errorf("unsupported mix format: tag=%#x bits=%d channels=%d rate=%d",
				wfx.WFormatTag, wfx.WBitsPerSample, wfx.NChannels, wfx.NSamplesPerSec)
			return
		}
		if s.nativeRate == 0 || s.nativeChannels == 0 {
			s.startErr <- fmt.Errorf("invalid mix format dimensions: rate=%d channels=%d", s.nativeRate, s.nativeChannels)
			return
		}

		var streamFlags uint32
		if s.loopback {
			streamFlags = wca.AUDCLNT_STREAMFLAGS_LOOPBACK
		}
		if err := ac.Initialize(wca.AUDCLNT_SHAREMODE_SHARED, streamFlags, bufferDuration, 0, wfx, nil); err != nil {
			s.startErr <- fmt.Errorf("IAudioClient.Initialize(%s): %w", s.source, err)
			return
		}
	}

	var capture *wca.IAudioCaptureClient
	if err := ac.GetService(wca.IID_IAudioCaptureClient, &capture); err != nil {
		s.startErr <- fmt.Errorf("GetService(IAudioCaptureClient): %w", err)
		return
	}
	defer capture.Release()

	if err := ac.Start(); err != nil {
		s.startErr <- fmt.Errorf("IAudioClient.Start: %w", err)
		return
	}
	defer func() {
		// AudioClient.Stop is documented to be safe to call on an
		// already-stopped client. Ignore the error — we're shutting down.
		_ = ac.Stop()
	}()

	s.running.Store(true)
	s.startErr <- nil

	resamplePath := "wasapi-native"
	if !usedAutoConvert {
		resamplePath = "go-linear-interp"
	}
	startMessage := fmt.Sprintf("WASAPI %s: device=%q deviceID=%s rate=%d channels=%d bits=%d format=%d loopback=%t resample=%s",
		s.source, s.deviceFriendlyName, s.deviceID, s.nativeRate, s.nativeChannels, s.nativeBits, s.nativeFormat, s.loopback, resamplePath)
	recordAudioCaptureEvent(AudioCaptureEvent{
		Source: string(s.source), Kind: "started", Outcome: "success",
		Message: startMessage,
	})
	// Mirror the start line into the file log too: the DevTools event ring
	// is in-memory only, but most "system_audio is dead" support reports
	// come with yansu-gui.log, not with a panel snapshot.
	log.Printf("[background-audio] %s", startMessage)

	// Spawn the health-log watchdog. The deferred close fires after
	// captureLoop returns; runHealthLogger emits one final summary line
	// inside its stop branch so the log records lifetime totals even
	// when stop() arrives between ticks.
	healthDone := make(chan struct{})
	go s.runHealthLogger(healthDone)
	defer close(healthDone)

	// blockAlign matches whichever format Initialize actually accepted:
	// for AUTOCONVERTPCM we requested 16k mono 16-bit (2 bytes/frame), for
	// the mix-format path it's whatever the engine has (e.g. 8 bytes/frame
	// for 32-bit float stereo at 48k).
	blockAlign := uint32(s.nativeBits/8) * uint32(s.nativeChannels)
	s.captureLoop(capture, blockAlign)
}

// wasapiDeviceIdentity reads PKEY_Device_FriendlyName + endpoint ID off
// an IMMDevice. Returns ("", "") on any failure — the caller can still
// run, just without device labels in its diagnostic logs.
func wasapiDeviceIdentity(device *wca.IMMDevice) (friendlyName, id string) {
	if device == nil {
		return "", ""
	}
	var idPtr string
	if err := device.GetId(&idPtr); err == nil {
		id = idPtr
	}
	var ps *wca.IPropertyStore
	if err := device.OpenPropertyStore(wca.STGM_READ, &ps); err != nil {
		return "", id
	}
	defer ps.Release()
	var pv wca.PROPVARIANT
	key := wca.PKEY_Device_FriendlyName
	if err := ps.GetValue(&key, &pv); err != nil {
		return "", id
	}
	return pv.String(), id
}

// runHealthLogger writes one line per wasapiHealthLogInterval to the
// file log so we can tell, from a support log alone, whether a started
// WASAPI session is actually receiving packets. This caught the original
// Windows transcription bug (loopback bound to the default endpoint
// after an app restart but delivering 0 packets for the entire session);
// without the heartbeat the only visible signal was the absence of
// "chunk captured" lines, which is easy to mistake for a higher-level
// gate. A final summary line fires on shutdown so the log records the
// session's lifetime totals even when stop() arrives between ticks.
//
// The watchdog deliberately does NOT push events into
// recordAudioCaptureEvent's ring buffer — those are for state
// transitions the DevTools panel renders. Heartbeats belong in the file
// log where they survive process exit.
func (s *wasapiSession) runHealthLogger(stop <-chan struct{}) {
	ticker := time.NewTicker(wasapiHealthLogInterval)
	defer ticker.Stop()

	var lastPackets, lastSilent, lastFrames int64
	for {
		select {
		case <-stop:
			s.logHealthSummary("final", lastPackets, lastSilent, lastFrames)
			return
		case <-ticker.C:
		}

		packets, silent, frames := s.logHealthSummary("health", lastPackets, lastSilent, lastFrames)
		lastPackets, lastSilent, lastFrames = packets, silent, frames
	}
}

// logHealthSummary writes one health/final line and returns the current
// cumulative counters so the caller can advance its "last" baselines.
// Extracted so the per-tick path and the on-shutdown path stay in sync.
func (s *wasapiSession) logHealthSummary(kind string, lastPackets, lastSilent, lastFrames int64) (packets, silent, frames int64) {
	packets = s.packetsTotal.Load()
	silent = s.packetsSilent.Load()
	frames = s.framesTotal.Load()
	deltaPackets := packets - lastPackets
	deltaSilent := silent - lastSilent
	deltaFrames := frames - lastFrames

	s.ringMu.Lock()
	ringAvail := s.ringWriteIdx - s.ringReadIdx
	s.ringMu.Unlock()

	level := ""
	if kind == "health" && s.loopback && deltaPackets == 0 {
		// Loopback delivering zero packets for 30s while the session
		// claims to be running is the exact symptom of the bug this
		// log was added to surface. Prefix WARN so support tools and
		// human readers can grep for it. The "final" line skips this
		// check — a zero-packet delta at shutdown can also just mean
		// "the user stopped capture mid-interval".
		level = "[WARN] "
	}
	log.Printf("[background-audio] %sWASAPI %s %s: device=%q packets=%d silent=%d frames=%d ringSamples=%d (delta packets=%d silent=%d frames=%d over %s)",
		level, kind, s.source, s.deviceFriendlyName, packets, silent, frames, ringAvail,
		deltaPackets, deltaSilent, deltaFrames, wasapiHealthLogInterval)
	return packets, silent, frames
}

// captureLoop polls WASAPI's capture client every wasapiPollInterval and
// drains every available packet per tick. Mirrors the canonical timer-
// driven WASAPI capture pattern (see Microsoft's CaptureSharedTimerDriven
// sample and the moutend/go-wca _example projects).
func (s *wasapiSession) captureLoop(capture *wca.IAudioCaptureClient, blockAlign uint32) {
	ticker := time.NewTicker(wasapiPollInterval)
	defer ticker.Stop()

	// Per-call-site error throttles. The capture loop fires every 10 ms,
	// so a persistent failure (e.g. AUDCLNT_E_DEVICE_INVALIDATED after
	// the user yanked a USB audio device) would spam the log without
	// these. The DevTools event ring still gets every event — only the
	// file log is throttled.
	var nextLogPacketErr, nextLogGetBufErr, nextLogRelBufErr time.Time

	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
		}

		for {
			var packetFrames uint32
			if err := capture.GetNextPacketSize(&packetFrames); err != nil {
				recordAudioCaptureEvent(AudioCaptureEvent{
					Source: string(s.source), Kind: "error", Outcome: "failed",
					Message: fmt.Sprintf("GetNextPacketSize: %v", err),
				})
				s.logCaptureErrorThrottled(&nextLogPacketErr, "GetNextPacketSize", err)
				break
			}
			if packetFrames == 0 {
				break
			}

			var dataPtr *byte
			var framesAvailable uint32
			var flags uint32
			var devicePos, qpcPos uint64
			if err := capture.GetBuffer(&dataPtr, &framesAvailable, &flags, &devicePos, &qpcPos); err != nil {
				recordAudioCaptureEvent(AudioCaptureEvent{
					Source: string(s.source), Kind: "error", Outcome: "failed",
					Message: fmt.Sprintf("GetBuffer: %v", err),
				})
				s.logCaptureErrorThrottled(&nextLogGetBufErr, "GetBuffer", err)
				break
			}

			silent := flags&audclntBufferFlagsSilent != 0
			s.packetsTotal.Add(1)
			if silent {
				s.packetsSilent.Add(1)
			}
			s.framesTotal.Add(int64(framesAvailable))

			var srcBytes []byte
			if !silent && dataPtr != nil && framesAvailable > 0 {
				size := int(framesAvailable) * int(blockAlign)
				srcBytes = unsafe.Slice(dataPtr, size)
			}
			s.appendFrames(srcBytes, int(framesAvailable), silent)

			if err := capture.ReleaseBuffer(framesAvailable); err != nil {
				recordAudioCaptureEvent(AudioCaptureEvent{
					Source: string(s.source), Kind: "error", Outcome: "failed",
					Message: fmt.Sprintf("ReleaseBuffer: %v", err),
				})
				s.logCaptureErrorThrottled(&nextLogRelBufErr, "ReleaseBuffer", err)
			}
		}
	}
}

// logCaptureErrorThrottled writes one log line per wasapiCaptureErrorLogThrottle
// per call-site, mutating nextAllowed in place. Same-site errors that
// fire faster than the throttle are dropped from the file log (the
// DevTools event ring already keeps the full sequence).
func (s *wasapiSession) logCaptureErrorThrottled(nextAllowed *time.Time, op string, err error) {
	now := time.Now()
	if !nextAllowed.IsZero() && now.Before(*nextAllowed) {
		return
	}
	*nextAllowed = now.Add(wasapiCaptureErrorLogThrottle)
	log.Printf("[background-audio] WASAPI %s %s failed: %v (device=%q)", s.source, op, err, s.deviceFriendlyName)
}

// appendFrames downsamples one WASAPI packet to wasapiTargetSampleRate
// mono int16 and pushes the result into the ring buffer. Linear
// interpolation matches the macOS CoreAudio recorder — adequate for the
// 16 kHz speech-recognition pipeline, no third-party resampler required.
func (s *wasapiSession) appendFrames(srcBytes []byte, frameCount int, silent bool) {
	if frameCount <= 0 {
		return
	}
	ratio := float64(s.nativeRate) / float64(wasapiTargetSampleRate)
	if ratio <= 0 {
		return
	}
	pos := s.resamplePos
	limit := float64(frameCount)
	for pos < limit {
		frameIdx := int(pos)
		if frameIdx >= frameCount {
			break
		}
		var sample int16
		if !silent {
			sample = s.frameToMonoInt16(srcBytes, frameIdx)
		}
		s.ringWrite(sample)
		pos += ratio
	}
	s.resamplePos = pos - limit
}

func (s *wasapiSession) frameToMonoInt16(srcBytes []byte, frameIdx int) int16 {
	if len(srcBytes) == 0 || s.nativeChannels == 0 || s.nativeBits == 0 {
		return 0
	}
	bytesPerSample := int(s.nativeBits / 8)
	if bytesPerSample == 0 {
		return 0
	}
	frameStride := bytesPerSample * int(s.nativeChannels)
	off := frameIdx * frameStride
	if off+frameStride > len(srcBytes) {
		return 0
	}
	var sum float32
	for ch := 0; ch < int(s.nativeChannels); ch++ {
		sum += s.readChannelSample(srcBytes[off+ch*bytesPerSample:])
	}
	mixed := sum / float32(s.nativeChannels)
	if mixed > 1.0 {
		mixed = 1.0
	}
	if mixed < -1.0 {
		mixed = -1.0
	}
	return int16(mixed * 32767.0)
}

func (s *wasapiSession) readChannelSample(b []byte) float32 {
	switch s.nativeFormat {
	case wavFormatIEEEFloat:
		switch s.nativeBits {
		case 32:
			if len(b) >= 4 {
				return math.Float32frombits(binary.LittleEndian.Uint32(b))
			}
		case 64:
			if len(b) >= 8 {
				return float32(math.Float64frombits(binary.LittleEndian.Uint64(b)))
			}
		}
	case wavFormatPCM:
		switch s.nativeBits {
		case 16:
			if len(b) >= 2 {
				return float32(int16(binary.LittleEndian.Uint16(b))) / 32768.0
			}
		case 24:
			if len(b) >= 3 {
				// 24-bit LE PCM, sign-extend the top byte.
				v := int32(b[0]) | int32(b[1])<<8 | int32(b[2])<<16
				if v&0x00800000 != 0 {
					v |= ^0x00FFFFFF
				}
				return float32(v) / 8388608.0
			}
		case 32:
			if len(b) >= 4 {
				return float32(int32(binary.LittleEndian.Uint32(b))) / 2147483648.0
			}
		}
	}
	return 0
}

// decodeWASAPIFormat resolves WAVE_FORMAT_EXTENSIBLE to its concrete
// PCM/float code by reading the SubFormat GUID immediately after the
// WAVEFORMATEX header. Returns 0 for unsupported formats so the caller
// can surface a descriptive init error.
func decodeWASAPIFormat(wfx *wca.WAVEFORMATEX) int {
	switch wfx.WFormatTag {
	case wavFormatPCM:
		return wavFormatPCM
	case wavFormatIEEEFloat:
		return wavFormatIEEEFloat
	case wavFormatExtensible:
		// WAVEFORMATEXTENSIBLE = WAVEFORMATEX (18 bytes) + union (2 bytes)
		// + ChannelMask (4 bytes) + SubFormat GUID (16 bytes). The first
		// uint32 of the GUID is the format tag: 1=PCM, 3=IEEE float.
		if wfx.CbSize < 22 {
			return 0
		}
		const subFormatOffset = 18 + 2 + 4 // 24 bytes into the extensible struct
		base := unsafe.Pointer(wfx)
		sub := *(*uint32)(unsafe.Pointer(uintptr(base) + subFormatOffset))
		switch sub {
		case 1:
			return wavFormatPCM
		case 3:
			return wavFormatIEEEFloat
		}
	}
	return 0
}

func (s *wasapiSession) ringWrite(sample int16) {
	s.ringMu.Lock()
	capLen := uint64(len(s.ring))
	if capLen == 0 {
		s.ringMu.Unlock()
		return
	}
	if s.ringWriteIdx-s.ringReadIdx >= capLen {
		s.ringReadIdx = s.ringWriteIdx - capLen + 1
		s.ringOverflow.Store(true)
	}
	s.ring[s.ringWriteIdx%capLen] = sample
	s.ringWriteIdx++
	s.ringMu.Unlock()
}

// ReadChunk waits up to ~(chunkSeconds+1) seconds for chunkSeconds of
// audio to accumulate, then drains up to that many samples out of the
// ring buffer and returns them wrapped in a 16k mono int16 WAV blob.
// Returns (nil, nil) if the session stopped while waiting (mirrors the
// macOS recorder so captureLoop's empty-chunk-then-stopCh branch fires).
func (s *wasapiSession) ReadChunk(chunkSeconds int) ([]byte, error) {
	if chunkSeconds <= 0 {
		chunkSeconds = 1
	}
	target := uint64(chunkSeconds) * uint64(wasapiTargetSampleRate)

	// Wait loop: 20 ms ticks for at most (chunkSeconds+1)*50 iterations,
	// matching CoreAudioInputRecorder_ReadChunk on macOS so timing
	// behavior is symmetric across platforms.
	maxWaitIterations := (chunkSeconds + 1) * 50
	for waited := 0; waited < maxWaitIterations; waited++ {
		if !s.running.Load() {
			return nil, nil
		}
		s.ringMu.Lock()
		avail := s.ringWriteIdx - s.ringReadIdx
		s.ringMu.Unlock()
		if avail >= target {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if s.ringOverflow.Swap(false) {
		recordAudioCaptureEvent(AudioCaptureEvent{
			Source: string(s.source), Kind: "error", Outcome: "failed",
			Message: "ring buffer overflowed; dropped oldest samples",
		})
	}

	s.ringMu.Lock()
	capLen := uint64(len(s.ring))
	avail := s.ringWriteIdx - s.ringReadIdx
	if avail > capLen {
		s.ringReadIdx = s.ringWriteIdx - capLen
		avail = capLen
	}
	if avail == 0 {
		s.ringMu.Unlock()
		return nil, nil
	}
	sampleCount := avail
	if sampleCount > target {
		sampleCount = target
	}
	pcm := make([]int16, sampleCount)
	base := s.ringReadIdx
	for i := uint64(0); i < sampleCount; i++ {
		pcm[i] = s.ring[(base+i)%capLen]
	}
	s.ringReadIdx += sampleCount
	s.ringMu.Unlock()

	var out bytes.Buffer
	out.Grow(wavHeaderSize + int(sampleCount)*2)
	if err := writeWAVHeader(&out, uint32(sampleCount)*2); err != nil {
		return nil, fmt.Errorf("write WAV header: %w", err)
	}
	pcmBytes := make([]byte, len(pcm)*2)
	for i, v := range pcm {
		binary.LittleEndian.PutUint16(pcmBytes[i*2:i*2+2], uint16(v))
	}
	out.Write(pcmBytes)
	return out.Bytes(), nil
}
