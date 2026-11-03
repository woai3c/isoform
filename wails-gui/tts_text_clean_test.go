package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestDominantSpeechLang(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{name: "English", text: "actions speak", want: "en"},
		{name: "Chinese", text: "行胜于言", want: "zh"},
		{name: "Mixed", text: "使用 API 完成操作", want: "zh"},
		{name: "No speech", text: "123", want: "zh"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dominantSpeechLang(tt.text); got != tt.want {
				t.Fatalf("dominantSpeechLang(%q) = %q, want %q", tt.text, got, tt.want)
			}
		})
	}
}

func TestStripMarkdownForSpeech(t *testing.T) {
	input := "# Result\nRead [the docs](https://example.com) and `run`. ✅ **Done**"
	want := "Result\nRead the docs and run. Done"

	if got := stripMarkdownForSpeech(input); got != want {
		t.Fatalf("stripMarkdownForSpeech() = %q, want %q", got, want)
	}

	if got := stripMarkdownForSpeech("Before\n```sh\nrm -rf /\n```\nAfter"); strings.Contains(got, "rm -rf") {
		t.Fatalf("stripMarkdownForSpeech() retained fenced code: %q", got)
	}
}

func TestReadAloudPreservesExactTimesAndSourceNumbers(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"Your interview is from 11:05 AM to 12:55 PM.", "Your interview is from 11:05am to 12:55pm."},
		{"Set the deployment timeout to 1 h 33 min.", "Set the deployment timeout to an hour and 33 minutes."},
		{"面试从上午11点5分到下午12点55分。", "面试从上午11点5分到中午12点55分。"},
		{"超时设为1小时33分钟。", "超时设为1小时33分钟。"},
		{"Use 16:10 displays with aspect ratio 16:09.", "Use 16:10 displays with aspect ratio 16:09."},
		{"Recording 1:05 to 1:10.", "Recording 1:05 to 1:10."},
		{"使用16:10显示器，录音1:05到1:10。", "使用16:10显示器，录音1:05到1:10。"},
		{"Meet at 6:00 PM, then 1:30 PM.", "Meet at 6pm, then 1:30pm."},
		{"会面时间6pm，讨论时间1:30pm。", "会面时间下午6点，讨论时间下午1点半。"},
		{"Read the notes from Fri Oct 16, 2020.", "Read the notes from Fri Oct 16, 2020."},
	} {
		t.Run(tc.input, func(t *testing.T) {
			if got := stripMarkdownForSpeech(tc.input); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSplitSpeechSentences(t *testing.T) {
	input := "First sentence. Second sentence!\n第三句。"
	want := []string{"First sentence.", "Second sentence!", "第三句。"}

	if got := splitSpeechSentences(input); !reflect.DeepEqual(got, want) {
		t.Fatalf("splitSpeechSentences() = %#v, want %#v", got, want)
	}
}

func TestSplitSpeechSentencesKeepsDecimalTogether(t *testing.T) {
	want := []string{"Version 1.25 is ready."}
	if got := splitSpeechSentences("Version 1.25 is ready."); !reflect.DeepEqual(got, want) {
		t.Fatalf("splitSpeechSentences() = %#v, want %#v", got, want)
	}
}

func TestStripMarkdownCollapsesHanSpaces(t *testing.T) {
	// A title/author line ("《静夜思》 李白") leaves an ASCII space between two
	// Han characters after the quote brackets are stripped; MeloTTS glitches on
	// it and drops the following word, so it must be collapsed.
	if got := stripMarkdownForSpeech("《静夜思》 李白"); got != "静夜思李白" {
		t.Fatalf("stripMarkdownForSpeech() = %q, want %q", got, "静夜思李白")
	}
	// A run of Han-separated spaces collapses fully (non-overlapping regex needs
	// the loop).
	if got := stripMarkdownForSpeech("甲 乙 丙"); got != "甲乙丙" {
		t.Fatalf("stripMarkdownForSpeech() = %q, want %q", got, "甲乙丙")
	}
	// Han↔Latin spaces are meaningful and must be preserved.
	if got := stripMarkdownForSpeech("使用 Python 写代码"); got != "使用 Python 写代码" {
		t.Fatalf("stripMarkdownForSpeech() = %q, want %q", got, "使用 Python 写代码")
	}
}

func TestStripMarkdownDropsKaomoji(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"fullwidth block face", "可谓千古绝唱！（▏▽▏）", "可谓千古绝唱！"},
		{"box-drawing degree face", "可愿与吾聊聊？ ╰(°▽°)╯", "可愿与吾聊聊？"},
		{"ascii emoticon", "开心 (^_^) 啊", "开心啊"},
		// Real parentheticals with speakable content are preserved.
		{"degree with unit kept", "温度 25°C 正常", "温度 25°C 正常"},
		{"han parenthetical kept", "(参见下文)", "(参见下文)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stripMarkdownForSpeech(tt.in); got != tt.want {
				t.Fatalf("stripMarkdownForSpeech(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestStripMarkdownDropsStrayBackticks(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		// A lone unpaired backtick must not reach the engine (clicks/noise),
		// e.g. a trailing decoration after an emoji: "吗？ 😊`".
		{"lone trailing backtick", "有什么我能帮上忙的吗？ 😊`", "有什么我能帮上忙的吗？"},
		{"lone leading backtick", "`开始干活", "开始干活"},
		// Paired backticks are inline code and keep their content.
		{"inline code unwrapped", "运行 `make build` 即可", "运行 make build 即可"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stripMarkdownForSpeech(tt.in); got != tt.want {
				t.Fatalf("stripMarkdownForSpeech(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestStripMarkdownNormalizesTTSSeparators(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"em dash pause", "他说——可以", "他说，可以"},
		{"fullwidth wave numeric range", "营业时间 9～18 点", "营业时间 9到18 点"},
		{"em dash year range", "2024—2026 年", "2024到2026 年"},
		{"interpunct pause", "宋·陆游", "宋，陆游"},
		{"decorative wave dropped", "好耶～", "好耶"},
		{"english em dash pause", "Ready—start now", "Ready, start now"},
		{"english numeric range", "Open from 9—18 daily", "Open from 9 to 18 daily"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stripMarkdownForSpeech(tt.in); got != tt.want {
				t.Fatalf("stripMarkdownForSpeech(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSplitSpeechSentencesCollapsesSpaceAfterCJKPunctuation(t *testing.T) {
	input := "这是一个很长的句子，需要在这里分段，然后继续说完后面的内容。"
	for _, chunk := range splitSpeechSentences(input) {
		if strings.Contains(chunk, "， ") {
			t.Fatalf("splitSpeechSentences(%q) retained space after punctuation: %q", input, chunk)
		}
	}
}
