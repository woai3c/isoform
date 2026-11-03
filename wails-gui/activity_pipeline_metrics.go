package main

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

const pipelineDurationWindowSize = 128

type pipelineDurationWindow struct {
	mu     sync.Mutex
	values []int64
	next   int
	filled bool
}

func (w *pipelineDurationWindow) add(d time.Duration) {
	ms := d.Milliseconds()
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.values) < pipelineDurationWindowSize {
		w.values = append(w.values, ms)
		return
	}
	w.values[w.next] = ms
	w.next = (w.next + 1) % len(w.values)
	w.filled = true
}

func (w *pipelineDurationWindow) snapshot() (lastMs, p50Ms, p95Ms float64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.values) == 0 {
		return 0, 0, 0
	}
	values := append([]int64(nil), w.values...)
	if w.filled && w.next > 0 {
		// Present values in chronological order for the last sample.
		values = append(values[w.next:], values[:w.next]...)
	}
	lastMs = float64(values[len(values)-1])
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	p50Ms = percentileInt64(values, 0.50)
	p95Ms = percentileInt64(values, 0.95)
	return lastMs, p50Ms, p95Ms
}

func percentileInt64(values []int64, pct float64) float64 {
	if len(values) == 0 {
		return 0
	}
	if len(values) == 1 {
		return float64(values[0])
	}
	idx := int(float64(len(values)-1) * pct)
	if idx < 0 {
		idx = 0
	}
	if idx >= len(values) {
		idx = len(values) - 1
	}
	return float64(values[idx])
}

type pipelineSourceMetrics struct {
	queuedChunks    atomic.Uint64
	droppedChunks   atomic.Uint64
	coalescedChunks atomic.Uint64
	queueDepth      atomic.Int64
	queueDurationMs atomic.Int64
}

func (m *pipelineSourceMetrics) snapshot() PerfBackgroundAudioSourceStats {
	return PerfBackgroundAudioSourceStats{
		QueuedChunks:    m.queuedChunks.Load(),
		DroppedChunks:   m.droppedChunks.Load(),
		CoalescedChunks: m.coalescedChunks.Load(),
		QueueDepth:      int(m.queueDepth.Load()),
		QueueDurationMs: m.queueDurationMs.Load(),
	}
}

type pipelineMetrics struct {
	micAudio    pipelineSourceMetrics
	systemAudio pipelineSourceMetrics

	whisperInFlight            atomic.Int64
	whisperRequests            atomic.Uint64
	whisperErrors              atomic.Uint64
	whisperEmptyTranscripts    atomic.Uint64
	whisperDroppedTranscripts  atomic.Uint64
	whisperAcceptedTranscripts atomic.Uint64
	whisperDurations           pipelineDurationWindow

	captureAttempts            atomic.Uint64
	captureCompleted           atomic.Uint64
	captureSkippedBackpressure atomic.Uint64
	captureSkippedDedup        atomic.Uint64
	captureSkippedSelfFocus    atomic.Uint64
	captureSkippedFocusChanged atomic.Uint64
	captureDroppedPIIBusy      atomic.Uint64
	captureDurations           pipelineDurationWindow

	piiInFlight  atomic.Int64
	piiLaunched  atomic.Uint64
	piiBusyDrops atomic.Uint64
	piiThrottled atomic.Uint64
	piiDurations pipelineDurationWindow

	visualChecks    atomic.Uint64
	visualSkipped   atomic.Uint64
	visualPromoted  atomic.Uint64
	visualDurations pipelineDurationWindow
}

var activityPipelineMetrics = &pipelineMetrics{}

func (m *pipelineMetrics) source(source backgroundListeningSource) *pipelineSourceMetrics {
	switch source {
	case backgroundListeningSourceSystem:
		return &m.systemAudio
	default:
		return &m.micAudio
	}
}

func (m *pipelineMetrics) recordAudioQueued(source backgroundListeningSource, depth int, duration time.Duration) {
	src := m.source(source)
	src.queuedChunks.Add(1)
	src.queueDepth.Store(int64(depth))
	src.queueDurationMs.Store(duration.Milliseconds())
}

func (m *pipelineMetrics) recordAudioDropped(source backgroundListeningSource, dropped int, depth int, duration time.Duration) {
	if dropped <= 0 {
		return
	}
	src := m.source(source)
	src.droppedChunks.Add(uint64(dropped))
	src.queueDepth.Store(int64(depth))
	src.queueDurationMs.Store(duration.Milliseconds())
}

func (m *pipelineMetrics) recordAudioDequeued(source backgroundListeningSource, depth int, duration time.Duration, batchChunks int) {
	src := m.source(source)
	if batchChunks > 1 {
		src.coalescedChunks.Add(uint64(batchChunks - 1))
	}
	src.queueDepth.Store(int64(depth))
	src.queueDurationMs.Store(duration.Milliseconds())
}

func (m *pipelineMetrics) recordWhisperStart() time.Time {
	m.whisperInFlight.Add(1)
	m.whisperRequests.Add(1)
	return time.Now()
}

func (m *pipelineMetrics) recordWhisperDone(start time.Time, err error) {
	m.whisperInFlight.Add(-1)
	m.whisperDurations.add(time.Since(start))
	if err != nil {
		m.whisperErrors.Add(1)
	}
}

func (m *pipelineMetrics) snapshot() PerfActivityPipelineStats {
	lastWhisper, p50Whisper, p95Whisper := m.whisperDurations.snapshot()
	lastCapture, p50Capture, p95Capture := m.captureDurations.snapshot()
	lastPII, p50PII, p95PII := m.piiDurations.snapshot()
	lastVisual, p50Visual, p95Visual := m.visualDurations.snapshot()

	return PerfActivityPipelineStats{
		BackgroundAudio: PerfBackgroundAudioStats{
			Microphone:                 m.micAudio.snapshot(),
			SystemAudio:                m.systemAudio.snapshot(),
			WhisperInFlight:            m.whisperInFlight.Load(),
			WhisperRequests:            m.whisperRequests.Load(),
			WhisperErrors:              m.whisperErrors.Load(),
			WhisperEmptyTranscripts:    m.whisperEmptyTranscripts.Load(),
			WhisperDroppedTranscripts:  m.whisperDroppedTranscripts.Load(),
			WhisperAcceptedTranscripts: m.whisperAcceptedTranscripts.Load(),
			WhisperLastMs:              lastWhisper,
			WhisperP50Ms:               p50Whisper,
			WhisperP95Ms:               p95Whisper,
		},
		Capture: PerfCapturePipelineStats{
			Attempts:            m.captureAttempts.Load(),
			Completed:           m.captureCompleted.Load(),
			SkippedBackpressure: m.captureSkippedBackpressure.Load(),
			SkippedDedup:        m.captureSkippedDedup.Load(),
			SkippedSelfFocus:    m.captureSkippedSelfFocus.Load(),
			SkippedFocusChanged: m.captureSkippedFocusChanged.Load(),
			DroppedPIIBusy:      m.captureDroppedPIIBusy.Load(),
			LastMs:              lastCapture,
			P50Ms:               p50Capture,
			P95Ms:               p95Capture,
		},
		PII: PerfPIIPipelineStats{
			InFlight:  m.piiInFlight.Load(),
			Launched:  m.piiLaunched.Load(),
			BusyDrops: m.piiBusyDrops.Load(),
			Throttled: m.piiThrottled.Load(),
			LastMs:    lastPII,
			P50Ms:     p50PII,
			P95Ms:     p95PII,
		},
		Visual: PerfVisualPipelineStats{
			Checks:   m.visualChecks.Load(),
			Skipped:  m.visualSkipped.Load(),
			Promoted: m.visualPromoted.Load(),
			LastMs:   lastVisual,
			P50Ms:    p50Visual,
			P95Ms:    p95Visual,
		},
	}
}
