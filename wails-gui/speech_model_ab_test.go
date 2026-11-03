//go:build !bindings && darwin && (amd64 || arm64) && !ios

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	sherpa "github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"
)

// TestSpeechModelAB measures one speech model on the after-call pass
// (TranscribeLongForm): error rate on scripted English and Mandarin speech
// spoken by macOS voices, and, when recordings are given, words recovered,
// agreement with reference lines and the real-time factor. Run it once per
// model and compare the JSON lines it logs:
//
//	YANSU_ASR_AB_ENGINE=sensevoice-int8|sensevoice-fp32|qwen3-asr|qwen3-asr-app \
//	YANSU_ASR_AB_DIR=<model folder> YANSU_ASR_AB_SILERO=<silero_vad.onnx> \
//	[YANSU_ASR_AB_RECORDINGS=<folder of meeting WAVs>] [YANSU_ASR_AB_REFS=<refs.json>] \
//	[YANSU_ASR_AB_MAX_SPEECH_S=15] [YANSU_ASR_AB_MAX_DECODE_S=18] \
//	go test -run TestSpeechModelAB -count=1 -v .
//
// refs.json is [{"file": "<wav name>", "text": "<what was said>"}]. The
// recordings and references stay outside the repository.
func TestSpeechModelAB(t *testing.T) {
	engine := os.Getenv("YANSU_ASR_AB_ENGINE")
	dir := os.Getenv("YANSU_ASR_AB_DIR")
	silero := os.Getenv("YANSU_ASR_AB_SILERO")
	if engine == "" || dir == "" || silero == "" {
		t.Skip("set YANSU_ASR_AB_ENGINE, YANSU_ASR_AB_DIR and YANSU_ASR_AB_SILERO")
	}
	var err error
	loadStart := time.Now()
	var transcriber *sherpaTranscriber
	if engine == "qwen3-asr-app" {
		// The app's own after-call configuration.
		transcriber, err = newQwen3ASRTranscriber(qwen3ASRPathsIn(dir), silero)
	} else {
		var cfg *sherpa.OfflineRecognizerConfig
		if cfg, err = speechModelABConfig(engine, dir); err == nil {
			transcriber, err = newSherpaTranscriberWithRecognizer(cfg, sherpaPaths{silero: silero}, nil)
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	defer transcriber.Stop()
	load := time.Since(loadStart)
	if max := os.Getenv("YANSU_ASR_AB_MAX_SPEECH_S"); max != "" {
		var seconds float32
		if _, err := fmt.Sscanf(max, "%g", &seconds); err != nil || seconds <= 0 {
			t.Fatalf("YANSU_ASR_AB_MAX_SPEECH_S=%q", max)
		}
		transcriber.vadCfg.SileroVad.MaxSpeechDuration = seconds
	}
	if max := os.Getenv("YANSU_ASR_AB_MAX_DECODE_S"); max != "" {
		var seconds float64
		if _, err := fmt.Sscanf(max, "%g", &seconds); err != nil || seconds <= 0 {
			t.Fatalf("YANSU_ASR_AB_MAX_DECODE_S=%q", max)
		}
		transcriber.maxDecodeSamples = int(seconds * meetingAudioSampleRate)
	}

	result := map[string]any{"engine": engine, "load_s": load.Seconds(), "max_speech_s": transcriber.vadCfg.SileroVad.MaxSpeechDuration, "max_decode_s": float64(transcriber.maxDecodeSamples) / meetingAudioSampleRate}

	en := speechModelABScripted(t, transcriber, []abLine{
		{"Samantha", "Thanks for joining. I want to walk through the recorder changes today."},
		{"Daniel", "Sure. Last week the transcripts came out in tiny pieces, and most of the call was missing."},
		{"Samantha", "As of now, do we record everything going on, all of the ambient sounds?"},
		{"Daniel", "Please schedule the security review for Thursday and send the summary to Sasha."},
		{"Samantha", "Six thick thistle sticks were fixed specifically for the first session."},
	})
	result["en_wer"], result["en_rtf"] = en.errorRate, en.rtf
	zh := speechModelABScripted(t, transcriber, []abLine{
		{"Eddy (Chinese (China mainland))", "新一版的严肃要来了，它会把你的电脑变成你的数据中心。"},
		{"Flo (Chinese (China mainland))", "我觉得未来的软件开发都会变成一个 app of apps。"},
		{"Eddy (Chinese (China mainland))", "数据在你这儿，运算也在你这儿，它会更好地理解你在做什么。"},
		{"Flo (Chinese (China mainland))", "我们下周五发布，然后看看第一批真实会议的效果。"},
	})
	result["zh_cer"], result["zh_rtf"] = zh.errorRate, zh.rtf

	if recordings := os.Getenv("YANSU_ASR_AB_RECORDINGS"); recordings != "" {
		refs := map[string][]string{}
		if path := os.Getenv("YANSU_ASR_AB_REFS"); path != "" {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var list []struct{ File, Text string }
			if err := json.Unmarshal(raw, &list); err != nil {
				t.Fatal(err)
			}
			for _, r := range list {
				refs[r.File] = append(refs[r.File], r.Text)
			}
		}
		files, _ := filepath.Glob(filepath.Join(recordings, "*.wav"))
		sort.Strings(files)
		var audio, spent time.Duration
		var tokens, refHit, refTotal int
		for _, file := range files {
			info, err := readMeetingWAVInfo(file)
			if err != nil {
				t.Fatalf("%s: %v", filepath.Base(file), err)
			}
			reader, err := openMeetingWAVChannel(file, info, 0)
			if err != nil {
				t.Fatal(err)
			}
			start := time.Now()
			segments, err := transcriber.TranscribeLongForm(reader, string(backgroundListeningSourceMicrophone))
			reader.Close()
			if err != nil {
				t.Fatal(err)
			}
			spent += time.Since(start)
			audio += time.Duration(info.dataSize/int64(2*info.channels)) * time.Second / meetingAudioSampleRate
			var text []string
			for _, s := range segments {
				text = append(text, s.Text)
			}
			hyp := transcriptTokens(strings.Join(text, " "))
			tokens += len(hyp)
			for _, ref := range refs[filepath.Base(file)] {
				want := transcriptTokens(ref)
				refHit += tokenLCS(want, hyp)
				refTotal += len(want)
			}
			if os.Getenv("YANSU_ASR_AB_SHOW") != "" {
				t.Logf("%s:\n%s", filepath.Base(file), strings.Join(text, "\n"))
			}
		}
		result["recordings_min"] = audio.Minutes()
		result["recordings_tokens"] = tokens
		if audio > 0 {
			result["recordings_rtf"] = spent.Seconds() / audio.Seconds()
		}
		if refTotal > 0 {
			result["ref_recall"] = float64(refHit) / float64(refTotal)
		}
	}
	line, _ := json.Marshal(result)
	t.Logf("AB %s", line)
}

type abLine struct{ voice, text string }

type abScore struct {
	errorRate float64
	rtf       float64
}

// speechModelABScripted speaks each line, puts the lines one after another
// with pauses into one recording, transcribes it whole and scores the
// result against the script (token edit distance; Han characters count one
// by one).
func speechModelABScripted(t *testing.T, transcriber *sherpaTranscriber, lines []abLine) abScore {
	t.Helper()
	dir := t.TempDir()
	var samples []int16
	var want []string
	for i, line := range lines {
		samples = append(samples, make([]int16, meetingAudioSampleRate)...)
		samples = append(samples, sayPCM(t, dir, fmt.Sprintf("ab-%d", i), line.voice, line.text)...)
		want = append(want, transcriptTokens(line.text)...)
	}
	samples = append(samples, make([]int16, meetingAudioSampleRate)...)
	path := filepath.Join(dir, "1792000000000-abcd1234.wav")
	if err := os.WriteFile(path, realWAV(samples), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := readMeetingWAVInfo(path)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := openMeetingWAVChannel(path, info, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	start := time.Now()
	segments, err := transcriber.TranscribeLongForm(reader, string(backgroundListeningSourceMicrophone))
	if err != nil {
		t.Fatal(err)
	}
	spent := time.Since(start)
	var text []string
	for _, s := range segments {
		text = append(text, s.Text)
	}
	got := transcriptTokens(strings.Join(text, " "))
	t.Logf("scripted -> %s", strings.Join(text, " | "))
	audio := time.Duration(len(samples)) * time.Second / meetingAudioSampleRate
	return abScore{
		errorRate: float64(tokenEditDistance(want, got)) / float64(len(want)),
		rtf:       spent.Seconds() / audio.Seconds(),
	}
}

func tokenEditDistance(a, b []string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

// tokenLCS is how many of want's tokens appear in hyp in the same order.
func tokenLCS(want, hyp []string) int {
	prev := make([]int, len(hyp)+1)
	cur := make([]int, len(hyp)+1)
	for i := 1; i <= len(want); i++ {
		for j := 1; j <= len(hyp); j++ {
			if want[i-1] == hyp[j-1] {
				cur[j] = prev[j-1] + 1
			} else {
				cur[j] = max(prev[j], cur[j-1])
			}
		}
		prev, cur = cur, prev
	}
	return prev[len(hyp)]
}

// speechModelABConfig builds the recognizer for one model folder.
func speechModelABConfig(engine, dir string) (*sherpa.OfflineRecognizerConfig, error) {
	cfg := &sherpa.OfflineRecognizerConfig{}
	cfg.FeatConfig.SampleRate = 16000
	cfg.FeatConfig.FeatureDim = 80
	cfg.ModelConfig.NumThreads = 2
	cfg.DecodingMethod = "greedy_search"
	find := func(pattern string) string {
		matches, _ := filepath.Glob(filepath.Join(dir, pattern))
		if len(matches) == 0 {
			return ""
		}
		return matches[0]
	}
	switch engine {
	case "sensevoice-int8", "sensevoice-fp32":
		model := "model.int8.onnx"
		if engine == "sensevoice-fp32" {
			model = "model.onnx"
		}
		cfg.ModelConfig.SenseVoice.Model = filepath.Join(dir, model)
		cfg.ModelConfig.SenseVoice.Language = "auto"
		cfg.ModelConfig.SenseVoice.UseInverseTextNormalization = 1
		cfg.ModelConfig.Tokens = filepath.Join(dir, "tokens.txt")
	case "qwen3-asr":
		cfg.ModelConfig.Qwen3ASR.ConvFrontend = find("conv_frontend*.onnx")
		cfg.ModelConfig.Qwen3ASR.Encoder = find("encoder*.onnx")
		cfg.ModelConfig.Qwen3ASR.Decoder = find("decoder*.onnx")
		cfg.ModelConfig.Qwen3ASR.Tokenizer = find("tokenizer")
		cfg.ModelConfig.Qwen3ASR.MaxTotalLen = 512
		cfg.ModelConfig.Qwen3ASR.MaxNewTokens = 256
		cfg.ModelConfig.Qwen3ASR.Temperature = 1e-6
		cfg.ModelConfig.Qwen3ASR.TopP = 0.8
		cfg.ModelConfig.Qwen3ASR.Seed = 42
		cfg.ModelConfig.Qwen3ASR.Hotwords = os.Getenv("YANSU_ASR_AB_HOTWORDS")
	default:
		return nil, fmt.Errorf("unknown engine %q", engine)
	}
	for _, p := range []string{cfg.ModelConfig.SenseVoice.Model, cfg.ModelConfig.Qwen3ASR.Encoder} {
		if p != "" {
			if _, err := os.Stat(p); err != nil {
				return nil, err
			}
		}
	}
	return cfg, nil
}

// qwen3ASRPathsIn is defaultQwen3ASRPaths for a model folder elsewhere.
func qwen3ASRPathsIn(dir string) qwen3ASRPaths {
	return qwen3ASRPaths{
		root:         filepath.Dir(dir),
		modelDir:     dir,
		convFrontend: filepath.Join(dir, "conv_frontend.onnx"),
		encoder:      filepath.Join(dir, "encoder.int8.onnx"),
		decoder:      filepath.Join(dir, "decoder.int8.onnx"),
		tokenizer:    filepath.Join(dir, "tokenizer"),
	}
}
