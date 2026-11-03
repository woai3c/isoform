package pii

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"wails-gui/internal/platform/ocr"
)

func TestDisplayModelTextNeverSpeaksMasksOrBrokenFragments(t *testing.T) {
	for _, language := range []string{"en", "zh-CN", "Simplified Chinese"} {
		for _, raw := range []string{
			"the contract [REDACTED:account_number]sed out",
			"lap[REDACTED:account_number]sed out",
			"[REDACTED:private_email] and [REDACTED:future_kind]",
			"[redacted:account_number]", "[REDACTED:account_number", "[REDACTED",
		} {
			got := DisplayModelText(raw, language)
			if strings.Contains(strings.ToUpper(got), "[REDACTED") || strings.Contains(got, "numbersed") || strings.Contains(got, "账号sed") || strings.Contains(got, "]sed") {
				t.Errorf("%s: %q => %q", language, raw, got)
			}
			if DisplayModelText(got, language) != got {
				t.Errorf("not idempotent: %q", got)
			}
		}
	}
	for _, tc := range []struct{ raw, language, want string }{
		{"Check [REDACTED:account_number].", "en", "Check an account number."},
		{"请核对[REDACTED:account_number]后再继续", "zh-CN", "请核对一个账号后再继续"},
		{"[REDACTED:unknown]", "en", "a private detail"},
		{"[REDACTED:account_number][REDACTED:api_key]", "en", "an account number an API key"},
		{"the contract lapsed out", "en", "the contract lapsed out"},
		{"Check:[REDACTED:account_number]", "en", "Check: an account number"},
		{"[REDACTED:account_number]sed[REDACTED:api_key]", "en", "an account number an API key"},
	} {
		if got := DisplayModelText(tc.raw, tc.language); got != tc.want {
			t.Errorf("%q => %q want %q", tc.raw, got, tc.want)
		}
	}
}

func TestModelDisplayCatalogMatchesFrontendAndDetectorKinds(t *testing.T) {
	for language, copy := range modelRedactionCopy {
		raw, err := os.ReadFile("../../frontend/src/i18n/locales/" + language + "/common.json")
		if err != nil {
			t.Fatal(err)
		}
		var frontend struct {
			Redactions map[string]string `json:"redactions"`
		}
		if err := json.Unmarshal(raw, &frontend); err != nil {
			t.Fatal(err)
		}
		if len(frontend.Redactions) != len(copy) {
			t.Fatal("copy catalog length mismatch")
		}
		for kind, phrase := range copy {
			if frontend.Redactions[kind] != phrase || phrase == "" {
				t.Errorf("%s/%s copy mismatch", language, kind)
			}
		}
		for _, pattern := range regexPatterns {
			if copy[pattern.category] == "" {
				t.Errorf("missing %s/%s", language, pattern.category)
			}
		}
		for _, label := range opfID2Label {
			_, kind := parseBIOESLabel(label)
			if kind != "" && copy[kind] == "" {
				t.Errorf("missing OPF copy %s/%s", language, kind)
			}
		}
	}
}

func TestAccountCaptureAndOCRKeepWholeWords(t *testing.T) {
	for _, raw := range []string{
		"the contract lapsed out", "the contract passed out",
		"the contract lapsed out. Nothing on tomorrow's calendar competes with it.",
		"écontract 12345678", "contract12345678w", "myaccount12345678",
		"Account: A", "Account: AB", "Account: 12",
		"account123 456abc", "contract123 456w",
	} {
		if got, n := RedactText(raw); got != raw || n != 0 {
			t.Errorf("prose damaged: %q => %q", raw, got)
		}
		if got := DetectWithRegex([]ocr.TextObservation{{Text: raw, Confidence: 1}}); len(got) != 0 {
			t.Errorf("OCR prose damaged: %q => %+v", raw, got)
		}
	}
	for _, value := range []string{"1234567890", "AB-123456", "AB 123456", "A 123456", "12 345678", "GB12 BANK 123 4567", strings.Repeat("9", 44), strings.Repeat("A", 44)} {
		raw := "Account: " + value + "; check later"
		if got, n := RedactText(raw); got != "Account: [REDACTED:account_number]; check later" || n != 1 {
			t.Errorf("incomplete redaction: %q => %q", raw, got)
		}
		found := DetectWithRegex([]ocr.TextObservation{{Text: raw, Confidence: 1}})
		if len(found) != 1 || found[0].Text != value {
			t.Errorf("incomplete OCR redaction: %+v", found)
		}
	}
}

func TestOPFAccountSpansRequireBothWordBoundaries(t *testing.T) {
	for _, tc := range []struct {
		text, span string
		allowed    bool
	}{
		{"the contract lapsed out", "lap", false},
		{"the contract passed out", "pas", false},
		{"the contract lapsed out", "apsed", false},
		{"the contract lapsed out", " lap", false},
		{"my12345678id", "12345678", false},
		{"é12345678", "12345678", false},
		{"Account: 12345678.", "12345678", true},
		{"Account: AB-123456.", "AB-123456", true},
		{"(12345678)", "12345678", true},
	} {
		start := strings.Index(tc.text, tc.span)
		// Exercise single-token and multi-token BIOES closures without a model
		// download. These are decoder unit tests, not inference evaluations.
		for _, labels := range [][]int{{4}, {1, 3}} {
			logits := make([]float32, len(labels)*opfNumLabels)
			offsets := make([][2]int, len(labels))
			for i, label := range labels {
				logits[i*opfNumLabels+label] = 10
				lo := start + i*len(tc.span)/len(labels)
				hi := start + (i+1)*len(tc.span)/len(labels)
				offsets[i] = [2]int{lo, hi}
			}
			got := decodeBIOES(logits, len(labels), offsets, tc.text)
			if (len(got) == 1) != tc.allowed {
				t.Errorf("%q / %q labels %v: %+v", tc.text, tc.span, labels, got)
			}
		}
	}
}

func TestAccountBoundaryKeepsMultilineInsuranceValue(t *testing.T) {
	raw := "保険会社: 日本生命保険\n証券番号: POL-JP-2024-0042\n電話: 03-0000-0123"
	got, _ := RedactText(raw)
	if strings.Contains(got, "POL-JP-2024-0042") {
		t.Fatalf("multiline policy escaped redaction: %q", got)
	}
	found := false
	for _, d := range DetectWithRegex([]ocr.TextObservation{{Text: raw, Confidence: 1}}) {
		if d.Category == "account_number" && d.Text == "POL-JP-2024-0042" {
			found = true
		}
	}
	if !found {
		t.Fatal("account label was consumed by preceding insurance prose")
	}
}

func TestAccountBoundaryPreservesValuesOnFollowingLines(t *testing.T) {
	for _, raw := range []string{"Account number:\n12345678", "Account:\nAB-123456", "Policy No\nAB-123456", "証券番号:\nPOL-JP-2024-0042"} {
		got, n := RedactText(raw)
		if n == 0 || !strings.Contains(got, "[REDACTED:account_number]") || strings.Contains(got, "123456") || strings.Contains(got, "0042") {
			t.Errorf("multiline identifier escaped: %q => %q", raw, got)
		}
	}
}
