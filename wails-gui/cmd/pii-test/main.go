package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"wails-gui/internal/pii"
	"wails-gui/internal/pii/eval"
	"wails-gui/internal/platform/ocr"

	"wails-gui/internal/apphome"
)

func main() {
	// A labeled text corpus needs neither private snapshots nor model downloads.
	if len(os.Args) == 3 && os.Args[1] == "-dataset" {
		testDataset(os.Args[2])
		return
	}

	configDir, err := apphome.DeveloperDir()
	if err != nil {
		log.Fatal(err)
	}

	// Use arg or find latest snapshot
	var latest string
	if len(os.Args) > 1 {
		latest = os.Args[1]
	} else {
		snapshotBase := filepath.Join(configDir, "activity", "snapshots")
		// Find the most recent date directory
		dateDirs, err := os.ReadDir(snapshotBase)
		if os.IsNotExist(err) {
			// An isolated test HOME has no private screenshots. Use the
			// checked-in labeled corpus for the no-argument diagnostic.
			testDataset("internal/pii/eval/testdata/sample_dataset.json")
			return
		}
		if err != nil {
			log.Fatalf("read snapshot base: %v", err)
		}
		sort.Slice(dateDirs, func(i, j int) bool {
			return dateDirs[i].Name() > dateDirs[j].Name()
		})
		if len(dateDirs) == 0 {
			log.Fatal("no snapshot directories found")
		}
		snapshotDir := filepath.Join(snapshotBase, dateDirs[0].Name())
		entries, err := os.ReadDir(snapshotDir)
		if err != nil {
			log.Fatalf("read snapshot dir: %v", err)
		}
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].Name() > entries[j].Name()
		})
		if len(entries) == 0 {
			log.Fatal("no snapshots found")
		}
		latest = filepath.Join(snapshotDir, entries[0].Name())
	}
	fmt.Printf("Testing with: %s\n\n", latest)

	// Read the JPEG
	jpegData, err := os.ReadFile(latest)
	if err != nil {
		log.Fatalf("read snapshot: %v", err)
	}

	// Step 1: OCR
	fmt.Println("=== Step 1: OCR ===")
	ocrEngine := ocr.New()
	if !ocrEngine.IsSupported() {
		log.Fatal("OCR not supported")
	}

	observations, err := ocrEngine.RecognizeJPEGWithBoxes(jpegData, []string{"en-US", "zh-Hans"}, ocr.PriorityMid)
	if err != nil {
		log.Fatalf("OCR failed: %v", err)
	}
	fmt.Printf("OCR found %d text observations:\n", len(observations))
	for i, obs := range observations {
		fmt.Printf("  [%d] (%.2f,%.2f %.2fx%.2f) conf=%.2f: %q\n",
			i, obs.X, obs.Y, obs.Width, obs.Height, obs.Confidence, obs.Text)
	}

	// Step 2: PII detection (GLiNER)
	fmt.Println("\n=== Step 2: PII Detection (GLiNER) ===")
	detector, err := pii.NewDetector(configDir, func(name string, downloaded, total int64, speed float64) {
		if total > 0 {
			pct := float64(downloaded) / float64(total) * 100
			fmt.Printf("  [download] %s: %.1f%% (%.1f MB/s)\n", name, pct, speed/1024/1024)
		}
	})
	if err != nil {
		log.Fatalf("detector init: %v", err)
	}
	defer detector.Destroy()

	glinerDetections := detector.Detect(observations)
	fmt.Printf("GLiNER detections: %d\n", len(glinerDetections))
	for i, d := range glinerDetections {
		fmt.Printf("  [%d] category=%s conf=%.3f text=%q box=(%.3f,%.3f %.3fx%.3f)\n",
			i, d.Category, d.Confidence, d.Text, d.X, d.Y, d.Width, d.Height)
	}

	// Step 3: Regex fallback
	fmt.Println("\n=== Step 3: PII Detection (Regex Fallback) ===")
	regexDetections := pii.DetectWithRegex(observations)
	fmt.Printf("Regex detections: %d\n", len(regexDetections))
	for i, d := range regexDetections {
		fmt.Printf("  [%d] category=%s conf=%.3f text=%q box=(%.3f,%.3f %.3fx%.3f)\n",
			i, d.Category, d.Confidence, d.Text, d.X, d.Y, d.Width, d.Height)
	}

	// Step 4: Merge
	detections := pii.MergeDetections(glinerDetections, regexDetections)
	fmt.Printf("\n=== Step 4: Merged Detections: %d ===\n", len(detections))
	for i, d := range detections {
		fmt.Printf("  [%d] category=%s conf=%.3f text=%q\n",
			i, d.Category, d.Confidence, d.Text)
	}

	if len(detections) > 0 {
		fmt.Println("\n=== Step 5: Masking ===")
		masked, err := pii.MaskRegions(jpegData, detections, 50)
		if errors.Is(err, pii.ErrMaskRegionTooLarge) {
			// The app drops such a frame rather than writing it half-masked.
			log.Printf("mask refused, the app would drop this frame: %v", err)
			return
		}
		if err != nil {
			log.Fatalf("mask failed: %v", err)
		}
		outPath := "/tmp/pii-masked-test.jpg"
		if err := os.WriteFile(outPath, masked, 0644); err != nil {
			log.Fatalf("write masked: %v", err)
		}
		fmt.Printf("Masked image saved to: %s\n", outPath)
	} else {
		fmt.Println("\nNo PII detected - nothing to mask.")
	}
}

// testDataset uses the same scorer as pii-eval. The screenshot diagnostic
// remains available when passed an image path.
func testDataset(path string) {
	ds, err := eval.LoadDataset(path)
	if err != nil {
		log.Fatal(err)
	}
	predictions := make(map[string][]eval.Prediction)
	for _, sample := range ds.Samples {
		for _, d := range pii.DetectWithRegex([]ocr.TextObservation{{Text: sample.Text, Confidence: 1}}) {
			start := strings.Index(sample.Text, d.Text)
			predictions[sample.ID] = append(predictions[sample.ID], eval.Prediction{Text: d.Text, Category: d.Category, Start: start, End: start + len(d.Text), Confidence: d.Confidence})
		}
	}
	eval.FormatTable(os.Stdout, eval.Score(ds.Samples, predictions, eval.DefaultScoreOptions()))
	eval.FormatDebug(os.Stdout, eval.DebugScore(ds.Samples, predictions, eval.DefaultScoreOptions()))
}
