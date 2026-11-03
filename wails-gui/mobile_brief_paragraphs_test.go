package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/pii"
	"wails-gui/storage"
)

func TestMobileBriefParagraphTopicRefs(t *testing.T) {
	a, _, now := registryChatTestApp(t)
	goal := mobileTestPurpose(t, NewPurposeMapStore(a.activity.store.db), "release-goal", PurposeKindGoal, "Prepare release notes", false, nil)
	views, err := collectAndReadTopicViewsForTest(a)
	if err != nil || len(views) != 2 {
		t.Fatalf("registry: %+v %v", views, err)
	}
	brief := MobileBriefV1{Evidence: []MobileBriefRef{
		{Ref: "thread", SourceKind: "activity", SourceID: "atlas"},
		{Ref: "goal", SourceKind: "purpose", SourceID: goal.OpenLoop.ID},
		{Ref: "unmatched", SourceKind: "memory", SourceID: "atlas"},
	}, Narrative: []MobileBriefPara{
		{Text: "Atlas release is ready.", Refs: []string{"thread"}},
		{Text: "Prepare release notes.", Refs: []string{"goal"}},
		{Text: "Something else.", Refs: []string{"unmatched"}, TopicID: "invented", TopicName: "Invented"},
	}}
	got := a.mobileBriefWithParagraphTopics(t.Context(), brief)
	for i, kind := range []string{"thread", "goal"} {
		para := got.Narrative[i]
		if !validChatTopicID(para.TopicID) || para.TopicName == "" {
			t.Fatalf("%s has no registry topic: %+v", kind, para)
		}
		if _, _, err := a.chatTopicSnapshot(t.Context(), para.TopicID); err != nil {
			t.Fatalf("%s topic invalid: %v", kind, err)
		}
	}
	if got.Narrative[2].TopicID != "" || got.Narrative[2].TopicName != "" || brief.Narrative[0].TopicID != "" {
		t.Fatal("invented topic survived or caller mutated")
	}
	got.Version, got.BriefID = 1, now.Format("2006-01-02")+"-morning"
	store := newMobileBriefStore(t.TempDir())
	if err := store.save(got, now); err != nil {
		t.Fatal(err)
	}
	loaded, ok := store.load(got.BriefID)
	if !ok || !reflect.DeepEqual(loaded.Narrative, got.Narrative) {
		t.Fatal("paragraph topics lost on disk")
	}
}

func TestMobileBriefParagraphTopicsWithdrawPrivateSources(t *testing.T) {
	for _, change := range []string{"protected", "private", "sensitive", "closed_eyes", "forgotten"} {
		t.Run(change, func(t *testing.T) {
			a, _, _ := registryChatTestApp(t)
			if _, err := collectAndReadTopicViewsForTest(a); err != nil {
				t.Fatal(err)
			}
			brief := MobileBriefV1{Evidence: []MobileBriefRef{{Ref: "e1", SourceKind: "activity", SourceID: "atlas"}}, Narrative: []MobileBriefPara{{Text: "Atlas release is ready.", Refs: []string{"e1"}}}}
			brief = a.mobileBriefWithParagraphTopics(t.Context(), brief)
			if brief.Narrative[0].TopicID == "" {
				t.Fatal("readable control has no topic")
			}
			var err error
			switch change {
			case "protected":
				_, err = a.activity.store.db.Exec(`UPDATE activity_sessions SET triage_status='protected' WHERE id='session-atlas'`)
			case "private":
				_, err = a.activity.store.db.Exec(`UPDATE activities SET title='Atlas - Incognito' WHERE id='atlas'`)
			case "sensitive":
				pii.SetUserSensitiveApps(map[string]string{"Code": "Code"})
			case "closed_eyes":
				a.closedEyes.enabled.Store(true)
			case "forgotten":
				err = a.activity.store.DeleteSession("session-atlas")
			}
			if err != nil {
				t.Fatal(err)
			}
			got := a.mobileBriefWithParagraphTopics(t.Context(), brief)
			if got.Narrative[0].TopicID != "" || got.Narrative[0].TopicName != "" {
				t.Fatalf("withheld topic delivered: %+v", got.Narrative)
			}
		})
	}
}

func TestMobileBriefParagraphQuestionsModelAndPatternBudget(t *testing.T) {
	item := MobileBriefItem{ID: "deliverable", Kind: "deliverable", Title: "Shoe insert follow-up", Refs: []string{"e1"}}
	brief := MobileBriefV1{BriefID: "chip-model", AsOf: "2026-10-23T07:00:00Z", Timezone: "UTC",
		Evidence:  []MobileBriefRef{{Ref: "e1", SourceKind: "history", SourceID: "shoe"}},
		Sections:  MobileBriefSections{Deliverables: []MobileBriefItem{item}},
		Narrative: []MobileBriefPara{{Text: "The shoe insert follow-up is ready.", Refs: []string{"e1"}}}}
	pattern := mobileBriefParagraphCards(brief, "en", nil, 1)
	if len(pattern) != 1 || pattern[0].Label != "Open Shoe insert follow-up" || pattern[0].Message != "Open Shoe insert follow-up and walk me through it." {
		t.Fatalf("pattern chip: %+v", pattern)
	}
	word := mobileBriefCardWords{ID: pattern[0].ID, Label: "Open Shoe insert follow-up", Message: "Open the shoe insert follow-up and walk me through it."}
	got := mobileBriefApplyCardWords(brief, pattern, []mobileBriefCardWords{word}, "en")
	if len(got) != 1 || got[0].Writer != mobileBriefCardWriterModel || got[0].Message != word.Message || got[0].Label != word.Label {
		t.Fatalf("model chip: %+v", got)
	}
	fallback := mobileBriefApplyCardWords(brief, pattern, nil, "en")
	if len(fallback) != 1 || fallback[0].Writer != mobileBriefCardWriterPattern || fallback[0].Label != "Open Shoe insert follow-up" {
		t.Fatalf("pattern fallback: %+v", fallback)
	}
	wire, _ := json.Marshal(got[0])
	if !strings.Contains(string(wire), `"paragraph":0`) || len(got) >= todayChatPrepareLimit {
		t.Fatalf("chip wire or budget: %s", wire)
	}
}

func TestTodayBriefQuestionsAndClosingAnswersWarmWithoutWrites(t *testing.T) {
	rig, _, brief := cardRigWithParagraphChip(t)
	a := rig.app
	main, err := a.ensureMainChat(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	before, err := a.store.LoadStreamItems(t.Context(), main.LocalID)
	if err != nil {
		t.Fatal(err)
	}
	var refs []TodayChatContextRef
	questions, answers := 0, 0
	for _, card := range brief.Cards {
		paragraphChip := card.Kind == mobileBriefCardQuestion || card.Kind == mobileBriefCardItem && card.Paragraph != nil && card.Label != ""
		if !paragraphChip && card.Kind != mobileBriefCardAction {
			continue
		}
		if paragraphChip {
			questions++
		} else {
			answers++
		}
		if card.Mode != mobileBriefCardSend || card.Target != nil {
			t.Fatalf("prepared text is executable: %+v", card)
		}
		refs = append(refs, TodayChatContextRef{SourceKind: todayChatBriefCardKind, SourceID: card.ID, BriefID: brief.BriefID})
	}
	if questions == 0 || answers != 0 {
		t.Fatalf("questions=%d answers=%d", questions, answers)
	}
	for i, result := range a.PrepareTodayChatDrafts(refs) {
		card, _ := mobileBriefFindCard(brief, refs[i].SourceID)
		if result.Error != "" || result.Draft == nil || result.Draft.Message == "" {
			t.Fatalf("warming %s: %+v", card.ID, result)
		}
	}
	after, err := a.store.LoadStreamItems(t.Context(), main.LocalID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("preparation wrote history")
	}
	if err := a.handoffStore.Delete("memo"); err != nil {
		t.Fatal(err)
	}
	for _, ref := range refs {
		card, _ := mobileBriefFindCard(brief, ref.SourceID)
		if strings.Contains(card.Message, "Release memo") {
			if _, err := a.PrepareTodayChatDraft(ref); !errors.Is(err, errTodayChatItemUnavailable) {
				t.Fatalf("withdrawn card prepared: %v", err)
			}
		}
	}
}

func TestTodayBriefQuestionSendRetainsTopicAndAdmissionProof(t *testing.T) {
	a, tag, now := registryChatTestApp(t)
	goal := mobileTestPurpose(t, NewPurposeMapStore(a.activity.store.db), "atlas-goal", PurposeKindGoal, "Atlas checklist", false, nil)
	if _, err := collectAndReadTopicViewsForTest(a); err != nil {
		t.Fatal(err)
	}
	brief := buildMobileBriefV1(mobileBriefInput{Slot: "morning", AsOf: now, WindowStart: now.Add(-time.Hour), Location: time.Local, WorkingToward: []mobileBriefCandidate{{SourceKind: "purpose", SourceID: goal.OpenLoop.ID, Title: goal.Current.Title, Kind: "goal", Status: "active", At: now}}})
	brief.Narrative, brief.NarrativeStatus = mobileBriefNarrativeTemplate(brief, "en"), "unavailable"
	// The closing asks about the only item, which then gets no chip
	// (today-review v1.1.4 #4); keep the paragraph whose chip this covers.
	brief.Narrative = brief.Narrative[:1]
	brief = a.mobileBriefWithCards(a.mobileBriefForDelivery(brief))
	cards := cardsByKind(brief.Cards, mobileBriefCardQuestion)
	if len(cards) == 0 || brief.Narrative[0].TopicID == "" {
		t.Fatalf("missing topic question: %+v", brief)
	}
	dir, _ := mobileBriefStateDir()
	if err := newMobileBriefStore(dir).save(brief, now); err != nil {
		t.Fatal(err)
	}
	card := cards[0]
	ref := TodayChatContextRef{SourceKind: todayChatBriefCardKind, SourceID: card.ID, BriefID: brief.BriefID, GeneratedMessage: card.Message}
	draft, err := a.PrepareTodayChatDraft(ref)
	if err != nil || draft.TopicID != brief.Narrative[0].TopicID || draft.Message != card.Message {
		t.Fatalf("draft %+v %v", draft, err)
	}
	runner := &topicCaptureRunner{a: a, tag: tag, prompt: make(chan string, 1)}
	a.runner = runner
	_, err = a.SendPromptWithTodayContext(card.Message, "", "m", "", tag, nil, nil, false, false, "en", "",
		StreamItemData{ID: "question-send", Kind: "user"}, ref)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-runner.prompt:
	case <-time.After(5 * time.Second):
		t.Fatal("question did not reach the runner")
	}
	waitForTagIdle(t, a, tag)
	row, err := a.store.LoadStreamItem(t.Context(), "question-send")
	if err != nil || row == nil || row.Content != card.Message || !strings.Contains(row.AutomationContext, `"topicContext"`) {
		t.Fatalf("topic send: %+v %v", row, err)
	}
	tags, err := a.store.LoadTopicStreamItems(t.Context(), tag, draft.TopicID, now.Add(time.Hour).UnixMilli(), "~", 10)
	if err != nil || len(tags) != 2 {
		t.Fatalf("user and reply topic tags: %+v %v", tags, err)
	}
	a.closedEyes.enabled.Store(true)
	if _, _, err := a.admitTodayChatTurn(t.Context(), tag, storage.StreamItemRow{ID: "withdrawn-question", Kind: "user", Content: card.Message}, ref, nil); err == nil {
		t.Fatal("paused question admitted")
	}
	if row, _ := a.store.LoadStreamItem(t.Context(), "withdrawn-question"); row != nil {
		t.Fatal("rejected question left history")
	}
	if _, err := a.PrepareTodayChatDraft(ref); err == nil {
		t.Fatal("Closed Eyes question prepared")
	}
}

func TestMobileBriefParagraphLegacyFilesAndOptionalFields(t *testing.T) {
	old := []byte(`{"version":1,"briefId":"2026-10-22-morning","narrative":[{"text":"Legacy brief.","refs":["e1"]}],"cards":[{"id":"offer-1","kind":"action","mode":"send","message":"Prepare the memo.","refs":["e1"]}]}`)
	var brief MobileBriefV1
	if err := unmarshalStoredMobileBrief(old, &brief); err != nil {
		t.Fatal(err)
	}
	if brief.Version != 1 || brief.Narrative[0].Text != "Legacy brief." || brief.Narrative[0].TopicID != "" || brief.Cards[0].Paragraph != nil {
		t.Fatal("old brief changed")
	}
	raw, err := marshalStoredMobileBrief(brief)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"topicId"`, `"topicName"`, `"paragraph"`} {
		if strings.Contains(string(raw), field) {
			t.Fatalf("empty additive field: %s", field)
		}
	}
	if mobileBriefModelDigest(todayChatItem{}) != mobileBriefModelDigest(todayChatItem{topicID: "navigation-only"}) {
		t.Fatal("navigation changed existing topic source fingerprints")
	}
	before := mobileBriefSynthesisDigest(brief)
	brief.Narrative[0].TopicID, brief.Narrative[0].TopicName = "registry-id", "Atlas"
	if before != mobileBriefSynthesisDigest(brief) {
		t.Fatal("navigation invalidated frozen prose")
	}
}

func TestMobileBriefParagraphThreadOutcomeRef(t *testing.T) {
	a, _, now := registryChatTestApp(t)
	fixture := threadOutcomeFixtures(t)[0]
	row := seedThreadFixture(t, a, "meridian", fixture, now)
	if _, err := a.activity.store.db.Exec(`UPDATE activities SET entities='[{"type":"project","name":"Meridian"}]' WHERE id=?`, row.ID); err != nil {
		t.Fatal(err)
	}
	current, err := a.activity.store.GetActivityByID(row.ID)
	if err != nil || current == nil {
		t.Fatal(err)
	}
	row = saveFixtureOutcome(t, a, *current, fixture)
	outcome := a.readableThreadOutcome(context.Background(), row)
	if outcome == nil {
		t.Fatal("missing readable thread outcome")
	}
	if _, err := collectAndReadTopicViewsForTest(a); err != nil {
		t.Fatal(err)
	}
	brief := MobileBriefV1{Evidence: []MobileBriefRef{{Ref: "e1", SourceKind: mobileBriefRecapSource, SourceID: threadOutcomeSourceID(row.ID, outcome)}},
		Narrative: []MobileBriefPara{{Text: outcome.Summary, Refs: []string{"e1"}}}}
	got := a.mobileBriefWithParagraphTopics(t.Context(), brief)
	if !validChatTopicID(got.Narrative[0].TopicID) || got.Narrative[0].TopicName != "Meridian" {
		t.Fatalf("thread ref topic: %+v", got.Narrative)
	}
}

func TestMobileBriefParagraphChipsWithoutTopicOrSectionSubject(t *testing.T) {
	for _, lang := range []string{"en", "zh"} {
		brief := MobileBriefV1{Evidence: []MobileBriefRef{{Ref: "e1", SourceKind: "activity", SourceID: "reply"}},
			Narrative: []MobileBriefPara{{Text: "Kehan is waiting for the BP and financing notes.", Refs: []string{"e1"}}}}
		cards := mobileBriefPatternCards(brief, lang)
		questions := cardsByKind(cards, mobileBriefCardQuestion)
		if len(questions) != 0 || brief.Narrative[0].TopicID != "" {
			t.Fatalf("paragraph without a typed section subject received a chip: %+v", questions)
		}
	}
}

func TestMobileBriefParagraphQuestionLabelsStayCompact(t *testing.T) {
	for _, lang := range []string{"en", "zh"} {
		title := "Shoe insert follow-up"
		item := MobileBriefItem{ID: "deliverable", Kind: "deliverable", Title: title, Refs: []string{"e1"}}
		brief := MobileBriefV1{Evidence: []MobileBriefRef{{Ref: "e1", SourceKind: "history", SourceID: "shoe"}},
			Sections:  MobileBriefSections{Deliverables: []MobileBriefItem{item}},
			Narrative: []MobileBriefPara{{Text: "The follow-up is ready.", Refs: []string{"e1"}}}}
		cards := mobileBriefParagraphCards(brief, lang, nil, 1)
		if len(cards) != 1 || cards[0].Paragraph == nil || *cards[0].Paragraph != 0 {
			t.Fatalf("missing single chip: %+v", cards)
		}
		for _, card := range cards {
			if card.Label == "" || len([]rune(card.Label)) > 40 || !strings.Contains(card.Label, title) {
				t.Fatalf("unbounded or untitled %s label: %q", lang, card.Label)
			}
		}
		if len(mobileBriefParagraphCards(brief, lang, nil, 0)) != 0 {
			t.Fatal("chip ignored remaining budget")
		}
	}
}

func TestMobileBriefParagraphChipCopyBySection(t *testing.T) {
	tests := []struct {
		section, kind, title, detail string
		at                           string
		enLabel, enMessage           string
		zhLabel, zhMessage           string
	}{
		{"waiting", "waiting_on_reply", "Priya", "", "", "Draft the reply to Priya", "Draft my reply to Priya. Don't send it.", "起草给Priya的回复", "起草我给Priya的回复，先不要发送。"},
		{"meeting", "meeting", "Planning", "11:00 AM; Room 4; Dan Smith, +2, 3 others, Priya Singh", "2026-10-23T18:00:00Z", "Prep for 11:00 AM with Dan Smith", "Prep me for my 11:00 AM meeting with Dan Smith: what it's about and what I should bring.", "准备11:00和Dan Smith的会", "帮我准备11:00和Dan Smith的会：会议内容和我该准备什么。"},
		{"meeting-no-time", "meeting", "Planning", "Room 4; Dan Smith, Priya Singh", "", "", "", "", ""},
		{"meeting-title-fallback", "meeting", "Planning", "11:00 AM; Room 4; dana@example.com, +2, 3 others", "2026-10-23T18:00:00Z", "Prep for Planning at 11:00 AM", "Prep me for Planning at 11:00 AM: what it's about and what I should bring.", "准备11:00的Planning", "帮我准备11:00的Planning：会议内容和我该准备什么。"},
		{"commitment", "commitment", "Q4 plan", "", "", "Find time for Q4 plan", "Find time on my calendar for Q4 plan and suggest a slot. Don't book anything.", "为“Q4 plan”留出时间", "在我的日历里为“Q4 plan”找一个时段并给出建议，先不要预订。"},
		{"goal", "goal", "pricing page", "", "", "What's left on pricing page?", "What's left on pricing page, and what's the next step?", "“pricing page”还差什么？", "“pricing page”还差什么？下一步是什么？"},
		{"deliverable", "deliverable", "weekly update", "", "", "Open weekly update", "Open weekly update and walk me through it.", "打开weekly update", "打开weekly update，带我看一遍。"},
		{"moved", "activity", "Atlas", "", "", "", "", "", ""},
		{"people", "person", "Dana", "", "", "", "", "", ""},
		{"learned", "memory", "Preference", "", "", "", "", "", ""},
		{"openLoops", "goal", "Release", "", "", "", "", "", ""},
	}
	for _, language := range []string{"en", "zh"} {
		for index, tc := range tests {
			t.Run(fmt.Sprintf("%s/%s", language, tc.section), func(t *testing.T) {
				ref := fmt.Sprintf("e%d", index)
				item := MobileBriefItem{ID: tc.section, Kind: tc.kind, Title: tc.title, Detail: tc.detail, At: tc.at, Refs: []string{ref}}
				brief := MobileBriefV1{AsOf: "2026-10-23T15:00:00Z", Timezone: "America/Los_Angeles",
					Evidence:  []MobileBriefRef{{Ref: ref, SourceKind: "history", SourceID: tc.section}},
					Narrative: []MobileBriefPara{{Text: "A subject-specific paragraph.", Refs: []string{ref}}}}
				switch tc.section {
				case "waiting":
					brief.Sections.WaitingOnYou = []MobileBriefItem{item}
				case "meeting", "meeting-no-time", "meeting-title-fallback":
					item.Status = "upcoming"
					brief.Evidence[0].SourceKind = mobileBriefCalendarSource
					brief.Sections.Meetings = []MobileBriefItem{item}
				case "commitment":
					brief.Sections.Commitments = []MobileBriefItem{item}
				case "goal":
					brief.Sections.WorkingToward = []MobileBriefItem{item}
				case "deliverable":
					brief.Sections.Deliverables = []MobileBriefItem{item}
				case "moved":
					brief.Sections.Moved = []MobileBriefItem{item}
				case "people":
					brief.Sections.People = []MobileBriefItem{item}
				case "learned":
					brief.Sections.Learned = []MobileBriefItem{item}
				case "openLoops":
					brief.Sections.OpenLoops = []MobileBriefItem{item}
				}
				cards := mobileBriefParagraphCards(brief, language, nil, 1)
				label, message := tc.enLabel, tc.enMessage
				if language == "zh" {
					label, message = tc.zhLabel, tc.zhMessage
					if tc.section == "meeting" {
						clock := briefShortClock(11, 0, language)
						label = "准备" + clock + "和Dan Smith的会"
						message = "帮我准备" + clock + "和Dan Smith的会：会议内容和我该准备什么。"
					} else if tc.section == "meeting-title-fallback" {
						clock := briefShortClock(11, 0, language)
						label = "准备" + clock + "的Planning"
						message = "帮我准备" + clock + "的Planning：会议内容和我该准备什么。"
					}
				}
				if label == "" {
					if len(cards) != 0 {
						t.Fatalf("unexpected chip: %+v", cards)
					}
					return
				}
				if len(cards) != 1 || cards[0].Label != label || cards[0].Message != message {
					t.Fatalf("cards = %+v, want %q / %q", cards, label, message)
				}
			})
		}
	}
}

func TestMobileBriefMeetingPersonUsesFirstPlausibleAttendee(t *testing.T) {
	for _, tc := range []struct {
		detail string
		want   string
	}{
		{"9:30 AM to 9:45 AM; Zoom; Ann, Bob", "Ann"},
		{"9:30 AM to 9:45 AM; Zoom", ""},
		{"12:30 PM to 1:30 PM; Cafe Nero; Ann; invite not answered", "Ann"},
		{"11:00 AM; Room 4; +2, 3 others, Dan Smith, Priya Singh", "Dan Smith"},
		{"11:00 AM; Room 4; dana@example.com and Priya Singh", "Priya Singh"},
		{"11:00 AM; Room 4; +2、张三和李四", "张三"},
		{"11:00 AM; Room 4; 3 others", ""},
	} {
		if got := mobileBriefMeetingPerson(tc.detail); got != tc.want {
			t.Errorf("%q: attendee=%q, want %q", tc.detail, got, tc.want)
		}
	}
}

func TestMobileBriefQuestionChipLabelCap(t *testing.T) {
	for _, tc := range []struct{ lang, message string }{
		{"en", "Draft the prep for Jenny before the long meeting about the release tomorrow"},
		{"zh", "如何" + strings.Repeat("继续准备明天会议", 10)},
	} {
		// Too long to read at a glance: no chip, never an ellipsis (today-review v1.1.4 #5).
		if label := mobileBriefQuestionChipLabel(tc.message, tc.lang); label != "" {
			t.Fatalf("oversized label offered: %q", label)
		}
	}
	for _, tc := range []struct{ label, lang string }{
		{"Draft the reply to Dana Whitfield", "en"},
		{"准备明天和产品团队的季度规划评审会议", "zh"},
		{"Draft the reply…", "en"},
		{"The report?", "en"},
		{"What should I consider next?", "en"},
		{"Ask about this topic", "en"},
		{"如何继续推进？", "zh"},
		{"了解", "zh"},
	} {
		if got := mobileBriefQuestionChipLabel(tc.label, tc.lang); got != "" {
			t.Fatalf("generic or non-action label %q survived: %q", tc.label, got)
		}
	}
}
