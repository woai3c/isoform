package engagement

import (
	"regexp"
	"strings"
	"testing"
)

func TestCopyCoversEveryKindInEveryLanguage(t *testing.T) {
	muteLabels := map[string]string{"en": "Mute this kind", "zh-CN": "静音此类消息"}
	for _, lang := range Languages {
		for _, kind := range AllKinds() {
			set, ok := Copy(kind, lang)
			if !ok {
				t.Fatalf("%s has no %s copy", kind, lang)
			}
			if strings.TrimSpace(set.PushBody) == "" || strings.TrimSpace(set.Sealed) == "" {
				t.Fatalf("%s %s copy is incomplete: %+v", kind, lang, set)
			}
			if strings.Contains(set.PushBody, "{") {
				t.Fatalf("%s %s push body must not carry parameters: %q", kind, lang, set.PushBody)
			}
			if strings.ContainsAny(set.PushBody, "!") || strings.Contains(set.Sealed, "!") {
				t.Fatalf("%s %s copy must not shout: %+v", kind, lang, set)
			}
			spec := mustSpec(t, kind)
			for _, action := range spec.Actions {
				if _, ok := set.Actions[action.ID]; !ok {
					t.Fatalf("%s %s copy lacks the %q action label", kind, lang, action.ID)
				}
			}
			allowed := map[string]bool{ActionMuteKind: true}
			muteLabel := muteLabels[lang]
			if kind == KindFirstWeek {
				muteLabel = NotForMeLabel(lang)
			}
			if label := set.Actions[ActionMuteKind]; label != muteLabel || label == "" {
				t.Fatalf("%s %s has incorrect mute action label %q", kind, lang, label)
			}
			for _, action := range append(spec.Actions, ChatActions(kind, true, true)...) {
				allowed[action.ID] = true
				if strings.TrimSpace(set.Actions[action.ID]) == "" {
					t.Fatalf("%s %s lacks action label %s", kind, lang, action.ID)
				}
			}
			if len(set.Actions) != len(allowed) {
				t.Fatalf("%s %s has %d labels for %d actions", kind, lang, len(set.Actions), len(allowed))
			}
		}
	}
}

func TestCopyIsShortCalmAndUsesIsoform(t *testing.T) {
	for _, lang := range Languages {
		for _, kind := range AllKinds() {
			set, _ := Copy(kind, lang)
			texts := []string{set.PushBody, set.Sealed, set.Text, set.EmailSubject, set.EmailBody}
			for _, label := range set.Actions {
				texts = append(texts, label)
			}
			for _, text := range texts {
				if !ValidBody(text) {
					t.Errorf("%s %s copy breaks length or language rules: %q", lang, kind, text)
				}
			}
		}
		for _, text := range []string{DigestHeadline(lang), DigestHeadline(lang, 1), DigestHeadline(lang, 5), DigestStatus(lang), TextHint(lang), PlaybookFailedResult(lang, ""), PlaybookChangedReason(lang), StatusReport(lang, "handoff_completed", "Draft"), StatusReport(lang, "schedule_ok", "Check"), StatusReport(lang, "schedule_error", "Check")} {
			if !ValidBody(text) {
				t.Errorf("%s supporting copy breaks length or language rules: %q", lang, text)
			}
		}
	}
}

func TestDigestHeadlineDescribesTheDisplayedCount(t *testing.T) {
	for _, tt := range []struct {
		lang  string
		count int
		want  string
	}{
		{"en", 1, "One thing to catch up on."},
		{"en", 2, "2 things to catch up on."},
		{"zh-CN", 1, "有一件事可以看看。"},
		{"zh-CN", 5, "有 5 件事可以看看。"},
		{"zh-Hans", 1, "有一件事可以看看。"},
		{"fr", 1, "One thing to catch up on."},
	} {
		if got := DigestHeadline(tt.lang, tt.count); got != tt.want {
			t.Errorf("%s count %d: %q, want %q", tt.lang, tt.count, got, tt.want)
		}
	}
	for _, lang := range Languages {
		if DigestHeadline(lang, 0) != DigestHeadline(lang) || DigestHeadline(lang, -1) != DigestHeadline(lang) {
			t.Errorf("%s empty digest lost the existing fallback", lang)
		}
	}
}

func TestChatActionLabelsDescribeTheReachableDestination(t *testing.T) {
	for _, tt := range []struct {
		kind           Kind
		action, en, zh string
	}{
		{KindPersonReappeared, "open_person", "Show person", "查看人物"},
		{KindMeetingPrep, "open", "Open in Calendar", "在日历中打开"},
		{KindCalendarConflict, "open", "Open in Calendar", "在日历中打开"},
		{KindWaitingOnReply, "open", "Review", "查看"},
	} {
		for _, lang := range Languages {
			want := tt.en
			if lang == "zh-CN" {
				want = tt.zh
			}
			set, _ := Copy(tt.kind, lang)
			if got := set.Actions[tt.action]; got != want {
				t.Errorf("%s %s %s = %q, want %q", tt.kind, lang, tt.action, got, want)
			}
		}
	}
}

func TestChatActionLabelsFollowSectionSeven(t *testing.T) {
	for _, lang := range Languages {
		for _, kind := range AllKinds() {
			set, _ := Copy(kind, lang)
			for action, label := range set.Actions {
				if lang == "zh-CN" {
					switch label {
					case "打开", "打开日历", "打开简报", "忽略", "以后再说":
						t.Errorf("%s has retired %s action label %q", kind, action, label)
					}
					if action == "not_now" && label != "暂不" {
						t.Errorf("%s not_now label = %q, want 暂不", kind, label)
					}
				}
			}
		}
	}

	for _, tt := range []struct {
		kind   Kind
		action string
		en, zh string
	}{
		{KindBriefReady, "open", "Play", "播放"},
		{KindDecisionDue, "approve", "Allow", "允许"},
		{KindDecisionDue, "decline", "Not now", "暂不"},
		{KindPersonReappeared, "open_person", "Show person", "查看人物"},
		{KindWaitingOnReply, "open", "Review", "查看"},
		{KindCalendarConflict, "keep_both", "Keep both", "两个都保留"},
		{KindAnswerReady, "open", "Read it", "阅读"},
	} {
		for _, lang := range Languages {
			want := tt.en
			if lang == "zh-CN" {
				want = tt.zh
			}
			set, _ := Copy(tt.kind, lang)
			if got := set.Actions[tt.action]; got != want {
				t.Errorf("%s %s action %s = %q, want %q", tt.kind, lang, tt.action, got, want)
			}
		}
	}

	for _, lang := range Languages {
		want := "Take me there"
		if lang == "zh-CN" {
			want = "带我过去"
		}
		set, _ := Copy(KindDecisionDue, lang, map[string]string{"_activity_thread": "1"})
		if got := set.Actions["open"]; got != want {
			t.Errorf("thread outcome %s open action = %q, want %q", lang, got, want)
		}
	}
}

func TestValidBodyRejectsForbiddenCopyAndOverlongText(t *testing.T) {
	for _, text := range []string{"Ready!", "好了！", "Yansu has a suggestion.", "This is urgent.", "Critical update", "Ready \U0001f600", "Ready \u2726", strings.Repeat("word ", 61), strings.Repeat("字", 241)} {
		if ValidBody(text) {
			t.Errorf("accepted forbidden copy %q", text)
		}
	}
	if !ValidBody(strings.Repeat("word ", 60)) || !ValidBody("Your brief is ready.") {
		t.Fatal("rejected valid short copy")
	}
}

func TestChatActionsOfferOnlyImplementedActions(t *testing.T) {
	allowed := map[string]bool{"open": true, "answer": true, "review": true, "open_settings": true, "resume_capture": true, "make_it_now": true, "run_now": true, "done": true, "keep": true, "correct": true, "see": true, "open_person": true, "add_to_upcoming": true, "do_once": true, "not_now": true, "dismiss": true, "keep_both": true, "copy_draft": true, "send": true}
	for _, kind := range AllKinds() {
		for _, action := range ChatActions(kind) {
			if !allowed[action.ID] {
				t.Errorf("%s advertises unavailable action %q", kind, action.ID)
			}
			if action.ID == "not_now" &&
				kind != KindSuggestion && kind != KindWaitingOnReply && kind != KindLearnedToConfirm && kind != KindReminder {
				t.Errorf("%s advertises an unexpected Not now action", kind)
			}
			if (action.ID == "add_to_upcoming" || action.ID == "do_once") && kind != KindSuggestion {
				t.Errorf("%s advertises a suggestion-only write", kind)
			}
		}
	}
}

func TestStatusReportsLocalizeAndBoundSourceTitles(t *testing.T) {
	for _, lang := range Languages {
		for _, kind := range []string{"handoff_completed", "schedule_ok", "schedule_error", "schedule_missed_once"} {
			fallback := StatusReport(lang, kind, "")
			if fallback == "" || !ValidBody(fallback) {
				t.Fatalf("missing status fallback %s %s: %q", lang, kind, fallback)
			}
			if lang == "zh-CN" && (strings.Contains(fallback, "schedule") || strings.Contains(fallback, "work")) {
				t.Fatalf("English fallback in Chinese: %q", fallback)
			}
			for _, title := range []string{"Yansu report", "Ready!", strings.Repeat("word ", 61)} {
				if got := StatusReport(lang, kind, title); got != fallback {
					t.Errorf("%s %s unsafe title survived: %q", lang, kind, got)
				}
			}
		}
	}
}

func TestCopyLanguageFallback(t *testing.T) {
	en, _ := Copy(KindBriefReady, "en")
	if got, _ := Copy(KindBriefReady, "fr"); got.PushBody != en.PushBody {
		t.Fatalf("unknown language must fall back to English: %+v", got)
	}
	zh, _ := Copy(KindBriefReady, "zh-CN")
	if got, _ := Copy(KindBriefReady, "zh-Hans"); got.PushBody != zh.PushBody {
		t.Fatalf("zh variants must map to zh-CN: %+v", got)
	}
	if _, ok := Copy(Kind("unknown"), "en"); ok {
		t.Fatal("unknown kinds must report false")
	}
}

func TestRender(t *testing.T) {
	set, _ := Copy(KindWorkBlocked, "en")
	got := Render(set.Sealed, map[string]string{"title": "The expense export", "question": "the project code."})
	if got != "The expense export needs the project code." {
		t.Fatalf("Render = %q", got)
	}
	if got := Render("{a} and {missing} done", map[string]string{"a": "x"}); got != "x and done" {
		t.Fatalf("missing params must not leak: %q", got)
	}
	if got := Render("no placeholders", nil); got != "no placeholders" {
		t.Fatalf("plain text changed: %q", got)
	}
	// Braces inside a value are text, not placeholders, and never loop.
	if got := Render("{a} and {b}", map[string]string{"a": "{a}", "b": "{missing} {a}"}); got != "{a} and {missing} {a}" {
		t.Fatalf("values must be written through unchanged: %q", got)
	}
	if got := Render("{a", map[string]string{"a": "x"}); got != "{a" {
		t.Fatalf("an unterminated placeholder stays as written: %q", got)
	}
}

func TestEveryCopyTemplateFillsEveryPlaceholder(t *testing.T) {
	params := map[string]string{
		"title": "Planning", "due": "today", "decisions": "2", "followups": "1",
		"name": "Dana", "where": "your notes", "question": "your answer.",
		"slot": "morning", "count": "3", "topic": "Design", "statement": "You prefer short notes.",
		"subject": "Design", "first_line": "A result is final.",
		"what": "Screen capture", "duration": "20 minutes", "minutes": "15", "place": "on the video link",
		"people": "with Dana", "summary": "Two events overlap.", "site": "the project page",
		"time": "09:00", "result": "Your summary is ready.", "detail": "A short summary.",
		"headline_subject": "Planning", "waiting_since": "Tuesday, Oct 13",
	}
	for _, lang := range Languages {
		for _, kind := range AllKinds() {
			set, _ := Copy(kind, lang, params)
			for _, template := range []string{set.PushBody, set.Sealed, set.Text, set.EmailSubject, set.EmailBody} {
				for _, match := range regexp.MustCompile(`\{([a-z_]+)\}`).FindAllStringSubmatch(template, -1) {
					if _, exists := params[match[1]]; !exists {
						t.Errorf("%s %s fixture does not fill %s", kind, lang, match[1])
					}
				}
				if got := Render(template, params); strings.ContainsAny(got, "{}") {
					t.Errorf("%s %s has unfilled template: %q", kind, lang, got)
				}
				if got := Render(template, nil); strings.ContainsAny(got, "{}") {
					t.Errorf("%s %s missing evidence leaks placeholder: %q", kind, lang, got)
				}
			}
		}
	}
}

func TestWaitingReplyCopyWithholdsSubjectUnlessHeadlineSubjectExists(t *testing.T) {
	for _, lang := range Languages {
		for _, params := range []map[string]string{
			{"name": "Dana", "subject": "PRIVATE SUBJECT"},
			{"name": "Dana", "subject": "PRIVATE SUBJECT", "waiting_since": "Tuesday, Oct 13"},
		} {
			set, _ := Copy(KindWaitingOnReply, lang, params)
			for _, template := range []string{set.Sealed, set.Text} {
				got := Render(template, params)
				if strings.ContainsAny(got, "{}") || strings.Contains(got, "PRIVATE SUBJECT") || strings.Contains(got, "about .") || strings.Contains(got, "“”") {
					t.Errorf("%s missing subject fallback = %q", lang, got)
				}
			}
		}
		params := map[string]string{"name": "Dana", "headline_subject": "Planning", "waiting_since": "Tuesday, Oct 13"}
		set, _ := Copy(KindWaitingOnReply, lang, params)
		if got := Render(set.Sealed, params); !strings.Contains(got, "Planning") || !strings.Contains(got, "Tuesday, Oct 13") {
			t.Errorf("%s approved subject and date missing: %q", lang, got)
		}
	}
}

func TestProactiveVoiceActionsKeepOnlyImplementedChoices(t *testing.T) {
	for _, kind := range []Kind{KindCommitmentSlipping, KindCapturePaused} {
		spec := mustSpec(t, kind)
		if len(spec.Actions) != 1 || len(ChatActions(kind)) != 1 {
			t.Fatalf("%s advertises unavailable snooze or ownership action", kind)
		}
	}
	withoutDraft, withDraft := ChatActions(KindWaitingOnReply), ChatActions(KindWaitingOnReply, true)
	withSend := ChatActions(KindWaitingOnReply, true, true)
	if len(withoutDraft) != 2 || withoutDraft[0].ID != "open" || withoutDraft[1].ID != "not_now" || len(withDraft) != 3 || withDraft[0].ID != "copy_draft" || len(withSend) != 4 || withSend[0].ID != "send" {
		t.Fatalf("draft action gating: without=%v with=%v send=%v", withoutDraft, withDraft, withSend)
	}
	for _, lang := range Languages {
		set, _ := Copy(KindCommitmentSlipping, lang)
		want := "I have no record of progress on it"
		if lang == "zh-CN" {
			want = "我没有相关进展的记录"
		}
		if !strings.Contains(set.Sealed, want) || !strings.Contains(set.Text, want) {
			t.Errorf("%s commitment copy claims absence rather than the recorded view", lang)
		}
	}
}

func TestBriefReadyPushNamesItsSlot(t *testing.T) {
	for _, tc := range []struct{ lang, slot, want string }{
		{"en", "morning", "Your morning brief is ready."}, {"en", "evening", "Your evening brief is ready."},
		{"zh-CN", "morning", "你的早间简报已经准备好了。"}, {"zh-CN", "evening", "你的晚间简报已经准备好了。"},
	} {
		params := map[string]string{"slot": tc.slot}
		got, _ := Copy(KindBriefReady, tc.lang, params)
		if got.PushBody != tc.want || Render(got.Sealed, params) != tc.want {
			t.Errorf("%s %s: %+v", tc.lang, tc.slot, got)
		}
	}
}

func TestBriefReadyCopyOmitsUnknownSlot(t *testing.T) {
	for _, tc := range []struct{ lang, want, wantText string }{
		{"en", "Your brief is ready.", "Isoform: your brief is ready on your computer."},
		{"zh-CN", "你的简报已经准备好了。", "Isoform：你的简报已在电脑上准备好了。"},
	} {
		for _, slot := range []string{"", "unknown"} {
			params := map[string]string{"slot": slot}
			got, _ := Copy(KindBriefReady, tc.lang, params)
			if got.PushBody != tc.want || Render(got.Sealed, params) != tc.want || Render(got.Text, params) != tc.wantText {
				t.Errorf("%s %q: %+v", tc.lang, slot, got)
			}
		}
	}
}

// settings-review ST66: only a stop nobody chose is announced, with its cause.
func TestCapturePausedCopyNamesTheCause(t *testing.T) {
	for _, tc := range []struct{ lang, cause, want string }{
		{"en", "permission", "Screen capture has been off for 20 minutes: its permission was turned off in System Settings. Turn it back on?"},
		{"en", "stopped", "Screen capture has been off for 20 minutes: it stopped on its own. Turn it back on?"},
		{"zh-CN", "permission", "屏幕捕获已经停了20 分钟：系统设置里关闭了它的权限。要重新打开吗？"},
	} {
		p := map[string]string{"what": map[string]string{"en": "Screen capture", "zh-CN": "屏幕捕获"}[tc.lang], "duration": "20 minutes", "cause": tc.cause}
		if tc.lang == "zh-CN" {
			p["duration"] = "20 分钟"
		}
		set, ok := Copy(KindCapturePaused, tc.lang, p)
		if !ok {
			t.Fatalf("no %s copy", tc.lang)
		}
		if got := Render(set.Sealed, p); got != tc.want {
			t.Errorf("%s %s: got %q, want %q", tc.lang, tc.cause, got, tc.want)
		}
	}
}

func TestBriefMissedCopyNamesOnlyARecordedReason(t *testing.T) {
	cases := []struct {
		lang   string
		params map[string]string
		want   string
	}{
		{"en", map[string]string{"slot": "morning", "reason": "failed", "time": "07:30"}, "Your morning brief didn't come at 7:30 AM: it couldn't be made."},
		{"en", map[string]string{"slot": "morning", "time": "07:30"}, "Your morning brief didn't come at 7:30 AM."},
		{"en", map[string]string{"slot": "morning", "reason": "asleep", "time": "07:30"}, "Your morning brief didn't come at 7:30 AM: your computer was asleep."},
		{"en", map[string]string{"slot": "evening", "reason": "closed"}, "Your evening brief didn't come: Isoform was closed."},
		{"zh-CN", map[string]string{"slot": "早间", "reason": "asleep"}, "你的早间简报没有来：当时电脑在睡眠。"},
		{"en", map[string]string{"slot": "morning", "reason": "guessed"}, "Your morning brief didn't come."},
		{"zh-CN", map[string]string{"slot": "早间", "reason": "failed"}, "你的早间简报没有来：没能生成。"},
		{"zh-CN", map[string]string{"slot": "早间", "time": "07:30"}, "你的早间简报在7:30没有来。"},
		{"zh-CN", map[string]string{"slot": "晚间", "time": "18:00"}, "你的晚间简报在18:00没有来。"},
	}
	for _, tc := range cases {
		set, ok := Copy(KindBriefMissed, tc.lang, tc.params)
		if !ok {
			t.Fatalf("no %s copy", tc.lang)
		}
		if got := Render(set.Sealed, tc.params); got != tc.want {
			t.Fatalf("%s %v: got %q, want %q", tc.lang, tc.params, got, tc.want)
		}
		if tc.lang == "zh-CN" && tc.params["reason"] != "" {
			if got := Render(set.Sealed, ChatParams(map[string]string{"slot": "morning", "reason": tc.params["reason"]}, "zh-CN")); strings.Contains(got, "morning") {
				t.Fatalf("zh-CN copy kept the English slot: %q", got)
			}
		}
		if set.Actions["make_it_now"] == "" {
			t.Fatalf("%s missed brief lost Make it now", tc.lang)
		}
	}
}
