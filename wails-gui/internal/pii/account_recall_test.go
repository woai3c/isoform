package pii

import (
	"strings"
	"testing"

	"wails-gui/internal/platform/ocr"
)

func TestAccountGroupedNumericRecall(t *testing.T) {
	for _, value := range []string{"1 234 567 890", "12 3456 7890", "01 23 45 67 89", "1 23", "12 3", "123 456 789"} {
		for _, label := range []string{"Account: ", "Account number: ", "Account number:\n", "account"} {
			suffixes := []string{"", "; check later", " please confirm"}
			if label == "account" {
				suffixes = append(suffixes, ".")
			}
			for _, suffix := range suffixes {
				raw := label + value + suffix
				if got, n := RedactText(raw); got != label+"[REDACTED:account_number]"+suffix || n != 1 {
					t.Errorf("flat %q => %q (%d)", raw, got, n)
				}
				got := DetectWithRegex([]ocr.TextObservation{{Text: raw, Confidence: 1}})
				if len(got) != 1 || got[0].Category != "account_number" || got[0].Text != value {
					t.Errorf("OCR %q => %+v", raw, got)
				}
			}
		}
	}
}

func TestOPFAccountCJKBoundaryRecall(t *testing.T) {
	for _, tc := range []struct {
		text, value string
		allowed     bool
	}{
		{"请转账至12345678，谢谢", "12345678", true},
		{"请转账至12345678后确认", "12345678", true},
		{"口座12345678へ送金してください", "12345678", true},
		{"カナ12345678です", "12345678", true},
		{"请转账至123456789后确认", "12345678", false},
		{"请转账至912345678后确认", "12345678", false},
		{"请转账至a12345678后确认", "12345678", false},
		{"请转账至12345678x后确认", "12345678", false},
		{"请转账至_12345678后确认", "12345678", false},
		{"请转账至12345678\u0301后确认", "12345678", false},
		{"the contract lapsed out", "lap", false},
		{"the contract lapsed out", "apsed", false},
		{"Account: AB-123456.", "AB-123456", true},
		{"请核对AB-123456后确认", "AB-123456", false},
	} {
		start := strings.Index(tc.text, tc.value)
		for _, offsets := range [][][2]int{
			{{start, start + len(tc.value)}},
			{{start, start + len(tc.value)/2}, {start + len(tc.value)/2, start + len(tc.value)}},
		} {
			got := decodeAccountOffsets(tc.text, offsets)
			if (len(got) == 1) != tc.allowed {
				t.Errorf("%q span %q offsets %v => %+v", tc.text, tc.value, offsets, got)
			}
			if len(got) == 1 && (got[0].text != tc.value || got[0].byteStart != start || got[0].byteEnd != start+len(tc.value)) {
				t.Errorf("account offsets changed: %+v", got)
			}
		}
	}
}

func TestOPFAccountRejectsMalformedOffsets(t *testing.T) {
	for _, tc := range []struct {
		text    string
		offsets [][2]int
	}{
		{"12345678", [][2]int{{-1, 8}}},
		{"12345678", [][2]int{{0, 9}}},
		{"12345678", [][2]int{{7, 3}}},
		{"请12345678", [][2]int{{1, 11}}},
		{"12345678后", [][2]int{{0, 9}}},
		{"账号", [][2]int{{0, 1}, {1, 6}}},
		{"12\xff345678", [][2]int{{0, 9}}},
		{"\xff12345678", [][2]int{{1, 9}}},
		{"12345678\xff", [][2]int{{0, 8}}},
		{"12345678", [][2]int{{0, 6}, {4, 8}}},
	} {
		t.Run(strings.ReplaceAll(tc.text, "/", "_"), func(t *testing.T) {
			if got := decodeAccountOffsets(tc.text, tc.offsets); len(got) != 0 {
				t.Errorf("invalid offsets %v for %q => %+v", tc.offsets, tc.text, got)
			}
		})
	}
}

// Synthetic S-account (4) and B/E-account (1/3) labels exercise the actual
// decoder. These guards measure span admission, not neural model accuracy.
func decodeAccountOffsets(text string, offsets [][2]int) []bioesSpan {
	logits := make([]float32, len(offsets)*opfNumLabels)
	for i := range offsets {
		label := 4
		if len(offsets) > 1 {
			label = 1
			if i == len(offsets)-1 {
				label = 3
			}
		}
		logits[i*opfNumLabels+label] = 10
	}
	return decodeBIOES(logits, len(offsets), offsets, text)
}
