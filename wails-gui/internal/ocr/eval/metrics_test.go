package eval

import (
	"math"
	"testing"
)

func TestCER(t *testing.T) {
	tests := []struct {
		name   string
		ground string
		pred   string
		want   float64
	}{
		{"exact match", "hello world", "hello world", 0.0},
		{"empty both", "", "", 0.0},
		{"empty ground nonempty pred", "", "abc", 1.0},
		{"empty pred nonempty ground", "abc", "", 1.0},
		{"one substitution", "hello", "hallo", 0.2},
		{"one insertion", "hello", "heello", 0.2},
		{"one deletion", "hello", "helo", 0.2},
		{"chinese exact", "你好世界", "你好世界", 0.0},
		{"chinese one error", "你好世界", "你好世", 0.25},
		{"chinese substitution", "你好世界", "你好事界", 0.25},
		{"mixed zh-en exact", "Hello你好World世界", "Hello你好World世界", 0.0},
		{"mixed zh-en one error", "Hello你好World世界", "Hello好好World世界", 1.0 / 14},
		{"whitespace trimmed", "  hello  ", "hello", 0.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CER(tt.ground, tt.pred)
			if math.Abs(got-tt.want) > 0.01 {
				t.Errorf("CER(%q, %q) = %.4f, want %.4f", tt.ground, tt.pred, got, tt.want)
			}
		})
	}
}

func TestWER(t *testing.T) {
	tests := []struct {
		name   string
		ground string
		pred   string
		want   float64
	}{
		{"exact match", "hello world", "hello world", 0.0},
		{"one word wrong", "hello world", "hello earth", 0.5},
		{"chinese characters as words", "你好世界", "你好事界", 0.25},
		{"empty both", "", "", 0.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := WER(tt.ground, tt.pred)
			if math.Abs(got-tt.want) > 0.01 {
				t.Errorf("WER(%q, %q) = %.4f, want %.4f", tt.ground, tt.pred, got, tt.want)
			}
		})
	}
}

func TestTokenize(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  int
	}{
		{"english words", "hello world foo", 3},
		{"chinese chars", "你好世界", 4},
		{"mixed", "Hello你好World", 4},
		{"empty", "", 0},
		{"whitespace only", "   ", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokens := tokenize(tt.input)
			if len(tokens) != tt.want {
				t.Errorf("tokenize(%q) returned %d tokens %v, want %d", tt.input, len(tokens), tokens, tt.want)
			}
		})
	}
}

func TestScore(t *testing.T) {
	samples := []Sample{
		{ID: "s1", Language: "en", Category: "ide", GroundTruth: "hello world"},
		{ID: "s2", Language: "zh", Category: "chat", GroundTruth: "你好世界"},
	}
	results := map[string]OCRResult{
		"s1": {Text: "hello world", Confidence: 0.95},
		"s2": {Text: "你好世界", Confidence: 0.90},
	}

	report := Score(samples, results)

	if report.TotalSamples != 2 {
		t.Errorf("TotalSamples = %d, want 2", report.TotalSamples)
	}
	if report.Overall.MeanCER != 0.0 {
		t.Errorf("Overall.MeanCER = %.4f, want 0", report.Overall.MeanCER)
	}
	if len(report.ByLanguage) != 2 {
		t.Errorf("ByLanguage has %d groups, want 2", len(report.ByLanguage))
	}
}

func TestEditDistance(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "", 3},
		{"", "abc", 3},
		{"kitten", "sitting", 3},
		{"你好", "你好", 0},
		{"你好", "你坏", 1},
	}

	for _, tt := range tests {
		t.Run(tt.a+"->"+tt.b, func(t *testing.T) {
			got := editDistance([]rune(tt.a), []rune(tt.b))
			if got != tt.want {
				t.Errorf("editDistance(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}
