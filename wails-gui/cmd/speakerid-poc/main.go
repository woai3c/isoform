// speakerid-poc compares two WAV files using sherpa-onnx speaker embedding
// extraction. Intended as a one-shot sanity check before integrating speaker
// verification into the main app — run with two clips of yourself to confirm
// "same speaker" similarity is high, run with one of yourself + one of
// someone else to confirm "different speaker" similarity is low.
//
// Usage:
//
//	go run ./cmd/speakerid-poc \
//	    --model ~/.yansu-agent/sherpa/speaker/3dspeaker_campplus.onnx \
//	    --a clip_a.wav --b clip_b.wav
//
// The model must match the SpeakerEmbeddingExtractor format sherpa-onnx
// expects (3D-Speaker, WeSpeaker, or NeMo TitaNet ONNX). The default path
// matches what bundled_sherpa.go installs.
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

	"wails-gui/internal/speakerid"

	"wails-gui/internal/apphome"
)

func main() {
	dataDir, err := apphome.DeveloperDir()
	if err != nil {
		log.Fatal(err)
	}
	defaultModel := filepath.Join(dataDir, "sherpa", "speaker", "3dspeaker_campplus.onnx")
	model := flag.String("model", defaultModel, "path to speaker embedding ONNX")
	pathA := flag.String("a", "", "first WAV file")
	pathB := flag.String("b", "", "second WAV file")
	flag.Parse()

	if *pathA == "" || *pathB == "" {
		log.Fatalf("--a and --b are required (one WAV file each)")
	}
	if _, err := os.Stat(*model); err != nil {
		log.Fatalf("model not found at %q (download via app first, or pass --model): %v", *model, err)
	}

	ext, err := speakerid.New(speakerid.Config{Model: *model})
	if err != nil {
		log.Fatalf("load extractor: %v", err)
	}
	defer ext.Close()
	fmt.Printf("Loaded model %s (embedding dim=%d)\n", filepath.Base(*model), ext.Dim())

	embA, err := extractFromWAV(ext, *pathA)
	if err != nil {
		log.Fatalf("extract %s: %v", *pathA, err)
	}
	embB, err := extractFromWAV(ext, *pathB)
	if err != nil {
		log.Fatalf("extract %s: %v", *pathB, err)
	}

	score := speakerid.CosineSimilarity(embA, embB)
	fmt.Printf("\n%s  ↔  %s\n", filepath.Base(*pathA), filepath.Base(*pathB))
	fmt.Printf("cosine similarity = %.4f\n\n", score)

	switch {
	case score >= 0.75:
		fmt.Println("verdict: STRONG match (almost certainly same speaker)")
	case score >= 0.55:
		fmt.Println("verdict: WEAK match (probably same speaker, marginal)")
	case score >= 0.3:
		fmt.Println("verdict: UNRELATED (different speakers; cross-talk or partial overlap)")
	default:
		fmt.Println("verdict: CLEARLY DIFFERENT (different speakers)")
	}
	fmt.Println("\nguidance:")
	fmt.Println("  - same speaker, same recording session  → expect 0.85+")
	fmt.Println("  - same speaker, different recording days → expect 0.70-0.85")
	fmt.Println("  - different speakers                     → expect < 0.4")
	fmt.Println("  - threshold for production verification  → 0.65-0.70")
}

// extractFromWAV reads a 16-bit PCM mono WAV file and runs it through the
// extractor. Only the minimum-viable PCM-format header parsing — no
// resampling, no multi-channel mixing.
func extractFromWAV(ext *speakerid.Extractor, path string) ([]float32, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	samples, rate, err := readWavPCM16Mono(f)
	if err != nil {
		return nil, fmt.Errorf("parse wav: %w", err)
	}
	if rate != 16000 {
		log.Printf("warning: %s sample rate %d Hz != model's expected 16000 Hz — accuracy will degrade", path, rate)
	}
	return ext.ExtractFromPCM(samples, rate)
}

// readWavPCM16Mono parses a tiny subset of RIFF/WAVE: 16-bit PCM, mono.
// Anything else returns an error with a hint to re-export.
func readWavPCM16Mono(r io.Reader) ([]float32, int, error) {
	var header [44]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, 0, fmt.Errorf("read header: %w", err)
	}
	if string(header[0:4]) != "RIFF" || string(header[8:12]) != "WAVE" {
		return nil, 0, fmt.Errorf("not a RIFF/WAVE file")
	}
	audioFormat := binary.LittleEndian.Uint16(header[20:22])
	numChannels := binary.LittleEndian.Uint16(header[22:24])
	sampleRate := binary.LittleEndian.Uint32(header[24:28])
	bitsPerSample := binary.LittleEndian.Uint16(header[34:36])
	if audioFormat != 1 {
		return nil, 0, fmt.Errorf("only PCM (audioFormat=1) supported, got %d — re-export the clip as 16-bit PCM WAV", audioFormat)
	}
	if numChannels != 1 {
		return nil, 0, fmt.Errorf("only mono WAV supported, got %d channels — mix down to mono first", numChannels)
	}
	if bitsPerSample != 16 {
		return nil, 0, fmt.Errorf("only 16-bit PCM supported, got %d-bit — re-export at 16-bit", bitsPerSample)
	}
	pcm, err := io.ReadAll(r)
	if err != nil {
		return nil, 0, fmt.Errorf("read pcm: %w", err)
	}
	if len(pcm)%2 != 0 {
		return nil, 0, fmt.Errorf("truncated PCM body (odd byte count %d)", len(pcm))
	}
	samples := make([]float32, len(pcm)/2)
	for i := range samples {
		s := int16(binary.LittleEndian.Uint16(pcm[i*2 : i*2+2]))
		samples[i] = float32(s) / 32768.0
	}
	return samples, int(sampleRate), nil
}
