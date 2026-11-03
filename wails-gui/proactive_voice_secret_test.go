package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode"

	"wails-gui/internal/proactivevoice"
	"wails-gui/storage"
)

func TestProactiveVoiceStoredChatCodesAreRedactedBeforeProvider(t *testing.T) {
	proactiveVoiceFallbackTestHome(t)
	for _, tc := range []struct {
		name, language, content string
	}{
		{"generic code", "en", "Dana Ruiz: Your code is 482913"},
		{"PIN", "en", "Dana Ruiz: Your PIN is 1234"},
		{"grouped spaces", "en", "Dana Ruiz: Your code is 482 913"},
		{"individual digits", "en", "Dana Ruiz: Your code is 4 8 2 9 1 3"},
		{"repeated spaces", "en", "Dana Ruiz: Your code is 4  8  2  9  1  3"},
		{"tabs", "en", "Dana Ruiz: Your code is 4\t8\t2\t9\t1\t3"},
		{"nonbreaking spaces", "en", "Dana Ruiz: Your code is 4\u00a08\u00a02\u00a09\u00a01\u00a03"},
		{"hyphenated", "en", "Dana Ruiz: Your code is 482-913"},
		{"nonbreaking hyphen", "en", "Dana Ruiz: Your code is 482\u2011913"},
		{"en dash", "en", "Dana Ruiz: Your code is 482\u2013913"},
		{"fullwidth hyphen and digits", "zh-CN", "Dana Ruiz：你的验证码是４８２－９１３。"},
		{"Unicode hyphen", "en", "Dana Ruiz: Your code is 482\u2010913"},
		{"figure dash", "en", "Dana Ruiz: Your code is 482\u2012913"},
		{"em dash", "en", "Dana Ruiz: Your code is 482\u2014913"},
		{"minus sign", "en", "Dana Ruiz: Your code is 482\u2212913"},
		{"small hyphen", "en", "Dana Ruiz: Your code is 482\ufe63913"},
		{"mixed spaced dashes", "en", "Dana Ruiz: Your PIN is 1 \u2011 2\u20133 \uff0d 4"},
		{"fullwidth full stop", "zh-CN", "Dana Ruiz：你的验证码是４８２．９１３。"},
		{"middle dot", "en", "Dana Ruiz: Your code is 482·913"},
		{"division slash", "en", "Dana Ruiz: Your code is 482∕913"},
		{"parentheses", "en", "Dana Ruiz: Your code is (482) (913)"},
		{"zero width separator", "en", "Dana Ruiz: Your code is 482\u200b913"},
		{"word joiner", "en", "Dana Ruiz: Your code is 482\u2060913"},
		{"security label ASCII groups", "en", "Dana Ruiz: security code: 482-913"},
		{"security label nonbreaking hyphen", "en", "Dana Ruiz: security code: 482\u2011913"},
		{"security label en dash", "en", "Dana Ruiz: security code: 482\u2013913"},
		{"verification label en dash", "en", "Dana Ruiz: verification: 482\u2013913"},
		{"Chinese verification label", "zh-CN", "Dana Ruiz：验证码: 482\u2013913。"},
		{"security label fullwidth groups", "zh-CN", "Dana Ruiz：security code: ４８２－９１３。"},
		{"dotted", "en", "Dana Ruiz: Your code is 482.913"},
		{"comma grouped", "en", "Dana Ruiz: Your code is 482,913"},
		{"comma separated", "en", "Dana Ruiz: Your PIN is 1, 2, 3, 4"},
		{"Chinese code", "zh-CN", "Dana Ruiz：你的验证码是482913。"},
		{"Chinese PIN", "zh-CN", "Dana Ruiz：你的个人识别码是1234。"},
		{"fullwidth digits", "zh-CN", "Dana Ruiz：你的验证码是４８２９１３。"},
		{"fullwidth comma groups", "zh-CN", "Dana Ruiz：你的验证码是４８２，９１３。"},
		{"Arabic Indic digits", "en", "Dana Ruiz: Your code is ٤٨٢٩١٣"},
		{"Persian digits", "en", "Dana Ruiz: Your code is ۴۸۲۹۱۳"},
		{"Devanagari digits", "en", "Dana Ruiz: Your code is ४८२९१३"},
		{"mathematical digits", "en", "Dana Ruiz: Your code is 𝟜𝟠𝟚𝟡𝟙𝟛"},
		{"mixed decimal glyphs", "en", "Dana Ruiz: Your code is 4８2۹१𝟛"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := mainChatTestApp(t)
			ctx := context.Background()
			now := time.Now()
			moment := proactiveVoiceTestMail(t, app, now)
			main, err := app.ensureMainChat(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err := app.store.SetUIState(ctx, uiLanguageStateKey, tc.language); err != nil {
				t.Fatal(err)
			}
			if _, err := app.store.AppendStreamItem(ctx, main.LocalID, storage.StreamItemRow{
				ID: "user-code", Kind: "user", Content: tc.content, CreatedAt: now.Add(-time.Minute).UnixMilli(),
			}); err != nil {
				t.Fatal(err)
			}
			// The real stored user message reaches the source adapter unchanged;
			// the provider boundary must redact it before any assistant sees it.
			source, reason := app.loadProactiveVoiceSource(ctx, moment, now, tc.language)
			if reason != "" || len(source.UserWords) != 1 || source.UserWords[0].Text != tc.content {
				t.Fatalf("stored message was not selected: reason=%q words=%d", reason, len(source.UserWords))
			}
			runner := &proactiveVoiceTestRunner{raw: `{"headline":"","body":"","draft":null,"actions":[],"evidence":[],"abstain":true,"reason":"no_source"}`}
			app.runner = proactiveVoiceTestRouter(runner)
			item, ok := app.proactiveItem(moment, engagementText{})
			if runner.calls != 1 || runner.input == nil {
				t.Fatalf("provider job was not attempted: displayable=%v calls=%d", ok, runner.calls)
			}
			if ok {
				encoded, err := json.Marshal(item)
				if err != nil || strings.Contains(string(encoded), tc.content) {
					t.Fatalf("stored card retained the user's code: %s", encoded)
				}
			}
			var job proactivevoice.Job
			if err := json.Unmarshal([]byte(runner.input.Prompt), &job); err != nil {
				t.Fatal(err)
			}
			if job.Locale != tc.language || len(job.UserWords) != 1 || job.UserWords[0].Source != "chat" || len(job.UserWords[0].About) == 0 {
				t.Fatal("provider job lost the attributed user message")
			}
			text := job.UserWords[0].Text
			if strings.IndexFunc(text, unicode.IsDigit) >= 0 || !strings.Contains(text, "[REDACTED:") || !strings.Contains(text, "Dana Ruiz") {
				t.Fatal("provider job retained code digits or lost the safe context")
			}
			stored, err := app.store.LoadStreamItem(ctx, "user-code")
			if err != nil || stored == nil || stored.Content != tc.content {
				t.Fatal("provider redaction changed the user's stored message")
			}
		})
	}
}

func TestProactiveVoiceSecretRedactionPreservesComputedCalendarFacts(t *testing.T) {
	proactiveVoiceFallbackTestHome(t)
	app := mainChatTestApp(t)
	now := time.Date(2026, 10, 16, 9, 45, 0, 0, time.Local)
	event := proactiveVoiceTestEvent(t, app, now)
	for _, language := range []string{"en", "zh-CN"} {
		source, reason := app.loadProactiveVoiceSource(context.Background(), meetingPrepMoment(event, now, time.Local), now, language)
		if reason != "" {
			t.Fatal(reason)
		}
		job, _, reason := assembleProactiveVoiceJob("meeting_prep", language, true, now, source)
		if reason != "" || len(job.Items) != 1 {
			t.Fatalf("calendar job unavailable: %q", reason)
		}
		item := job.Items[0]
		if item.Start == nil || item.StartsIn == nil || item.AttendeeTotal == nil || item.Start.Display != source.Items[0].Start.Display || item.StartsIn.Display != source.Items[0].StartsIn.Display || item.AttendeeTotal.Display != source.Items[0].AttendeeTotal.Display {
			t.Fatal("code redaction changed computed calendar facts")
		}
	}
}

func TestProactiveVoiceSecretRedactionPreservesShortNumbers(t *testing.T) {
	for _, text := range []string{
		"3.5 stars, room 12, version v2.1",
		"Dana Ruiz: Follow‑up at room 12 – bring 3–5 pages",
		"Dana Ruiz：会议－准备，请带３页。",
	} {
		// Decimal glyphs are normalized, but short quantities and punctuation
		// outside candidate code runs must survive the provider boundary.
		want := strings.ReplaceAll(text, "３", "3")
		if proactiveVoiceClean(text, nil, 100) != want {
			t.Fatal("code redaction changed short non-secret numbers or prose")
		}
	}
}

func TestProactiveVoiceCodeRedactionPreservesPIIBoundary(t *testing.T) {
	for _, tc := range []struct{ text, want string }{
		{"Dana Ruiz: api_key: example-api-key-value", "Dana Ruiz: api_key: [REDACTED:api_key]"},
		{"Dana Ruiz: api_key: 1-2-3-4-5-6-a-b-c-d-e", "Dana Ruiz: api_key: [REDACTED:api_key]"},
		{"Dana Ruiz: sk-1234-abcd", "Dana Ruiz: [REDACTED:api_key]"},
		{"Dana Ruiz: 482‑[REDACTED:cvv]–913", "Dana Ruiz: [REDACTED:code]"},
		{"Dana Ruiz: ４８２－[REDACTED:one_time_password]", "Dana Ruiz: [REDACTED:code]"},
		{"Dana Ruiz: [REDACTED:cvv]‑9–1－3", "Dana Ruiz: [REDACTED:code]"},
	} {
		if got := proactiveVoiceClean(tc.text, nil, 100); got != tc.want {
			t.Fatal("code redaction fragmented a recognized secret")
		}
	}
}
