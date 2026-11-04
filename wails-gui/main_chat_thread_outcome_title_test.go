package main

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestThreadOutcomeTitleNamesTheWork(t *testing.T) {
	item := mainChatProactive{
		Headline: "Your assistant said the release is still building, not yet published, and public commits will now list the real PR titles.",
		Body:     "Next: check the release workflow.",
		Localized: map[string]mainChatProactiveCopy{
			"zh-CN": {Headline: "你的助手说发布仍在构建，尚未发布。", Body: "下一步：检查发布流程。"},
		},
	}
	applyThreadOutcomeWorkTitle(&item, "Releasing Isoform 1.0")
	if item.Headline != "Releasing Isoform 1.0" {
		t.Fatalf("headline = %q", item.Headline)
	}
	if item.Body != "The release is still building, not yet published. Next: check the release workflow." {
		t.Fatalf("body = %q", item.Body)
	}
	if zh := item.Localized["zh-CN"]; zh.Headline != "Releasing Isoform 1.0" || !strings.HasPrefix(zh.Body, "发布仍在构建，尚未发布。") {
		t.Fatalf("zh = %+v", zh)
	}
}

func TestThreadOutcomeTitleKeepsAShortWorkTitleAndClipsALongOne(t *testing.T) {
	item := mainChatProactive{Headline: "Lease summary for Marisol", Body: "Next: send it."}
	applyThreadOutcomeWorkTitle(&item, "ignored")
	if item.Headline != "Lease summary for Marisol" || item.Body != "Next: send it." {
		t.Fatalf("a short title that names the work stays: %+v", item)
	}
	long := mainChatProactive{Headline: strings.Repeat("Drafting the community garden budget ", 3), Body: "Next: share it."}
	applyThreadOutcomeWorkTitle(&long, "Drafting the community garden budget for the spring board meeting with Priya")
	if n := utf8.RuneCountInString(long.Headline); n > threadOutcomeTitleLimit || strings.HasPrefix(long.Headline, "Your assistant said") {
		t.Fatalf("headline %q has %d characters", long.Headline, n)
	}
	if strings.HasSuffix(long.Headline, " ") || !strings.HasPrefix(long.Headline, "Drafting the community garden budget") {
		t.Fatalf("headline must cut at a word: %q", long.Headline)
	}
}
