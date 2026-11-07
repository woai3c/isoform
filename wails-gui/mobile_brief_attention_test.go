package main

import (
	"strings"
	"testing"
	"time"

	"wails-gui/internal/calendar"
)

func attentionTestZone(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func TestMobileBriefDayWordsAcrossDayBoundariesAndZones(t *testing.T) {
	la := attentionTestZone(t)
	monday := time.Date(2026, 10, 19, 8, 0, 0, 0, la) // the 8:00 AM brief, a Monday
	for _, tc := range []struct {
		name     string
		at, ref  time.Time
		loc      *time.Location
		en, zh   string
		sinceEn  string
		carried  bool
		sinceZhs string
	}{
		{"same day", time.Date(2026, 10, 19, 0, 5, 0, 0, la), monday, la, "today", "今天", "", false, ""},
		{"late last night", time.Date(2026, 10, 18, 23, 30, 0, 0, la), monday, la, "yesterday", "昨天", "since yesterday", true, "从昨天起"},
		{"previous week", time.Date(2026, 10, 15, 14, 0, 0, 0, la), monday, la, "last Thursday", "上周四", "since last Thursday", true, "从上周四起"},
		{"a week or more", time.Date(2026, 10, 12, 9, 0, 0, 0, la), monday, la, "October 12", "10月12日", "since October 12", true, "从10月12日起"},
		{"another year", time.Date(2025, 12, 28, 9, 0, 0, 0, la), monday, la, "December 28, 2025", "2025年12月28日", "since December 28, 2025", true, "从2025年12月28日起"},
		{"same week", time.Date(2026, 10, 15, 9, 0, 0, 0, la), time.Date(2026, 10, 17, 9, 0, 0, 0, la), la, "Thursday", "周四", "since Thursday", true, "从周四起"},
		{"due this week", time.Date(2026, 10, 22, 17, 0, 0, 0, la), monday, la, "Thursday", "周四", "", false, ""},
		{"due Sunday", time.Date(2026, 10, 25, 17, 0, 0, 0, la), monday, la, "Sunday", "周日", "", false, ""},
		{"due next week", time.Date(2026, 10, 21, 17, 0, 0, 0, la), time.Date(2026, 10, 17, 9, 0, 0, 0, la), la, "Wednesday", "下周三", "", false, ""},
		// 06:30 UTC on the 19th is still the 18th in Los Angeles.
		{"zone decides the day", time.Date(2026, 10, 19, 6, 30, 0, 0, time.UTC), monday, la, "yesterday", "昨天", "since yesterday", true, "从昨天起"},
		{"same instant in UTC", time.Date(2026, 10, 19, 6, 30, 0, 0, time.UTC), monday, time.UTC, "today", "今天", "", false, ""},
		// US clocks fall back on Nov 1; civil days still count one per day.
		{"across DST", time.Date(2026, 10, 30, 23, 0, 0, 0, la), time.Date(2026, 11, 2, 8, 0, 0, 0, la), la, "last Friday", "上周五", "since last Friday", true, "从上周五起"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := mobileBriefDayWords(tc.at, tc.ref, tc.loc, "en"); got != tc.en {
				t.Errorf("en = %q, want %q", got, tc.en)
			}
			if got := mobileBriefDayWords(tc.at, tc.ref, tc.loc, "zh"); got != tc.zh {
				t.Errorf("zh = %q, want %q", got, tc.zh)
			}
			if got := mobileBriefCarriedOver(tc.at, tc.ref, tc.loc); got != tc.carried {
				t.Errorf("carried over = %v, want %v", got, tc.carried)
			}
			if tc.at.Before(tc.ref) {
				if got := mobileBriefSinceWords(tc.at, tc.ref, tc.loc, "en"); got != tc.sinceEn {
					t.Errorf("since en = %q, want %q", got, tc.sinceEn)
				}
				if got := mobileBriefSinceWords(tc.at, tc.ref, tc.loc, "zh"); got != tc.sinceZhs {
					t.Errorf("since zh = %q, want %q", got, tc.sinceZhs)
				}
			}
		})
	}
}

// attentionFixture is the 8am Monday brief reduced to the grounding each row has.
func attentionFixture(t *testing.T, language string) (mobileBriefInput, time.Time) {
	t.Helper()
	la := attentionTestZone(t)
	asOf := time.Date(2026, 10, 19, 8, 0, 0, 0, la)
	day := func(month time.Month, d, hour int) time.Time { return time.Date(2026, month, d, hour, 0, 0, 0, la) }
	in := mobileBriefInput{Slot: mobileBriefSlotMorning, AsOf: asOf, WindowStart: asOf.Add(-14 * time.Hour), Location: la, Timezone: "America/Los_Angeles", Language: language,
		OpenLoops: []mobileBriefCandidate{
			{SourceKind: "purpose", SourceID: "card", Title: "Pay the Citi and Amazon card", Kind: "commitment", Status: "active", OpenedAt: day(10, 12, 9), DueAt: day(10, 18, 17), At: day(10, 12, 9)},
			{SourceKind: "purpose", SourceID: "payroll", Title: "Run payroll", Kind: "commitment", Status: "active", OpenedAt: day(10, 16, 9)},
			{SourceKind: "purpose", SourceID: "halcyon", Title: "Halcyon Arc lamp follow-up", Detail: "Ask about the dimmer on the Arc lamp", Kind: "goal", Status: "active", OpenedAt: day(10, 15, 14), LastTouch: day(10, 15, 15), At: day(10, 15, 15)},
			{SourceKind: "purpose", SourceID: "discovery", Title: "Discovery", Detail: "Customer interview notes", Kind: "goal", Status: "active", OpenedAt: day(10, 14, 10)},
			{SourceKind: "purpose", SourceID: "deck", Title: "Board deck", Kind: "commitment", Status: "active", OpenedAt: day(10, 13, 10), DueAt: day(10, 19, 17)},
			{SourceKind: "purpose", SourceID: "visa", Title: "Visa paperwork", Kind: "commitment", Status: "active", OpenedAt: day(10, 13, 10), DueAt: day(10, 22, 17)},
			{SourceKind: mobileBriefWaitingReplySource, SourceID: "dana", Title: "Dana", Person: "Dana", Kind: "waiting_on_reply", Status: "waiting", OpenedAt: day(10, 13, 11), At: day(10, 13, 11), LastTouch: day(10, 13, 11)},
			{SourceKind: "history", SourceID: "memo", Title: "Release memo", Kind: "one_time_work", Status: "blocked", OpenedAt: day(10, 17, 9), At: day(10, 18, 9)},
			{SourceKind: mobileBriefRecapSource, SourceID: "thread-outcome:backpack:1", Title: "The Halcyon Tide backpack is still in the cart.", Kind: mobileBriefThreadOutcomeSource, Status: "needs_review", OpenedAt: day(10, 18, 10), At: day(10, 18, 11),
				Outcome: &ActivityThreadOutcome{Kind: "purchase", State: "cart_left", Summary: "The Halcyon Tide backpack is still in the cart.", Action: "order", NextStep: "Order the Halcyon Tide backpack.", Confidence: .9}},
			{SourceKind: "purpose", SourceID: "gone", Title: "Old plan", Kind: "goal", Status: "active", EvidenceUnavailable: true},
		},
	}
	return in, asOf
}

func attentionItem(t *testing.T, brief MobileBriefV1, sourceID string) MobileBriefItem {
	t.Helper()
	sources := map[string]string{}
	for _, ref := range brief.Evidence {
		sources[ref.Ref] = ref.SourceID
	}
	for _, section := range mobileBriefSectionLines(brief.Sections) {
		if section.name != mobileBriefSectionOpenLoops {
			continue
		}
		for _, item := range section.items {
			if len(item.Refs) > 0 && sources[item.Refs[0]] == sourceID || item.PurposeID == sourceID {
				return item
			}
		}
	}
	t.Fatalf("no open loop for %s", sourceID)
	return MobileBriefItem{}
}

func TestMobileBriefAttentionAdmitsOnlyGroundedItems(t *testing.T) {
	for _, tc := range []struct {
		language string
		want     map[string]struct {
			reason string
			rank   int
		}
	}{
		{"en", map[string]struct {
			reason string
			rank   int
		}{
			"card":                      {"Was due yesterday", mobileBriefAttentionOverdue},
			"deck":                      {"Due today", mobileBriefAttentionDueToday},
			"dana":                      {"Waiting on your reply since last Tuesday", mobileBriefAttentionPersonWaiting},
			"memo":                      {"Blocked, waiting for you", mobileBriefAttentionOwnWork},
			"visa":                      {"Due Thursday", mobileBriefAttentionDueSoon},
			"thread-outcome:backpack:1": {"Still in the cart since yesterday", mobileBriefAttentionUnfinished},
			"halcyon":                   {"No progress recorded since last Thursday", mobileBriefAttentionStalled},
			// No date, no person, no evidence: nothing to stand on.
			"payroll":   {"", 0},
			"discovery": {"", 0},
			"gone":      {"", 0},
		}},
		{"zh", map[string]struct {
			reason string
			rank   int
		}{
			"card":                      {"已于昨天到期", mobileBriefAttentionOverdue},
			"deck":                      {"今天到期", mobileBriefAttentionDueToday},
			"dana":                      {"从上周二起一直在等你回复", mobileBriefAttentionPersonWaiting},
			"visa":                      {"周四到期", mobileBriefAttentionDueSoon},
			"thread-outcome:backpack:1": {"从昨天起还在购物车里", mobileBriefAttentionUnfinished},
			"halcyon":                   {"上周四以来没有进展记录", mobileBriefAttentionStalled},
			"payroll":                   {"", 0},
		}},
	} {
		t.Run(tc.language, func(t *testing.T) {
			in, _ := attentionFixture(t, tc.language)
			in.PreviousBriefs = nil
			// The open-loop cap is four; give every row its own brief so each
			// one's admission is read from the builder.
			for id, want := range tc.want {
				single := in
				single.OpenLoops = nil
				for _, item := range in.OpenLoops {
					if item.SourceID == id {
						single.OpenLoops = append(single.OpenLoops, item)
					}
				}
				brief := buildMobileBriefV1(single)
				item := attentionItem(t, brief, id)
				if item.AttentionReason != want.reason || item.AttentionRank != want.rank {
					t.Errorf("%s: reason %q rank %d, want %q rank %d", id, item.AttentionReason, item.AttentionRank, want.reason, want.rank)
				}
			}
		})
	}
}

func TestMobileBriefSinceCarriesFirstSeenDate(t *testing.T) {
	in, _ := attentionFixture(t, "en")
	in.OpenLoops = in.OpenLoops[2:3] // the Halcyon Arc lamp follow-up
	brief := buildMobileBriefV1(in)
	item := attentionItem(t, brief, "halcyon")
	if item.Since != "2026-10-15T21:00:00Z" {
		t.Fatalf("since = %q", item.Since)
	}
	// A Today refresh rebuilds rows from the stored brief; the age survives.
	refreshed := projectMobileBriefToday(brief, mobileBriefInput{AsOf: in.AsOf.Add(time.Hour), Location: in.Location, Language: "en", Today: &mobileBriefTodayInput{Context: map[string]MobileBriefItemContext{}, Unavailable: map[string]bool{}}})
	if got := attentionItem(t, refreshed, "halcyon"); got.Since != item.Since || got.AttentionReason != item.AttentionReason {
		t.Fatalf("refresh lost the age: %+v", got)
	}
}

func TestMobileBriefTemplateNamesTheAgeOfCarriedOverWork(t *testing.T) {
	for _, language := range []string{"en", "zh-CN"} {
		t.Run(language, func(t *testing.T) {
			in, _ := attentionFixture(t, mobileBriefVoiceLang(language))
			in.OpenLoops = []mobileBriefCandidate{in.OpenLoops[2]}
			brief := buildMobileBriefV1(in)
			paragraphs := mobileBriefNarrativeTemplate(brief, language)
			text := ""
			for _, paragraph := range paragraphs {
				text += paragraph.Text + "\n"
			}
			want := "since last Thursday"
			if language == "zh-CN" {
				want = "从上周四起"
			}
			if !strings.Contains(text, "Halcyon Arc lamp follow-up") || !strings.Contains(text, want) {
				t.Fatalf("carried-over loop has no age:\n%s", text)
			}
			if err := validateMobileBriefNarrativeVoice(brief, paragraphs, language); err != nil {
				t.Fatalf("template fails its own guard: %v\n%s", err, text)
			}
			// A model reply that drops the age falls back to the template.
			ref := attentionItem(t, brief, "halcyon").Refs[0]
			closing, _ := mobileBriefClosing(brief, language)
			bare := []MobileBriefPara{{Text: "The Halcyon Arc lamp follow-up is next.", Refs: []string{ref}}, closing}
			if err := validateMobileBriefNarrativeVoice(brief, bare, language); err == nil {
				t.Fatal("narrative without the age was accepted")
			}
			named := []MobileBriefPara{{Text: "The Halcyon Arc lamp follow-up from last Thursday is next.", Refs: []string{ref}}, closing}
			if language == "zh-CN" {
				named[0].Text = "上周四的Halcyon Arc台灯跟进还没收尾。"
			}
			if err := validateMobileBriefNarrativeVoice(brief, named, language); err != nil {
				t.Fatalf("narrative with the age was rejected: %v", err)
			}
		})
	}
}

func TestMobileBriefOffersAreDistinctConcreteAndActionable(t *testing.T) {
	for _, language := range []string{"en", "zh-CN"} {
		t.Run(language, func(t *testing.T) {
			lang := mobileBriefVoiceLang(language)
			in, _ := attentionFixture(t, lang)
			// Two loops share the vague title the user saw twice as "Draft a plan".
			discovery := in.OpenLoops[3]
			twin := discovery
			twin.SourceID, twin.Detail = "discovery-2", "Second discovery"
			in.OpenLoops = []mobileBriefCandidate{discovery, twin, in.OpenLoops[2]}
			brief := buildMobileBriefV1(in)
			closing, offers := mobileBriefClosing(brief, language)
			if len(offers) != 2 {
				t.Fatalf("offers = %+v", offers)
			}
			if offers[0].Label == offers[1].Label || offers[0].Prompt == offers[1].Prompt {
				t.Fatalf("offers repeat: %+v", offers)
			}
			goal := pick(lang, "the goal is Customer interview notes", "目标是Customer interview notes")
			for _, offer := range offers {
				if offer.Action != mobileBriefOfferSend && offer.Action != mobileBriefOfferPrefill || offer.Kind == "" || offer.Label == "" || len(offer.Refs) == 0 {
					t.Fatalf("offer is not actionable: %+v", offer)
				}
				if strings.Contains(offer.Text, "I can draft the next steps") || strings.Contains(offer.Label, "Draft a plan") {
					t.Fatalf("old template: %+v", offer)
				}
			}
			// The stalled loop needs attention, so it is offered first; the
			// vague title is offered once, with the context that names it.
			if !strings.Contains(offers[0].Prompt, "Halcyon Arc lamp follow-up") || offers[0].Kind != "plan" {
				t.Fatalf("attention item not offered first: %+v", offers)
			}
			discoveryOffer := offers[1]
			if !strings.Contains(discoveryOffer.Prompt, "Discovery") || !strings.Contains(discoveryOffer.Prompt, goal) || !strings.Contains(discoveryOffer.Prompt, "Isoform") {
				t.Fatalf("prompt does not name the subject, its goal and its sources: %q", discoveryOffer.Prompt)
			}
			if lang == "en" && (!strings.Contains(discoveryOffer.Prompt, "open since last Wednesday") || discoveryOffer.Label != "Plan: Discovery") {
				t.Fatalf("prompt lacks what the brief knows: %+v", discoveryOffer)
			}
			// Said aloud, with no quotation marks or parentheses, and one
			// question about one thing (today-review v1.1.17 D18).
			if lang == "en" && closing.Text != "Want me to plan the next steps for the Halcyon Arc lamp follow-up?" {
				t.Fatalf("closing should ask about one thing: %q", closing.Text)
			}
			// The desired outcome is an item detail: main chat may get it, the
			// narrative model and the spoken closing never do.
			prompt := mobileBriefTestPrompt(brief, language)
			if strings.Contains(closing.Text, "ustomer interview notes") || strings.Contains(prompt, "ustomer interview notes") {
				t.Fatalf("desired outcome reached the closing or the model prompt: %q", closing.Text)
			}
			if !strings.HasSuffix(closing.Text, pick(lang, "?", "？")) || strings.Contains(closing.Text, "Discovery") || !strings.Contains(closing.Text, "Halcyon Arc lamp follow-up") {
				t.Fatalf("closing must ask about its first subject only: %q", closing.Text)
			}
			if strings.Count(closing.Text, pick(lang, "?", "？")) != 1 {
				t.Fatalf("closing asks more than one question: %q", closing.Text)
			}
		})
	}
}

func TestMobileBriefOfferKindsAndActions(t *testing.T) {
	in, asOf := attentionFixture(t, "en")
	byID := map[string]mobileBriefCandidate{}
	for _, item := range in.OpenLoops {
		byID[item.SourceID] = item
	}
	for _, tc := range []struct {
		id, kind, action, label, prompt string
	}{
		{"dana", "reply", mobileBriefOfferSend, "Reply to Dana", "Do not send anything."},
		{"thread-outcome:backpack:1", "order", mobileBriefOfferPrefill, "Finish the order", "Do not place the order until I confirm."},
		// The concrete deliverable, not "plan the next steps".
		{"card", "plan", mobileBriefOfferSend, "Pay the Citi and Amazon card", "Help me pay the Citi and Amazon card today."},
	} {
		t.Run(tc.id, func(t *testing.T) {
			single := in
			single.OpenLoops = []mobileBriefCandidate{byID[tc.id]}
			brief := buildMobileBriefV1(single)
			offers := mobileBriefConversationOffers(brief, "en")
			if len(offers) != 1 || offers[0].Kind != tc.kind || offers[0].Action != tc.action || offers[0].Label != tc.label || !strings.Contains(offers[0].Prompt, tc.prompt) {
				t.Fatalf("offer = %+v", offers)
			}
		})
	}
	// A bare title comes after work Isoform has something on.
	bare := in
	bare.OpenLoops = []mobileBriefCandidate{byID["payroll"], byID["discovery"], byID["halcyon"]}
	if offers := mobileBriefConversationOffers(buildMobileBriefV1(bare), "en"); len(offers) != 2 || strings.Contains(offers[0].Label+offers[1].Label, "Run payroll") {
		t.Fatalf("bare title offered ahead of grounded work: %+v", offers)
	}
	// A meeting later today is prepared with its clock, never its weekday.
	meeting := in
	meeting.OpenLoops = nil
	meeting.Meetings = []mobileBriefCandidate{{SourceKind: mobileBriefCalendarSource, SourceID: "one-on-one", Title: "Priya / Alex: 1:1", Kind: "meeting", Status: "upcoming", At: asOf.Add(2 * time.Hour), EndAt: asOf.Add(150 * time.Minute)}}
	brief := buildMobileBriefV1(meeting)
	offers := mobileBriefConversationOffers(brief, "en")
	if len(offers) != 1 || offers[0].Kind != "prepare" || offers[0].Text != "Want me to help you prepare for the 1:1 with Priya and Alex at 10:00 AM?" {
		t.Fatalf("prepare offer = %+v", offers)
	}
}

func TestMobileBriefDayShapeLineReadsLikeAPerson(t *testing.T) {
	la := attentionTestZone(t)
	at := func(hour, minute int) time.Time { return time.Date(2026, 10, 19, hour, minute, 0, 0, la) }
	priya := prepEvent("priya", "Priya / Alex: 1:1", at(10, 0), 30, "Halcyon Meet", 2)
	lesson := prepEvent("halcyon-lesson", "Halcyon Spanish lesson - Maya C.", at(10, 0), 60, "Halcyon Classroom", 1)
	dinner := prepEvent("dinner", "Dinner", at(18, 0), 90, "", 0)
	events := []calendar.Event{priya, lesson, dinner}

	morning := at(8, 0)
	shape := calendar.ShapeDay(events, morning, la, morning)
	en := mobileBriefDayShapeLine(shape, morning, la, "en")
	for _, want := range []string{"Your 10:00 AM Halcyon Spanish lesson, Maya C. overlaps the 1:1 with Priya and Alex.", "You're free 9:00 AM to 10:00 AM and 11:00 AM to 6:00 PM."} {
		if !strings.Contains(en, want) {
			t.Errorf("missing %q in %q", want, en)
		}
	}
	for _, bad := range []string{"Free.", "..", "C..", "Monday", "First meeting:", "Conflict:", "“", "(", " / "} {
		if strings.Contains(en, bad) {
			t.Errorf("shape line has %q: %q", bad, en)
		}
	}
	zh := mobileBriefDayShapeLine(shape, morning, la, "zh")
	if !strings.Contains(zh, "10:00的“Halcyon Spanish lesson - Maya C.”和“Priya / Alex: 1:1”时间冲突。") || strings.Contains(zh, "星期一") {
		t.Errorf("zh shape line = %q", zh)
	}

	// Later in the day only time still ahead is said: no past clock, no
	// empty fragment.
	afternoon := at(15, 0)
	shape = calendar.ShapeDay(events, afternoon, la, afternoon)
	later := mobileBriefDayShapeLine(shape, afternoon, la, "en")
	if later != "You're free now until 6:00 PM." {
		t.Errorf("afternoon shape line = %q", later)
	}
	evening := at(20, 0)
	shape = calendar.ShapeDay(events, evening, la, evening)
	if got := mobileBriefDayShapeLine(shape, evening, la, "en"); got != "" {
		t.Errorf("a finished day has no shape line: %q", got)
	}

	// The brief keeps today's clocks in the shape line.
	in := mobileBriefInput{Slot: mobileBriefSlotMorning, AsOf: morning, Location: la, Language: "en", Meetings: []mobileBriefCandidate{{
		SourceKind: mobileBriefDayShapeSource, SourceID: "2026-10-19", Title: "The shape of today", Kind: "day_shape", Status: "day_shape",
		Detail: mobileBriefDayShapeLine(calendar.ShapeDay(events, morning, la, morning), morning, la, "en"), At: at(0, 0),
	}}}
	brief := buildMobileBriefV1(in)
	if len(brief.Sections.Meetings) != 1 || !strings.Contains(brief.Sections.Meetings[0].Detail, "Your 10:00 AM") {
		t.Fatalf("shape line lost its clocks: %+v", brief.Sections.Meetings)
	}
}

func TestMobileBriefShapeLineNamesOnlyAClockAhead(t *testing.T) {
	la := attentionTestZone(t)
	at := func(hour, minute int) time.Time { return time.Date(2026, 10, 19, hour, minute, 0, 0, la) }
	// At 9:15 the 9am call is under way; the clash starts when B does.
	a := prepEvent("a", "Design review", at(9, 0), 60, "Halcyon Meet", 2)
	b := prepEvent("b", "Vendor call", at(9, 30), 30, "Halcyon Meet", 1)
	now := at(9, 15)
	line := mobileBriefDayShapeLine(calendar.ShapeDay([]calendar.Event{a, b}, now, la, now), now, la, "en")
	if !strings.HasPrefix(line, "Your 9:30 AM Vendor call overlaps the Design review.") || strings.Contains(line, "9:00 AM") {
		t.Fatalf("shape line names a past clock: %q", line)
	}
}

func TestMobileBriefSpeaksTitlesPlainly(t *testing.T) {
	for _, tc := range []struct{ title, kind, noun, task string }{
		{"Agree on the pricing plan", "commitment", "Agree on the pricing plan", "agree on the pricing plan"},
		{"Pay the Citi and Amazon card", "commitment", "Pay the Citi and Amazon card", "pay the Citi and Amazon card"},
		{"Draft the launch note", "goal", "the launch note", "draft the launch note"},
		{"Finalize the deck", "goal", "the deck", "finalize the deck"},
		{"Follow up with Dana", "commitment", "Follow up with Dana", "follow up with Dana"},
		{"Run payroll", "commitment", "Run payroll", "run payroll"},
		{"Send it to Dana", "commitment", "Send it to Dana", "send it to Dana"},
		{"Call Dana", "commitment", "Call Dana", "call Dana"},
		{"AFO calf raises, every day", "commitment", "AFO calf raises, every day", ""},
		{"Check in with Maya Chen", "one_time_work", "your task to check in with Maya Chen", "check in with Maya Chen"},
		// Names stay names whatever their kind (pr-desk round 1).
		{"Dinner with Maya Chen", "commitment", "Dinner with Maya Chen", ""},
		{"Notes on churn", "one_time_work", "the Notes on churn", ""},
		{"Trip to Japan", "commitment", "Trip to Japan", ""},
		{"Report for Dana", "commitment", "Report for Dana", ""},
		{"Call with investors", "commitment", "Call with investors", ""},
		{"Trip to Japan", "goal", "the Trip to Japan", ""},
		{"Halcyon Arc lamp follow-up", "goal", "the Halcyon Arc lamp follow-up", ""},
		{"Release memo", "commitment", "Release memo", ""},
		{"Discovery", "goal", "Discovery", ""},
		{"Priya / Alex: 1:1", "meeting", "the 1:1 with Priya and Alex", ""},
		{"Halcyon Spanish lesson - Maya C.", "meeting", "the Halcyon Spanish lesson, Maya C.", ""},
	} {
		if got := mobileBriefItemNoun(tc.title, tc.kind, "en"); got != tc.noun {
			t.Errorf("%q noun = %q, want %q", tc.title, got, tc.noun)
		}
		task, ok := mobileBriefTaskPhrase(tc.title, tc.kind, "en")
		if ok != (tc.task != "") || task != tc.task {
			t.Errorf("%q task = %q %v, want %q", tc.title, task, ok, tc.task)
		}
	}
	if got := mobileBriefItemNoun("Discovery", "goal", "zh"); got != "“Discovery”" {
		t.Errorf("zh keeps quotes: %q", got)
	}
	if got := mobileBriefItemNoun("每天做 AFO 提踵", "commitment", "zh"); got != "“每天做 AFO 提踵”" {
		t.Errorf("zh commitment name = %q", got)
	}
	line := mobileBriefTemplateItem(MobileBriefV1{}, MobileBriefItem{Kind: "commitment"}, "Due", "AFO calf raises, every day", "en", "en")
	if !strings.HasPrefix(line, "AFO calf raises, every day ") || strings.HasPrefix(line, "The ") {
		t.Errorf("commitment sentence changed its name: %q", line)
	}
	// A noun commitment closes on planning, never "help you dinner with Maya Chen".
	la := attentionTestZone(t)
	asOf := time.Date(2026, 10, 19, 8, 0, 0, 0, la)
	brief := buildMobileBriefV1(mobileBriefInput{Slot: mobileBriefSlotMorning, AsOf: asOf, Location: la, Language: "en",
		OpenLoops: []mobileBriefCandidate{{SourceKind: "purpose", SourceID: "dinner", Title: "Dinner with Maya Chen", Kind: "commitment", Status: "active",
			OpenedAt: asOf.AddDate(0, 0, -2), DueAt: asOf.Add(10 * time.Hour)}}})
	closing, _ := mobileBriefClosing(brief, "en")
	if closing.Text != "Want me to plan the next steps for Dinner with Maya Chen?" {
		t.Errorf("noun commitment closing = %q", closing.Text)
	}
	for _, lowered := range []struct{ in, want string }{
		{"Draft the interview guide", "draft the interview guide"},
		{"Dana's onboarding doc", "Dana's onboarding doc"},
		{"RTK license notes", "RTK license notes"},
		{"No progress recorded since Thursday", "no progress recorded since Thursday"},
	} {
		if got := mobileBriefLowerFirst(lowered.in); got != lowered.want {
			t.Errorf("lowerFirst(%q) = %q, want %q", lowered.in, got, lowered.want)
		}
	}
}

func TestMobileBriefBriefHasOneQuestionAndNoFillerOffers(t *testing.T) {
	in, asOf := attentionFixture(t, "en")
	// A finished item alone leaves nothing to offer: no "turn the next step
	// after ... into a goal".
	done := mobileBriefInput{Slot: mobileBriefSlotMorning, AsOf: asOf, Location: in.Location, Language: "en",
		Moved: []mobileBriefCandidate{{SourceKind: "history", SourceID: "deck", Title: "Sent the Q3 deck to Dana", Status: "done", At: asOf.Add(-12 * time.Hour)}}}
	brief := buildMobileBriefV1(done)
	if closing, offers := mobileBriefClosing(brief, "en"); len(offers) != 0 || closing.Text != "" {
		t.Fatalf("filler offer for a finished item: %q %+v", closing.Text, offers)
	}
	// With a closing question, the narrative may ask nothing else.
	in.OpenLoops = []mobileBriefCandidate{in.OpenLoops[8]}
	brief = buildMobileBriefV1(in)
	closing, _ := mobileBriefClosing(brief, "en")
	ref := brief.Sections.OpenLoops[0].Refs[0]
	two := []MobileBriefPara{{Text: "The Halcyon Tide backpack is still in the cart since yesterday. Did you order it elsewhere?", Refs: []string{ref}}, closing}
	if err := validateMobileBriefNarrativeVoice(brief, two, "en"); err == nil {
		t.Fatal("a second question was accepted")
	}
}

func TestMobileBriefTodayRefreshRecomputesTheShapeLine(t *testing.T) {
	app, st := newLinkTestApp(t)
	loc := time.UTC
	at := func(hour, minute int) time.Time { return time.Date(2026, 10, 19, hour, minute, 0, 0, loc) }
	standup := prepEvent("standup", "Standup", at(9, 30), 15, "Halcyon Meet", 2)
	review := prepEvent("review", "Design review", at(13, 0), 60, "Halcyon Classroom", 1)
	invite := prepEvent("invite", "Offsite planning", at(16, 0), 60, "", 3)
	invite.MyStatus = calendar.StatusPending
	for _, ev := range []calendar.Event{standup, review, invite} {
		storeEvent(t, st, ev)
	}
	shapeOf := func(brief MobileBriefV1) (string, bool) {
		for _, item := range brief.Sections.Meetings {
			if item.Kind == "day_shape" {
				return item.Detail, true
			}
		}
		return "", false
	}
	in := mobileBriefInput{Slot: mobileBriefSlotMorning, WindowStart: at(0, 0), AsOf: at(8, 0), Location: loc, Language: "en"}
	collectMobileBriefCalendar(app, &in)
	base := buildMobileBriefV1(in)
	morning, ok := shapeOf(base)
	if !ok || !strings.Contains(morning, "Standup at 9:30 AM") || !strings.Contains(morning, "The Offsite planning invite still needs your answer.") {
		t.Fatalf("8:00 AM shape line = %q", morning)
	}
	refresh := func(hour int) MobileBriefV1 {
		live := mobileBriefInput{AsOf: at(hour, 0), Location: loc, Language: "en",
			Today: &mobileBriefTodayInput{Context: map[string]MobileBriefItemContext{}, Unavailable: map[string]bool{}}}
		collectMobileBriefTodayShape(app, &live, base)
		return projectMobileBriefToday(base, live)
	}
	afternoon, ok := shapeOf(refresh(14))
	if !ok || strings.Contains(afternoon, "9:30 AM") || strings.Contains(afternoon, "9:00 AM") || !strings.Contains(afternoon, "now until 4:00 PM") {
		t.Fatalf("2:00 PM shape line still names past time: %q", afternoon)
	}
	if evening, ok := shapeOf(refresh(19)); ok {
		t.Fatalf("a day with nothing ahead keeps a shape line: %q", evening)
	}
}

func TestMobileBriefTodayRefreshDropsStoredAppMinutes(t *testing.T) {
	asOf := time.Date(2026, 10, 19, 8, 0, 0, 0, time.UTC)
	base := buildMobileBriefV1(mobileBriefInput{Slot: mobileBriefSlotMorning, AsOf: asOf, Location: time.UTC,
		OpenLoops: []mobileBriefCandidate{{SourceKind: "history", SourceID: "w", Title: "Release memo", Kind: "one_time_work", Status: "blocked", At: asOf.Add(-time.Hour)}}})
	// A brief stored before this change carried app minutes.
	base.DayShape = &MobileBriefDay{ActiveMinutes: 90, Hours: []MobileBriefHour{}, TopApps: []MobileBriefAppTime{{Name: "Figma", ActiveMinutes: 90}}}
	refreshed := projectMobileBriefToday(base, mobileBriefInput{AsOf: asOf.Add(time.Hour), Location: time.UTC, Language: "en",
		Today: &mobileBriefTodayInput{Context: map[string]MobileBriefItemContext{}, Unavailable: map[string]bool{}}})
	if refreshed.DayShape == nil || refreshed.DayShape.ActiveMinutes != 90 || len(refreshed.DayShape.TopApps) != 0 || len(base.DayShape.TopApps) != 1 {
		t.Fatalf("Today kept app minutes or changed the stored copy: %+v / %+v", refreshed.DayShape, base.DayShape)
	}
}

func TestMobileBriefGoalSentenceStaysOutOfTheModel(t *testing.T) {
	in, _ := attentionFixture(t, "en")
	in.OpenLoops = []mobileBriefCandidate{in.OpenLoops[3]} // Discovery, goal "Customer interview notes"
	brief := buildMobileBriefV1(in)
	paragraphs := mobileBriefNarrativeTemplate(brief, "en")
	brief.Narrative = nil
	spoken := mobileBriefSpokenText(brief, "en")
	text := ""
	for _, paragraph := range paragraphs {
		text += paragraph.Text + "\n"
	}
	// The template may name the goal; the model never sees it. A template is never spoken (design R74).
	if !strings.Contains(text, "The goal is Customer interview notes.") || spoken != "" {
		t.Fatalf("goal sentence missing or a template was spoken:\n%s\nspoken=%q", text, spoken)
	}
	if prompt := mobileBriefTestPrompt(brief, "English"); strings.Contains(prompt, "ustomer interview notes") {
		t.Fatal("the goal reached the narrative model")
	}
}

// pr-desk #65 round 2: a Today refresh rebuilds the shape line in the UI's
// language, as the brief build does.
func TestMobileBriefTodayRefreshShapeLineFollowsTheUILanguage(t *testing.T) {
	app, st := newLinkTestApp(t)
	if err := st.SetUIState(t.Context(), uiLanguageStateKey, "zh-CN"); err != nil {
		t.Fatal(err)
	}
	loc := time.UTC
	at := func(hour, minute int) time.Time { return time.Date(2026, 10, 19, hour, minute, 0, 0, loc) }
	storeEvent(t, st, prepEvent("review", "Design review", at(13, 0), 60, "Halcyon Classroom", 1))
	refresh := mobileBriefTodayRefreshInput(app, at(8, 0), loc)
	if refresh.Language != "zh" || refresh.ClockLayout != mobileBriefClockLayout("Simplified Chinese") {
		t.Fatalf("refresh input language %q clock %q", refresh.Language, refresh.ClockLayout)
	}
	in := mobileBriefInput{Slot: mobileBriefSlotMorning, WindowStart: at(0, 0), AsOf: at(8, 0), Location: loc, Language: "zh", ClockLayout: refresh.ClockLayout}
	collectMobileBriefCalendar(app, &in)
	base := buildMobileBriefV1(in)
	live := mobileBriefTodayRefreshInput(app, at(10, 0), loc)
	live.Today = &mobileBriefTodayInput{Context: map[string]MobileBriefItemContext{}, Unavailable: map[string]bool{}}
	collectMobileBriefTodayShape(app, &live, base)
	refreshed := projectMobileBriefToday(base, live)
	for _, item := range refreshed.Sections.Meetings {
		if item.Kind != "day_shape" {
			continue
		}
		if !strings.Contains(item.Detail, "今天第一个会议是13:00的“Design review”") || strings.Contains(item.Detail, "You") {
			t.Fatalf("refreshed shape line is not Chinese: %q", item.Detail)
		}
		return
	}
	t.Fatal("refresh lost the shape line")
}
