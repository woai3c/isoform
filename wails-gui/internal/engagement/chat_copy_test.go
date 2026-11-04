package engagement

import (
	"reflect"
	"testing"
)

func TestChatCopyLocalizesProducerVocabularyWithoutChangingSourceText(t *testing.T) {
	for _, tt := range []struct{ key, en, zh string }{
		{"slot", "morning", "早间"}, {"slot", "evening", "晚间"}, {"slot", "on-request", "按需"},
		{"what", "Screen capture", "屏幕捕获"}, {"what", "Microphone", "麦克风"}, {"what", "System audio", "系统音频"},
		{"duration", "20 minutes", "20 分钟"}, {"duration", "1 hour", "1 小时"}, {"duration", "2 hours", "2 小时"},
		{"due", "today", "今天"}, {"due", "tomorrow", "明天"}, {"due", "on Oct 14", "10月14日"},
	} {
		t.Run(tt.key+"/"+tt.en, func(t *testing.T) {
			params := map[string]string{tt.key: tt.en, "title": "morning", "subject": "Private subject"}
			got := ChatParams(params, "zh-CN")
			if got[tt.key] != tt.zh || got["title"] != "morning" || params[tt.key] != tt.en {
				t.Fatalf("bad translation or input mutation: %+v", got)
			}
			if back := CanonicalChatParams(got); !reflect.DeepEqual(back, params) {
				t.Fatalf("legacy recovery: %+v want %+v", back, params)
			}
			if !reflect.DeepEqual(ChatParams(params, "en"), params) {
				t.Fatal("English changed")
			}
		})
	}
}

func TestChatCopyLocalizesMeetingAndCalendarProducerSentences(t *testing.T) {
	for _, tt := range []struct{ key, en, zh string }{
		{"place", "on Zoom", "通过Zoom"}, {"place", "at Room 2", "在Room 2"}, {"people", "with Dana", "与Dana"},
		{"summary", "Planning (09:00-10:00) overlaps Review (09:30-10:30).", "Planning（09:00-10:00）与Review（09:30-10:30）时间重叠。"},
		{"summary", "You have not answered the invite to Planning (Mon 09:00-10:00).", "你还没有回复Planning（周一 09:00-10:00）的邀请。"},
	} {
		if got := ChatParams(map[string]string{tt.key: tt.en}, "zh-CN")[tt.key]; got != tt.zh || !ValidBody(got) {
			t.Errorf("%s = %q, want %q", tt.key, got, tt.zh)
		}
	}
}

func TestChatCopyLocalizesPlaybookFailureInstructionsBothWays(t *testing.T) {
	for _, reason := range []string{"", PlaybookChangedReason("en"), "source-specific detail"} {
		en := PlaybookFailedResult("en", reason)
		zhReason := reason
		if reason == PlaybookChangedReason("en") {
			zhReason = PlaybookChangedReason("zh-CN")
		}
		zh := PlaybookFailedResult("zh-CN", zhReason)
		got := ChatParams(map[string]string{"result": en}, "zh-CN")["result"]
		if got != zh || !ValidBody(got) {
			t.Fatalf("Chinese failure: %q, want %q", got, zh)
		}
		if restored := CanonicalChatParams(map[string]string{"result": zh})["result"]; restored != en {
			t.Fatalf("English failure: %q, want %q", restored, en)
		}
		if restored := ChatParams(map[string]string{"result": zh}, "en")["result"]; restored != en {
			t.Fatalf("English snapshot from Chinese producer: %q, want %q", restored, en)
		}
	}
}

func TestChatParamsUseTheChineseAppName(t *testing.T) {
	copy, _ := Copy(KindPersonReappeared, "zh-CN")
	if got := Render(copy.Sealed, ChatParams(map[string]string{"name": "王晓雨", "where": "WeChat"}, "zh-CN")); got != "王晓雨在微信中出现了。要记一笔吗？" {
		t.Fatalf("zh-CN = %q", got)
	}
	if got := ChatParams(map[string]string{"where": "Slack"}, "zh-CN")["where"]; got != "Slack" {
		t.Fatalf("Slack = %q", got)
	}
	if got := ChatParams(map[string]string{"where": "WeChat"}, "en")["where"]; got != "WeChat" {
		t.Fatalf("en = %q", got)
	}
}
