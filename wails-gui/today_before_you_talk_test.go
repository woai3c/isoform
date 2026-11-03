package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/calendar"
	"wails-gui/internal/engagement"
	"wails-gui/internal/pii"
	"wails-gui/internal/platform/activitymon"
	"wails-gui/storage"
)

func beforeYouTalkEvent(id, personID string, start, end time.Time) storage.CalendarEventRow {
	row := storage.CalendarEventRow{
		ID: id, Source: calendar.SourceEventKit, Provider: "eventkit", Title: "Meeting " + id,
		StartAt: start.UnixMilli(), EndAt: end.UnixMilli(), Availability: "busy",
	}
	if personID != "" {
		row.Attendees = []storage.CalendarAttendeeRow{{Email: personID + "@example.test", PrincipalID: personID}}
	} else {
		row.Attendees = []storage.CalendarAttendeeRow{{Email: "unknown@example.test", Name: "Unknown"}}
	}
	return row
}

func TestTodayBeforeYouTalkTimingAndAttendeeGates(t *testing.T) {
	now := time.Date(2026, 10, 24, 14, 10, 0, 0, time.Local)
	tests := []struct {
		name  string
		start time.Time
		end   time.Time
		want  bool
	}{
		{name: "in progress", start: now.Add(-time.Hour), end: now.Add(time.Minute), want: true},
		{name: "starting now", start: now, end: now.Add(time.Hour), want: true},
		{name: "exactly two hours", start: now.Add(2 * time.Hour), end: now.Add(3 * time.Hour), want: true},
		{name: "beyond two hours", start: now.Add(2*time.Hour + time.Millisecond), end: now.Add(3 * time.Hour), want: false},
		{name: "ended", start: now.Add(-time.Hour), end: now, want: false},
		{name: "zero duration", start: now, end: now, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			event := beforeYouTalkEvent("event", "person", test.start, test.end)
			if got := todayBeforeYouTalkEventEligible(event, now); got != test.want {
				t.Fatalf("eligible=%t want %t", got, test.want)
			}
		})
	}
	for _, test := range []struct {
		name   string
		mutate func(*storage.CalendarEventRow)
	}{
		{name: "all day", mutate: func(event *storage.CalendarEventRow) { event.AllDay = true }},
		{name: "deleted", mutate: func(event *storage.CalendarEventRow) { event.DeletedAt = 1 }},
		{name: "canceled", mutate: func(event *storage.CalendarEventRow) { event.Status = "canceled" }},
		{name: "declined", mutate: func(event *storage.CalendarEventRow) { event.MyStatus = calendar.StatusDeclined }},
		{name: "free", mutate: func(event *storage.CalendarEventRow) { event.Availability = "free" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			event := beforeYouTalkEvent("event", "person", now, now.Add(time.Hour))
			test.mutate(&event)
			if todayBeforeYouTalkEventEligible(event, now) {
				t.Fatal("ineligible event was admitted")
			}
		})
	}
	event := beforeYouTalkEvent("event", "person", now, now.Add(time.Hour))
	event.Attendees = []storage.CalendarAttendeeRow{
		{PrincipalID: "self", IsMe: true},
		{PrincipalID: "declined", Status: calendar.StatusDeclined},
		{PrincipalID: "known"},
	}
	if got := todayBeforeYouTalkPerson(event); got != "known" {
		t.Fatalf("person=%q", got)
	}
	if got := todayBeforeYouTalkPerson(beforeYouTalkEvent("unknown", "", now, now.Add(time.Hour))); got != "" {
		t.Fatalf("unknown attendee admitted as %q", got)
	}
}

func TestMeetingPrepAskUsesCurrentMeetingAndPersonContext(t *testing.T) {
	a := newTodayContextTestApp(t, true)
	if err := a.store.SetUIState(t.Context(), permissionUIStateKey(calendarPermissionKey), "true"); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	person := peopleTestContact(t, a, "Mei Chen")
	peopleTestPurpose(t, a, "Send the launch outline", person.DisplayName, PurposeOwnerUser, time.Time{})
	event := beforeYouTalkEvent("prep-event", person.ID, now.Add(20*time.Minute), now.Add(50*time.Minute))
	if err := a.store.UpsertCalendarEvents(t.Context(), []storage.CalendarEventRow{event}, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	ref := TodayChatContextRef{SourceKind: "calendar", SourceID: event.ID, MeetingPrep: true, MeetingStartMs: event.StartAt, GeneratedTitle: event.Title, GeneratedPeople: []string{person.DisplayName}}
	result, err := a.buildTodayChatContext(t.Context(), ref)
	if err != nil || !strings.Contains(result.Text, "Mei Chen") || !strings.Contains(result.Text, "Send the launch outline") || !strings.Contains(result.Text, "Who's coming, Last time, Still open, What they asked") {
		t.Fatalf("prep context: %q %v", result.Text, err)
	}
	note := ref
	note.MeetingPrep, note.MeetingNote = false, true
	noteResult, err := a.buildTodayChatContext(t.Context(), note)
	if err != nil || !strings.Contains(noteResult.Text, "Write only the text of a short note") || !strings.Contains(noteResult.Text, "Mei Chen") {
		t.Fatalf("note context: %q %v", noteResult.Text, err)
	}
	_, addressName, address, subject, err := a.todayMeetingNoteRecipient(t.Context(), event.ID)
	if err != nil || addressName != person.DisplayName || address != person.ID+"@example.test" || subject != event.Title {
		t.Fatalf("meeting note recipient=%q %q %q err=%v", addressName, address, subject, err)
	}
	item, _, err := a.loadTodayChatContextItem(t.Context(), note)
	if err != nil || item.MeetingRecipientProof == "" {
		t.Fatalf("missing recipient proof: %+v %v", item, err)
	}
	event.Attendees[0].Email = "changed@example.test"
	if err := a.store.UpsertCalendarEvents(t.Context(), []storage.CalendarEventRow{event}, now.Add(time.Second).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	changedItem, _, err := a.loadTodayChatContextItem(t.Context(), note)
	if err != nil || changedItem.MeetingRecipientProof == item.MeetingRecipientProof {
		t.Fatalf("changed recipient was not detected: %+v %v", changedItem, err)
	}
	if !panelAskCanSave(note) || panelAnswerKey(note) == panelAnswerKey(ref) {
		t.Fatal("meeting note did not get its own saved-answer identity")
	}
	invalid := note
	invalid.MeetingPrep = true
	if todayChatRefWithinBounds(invalid) {
		t.Fatal("meeting prep and note cannot share a request")
	}
	if !todayChatGeneratedFactsMatch(ref, result) {
		t.Fatalf("person or title did not bind: %+v", result)
	}
	main, err := a.ensureMainChat(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.admitTodayChatTurn(t.Context(), main.LocalID, storage.StreamItemRow{ID: "prep-user", Kind: "user", Content: "Brief me."}, ref, nil, true); err != nil {
		t.Fatal(err)
	}
	if err := a.store.BindAnswerChipUserRun(t.Context(), main.LocalID, "prep-user", "prep-run"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.AppendStreamItem(t.Context(), main.LocalID, storage.StreamItemRow{ID: "prep-answer", Kind: "assistant", RunID: "prep-run", Content: "Who's coming: Mei Chen"}); err != nil {
		t.Fatal(err)
	}
	a.savePanelAskAnswer(main.LocalID, "prep-user", "prep-run", "prep-answer", "Who's coming: Mei Chen")
	answer, err := a.GetTodayPanelAnswer(ref)
	if err != nil || answer == nil || answer.Answer != "Who's coming: Mei Chen" {
		t.Fatalf("saved prep = %+v %v", answer, err)
	}
	stale := ref
	stale.MeetingStartMs--
	if _, err := a.buildTodayChatContext(t.Context(), stale); err == nil {
		t.Fatal("stale meeting time was accepted")
	}
	a.closedEyes.enabled.Store(true)
	if _, err := a.buildTodayChatContext(t.Context(), ref); err == nil {
		t.Fatal("prep read while observing paused")
	}
	answer, err = a.GetTodayPanelAnswer(ref)
	if err != nil || answer != nil {
		t.Fatalf("paused prep returned saved answer: %+v %v", answer, err)
	}
}

func TestTodayBeforeYouTalkNearestContentAndMeetingScopedDismissal(t *testing.T) {
	a := newTodayContextTestApp(t, true)
	a.store.SetUIState(t.Context(), permissionUIStateKey(calendarPermissionKey), "true")
	now := time.Now().Truncate(time.Second)
	person := peopleTestContact(t, a, "Mei Chen")
	other := peopleTestContact(t, a, "Dana")
	peopleTestPurpose(t, a, "Send the Meridian memo outline", person.DisplayName, PurposeOwnerUser, time.Time{})
	peopleTestMoment(t, a, person, "waiting_on_reply", now.Add(-2*time.Hour))
	peopleTestActivity(t, a, person, "unrelated-later", "Code", "Unrelated later activity", "done", now.Add(-time.Hour).UnixMilli())

	pastStart := now.Add(-7 * 24 * time.Hour)
	session := ActivitySessionRow{ID: "last-meeting", AppNames: `["Code"]`, StartTime: pastStart.UnixMilli(), EndTime: pastStart.UnixMilli() + 1000, TriageStatus: "done", EventCount: 1}
	if err := a.activity.store.SaveSession(session, []activitymon.ActivityEvent{{Timestamp: pastStart.UnixMilli(), AppName: "Code", AppBundleID: "com.microsoft.VSCode", WindowTitle: "Meeting notes"}}, nil); err != nil {
		t.Fatal(err)
	}
	run := testRun("run-last-meeting", "Code", pastStart.UnixMilli(), pastStart.UnixMilli()+1000, 1000, 1)
	run.FirstEventRowID, run.LastEventRowID = pastStart.UnixMilli(), pastStart.UnixMilli()
	run.SessionIDs = []string{session.ID}
	insertComposableTimelineRun(t, a.activity.store, run, ActivityTimelineGroupSummary{})
	if err := a.activity.store.InsertActivity(ActivityRow{ID: session.ID, Title: "Agreed to a staging preview before any date change", RunIDs: `["run-last-meeting"]`, ActiveMsByApp: `{"Code":1000}`, StartMs: pastStart.UnixMilli(), EndMs: pastStart.UnixMilli() + 1000}); err != nil {
		t.Fatal(err)
	}
	if err := a.store.BumpPrincipalSighting(t.Context(), person.ID, "Code", session.ID, pastStart.UnixMilli(), "message text never returned"); err != nil {
		t.Fatal(err)
	}
	past := beforeYouTalkEvent("past", person.ID, pastStart, pastStart.Add(40*time.Minute))
	past.SessionID = "last-meeting"
	unknown := beforeYouTalkEvent("unknown", "", now.Add(5*time.Minute), now.Add(35*time.Minute))
	next := beforeYouTalkEvent("next", person.ID, now.Add(20*time.Minute), now.Add(50*time.Minute))
	next.ConferenceURL = "https://zoom.us/j/123"
	later := beforeYouTalkEvent("later", other.ID, now.Add(40*time.Minute), now.Add(70*time.Minute))
	if err := a.store.UpsertCalendarEvents(t.Context(), []storage.CalendarEventRow{past, unknown, next, later}, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	privacy, err := a.mcpReadPrivacy(t.Context())
	if err != nil || !privacy.calendarAllowed(past) {
		t.Fatalf("past calendar event is not readable: %v", err)
	}
	moments, err := a.activity.store.GetActivitiesInRange(pastStart.UnixMilli(), pastStart.UnixMilli()+1001)
	if err != nil || len(moments) != 1 {
		t.Fatalf("moments=%+v err=%v", moments, err)
	}
	policy, err := a.activityReadPrivacyPolicy()
	if err != nil || !a.peopleActivityReadable(t.Context(), moments[0], policy.pauses) {
		t.Fatalf("last meeting moment is not readable: %v", err)
	}
	if !a.todayBeforeYouTalkMomentInSession(t.Context(), moments[0], "last-meeting") {
		t.Fatal("last meeting moment is not associated with its session")
	}

	view, err := a.todayBeforeYouTalkAt(now)
	if err != nil {
		t.Fatal(err)
	}
	if view == nil || view.MeetingID != "next" || view.PersonID != person.ID || view.PersonName != person.DisplayName {
		t.Fatalf("view=%+v", view)
	}
	if view.ConferenceURL != next.ConferenceURL {
		t.Fatalf("conference URL = %q", view.ConferenceURL)
	}
	if view.YouOwe != "Send the Meridian memo outline" {
		t.Fatalf("you owe=%q", view.YouOwe)
	}
	if view.TheyAsked != "" || view.HasUnsentDraft {
		t.Fatalf("unprojected reply content leaked: %+v", view)
	}
	if view.LastTime != "Agreed to a staging preview before any date change" || view.LastTimeAt != pastStart.UnixMilli() || view.LastDurationMs != int64(40*time.Minute/time.Millisecond) {
		t.Fatalf("last time=%+v", view)
	}

	rig := newEngagementTestRig(t)
	a.engagement = rig.svc
	if err := rig.svc.SetKindEnabled(t.Context(), engagement.KindMeetingPrep, false); err != nil {
		t.Fatal(err)
	}
	if muted, err := a.todayBeforeYouTalkAt(now); err != nil || muted != nil {
		t.Fatalf("muted meeting prep = %+v, %v", muted, err)
	}
	if err := rig.svc.SetKindEnabled(t.Context(), engagement.KindMeetingPrep, true); err != nil {
		t.Fatal(err)
	}
	if restored, err := a.todayBeforeYouTalkAt(now); err != nil || restored == nil || restored.MeetingID != "next" {
		t.Fatalf("restored meeting prep = %+v, %v", restored, err)
	}

	if err := rig.svc.MuteKind(t.Context(), engagement.KindMeetingPrep, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if muted, err := a.todayBeforeYouTalkAt(now); err != nil || muted != nil {
		t.Fatalf("temporarily muted meeting prep = %+v, %v", muted, err)
	}
	if err := rig.svc.SetKindEnabled(t.Context(), engagement.KindMeetingPrep, true); err != nil {
		t.Fatal(err)
	}

	if err := a.DismissTodayBeforeYouTalk("next"); err != nil {
		t.Fatal(err)
	}
	view, err = a.todayBeforeYouTalkAt(now)
	if err != nil {
		t.Fatal(err)
	}
	if view == nil || view.MeetingID != "later" {
		t.Fatalf("dismissal did not select the other meeting: %+v", view)
	}
	if err := a.DismissTodayBeforeYouTalk(" "); err == nil {
		t.Fatal("blank meeting ID was accepted")
	}
}

func TestTodayBeforeYouTalkHiddenWithoutKnownPersonOrWhenEnded(t *testing.T) {
	a := newTodayContextTestApp(t, true)
	a.store.SetUIState(t.Context(), permissionUIStateKey(calendarPermissionKey), "true")
	now := time.Now().Truncate(time.Second)
	unknown := beforeYouTalkEvent("unknown", "", now.Add(10*time.Minute), now.Add(40*time.Minute))
	ended := beforeYouTalkEvent("ended", "known", now.Add(-time.Hour), now)
	if err := a.store.UpsertCalendarEvents(t.Context(), []storage.CalendarEventRow{unknown, ended}, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	view, err := a.todayBeforeYouTalkAt(now)
	if err != nil || view != nil {
		t.Fatalf("view=%+v err=%v", view, err)
	}
}

func TestTodayBeforeYouTalkPrivacyGates(t *testing.T) {
	before := pii.GetUserSensitiveApps()
	t.Cleanup(func() { pii.SetUserSensitiveApps(before) })
	for _, test := range []struct {
		name      string
		configure func(*App, *storage.CalendarEventRow)
	}{
		{name: "closed eyes", configure: func(a *App, _ *storage.CalendarEventRow) { a.closedEyes.enabled.Store(true) }},
		{name: "calendar disabled", configure: func(a *App, _ *storage.CalendarEventRow) {
			a.store.SetUIState(t.Context(), permissionUIStateKey(calendarPermissionKey), "false")
		}},
		{name: "sensitive calendar", configure: func(_ *App, _ *storage.CalendarEventRow) {
			pii.SetUserSensitiveApps(map[string]string{"com.apple.iCal": "Calendar"})
		}},
		{name: "unprovenanced web event", configure: func(_ *App, event *storage.CalendarEventRow) { event.Source = "web" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			pii.SetUserSensitiveApps(nil)
			a := newTodayContextTestApp(t, true)
			a.store.SetUIState(t.Context(), permissionUIStateKey(calendarPermissionKey), "true")
			person := peopleTestContact(t, a, "Mei Chen")
			now := time.Now().Truncate(time.Second)
			event := beforeYouTalkEvent("next", person.ID, now.Add(10*time.Minute), now.Add(40*time.Minute))
			test.configure(a, &event)
			if err := a.store.UpsertCalendarEvents(t.Context(), []storage.CalendarEventRow{event}, now.UnixMilli()); err != nil {
				t.Fatal(err)
			}
			view, err := a.todayBeforeYouTalkAt(now)
			if err != nil || view != nil {
				t.Fatalf("private meeting was shown: view=%+v err=%v", view, err)
			}
		})
	}
}

func TestTodayBeforeYouTalkReplyUsesOnlyOpenProjectedCard(t *testing.T) {
	for _, test := range []struct {
		state     string
		body      string
		wantShown string
	}{
		{state: "pending", body: "raw body replaced by localized catalog copy", wantShown: "Someone is waiting on your reply."},
		{state: "dismissed", body: "resolved private question", wantShown: ""},
	} {
		t.Run(test.state, func(t *testing.T) {
			a := newTodayContextTestApp(t, true)
			a.store.SetUIState(t.Context(), permissionUIStateKey(calendarPermissionKey), "true")
			a.store.SetUIState(t.Context(), permissionUIStateKey(mailPermissionKey), "true")
			person := peopleTestContact(t, a, "Mei Chen")
			now := time.Now().Truncate(time.Second)
			peopleTestMoment(t, a, person, "waiting_on_reply", now.Add(-2*time.Hour))
			moments, err := a.store.OpenEngagementMomentsOfKind(t.Context(), "waiting_on_reply")
			if err != nil || len(moments) != 1 {
				t.Fatalf("moments=%+v err=%v", moments, err)
			}
			card := mainChatProactive{MomentID: moments[0].ID, Kind: "waiting_on_reply", Body: test.body, State: test.state}
			a.bindEngagementCardRead(t.Context(), &card)
			payload, err := json.Marshal(card)
			if err != nil {
				t.Fatal(err)
			}
			main, err := a.ensureMainChat(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if id := a.appendMainChatItem(t.Context(), main.LocalID, storage.StreamItemRow{ID: "reply-card", Kind: "system", ToolName: mainChatProactiveTool, ToolInput: string(payload), CreatedAt: now.UnixMilli()}, mainChatSourceEngagement); id == "" {
				t.Fatal("reply card was not stored")
			}
			event := beforeYouTalkEvent("next", person.ID, now.Add(10*time.Minute), now.Add(40*time.Minute))
			if err := a.store.UpsertCalendarEvents(t.Context(), []storage.CalendarEventRow{event}, now.UnixMilli()); err != nil {
				t.Fatal(err)
			}
			view, err := a.todayBeforeYouTalkAt(now)
			if err != nil || view == nil || view.TheyAsked != test.wantShown || view.HasUnsentDraft {
				t.Fatalf("view=%+v err=%v", view, err)
			}
		})
	}
}
