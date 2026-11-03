package eval

import (
	"fmt"
	"io"
	"strings"
)

// DebugResult holds per-sample evaluation detail for debugging.
type DebugResult struct {
	SampleID string
	TruePos  []Prediction
	FalsePos []Prediction
	FalseNeg []Annotation
}

// DebugScore runs evaluation and returns per-sample debug info.
func DebugScore(samples []Sample, predictions map[string][]Prediction, opts ScoreOptions) []DebugResult {
	var results []DebugResult

	for _, sample := range samples {
		preds := predictions[sample.ID]
		dr := DebugResult{SampleID: sample.ID}

		matched := make([]bool, len(sample.Annotations))
		predMatched := make([]bool, len(preds))

		for pi, pred := range preds {
			for ai, ann := range sample.Annotations {
				if matched[ai] {
					continue
				}
				if !categoryMatch(pred.Category, ann.Category) {
					continue
				}
				if isMatch(ann, pred, opts) {
					matched[ai] = true
					predMatched[pi] = true
					dr.TruePos = append(dr.TruePos, pred)
					break
				}
			}
		}

		for pi, pred := range preds {
			if !predMatched[pi] {
				dr.FalsePos = append(dr.FalsePos, pred)
			}
		}

		for ai, ann := range sample.Annotations {
			if !matched[ai] {
				dr.FalseNeg = append(dr.FalseNeg, ann)
			}
		}

		if len(dr.FalsePos) > 0 || len(dr.FalseNeg) > 0 {
			results = append(results, dr)
		}
	}

	return results
}

// FormatDebug prints detailed error analysis.
func FormatDebug(w io.Writer, results []DebugResult) {
	if len(results) == 0 {
		fmt.Fprintf(w, "\nAll predictions matched ground truth perfectly.\n")
		return
	}

	fmt.Fprintf(w, "\n=== Error Analysis (%d samples with errors) ===\n", len(results))
	for _, dr := range results {
		fmt.Fprintf(w, "\n[%s]\n", dr.SampleID)
		for _, fp := range dr.FalsePos {
			fmt.Fprintf(w, "  FP: category=%-20s text=%q\n", fp.Category, truncateText(fp.Text, 50))
		}
		for _, fn := range dr.FalseNeg {
			fmt.Fprintf(w, "  FN: category=%-20s text=%q\n", fn.Category, truncateText(fn.Text, 50))
		}
	}
	fmt.Fprintf(w, "\n")

	fpByCategory := make(map[string]int)
	fnByCategory := make(map[string]int)
	for _, dr := range results {
		for _, fp := range dr.FalsePos {
			fpByCategory[fp.Category]++
		}
		for _, fn := range dr.FalseNeg {
			fnByCategory[fn.Category]++
		}
	}

	fmt.Fprintf(w, "=== Error Summary ===\n")
	fmt.Fprintf(w, "%-20s %5s %5s\n", "Category", "FP", "FN")
	fmt.Fprintf(w, "%s\n", strings.Repeat("-", 32))
	allCats := collectCategories(fpByCategory, fnByCategory)
	for _, cat := range allCats {
		fmt.Fprintf(w, "%-20s %5d %5d\n", cat, fpByCategory[cat], fnByCategory[cat])
	}
}

func truncateText(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
