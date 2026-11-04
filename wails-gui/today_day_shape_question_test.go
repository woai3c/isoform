package main

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestMobileBriefRejectHoldOffer(t *testing.T) {
	for _, tc := range []struct {
		name      string
		para      MobileBriefPara
		wantError bool
	}{
		{"tagged", MobileBriefPara{Text: "Shall we work on the memo?", Hold: &TodayDayShapeHold{}}, true},
		{"english", MobileBriefPara{Text: "Shall I hold time for the memo?"}, true},
		{"chinese", MobileBriefPara{Text: "为备忘录留出时间？"}, true},
		{"ordinary", MobileBriefPara{Text: "Shall I draft the memo?"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := mobileBriefRejectHoldOffer([]MobileBriefPara{tc.para})
			if (err != nil) != tc.wantError {
				t.Fatalf("error = %v, want error %v", err, tc.wantError)
			}
		})
	}
}

func TestMobileBriefWithoutHoldOfferPreservesOriginal(t *testing.T) {
	brief := MobileBriefV1{
		Narrative: []MobileBriefPara{{Text: "Your memo is due tomorrow."}, {Text: "Hold time for the memo?", Hold: &TodayDayShapeHold{}}},
		Cards:     []MobileBriefCard{{ID: "answer", Kind: mobileBriefCardAction}, {ID: "opener", Kind: mobileBriefCardOpener}},
		Audio:     &MobileBriefAudio{},
	}
	got := mobileBriefWithoutHoldOffer(brief)
	if len(got.Narrative) != 1 || len(got.Cards) != 1 || got.Cards[0].ID != "opener" || got.Audio != nil {
		t.Fatalf("hold offer survived: %+v", got)
	}
	if len(brief.Narrative) != 2 || len(brief.Cards) != 2 || brief.Audio == nil {
		t.Fatal("read projection changed the stored brief")
	}
}

func TestTodayDayShapeGeneratorQuestionHold(t *testing.T) {
	brief, _ := writtenBriefFixture()
	day := dayShapeTestDay(t)
	brief.LocalDate, brief.Timezone, brief.AsOf = "2026-10-23", day.Location().String(), day.Add(8*time.Hour).Format(time.RFC3339)
	hold := &TodayDayShapeHold{Start: day.Add(11 * time.Hour).Format(time.RFC3339), End: day.Add(12 * time.Hour).Format(time.RFC3339), Label: "Sam memo"}
	question := &mobileBriefNarrativeReplyClosing{Text: "Want me to hold time for the note to Sam?", Card: "offer-2", Hold: hold}
	got, err := mobileBriefWrittenClosing(brief, question, "English")
	if err != nil || !reflect.DeepEqual(got.Hold, hold) {
		t.Fatalf("generated hold=%+v %v", got, err)
	}
	question.Text = "Want me to hold 11:00 AM to 12:00 PM for the note to Sam?"
	if _, err := mobileBriefWrittenClosing(brief, question, "English"); err != nil {
		t.Fatalf("proposed clocks were refused: %v", err)
	}
	question.Text = "Want me to hold 10:00 AM to 12:00 PM for the note to Sam?"
	if _, err := mobileBriefWrittenClosing(brief, question, "English"); err == nil {
		t.Fatal("question clock disagreed with hold")
	}
	question.Text = "Want me to draft the note to Sam?"
	if _, err := mobileBriefWrittenClosing(brief, question, "English"); err == nil {
		t.Fatal("non-hold question acquired a hold")
	}
	question.Hold = nil
	got, err = mobileBriefWrittenClosing(brief, question, "English")
	if err != nil || got.Hold != nil {
		t.Fatalf("non-hold question=%+v %v", got, err)
	}
	question.Text = "Want me to hold time for the note to Sam?"
	invalid := *hold
	invalid.End = day.AddDate(0, 0, 1).Add(time.Hour).Format(time.RFC3339)
	question.Hold = &invalid
	if _, err := mobileBriefWrittenClosing(brief, question, "English"); err == nil {
		t.Fatal("generator accepted hold outside the day")
	}
	prompt := mobileBriefTestPrompt(brief, "English")
	for _, rule := range []string{"closing.hold to null", "Do not offer to hold", "9:00 AM"} {
		if !strings.Contains(prompt, rule) {
			t.Errorf("prompt missing %q", rule)
		}
	}
}

func TestTodayDayShapeHoldChangesAnswerIdentity(t *testing.T) {
	brief := MobileBriefV1{LocalDate: "2026-10-23", Evidence: []MobileBriefRef{{Ref: "e1", SourceKind: "calendar", SourceID: "review"}}}
	brief.Narrative = []MobileBriefPara{{Text: "Want me to hold time for Review?", Refs: []string{"e1"}, Hold: &TodayDayShapeHold{Start: "2026-10-23T11:00:00-07:00", End: "2026-10-23T12:00:00-07:00", Label: "Review"}}}
	before := mobileBriefClosingDigest(brief)
	brief.Narrative[0].Hold = &TodayDayShapeHold{Start: "2026-10-23T12:00:00-07:00", End: "2026-10-23T13:00:00-07:00", Label: "Review"}
	if before == "" || before == mobileBriefClosingDigest(brief) {
		t.Fatal("changed preview reused the previous answer identity")
	}
}

// An evening brief about tonight draws the rest of today (today-review
// v1.1.20 R44), so its hold is tonight and the strip stays on its own day.
func TestTodayDayShapeEveningShowsItsOwnDay(t *testing.T) {
	app := newTodayContextTestApp(t, true)
	day := dayShapeTestDay(t)
	brief, _ := writtenBriefFixture()
	brief.Slot, brief.LocalDate, brief.Timezone, brief.AsOf = mobileBriefSlotEvening, "2026-10-23", day.Location().String(), day.Add(18*time.Hour).Format(time.RFC3339)
	svc, _ := newTestMobileBriefService(t, &fakeMobileBriefClock{now: day.Add(18 * time.Hour)})
	svc.app, app.mobileBriefSvc = app, svc
	tonight := &TodayDayShapeHold{Start: day.Add(19 * time.Hour).Format(time.RFC3339), End: day.Add(20 * time.Hour).Format(time.RFC3339), Label: "Sam memo"}
	question := &mobileBriefNarrativeReplyClosing{Text: "Want me to hold time for the note to Sam?", Card: "offer-2", Hold: tonight}
	closing, err := mobileBriefWrittenClosing(brief, question, "English")
	if err != nil || !reflect.DeepEqual(closing.Hold, tonight) {
		t.Fatalf("tonight hold=%+v %v", closing, err)
	}
	tomorrow := day.AddDate(0, 0, 1)
	question.Hold = &TodayDayShapeHold{Start: tomorrow.Add(11 * time.Hour).Format(time.RFC3339), End: tomorrow.Add(12 * time.Hour).Format(time.RFC3339), Label: "Sam memo"}
	if closing, err := mobileBriefWrittenClosing(brief, question, "English"); err == nil && closing.Hold != nil {
		t.Fatalf("an evening brief about tonight held tomorrow: %+v", closing.Hold)
	}
	if prompt := mobileBriefTestPrompt(brief, "English"); !strings.Contains(prompt, "Do not offer to hold") {
		t.Fatal("evening prompt must not offer a hold")
	}
	for _, slot := range []string{mobileBriefSlotEvening, mobileBriefSlotMorning} {
		brief.Slot = slot
		phone := app.mobileBriefWithDayShape(brief)
		if phone.DayShape == nil || phone.DayShape.TodayDayShape == nil || !strings.HasPrefix(phone.DayShape.RangeStart, "2026-10-23T08:00:00") {
			t.Fatalf("%s phone day=%+v", slot, phone.DayShape)
		}
	}
}
