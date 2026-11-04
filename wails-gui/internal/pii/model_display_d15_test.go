package pii

import "testing"

// today-review v1.1.16 D15: a sentence with a private item left out still
// reads as a sentence.
func TestDisplayModelTextRewritesTheSentenceAroundAMask(t *testing.T) {
	for _, tc := range []struct{ raw, language, want string }{
		{"The [REDACTED:code] release is stalled.", "en", "A private release is stalled."},
		{"Ask about the [REDACTED:code] release today.", "en", "Ask about a private release today."},
		{"Your verification code is [REDACTED:code].", "en", "Your verification code is a private code."},
		{"Use code [REDACTED:code] to sign in", "en", "Use code a private code to sign in"},
		{"Check the [REDACTED:account_number].", "en", "Check an account number."},
		{"这个[REDACTED:code]发布卡住了。", "zh-CN", "一个私密代码发布卡住了。"},
		// v1.1.20 R46: the phrase follows the brief's language, not the app's.
		{"The [REDACTED:code] release is stalled, and Maya is waiting on it before Thursday.", "zh-CN", "A private release is stalled, and Maya is waiting on it before Thursday."},
		{"这个[REDACTED:code]发布卡住了。", "en", "一个私密代码发布卡住了。"},
		{"[REDACTED:api_key]", "zh-CN", "一个 API 密钥"},
		{"[REDACTED:api_key]", "en", "an API key"},
	} {
		if got := DisplayModelText(tc.raw, tc.language); got != tc.want {
			t.Errorf("%q => %q want %q", tc.raw, got, tc.want)
		}
		if got := DisplayModelText(DisplayModelText(tc.raw, tc.language), tc.language); got != DisplayModelText(tc.raw, tc.language) {
			t.Errorf("not idempotent: %q", got)
		}
	}
}
