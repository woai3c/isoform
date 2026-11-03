package eval

import (
	"strings"
	"unicode"
)

// MatchMode controls how predictions are matched to annotations.
type MatchMode int

const (
	// MatchExact requires the predicted span to exactly match the annotation.
	MatchExact MatchMode = iota
	// MatchOverlap requires any character overlap between prediction and annotation.
	MatchOverlap
	// MatchCategoryOnly only checks that the category matches (for bbox-based evaluation with IoU).
	MatchCategoryOnly
)

// CategoryMetrics holds precision/recall/F1 for a single category.
type CategoryMetrics struct {
	Category  string
	TP        int
	FP        int
	FN        int
	Precision float64
	Recall    float64
	F1        float64
	Support   int // total ground truth count
}

// BenchmarkMetrics holds category-agnostic compatibility diagnostics.
//
// These numbers are useful for candidate comparison and detector debugging, but
// they are not Something's product decision scorecard: UtilityScore is only a
// precision proxy, and OverallScore weights privacy and over-redaction equally.
type BenchmarkMetrics struct {
	// PrivacyLeakRate is the fraction of PII that goes undetected (= 1 - Recall).
	// Lower is better. 0.0 means no leaks.
	PrivacyLeakRate float64

	// PrivacyBlockRate is the fraction of PII successfully blocked (= Recall).
	// Higher is better. 1.0 means all PII blocked.
	PrivacyBlockRate float64

	// UtilityScore measures how much non-PII content survives redaction.
	// Higher is better. 1.0 means no over-redaction.
	// Computed as: 1 - (FP / (TP + FP + TotalNonPII)), approximated when
	// TotalNonPII is unknown as: TP / (TP + FP) = Precision.
	UtilityScore float64

	// OverallScore balances privacy protection with utility preservation.
	// Harmonic mean of PrivacyBlockRate and UtilityScore.
	OverallScore float64
}

// SomethingScorecard captures the product-level decision criteria for
// Something's PII protection pipeline. It is privacy-first: critical/high-risk
// misses drive the protection score, while context preservation estimates the
// amount of non-PII text or screen area lost to false-positive masking.
type SomethingScorecard struct {
	CriticalLeakCount int
	HighRiskLeakCount int
	TargetLeakCount   int

	TargetSupport        int
	RiskWeightedSupport  float64
	RiskWeightedLeakCost float64

	CriticalProtectionRate float64
	HighRiskProtectionRate float64

	RiskWeightedExposureRate    float64
	RiskWeightedProtectionScore float64

	OverProtectionCount      int
	OverProtectionExtent     float64
	ContextLossRate          float64
	ContextPreservationScore float64

	DecisionScore     float64
	ReadyForUserClaim bool
	Gates             []SafetyGate
}

// SafetyGate is a hard product-readiness gate. A candidate can still have a
// useful DecisionScore when a gate fails, but it should not be used for broad
// user-facing privacy claims until every gate passes.
type SafetyGate struct {
	Name      string
	Passed    bool
	Value     float64
	Threshold float64
	Direction string
}

// RiskTier classifies PII categories by severity of exposure.
type RiskTier int

const (
	TierCritical RiskTier = iota // Direct financial loss or security breach
	TierHigh                     // Social engineering or targeted harassment
	TierMedium                   // Low standalone risk, combinable for re-identification
)

// TierMetrics holds compatibility metrics for a single risk tier.
type TierMetrics struct {
	Tier       string
	BlockRate  float64
	Utility    float64
	Overall    float64
	Support    int
	TP, FP, FN int
}

// riskTierMap maps PII categories to risk tiers.
var riskTierMap = map[string]RiskTier{
	// Critical: direct financial loss or security breach
	"password":          TierCritical,
	"api_key":           TierCritical,
	"credit_card":       TierCritical,
	"cvv":               TierCritical,
	"government_id":     TierCritical,
	"one_time_password": TierCritical,
	"secret":            TierCritical,
	"credential":        TierCritical,
	"session_id":        TierCritical,
	"financial_info":    TierHigh,
	// High: social engineering, targeted harassment, or access to private systems
	"private_email":      TierHigh,
	"private_phone":      TierHigh,
	"street_address":     TierHigh,
	"private_address":    TierHigh,
	"private_link":       TierHigh,
	"private_url":        TierHigh,
	"device_id":          TierHigh,
	"device_identifier":  TierHigh,
	"account_number":     TierHigh,
	"account_identifier": TierHigh,
	"contact_info":       TierHigh,
	"license_plate":      TierHigh,
	"location_info":      TierHigh,
	"private_org":        TierHigh,
	// Medium: low standalone risk, combinable for re-identification
	"private_person":   TierMedium,
	"person_identity":  TierMedium,
	"private_date":     TierMedium,
	"date_time":        TierMedium,
	"org_context":      TierMedium,
	"sensitive_record": TierMedium,
	"activity_context": TierMedium,
}

func tierName(t RiskTier) string {
	switch t {
	case TierCritical:
		return "Critical"
	case TierHigh:
		return "High"
	case TierMedium:
		return "Medium"
	default:
		return "Unknown"
	}
}

func categoryTier(category string) RiskTier {
	if t, ok := riskTierMap[category]; ok {
		return t
	}
	return TierMedium
}

// Report holds the full evaluation results.
type Report struct {
	Categories   []CategoryMetrics
	MacroAvg     CategoryMetrics
	WeightedAvg  CategoryMetrics
	MicroAvg     CategoryMetrics
	Benchmark    BenchmarkMetrics
	Scorecard    SomethingScorecard
	Performance  PerformanceMetrics
	Tiers        []TierMetrics
	TotalSamples int
}

// ScoreOptions configures the scoring behavior.
type ScoreOptions struct {
	Mode                  MatchMode
	IoUThreshold          float64 // for bbox mode, default 0.5
	BboxCoverageThreshold float64 // annotation coverage required for bbox match, default 0.7
	CaseSensitive         bool
	PartialCredit         bool // count partial overlaps as 0.5 TP
}

func DefaultScoreOptions() ScoreOptions {
	return ScoreOptions{
		Mode:                  MatchOverlap,
		IoUThreshold:          0.5,
		BboxCoverageThreshold: 0.7,
	}
}

// Score evaluates predictions against annotations for a set of samples.
func Score(samples []Sample, predictions map[string][]Prediction, opts ScoreOptions) Report {
	categoryTP := make(map[string]int)
	categoryFP := make(map[string]int)
	categoryFN := make(map[string]int)

	for _, sample := range samples {
		preds := predictions[sample.ID]
		matchAnnotations(sample.Annotations, preds, opts, categoryTP, categoryFP, categoryFN)
	}

	allCategories := collectCategories(categoryTP, categoryFP, categoryFN)

	var report Report
	report.TotalSamples = len(samples)

	var totalTP, totalFP, totalFN int
	var macroP, macroR, macroF1 float64
	var weightedP, weightedR, weightedF1 float64
	var totalSupport int

	for _, cat := range allCategories {
		tp := categoryTP[cat]
		fp := categoryFP[cat]
		fn := categoryFN[cat]
		support := tp + fn

		m := CategoryMetrics{
			Category: cat,
			TP:       tp,
			FP:       fp,
			FN:       fn,
			Support:  support,
		}
		if tp+fp > 0 {
			m.Precision = float64(tp) / float64(tp+fp)
		}
		if tp+fn > 0 {
			m.Recall = float64(tp) / float64(tp+fn)
		}
		if m.Precision+m.Recall > 0 {
			m.F1 = 2 * m.Precision * m.Recall / (m.Precision + m.Recall)
		}

		report.Categories = append(report.Categories, m)

		totalTP += tp
		totalFP += fp
		totalFN += fn
		macroP += m.Precision
		macroR += m.Recall
		macroF1 += m.F1
		weightedP += m.Precision * float64(support)
		weightedR += m.Recall * float64(support)
		weightedF1 += m.F1 * float64(support)
		totalSupport += support
	}

	n := float64(len(allCategories))
	if n > 0 {
		report.MacroAvg = CategoryMetrics{
			Category:  "macro avg",
			Precision: macroP / n,
			Recall:    macroR / n,
			F1:        macroF1 / n,
			Support:   totalSupport,
		}
	}

	if totalSupport > 0 {
		report.WeightedAvg = CategoryMetrics{
			Category:  "weighted avg",
			Precision: weightedP / float64(totalSupport),
			Recall:    weightedR / float64(totalSupport),
			F1:        weightedF1 / float64(totalSupport),
			Support:   totalSupport,
		}
	}

	if totalTP+totalFP > 0 || totalTP+totalFN > 0 {
		microP := float64(0)
		microR := float64(0)
		if totalTP+totalFP > 0 {
			microP = float64(totalTP) / float64(totalTP+totalFP)
		}
		if totalTP+totalFN > 0 {
			microR = float64(totalTP) / float64(totalTP+totalFN)
		}
		microF1 := float64(0)
		if microP+microR > 0 {
			microF1 = 2 * microP * microR / (microP + microR)
		}
		report.MicroAvg = CategoryMetrics{
			Category:  "micro avg",
			Precision: microP,
			Recall:    microR,
			F1:        microF1,
			Support:   totalSupport,
		}
	}

	// Compute compatibility metrics using category-agnostic matching.
	// In production, a PII region is protected regardless of what category the
	// detector assigns. An SSN detected as "credit_card" is still masked.
	agnosticTP, agnosticFP, agnosticFN := scoreCategoryAgnostic(samples, predictions, opts)
	report.Benchmark = computeBenchmark(agnosticTP, agnosticFP, agnosticFN)
	report.Scorecard = scoreSomethingScorecard(samples, predictions, opts)

	// Compute per-risk-tier metrics.
	report.Tiers = scoreTiers(samples, predictions, opts)

	return report
}

// scoreTiers computes benchmark metrics per risk tier (Critical/High/Medium).
func scoreTiers(samples []Sample, predictions map[string][]Prediction, opts ScoreOptions) []TierMetrics {
	tierTP := map[RiskTier]int{}
	tierFP := map[RiskTier]int{}
	tierFN := map[RiskTier]int{}

	for _, sample := range samples {
		preds := predictions[sample.ID]
		matched, predMatched := matchCategoryAgnosticSample(sample.Annotations, preds, opts)

		for pi, pred := range preds {
			if !predMatched[pi] {
				tier := categoryTier(pred.Category)
				tierFP[tier]++
			}
		}
		for ai, ann := range sample.Annotations {
			tier := categoryTier(ann.Category)
			if matched[ai] {
				tierTP[tier]++
			} else {
				tierFN[tier]++
			}
		}
	}

	var tiers []TierMetrics
	for _, rt := range []RiskTier{TierCritical, TierHigh, TierMedium} {
		tp, fp, fn := tierTP[rt], tierFP[rt], tierFN[rt]
		support := tp + fn
		if support == 0 && fp == 0 {
			continue
		}
		bm := computeBenchmark(tp, fp, fn)
		tiers = append(tiers, TierMetrics{
			Tier:      tierName(rt),
			BlockRate: bm.PrivacyBlockRate,
			Utility:   bm.UtilityScore,
			Overall:   bm.OverallScore,
			Support:   support,
			TP:        tp,
			FP:        fp,
			FN:        fn,
		})
	}
	return tiers
}

// scoreCategoryAgnostic matches predictions to annotations ignoring category.
// A prediction matches an annotation if the text or bbox overlaps, regardless
// of what entity type label either side has.
func scoreCategoryAgnostic(samples []Sample, predictions map[string][]Prediction, opts ScoreOptions) (tp, fp, fn int) {
	for _, sample := range samples {
		preds := predictions[sample.ID]
		matched, predMatched := matchCategoryAgnosticSample(sample.Annotations, preds, opts)

		for pi := range preds {
			if !predMatched[pi] {
				fp++
			}
		}
		for ai := range sample.Annotations {
			if matched[ai] {
				tp++
			} else {
				fn++
			}
		}
	}
	return
}

func matchCategoryAgnosticSample(annotations []Annotation, preds []Prediction, opts ScoreOptions) ([]bool, []bool) {
	matched := make([]bool, len(annotations))
	predMatched := make([]bool, len(preds))

	for pi, pred := range preds {
		for ai, ann := range annotations {
			if matched[ai] {
				continue
			}
			if isMatch(ann, pred, opts) {
				matched[ai] = true
				predMatched[pi] = true
				break
			}
		}
	}

	return matched, predMatched
}

func computeBenchmark(tp, fp, fn int) BenchmarkMetrics {
	var bm BenchmarkMetrics

	// Privacy Block Rate = Recall = TP / (TP + FN)
	if tp+fn > 0 {
		bm.PrivacyBlockRate = float64(tp) / float64(tp+fn)
	}
	// Privacy Leak Rate = 1 - Recall = FN / (TP + FN)
	bm.PrivacyLeakRate = 1.0 - bm.PrivacyBlockRate

	// Utility Score: approximated as Precision = TP / (TP + FP).
	// A perfect score means zero false positives (no over-redaction).
	if tp+fp > 0 {
		bm.UtilityScore = float64(tp) / float64(tp+fp)
	} else if fp == 0 {
		bm.UtilityScore = 1.0
	}

	// Overall Score: harmonic mean of BlockRate and Utility (= F1).
	if bm.PrivacyBlockRate+bm.UtilityScore > 0 {
		bm.OverallScore = 2 * bm.PrivacyBlockRate * bm.UtilityScore /
			(bm.PrivacyBlockRate + bm.UtilityScore)
	}

	return bm
}

func scoreSomethingScorecard(samples []Sample, predictions map[string][]Prediction, opts ScoreOptions) SomethingScorecard {
	var sc SomethingScorecard
	var criticalSupport, criticalTP int
	var highSupport, highTP int

	for _, sample := range samples {
		preds := predictions[sample.ID]
		matched, predMatched := matchCategoryAgnosticSample(sample.Annotations, preds, opts)

		for ai, ann := range sample.Annotations {
			tier := categoryTier(ann.Category)
			switch tier {
			case TierCritical:
				criticalSupport++
				if matched[ai] {
					criticalTP++
				} else {
					sc.CriticalLeakCount++
				}
			case TierHigh:
				highSupport++
				if matched[ai] {
					highTP++
				} else {
					sc.HighRiskLeakCount++
				}
			case TierMedium:
				// Medium-tier categories are intentionally low-weight for
				// Something: names and dates often carry essential task
				// context and are not blanket-redacted by design.
			default:
			}

			weight := categoryRiskWeight(ann.Category)
			if weight <= 0 {
				continue
			}
			sc.TargetSupport++
			sc.RiskWeightedSupport += weight
			if !matched[ai] {
				sc.TargetLeakCount++
				sc.RiskWeightedLeakCost += weight
			}
		}

		sampleLoss := 0.0
		for pi, pred := range preds {
			if predMatched[pi] {
				continue
			}
			sc.OverProtectionCount++
			sampleLoss += predictionContextExtent(sample, pred)
		}
		if sampleLoss > 1.0 {
			sampleLoss = 1.0
		}
		sc.OverProtectionExtent += sampleLoss
	}

	sc.CriticalProtectionRate = rate(criticalTP, criticalSupport)
	sc.HighRiskProtectionRate = rate(highTP, highSupport)

	if sc.RiskWeightedSupport > 0 {
		sc.RiskWeightedExposureRate = sc.RiskWeightedLeakCost / sc.RiskWeightedSupport
	}
	sc.RiskWeightedProtectionScore = 1.0 - sc.RiskWeightedExposureRate

	if len(samples) > 0 {
		sc.ContextLossRate = sc.OverProtectionExtent / float64(len(samples))
	}
	if sc.ContextLossRate > 1.0 {
		sc.ContextLossRate = 1.0
	}
	sc.ContextPreservationScore = 1.0 - sc.ContextLossRate

	sc.DecisionScore = 0.80*sc.RiskWeightedProtectionScore + 0.20*sc.ContextPreservationScore
	sc.Gates = []SafetyGate{
		gateLTE("critical_leak_count", float64(sc.CriticalLeakCount), 0),
		gateLTE("risk_weighted_exposure_rate", sc.RiskWeightedExposureRate, 0.02),
		gateGTE("high_risk_protection_rate", sc.HighRiskProtectionRate, 0.95),
		gateGTE("context_preservation_score", sc.ContextPreservationScore, 0.80),
	}
	sc.ReadyForUserClaim = true
	for _, gate := range sc.Gates {
		if !gate.Passed {
			sc.ReadyForUserClaim = false
			break
		}
	}

	return sc
}

func categoryRiskWeight(category string) float64 {
	switch categoryTier(category) {
	case TierCritical:
		return 10
	case TierHigh:
		return 3
	case TierMedium:
		return 0
	default:
		return 0
	}
}

func predictionContextExtent(sample Sample, pred Prediction) float64 {
	if len(sample.Text) > 0 {
		if pred.Start >= 0 && pred.End > pred.Start && pred.End <= len(sample.Text) {
			return clamp01(float64(pred.End-pred.Start) / float64(len(sample.Text)))
		}
		if pred.Text != "" {
			return clamp01(float64(len(pred.Text)) / float64(len(sample.Text)))
		}
	}

	if pred.Width > 0 && pred.Height > 0 {
		return clamp01(pred.Width * pred.Height)
	}

	if pred.Text != "" {
		return clamp01(float64(len(pred.Text)) / 1000.0)
	}

	return 0.01
}

func rate(numerator, denominator int) float64 {
	if denominator == 0 {
		return 1.0
	}
	return float64(numerator) / float64(denominator)
}

func gateLTE(name string, value, threshold float64) SafetyGate {
	return SafetyGate{
		Name:      name,
		Passed:    value <= threshold,
		Value:     value,
		Threshold: threshold,
		Direction: "<=",
	}
}

func gateGTE(name string, value, threshold float64) SafetyGate {
	return SafetyGate{
		Name:      name,
		Passed:    value >= threshold,
		Value:     value,
		Threshold: threshold,
		Direction: ">=",
	}
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func matchAnnotations(annotations []Annotation, preds []Prediction, opts ScoreOptions,
	tp, fp, fn map[string]int) {

	matched := make([]bool, len(annotations))
	predMatched := make([]bool, len(preds))

	for pi, pred := range preds {
		for ai, ann := range annotations {
			if matched[ai] {
				continue
			}
			if !categoryMatch(pred.Category, ann.Category) {
				continue
			}
			if isMatch(ann, pred, opts) {
				matched[ai] = true
				predMatched[pi] = true
				tp[ann.Category]++
				break
			}
		}
	}

	for pi, pred := range preds {
		if !predMatched[pi] {
			fp[pred.Category]++
		}
	}

	for ai, ann := range annotations {
		if !matched[ai] {
			fn[ann.Category]++
		}
	}
}

func isMatch(ann Annotation, pred Prediction, opts ScoreOptions) bool {
	switch opts.Mode {
	case MatchExact:
		return pred.Start == ann.Start && pred.End == ann.End
	case MatchOverlap:
		annHasSpan := ann.Start != 0 || ann.End != 0
		predHasSpan := pred.Start != 0 || pred.End != 0
		if annHasSpan && predHasSpan {
			if spansOverlap(ann.Start, ann.End, pred.Start, pred.End) {
				return true
			}
		}
		if ann.Width > 0 && pred.Width > 0 {
			if bboxIoU(ann.X, ann.Y, ann.Width, ann.Height,
				pred.X, pred.Y, pred.Width, pred.Height) >= iouThreshold(opts) {
				return true
			}
			if bboxAnnotationCoverage(ann.X, ann.Y, ann.Width, ann.Height,
				pred.X, pred.Y, pred.Width, pred.Height) >= bboxCoverageThreshold(opts) {
				return true
			}
		}
		// Text-based fallback: check if texts overlap (substring containment).
		if ann.Text != "" && pred.Text != "" {
			if strings.Contains(pred.Text, ann.Text) || strings.Contains(ann.Text, pred.Text) {
				return true
			}
			return normalizedTextContains(pred.Text, ann.Text) || normalizedTextContains(ann.Text, pred.Text)
		}
		return false
	case MatchCategoryOnly:
		return true
	}
	return false
}

func normalizedTextContains(haystack, needle string) bool {
	normalizedHaystack := normalizeOCRMatchText(haystack)
	normalizedNeedle := normalizeOCRMatchText(needle)
	if normalizedHaystack == "" || normalizedNeedle == "" {
		return false
	}
	return strings.Contains(normalizedHaystack, normalizedNeedle)
}

func normalizeOCRMatchText(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
			continue
		}
		switch r {
		case 'o':
			r = '0'
		case 'i', 'l':
			r = '1'
		default:
		}
		b.WriteRune(r)
	}
	return b.String()
}

func iouThreshold(opts ScoreOptions) float64 {
	if opts.IoUThreshold > 0 {
		return opts.IoUThreshold
	}
	return 0.5
}

func bboxCoverageThreshold(opts ScoreOptions) float64 {
	if opts.BboxCoverageThreshold > 0 {
		return opts.BboxCoverageThreshold
	}
	return 0.7
}

func spansOverlap(s1, e1, s2, e2 int) bool {
	return s1 < e2 && s2 < e1
}

func bboxIoU(x1, y1, w1, h1, x2, y2, w2, h2 float64) float64 {
	ax1, ay1, ax2, ay2 := x1, y1, x1+w1, y1+h1
	bx1, by1, bx2, by2 := x2, y2, x2+w2, y2+h2

	ix1 := max64(ax1, bx1)
	iy1 := max64(ay1, by1)
	ix2 := min64(ax2, bx2)
	iy2 := min64(ay2, by2)

	if ix2 <= ix1 || iy2 <= iy1 {
		return 0
	}

	inter := (ix2 - ix1) * (iy2 - iy1)
	union := w1*h1 + w2*h2 - inter
	if union <= 0 {
		return 0
	}
	return inter / union
}

func bboxAnnotationCoverage(x1, y1, w1, h1, x2, y2, w2, h2 float64) float64 {
	ax1, ay1, ax2, ay2 := x1, y1, x1+w1, y1+h1
	bx1, by1, bx2, by2 := x2, y2, x2+w2, y2+h2

	ix1 := max64(ax1, bx1)
	iy1 := max64(ay1, by1)
	ix2 := min64(ax2, bx2)
	iy2 := min64(ay2, by2)

	if ix2 <= ix1 || iy2 <= iy1 {
		return 0
	}

	annArea := w1 * h1
	if annArea <= 0 {
		return 0
	}
	inter := (ix2 - ix1) * (iy2 - iy1)
	return inter / annArea
}

func max64(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func min64(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func categoryMatch(predicted, ground string) bool {
	return strings.EqualFold(predicted, ground)
}

func collectCategories(maps ...map[string]int) []string {
	seen := make(map[string]bool)
	var out []string
	for _, m := range maps {
		for k := range m {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	sortStrings(out)
	return out
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
