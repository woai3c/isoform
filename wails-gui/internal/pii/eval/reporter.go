package eval

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// FormatTable writes a sklearn-style classification report to w.
func FormatTable(w io.Writer, report Report) {
	fmt.Fprintf(w, "\n%-20s %9s %9s %9s %9s\n", "", "precision", "recall", "f1-score", "support")
	fmt.Fprintf(w, "%s\n", strings.Repeat("-", 60))

	for _, m := range report.Categories {
		fmt.Fprintf(w, "%-20s %9.4f %9.4f %9.4f %9d\n",
			truncate(m.Category, 20), m.Precision, m.Recall, m.F1, m.Support)
	}

	fmt.Fprintf(w, "\n%-20s %9.4f %9.4f %9.4f %9d\n",
		"micro avg", report.MicroAvg.Precision, report.MicroAvg.Recall, report.MicroAvg.F1, report.MicroAvg.Support)
	fmt.Fprintf(w, "%-20s %9.4f %9.4f %9.4f %9d\n",
		"macro avg", report.MacroAvg.Precision, report.MacroAvg.Recall, report.MacroAvg.F1, report.MacroAvg.Support)
	fmt.Fprintf(w, "%-20s %9.4f %9.4f %9.4f %9d\n",
		"weighted avg", report.WeightedAvg.Precision, report.WeightedAvg.Recall, report.WeightedAvg.F1, report.WeightedAvg.Support)
	fmt.Fprintf(w, "\nTotal samples: %d\n", report.TotalSamples)
}

// FormatJSON writes the report as JSON.
func FormatJSON(w io.Writer, report Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}

// FormatMarkdown writes the report as a Markdown table.
func FormatMarkdown(w io.Writer, report Report) {
	fmt.Fprintf(w, "| Category | Precision | Recall | F1 | Support |\n")
	fmt.Fprintf(w, "|----------|-----------|--------|----|---------|\n")

	for _, m := range report.Categories {
		fmt.Fprintf(w, "| %s | %.4f | %.4f | %.4f | %d |\n",
			m.Category, m.Precision, m.Recall, m.F1, m.Support)
	}

	fmt.Fprintf(w, "| **micro avg** | %.4f | %.4f | %.4f | %d |\n",
		report.MicroAvg.Precision, report.MicroAvg.Recall, report.MicroAvg.F1, report.MicroAvg.Support)
	fmt.Fprintf(w, "| **macro avg** | %.4f | %.4f | %.4f | %d |\n",
		report.MacroAvg.Precision, report.MacroAvg.Recall, report.MacroAvg.F1, report.MacroAvg.Support)
	fmt.Fprintf(w, "| **weighted avg** | %.4f | %.4f | %.4f | %d |\n",
		report.WeightedAvg.Precision, report.WeightedAvg.Recall, report.WeightedAvg.F1, report.WeightedAvg.Support)
}

// CompareReports formats a side-by-side comparison of two reports.
func CompareReports(w io.Writer, baseline, candidate Report, baselineName, candidateName string) {
	fmt.Fprintf(w, "\n%-20s | %-27s | %-27s | %s\n",
		"Category",
		baselineName+" (P/R/F1)",
		candidateName+" (P/R/F1)",
		"Delta F1")
	fmt.Fprintf(w, "%s\n", strings.Repeat("-", 95))

	catMap := make(map[string]CategoryMetrics)
	for _, m := range baseline.Categories {
		catMap[m.Category] = m
	}

	for _, cm := range candidate.Categories {
		bm := catMap[cm.Category]
		delta := cm.F1 - bm.F1
		sign := "+"
		if delta < 0 {
			sign = ""
		}
		fmt.Fprintf(w, "%-20s | %6.3f / %6.3f / %6.3f    | %6.3f / %6.3f / %6.3f    | %s%.4f\n",
			truncate(cm.Category, 20),
			bm.Precision, bm.Recall, bm.F1,
			cm.Precision, cm.Recall, cm.F1,
			sign, delta)
	}

	deltaF1 := candidate.WeightedAvg.F1 - baseline.WeightedAvg.F1
	sign := "+"
	if deltaF1 < 0 {
		sign = ""
	}
	fmt.Fprintf(w, "\n%-20s | %6.3f / %6.3f / %6.3f    | %6.3f / %6.3f / %6.3f    | %s%.4f\n",
		"WEIGHTED AVG",
		baseline.WeightedAvg.Precision, baseline.WeightedAvg.Recall, baseline.WeightedAvg.F1,
		candidate.WeightedAvg.Precision, candidate.WeightedAvg.Recall, candidate.WeightedAvg.F1,
		sign, deltaF1)
}

// FormatScorecard writes Something's product-level privacy scorecard.
func FormatScorecard(w io.Writer, report Report, name string) {
	sc := report.Scorecard
	fmt.Fprintf(w, "\n")
	fmt.Fprintf(w, "  Something Privacy Scorecard: %s\n", name)
	fmt.Fprintf(w, "  %s\n", strings.Repeat("-", 56))
	fmt.Fprintf(w, "\n")
	fmt.Fprintf(w, "  %-34s %6d\n", "Critical leaks", sc.CriticalLeakCount)
	fmt.Fprintf(w, "  %-34s %6d\n", "High-risk leaks", sc.HighRiskLeakCount)
	fmt.Fprintf(w, "  %-34s %6.2f\n", "Risk-weighted protection", sc.RiskWeightedProtectionScore)
	fmt.Fprintf(w, "  %-34s %6.2f\n", "Risk-weighted exposure", sc.RiskWeightedExposureRate)
	fmt.Fprintf(w, "  %-34s %6.2f\n", "Context preservation", sc.ContextPreservationScore)
	fmt.Fprintf(w, "  %-34s %6.2f\n", "Decision score", sc.DecisionScore)
	fmt.Fprintf(w, "\n")
	fmt.Fprintf(w, "  Samples: %d | Target annotations: %d | Over-protection count: %d\n",
		report.TotalSamples, sc.TargetSupport, sc.OverProtectionCount)
	if report.Performance.SampleCount > 0 {
		pm := report.Performance
		fmt.Fprintf(w, "  Performance: init %.0f ms | mean %.1f ms/sample | p95 %.1f ms/sample | %.2f samples/s\n",
			pm.InitMs, pm.MeanSampleMs, pm.P95SampleMs, pm.SamplesPerSecond)
	}

	if len(sc.Gates) > 0 {
		fmt.Fprintf(w, "\n  Safety Gates:\n")
		fmt.Fprintf(w, "  %-34s %8s %12s %10s\n", "Gate", "Status", "Value", "Threshold")
		fmt.Fprintf(w, "  %s\n", strings.Repeat("-", 70))
		for _, gate := range sc.Gates {
			fmt.Fprintf(w, "  %-34s %8s %12.4f %3s %-6.4f\n",
				truncate(gate.Name, 34), passFail(gate.Passed), gate.Value, gate.Direction, gate.Threshold)
		}
	}
}

// CompareScorecards formats a side-by-side comparison of Something scorecards.
func CompareScorecards(w io.Writer, results []NamedReport) {
	fmt.Fprintf(w, "\n  Something Privacy Scorecard\n")
	fmt.Fprintf(w, "  %s\n\n", strings.Repeat("=", 60))

	fmt.Fprintf(w, "  %-30s", "")
	for _, r := range results {
		fmt.Fprintf(w, " %12s", truncate(r.Name, 12))
	}
	fmt.Fprintf(w, "\n")
	fmt.Fprintf(w, "  %s\n", strings.Repeat("-", 30+13*len(results)))

	writeIntRow := func(label string, value func(SomethingScorecard) int) {
		fmt.Fprintf(w, "  %-30s", label)
		for _, r := range results {
			fmt.Fprintf(w, " %12d", value(r.Report.Scorecard))
		}
		fmt.Fprintf(w, "\n")
	}
	writeFloatRow := func(label string, value func(SomethingScorecard) float64) {
		fmt.Fprintf(w, "  %-30s", label)
		for _, r := range results {
			fmt.Fprintf(w, " %12.2f", value(r.Report.Scorecard))
		}
		fmt.Fprintf(w, "\n")
	}
	writeIntRow("Critical leaks", func(sc SomethingScorecard) int { return sc.CriticalLeakCount })
	writeIntRow("High-risk leaks", func(sc SomethingScorecard) int { return sc.HighRiskLeakCount })
	writeFloatRow("Risk-weighted protection", func(sc SomethingScorecard) float64 { return sc.RiskWeightedProtectionScore })
	writeFloatRow("Context preservation", func(sc SomethingScorecard) float64 { return sc.ContextPreservationScore })
	writeFloatRow("Decision score", func(sc SomethingScorecard) float64 { return sc.DecisionScore })
	writePerformanceRow(w, "Mean ms/sample", results, func(pm PerformanceMetrics) float64 { return pm.MeanSampleMs })
	writePerformanceRow(w, "P95 ms/sample", results, func(pm PerformanceMetrics) float64 { return pm.P95SampleMs })
	writePerformanceRow(w, "Samples/sec", results, func(pm PerformanceMetrics) float64 { return pm.SamplesPerSecond })
	fmt.Fprintf(w, "\n")
}

func writePerformanceRow(w io.Writer, label string, results []NamedReport, value func(PerformanceMetrics) float64) {
	fmt.Fprintf(w, "  %-30s", label)
	for _, r := range results {
		if r.Report.Performance.SampleCount == 0 {
			fmt.Fprintf(w, " %12s", "-")
			continue
		}
		fmt.Fprintf(w, " %12.2f", value(r.Report.Performance))
	}
	fmt.Fprintf(w, "\n")
}

// NamedReport pairs a report with a display name for comparison tables.
type NamedReport struct {
	Name   string
	Report Report
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-1] + "."
}

func passFail(ok bool) string {
	if ok {
		return "pass"
	}
	return "fail"
}
