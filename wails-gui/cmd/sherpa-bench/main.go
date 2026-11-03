//go:build darwin

package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	sherpa "github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"

	"wails-gui/internal/apphome"
)

func main() {
	dataDir, err := apphome.DeveloperDir()
	if err != nil {
		log.Fatal(err)
	}
	var (
		modelDir = flag.String("model-dir", filepath.Join(dataDir, "sherpa"),
			"directory containing sense-voice/model.int8.onnx, sense-voice/tokens.txt, silero_vad.onnx")
		wav    = flag.String("wav", "", "path to a WAV file (single channel; sherpa resamples internally)")
		useVAD = flag.Bool("vad", false, "filter via Silero VAD before decoding")
		lang   = flag.String("lang", "auto", "SenseVoice language hint: auto/zh/en/ja/ko/yue")
	)
	flag.Parse()

	if *wav == "" {
		log.Fatalf("-wav is required")
	}

	senseVoiceModel := filepath.Join(*modelDir, "sense-voice/model.int8.onnx")
	tokens := filepath.Join(*modelDir, "sense-voice/tokens.txt")
	vadModel := filepath.Join(*modelDir, "silero_vad.onnx")
	for _, p := range []string{senseVoiceModel, tokens, vadModel} {
		if _, err := os.Stat(p); err != nil {
			log.Fatalf("missing model file: %s", p)
		}
	}

	cfg := &sherpa.OfflineRecognizerConfig{}
	cfg.FeatConfig.SampleRate = 16000
	cfg.FeatConfig.FeatureDim = 80
	cfg.ModelConfig.SenseVoice.Model = senseVoiceModel
	cfg.ModelConfig.SenseVoice.Language = *lang
	cfg.ModelConfig.SenseVoice.UseInverseTextNormalization = 1
	cfg.ModelConfig.Tokens = tokens
	cfg.ModelConfig.NumThreads = 2
	cfg.DecodingMethod = "greedy_search"

	loadStart := time.Now()
	recog := sherpa.NewOfflineRecognizer(cfg)
	if recog == nil {
		log.Fatalf("failed to create OfflineRecognizer")
	}
	defer sherpa.DeleteOfflineRecognizer(recog)
	loadDur := time.Since(loadStart)

	wave := sherpa.ReadWave(*wav)
	if wave == nil {
		log.Fatalf("failed to read wav %s", *wav)
	}
	audioSec := float32(len(wave.Samples)) / float32(wave.SampleRate)

	if *useVAD {
		vadCfg := &sherpa.VadModelConfig{}
		vadCfg.SileroVad.Model = vadModel
		vadCfg.SileroVad.Threshold = 0.5
		vadCfg.SileroVad.MinSilenceDuration = 0.5
		vadCfg.SileroVad.MinSpeechDuration = 0.25
		vadCfg.SileroVad.WindowSize = 512
		vadCfg.SileroVad.MaxSpeechDuration = 30
		vadCfg.SampleRate = wave.SampleRate
		vadCfg.NumThreads = 1

		vad := sherpa.NewVoiceActivityDetector(vadCfg, 60)
		if vad == nil {
			log.Fatalf("failed to create VAD")
		}
		defer sherpa.DeleteVoiceActivityDetector(vad)

		ws := vadCfg.SileroVad.WindowSize
		for i := 0; i+ws <= len(wave.Samples); i += ws {
			vad.AcceptWaveform(wave.Samples[i : i+ws])
		}
		vad.Flush()

		decodeStart := time.Now()
		var segCount int
		for !vad.IsEmpty() {
			seg := vad.Front()
			vad.Pop()
			segCount++
			stream := sherpa.NewOfflineStream(recog)
			stream.AcceptWaveform(wave.SampleRate, seg.Samples)
			recog.Decode(stream)
			res := stream.GetResult()
			sherpa.DeleteOfflineStream(stream)
			startSec := float32(seg.Start) / float32(wave.SampleRate)
			endSec := startSec + float32(len(seg.Samples))/float32(wave.SampleRate)
			fmt.Printf("[%.2fs-%.2fs] lang=%s text=%q\n", startSec, endSec, res.Lang, stripTags(res.Text))
		}
		decodeDur := time.Since(decodeStart)
		fmt.Printf("\nload=%v audio=%.2fs vad-segs=%d decode=%v RTF=%.4f\n",
			loadDur, audioSec, segCount, decodeDur,
			decodeDur.Seconds()/float64(audioSec))
		return
	}

	decodeStart := time.Now()
	stream := sherpa.NewOfflineStream(recog)
	stream.AcceptWaveform(wave.SampleRate, wave.Samples)
	recog.Decode(stream)
	res := stream.GetResult()
	sherpa.DeleteOfflineStream(stream)
	decodeDur := time.Since(decodeStart)

	fmt.Printf("text=%q\n", stripTags(res.Text))
	fmt.Printf("raw=%q\n", res.Text)
	fmt.Printf("lang=%s emotion=%s event=%s\n", res.Lang, res.Emotion, res.Event)
	fmt.Printf("load=%v audio=%.2fs decode=%v RTF=%.4f\n",
		loadDur, audioSec, decodeDur, decodeDur.Seconds()/float64(audioSec))
}

// stripTags removes SenseVoice control tags like <|zh|> <|NEUTRAL|> <|Speech|>
// from the recognizer text. Returned text is also left-trimmed.
func stripTags(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inside := false
	for i := 0; i < len(s); i++ {
		switch {
		case !inside && i+1 < len(s) && s[i] == '<' && s[i+1] == '|':
			inside = true
			i++
		case inside && i+1 < len(s) && s[i] == '|' && s[i+1] == '>':
			inside = false
			i++
		case !inside:
			b.WriteByte(s[i])
		}
	}
	return strings.TrimLeft(b.String(), " \t")
}
