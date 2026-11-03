package main

import "testing"

func TestOpenAnswerAskSendRequiresExactLocalizedText(t *testing.T) {
	for _, tc := range []struct {
		text string
		want bool
	}{
		{openAnswerAskSendEnglish, true},
		{openAnswerAskSendChinese, true},
		{"  " + openAnswerAskSendEnglish + "  ", true},
		{"Let me know when it settles.", false},
		{"Let me know when it's settled. Thanks.", false},
		{"告诉我有结果。", false},
		{"", false},
	} {
		if got := isOpenAnswerAskSend(tc.text); got != tc.want {
			t.Errorf("isOpenAnswerAskSend(%q) = %v, want %v", tc.text, got, tc.want)
		}
	}
}
