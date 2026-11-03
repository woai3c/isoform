package pii

import (
	"strings"
	"testing"
)

func TestRedactURL(t *testing.T) {
	cases := map[string]string{
		"https://accounts.example.com/reset?token=abc123#step2":           "https://accounts.example.com/reset",
		"https://app.example.com/reset-password/9f8a7b6c5d4e3f2a1b0c":     "https://app.example.com/reset-password/...",
		"https://github.com/example/isoform/pull/12":                      "https://github.com/example/isoform/pull/12",
		"https://docs.google.com/document/d/1AbCdEfGhIjKlMnOpQrStUv/edit": "https://docs.google.com/document/d/...",
		"https://www.example.com":                                         "https://www.example.com",
		"https://shop.example.com/orders/88421937":                        "https://shop.example.com/orders/...",
		"not a link":     "",
		"/relative/path": "",
	}
	for raw, want := range cases {
		if got := RedactURL(raw); got != want {
			t.Errorf("RedactURL(%q) = %q, want %q", raw, got, want)
		}
	}
	if got := RedactURL("https://example.com/people/ann@example.com"); strings.Contains(got, "ann@example.com") {
		t.Errorf("an email in the path survived: %q", got)
	}
}

func TestRedactCodes(t *testing.T) {
	if got := RedactCodes("Your code is 482913, PIN 1234 and room 12"); strings.Contains(got, "482913") || strings.Contains(got, "1234") || !strings.Contains(got, "room 12") {
		t.Fatalf("RedactCodes = %q", got)
	}
	for _, spaced := range []string{"code 482 913 now", "code 482-913 now", "code 482.913 now", "code 4 8 2 9 now", "1234 5678,9012 3456"} {
		if got := RedactCodes(spaced); strings.ContainsAny(got, "0123456789") {
			t.Fatalf("a code split into groups must go: %q -> %q", spaced, got)
		}
	}
	if got := RedactCodes("3.5 stars, room 12, v2.1"); got != "3.5 stars, room 12, v2.1" {
		t.Fatalf("short numbers stay: %q", got)
	}
	if got := RedactCodes("version v2026 and abc12345"); got != "version v2026 and abc12345" {
		t.Fatalf("short words with digits stay: %q", got)
	}
	if got := RedactCodes("Order 98765432101 shipped, tracking 1Z999AA10123456784"); strings.Contains(got, "98765432101") || strings.Contains(got, "1Z999AA10123456784") {
		t.Fatalf("long ids must go: %q", got)
	}
}

func TestRedactCodesShortCodes(t *testing.T) {
	cases := map[string]string{
		// A code keyword near a 3-digit run makes it a code.
		"my PIN is 482":                  "my PIN is [REDACTED:code]",
		"PIN: 482":                       "PIN: [REDACTED:code]",
		"CVV 913":                        "CVV [REDACTED:code]",
		"your verification code is 731.": "your verification code is [REDACTED:code].",
		"Use code 509 to sign in":        "Use code [REDACTED:code] to sign in",
		"one-time code 207":              "one-time code [REDACTED:code]",
		"验证码 731":                        "验证码 [REDACTED:code]",
		"密码是 482":                        "密码是 [REDACTED:code]",
		"482 is your passcode":           "[REDACTED:code] is your passcode",
		"PIN 48 2":                       "PIN [REDACTED:code]",
		"security code 318":              "security code [REDACTED:code]",
		"token is 482":                   "token is [REDACTED:code]",
		"验证码是731":                        "验证码是[REDACTED:code]",
		"call 5551234 then 456":          "call [REDACTED:code] then 456",
		// The keyword search stops at a sentence end or a comma.
		"enter code 482. Now 913 please": "enter code [REDACTED:code]. Now 913 please",
		"code 731, then 482":             "code [REDACTED:code], then 482",
		"room 302, code 1234":            "room 302, code [REDACTED:code]",
		// Without a keyword nearby a 3-digit run stays.
		"meet at 3pm":          "meet at 3pm",
		"room 302":             "room 302",
		"$450 for the deposit": "$450 for the deposit",
		"page 123":             "page 123",
		"the 1:1 at 9am":       "the 1:1 at 9am",
		"call me in 5 minutes": "call me in 5 minutes",
		"I have 3 kids":        "I have 3 kids",
		"the spin 482 class":   "the spin 482 class",
		// A dotted run is a version, a decimal or a price, not a PIN.
		"Python 3.12 code style": "Python 3.12 code style",
		// A run grouped into a larger number by commas is not a PIN.
		"send code 450,000 now": "send code 450,000 now",
		"450,000 is the code":   "450,000 is the code",
		// "code" after a word naming a non-secret code is not a keyword.
		"Exit code 127 debugging":             "Exit code 127 debugging",
		"HTTP status code 404 pages":          "HTTP status code 404 pages",
		"exit code 127":                       "exit code 127",
		"status code 404":                     "status code 404",
		"error code 500":                      "error code 500",
		"area code 415":                       "area code 415",
		"The build failed with exit code 127": "The build failed with exit code 127",
	}
	for text, want := range cases {
		if got := RedactCodes(text); got != want {
			t.Errorf("RedactCodes(%q) = %q, want %q", text, got, want)
		}
		// With nothing kept, RedactCodesKeeping is RedactCodes.
		if got := RedactCodesKeeping(text, nil); got != want {
			t.Errorf("RedactCodesKeeping(%q, nil) = %q, want %q", text, got, want)
		}
	}
}

// A kept span (a date the caller shows) is left as written, and a short
// code's keyword is read across it as if it were blank: the keyword on its far
// side still counts, the span takes no word of the window, and a blocker such
// as "exit" still stands next to its "code".
func TestRedactCodesKeepingReadsKeywordsAcrossKeptSpans(t *testing.T) {
	keep := func(text, span string) [][2]int {
		i := strings.Index(text, span)
		return [][2]int{{i, i + len(span)}}
	}
	for _, tc := range []struct{ text, span, want string }{
		{"the door code for 2026-10-20 is 482", "2026-10-20", "the door code for 2026-10-20 is [REDACTED:code]"},
		{"my PIN as of 2026-10-20: 731", "2026-10-20", "my PIN as of 2026-10-20: [REDACTED:code]"},
		{"enter code 482 on 2026-10-20", "2026-10-20", "enter code [REDACTED:code] on 2026-10-20"},
		{"exit code on 2026-10-20 was 127", "2026-10-20", "exit code on 2026-10-20 was 127"},
		{"code review 2026-10-20, room 302", "2026-10-20", "code review 2026-10-20, room 302"},
		{"code on 2026-10-20 cost 450,000", "2026-10-20", "code on 2026-10-20 cost 450,000"},
		{"code 2026-10-20 build 3.12", "2026-10-20", "code 2026-10-20 build 3.12"},
		// A gap hides at least what RedactCodes hides in it alone.
		{"exit 2026-10-20 code 127", "2026-10-20", "exit 2026-10-20 code [REDACTED:code]"},
		{"order 482913 on 2026-10-20 ref AB12CD34EF56", "2026-10-20", "order [REDACTED:code] on 2026-10-20 ref [REDACTED:code]"},
	} {
		if got := RedactCodesKeeping(tc.text, keep(tc.text, tc.span)); got != tc.want {
			t.Errorf("RedactCodesKeeping(%q) = %q, want %q", tc.text, got, tc.want)
		}
	}
}
