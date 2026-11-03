package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"wails-gui/internal/pii"
	"wails-gui/internal/pii/eval"
	"wails-gui/internal/platform/ocr"

	"wails-gui/internal/apphome"
)

func main() {
	datasetPath := flag.String("dataset", "", "path to evaluation dataset JSON")
	outputPath := flag.String("output", "", "path to save report JSON (optional)")
	comparePath := flag.String("compare", "", "path to baseline report JSON for comparison")
	format := flag.String("format", "table", "output format: table, json, markdown, scorecard")
	mode := flag.String("mode", "text", "evaluation mode: text (regex-only) or full (GLiNER + regex)")
	debug := flag.Bool("debug", false, "show per-sample error analysis")
	flag.Parse()

	if *datasetPath == "" {
		fmt.Fprintf(os.Stderr, "Usage: pii-eval -dataset <path.json> [-output report.json] [-compare baseline.json] [-format table|json|markdown|scorecard] [-mode text|ocr-regex|full|opf|opf-regex]\n\n")
		fmt.Fprintf(os.Stderr, "Evaluate PII detection accuracy against an annotated dataset.\n\n")
		fmt.Fprintf(os.Stderr, "Modes:\n")
		fmt.Fprintf(os.Stderr, "  text  - Regex-based detection on text samples (fast, no model download)\n")
		fmt.Fprintf(os.Stderr, "  ocr-regex  - OCR + Regex on screenshot samples\n")
		fmt.Fprintf(os.Stderr, "  full  - GLiNER + Regex on OCR observations (requires model + images)\n")
		fmt.Fprintf(os.Stderr, "  opf  - OpenAI Privacy Filter on OCR observations (experimental)\n")
		fmt.Fprintf(os.Stderr, "  opf-regex  - OPF + Regex on OCR observations (experimental)\n")
		os.Exit(1)
	}

	ds, err := eval.LoadDataset(*datasetPath)
	if err != nil {
		log.Fatalf("load dataset: %v", err)
	}
	fmt.Fprintf(os.Stderr, "Loaded dataset %q: %d samples\n", ds.Name, len(ds.Samples))

	predictions := make(map[string][]eval.Prediction)
	var detectorInit time.Duration
	sampleDurations := make([]time.Duration, 0, len(ds.Samples))
	measureSample := func(sample eval.Sample, fn func(eval.Sample) []eval.Prediction) {
		start := time.Now()
		predictions[sample.ID] = fn(sample)
		sampleDurations = append(sampleDurations, time.Since(start))
	}

	switch *mode {
	case "text":
		for _, sample := range ds.Samples {
			measureSample(sample, runTextDetection)
		}
	case "ocr-regex":
		for _, sample := range ds.Samples {
			measureSample(sample, runOCRRegexDetection)
		}
	case "full":
		configDir, err := apphome.DeveloperDir()
		if err != nil {
			log.Fatal(err)
		}
		initStart := time.Now()
		detector, err := pii.NewDetector(configDir, func(name string, downloaded, total int64, speed float64) {
			if total > 0 {
				pct := float64(downloaded) / float64(total) * 100
				fmt.Fprintf(os.Stderr, "  [download] %s: %.1f%%\n", name, pct)
			}
		})
		detectorInit = time.Since(initStart)
		if err != nil {
			log.Fatalf("init detector: %v", err)
		}
		defer detector.Destroy()

		for _, sample := range ds.Samples {
			measureSample(sample, func(sample eval.Sample) []eval.Prediction {
				return runFullDetection(sample, detector)
			})
		}
	case "opf":
		configDir, err := apphome.DeveloperDir()
		if err != nil {
			log.Fatal(err)
		}
		initStart := time.Now()
		detector, err := pii.NewOPFDetector(configDir, func(name string, downloaded, total int64, speed float64) {
			if total > 0 {
				pct := float64(downloaded) / float64(total) * 100
				fmt.Fprintf(os.Stderr, "  [download] %s: %.1f%%\n", name, pct)
			}
		})
		detectorInit = time.Since(initStart)
		if err != nil {
			log.Fatalf("init OPF detector: %v", err)
		}
		defer detector.Destroy()

		for _, sample := range ds.Samples {
			measureSample(sample, func(sample eval.Sample) []eval.Prediction {
				return runFullDetection(sample, detector)
			})
		}
	case "opf-regex":
		configDir, err := apphome.DeveloperDir()
		if err != nil {
			log.Fatal(err)
		}
		initStart := time.Now()
		detector, err := pii.NewOPFDetector(configDir, func(name string, downloaded, total int64, speed float64) {
			if total > 0 {
				pct := float64(downloaded) / float64(total) * 100
				fmt.Fprintf(os.Stderr, "  [download] %s: %.1f%%\n", name, pct)
			}
		})
		detectorInit = time.Since(initStart)
		if err != nil {
			log.Fatalf("init OPF detector: %v", err)
		}
		defer detector.Destroy()

		for _, sample := range ds.Samples {
			measureSample(sample, func(sample eval.Sample) []eval.Prediction {
				return runOPFRegexDetection(sample, detector)
			})
		}
	default:
		log.Fatalf("unknown mode %q (use text, ocr-regex, full, opf, or opf-regex)", *mode)
	}

	opts := eval.DefaultScoreOptions()
	report := eval.Score(ds.Samples, predictions, opts)
	report.Performance = eval.PerformanceFromDurations(detectorInit, sampleDurations)

	detectorName := "Yansu " + *mode
	switch *format {
	case "table":
		eval.FormatTable(os.Stdout, report)
		eval.FormatScorecard(os.Stdout, report, detectorName)
	case "json":
		eval.FormatJSON(os.Stdout, report)
	case "markdown":
		eval.FormatMarkdown(os.Stdout, report)
	case "scorecard":
		eval.FormatScorecard(os.Stdout, report, detectorName)
	default:
		log.Fatalf("unknown format %q (use table, json, markdown, or scorecard)", *format)
	}

	if *debug {
		debugResults := eval.DebugScore(ds.Samples, predictions, opts)
		eval.FormatDebug(os.Stdout, debugResults)
	}

	if *outputPath != "" {
		if err := eval.SaveReport(*outputPath, report); err != nil {
			log.Fatalf("save report: %v", err)
		}
		fmt.Fprintf(os.Stderr, "\nReport saved to: %s\n", *outputPath)
	}

	if *comparePath != "" {
		baseline, err := eval.LoadReport(*comparePath)
		if err != nil {
			log.Fatalf("load baseline: %v", err)
		}
		fmt.Println()
		eval.CompareReports(os.Stdout, *baseline, report, "baseline", "candidate")
		eval.CompareScorecards(os.Stdout, []eval.NamedReport{
			{Name: "baseline", Report: *baseline},
			{Name: "candidate", Report: report},
		})
	}
}

func runTextDetection(sample eval.Sample) []eval.Prediction {
	if sample.Text == "" {
		return nil
	}

	observations := textToObservations(sample.Text)
	detections := pii.DetectWithRegex(observations)

	var preds []eval.Prediction
	for _, d := range detections {
		start, end := findOffsets(sample.Text, d.Text)
		preds = append(preds, eval.Prediction{
			Text:       d.Text,
			Category:   d.Category,
			Start:      start,
			End:        end,
			Confidence: d.Confidence,
		})
	}
	return preds
}

func runOCRRegexDetection(sample eval.Sample) []eval.Prediction {
	if sample.ImagePath == "" {
		return runTextDetection(sample)
	}

	ocrEngine := ocr.New()
	if !ocrEngine.IsSupported() {
		log.Printf("[%s] OCR not supported", sample.ID)
		return nil
	}
	jpegData, err := os.ReadFile(sample.ImagePath)
	if err != nil {
		log.Printf("[%s] read image: %v", sample.ID, err)
		return nil
	}
	observations, err := ocrEngine.RecognizeJPEGWithBoxes(jpegData, []string{"en-US", "zh-Hans"}, ocr.PriorityMid)
	if err != nil {
		log.Printf("[%s] OCR failed: %v", sample.ID, err)
		return nil
	}

	detections := pii.DetectWithRegex(observations)
	var preds []eval.Prediction
	for _, d := range detections {
		preds = append(preds, eval.Prediction{
			Text:       d.Text,
			Category:   d.Category,
			X:          d.X,
			Y:          d.Y,
			Width:      d.Width,
			Height:     d.Height,
			Confidence: d.Confidence,
		})
	}
	return preds
}

func runFullDetection(sample eval.Sample, detector pii.Detector) []eval.Prediction {
	var observations []ocr.TextObservation

	if sample.ImagePath != "" {
		ocrEngine := ocr.New()
		if !ocrEngine.IsSupported() {
			log.Printf("[%s] OCR not supported, falling back to text mode", sample.ID)
			return runTextDetection(sample)
		}
		jpegData, err := os.ReadFile(sample.ImagePath)
		if err != nil {
			log.Printf("[%s] read image: %v", sample.ID, err)
			return nil
		}
		observations, err = ocrEngine.RecognizeJPEGWithBoxes(jpegData, []string{"en-US", "zh-Hans"}, ocr.PriorityMid)
		if err != nil {
			log.Printf("[%s] OCR failed: %v", sample.ID, err)
			return nil
		}
	} else if sample.Text != "" {
		observations = textToObservations(sample.Text)
	}

	glinerDetections := detector.Detect(observations)
	regexDetections := pii.DetectWithRegex(observations)
	merged := pii.MergeDetections(glinerDetections, regexDetections)

	var preds []eval.Prediction
	for _, d := range merged {
		start, end := findOffsets(sample.Text, d.Text)
		preds = append(preds, eval.Prediction{
			Text:       d.Text,
			Category:   d.Category,
			Start:      start,
			End:        end,
			X:          d.X,
			Y:          d.Y,
			Width:      d.Width,
			Height:     d.Height,
			Confidence: d.Confidence,
		})
	}
	return preds
}

func runOPFRegexDetection(sample eval.Sample, detector pii.Detector) []eval.Prediction {
	var observations []ocr.TextObservation
	if sample.ImagePath != "" {
		ocrEngine := ocr.New()
		if !ocrEngine.IsSupported() {
			log.Printf("[%s] OCR not supported, falling back to text mode", sample.ID)
			return runTextDetection(sample)
		}
		jpegData, err := os.ReadFile(sample.ImagePath)
		if err != nil {
			log.Printf("[%s] read image: %v", sample.ID, err)
			return nil
		}
		observations, err = ocrEngine.RecognizeJPEGWithBoxes(jpegData, []string{"en-US", "zh-Hans"}, ocr.PriorityMid)
		if err != nil {
			log.Printf("[%s] OCR failed: %v", sample.ID, err)
			return nil
		}
	} else if sample.Text != "" {
		observations = textToObservations(sample.Text)
	}

	opfDetections := detector.Detect(observations)
	regexDetections := pii.DetectWithRegex(observations)
	merged := pii.MergeDetections(opfDetections, regexDetections)

	var preds []eval.Prediction
	for _, d := range merged {
		preds = append(preds, eval.Prediction{
			Text:       d.Text,
			Category:   d.Category,
			X:          d.X,
			Y:          d.Y,
			Width:      d.Width,
			Height:     d.Height,
			Confidence: d.Confidence,
		})
	}
	return preds
}

func textToObservations(text string) []ocr.TextObservation {
	lines := strings.Split(text, "\n")
	observations := make([]ocr.TextObservation, 0, len(lines))
	for i, line := range lines {
		if line == "" {
			continue
		}
		observations = append(observations, ocr.TextObservation{
			Text:       line,
			X:          0.05,
			Y:          0.05 + float64(i)*0.025,
			Width:      0.4,
			Height:     0.018,
			Confidence: 1.0,
		})
	}
	return observations
}

func findOffsets(fullText, substr string) (int, int) {
	idx := strings.Index(fullText, substr)
	if idx < 0 {
		idx = strings.Index(strings.ToLower(fullText), strings.ToLower(substr))
	}
	if idx < 0 {
		return 0, 0
	}
	return idx, idx + len(substr)
}
