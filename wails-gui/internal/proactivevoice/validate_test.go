package proactivevoice

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func baseCase() (Job, Output) {
	job := Job{Contract: Contract, Kind: "capture_paused", Locale: "en", OutputLanguage: "en", Surface: "main_chat", NamesAllowed: true, MaxNames: 3, MailFlag: "none", Limits: DefaultLimits("en", false), Actions: Actions("capture_paused", false), People: []Person{}, UserWords: []UserWords{}, Items: []Item{{Ref: "c1", Type: "capture_status", What: &Display{"Screen capture"}, OffFor: &Display{"20 minutes"}}}}
	out := Output{Headline: "Screen capture has been off for 20 minutes.", Body: "Turn it back on from the menu bar.", Actions: job.Actions, Evidence: []string{"c1"}}
	return job, out
}

func checkOutput(t *testing.T, job Job, out Output, denylist []string, want Rejection) {
	t.Helper()
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	got, reject := Validate(job, raw, denylist)
	if reject != want {
		t.Fatalf("rejection = %q, want %q; output = %s", reject, want, raw)
	}
	if reject != "" && (got.Headline != "" || got.Body != "" || got.Draft != nil) {
		t.Fatal("rejected partial output escaped")
	}
}

func TestReviewedFixtures(t *testing.T) {
	// The repository contains four worked examples but no original eight
	// fixture files. Preserve all four literally and add the four documented
	// headers-only, ISO-date, money and injected-echo regression cases.
	raw, err := os.ReadFile("testdata/fixtures.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name   string
		Job    Job
		Output Output
		Reject Rejection
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != 8 {
		t.Fatalf("fixtures = %d, want 8", len(cases))
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) { checkOutput(t, tc.Job, tc.Output, nil, tc.Reject) })
	}
}

func TestSystemPromptMatchesReviewedDocument(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/proactive-voice-prompt.md")
	if err != nil {
		t.Fatal(err)
	}
	document := string(raw)
	start := strings.Index(document, "```text\n")
	if start < 0 {
		t.Fatal("reviewed prompt missing")
	}
	rest := document[start+len("```text\n"):]
	end := strings.Index(rest, "\n```")
	if end < 0 || SystemPrompt != rest[:end] {
		t.Fatal("system prompt differs from the reviewed package")
	}
	if PromptVersion == "" {
		t.Fatal("prompt version missing")
	}
}

func TestChecksInOrder(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Job, *Output)
		reject Rejection
	}{
		{"valid", func(*Job, *Output) {}, ""},
		{"missing_evidence", func(_ *Job, o *Output) { o.Evidence = []string{} }, RejectShape},
		{"unknown_ref", func(_ *Job, o *Output) { o.Evidence = []string{"unknown"} }, RejectRefs},
		{"uncited_ref", func(j *Job, o *Output) {
			j.Items = append(j.Items, Item{Ref: "x", Type: "calendar_event", Title: Text("Unrelated gathering")})
			o.Evidence = append(o.Evidence, "x")
		}, RejectRefs},
		{"writing_sample_ref", func(j *Job, o *Output) {
			j.WritingSamples = []WritingSample{{Ref: "s1", Text: "Screen capture"}}
			o.Evidence = append(o.Evidence, "s1")
		}, RejectRefs},
		{"forbidden_source", func(j *Job, _ *Output) { j.Items[0].Type = "screen_note" }, RejectRefs},
		{"unsupported_number", func(_ *Job, o *Output) { o.Headline = "Screen capture has been off for 21 minutes." }, RejectFacts},
		{"whole_number", func(j *Job, _ *Output) { j.Items[0].OffFor = &Display{"120 minutes"} }, RejectFacts},
		{"wrong_unit", func(_ *Job, o *Output) { o.Headline = "Screen capture has been off for 20 hours." }, RejectFacts},
		{"free_text_number", func(j *Job, o *Output) {
			j.Items[0].Text = "42 minutes"
			o.Headline = "Screen capture has been off for 42 minutes."
		}, RejectFacts},
		{"free_text_day", func(j *Job, o *Output) { j.Items[0].Text = "Tuesday"; o.Body = "Open it on Tuesday." }, RejectFacts},
		{"number_word", func(_ *Job, o *Output) { o.Body = "Open one item." }, RejectFacts},
		{"headline_limit", func(j *Job, _ *Output) { j.Limits.Headline.Max = 5 }, RejectLimits},
		{"body_word_limit", func(j *Job, _ *Output) { j.Limits.Body.Max = 2 }, RejectLimits},
		{"unknown_unit", func(j *Job, _ *Output) { j.Limits.Body.Unit = "bytes" }, RejectLimits},
		{"exclamation", func(_ *Job, o *Output) { o.Body += "!" }, RejectBanned},
		{"emoji", func(_ *Job, o *Output) { o.Body += "\U0001f600" }, RejectBanned},
		{"urgent", func(_ *Job, o *Output) { o.Body = "Open the urgent item." }, RejectBanned},
		{"saved", func(_ *Job, o *Output) { o.Body = "I saved the source." }, RejectBanned},
		{"whole_banned_word", func(_ *Job, o *Output) { o.Body = "The consent is present." }, ""},
		{"link", func(_ *Job, o *Output) { o.Body = "Open example.com." }, RejectLinks},
		{"http", func(_ *Job, o *Output) { o.Body = "Open http." }, RejectLinks},
		{"mailto", func(_ *Job, o *Output) { o.Body = "Open mailto:person@example.test." }, RejectLinks},
		{"markup", func(_ *Job, o *Output) { o.Body = "Open <strong>the source</strong>." }, RejectLinks},
		{"backslash", func(_ *Job, o *Output) { o.Body = "Open the \\ source." }, RejectLinks},
		{"money", func(_ *Job, o *Output) { o.Body = "Open the source for dollars." }, RejectSecrets},
		{"secret_without_digit", func(_ *Job, o *Output) { o.Body = "Open the password source." }, RejectSecrets},
		{"draft_disallowed", func(j *Job, o *Output) {
			j.Limits.Draft = &Limit{"words", 120}
			o.Draft = &Draft{To: "addr_1", Subject: "", Text: "Thanks for waiting."}
		}, RejectDraft},
		{"closing_without_draft", func(_ *Job, o *Output) { o.Body = ClosingLine("en") }, RejectClosing},
		{"promise", func(_ *Job, o *Output) { o.Body = "I will open the source." }, RejectPromise},
		{"action_table", func(j *Job, o *Output) { j.Actions = []string{"dismiss"}; o.Actions = j.Actions }, RejectActions},
		{"echo_six_words", func(j *Job, o *Output) {
			j.Items[0].Text = "Open the source and read the whole message."
			o.Body = "Open the source and read the whole message."
		}, RejectEcho},
		{"echo_five_words", func(j *Job, o *Output) {
			j.Items[0].Text = "Open the source and read."
			o.Body = "Open the source and read."
		}, ""},
		{"quoted_echo_exempt", func(j *Job, o *Output) {
			j.Items[0].Text = "Open the source and read the whole message."
			o.Body = "You wrote “Open the source and read the whole message.”"
		}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { j, o := baseCase(); tc.change(&j, &o); checkOutput(t, j, o, nil, tc.reject) })
	}
}

func TestStrictShape(t *testing.T) {
	job, out := baseCase()
	raw, _ := json.Marshal(out)
	cases := []string{
		" " + string(raw), string(raw) + "\n", string(raw) + string(raw), "```json\n" + string(raw) + "\n```",
		strings.Replace(string(raw), `"headline":`, `"extra":null,"headline":`, 1),
		strings.Replace(string(raw), `"draft":null`, `"draft":{"to":"addr_1","subject":"","text":"a","extra":true}`, 1),
		strings.Replace(string(raw), `"draft":null`, `"draft":{"to":"addr_1","to":"addr_1","subject":"","text":"a"}`, 1),
		strings.Replace(string(raw), `"draft":null`, `"draft":{"to":null,"subject":"","text":"a"}`, 1),
		strings.Replace(string(raw), `"abstain":false`, `"abstain":null`, 1),
		strings.Replace(string(raw), `"reason":null`, `"reason":null,"reason":null`, 1),
		strings.Replace(string(raw), `"actions":["open_settings"]`, `"actions":null`, 1),
		strings.Replace(string(raw), `"headline":`, `"body":`, 1),
	}
	for i, raw := range cases {
		if _, reject := Validate(job, []byte(raw), nil); reject != RejectShape {
			t.Errorf("case %d: %s", i, reject)
		}
	}
}

func TestChineseRules(t *testing.T) {
	job, out := baseCase()
	job.Locale = "zh-CN"
	job.OutputLanguage = "zh-CN"
	job.Limits = DefaultLimits("zh-CN", false)
	job.Items[0].What = &Display{"屏幕捕获"}
	job.Items[0].OffFor = &Display{"20 分钟"}
	out.Headline = "屏幕捕获已关闭 20 分钟。"
	out.Body = "从菜单栏重新开启。"
	checkOutput(t, job, out, nil, "")
	for _, phrase := range []string{"没有回复", "没回复", "未回复", "忽略了", "忘了", "紧急", "严重", "关键", "温馨提示", "别忘了", "加油", "已发送", "已保存", "已预约", "已回复", "取消了", "改期"} {
		t.Run(phrase, func(t *testing.T) { o := out; o.Body = phrase; checkOutput(t, job, o, nil, RejectBanned) })
	}
	for _, phrase := range []string{"我会", "会把", "会给", "会先", "打算", "计划", "答应", "说会", "之前", "前给"} {
		t.Run(phrase, func(t *testing.T) { o := out; o.Body = phrase; checkOutput(t, job, o, nil, RejectPromise) })
	}
	o := out
	o.Body = "会议在菜单栏显示。"
	checkOutput(t, job, o, nil, "")
	o.Body = strings.Repeat("好", 120)
	checkOutput(t, job, o, nil, "")
	o.Body += "。"
	checkOutput(t, job, o, nil, RejectLimits)
	o = out
	o.Body = "有三个人。"
	checkOutput(t, job, o, nil, RejectFacts)
	o = out
	o.Body = "从这里打开并仔细阅读原文内容"
	job.Items[0].Text = o.Body
	checkOutput(t, job, o, nil, RejectEcho)
}

func TestNamesDenylist(t *testing.T) {
	job, out := baseCase()
	job.NamesAllowed = false
	for _, name := range []string{"Dana Ruiz", "林悦"} {
		o := out
		o.Body = "Open the source with " + name + "."
		checkOutput(t, job, o, []string{name}, RejectNames)
	}
	out.Body = "Open the source with Danaish."
	checkOutput(t, job, out, []string{"Dana"}, RejectFacts)
	out.Body = "Open the source with danaish."
	checkOutput(t, job, out, []string{"Dana"}, "")
}

func TestActionTableAndLimits(t *testing.T) {
	for _, kind := range []string{"waiting_on_reply", "meeting_followup", "quiet_thread", "admin_item", "commitment_slipping", "meeting_prep", "brief_ready", "capture_paused"} {
		if Guidance(kind) == "" {
			t.Errorf("missing guidance %s", kind)
		}
	}
	for _, kind := range []string{"admin_item", "meeting_followup", "quiet_thread", "work_finished"} {
		if Supported(kind) {
			t.Errorf("unsupported kind enabled %s", kind)
		}
	}
	a := Actions("waiting_on_reply", true)
	a[0] = "send"
	if Actions("waiting_on_reply", true)[0] != "copy_draft" {
		t.Fatal("mutable action authority")
	}
	if got := StripLinks("read https://example.com and a@example.test"); strings.Contains(got, "example") {
		t.Fatal("link not stripped")
	}
}

func draftCase(t *testing.T) (Job, Output) {
	t.Helper()
	raw, err := os.ReadFile("testdata/fixtures.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Job    Job
		Output Output
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	return cases[0].Job, cases[0].Output
}

func TestDraftContract(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Job, *Output)
		reject Rejection
	}{
		{"valid_opaque_token", func(*Job, *Output) {}, ""},
		{"missing_required_draft", func(_ *Job, o *Output) { o.Draft = nil }, RejectDraft},
		{"wrong_token", func(_ *Job, o *Output) { o.Draft.To = "addr_2" }, RejectDraft},
		{"raw_address", func(_ *Job, o *Output) { o.Draft.To = "dana@example.test" }, RejectDraft},
		{"raw_address_in_job", func(j *Job, o *Output) { j.DraftTo = "dana@example.test"; o.Draft.To = j.DraftTo }, RejectDraft},
		{"changed_subject", func(_ *Job, o *Output) { o.Draft.Subject = "Different subject" }, RejectDraft},
		{"empty_draft", func(_ *Job, o *Output) { o.Draft.Text = " " }, RejectDraft},
		{"mail_flag", func(j *Job, _ *Output) { j.MailFlag = "link" }, RejectDraft},
		{"blank_run", func(_ *Job, o *Output) { o.Draft.Text = "Dana, thank you. ___" }, RejectDraft},
		{"blank_marker", func(_ *Job, o *Output) { o.Draft.Text = "Dana, thank you. fill in" }, RejectDraft},
		{"missing_closing", func(_ *Job, o *Output) { o.Body = strings.TrimSuffix(o.Body, ClosingLine("en")) }, RejectClosing},
		{"closing_not_last", func(_ *Job, o *Output) { o.Body += " Open it." }, RejectClosing},
		{"passed_day_not_acknowledged", func(_ *Job, o *Output) { o.Body = strings.Replace(o.Body, ", and that day has passed", "", 1) }, RejectPromise},
		{"draft_secret", func(_ *Job, o *Output) { o.Draft.Text = "Dana, thank you. I will get you the password." }, RejectSecrets},
		{"draft_promises_without_words", func(j *Job, o *Output) {
			j.UserWords = nil
			o.Evidence = []string{"m1", "p1"}
			o.Body = "Here is a draft in the way you usually write. " + ClosingLine("en")
		}, RejectPromise},
		{"draft_echo_style", func(j *Job, o *Output) {
			j.WritingSamples = []WritingSample{{Ref: "s1", Text: "Dana, thank you for your patience."}}
			o.Draft.Text = j.WritingSamples[0].Text
		}, RejectEcho},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { j, o := draftCase(t); tc.change(&j, &o); checkOutput(t, j, o, nil, tc.reject) })
	}
}

func TestMoneyAndDigitRuns(t *testing.T) {
	for _, marker := range []string{"$", "€", "£", "¥", "￥", "%", "USD", "EUR", "GBP", "CNY", "RMB", "dollars", "euros", "yuan", "cents", "percent", "元", "块", "美元", "美金", "欧元", "英镑", "日元", "人民币", "百分之"} {
		t.Run(marker, func(t *testing.T) {
			j, o := baseCase()
			j.Items[0].Title = Text(marker)
			o.Body = "Open the source for " + marker + "."
			checkOutput(t, j, o, nil, RejectSecrets)
		})
	}
	j, o := baseCase()
	j.Items[0].Title = Text("1234")
	o.Body = "Open source 1234."
	checkOutput(t, j, o, nil, "")
	for _, run := range []string{"1 2 3 4", "1-2-3-4", "1.2.3.4", "1,2,3,4"} {
		t.Run(run, func(t *testing.T) {
			j, o := baseCase()
			j.Items[0].Title = Text(run)
			o.Body = "Open source " + run + "."
			checkOutput(t, j, o, nil, RejectSecrets)
		})
	}
}

func TestNamedPeopleAndCitedTitles(t *testing.T) {
	j, o := baseCase()
	j.People = []Person{{Ref: "p1", Name: Text("Dana Ruiz"), RoleLabel: "a colleague"}, {Ref: "p2", Name: Text("Mei Lin"), RoleLabel: "a colleague"}}
	j.MaxNames = 1
	o.Body = "Open the source with Dana Ruiz and Mei Lin."
	o.Evidence = append(o.Evidence, "p1", "p2")
	checkOutput(t, j, o, nil, RejectFacts)
	j.MaxNames = 2
	checkOutput(t, j, o, nil, "")
	o.Evidence = []string{"c1", "p1"}
	checkOutput(t, j, o, nil, RejectFacts)
	j, o = baseCase()
	o.Body = "Open the source with Alice."
	checkOutput(t, j, o, nil, RejectFacts)
	j, o = baseCase()
	j.Items = append(j.Items, Item{Ref: "k1", Type: "commitment", Title: Text("Private project")})
	o.Body = "Open Private project."
	checkOutput(t, j, o, nil, RejectFacts)
}

func TestWhitespaceCannotBypassBannedPhrases(t *testing.T) {
	j, o := baseCase()
	o.Body = "I have no \n\t reply."
	checkOutput(t, j, o, nil, RejectBanned)
	o.Body = "I am going \n\t to open it."
	checkOutput(t, j, o, nil, RejectPromise)
	j.NamesAllowed = false
	o.Body = "Open the source with Dana."
	checkOutput(t, j, o, []string{"Dana Ruiz"}, RejectNames)
	if whole("anything", " ") {
		t.Fatal("empty fact matched")
	}
}

func TestQuotedTitlesRequireCitedEvidence(t *testing.T) {
	j, o := baseCase()
	o.Body = "Open “hidden project agenda”."
	checkOutput(t, j, o, nil, RejectFacts)
	j.Items[0].Title = Text("hidden project agenda")
	checkOutput(t, j, o, nil, "")
	j.Items[0].Title = nil
	j.Items[0].Text = "You can open the source and read the whole message."
	o.Body = "You wrote “open the source and read the whole message”."
	checkOutput(t, j, o, nil, "")
}

func TestPromiseDatePrefixesRequireUserWords(t *testing.T) {
	for _, body := range []string{"Open the source by 15 October.", "Open the source before 20.", "Open the source by the 15th."} {
		t.Run(body, func(t *testing.T) {
			job, out := baseCase()
			job.Now.Display = "15 October, 20, 15th"
			out.Body = body
			checkOutput(t, job, out, nil, RejectPromise)
		})
	}
}

func TestEmojiOutsidePictographBlockIsBanned(t *testing.T) {
	for _, symbol := range []rune{0x231A, 0x231B, 0x23F0, 0x23F1, 0x2194, 0x21A9, 0x2328, 0x23CF, 0x24C2, 0x25B6, 0x2934, 0x2B50} {
		job, out := baseCase()
		out.Body += string(symbol)
		checkOutput(t, job, out, nil, RejectBanned)
	}
}

func TestUnicodeWhitespaceCannotSplitSecretDigitRun(t *testing.T) {
	for _, separator := range []string{"\t", "\n", "\r\n", "\u00a0", "\u0085", "\u2003", "\u2028"} {
		job, out := baseCase()
		job.Items[0].Title = Text("1 2 3 4")
		out.Body = "Open source " + strings.Join([]string{"1", "2", "3", "4"}, separator) + "."
		checkOutput(t, job, out, nil, RejectSecrets)
	}
}

func TestEchoExemptionsRequireWholeTokens(t *testing.T) {
	job, out := baseCase()
	job.People = []Person{{Ref: "p1", Name: Text("Lin"), RoleLabel: "a colleague"}}
	job.Items[0].Text = "Link the source and read everything."
	out.Body = job.Items[0].Text
	checkOutput(t, job, out, nil, RejectEcho)
	if got := exemptWholeValue("Link with Lin, then Linford", "Lin"); got != "Link with \x00, then Linford" {
		t.Fatalf("exemption matched partial name: %q", got)
	}
}

func TestRelativeDatesRequireDisplayEvidence(t *testing.T) {
	for _, date := range []string{"next month", "this week", "next year", "next Friday", "后天", "前天", "明年", "本周", "星期天"} {
		t.Run(date, func(t *testing.T) {
			job, out := baseCase()
			job.Now.Display = "Friday, Oct 16"
			out.Body = "Open the source " + date + "."
			checkOutput(t, job, out, nil, RejectFacts)
		})
	}
}

func TestDateCombinationsMustComeFromOneDisplay(t *testing.T) {
	for _, tc := range []struct{ now, source, body string }{
		{"Friday, Oct 16", "Tuesday, Oct 13", "Open the source Tuesday, Oct 16."},
		{"Friday, Oct 16", "Tuesday", "Open the source Tuesday Oct 16."},
		{"Friday, 16 October", "Tuesday, 13 October", "Open the source Tuesday, 16 October."},
		{"10 月 16 日（周五）", "10 月 13 日（周二）", "10 月 16 日（周二）打开记录。"},
		{"周五", "上周", "上周五打开记录。"},
	} {
		t.Run(tc.body, func(t *testing.T) {
			job, out := baseCase()
			job.Now.Display = tc.now
			job.Items[0].Start = &Display{tc.source}
			out.Body = tc.body
			checkOutput(t, job, out, nil, RejectFacts)
		})
	}
	job, out := baseCase()
	job.Items[0].Start = &Display{"Tuesday, Oct 13"}
	out.Body = "Open the source Tuesday, Oct 13."
	checkOutput(t, job, out, nil, "")
}

func TestUnicodeDecimalDigitsCannotBypassGrounding(t *testing.T) {
	for _, digits := range []string{"５", "٥", "１２３４５６７８", "١٢٣٤٥٦٧٨", "1２3４", "1٢3٤"} {
		t.Run(digits, func(t *testing.T) {
			job, out := baseCase()
			out.Body = "Open source " + digits + "."
			checkOutput(t, job, out, nil, RejectFacts)
			// Even a noncanonical source cannot authorize an alternate digit
			// script: the job contract uses assembled ASCII display numbers.
			job.Items[0].Title = Text(digits)
			checkOutput(t, job, out, nil, RejectFacts)
		})
	}
}

func TestUnicodeDecimalDigitsCannotBypassSecretRunCheck(t *testing.T) {
	for _, digits := range []string{"１２３４", "١٢٣٤", "1２3４", "1٢3٤", "１ ２ ３ ４", "١\t٢\t٣\t٤"} {
		job, _ := baseCase()
		job.Items[0].Title = Text(digits)
		if validDigitRuns("Open source "+digits+".", job, evidenceIndex(job)) {
			t.Errorf("secret run check accepted unsupported decimal digits %q", digits)
		}
	}
	job, _ := baseCase()
	job.Items[0].Title = Text("1234")
	if !validDigitRuns("Open source 1234.", job, evidenceIndex(job)) {
		t.Fatal("supported ASCII number was rejected")
	}
}

func TestQuantityAndUnitMustBeGroundedTogether(t *testing.T) {
	for _, tc := range []struct {
		name, display, quantity string
		want                    Rejection
	}{
		{"supported_minutes", "20 minutes", "20 minutes", ""},
		{"seconds", "20 minutes", "20 seconds", RejectFacts},
		{"months", "20 minutes", "20 months", RejectFacts},
		{"years", "20 minutes", "20 years", RejectFacts},
		{"superscript", "20 minutes", "²¹ minutes", RejectFacts},
		{"circled", "20 minutes", "㉑ minutes", RejectFacts},
		{"roman", "20 minutes", "Ⅹ minutes", RejectFacts},
		{"abbreviation", "20 minutes", "20 secs", RejectFacts},
		{"unsupported_count_noun", "20 minutes", "20 documents", RejectFacts},
		{"supported_count_noun", "20 documents", "20 documents", ""},
		{"supported_seconds", "20 seconds", "20 seconds", ""},
		{"unit_prefix", "20 days", "20 dayslong", RejectFacts},
		{"singular_prefix", "20 years", "20 year", RejectFacts},
		{"number_word_unit", "twenty minutes", "twenty seconds", RejectFacts},
		{"supported_number_word", "twenty minutes", "twenty minutes", ""},
		{"hyphenated_unit", "20 minutes", "20-month", RejectFacts},
		{"supported_hyphenated_unit", "20-month review", "20-month", ""},
		{"compound_unit", "20 business days", "20 business hours", RejectFacts},
		{"supported_compound_unit", "20 business days", "20 business days", ""},
		{"zh_supported_minutes", "20 分钟", "20 分钟", ""},
		{"zh_seconds", "20 分钟", "20 秒", RejectFacts},
		{"zh_months", "20 分钟", "20 个月", RejectFacts},
		{"zh_years", "20 分钟", "20 年", RejectFacts},
		{"zh_month_classifier", "20 个", "20 个月", RejectFacts},
		{"zh_short_unit_prefix", "20 个月", "20 个", RejectFacts},
		{"zh_short_number_prefix", "二十分钟", "十分钟", RejectFacts},
		{"zh_long_number_prefix", "三十分钟", "二十分钟", RejectFacts},
		{"zh_number_word_seconds", "二十分钟", "二十秒", RejectFacts},
		{"zh_number_word_years", "二十分钟", "二十年", RejectFacts},
		{"zh_supported_number_word", "二十分钟", "二十分钟", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			job, out := baseCase()
			job.Items[0].OffFor = &Display{tc.display}
			out.Headline = "Screen capture has been off for " + tc.quantity + "."
			checkOutput(t, job, out, nil, tc.want)
		})
	}
	job, out := baseCase()
	job.Items[0].Title = Text("21 seconds")
	out.Headline = "Screen capture has been off for 20 seconds."
	checkOutput(t, job, out, nil, RejectFacts)
}

func TestUnicodeNumbersCannotBypassSecretRunCheck(t *testing.T) {
	for _, numbers := range []string{"²¹", "②①", "㉑", "Ⅹ", "½", "1²3④"} {
		t.Run(numbers, func(t *testing.T) {
			job, out := baseCase()
			job.Items[0].Title = Text(numbers)
			out.Body = "Open source " + numbers + "."
			checkOutput(t, job, out, nil, RejectFacts)
			if validDigitRuns(out.Body, job, evidenceIndex(job)) {
				t.Errorf("secret run check accepted unsupported numeric characters %q", numbers)
			}
		})
	}
}

func TestQuantityPunctuationCannotBypassGrounding(t *testing.T) {
	for _, tc := range []struct {
		name, display, quantity string
		want                    Rejection
	}{
		{"supported_minutes", "20 minutes", "20 minutes", ""},
		{"supported_hyphen", "20-year review", "20-year", ""},
		{"ascii_hyphen", "20 minutes", "20-year", RejectFacts},
		{"hyphen", "20 minutes", "20\u2010year", RejectFacts},
		{"nonbreaking_hyphen", "20 minutes", "20\u2011year", RejectFacts},
		{"figure_dash", "20 minutes", "20\u2012year", RejectFacts},
		{"en_dash", "20 minutes", "20\u2013year", RejectFacts},
		{"em_dash", "20 minutes", "20\u2014year", RejectFacts},
		{"horizontal_bar", "20 minutes", "20\u2015year", RejectFacts},
		{"minus", "20 minutes", "20\u2212year", RejectFacts},
		{"fullwidth_hyphen", "20 minutes", "20\uff0dyear", RejectFacts},
		{"soft_hyphen", "20 minutes", "20\u00adyear", RejectFacts},
		{"parentheses", "20 minutes", "20 (years)", RejectFacts},
		{"fullwidth_parentheses", "20 minutes", "20\uff08years\uff09", RejectFacts},
		{"slash", "20 minutes", "20 / years", RejectFacts},
		{"fraction_slash", "20 minutes", "20\u2044years", RejectFacts},
		{"division_slash", "20 minutes", "20\u2215years", RejectFacts},
		{"fullwidth_slash", "20 minutes", "20\uff0fyears", RejectFacts},
		{"brackets", "20 minutes", "20 [years]", RejectFacts},
		{"colon", "20 minutes", "20: years", RejectFacts},
		{"comma", "20 minutes", "20, years", RejectFacts},
		{"period", "20 minutes", "20. years", RejectFacts},
		{"period_capitalized_unit", "20 minutes", "20. Years", RejectFacts},
		{"period_contract_unit", "20 minutes", "20. Day", RejectFacts},
		{"period_contract_count", "20 minutes", "20. Meetings", RejectFacts},
		{"period_contract_noun", "20 minutes", "20. Notes", RejectFacts},
		{"mixed_punctuation", "20 minutes", "20 (/ years)", RejectFacts},
		{"zero_width_space", "20 minutes", "20\u200byears", RejectFacts},
		{"nonbreaking_space", "20 minutes", "20\u00a0years", RejectFacts},
		{"narrow_nonbreaking_space", "20 minutes", "20\u202fyears", RejectFacts},
		{"ideographic_space", "20 minutes", "20\u3000years", RejectFacts},
		{"compound_unit_punctuation", "20 business days", "20 business / years", RejectFacts},
		{"grammar_after_slash", "20 minutes", "20 / of years", RejectFacts},
		{"number_word", "twenty minutes", "twenty (years)", RejectFacts},
		{"zh_punctuation", "20 分钟", "20 / 年", RejectFacts},
		{"zh_number_word", "二十分钟", "二十（年）", RejectFacts},
	} {
		t.Run(tc.name, func(t *testing.T) {
			job, out := baseCase()
			job.Items[0].OffFor = &Display{tc.display}
			out.Headline = "Screen capture has been off for " + tc.quantity + "."
			checkOutput(t, job, out, nil, tc.want)
		})
	}
	job, out := baseCase()
	out.Body = "Open source 20. Here is the source."
	checkOutput(t, job, out, nil, "")
}
