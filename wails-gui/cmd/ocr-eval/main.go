package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"wails-gui/internal/ocr/eval"
	"wails-gui/internal/platform/ocr"
)

func main() {
	datasetPath := flag.String("dataset", "", "path to evaluation dataset JSON")
	outputPath := flag.String("output", "", "path to save report JSON")
	comparePath := flag.String("compare", "", "path to baseline report JSON for comparison")
	format := flag.String("format", "table", "output format: table, json")
	genMode := flag.String("gen-mode", "", "generate dataset: 'synthetic' (requires -font) or 'native' (macOS WebKit render)")
	genDir := flag.String("gen-dir", "", "directory for generated images (default: temp dir)")
	fontPath := flag.String("font", "", "path to a .ttf/.otf font for synthetic image generation")
	errorThreshold := flag.Float64("error-threshold", 0.05, "CER threshold for error analysis (0.05 = 5%)")
	langs := flag.String("langs", "en-US,zh-Hans", "comma-separated OCR languages")
	jpegQuality := flag.Int("jpeg-quality", 0, "re-encode input at this JPEG quality before OCR (0 = use original)")
	normalize := flag.Bool("normalize", false, "apply fullwidth-to-halfwidth normalization on OCR output")
	genOnly := flag.Bool("gen", false, "only generate dataset, do not run OCR")
	debug := flag.Bool("debug", false, "show per-sample error details")
	dump := flag.Bool("dump", false, "dump ground truth vs OCR output for each sample")
	flag.Parse()

	ocrLangs := strings.Split(*langs, ",")

	if *datasetPath == "" && *genMode == "" {
		fmt.Fprintf(os.Stderr, "Usage:\n")
		fmt.Fprintf(os.Stderr, "  ocr-eval -dataset <path.json> [-output report.json] [-compare baseline.json]\n")
		fmt.Fprintf(os.Stderr, "  ocr-eval -gen-mode native [-gen] [-gen-dir <dir>]\n")
		fmt.Fprintf(os.Stderr, "  ocr-eval -gen-mode synthetic -font <path.ttf> [-gen] [-gen-dir <dir>]\n\n")
		fmt.Fprintf(os.Stderr, "Evaluate OCR recognition quality against annotated ground truth.\n\n")
		fmt.Fprintf(os.Stderr, "Gen modes:\n")
		fmt.Fprintf(os.Stderr, "  native     Render HTML via macOS WebKit (CoreText fonts, subpixel AA)\n")
		fmt.Fprintf(os.Stderr, "  synthetic  Render text via Go font rasterizer (requires -font)\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		flag.PrintDefaults()
		os.Exit(1)
	}

	var ds *eval.Dataset

	if *datasetPath != "" {
		loaded, err := eval.LoadDataset(*datasetPath)
		if err != nil {
			log.Fatalf("load dataset: %v", err)
		}
		ds = loaded
	} else {
		dir := *genDir
		if dir == "" {
			var err error
			dir, err = os.MkdirTemp("", "ocr-eval-*")
			if err != nil {
				log.Fatalf("create temp dir: %v", err)
			}
			if *genOnly {
				fmt.Fprintf(os.Stderr, "Generated dataset dir: %s\n", dir)
			}
		}

		var err error
		switch *genMode {
		case "native":
			ds, err = generateNativeDataset(dir)
		case "synthetic":
			if *fontPath == "" {
				log.Fatal("synthetic mode requires -font <path.ttf>")
			}
			fontData, fontErr := os.ReadFile(*fontPath)
			if fontErr != nil {
				log.Fatalf("read font: %v", fontErr)
			}
			ds, err = generateDataset(dir, fontData)
		default:
			log.Fatalf("unknown gen-mode %q (use native or synthetic)", *genMode)
		}
		if err != nil {
			log.Fatalf("generate dataset: %v", err)
		}

		dsPath := filepath.Join(dir, "dataset.json")
		if err := eval.SaveDataset(dsPath, ds); err != nil {
			log.Fatalf("save dataset: %v", err)
		}
		fmt.Fprintf(os.Stderr, "Generated %d samples -> %s\n", len(ds.Samples), dsPath)

		if *genOnly {
			return
		}
	}

	fmt.Fprintf(os.Stderr, "Dataset %q: %d samples\n", ds.Name, len(ds.Samples))

	engine := ocr.New()
	if !engine.IsSupported() {
		log.Fatal("OCR engine not supported on this platform")
	}

	results := make(map[string]eval.OCRResult)
	for i, sample := range ds.Samples {
		jpegData, err := os.ReadFile(sample.ImagePath)
		if err != nil {
			log.Printf("[%s] read image: %v", sample.ID, err)
			continue
		}

		if *jpegQuality > 0 {
			jpegData, err = reencodeJPEG(jpegData, *jpegQuality)
			if err != nil {
				log.Printf("[%s] re-encode at q=%d: %v", sample.ID, *jpegQuality, err)
				continue
			}
		}

		observations, err := engine.RecognizeJPEGWithBoxes(jpegData, ocrLangs, ocr.PriorityMid)
		if err != nil {
			log.Printf("[%s] OCR failed: %v", sample.ID, err)
			continue
		}

		var text strings.Builder
		var totalConf float64
		var count int
		for _, obs := range observations {
			if obs.Text == "" {
				continue
			}
			if text.Len() > 0 {
				text.WriteByte('\n')
			}
			text.WriteString(obs.Text)
			totalConf += obs.Confidence
			count++
		}

		conf := 0.0
		if count > 0 {
			conf = totalConf / float64(count)
		}

		ocrText := text.String()
		if *normalize {
			ocrText = fullwidthToHalfwidth(ocrText)
		}

		results[sample.ID] = eval.OCRResult{
			Text:       ocrText,
			Confidence: conf,
		}

		fmt.Fprintf(os.Stderr, "  [%d/%d] %s: %d chars recognized (conf=%.3f)\n",
			i+1, len(ds.Samples), sample.ID, len(text.String()), conf)
	}

	report := eval.Score(ds.Samples, results)

	switch *format {
	case "table":
		eval.FormatTable(os.Stdout, report)
		if *debug {
			eval.FormatErrors(os.Stdout, report, *errorThreshold)
		}
		if *dump {
			fmt.Fprintf(os.Stdout, "\n  Ground Truth vs OCR Output\n")
			fmt.Fprintf(os.Stdout, "  %s\n", strings.Repeat("=", 60))
			for _, sample := range ds.Samples {
				r := results[sample.ID]
				fmt.Fprintf(os.Stdout, "\n  [%s] lang=%s cat=%s conf=%.3f\n", sample.ID, sample.Language, sample.Category, r.Confidence)
				fmt.Fprintf(os.Stdout, "  GROUND: %q\n", sample.GroundTruth)
				fmt.Fprintf(os.Stdout, "  OCR:    %q\n", r.Text)
			}
		}
	case "json":
		eval.FormatJSON(os.Stdout, report)
	default:
		log.Fatalf("unknown format %q", *format)
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
		eval.CompareReports(os.Stdout, *baseline, report, "baseline", "candidate")
	}
}
