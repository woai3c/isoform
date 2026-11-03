package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/calendar"
)

func dayShapeHoldFixture(t *testing.T) (*App, *fakeMobileBriefClock, MobileBriefV1, time.Time) {
	t.Helper()
	app := newTodayContextTestApp(t, true)
	day := dayShapeTestDay(t)
	clock := &fakeMobileBriefClock{now: day.Add(8 * time.Hour)}
	svc, _ := newTestMobileBriefService(t, clock)
	svc.app, app.mobileBriefSvc = app, svc
	storeEvent(t, app.store, calendar.Event{ID: "review", Source: calendar.SourceEventKit, Title: "Review", Start: day.Add(14 * time.Hour), End: day.Add(15 * time.Hour)})
	in := mobileBriefInput{Slot: mobileBriefSlotMorning, AsOf: clock.Now(), Location: day.Location()}
	collectMobileBriefCalendar(app, &in)
	brief := buildMobileBriefV1(in)
	brief.Narrative = []MobileBriefPara{{Text: "Want me to hold time for Review?", Refs: []string{brief.Evidence[0].Ref}, Hold: &TodayDayShapeHold{Start: day.Add(14 * time.Hour).Format(time.RFC3339), End: day.Add(15 * time.Hour).Format(time.RFC3339), Label: "Review prep"}}}
	return app, clock, brief, day
}

func TestTodayDayShapeRound1OverlapAtDelivery(t *testing.T) {
	app, clock, brief, day := dayShapeHoldFixture(t)
	before, _ := json.Marshal(brief)
	shape, err := app.todayDayShapeAt(day, clock.Now(), brief)
	if err != nil || len(shape.Events) != 1 || shape.Hold != nil {
		t.Fatalf("calendar conflict reached desktop: %+v err=%v", shape, err)
	}
	phone := app.mobileBriefWithDayShape(brief)
	if phone.DayShape.Hold != nil {
		t.Fatal("calendar conflict reached phone")
	}
	after, _ := json.Marshal(brief)
	if string(before) != string(after) {
		t.Fatal("delivery changed stored prose or endpoints")
	}
}

func TestTodayDayShapeRound1ExpiredAtDelivery(t *testing.T) {
	app, clock, brief, day := dayShapeHoldFixture(t)
	hold := &TodayDayShapeHold{Start: day.Add(8*time.Hour + time.Minute).Format(time.RFC3339), End: day.Add(8*time.Hour + 2*time.Minute).Format(time.RFC3339), Label: "Review prep"}
	brief.Narrative[0].Hold = hold
	clock.Advance(3 * time.Minute)
	before, _ := json.Marshal(brief)
	shape, err := app.todayDayShapeAt(day, clock.Now(), brief)
	if err != nil || shape.Hold != nil {
		t.Fatalf("expired hold reached desktop: %+v err=%v", shape.Hold, err)
	}
	if app.mobileBriefWithDayShape(brief).DayShape.Hold != nil {
		t.Fatal("expired hold reached phone")
	}
	after, _ := json.Marshal(brief)
	if string(before) != string(after) || !reflect.DeepEqual(brief.Narrative[0].Hold, hold) {
		t.Fatal("withholding rewrote stored question")
	}
}

func TestTodayDayShapeRound1OrderedClosingInterval(t *testing.T) {
	brief, _ := writtenBriefFixture()
	day := dayShapeTestDay(t)
	brief.LocalDate, brief.Timezone, brief.AsOf = "2026-10-23", day.Location().String(), day.Add(8*time.Hour).Format(time.RFC3339)
	closing := &mobileBriefNarrativeReplyClosing{Text: "Want me to hold 12:00 PM to 11:00 AM for the note to Sam?", Card: "offer-2", Hold: &TodayDayShapeHold{Start: day.Add(11 * time.Hour).Format(time.RFC3339), End: day.Add(12 * time.Hour).Format(time.RFC3339), Label: "Sam memo"}}
	if _, err := mobileBriefWrittenClosing(brief, closing, "English"); err == nil {
		t.Fatal("reversed prose interval accepted for forward structured hold")
	}
}

func TestTodayDayShapeRound1UnchangedSourceEmitsNoUpdate(t *testing.T) {
	app := &App{eventBroadcaster: NewEventBroadcaster()}
	client := app.eventBroadcaster.AddClient("round1")
	defer app.eventBroadcaster.RemoveClient("round1")
	app.emitBrowserEvent("calendar:synced")
	app.emitBrowserEvent("brief:stored")
	timer := time.NewTimer(200 * time.Millisecond)
	defer timer.Stop()
	for {
		select {
		case raw := <-client:
			var event struct {
				Name string `json:"event"`
			}
			if err := json.Unmarshal(raw, &event); err != nil {
				t.Fatal(err)
			}
			if event.Name == "today-dayshape:updated" {
				t.Fatal("unchanged unavailable calendar/hold emitted strip update")
			}
		case <-timer.C:
			return
		}
	}
}

// The same post-reply check is used by production composition. Advancing the
// injected clock inside the model call reproduces a hold expiring in flight.
func TestTodayDayShapeRound1AcceptanceRetries(t *testing.T) {
	for _, reason := range []string{"overlap", "elapsed during model", "ordered interval"} {
		t.Run(reason, func(t *testing.T) {
			app := newTodayContextTestApp(t, true)
			brief, picks := writtenBriefFixture()
			day, err := todayDayShapeBriefDay(brief)
			if err != nil {
				t.Fatal(err)
			}
			clock := &fakeMobileBriefClock{now: mobileBriefAsOfTime(brief)}
			start := clock.Now().Add(time.Hour)
			end := start.Add(time.Hour)
			event := calendar.Event{ID: "busy", Source: calendar.SourceEventKit, Title: "Busy", Start: start, End: end}
			if reason != "overlap" {
				event.Start, event.End = end.Add(time.Hour), end.Add(2*time.Hour)
			}
			storeEvent(t, app.store, event)
			hold := &TodayDayShapeHold{Start: start.Format(time.RFC3339), End: end.Format(time.RFC3339), Label: "Sam memo"}
			question := "Want me to hold time for the note to Sam?"
			if reason == "ordered interval" {
				question = "Want me to hold " + end.Format("3:04 PM") + " to " + start.Format("3:04 PM") + " for the note to Sam?"
			}
			raw := writtenReply(t, &mobileBriefNarrativeReplyClosing{Text: question, Card: "offer-2", Hold: hold}, mobileBriefNarrativeReplyParagraph{Text: writtenShoe, Refs: []string{"e7"}})
			calls := 0
			paragraphs, _, check := writeMobileBriefNarrative(context.Background(), brief, picks, "PROMPT", "English", func(ask string) (string, error) {
				calls++
				if calls == 1 {
					if reason == "elapsed during model" {
						clock.Set(end.Add(time.Minute))
					}
					return raw, nil
				}
				if !strings.Contains(ask, "Your previous reply could not be used:") {
					t.Fatal("hold rejection bypassed existing retry path")
				}
				return writtenGoodReply(t), nil
			}, func(paragraphs []MobileBriefPara) error {
				return app.validateMobileBriefQuestionHold(brief, paragraphs, clock.Now())
			})
			if check != "" || calls != 2 || len(paragraphs) == 0 {
				t.Fatalf("check=%q calls=%d paragraphs=%d day=%s", check, calls, len(paragraphs), day)
			}
			if paragraphs[len(paragraphs)-1].Text != "Want me to draft the note to Sam?" {
				t.Fatal("retry prose was rewritten")
			}
		})
	}
}

func TestTodayDayShapeRound1GatedConflictsAndBoundaries(t *testing.T) {
	app, clock, brief, day := dayShapeHoldFixture(t)
	for _, interval := range []struct {
		start, end int
		valid      bool
	}{{13, 14, true}, {15, 16, true}, {13, 15, false}, {14, 16, false}, {14, 15, false}, {14, 14, false}} {
		hold := &TodayDayShapeHold{Start: day.Add(time.Duration(interval.start) * time.Hour).Format(time.RFC3339), End: day.Add(time.Duration(interval.end) * time.Hour).Format(time.RFC3339), Label: "Review prep"}
		brief.Narrative[0].Hold = hold
		shape, err := app.todayDayShapeAt(day, clock.Now(), brief)
		validated, validationErr := validatedTodayCalendarHold(hold, day, clock.Now(), shape)
		if err != nil || (validated != nil && validationErr == nil) != interval.valid || shape.Hold != nil {
			t.Fatalf("interval=%+v shape=%+v err=%v", interval, shape, err)
		}
		phone := app.mobileBriefWithDayShape(brief)
		if !reflect.DeepEqual(phone.DayShape.Hold, shape.Hold) {
			t.Fatalf("desktop/phone diverged: %+v %+v", shape.Hold, phone.DayShape.Hold)
		}
	}
	// A held-back event cannot constrain a proposal or leak through retries.
	seedMobileBriefSubject(t, app, "Review")
	storeEvent(t, app.store, calendar.Event{ID: "other", Source: calendar.SourceEventKit, Title: "Other", Start: day.Add(16 * time.Hour), End: day.Add(17 * time.Hour)})
	candidate := MobileBriefV1{LocalDate: brief.LocalDate, Timezone: brief.Timezone, AsOf: brief.AsOf, Slot: brief.Slot}
	paragraphs := []MobileBriefPara{{Text: "Want me to hold time for a memo?", Hold: &TodayDayShapeHold{Start: day.Add(14 * time.Hour).Format(time.RFC3339), End: day.Add(15 * time.Hour).Format(time.RFC3339), Label: "Memo"}}}
	if err := app.validateMobileBriefQuestionHold(candidate, paragraphs, clock.Now()); err != nil {
		t.Fatalf("mail-held-back event influenced validation: %v", err)
	}
}

func countDayShapeUpdates(t *testing.T, client chan []byte, duration time.Duration) int {
	t.Helper()
	timer := time.NewTimer(duration)
	defer timer.Stop()
	count := 0
	for {
		select {
		case raw := <-client:
			var event struct {
				Name string `json:"event"`
			}
			if err := json.Unmarshal(raw, &event); err != nil {
				t.Fatal(err)
			}
			if event.Name == "today-dayshape:updated" {
				count++
			}
		case <-timer.C:
			return count
		}
	}
}

func TestTodayDayShapeRound1MeaningfulCoalescedUpdates(t *testing.T) {
	app, clock, brief, day := dayShapeHoldFixture(t)
	svc := app.mobileBriefSvc.(*mobileBriefService)
	if err := svc.store.save(brief, clock.Now()); err != nil {
		t.Fatal(err)
	}
	app.eventBroadcaster = NewEventBroadcaster()
	client := app.eventBroadcaster.AddClient("coalesced")
	defer app.eventBroadcaster.RemoveClient("coalesced")
	app.primeTodayDayShapeUpdates()
	emitSources := func() {
		app.emitBrowserEvent("calendar:synced")
		app.emitBrowserEvent("brief:stored")
		app.noteTodayDayShapeUpdate()
	}
	emitSources()
	if got := countDayShapeUpdates(t, client, 150*time.Millisecond); got != 0 {
		t.Fatalf("unchanged source emitted %d updates", got)
	}
	// Clock advancement and prose-only storage leave the strip untouched.
	clock.Advance(time.Minute)
	brief.Narrative[0].Text = "Want me to hold time to prepare for Review?"
	if err := svc.store.save(brief, clock.Now()); err != nil {
		t.Fatal(err)
	}
	emitSources()
	if got := countDayShapeUpdates(t, client, 150*time.Millisecond); got != 0 {
		t.Fatalf("clock/prose emitted %d updates", got)
	}
	// A calendar change and a hidden hold in the same burst coalesce.
	storeEvent(t, app.store, calendar.Event{ID: "review", Source: calendar.SourceEventKit, Title: "Updated review", Start: day.Add(14 * time.Hour), End: day.Add(15 * time.Hour)})
	brief.Narrative[0].Hold = &TodayDayShapeHold{Start: day.Add(13 * time.Hour).Format(time.RFC3339), End: day.Add(14 * time.Hour).Format(time.RFC3339), Label: "Review prep"}
	if err := svc.store.save(brief, clock.Now()); err != nil {
		t.Fatal(err)
	}
	emitSources()
	if got := countDayShapeUpdates(t, client, 200*time.Millisecond); got != 1 {
		t.Fatalf("one calendar/hold change emitted %d updates", got)
	}
	emitSources()
	if got := countDayShapeUpdates(t, client, 150*time.Millisecond); got != 0 {
		t.Fatalf("repeated unchanged source emitted %d updates", got)
	}
	clock.Set(day.Add(13*time.Hour + time.Minute))
	emitSources()
	if got := countDayShapeUpdates(t, client, 200*time.Millisecond); got != 0 {
		t.Fatalf("hidden hold expiry emitted %d updates", got)
	}
}

func TestTodayDayShapeRound1PhoneRouteWithholdsAtPublication(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(fmt.Sprint("expired=", expired), func(t *testing.T) {
			app, clock, brief, day := dayShapeHoldFixture(t)
			if expired {
				brief.Narrative[0].Hold = &TodayDayShapeHold{Start: day.Add(8*time.Hour + time.Minute).Format(time.RFC3339), End: day.Add(8*time.Hour + 2*time.Minute).Format(time.RFC3339), Label: "Review prep"}
			}
			svc := app.mobileBriefSvc.(*mobileBriefService)
			if err := svc.store.save(brief, clock.Now()); err != nil {
				t.Fatal(err)
			}
			if expired {
				clock.Advance(3 * time.Minute)
			}
			bridge := newMobileBriefPrivacyBridge(t, app)
			response := httptest.NewRecorder()
			bridge.serveBrief(response, httptest.NewRequest(http.MethodGet, "/briefs/"+brief.BriefID, nil), brief.BriefID)
			// The package never carries the live calendar strip (PROTOCOL.md
			// section 8.1); the Today snapshot's day does.
			var wire struct {
				Data struct {
					DayShape *TodayDayShape `json:"dayShape"`
				} `json:"data"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &wire); err != nil || response.Code != 200 || wire.Data.DayShape != nil && (wire.Data.DayShape.CalendarConnected || wire.Data.DayShape.Hold != nil) {
				t.Fatalf("route=%s err=%v", response.Body.String(), err)
			}
			stored, ok := svc.store.load(brief.BriefID)
			if !ok || !reflect.DeepEqual(stored.Narrative, brief.Narrative) {
				t.Fatal("publication changed stored question")
			}
		})
	}
}

func TestTodayDayShapeRound1OrderedIntervalEvenWithSourceClocks(t *testing.T) {
	day := dayShapeTestDay(t)
	hold := &TodayDayShapeHold{Start: day.Add(11 * time.Hour).Format(time.RFC3339), End: day.Add(12 * time.Hour).Format(time.RFC3339), Label: "Prep"}
	clocks := map[string]map[string]bool{"e1": {"11:00 AM": true, "12:00 PM": true, "2:00 PM": true, "3:00 PM": true, "中午12点": true, "上午11点": true}}
	for _, text := range []string{"hold 12:00 PM to 11:00 AM", "hold between 12:00 PM and 11:00 AM", "hold 12:00 PM until 11:00 AM", "hold 2:00 PM to 3:00 PM", "hold 12:00 PM UNTIL 11:00 AM", "hold between 12:00 PM AND 11:00 AM", "预留中午12点到上午11点"} {
		if !mobileBriefClocksSupported(text, []string{"e1"}, clocks) {
			t.Fatalf("fixture clocks are not source-backed: %s", text)
		}
		if mobileBriefClosingClocksSupported(text, []string{"e1"}, clocks, hold, day.Location()) {
			t.Errorf("mismatched interval accepted: %s", text)
		}
	}
	for _, text := range []string{"hold 11:00 AM to 12:00 PM", "hold between 11:00 AM and 12:00 PM", "预留上午11点到中午12点"} {
		if !mobileBriefClosingClocksSupported(text, []string{"e1"}, clocks, hold, day.Location()) {
			t.Errorf("ordered interval rejected: %s", text)
		}
	}
}
