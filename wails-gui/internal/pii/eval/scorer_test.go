package eval

import (
	"math"
	"strings"
	"testing"
)

func TestScorePerfectDetection(t *testing.T) {
	samples := []Sample{
		{
			ID:   "s1",
			Text: "My email is john@example.com and SSN is 123-45-6789",
			Annotations: []Annotation{
				{Text: "john@example.com", Category: "private_email", Start: 12, End: 28},
				{Text: "123-45-6789", Category: "government_id", Start: 40, End: 51},
			},
		},
	}

	predictions := map[string][]Prediction{
		"s1": {
			{Text: "john@example.com", Category: "private_email", Start: 12, End: 28, Confidence: 0.99},
			{Text: "123-45-6789", Category: "government_id", Start: 40, End: 51, Confidence: 0.95},
		},
	}

	report := Score(samples, predictions, DefaultScoreOptions())

	if report.MicroAvg.F1 != 1.0 {
		t.Errorf("expected perfect F1=1.0, got %.4f", report.MicroAvg.F1)
	}
	if report.MicroAvg.Precision != 1.0 {
		t.Errorf("expected precision=1.0, got %.4f", report.MicroAvg.Precision)
	}
	if report.MicroAvg.Recall != 1.0 {
		t.Errorf("expected recall=1.0, got %.4f", report.MicroAvg.Recall)
	}
	if report.Benchmark.PrivacyBlockRate != 1.0 {
		t.Errorf("expected benchmark block rate=1.0, got %.4f", report.Benchmark.PrivacyBlockRate)
	}
	if report.Scorecard.RiskWeightedProtectionScore != 1.0 {
		t.Errorf("expected scorecard protection=1.0, got %.4f", report.Scorecard.RiskWeightedProtectionScore)
	}
}

func TestScoreMissedDetection(t *testing.T) {
	samples := []Sample{
		{
			ID:   "s1",
			Text: "Call me at 555-0123 or email test@xx.test",
			Annotations: []Annotation{
				{Text: "555-0123", Category: "private_phone", Start: 11, End: 19},
				{Text: "test@xx.test", Category: "private_email", Start: 29, End: 41},
			},
		},
	}

	predictions := map[string][]Prediction{
		"s1": {
			{Text: "test@xx.test", Category: "private_email", Start: 29, End: 41, Confidence: 0.95},
		},
	}

	report := Score(samples, predictions, DefaultScoreOptions())

	if report.MicroAvg.Precision != 1.0 {
		t.Errorf("expected precision=1.0 (no FP), got %.4f", report.MicroAvg.Precision)
	}
	if report.MicroAvg.Recall != 0.5 {
		t.Errorf("expected recall=0.5 (1/2 detected), got %.4f", report.MicroAvg.Recall)
	}
}

func TestScoreFalsePositive(t *testing.T) {
	samples := []Sample{
		{
			ID:          "s1",
			Text:        "The order number is 12345",
			Annotations: []Annotation{},
		},
	}

	predictions := map[string][]Prediction{
		"s1": {
			{Text: "12345", Category: "credit_card", Start: 20, End: 25, Confidence: 0.6},
		},
	}

	report := Score(samples, predictions, DefaultScoreOptions())

	if report.MicroAvg.Precision != 0.0 {
		t.Errorf("expected precision=0.0, got %.4f", report.MicroAvg.Precision)
	}
}

func TestScoreOverlapMode(t *testing.T) {
	samples := []Sample{
		{
			ID:   "s1",
			Text: "My card is 4532-0123-4567-8901",
			Annotations: []Annotation{
				{Text: "4532-0123-4567-8901", Category: "credit_card", Start: 11, End: 30},
			},
		},
	}

	predictions := map[string][]Prediction{
		"s1": {
			{Text: "4532-0123-4567", Category: "credit_card", Start: 11, End: 25, Confidence: 0.8},
		},
	}

	report := Score(samples, predictions, ScoreOptions{Mode: MatchOverlap})

	if report.MicroAvg.F1 != 1.0 {
		t.Errorf("overlap mode should match partial spans, got F1=%.4f", report.MicroAvg.F1)
	}
}

func TestScoreBboxIoU(t *testing.T) {
	samples := []Sample{
		{
			ID: "s1",
			Annotations: []Annotation{
				{Text: "secret", Category: "api_key", X: 0.1, Y: 0.1, Width: 0.3, Height: 0.05},
			},
		},
	}

	predictions := map[string][]Prediction{
		"s1": {
			{Text: "secret", Category: "api_key", X: 0.1, Y: 0.1, Width: 0.3, Height: 0.05, Confidence: 0.9},
		},
	}

	report := Score(samples, predictions, ScoreOptions{Mode: MatchOverlap, IoUThreshold: 0.5})

	if report.MicroAvg.F1 != 1.0 {
		t.Errorf("perfect bbox overlap should yield F1=1.0, got %.4f", report.MicroAvg.F1)
	}
}

func TestScoreBboxAnnotationCoverage(t *testing.T) {
	samples := []Sample{
		{
			ID: "s1",
			Annotations: []Annotation{
				{Text: "secret", Category: "api_key", X: 0.30, Y: 0.10, Width: 0.10, Height: 0.05},
			},
		},
	}

	predictions := map[string][]Prediction{
		"s1": {
			{Text: "line containing secret", Category: "api_key", X: 0.10, Y: 0.10, Width: 0.40, Height: 0.05, Confidence: 0.9},
		},
	}

	report := Score(samples, predictions, ScoreOptions{Mode: MatchOverlap, IoUThreshold: 0.5, BboxCoverageThreshold: 0.7})

	if report.MicroAvg.F1 != 1.0 {
		t.Errorf("bbox annotation coverage should match protected PII, got F1=%.4f", report.MicroAvg.F1)
	}
}

func TestScoreOCRNormalizedTextOverlap(t *testing.T) {
	samples := []Sample{
		{
			ID: "s1",
			Annotations: []Annotation{
				{Text: "Tr0ub4dor&3!xQ", Category: "password"},
			},
		},
	}

	predictions := map[string][]Prediction{
		"s1": {
			{Text: "TrOub4dor & 3 ! xQ", Category: "password", Confidence: 0.9},
		},
	}

	report := Score(samples, predictions, DefaultScoreOptions())
	if report.MicroAvg.F1 != 1.0 {
		t.Errorf("OCR-normalized text overlap should match, got F1=%.4f", report.MicroAvg.F1)
	}
}

func TestBboxIoU(t *testing.T) {
	iou := bboxIoU(0.0, 0.0, 0.5, 0.5, 0.25, 0.25, 0.5, 0.5)
	expected := 0.0625 / (0.25 + 0.25 - 0.0625) // intersection / union
	if math.Abs(iou-expected) > 0.001 {
		t.Errorf("IoU expected %.4f, got %.4f", expected, iou)
	}
}

func TestBboxAnnotationCoverage(t *testing.T) {
	coverage := bboxAnnotationCoverage(0.30, 0.10, 0.10, 0.05, 0.10, 0.10, 0.40, 0.05)
	if math.Abs(coverage-1.0) > 0.001 {
		t.Errorf("annotation coverage expected 1.0, got %.4f", coverage)
	}
}

func TestMultiCategoryReport(t *testing.T) {
	samples := []Sample{
		{
			ID:   "s1",
			Text: "email: a@b.test, phone: 555-0123, ssn: 123-45-6789",
			Annotations: []Annotation{
				{Text: "a@b.test", Category: "private_email", Start: 7, End: 15},
				{Text: "555-0123", Category: "private_phone", Start: 24, End: 32},
				{Text: "123-45-6789", Category: "government_id", Start: 39, End: 50},
			},
		},
	}

	predictions := map[string][]Prediction{
		"s1": {
			{Text: "a@b.test", Category: "private_email", Start: 7, End: 15, Confidence: 0.99},
			{Text: "555-0123", Category: "private_phone", Start: 24, End: 32, Confidence: 0.9},
		},
	}

	report := Score(samples, predictions, DefaultScoreOptions())

	if len(report.Categories) != 3 {
		t.Fatalf("expected 3 categories, got %d", len(report.Categories))
	}

	for _, m := range report.Categories {
		switch m.Category {
		case "private_email":
			if m.F1 != 1.0 {
				t.Errorf("private_email F1 should be 1.0, got %.4f", m.F1)
			}
		case "private_phone":
			if m.F1 != 1.0 {
				t.Errorf("private_phone F1 should be 1.0, got %.4f", m.F1)
			}
		case "government_id":
			if m.Recall != 0.0 {
				t.Errorf("government_id recall should be 0.0, got %.4f", m.Recall)
			}
		}
	}
}

func TestSomethingScorecardRiskWeightedSafetyGates(t *testing.T) {
	samples := []Sample{
		{
			ID:   "critical",
			Text: "api key sk-test-12345",
			Annotations: []Annotation{
				{Text: "sk-test-12345", Category: "api_key", Start: 8, End: 21},
			},
		},
		{
			ID:   "high",
			Text: "email ops@example.com",
			Annotations: []Annotation{
				{Text: "ops@example.com", Category: "private_email", Start: 6, End: 21},
			},
		},
	}
	predictions := map[string][]Prediction{
		"high": {
			{Text: "ops@example.com", Category: "private_email", Start: 6, End: 21, Confidence: 0.99},
		},
	}

	report := Score(samples, predictions, DefaultScoreOptions())
	sc := report.Scorecard

	if sc.CriticalLeakCount != 1 {
		t.Fatalf("expected one critical leak, got %d", sc.CriticalLeakCount)
	}
	if sc.HighRiskLeakCount != 0 {
		t.Fatalf("expected no high-risk leaks, got %d", sc.HighRiskLeakCount)
	}
	if sc.RiskWeightedExposureRate != 10.0/13.0 {
		t.Fatalf("weighted exposure = %.4f, want %.4f", sc.RiskWeightedExposureRate, 10.0/13.0)
	}
	if sc.ReadyForUserClaim {
		t.Fatal("critical leak must fail user-claim readiness")
	}
}

func TestSomethingScorecardContextPreservationUsesExtent(t *testing.T) {
	text := strings.Repeat("a", 100)
	samples := []Sample{{ID: "clean", Text: text}}
	predictions := map[string][]Prediction{
		"clean": {
			{Text: text[:50], Category: "private_email", Start: 0, End: 50, Confidence: 0.6},
		},
	}

	report := Score(samples, predictions, DefaultScoreOptions())
	sc := report.Scorecard

	if sc.OverProtectionCount != 1 {
		t.Fatalf("expected one over-protection, got %d", sc.OverProtectionCount)
	}
	if math.Abs(sc.ContextLossRate-0.5) > 0.001 {
		t.Fatalf("context loss = %.4f, want 0.5", sc.ContextLossRate)
	}
	if math.Abs(sc.ContextPreservationScore-0.5) > 0.001 {
		t.Fatalf("context preservation = %.4f, want 0.5", sc.ContextPreservationScore)
	}
}
