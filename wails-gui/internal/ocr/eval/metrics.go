package eval

import (
	"sort"
	"strings"
	"unicode/utf8"
)

// CER computes the Character Error Rate between ground truth and prediction.
// CER = edit_distance(ground, pred) / len(ground).
// Returns 0 for empty ground truth with empty prediction, 1.0 for empty ground
// truth with non-empty prediction.
func CER(ground, pred string) float64 {
	groundRunes := []rune(strings.TrimSpace(ground))
	predRunes := []rune(strings.TrimSpace(pred))

	if len(groundRunes) == 0 {
		if len(predRunes) == 0 {
			return 0
		}
		return 1.0
	}

	dist := editDistance(groundRunes, predRunes)
	cer := float64(dist) / float64(len(groundRunes))
	if cer > 1.0 {
		return 1.0
	}
	return cer
}

// WER computes the Word Error Rate between ground truth and prediction.
// For CJK text, each character is treated as a word.
func WER(ground, pred string) float64 {
	groundWords := tokenize(strings.TrimSpace(ground))
	predWords := tokenize(strings.TrimSpace(pred))

	if len(groundWords) == 0 {
		if len(predWords) == 0 {
			return 0
		}
		return 1.0
	}

	dist := editDistanceStrings(groundWords, predWords)
	wer := float64(dist) / float64(len(groundWords))
	if wer > 1.0 {
		return 1.0
	}
	return wer
}

// editDistance computes the Levenshtein distance between two rune slices.
func editDistance(a, b []rune) int {
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}

	prev := make([]int, lb+1)
	curr := make([]int, lb+1)

	for j := 0; j <= lb; j++ {
		prev[j] = j
	}

	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = minInt(
				prev[j]+1,
				minInt(curr[j-1]+1, prev[j-1]+cost),
			)
		}
		prev, curr = curr, prev
	}
	return prev[lb]
}

// editDistanceStrings computes Levenshtein distance between two string slices.
func editDistanceStrings(a, b []string) int {
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}

	prev := make([]int, lb+1)
	curr := make([]int, lb+1)

	for j := 0; j <= lb; j++ {
		prev[j] = j
	}

	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = minInt(
				prev[j]+1,
				minInt(curr[j-1]+1, prev[j-1]+cost),
			)
		}
		prev, curr = curr, prev
	}
	return prev[lb]
}

// tokenize splits text into words. CJK characters are treated as individual tokens.
func tokenize(s string) []string {
	var tokens []string
	var current strings.Builder

	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size <= 1 {
			i++
			continue
		}

		if isCJKRune(r) {
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
			tokens = append(tokens, string(r))
		} else if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
		} else {
			current.WriteRune(r)
		}
		i += size
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	return tokens
}

func isCJKRune(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) || // CJK Unified Ideographs
		(r >= 0x3400 && r <= 0x4DBF) || // CJK Extension A
		(r >= 0x20000 && r <= 0x2A6DF) || // CJK Extension B
		(r >= 0x2A700 && r <= 0x2B73F) || // CJK Extension C
		(r >= 0x2B740 && r <= 0x2B81F) || // CJK Extension D
		(r >= 0xF900 && r <= 0xFAFF) || // CJK Compatibility Ideographs
		(r >= 0x3000 && r <= 0x303F) || // CJK Symbols and Punctuation
		(r >= 0xFF00 && r <= 0xFFEF) // Halfwidth and Fullwidth Forms
}

// Score evaluates OCR results against ground truth for all samples.
func Score(samples []Sample, results map[string]OCRResult) Report {
	var report Report
	report.TotalSamples = len(samples)

	langGroups := make(map[string][]SampleMetrics)
	catGroups := make(map[string][]SampleMetrics)

	for _, sample := range samples {
		result := results[sample.ID]
		cer := CER(sample.GroundTruth, result.Text)
		wer := WER(sample.GroundTruth, result.Text)

		sm := SampleMetrics{
			ID:         sample.ID,
			Language:   sample.Language,
			Category:   sample.Category,
			CER:        cer,
			WER:        wer,
			Confidence: result.Confidence,
			GroundLen:  utf8.RuneCountInString(sample.GroundTruth),
			PredLen:    utf8.RuneCountInString(result.Text),
		}
		report.Samples = append(report.Samples, sm)

		langGroups[sample.Language] = append(langGroups[sample.Language], sm)
		catGroups[sample.Category] = append(catGroups[sample.Category], sm)
	}

	report.Overall = aggregateGroup("overall", report.Samples)

	for _, lang := range sortedKeys(langGroups) {
		report.ByLanguage = append(report.ByLanguage, aggregateGroup(lang, langGroups[lang]))
	}
	for _, cat := range sortedKeys(catGroups) {
		report.ByCategory = append(report.ByCategory, aggregateGroup(cat, catGroups[cat]))
	}

	return report
}

func aggregateGroup(name string, metrics []SampleMetrics) GroupMetrics {
	n := len(metrics)
	if n == 0 {
		return GroupMetrics{Group: name}
	}

	var sumCER, sumWER, sumConf float64
	cers := make([]float64, n)
	for i, m := range metrics {
		sumCER += m.CER
		sumWER += m.WER
		sumConf += m.Confidence
		cers[i] = m.CER
	}

	sort.Float64s(cers)
	var medianCER float64
	if n%2 == 0 {
		medianCER = (cers[n/2-1] + cers[n/2]) / 2
	} else {
		medianCER = cers[n/2]
	}

	return GroupMetrics{
		Group:     name,
		Samples:   n,
		MeanCER:   sumCER / float64(n),
		MedianCER: medianCER,
		MeanWER:   sumWER / float64(n),
		MeanConf:  sumConf / float64(n),
	}
}

func sortedKeys(m map[string][]SampleMetrics) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
