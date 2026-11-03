package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"time"

	sherpa "github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"
	"wails-gui/internal/asr"
	"wails-gui/internal/speakerid"
)

// Only model files are shared with voice enrollment. Call signatures live in
// the activity database and never enter identity handles, ASR or model prompts.
const callVoiceModel = "campplus-16k-198893103"

var errCallModelsUnavailable = errors.New("call speaker models unavailable")

// The only test seam selects installed files, never an alternate downloader.
var callSpeakerModelPaths = defaultSherpaPaths

func verifiedCallModel(path, want string) bool {
	if path == "" || want == "" {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false
	}
	return hex.EncodeToString(h.Sum(nil)) == want
}

type callSpeakerSpan struct {
	Start, End time.Duration
	Speaker    int
	// Transient, never serialized or saved before an explicit rename.
	Embedding []float32
}

type callDiarizer interface {
	Process([]float32) ([]callSpeakerSpan, error)
	Close()
}

type sherpaCallDiarizer struct {
	model     *sherpa.OfflineSpeakerDiarization
	extractor *speakerid.Extractor
}

// This factory only opens installed, verified files. In particular it must
// never call ensureSherpaAssets: downloads belong to existing voice setup.
func newCallDiarizer(paths sherpaPaths) (callDiarizer, error) {
	if !verifiedCallModel(paths.segmentation, sha256SpeakerSegmentation) || !verifiedCallModel(paths.speakerEmbd, sha256SpeakerEmbedding) {
		return nil, errCallModelsUnavailable
	}
	cfg := &sherpa.OfflineSpeakerDiarizationConfig{}
	cfg.Segmentation.Pyannote.Model = paths.segmentation
	cfg.Segmentation.NumThreads = 1
	cfg.Segmentation.Provider = "cpu"
	cfg.Embedding.Model = paths.speakerEmbd
	cfg.Embedding.NumThreads = 1
	cfg.Embedding.Provider = "cpu"
	cfg.Clustering.Threshold = 0.5
	cfg.MinDurationOn = 0.2
	cfg.MinDurationOff = 0.3
	model := sherpa.NewOfflineSpeakerDiarization(cfg)
	if model == nil {
		return nil, errors.New("cannot load call segmentation model")
	}
	ext, err := speakerid.New(speakerid.Config{Model: paths.speakerEmbd, NumThreads: 1})
	if err != nil {
		sherpa.DeleteOfflineSpeakerDiarization(model)
		return nil, err
	}
	return &sherpaCallDiarizer{model: model, extractor: ext}, nil
}

func (d *sherpaCallDiarizer) Close() {
	sherpa.DeleteOfflineSpeakerDiarization(d.model)
	d.extractor.Close()
}

func (d *sherpaCallDiarizer) Process(samples []float32) ([]callSpeakerSpan, error) {
	if len(samples) == 0 {
		return nil, nil
	}
	segments := d.model.Process(samples)
	out := make([]callSpeakerSpan, 0, len(segments))
	for _, seg := range segments {
		from, to := max(0, int(seg.Start*16000)), min(len(samples), int(seg.End*16000))
		if to <= from {
			continue
		}
		span := callSpeakerSpan{Start: time.Duration(from) * time.Second / 16000, End: time.Duration(to) * time.Second / 16000, Speaker: seg.Speaker}
		// Mixed voices are useful turns, but cannot teach a voice signature.
		clean := true
		for _, other := range segments {
			if other.Speaker != seg.Speaker && other.Start < seg.End && other.End > seg.Start {
				clean = false
				break
			}
		}
		if clean && to-from >= 24000 {
			emb, err := d.extractor.ExtractFromPCM(samples[from:min(to, from+160000)], 16000)
			if err == nil {
				span.Embedding = emb
			}
		}
		out = append(out, span)
	}
	return out, nil
}

type callSamples struct{ samples []float32 }

func (r *callSamples) ReadSamples() ([]float32, error) {
	if len(r.samples) == 0 {
		return nil, io.EOF
	}
	n := min(len(r.samples), 32000)
	out := r.samples[:n]
	r.samples = r.samples[n:]
	return out, nil
}

// A bounded window keeps long recordings from requiring a whole-call PCM
// allocation. Embeddings align speaker clusters between windows. Both tracks
// keep their own clock; no overlap or textual echo heuristic deletes turns.
func transcribeDiarizedMeetingRecording(transcriber asr.LongFormTranscriber, path string, diarizer callDiarizer, monoSource ...string) ([]meetingUtterance, error) {
	start, ok := meetingAudioStartFromPath(path)
	if !ok {
		return nil, errors.New("missing recording start")
	}
	info, err := readMeetingWAVInfo(path)
	if err != nil {
		return nil, err
	}
	micChannel, systemChannel := 0, 1
	if info.channels == 1 {
		if len(monoSource) == 0 {
			return nil, errCallModelsUnavailable
		}
		switch monoSource[0] {
		case "microphone":
			return transcribeMeetingRecordingSources(transcriber, path, false, "microphone")
		case "system_audio":
			micChannel, systemChannel = -1, 0
		default:
			return nil, errCallModelsUnavailable
		}
	}
	var out []meetingUtterance
	if micChannel >= 0 {
		mic, err := openMeetingWAVChannel(path, info, micChannel)
		if err != nil {
			return nil, err
		}
		segments, err := transcriber.TranscribeLongForm(mic, "microphone")
		mic.Close()
		if err != nil {
			return nil, err
		}
		for _, seg := range segments {
			out = append(out, meetingUtterance{At: start.Add(seg.Start), End: start.Add(seg.End), Source: "microphone", SpeakerID: "you", Text: seg.Text})
		}
	}
	system, err := openMeetingWAVChannel(path, info, systemChannel)
	if err != nil {
		return nil, err
	}
	defer system.Close()
	var centroids [][]float32
	offset := time.Duration(0)
	for {
		const window = 16000 * 300
		samples := make([]float32, 0, window)
		for len(samples) < window {
			block, err := system.ReadSamples()
			samples = append(samples, block...)
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
		}
		if len(samples) == 0 {
			break
		}
		spans, err := diarizer.Process(samples)
		if err != nil {
			return nil, err
		}
		spans = coverCallSpeakerSpans(spans, time.Duration(len(samples))*time.Second/16000)
		// Keep each detector cluster within this window stable, even for a
		// short reply with insufficient clean speech for an embedding.
		clusters := map[int]int{}
		priorCount := len(centroids)
		usedPrior := map[int]bool{}
		for _, span := range spans {
			if span.Speaker < 0 || clusters[span.Speaker] != 0 || len(span.Embedding) == 0 {
				continue
			}
			best, score, second := -1, 0.0, 0.0
			// Distinct detector clusters in this window stay distinct.
			// Only align one-to-one with identities from earlier windows.
			for i, ref := range centroids[:priorCount] {
				if usedPrior[i] {
					continue
				}
				if s := speakerid.CosineSimilarity(span.Embedding, ref); s > score {
					second = score
					best, score = i, s
				} else if s > second {
					second = s
				}
			}
			if best < 0 || score < 0.75 || score-second < 0.10 {
				centroids = append(centroids, span.Embedding)
				best = len(centroids) - 1
			}
			usedPrior[best] = true
			clusters[span.Speaker] = best + 2
		}
		for _, span := range spans {
			id := clusters[span.Speaker]
			if id == 0 && span.Speaker >= 0 {
				centroids = append(centroids, nil)
				id = len(centroids) + 1
				clusters[span.Speaker] = id
			}
			from, to := max(0, int(span.Start*16000/time.Second)), min(len(samples), int(span.End*16000/time.Second))
			if to <= from {
				continue
			}
			parts, err := transcriber.TranscribeLongForm(&callSamples{samples: samples[from:to]}, "system_audio")
			if err != nil {
				return nil, err
			}
			for _, part := range parts {
				speakerID := fmt.Sprintf("speaker-%d", id)
				if span.Speaker < 0 {
					speakerID = fmt.Sprintf("unidentified-%d", offset+span.Start)
				}
				out = append(out, meetingUtterance{At: start.Add(offset + span.Start + part.Start), End: start.Add(offset + span.Start + min(part.End, span.End-span.Start)), Source: "system_audio", SpeakerID: speakerID, Text: part.Text, Embedding: span.Embedding})
			}
		}
		offset += time.Duration(len(samples)) * time.Second / 16000
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out, nil
}

// Segmentation is an attribution hint, never permission to discard audio.
// Give speech its edge consonants within neighboring gaps, and send every
// uncovered interval through the recognizer's own VAD with an unknown label.
func coverCallSpeakerSpans(spans []callSpeakerSpan, duration time.Duration) []callSpeakerSpan {
	out := make([]callSpeakerSpan, 0, len(spans)*2+1)
	for i, span := range spans {
		from, to := max(time.Duration(0), span.Start-200*time.Millisecond), min(duration, span.End+200*time.Millisecond)
		for j, other := range spans {
			if i == j {
				continue
			}
			if other.End <= span.Start {
				from = max(from, (other.End+span.Start)/2)
			} else if other.Start < span.Start {
				from = span.Start
			}
			if other.Start >= span.End {
				to = min(to, (other.Start+span.End)/2)
			} else if other.End > span.End {
				to = span.End
			}
		}
		span.Start, span.End = from, to
		out = append(out, span)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	covered := time.Duration(0)
	var gaps []callSpeakerSpan
	for _, span := range out {
		if span.Start > covered {
			gaps = append(gaps, callSpeakerSpan{Start: covered, End: span.Start, Speaker: -1})
		}
		covered = max(covered, span.End)
	}
	if covered < duration {
		gaps = append(gaps, callSpeakerSpan{Start: covered, End: duration, Speaker: -1})
	}
	out = append(out, gaps...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	return out
}
