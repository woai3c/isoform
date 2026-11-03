package eval

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// FormatTable writes a human-readable OCR evaluation report to w.
func FormatTable(w io.Writer, report Report) {
	fmt.Fprintf(w, "\n  OCR Evaluation Report\n")
	fmt.Fprintf(w, "  %s\n\n", strings.Repeat("=", 60))

	fmt.Fprintf(w, "  %-14s %8s %10s %10s %10s %8s\n",
		"Group", "Samples", "Mean CER", "Med CER", "Mean WER", "Conf")
	fmt.Fprintf(w, "  %s\n", strings.Repeat("-", 66))

	fmt.Fprintf(w, "  %-14s %8d %9.4f%% %9.4f%% %9.4f%% %8.3f\n",
		"OVERALL",
		report.Overall.Samples,
		report.Overall.MeanCER*100,
		report.Overall.MedianCER*100,
		report.Overall.MeanWER*100,
		report.Overall.MeanConf)

	if len(report.ByLanguage) > 0 {
		fmt.Fprintf(w, "\n  By Language:\n")
		fmt.Fprintf(w, "  %s\n", strings.Repeat("-", 66))
		for _, g := range report.ByLanguage {
			fmt.Fprintf(w, "  %-14s %8d %9.4f%% %9.4f%% %9.4f%% %8.3f\n",
				g.Group, g.Samples, g.MeanCER*100, g.MedianCER*100, g.MeanWER*100, g.MeanConf)
		}
	}

	if len(report.ByCategory) > 0 {
		fmt.Fprintf(w, "\n  By Category:\n")
		fmt.Fprintf(w, "  %s\n", strings.Repeat("-", 66))
		for _, g := range report.ByCategory {
			fmt.Fprintf(w, "  %-14s %8d %9.4f%% %9.4f%% %9.4f%% %8.3f\n",
				g.Group, g.Samples, g.MeanCER*100, g.MedianCER*100, g.MeanWER*100, g.MeanConf)
		}
	}

	fmt.Fprintf(w, "\n  Total samples: %d\n", report.TotalSamples)
}

// FormatErrors writes per-sample error analysis for samples above a CER threshold.
func FormatErrors(w io.Writer, report Report, threshold float64) {
	fmt.Fprintf(w, "\n  Error Analysis (CER > %.0f%%)\n", threshold*100)
	fmt.Fprintf(w, "  %s\n\n", strings.Repeat("-", 50))

	count := 0
	for _, s := range report.Samples {
		if s.CER <= threshold {
			continue
		}
		count++
		fmt.Fprintf(w, "  [%s] lang=%s cat=%s CER=%.2f%% WER=%.2f%% conf=%.3f ground_len=%d pred_len=%d\n",
			s.ID, s.Language, s.Category, s.CER*100, s.WER*100, s.Confidence, s.GroundLen, s.PredLen)
	}

	if count == 0 {
		fmt.Fprintf(w, "  No samples above threshold.\n")
	}
}

// FormatJSON writes the report as JSON.
func FormatJSON(w io.Writer, report Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}

// CompareReports formats a side-by-side comparison of two reports.
func CompareReports(w io.Writer, baseline, candidate Report, baselineName, candidateName string) {
	fmt.Fprintf(w, "\n  OCR Comparison: %s vs %s\n", baselineName, candidateName)
	fmt.Fprintf(w, "  %s\n\n", strings.Repeat("=", 60))

	fmt.Fprintf(w, "  %-14s | %-20s | %-20s | %s\n",
		"Group", baselineName+" CER", candidateName+" CER", "Delta")
	fmt.Fprintf(w, "  %s\n", strings.Repeat("-", 70))

	printRow := func(name string, base, cand GroupMetrics) {
		delta := cand.MeanCER - base.MeanCER
		sign := "+"
		if delta <= 0 {
			sign = ""
		}
		fmt.Fprintf(w, "  %-14s | %18.4f%% | %18.4f%% | %s%.4f%%\n",
			name, base.MeanCER*100, cand.MeanCER*100, sign, delta*100)
	}

	printRow("OVERALL", baseline.Overall, candidate.Overall)

	baseByLang := make(map[string]GroupMetrics)
	for _, g := range baseline.ByLanguage {
		baseByLang[g.Group] = g
	}
	for _, g := range candidate.ByLanguage {
		if base, ok := baseByLang[g.Group]; ok {
			printRow(g.Group, base, g)
		} else {
			fmt.Fprintf(w, "  %-14s | %18s | %18.4f%% | N/A\n",
				g.Group, "N/A", g.MeanCER*100)
		}
	}
}

// SaveReport persists an evaluation report to a JSON file.
func SaveReport(path string, report Report) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

// LoadReport loads a previously saved report for comparison.
func LoadReport(path string) (*Report, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read report: %w", err)
	}
	var r Report
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("parse report: %w", err)
	}
	return &r, nil
}

// LoadDataset reads an OCR evaluation dataset from a JSON file.
func LoadDataset(path string) (*Dataset, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read dataset: %w", err)
	}
	var ds Dataset
	if err := json.Unmarshal(data, &ds); err != nil {
		return nil, fmt.Errorf("parse dataset: %w", err)
	}
	return &ds, nil
}

// SaveDataset writes a dataset to a JSON file.
func SaveDataset(path string, ds *Dataset) error {
	data, err := json.MarshalIndent(ds, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal dataset: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}
