package engagement

import (
	"strings"
	"testing"
)

func TestFirstWeekHistoryWelcomeCopy(t *testing.T) {
	cases := []struct {
		history, name, lang, want string
	}{
		{"claude", "Alex", "en", "Hi Alex. Here's your first brief, made from today's calendar and your Claude Code history."},
		{"codex", "Alex", "en", "Hi Alex. Here's your first brief, made from today's calendar and your Codex history."},
		{"both", "Alex", "en", "Hi Alex. Here's your first brief, made from today's calendar and your Claude Code and Codex history."},
		{"claude", "", "en", "Hi there. Here's your first brief, made from today's calendar and your Claude Code history."},
		{"claude", "Alex", "zh-CN", "你好 Alex。这是根据今天的日历和你的 Claude Code 记录生成的第一份简报。"},
		{"both", "", "zh-CN", "你好。这是根据今天的日历和你的 Claude Code 和 Codex 记录生成的第一份简报。"},
	}
	for _, tc := range cases {
		got := FirstWeekHistoryWelcome(tc.history, tc.name, tc.lang)
		if got != tc.want {
			t.Fatalf("%s %s %s:\n got %q\nwant %q", tc.history, tc.name, tc.lang, got, tc.want)
		}
		if !ValidBody(got) {
			t.Fatalf("invalid body: %q", got)
		}
	}
}

func TestFirstWeekTextKeepsTheWelcomeWithoutHistory(t *testing.T) {
	for _, lang := range Languages {
		if got, want := FirstWeekText("welcome", "2026-10-28 14:05", "", "Alex", lang), FirstWeekCopy("welcome", "2026-10-28 14:05", lang); got != want {
			t.Fatalf("%s: welcome changed without history: %q", lang, got)
		}
		if got, want := FirstWeekText("brief", "", "claude", "Alex", lang), FirstWeekCopy("brief", "", lang); got != want {
			t.Fatalf("%s: non-welcome stage changed: %q", lang, got)
		}
	}
}

func TestFirstWeekHistoryWelcomeNamesNoMac(t *testing.T) {
	for _, lang := range Languages {
		for _, history := range []string{"claude", "codex", "both"} {
			if got := FirstWeekHistoryWelcome(history, "Alex", lang); strings.Contains(got, "Mac") || strings.Contains(got, "iPhone") {
				t.Fatal(got)
			}
		}
	}
}
