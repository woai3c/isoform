package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"wails-gui/internal/calendar"
	"wails-gui/internal/meetingdetect"
	"wails-gui/internal/platform/activitymon"
	"wails-gui/storage"
)

func TestApplyActivityMark(t *testing.T) {
	loc := time.UTC
	day := time.Date(2026, 10, 14, 8, 30, 0, 0, loc)
	var marks contextDayMarks
	if !applyActivityMark(&marks, day, true, time.Time{}, loc) {
		t.Fatal("first activity changes the marks")
	}
	if marks.Date != "2026-10-14" || marks.FirstActive != day.UnixMilli() || marks.LastActive != day.UnixMilli() {
		t.Fatalf("marks %+v", marks)
	}
	// Present again: only the last mark moves.
	applyActivityMark(&marks, day.Add(time.Hour), true, time.Time{}, loc)
	if marks.FirstActive != day.UnixMilli() || marks.LastActive != day.Add(time.Hour).UnixMilli() {
		t.Fatalf("marks %+v", marks)
	}
	// Away since 09:40 (noticed at 09:55): a break opens at 09:40.
	awayAt := day.Add(85 * time.Minute)
	applyActivityMark(&marks, awayAt, false, awayAt.Add(-15*time.Minute), loc)
	if marks.openBreak != awayAt.Add(-15*time.Minute).UnixMilli() || len(marks.Breaks) != 0 {
		t.Fatalf("open break %+v", marks)
	}
	// Still away: nothing changes.
	if applyActivityMark(&marks, awayAt.Add(5*time.Minute), false, time.Time{}, loc) {
		t.Fatal("a second away mark changes nothing")
	}
	// Back at 10:30: the break closes.
	back := day.Add(2 * time.Hour)
	applyActivityMark(&marks, back, true, time.Time{}, loc)
	if len(marks.Breaks) != 1 || marks.BreakMinutes() != 50 || marks.openBreak != 0 || marks.LastActive != back.UnixMilli() {
		t.Fatalf("closed break %+v minutes %d", marks, marks.BreakMinutes())
	}
	// A break never starts before the last activity.
	applyActivityMark(&marks, back.Add(20*time.Minute), false, back.Add(-time.Hour), loc)
	if marks.openBreak != back.UnixMilli() {
		t.Fatalf("break start clamped to last activity: %+v", marks)
	}
	// A new day resets everything.
	next := day.Add(24 * time.Hour)
	applyActivityMark(&marks, next, true, time.Time{}, loc)
	if marks.Date != "2026-10-15" || len(marks.Breaks) != 0 || marks.FirstActive != next.UnixMilli() {
		t.Fatalf("new day %+v", marks)
	}
}

func TestMeetingSignalFromDevices(t *testing.T) {
	now := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	live := prepEvent("ev", "Standup", now, 30, "Zoom", 2)
	zoom := activitymon.RunningApp{Name: "zoom.us", BundleID: "us.zoom.xos", PID: 10, IsActive: true}
	backgroundZoom := activitymon.RunningApp{Name: "zoom.us", BundleID: "us.zoom.xos", PID: 10}
	teams := activitymon.RunningApp{Name: "Microsoft Teams", BundleID: "com.microsoft.teams2", PID: 13, IsActive: true}
	facetime := activitymon.RunningApp{Name: "FaceTime", BundleID: "com.apple.FaceTime", PID: 14, IsActive: true}
	chrome := activitymon.RunningApp{Name: "Google Chrome", BundleID: "com.google.Chrome", PID: 11, IsActive: true}
	backgroundChrome := activitymon.RunningApp{Name: "Google Chrome", BundleID: "com.google.Chrome", PID: 11}
	finder := activitymon.RunningApp{Name: "Finder", BundleID: "com.apple.finder", PID: 12}

	if _, ok := meetingSignalFromDevices(true, false, false, nil, []activitymon.RunningApp{zoom}); ok {
		t.Fatal("no live event, no signal")
	}
	if _, ok := meetingSignalFromDevices(false, false, false, &live, []activitymon.RunningApp{zoom}); ok {
		t.Fatal("no device in use, no signal")
	}
	if _, ok := meetingSignalFromDevices(true, false, true, &live, []activitymon.RunningApp{zoom}); ok {
		t.Fatal("Isoform's own listening explains the microphone")
	}
	if got, ok := meetingSignalFromDevices(false, true, true, &live, []activitymon.RunningApp{zoom}); !ok || got.MeetingName != "Zoom" || got.BundleID != "us.zoom.xos" {
		t.Fatalf("the camera counts even while listening: %+v %v", got, ok)
	}
	if got, ok := meetingSignalFromDevices(true, false, false, &live, []activitymon.RunningApp{finder, zoom}); !ok || got.MeetingName != "Zoom" {
		t.Fatalf("running Zoom: %+v %v", got, ok)
	}
	meet := prepEvent("meet", "Design review", now, 30, "Google Meet", 1)
	if got, ok := meetingSignalFromDevices(true, false, false, &meet, []activitymon.RunningApp{finder, chrome}); !ok || got.MeetingName != "Google Meet" || got.BundleID != "com.google.Chrome" {
		t.Fatalf("Meet in a browser: %+v %v", got, ok)
	}
	if _, ok := meetingSignalFromDevices(true, false, false, &meet, []activitymon.RunningApp{finder}); ok {
		t.Fatal("no meeting app and no browser, no signal")
	}
	if _, ok := meetingSignalFromDevices(true, false, false, &meet, []activitymon.RunningApp{finder, backgroundChrome}); ok {
		t.Fatal("a browser in the background must not start a recording")
	}
	solo := prepEvent("solo", "Focus", now, 30, "", 0)
	if _, ok := meetingSignalFromDevices(true, true, false, &solo, []activitymon.RunningApp{zoom}); ok {
		t.Fatal("an event without people or a link is not a meeting")
	}
	inPerson := prepEvent("room", "Room sync", now, 30, "", 3)
	if _, ok := meetingSignalFromDevices(true, true, false, &inPerson, []activitymon.RunningApp{zoom}); ok {
		t.Fatal("an in-person event (no link) never starts a recording")
	}
	if _, ok := meetingSignalFromDevices(true, false, false, &live, []activitymon.RunningApp{finder, backgroundZoom}); ok {
		t.Fatal("Zoom in the background does not count")
	}
	if _, ok := meetingSignalFromDevices(true, false, false, &live, []activitymon.RunningApp{teams}); ok {
		t.Fatal("a different meeting app in front than the event's link does not count")
	}
	if _, ok := meetingSignalFromDevices(true, true, false, &live, []activitymon.RunningApp{facetime}); ok {
		t.Fatal("a FaceTime call during a Zoom event is not that meeting")
	}
}

func TestContextSignalServiceEventsAndMarks(t *testing.T) {
	st, err := storage.New(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	now := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	idle := time.Duration(0)
	mic, camera := false, false
	power := activitymon.PowerState{OnBattery: false, Percent: 100, Known: true}
	var emitted []activitymon.ActivityEvent
	wakes, meetings := 0, 0
	live := prepEvent("ev", "Standup", now, 30, "Zoom", 2)
	svc := &contextSignalService{
		store:  st,
		now:    func() time.Time { return now },
		loc:    time.UTC,
		idle:   func() time.Duration { return idle },
		mic:    func() bool { return mic },
		camera: func() bool { return camera },
		power:  func() activitymon.PowerState { return power },
		locked: func() bool { return false },
		running: func() []activitymon.RunningApp {
			return []activitymon.RunningApp{{Name: "zoom.us", BundleID: "us.zoom.xos", IsActive: true}}
		},
		emit:         func(evt activitymon.ActivityEvent) { emitted = append(emitted, evt) },
		onWake:       func() { wakes++ },
		onMeeting:    func(meetingdetect.Result) { meetings++ },
		liveEvent:    func(time.Time) *calendar.Event { return &live },
		ownListening: func() bool { return false },
	}
	ctx := context.Background()

	svc.pollIdle(ctx)
	marks, ok := loadContextDayMarks(ctx, st, "2026-10-14")
	if !ok || marks.FirstActive != now.UnixMilli() {
		t.Fatalf("first activity stored: %+v %v", marks, ok)
	}
	if len(emitted) != 0 {
		t.Fatalf("no event while present: %+v", emitted)
	}

	// Away for six minutes: one idle event, no break yet (under fifteen).
	now = now.Add(6 * time.Minute)
	idle = 6 * time.Minute
	svc.pollIdle(ctx)
	if len(emitted) != 1 || emitted[0].Kind != activitymon.EventSessionState || emitted[0].StateName != "idle" {
		t.Fatalf("idle event %+v", emitted)
	}
	svc.pollIdle(ctx)
	if len(emitted) != 1 {
		t.Fatalf("idle reported once: %+v", emitted)
	}
	// Twenty minutes idle: a break opens; back: it closes.
	now = now.Add(14 * time.Minute)
	idle = 20 * time.Minute
	svc.pollIdle(ctx)
	now = now.Add(time.Minute)
	idle = 0
	svc.pollIdle(ctx)
	marks, _ = loadContextDayMarks(ctx, st, "2026-10-14")
	if len(marks.Breaks) != 1 || marks.BreakMinutes() != 21 || marks.LastActive != now.UnixMilli() {
		t.Fatalf("break %+v (%d min)", marks, marks.BreakMinutes())
	}
	if emitted[len(emitted)-1].StateName != "active" {
		t.Fatalf("back event %+v", emitted[len(emitted)-1])
	}

	// The camera turns on during the live Zoom event: a device event and a
	// meeting confirmation.
	camera = true
	svc.pollDevices()
	last := emitted[len(emitted)-1]
	if last.Kind != activitymon.EventDeviceInUse || last.Device != "camera" || !last.InUse || meetings != 1 {
		t.Fatalf("camera event %+v meetings %d", last, meetings)
	}
	count := len(emitted)
	svc.pollDevices()
	if meetings != 1 || len(emitted) != count {
		t.Fatal("no repeat while the camera stays on")
	}
	camera = false
	svc.pollDevices()
	if got := emitted[len(emitted)-1]; got.Device != "camera" || got.InUse {
		t.Fatalf("camera off event %+v", got)
	}

	// Lock, then unlock: state events, a break, and a wake hook on unlock.
	svc.handleChange(ctx, activitymon.SystemStateChange{Kind: activitymon.SystemStateLocked, At: now.Add(time.Minute)})
	if !svc.snapshot().Locked || emitted[len(emitted)-1].StateName != "locked" {
		t.Fatalf("locked %+v", svc.snapshot())
	}
	svc.handleChange(ctx, activitymon.SystemStateChange{Kind: activitymon.SystemStateUnlocked, At: now.Add(31 * time.Minute)})
	if svc.snapshot().Locked || wakes != 1 {
		t.Fatalf("unlocked %+v wakes %d", svc.snapshot(), wakes)
	}
	marks, _ = loadContextDayMarks(ctx, st, "2026-10-14")
	if len(marks.Breaks) != 2 || marks.BreakMinutes() != 51 {
		t.Fatalf("lock break %+v (%d min)", marks, marks.BreakMinutes())
	}

	// Power: only a change is announced.
	svc.pollPower()
	before := len(emitted)
	power = activitymon.PowerState{OnBattery: true, Percent: 80, Known: true}
	svc.pollPower()
	if len(emitted) != before+1 || emitted[len(emitted)-1].StateName != "battery" {
		t.Fatalf("battery event %+v", emitted[len(emitted)-1])
	}
	svc.pollPower()
	if len(emitted) != before+1 {
		t.Fatal("no repeat while on battery")
	}
}

func TestContextSignalInitialDeviceStates(t *testing.T) {
	for _, test := range []struct {
		name string
		mic  bool
	}{
		{name: "microphone already on", mic: true},
		{name: "both devices off"},
	} {
		t.Run(test.name, func(t *testing.T) {
			now := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
			var emitted []activitymon.ActivityEvent
			meetings := 0
			live := prepEvent("ev", "Standup", now, 30, "Zoom", 2)
			svc := &contextSignalService{
				now:       func() time.Time { return now },
				mic:       func() bool { return test.mic },
				camera:    func() bool { return false },
				observing: func() bool { return true },
				emit:      func(evt activitymon.ActivityEvent) { emitted = append(emitted, evt) },
				onMeeting: func(meetingdetect.Result) { meetings++ },
				liveEvent: func(time.Time) *calendar.Event { return &live },
				running: func() []activitymon.RunningApp {
					return []activitymon.RunningApp{{Name: "zoom.us", BundleID: "us.zoom.xos", IsActive: true}}
				},
				// The service reads these probes at startup before its first poll.
				state: contextState{MicInUse: test.mic},
			}
			svc.pollDevices()
			if len(emitted) != 2 {
				t.Fatalf("initial device snapshots = %+v", emitted)
			}
			for i, device := range []string{"microphone", "camera"} {
				wantInUse := device == "microphone" && test.mic
				wantDetail := "The " + device + " is not in use"
				if wantInUse {
					wantDetail = "The " + device + " is in use"
				}
				got := emitted[i]
				if got.Kind != activitymon.EventDeviceInUse || got.Timestamp != now.UnixMilli() || got.Device != device || got.InUse != wantInUse || got.Detail != wantDetail {
					t.Fatalf("initial %s snapshot = %+v", device, got)
				}
			}
			now = now.Add(contextDevicePoll)
			svc.pollDevices()
			if len(emitted) != 2 || meetings != 0 {
				t.Fatalf("snapshots repeated or confirmed a meeting: %+v, meetings=%d", emitted, meetings)
			}
		})
	}
}

func TestContextSignalDeviceStatesAfterObservingResumes(t *testing.T) {
	now := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	observing, mic, camera := true, false, true
	var recorded []activitymon.ActivityEvent
	svc := &contextSignalService{
		now:       func() time.Time { return now },
		mic:       func() bool { return mic },
		camera:    func() bool { return camera },
		observing: func() bool { return observing },
		state:     contextState{CameraInUse: camera},
		emit: func(evt activitymon.ActivityEvent) {
			// The real recording sink drops events while observing is paused.
			if observing {
				recorded = append(recorded, evt)
			}
		},
	}
	svc.pollDevices()
	if len(recorded) != 2 {
		t.Fatalf("initial snapshots = %+v", recorded)
	}
	observing, mic, camera = false, true, false
	now = now.Add(contextDevicePoll)
	svc.pollDevices()
	if len(recorded) != 2 {
		t.Fatalf("paused changes were recorded: %+v", recorded)
	}
	observing = true
	now = now.Add(contextDevicePoll)
	svc.pollDevices()
	if len(recorded) != 4 {
		t.Fatalf("resumed current states = %+v", recorded)
	}
	if got := recorded[2]; got.Device != "microphone" || !got.InUse || got.Detail != "The microphone is in use" || got.Timestamp != now.UnixMilli() {
		t.Fatalf("resumed microphone state = %+v", got)
	}
	if got := recorded[3]; got.Device != "camera" || got.InUse || got.Detail != "The camera is not in use" || got.Timestamp != now.UnixMilli() {
		t.Fatalf("resumed camera state = %+v", got)
	}
	now = now.Add(contextDevicePoll)
	svc.pollDevices()
	if len(recorded) != 4 {
		t.Fatalf("unchanged resumed states repeated: %+v", recorded)
	}
	mic = false
	svc.pollDevices()
	if len(recorded) != 5 || recorded[4].Detail != "The microphone stopped being used" {
		t.Fatalf("ordinary transition changed: %+v", recorded)
	}
}
