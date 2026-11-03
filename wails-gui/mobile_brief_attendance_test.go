package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/calendar"
	"wails-gui/internal/pii"
	"wails-gui/internal/platform/activitymon"
)

func attendanceFixture(t *testing.T, state string) (calendar.Event, []mobileBriefAttendanceActivity, time.Time) {
	t.Helper()
	var fixture struct {
		Start           string                                 `json:"start"`
		DurationMinutes int                                    `json:"durationMinutes"`
		Activity        map[string][]activitymon.ActivityEvent `json:"activity"`
	}
	raw, err := os.ReadFile("testdata/brief_attendance_activity.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	start, err := time.Parse(time.RFC3339, fixture.Start)
	if err != nil {
		t.Fatal(err)
	}
	event := prepEvent("calendar-review", "Review with Tim", start, fixture.DurationMinutes, "Zoom", 1)
	var activity []mobileBriefAttendanceActivity
	for _, row := range fixture.Activity[state] {
		activity = append(activity, mobileBriefAttendanceActivity{SessionID: "fixture-session", Event: row})
	}
	if len(activity) == 0 {
		t.Fatalf("missing fixture state %s", state)
	}
	return event, activity, event.End.Add(time.Hour)
}

func TestMobileBriefAttendanceAttendedFromRecordedActivity(t *testing.T) {
	event, activity, asOf := attendanceFixture(t, "attended")
	observations := inferMobileBriefAttendance([]calendar.Event{event}, activity, nil, nil, asOf, true)
	if len(observations) != 1 || observations[0].State != "attended" || observations[0].Reason != "matching_call_activity" {
		t.Fatalf("attendance = %+v", observations)
	}
	if got := observations[0].SourceSessionIDs; len(got) != 1 || got[0] != "fixture-session" {
		t.Fatalf("provenance = %v", got)
	}
	// A silent user still attended when the call window remained active. No
	// speech body or recording permission is required for this observation.
	for i := range activity {
		activity[i].Event.Kind = activitymon.EventFocus
		activity[i].Event.SpeechInCall = false
		activity[i].Event.SpeechText = ""
	}
	if got := inferMobileBriefAttendance([]calendar.Event{event}, activity, nil, nil, asOf, true)[0].State; got != "attended" {
		t.Fatalf("silent call = %s", got)
	}
}

func TestMobileBriefAttendanceSkippedFromRecordedActivity(t *testing.T) {
	event, activity, asOf := attendanceFixture(t, "skipped")
	observations := inferMobileBriefAttendance([]calendar.Event{event}, activity, nil, nil, asOf, true)
	if len(observations) != 1 || observations[0].State != "skipped" || observations[0].Reason != "active_elsewhere_on_computer" {
		t.Fatalf("attendance = %+v", observations)
	}
	// Sparse activity is not proof that the call was skipped.
	sparse := []mobileBriefAttendanceActivity{activity[0], activity[len(activity)-1]}
	if got := inferMobileBriefAttendance([]calendar.Event{event}, sparse, nil, nil, asOf, true)[0].State; got != "unknown" {
		t.Fatalf("sparse activity = %s", got)
	}
}

func TestMobileBriefAttendanceCandidateSpeechIsNotConfirmed(t *testing.T) {
	event, activity, asOf := attendanceFixture(t, "attended")
	for i := range activity {
		activity[i].Event.Kind = activitymon.EventSpeech
		activity[i].Event.AppBundleID = "com.apple.Notes"
		activity[i].Event.WindowTitle = "Notes"
		activity[i].Event.AppName = "Zoom"
		activity[i].Event.SpeechInCall = true
		activity[i].Event.MeetingCandidate = true
	}
	if got := inferMobileBriefAttendance([]calendar.Event{event}, activity, nil, nil, asOf, true)[0].State; got != "unknown" {
		t.Fatalf("candidate speech must remain uncertain: %s", got)
	}
}

func TestMobileBriefAttendanceRequiresDeviceProvenanceForSkipped(t *testing.T) {
	event, recorded, asOf := attendanceFixture(t, "skipped")
	var activity []mobileBriefAttendanceActivity
	for _, item := range recorded {
		if item.Event.Kind != activitymon.EventDeviceInUse {
			activity = append(activity, item)
		}
	}
	if got := inferMobileBriefAttendance([]calendar.Event{event}, activity, nil, nil, asOf, true)[0].State; got != "unknown" {
		t.Fatalf("launch during a background call has no starting device state: %s", got)
	}
	for _, device := range []string{"microphone", "camera"} {
		activity = append(activity, mobileBriefAttendanceActivity{Event: activitymon.ActivityEvent{Timestamp: event.Start.Add(10 * time.Minute).UnixMilli(), Kind: activitymon.EventDeviceInUse, Device: device}})
	}
	if got := inferMobileBriefAttendance([]calendar.Event{event}, activity, nil, nil, asOf, true)[0].State; got != "unknown" {
		t.Fatalf("late device snapshot must not fill earlier unknown coverage: %s", got)
	}
	pauses := []observationPause{{Start: recorded[0].Event.Timestamp, End: event.Start.UnixMilli()}}
	if got := inferMobileBriefAttendance([]calendar.Event{event}, recorded, nil, pauses, asOf, true)[0].State; got != "unknown" {
		t.Fatalf("pre-pause device state must not fill resume coverage: %s", got)
	}
}

func TestMobileBriefAttendanceIdleDoesNotEraseConfirmedCall(t *testing.T) {
	event, activity, asOf := attendanceFixture(t, "attended")
	activity = append(activity, mobileBriefAttendanceActivity{Event: activitymon.ActivityEvent{Timestamp: event.Start.Add(time.Minute).UnixMilli(), Kind: activitymon.EventSessionState, StateName: "idle"}})
	if got := inferMobileBriefAttendance([]calendar.Event{event}, activity, nil, nil, asOf, true)[0].State; got != "attended" {
		t.Fatalf("listening without keyboard input is still observed attendance: %s", got)
	}
}

func TestMobileBriefAttendanceUnknownFromRecordedActivity(t *testing.T) {
	event, activity, asOf := attendanceFixture(t, "unknown")
	observations := inferMobileBriefAttendance([]calendar.Event{event}, activity, nil, nil, asOf, true)
	if len(observations) != 1 || observations[0].State != "unknown" || observations[0].Reason != "computer_away" {
		t.Fatalf("attendance = %+v", observations)
	}
}

func TestMobileBriefAttendanceUncertaintyAndPrivacy(t *testing.T) {
	event, attended, asOf := attendanceFixture(t, "attended")
	_, active, _ := attendanceFixture(t, "skipped")
	type uncertaintyCase struct {
		name      string
		event     calendar.Event
		activity  []mobileBriefAttendanceActivity
		gaps      []mobileBriefAttendanceGap
		pauses    []observationPause
		available bool
		asOf      time.Time
	}
	tests := []uncertaintyCase{
		{name: "capture off", event: event, available: true, asOf: asOf},
		{name: "store unavailable", event: event, activity: attended, asOf: asOf},
		{name: "pause inside slot", event: event, activity: attended, pauses: []observationPause{{Start: event.Start.Add(time.Minute).UnixMilli(), End: event.Start.Add(2 * time.Minute).UnixMilli()}}, available: true, asOf: asOf},
		{name: "protected overlap", event: event, activity: attended, gaps: []mobileBriefAttendanceGap{{Start: event.Start.Add(time.Minute).UnixMilli(), End: event.End.UnixMilli()}}, available: true, asOf: asOf},
		{name: "unfinished", event: event, activity: attended, available: true, asOf: event.End.Add(-time.Minute)},
		{name: "brief app visit", event: event, activity: attended[:1], available: true, asOf: asOf},
	}
	inPerson := event
	inPerson.ConferenceURL, inPerson.ConferenceHost, inPerson.Location = "", "", "Office"
	tests = append(tests, uncertaintyCase{name: "meeting elsewhere", event: inPerson, activity: active, available: true, asOf: asOf})
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := inferMobileBriefAttendance([]calendar.Event{test.event}, test.activity, test.gaps, test.pauses, test.asOf, test.available)[0]
			if got.State != "unknown" || (test.name == "brief app visit") != (len(got.SourceSessionIDs) > 0) {
				t.Fatalf("uncertain source = %+v", got)
			}
		})
	}
	t.Run("same platform overlap", func(t *testing.T) {
		other := event
		other.ID = "other"
		for _, got := range inferMobileBriefAttendance([]calendar.Event{event, other}, attended, nil, nil, asOf, true) {
			if got.State != "unknown" || got.Reason != "ambiguous_calendar_slots" {
				t.Fatalf("overlap = %+v", got)
			}
		}
	})
	t.Run("unrelated audio", func(t *testing.T) {
		rows := append([]mobileBriefAttendanceActivity(nil), active...)
		rows = append(rows, mobileBriefAttendanceActivity{Event: activitymon.ActivityEvent{Timestamp: event.Start.UnixMilli(), Kind: activitymon.EventDeviceInUse, Device: "microphone", InUse: true}})
		if got := inferMobileBriefAttendance([]calendar.Event{event}, rows, nil, nil, asOf, true)[0].State; got != "unknown" {
			t.Fatalf("microphone does not prove attendance or absence: %s", got)
		}
	})
	t.Run("sensitive app", func(t *testing.T) {
		prior := pii.GetUserSensitiveApps()
		t.Cleanup(func() { pii.SetUserSensitiveApps(prior) })
		pii.SetUserSensitiveApps(map[string]string{"us.zoom.xos": "Zoom"})
		if got := inferMobileBriefAttendance([]calendar.Event{event}, attended, nil, nil, asOf, true)[0].State; got != "unknown" {
			t.Fatalf("sensitive attendance = %s", got)
		}
	})
	t.Run("private window", func(t *testing.T) {
		rows := append([]mobileBriefAttendanceActivity(nil), attended...)
		rows[0].Event.PrivateWindow = true
		if got := inferMobileBriefAttendance([]calendar.Event{event}, rows, nil, nil, asOf, true)[0].State; got != "unknown" {
			t.Fatalf("private attendance = %s", got)
		}
	})
}

func TestMobileBriefAttendancePersistsAndRechecksSources(t *testing.T) {
	event, activity, asOf := attendanceFixture(t, "attended")
	app, state := newLinkTestApp(t)
	store := newTestStore(t)
	app.activity = &activityMonitorState{store: store}
	storeEvent(t, state, event)
	insertTestSession(t, store, "fixture-session", "pending", event.Start.Add(-time.Minute).UnixMilli())
	if _, err := store.db.Exec(`DELETE FROM activity_events WHERE session_id = ?`, "fixture-session"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE activity_sessions SET end_time = ? WHERE id = ?`, event.End.Add(time.Minute).UnixMilli(), "fixture-session"); err != nil {
		t.Fatal(err)
	}
	for i, item := range activity {
		raw, err := json.Marshal(item.Event)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.db.Exec(`INSERT INTO activity_events (session_id,timestamp,kind,data,sort_order) VALUES(?,?,?,?,?)`, item.SessionID, item.Event.Timestamp, string(item.Event.Kind), string(raw), i); err != nil {
			t.Fatal(err)
		}
	}
	in := mobileBriefInput{AsOf: asOf, Location: time.UTC}
	collectMobileBriefAttendance(app, &in)
	if len(in.Attendance) != 1 || in.Attendance[0].State != "attended" {
		t.Fatalf("collected = %+v", in.Attendance)
	}
	key := mobileBriefAttendanceKeyPrefix + event.Start.Format("2006-01-02")
	raw, err := state.GetUIState(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, `"state":"attended"`) || !strings.Contains(raw, "fixture-session") {
		t.Fatalf("projection missing evidence: %s", raw)
	}
	for _, withheld := range []string{event.Title, "Synthetic fixture", "Zoom Meeting", "speechText"} {
		if strings.Contains(raw, withheld) {
			t.Fatalf("projection retained raw content %q", withheld)
		}
	}
	if _, err := store.db.Exec(`UPDATE activity_sessions SET triage_status = 'protected' WHERE id = ?`, "fixture-session"); err != nil {
		t.Fatal(err)
	}
	collectMobileBriefAttendance(app, &in)
	if got := in.Attendance[0].State; got != "unknown" {
		t.Fatalf("protected source resurrected from cache: %s", got)
	}
	if _, err := store.db.Exec(`DELETE FROM activity_events WHERE session_id = ?`, "fixture-session"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE activity_sessions SET triage_status = 'pending' WHERE id = ?`, "fixture-session"); err != nil {
		t.Fatal(err)
	}
	collectMobileBriefAttendance(app, &in)
	if got := in.Attendance[0].State; got != "unknown" {
		t.Fatalf("purged source resurrected from cache: %s", got)
	}
	if err := state.SetUIState(context.Background(), observationPausesKey, "broken"); err != nil {
		t.Fatal(err)
	}
	collectMobileBriefAttendance(app, &in)
	if got := in.Attendance[0].Reason; got != "evidence_unavailable" {
		t.Fatalf("pause-log failure = %s", got)
	}
}

func TestMobileBriefAttendanceWireUsesBoundedEvidence(t *testing.T) {
	event, activity, asOf := attendanceFixture(t, "attended")
	in := mobileBriefInput{AsOf: asOf, Location: time.UTC, Attendance: inferMobileBriefAttendance([]calendar.Event{event}, activity, nil, nil, asOf, true)}
	brief := buildMobileBriefV1(in)
	if len(brief.MeetingAttendance) != 1 {
		t.Fatalf("wire = %+v", brief.MeetingAttendance)
	}
	item := brief.MeetingAttendance[0]
	if item.EventID != event.ID || item.Title != mobileBriefMeetingName(event) || item.Scope != "computer" || len(item.Refs) != 1 {
		t.Fatalf("wire = %+v", item)
	}
	if len(brief.Evidence) != 1 || brief.Evidence[0].SourceKind != mobileBriefAttendanceSource || brief.Evidence[0].Ref != item.Refs[0] {
		t.Fatalf("evidence = %+v", brief.Evidence)
	}
	raw, err := json.Marshal(brief)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"meetingAttendance"`) || strings.Contains(string(raw), "fixture-session") || strings.Contains(string(raw), "speechText") {
		t.Fatalf("wire shape = %s", raw)
	}
	for len(in.Attendance) < mobileBriefAttendanceLimit+5 {
		copy := in.Attendance[0]
		copy.EventID = strings.Repeat("x", len(in.Attendance)+1)
		in.Attendance = append(in.Attendance, copy)
	}
	if got := len(buildMobileBriefV1(in).MeetingAttendance); got != mobileBriefAttendanceLimit {
		t.Fatalf("attendance cap = %d", got)
	}
}

func TestMobileBriefAttendanceKeepsStateAcrossMidnight(t *testing.T) {
	event, activity, asOf := attendanceFixture(t, "skipped")
	activity = attendanceWithoutDevices(activity)
	start := time.Date(event.Start.Year(), event.Start.Month(), event.Start.Day(), 0, 0, 0, 0, time.UTC)
	for _, device := range []string{"microphone", "camera"} {
		activity = append(activity, mobileBriefAttendanceActivity{Event: activitymon.ActivityEvent{Timestamp: start.Add(-2 * time.Minute).UnixMilli(), Kind: activitymon.EventDeviceInUse, Device: device}})
	}
	tests := []struct {
		name string
		seed activitymon.ActivityEvent
	}{
		{name: "microphone", seed: activitymon.ActivityEvent{Kind: activitymon.EventDeviceInUse, Device: "microphone", InUse: true}},
		{name: "camera", seed: activitymon.ActivityEvent{Kind: activitymon.EventDeviceInUse, Device: "camera", InUse: true}},
		{name: "sleep", seed: activitymon.ActivityEvent{Kind: activitymon.EventSessionState, StateName: "sleep"}},
		{name: "lock", seed: activitymon.ActivityEvent{Kind: activitymon.EventSessionState, StateName: "locked"}},
		{name: "idle", seed: activitymon.ActivityEvent{Kind: activitymon.EventSessionState, StateName: "idle"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newTestStore(t)
			insertTestSession(t, store, "prior-day", "pending", start.Add(-time.Hour).UnixMilli())
			seed := test.seed
			seed.Timestamp = start.Add(-time.Minute).UnixMilli()
			raw, err := json.Marshal(seed)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.db.Exec(`INSERT INTO activity_events (session_id,timestamp,kind,data,sort_order) VALUES(?,?,?,?,?)`, "prior-day", seed.Timestamp, string(seed.Kind), string(raw), 1); err != nil {
				t.Fatal(err)
			}
			seeded, _, err := loadMobileBriefAttendanceActivity(store, start, asOf)
			if err != nil {
				t.Fatal(err)
			}
			rows := append(seeded, activity...)
			got := inferMobileBriefAttendance([]calendar.Event{event}, rows, nil, nil, asOf, true)[0]
			if got.State != "unknown" {
				t.Fatalf("prior-day %s state lost: %+v", test.name, got)
			}
		})
	}
}

func TestMobileBriefAttendanceIndependentDeviceStates(t *testing.T) {
	event, activity, asOf := attendanceFixture(t, "skipped")
	activity = attendanceWithoutDevices(activity)
	activity = append(activity,
		mobileBriefAttendanceActivity{Event: activitymon.ActivityEvent{Timestamp: event.Start.Add(-2 * time.Minute).UnixMilli(), Kind: activitymon.EventDeviceInUse, Device: "camera", InUse: true}},
		mobileBriefAttendanceActivity{Event: activitymon.ActivityEvent{Timestamp: event.Start.Add(-time.Minute).UnixMilli(), Kind: activitymon.EventDeviceInUse, Device: "microphone", InUse: false}},
	)
	if got := inferMobileBriefAttendance([]calendar.Event{event}, activity, nil, nil, asOf, true)[0].State; got != "unknown" {
		t.Fatalf("mic stopped while camera remained on: %s", got)
	}
}

func attendanceWithoutDevices(activity []mobileBriefAttendanceActivity) []mobileBriefAttendanceActivity {
	var out []mobileBriefAttendanceActivity
	for _, item := range activity {
		if item.Event.Kind != activitymon.EventDeviceInUse {
			out = append(out, item)
		}
	}
	return out
}

func TestMobileBriefAttendanceProtectedSeedCannotRestoreOlderState(t *testing.T) {
	event, _, asOf := attendanceFixture(t, "skipped")
	start := time.Date(event.Start.Year(), event.Start.Month(), event.Start.Day(), 0, 0, 0, 0, time.UTC)
	store := newTestStore(t)
	for i, status := range []string{"pending", "protected"} {
		id := "seed-" + status
		at := start.Add(time.Duration(i-3) * time.Hour)
		insertTestSession(t, store, id, status, at.UnixMilli())
		if _, err := store.db.Exec(`UPDATE activity_sessions SET end_time = ? WHERE id = ?`, at.Add(time.Minute).UnixMilli(), id); err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(activitymon.ActivityEvent{Timestamp: at.UnixMilli(), Kind: activitymon.EventDeviceInUse, Device: "microphone", InUse: i == 1})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.db.Exec(`INSERT INTO activity_events (session_id,timestamp,kind,data,sort_order) VALUES(?,?,?,?,?)`, id, at.UnixMilli(), "device_in_use", string(raw), 1); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := loadMobileBriefAttendanceActivity(store, start, asOf); err == nil {
		t.Fatal("protected latest state must not revive an older device-off observation")
	}
}

func TestMobileBriefAttendanceEarlyWakeDoesNotEraseGap(t *testing.T) {
	event, activity, asOf := attendanceFixture(t, "skipped")
	activity = activity[1:]
	activity = append(activity,
		mobileBriefAttendanceActivity{Event: activitymon.ActivityEvent{Timestamp: event.Start.Add(-time.Minute).UnixMilli(), Kind: activitymon.EventSessionState, StateName: "sleep"}},
		mobileBriefAttendanceActivity{Event: activitymon.ActivityEvent{Timestamp: event.Start.Add(time.Minute).UnixMilli(), Kind: activitymon.EventSessionState, StateName: "wake"}},
	)
	got := inferMobileBriefAttendance([]calendar.Event{event}, activity, nil, nil, asOf, true)[0]
	if got.State != "unknown" || got.Reason != "computer_away" {
		t.Fatalf("wake erased the absence at the slot start: %+v", got)
	}
}
