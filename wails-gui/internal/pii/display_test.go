package pii

import (
	"strings"
	"testing"
	"wails-gui/internal/platform/ocr"
)

func TestPIIAccountLabelWordAndPathBoundaries(t *testing.T) {
	for _, text := range []string{
		"Reclaim12345678w outdated Command Line Tools error",
		"Reclaim now outdated Command Line Tools error",
		"Reclaim /Library/Developer/CommandLineTools/12345678w outdated Command Line Tools error",
		"/tmp/claim12345678w/tools", "version/account12345678w", "account12345678w", "claimant12345678w",
	} {
		if got, n := RedactText(text); n != 0 || got != text {
			t.Errorf("false positive %q => %q (%d)", text, got, n)
		}
		if got := DetectWithRegex([]ocr.TextObservation{{Text: text, Confidence: 1}}); len(got) != 0 {
			t.Errorf("OCR false positive %q: %+v", text, got)
		}
	}
	for _, text := range []string{"Account number: 1234567890", "account:1234567890", "Claim No: AB-123456", "保单号12345678", "我的保单号12345678", "私の保険契約番号12345678"} {
		got, n := RedactText(text)
		if n == 0 || !strings.Contains(got, "[REDACTED:account_number]") {
			t.Errorf("unmasked account %q => %q", text, got)
		}
		if got := DetectWithRegex([]ocr.TextObservation{{Text: text, Confidence: 1}}); len(got) == 0 {
			t.Errorf("OCR unmasked account %q", text)
		}
	}
}

// These guards cover both stored text and screenshot/OCR masking.
func TestPIIAccountNumericLabelPrivacy(t *testing.T) {
	for _, text := range []string{
		"account12345678", "acct12345678", "acct 12345678", "Account:12345678",
		"Account#12345678", "account12345678.", "(account12345678)",
		"_account12345678", "account123", "acct 123", "\u3000account12345678",
		"Account number: 12345678", "Claim No: AB-123456",
		"保单号12345678", "我的保单号12345678", "私の保険契約番号12345678",
	} {
		t.Run(text, func(t *testing.T) {
			got, n := RedactText(text)
			if n != 1 || !strings.Contains(got, "[REDACTED:account_number]") || strings.Contains(got, "123") {
				t.Errorf("unmasked account %q => %q (%d)", text, got, n)
			}
			if !HasRedaction(text) || strings.Contains(DisplayText(text), "123") {
				t.Errorf("display/read gate missed account %q", text)
			}
			if got := DetectWithRegex([]ocr.TextObservation{{Text: text, Confidence: 1}}); countDetections(got, "account_number") == 0 {
				t.Errorf("OCR unmasked account %q: %+v", text, got)
			}
		})
	}
}

func TestPIIAccountNumericLabelFalsePositives(t *testing.T) {
	for _, text := range []string{
		"Reclaim12345678w outdated Command Line Tools error",
		"Reclaim now outdated Command Line Tools error",
		"/usr/local/acct12345/bin", `C:\usr\local\acct12345\bin`,
		"/statements/account:1234567890", "/statements/account: 1234567890",
		"https://example.test?account12345678", "www.example.test?acct12345678",
		"example.test?account12345678", "https://example.test/account12345678",
		"\u3000www.example.test?acct12345678",
		"v12.3.45678", "account12.3.45678", "acct v12.3.45678",
		"subaccounts12", "subaccount12345678", "myacct12345678", "account12345678w", "account12",
	} {
		t.Run(text, func(t *testing.T) {
			if got, n := RedactText(text); n != 0 || got != text {
				t.Errorf("false positive %q => %q (%d)", text, got, n)
			}
			if got := DetectWithRegex([]ocr.TextObservation{{Text: text, Confidence: 1}}); len(got) != 0 {
				t.Errorf("OCR false positive %q: %+v", text, got)
			}
		})
	}
}

func TestPIIAccountNumericLabelKeepsFollowingTextAndAccounts(t *testing.T) {
	text := "account12345678. Check /usr/local/bin and acct87654321."
	want := "account[REDACTED:account_number]. Check /usr/local/bin and acct[REDACTED:account_number]."
	if got, n := RedactText(text); got != want || n != 2 {
		t.Fatalf("RedactText() = %q (%d), want %q (2)", got, n, want)
	}
}

func TestPIIAccountFalsePositiveCannotConsumeLaterAccount(t *testing.T) {
	for _, prefix := range []string{"/tmp/account:12345678", "account v12.3.45678", "https://example.test?account12345678"} {
		t.Run(prefix, func(t *testing.T) {
			text := prefix + " Account:87654321"
			if got, n := RedactText(text); n != 1 || got != prefix+" Account:[REDACTED:account_number]" {
				t.Errorf("later account was skipped: %q (%d)", got, n)
			}
			got := DetectWithRegex([]ocr.TextObservation{{Text: text, Confidence: 1}})
			if countDetections(got, "account_number") != 1 || got[0].Text != "87654321" {
				t.Errorf("OCR later account was skipped: %+v", got)
			}
		})
	}
}

func TestPIIAccountSeparatedFormatting(t *testing.T) {
	for _, tc := range []struct{ text, want string }{
		{"Account:123456789/0800", "Account:[REDACTED:account_number]"},
		{"account:12.3.45678", "account:[REDACTED:account_number]"},
		{"acct 1234.5678.90", "acct [REDACTED:account_number]"},
		{"Account No.1234.5678.90", "Account No.[REDACTED:account_number]"},
		{"claim number2023/0045/77", "claim number[REDACTED:account_number]"},
		{"AccountNo.1234.5678.90", "AccountNo.[REDACTED:account_number]"},
	} {
		t.Run(tc.text, func(t *testing.T) {
			if got, n := RedactText(tc.text); got != tc.want || n != 1 {
				t.Errorf("RedactText() = %q (%d), want %q (1)", got, n, tc.want)
			}
		})
	}
}

// Restore feat's formatted identifiers while retaining the boundary controls.
func TestPIIAccountFormattedValueCompatibility(t *testing.T) {
	for _, tc := range []struct{ text, want string }{
		{"Policy No: P/123/456/2023", "Policy No: [REDACTED:account_number]"},
		{"Account: 123456789/0800", "Account: [REDACTED:account_number]"},
		{"claim number 2023/0045/77", "claim number [REDACTED:account_number]"},
		{"Account No. 1234.5678.90", "Account No. [REDACTED:account_number]"},
		{"account12345678", "account[REDACTED:account_number]"},
		{"acct 12345678", "acct [REDACTED:account_number]"},
		{"Account: 12345678", "Account: [REDACTED:account_number]"},
		{"account 12-34-5678", "account [REDACTED:account_number]"},
		{"保单号12345678", "保单号[REDACTED:account_number]"},
		{"Reclaim12345678w", "Reclaim12345678w"},
		{"see /Users/me/account1234/file.txt", "see /Users/me/account1234/file.txt"},
		{"version account v1.2.3", "version account v1.2.3"},
		{"acct12345678", "acct[REDACTED:account_number]"},
	} {
		t.Run(tc.text, func(t *testing.T) {
			wantCount := 0
			if tc.want != tc.text {
				wantCount = 1
			}
			got, n := RedactText(tc.text)
			if got != tc.want || n != wantCount {
				t.Errorf("RedactText(%q) = %q (%d), want %q (%d)", tc.text, got, n, tc.want, wantCount)
			}
			wantDisplay := strings.ReplaceAll(tc.want, "[REDACTED:account_number]", "[...]")
			if HasRedaction(tc.text) != (wantCount > 0) || DisplayText(tc.text) != wantDisplay || DisplayText(got) != wantDisplay {
				t.Errorf("display/read gate mismatch for %q: %q", tc.text, DisplayText(tc.text))
			}
			detections := DetectWithRegex([]ocr.TextObservation{{Text: tc.text, Confidence: 1}})
			if len(detections) != wantCount || countDetections(detections, "account_number") != wantCount {
				t.Errorf("OCR mismatch for %q: %+v", tc.text, detections)
			}
		})
	}
}

func TestRedactionDisplayTextIsMonotonic(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Reclaim[REDACTED:account_number]w outdated Command Line Tools error", "Reclaim[...]w outdated Command Line Tools error"},
		{"Saved [REDACTED:api_key] and [REDACTED:private_email]", "Saved [...] and [...]"},
		{"Account number: 1234567890", "Account number: [...]"},
		{"Ordinary title", "Ordinary title"},
	} {
		if got := DisplayText(tc.in); got != tc.want || DisplayText(got) != got {
			t.Errorf("%q => %q want %q", tc.in, got, tc.want)
		}
	}
}
