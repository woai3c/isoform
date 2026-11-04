package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"wails-gui/storage"
)

// N38: Together holds only moments that involve the person; other work from
// a session they appeared in stays in Activity.
func TestPersonTogetherLeavesOutUnrelatedSameDayWork(t *testing.T) {
	a := newTodayContextTestApp(t, true)
	person := peopleTestContact(t, a, "Lina Okafor")
	day := time.Now().Add(-26 * time.Hour).Truncate(time.Minute).UnixMilli()
	session := ActivitySessionRow{ID: "day-session", AppNames: `["WeChat","Safari","Keynote"]`, StartTime: day, EndTime: day + 4*3_600_000, TriageStatus: "done"}
	if err := a.activity.store.SaveSession(session, nil, nil); err != nil {
		t.Fatal(err)
	}
	moment := func(id, app, title string, at int64) {
		run := testRun("run-"+id, app, at, at+60_000, 60_000, 1)
		run.FirstEventRowID, run.LastEventRowID = at, at
		run.SessionIDs = []string{session.ID}
		insertComposableTimelineRun(t, a.activity.store, run, ActivityTimelineGroupSummary{})
		if err := a.activity.store.InsertActivity(ActivityRow{ID: id, Title: title, RunIDs: fmt.Sprintf(`["%s"]`, run.ID), ActiveMsByApp: fmt.Sprintf(`{%q:60000}`, app), StartMs: at, EndMs: at + 60_000}); err != nil {
			t.Fatal(err)
		}
	}
	moment("chat", "WeChat", "Message Lina in WeChat about the launch timing", day+10*60_000)
	moment("deck", "Keynote", "Wrote the launch notes for Lina Okafor", day+30*60_000)
	moment("search", "Safari", "Searched for pet adoption centers", day+60*60_000)
	moment("pitch", "Keynote", "Opened the pitch deck", day+90*60_000)
	moment("review", "Isoform", "Reviewed Today, Chat and Tasks views", day+120*60_000)
	if err := a.store.BumpPrincipalSighting(t.Context(), person.ID, "wechat", session.ID, day+10*60_000+5_000, "message text never returned"); err != nil {
		t.Fatal(err)
	}

	detail, err := a.GetPersonDetail(person.ID)
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, item := range detail.Together {
		if item.Kind == "activity" {
			titles = append(titles, item.Title)
		}
	}
	got := strings.Join(titles, " | ")
	for _, want := range []string{"Messaged Lina about the launch timing", "Wrote the launch notes for Lina Okafor"} {
		if !strings.Contains(got, want) {
			t.Fatalf("Together lost %q: %s", want, got)
		}
	}
	for _, unrelated := range []string{"pet adoption", "pitch deck", "Reviewed Today"} {
		if strings.Contains(got, unrelated) {
			t.Fatalf("Together shows unrelated work %q on Lina's card: %s", unrelated, got)
		}
	}
}

func TestPersonTogetherEmptyWithoutInvolvement(t *testing.T) {
	a := newTodayContextTestApp(t, true)
	person := peopleTestContact(t, a, "Theo Marsh")
	day := time.Now().Add(-26 * time.Hour).Truncate(time.Minute).UnixMilli()
	session := ActivitySessionRow{ID: "theo-day", AppNames: `["Safari"]`, StartTime: day, EndTime: day + 3_600_000, TriageStatus: "done"}
	if err := a.activity.store.SaveSession(session, nil, nil); err != nil {
		t.Fatal(err)
	}
	run := testRun("run-theo", "Safari", day+600_000, day+660_000, 60_000, 1)
	run.FirstEventRowID, run.LastEventRowID = day+600_000, day+600_000
	run.SessionIDs = []string{session.ID}
	insertComposableTimelineRun(t, a.activity.store, run, ActivityTimelineGroupSummary{})
	if err := a.activity.store.InsertActivity(ActivityRow{ID: "theo-search", Title: "Searched for voice-agent platforms", RunIDs: `["run-theo"]`, ActiveMsByApp: `{"Safari":60000}`, StartMs: day + 600_000, EndMs: day + 660_000}); err != nil {
		t.Fatal(err)
	}
	// Seen in this session, but not while the search ran.
	if err := a.store.BumpPrincipalSighting(t.Context(), person.ID, "wechat", session.ID, day+50*60_000, "x"); err != nil {
		t.Fatal(err)
	}
	detail, err := a.GetPersonDetail(person.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range detail.Together {
		if item.Kind == "activity" {
			t.Fatalf("unrelated activity on the card: %+v", item)
		}
	}
}

// N38 on real data: in a long, busy day the person messages you all day long,
// so every moment overlaps a message of theirs. Only the moments where they
// are the other side stay; work in the same app, at the same hour, or that
// names them in passing does not.
func TestPersonTogetherNeedsThePersonAsAParticipant(t *testing.T) {
	a := newTodayContextTestApp(t, true)
	mira := peopleTestContact(t, a, "Mira Castell")
	lin := peopleTestContact(t, a, "林晓峰")
	day := time.Now().Add(-26 * time.Hour).Truncate(time.Minute).UnixMilli()
	session := ActivitySessionRow{ID: "busy-day", AppNames: `["WeChat","Claude","Safari","Google Chrome","Lark","Pages"]`, StartTime: day, EndTime: day + 10*3_600_000, TriageStatus: "done"}
	if err := a.activity.store.SaveSession(session, nil, nil); err != nil {
		t.Fatal(err)
	}
	moment := func(id, app, title string, startMin, minutes int64) {
		at, end := day+startMin*60_000, day+(startMin+minutes)*60_000
		run := testRun("run-"+id, app, at, end, end-at, 1)
		run.FirstEventRowID, run.LastEventRowID = at, at
		run.SessionIDs = []string{session.ID}
		insertComposableTimelineRun(t, a.activity.store, run, ActivityTimelineGroupSummary{})
		if err := a.activity.store.InsertActivity(ActivityRow{ID: id, Title: title, RunIDs: fmt.Sprintf(`["%s"]`, run.ID), ActiveMsByApp: fmt.Sprintf(`{%q:%d}`, app, end-at), StartMs: at, EndMs: end}); err != nil {
			t.Fatal(err)
		}
	}
	moment("msg", "WeChat", "Message Mira in WeChat about the website and release timing", 20, 15)
	moment("reply", "WeChat", "Reply to Mira in WeChat about the beta invite", 300, 10)
	moment("notes", "Pages", "Write the launch notes for Mira", 400, 40)
	moment("agents", "Claude", "Research and architect Echo AI phone and SignalWire text agents", 0, 180)
	moment("platforms", "Safari", "Research voice agent platforms Mira mentioned", 180, 90)
	moment("repo", "Google Chrome", "You read the e-reader repository on GitHub", 270, 30)
	moment("steer", "Claude", "Steer coding agent sessions on the release branch", 330, 60)
	moment("meeting", "Lark", "Attend Lark video meeting 790 818 477 with 林晓峰", 450, 45)
	moment("passing", "WeChat", "Message 林晓峰 in WeChat about Mira's launch notes", 500, 10)
	for minute := int64(0); minute < 600; minute += 25 {
		if err := a.store.BumpPrincipalSighting(t.Context(), mira.ID, "wechat", session.ID, day+minute*60_000+5_000, "message text never returned"); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.store.BumpPrincipalSighting(t.Context(), lin.ID, "lark", session.ID, day+460*60_000, "x"); err != nil {
		t.Fatal(err)
	}

	together := func(id string) []PersonTogether {
		t.Helper()
		detail, err := a.GetPersonDetail(id)
		if err != nil {
			t.Fatal(err)
		}
		var out []PersonTogether
		for _, item := range detail.Together {
			if item.Kind == "activity" {
				out = append(out, item)
			}
		}
		return out
	}
	var got []string
	for _, item := range together(mira.ID) {
		got = append(got, item.Title+" · "+item.App)
	}
	want := []string{
		"Wrote the launch notes for Mira · Pages",
		"Replied to Mira about the beta invite · WeChat",
		"Messaged Mira about the website and release timing · WeChat",
	}
	if strings.Join(got, " | ") != strings.Join(want, " | ") {
		t.Fatalf("Mira's Together = %q, want %q", got, want)
	}
	var lins []string
	for _, item := range together(lin.ID) {
		lins = append(lins, item.Title)
	}
	if strings.Join(lins, " | ") != "Messaged 林晓峰 about Mira's launch notes | Attended Lark video meeting with 林晓峰" {
		t.Fatalf("林晓峰's Together = %q", lins)
	}
	for _, id := range []string{mira.ID, lin.ID} {
		detail, err := a.GetPersonDetail(id)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range detail.Together {
			if strings.Contains(item.Title, "790 818 477") {
				t.Errorf("meeting ID appeared in Together: %+v", item)
			}
			if id == mira.ID {
				for _, unwanted := range []string{"Research and architect Echo AI phone and SignalWire text agents", "Research voice agent platforms", "e-reader repository on GitHub", "video meeting"} {
					if strings.Contains(item.Title, unwanted) {
						t.Errorf("Mira's Together contains unrelated %q: %+v", unwanted, item)
					}
				}
			}
		}
	}
}

func TestPersonTogetherCalendarMeetingOmitsMeetingID(t *testing.T) {
	a := newTodayContextTestApp(t, true)
	person := peopleTestContact(t, a, "Mira Castell")
	at := time.Now().Add(-time.Hour).UnixMilli()
	if err := a.store.SetUIState(t.Context(), permissionUIStateKey(calendarPermissionKey), "true"); err != nil {
		t.Fatal(err)
	}
	event := storage.CalendarEventRow{ID: "meeting", Source: "eventkit", Provider: "eventkit", Title: "Planning call 123-456-7890 with Mira", StartAt: at, EndAt: at + 60_000, Attendees: []storage.CalendarAttendeeRow{{PrincipalID: person.ID, Email: "mira@example.test"}}}
	if err := a.store.UpsertCalendarEvents(t.Context(), []storage.CalendarEventRow{event}, at); err != nil {
		t.Fatal(err)
	}
	detail, err := a.GetPersonDetail(person.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Together) != 1 || detail.Together[0].Title != "Planning call with Mira" {
		t.Fatalf("Together = %+v", detail.Together)
	}
}

func TestPersonTogetherParticipantWords(t *testing.T) {
	names := personTogetherNames(storage.PrincipalRow{DisplayName: "Mira Castell", Aliases: []string{"王晓雨"}})
	for title, want := range map[string]bool{
		"Messaged Mira about the launch":          true,
		"Called Mira Castell":                     true,
		"Shared the deck with Mira":               true,
		"Mira shared the roadmap":                 true,
		"给王晓雨发消息":                                 true,
		"和王晓雨聊了发布时间":                              true,
		"Research voice platforms Mira mentioned": false,
		"Read about Mira's launch":                false,
		"Messaged Miranda about the launch":       false,
		"王晓雨的博客":                                  false,
		"Steer coding agent sessions":             false,
	} {
		if got := personTogetherParticipant(title, names); got != want {
			t.Errorf("personTogetherParticipant(%q) = %v, want %v", title, got, want)
		}
	}
}

func TestPeopleOverviewDoesNotComputeTogether(t *testing.T) {
	a := newTodayContextTestApp(t, true)
	for i := 0; i < 33; i++ {
		peopleTestContact(t, a, fmt.Sprintf("Fictional Person %02d", i))
	}
	called := make(chan struct{}, 1)
	a.peopleTogetherProbe = func() error {
		called <- struct{}{}
		time.Sleep(3 * time.Second)
		return nil
	}
	type answer struct {
		overview PeopleOverview
		err      error
	}
	result := make(chan answer, 1)
	go func() {
		overview, err := a.ListPeopleOverview()
		result <- answer{overview, err}
	}()
	select {
	case got := <-result:
		if got.err != nil || got.overview.KnownCount != 33 {
			t.Fatalf("overview count=%d err=%v", got.overview.KnownCount, got.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("overview waited for Together evidence")
	}
	select {
	case <-called:
		t.Fatal("overview computed Together evidence")
	default:
	}
}

func TestPersonTogetherFailureKeepsDetail(t *testing.T) {
	a := newTodayContextTestApp(t, true)
	person := peopleTestContact(t, a, "Mira Castell")
	a.peopleTogetherProbe = func() error { return fmt.Errorf("evidence unavailable") }
	detail, err := a.GetPersonDetail(person.ID)
	if err != nil || detail.Name != person.DisplayName || !detail.TogetherFailed || len(detail.Together) != 0 {
		t.Fatalf("detail=%+v err=%v", detail, err)
	}
	a.peopleTogetherProbe = nil
	detail, err = a.GetPersonDetail(person.ID)
	if err != nil || detail.TogetherFailed {
		t.Fatalf("retry detail=%+v err=%v", detail, err)
	}
}
