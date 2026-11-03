package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"wails-gui/internal/calendar"
	"wails-gui/internal/meetingdetect"
	"wails-gui/internal/pii"
	"wails-gui/internal/platform/activitymon"
)

const (
	mobileBriefAttendanceSource      = "meeting_attendance"
	mobileBriefAttendanceLimit       = 32
	mobileBriefAttendanceEventsLimit = 50000
	mobileBriefAttendanceKeyPrefix   = "context_attendance:"
	mobileBriefAttendanceMaxGap      = 2 * time.Minute
)

// This is a disposable observation projection, never a durable fact about a
// person's attendance elsewhere. Titles and raw activity content are not cached.
type mobileBriefAttendanceObservation struct {
	EventID          string    `json:"eventId"`
	Title            string    `json:"-"`
	Start            time.Time `json:"start"`
	End              time.Time `json:"end"`
	State            string    `json:"state"`
	Reason           string    `json:"reason"`
	ObservedAt       time.Time `json:"observedAt"`
	SourceSessionIDs []string  `json:"sourceSessionIds"`
}

type mobileBriefAttendanceActivity struct {
	SessionID string
	Event     activitymon.ActivityEvent
}

type mobileBriefAttendanceGap struct{ Start, End int64 }

type mobileBriefAttendanceDay struct {
	Date         string                             `json:"date"`
	Scope        string                             `json:"scope"`
	Observations []mobileBriefAttendanceObservation `json:"observations"`
}

// collectMobileBriefAttendance always re-derives from readable source rows.
// The daily cache is an output only: a deleted or protected source cannot be
// restored by yesterday's classification or a previous brief generation.
func collectMobileBriefAttendance(a *App, in *mobileBriefInput) {
	if in == nil {
		return
	}
	in.Attendance = nil
	if a == nil || a.store == nil || a.IsClosedEyesModeEnabled() {
		return
	}
	ctx := context.Background()
	loc := in.Location
	if loc == nil {
		loc = time.Local
	}
	now := in.AsOf.In(loc)
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	end := start.AddDate(0, 0, 1)
	events, err := a.listCalendarEvents(ctx, start, end)
	if err != nil {
		return
	}
	pauses, pauseErr := a.observationPauses()
	if pauseErr == nil && pausedAt(pauses, in.AsOf) {
		return
	}
	var activity []mobileBriefAttendanceActivity
	var gaps []mobileBriefAttendanceGap
	var activityErr error
	switch true {
	case a.activity == nil || a.activity.store == nil:
		activityErr = fmt.Errorf("activity store is unavailable")
	default:
		activity, gaps, activityErr = loadMobileBriefAttendanceActivity(a.activity.store, start, in.AsOf)
		if activityErr == nil {
			activity, gaps = filterMobileBriefAttendanceActivity(a, activity, gaps, in.AsOf)
		}
	}
	available := pauseErr == nil && activityErr == nil
	in.Attendance = inferMobileBriefAttendance(events, activity, gaps, pauses, in.AsOf, available)
	projection := mobileBriefAttendanceDay{Date: start.Format("2006-01-02"), Scope: "computer", Observations: in.Attendance}
	if in.deferAttendanceCache {
		in.attendanceCache = &projection
		return
	}
	a.saveMobileBriefAttendanceCache(ctx, projection)
}

func (a *App) saveMobileBriefAttendanceCache(ctx context.Context, projection mobileBriefAttendanceDay) {
	encoded, err := json.Marshal(projection)
	if err != nil {
		return
	}
	if err := a.store.SetUIState(ctx, mobileBriefAttendanceKeyPrefix+projection.Date, string(encoded)); err != nil {
		log.Printf("[mobile-brief] attendance observation could not be stored")
	}
}

// Recheck whole-session provenance, including private windows or sensitive
// apps outside the calendar slot. A withheld state transition also leaves the
// following device state unknown, rather than reviving an older readable one.
func filterMobileBriefAttendanceActivity(a *App, activity []mobileBriefAttendanceActivity, gaps []mobileBriefAttendanceGap, asOf time.Time) ([]mobileBriefAttendanceActivity, []mobileBriefAttendanceGap) {
	allowed := map[string]bool{}
	var out []mobileBriefAttendanceActivity
	for _, item := range activity {
		readable, checked := allowed[item.SessionID]
		if !checked {
			readable = a.activityDependencyAllowsModelRead(item.SessionID, nil)
			allowed[item.SessionID] = readable
		}
		if readable {
			out = append(out, item)
			continue
		}
		end := item.Event.Timestamp + 1
		switch item.Event.Kind {
		case activitymon.EventDeviceInUse, activitymon.EventSessionState:
			end = asOf.UnixMilli()
		}
		gaps = append(gaps, mobileBriefAttendanceGap{Start: item.Event.Timestamp, End: end})
	}
	return out, gaps
}

// Query by event overlap, not session containment: a capture session may cross
// midnight or the brief boundary. Protected sessions are never read. Their
// spans only mark gaps so other activity cannot turn withheld evidence into a
// negative attendance claim. Raw speech content is discarded before inference.
func loadMobileBriefAttendanceActivity(store *ActivityStore, start, asOf time.Time) ([]mobileBriefAttendanceActivity, []mobileBriefAttendanceGap, error) {
	if store == nil || store.db == nil {
		return nil, nil, fmt.Errorf("activity store is unavailable")
	}
	rows, err := store.db.Query(`SELECT e.session_id, e.timestamp, e.data
		FROM activity_events e JOIN activity_sessions s ON e.session_id = s.id
		WHERE e.timestamp >= ? AND e.timestamp < ?`+sqlExcludeProtected+`
		ORDER BY e.timestamp, e.id LIMIT ?`, start.UnixMilli(), asOf.UnixMilli(), mobileBriefAttendanceEventsLimit+1)
	if err != nil {
		return nil, nil, err
	}
	var result []mobileBriefAttendanceActivity
	var gaps []mobileBriefAttendanceGap
	count := 0
	for rows.Next() {
		count++
		var item mobileBriefAttendanceActivity
		var at int64
		var raw string
		if err := rows.Scan(&item.SessionID, &at, &raw); err != nil {
			rows.Close()
			return nil, nil, err
		}
		if count > mobileBriefAttendanceEventsLimit {
			rows.Close()
			return nil, nil, fmt.Errorf("attendance activity limit exceeded")
		}
		if err := json.Unmarshal([]byte(raw), &item.Event); err != nil {
			rows.Close()
			return nil, nil, err
		}
		item.Event.Timestamp = at
		if attendanceEventWithheld(item.Event) {
			gaps = append(gaps, mobileBriefAttendanceGap{Start: at, End: at + 1})
			continue
		}
		item.Event.SpeechText = ""
		item.Event.TypedText = ""
		item.Event.KeyChars = nil
		item.Event.KeyChar = ""
		item.Event.ClipboardPreview = ""
		result = append(result, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, nil, err
	}
	protected, err := store.db.Query(`SELECT s.start_time, s.end_time FROM activity_sessions s
		WHERE s.start_time < ? AND s.end_time > ? AND COALESCE(s.triage_status, '') = 'protected'`, asOf.UnixMilli(), start.UnixMilli())
	if err != nil {
		return nil, nil, err
	}
	defer protected.Close()
	for protected.Next() {
		var gap mobileBriefAttendanceGap
		if err := protected.Scan(&gap.Start, &gap.End); err != nil {
			return nil, nil, err
		}
		gaps = append(gaps, gap)
	}
	err = protected.Err()
	protected.Close()
	if err != nil {
		return nil, nil, err
	}
	// Device and absence state can begin before midnight and remain in effect
	// into today's first meeting. Read the last transition for each independent
	// state rather than treating a missing transition today as "off".
	for _, predicate := range []string{
		`e.kind = 'device_in_use' AND json_extract(e.data, '$.device') = 'microphone'`,
		`e.kind = 'device_in_use' AND json_extract(e.data, '$.device') = 'camera'`,
		`e.kind = 'session_state' AND json_extract(e.data, '$.stateName') IN ('locked','unlocked')`,
		`e.kind = 'session_state' AND json_extract(e.data, '$.stateName') IN ('sleep','wake')`,
		`e.kind = 'session_state' AND json_extract(e.data, '$.stateName') IN ('idle','active')`,
	} {
		var seed mobileBriefAttendanceActivity
		var at int64
		var raw string
		var protected bool
		err := store.db.QueryRow(`SELECT e.session_id, e.timestamp,
			CASE WHEN COALESCE(s.triage_status, '') = 'protected' THEN '' ELSE e.data END,
			COALESCE(s.triage_status, '') = 'protected'
			FROM activity_events e JOIN activity_sessions s ON e.session_id = s.id
			WHERE e.timestamp < ? AND (`+predicate+`)
			ORDER BY e.timestamp DESC, e.id DESC LIMIT 1`, start.UnixMilli()).Scan(&seed.SessionID, &at, &raw, &protected)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		if protected {
			return nil, nil, fmt.Errorf("attendance starting state is protected")
		}
		if err := json.Unmarshal([]byte(raw), &seed.Event); err != nil {
			return nil, nil, err
		}
		seed.Event.Timestamp = at
		if attendanceEventWithheld(seed.Event) {
			return nil, nil, fmt.Errorf("attendance starting state is withheld")
		}
		result = append(result, seed)
	}
	return result, gaps, nil
}

func attendanceEventWithheld(evt activitymon.ActivityEvent) bool {
	_, sensitive := pii.IsSensitiveApp(evt.AppBundleID)
	return sensitive || pii.IsSensitiveAppName(evt.AppName) || evt.PrivateWindow || activitymon.IsPrivateBrowsing(evt.WindowTitle)
}

func inferMobileBriefAttendance(events []calendar.Event, activity []mobileBriefAttendanceActivity, gaps []mobileBriefAttendanceGap, pauses []observationPause, asOf time.Time, available bool) []mobileBriefAttendanceObservation {
	ordered := append([]calendar.Event(nil), events...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Start.Before(ordered[j].Start) })
	observations := make([]mobileBriefAttendanceObservation, 0, min(len(ordered), mobileBriefAttendanceLimit))
	for _, ev := range ordered {
		if strings.TrimSpace(ev.ID) == "" || ev.Status == "canceled" || ev.MyStatus == calendar.StatusDeclined {
			continue
		}
		if len(observations) >= mobileBriefAttendanceLimit {
			break
		}
		observation := mobileBriefAttendanceObservation{EventID: ev.ID, Title: mobileBriefMeetingName(ev), Start: ev.Start, End: ev.End,
			State: "unknown", Reason: "insufficient_observation", ObservedAt: asOf, SourceSessionIDs: []string{}}
		switch true {
		case !ev.End.After(ev.Start) || ev.End.After(asOf):
			observation.Reason = "slot_not_finished"
		case !available:
			observation.Reason = "evidence_unavailable"
		case ev.AllDay || !ev.IsMeetingLike() || ev.ConferenceHost == "" || ev.InPerson():
			observation.Reason = "meeting_elsewhere"
		case attendanceSlotPaused(ev, pauses):
			observation.Reason = "observing_paused"
		case attendanceSlotGap(ev, gaps):
			observation.Reason = "evidence_unavailable"
		case attendanceSlotAmbiguous(ev, ordered):
			observation.Reason = "ambiguous_calendar_slots"
		default:
			inputs := attendanceAfterPause(activity, pauses, ev.Start)
			observation.State, observation.Reason, observation.SourceSessionIDs = classifyMobileBriefAttendance(ev, inputs)
			// Device and session-state seeds contribute too, including transitions
			// before the slot. Retain all consumed sessions for later withdrawal.
			var consumed []mobileBriefAttendanceActivity
			for _, item := range inputs {
				if item.Event.Timestamp < ev.End.UnixMilli() {
					consumed = append(consumed, item)
				}
			}
			observation.SourceSessionIDs = attendanceSourceSessions(consumed)
		}
		observations = append(observations, observation)
	}
	return observations
}

// A device transition before a capture pause cannot establish its state after
// the pause. New context snapshots restore that provenance on resume; older
// recordings remain unknown until they contain a fresh device observation.
func attendanceAfterPause(activity []mobileBriefAttendanceActivity, pauses []observationPause, start time.Time) []mobileBriefAttendanceActivity {
	var after int64
	for _, pause := range pauses {
		if pause.End > after && pause.End <= start.UnixMilli() {
			after = pause.End
		}
	}
	if after == 0 {
		return activity
	}
	out := make([]mobileBriefAttendanceActivity, 0, len(activity))
	for _, item := range activity {
		if item.Event.Kind != activitymon.EventDeviceInUse || item.Event.Timestamp >= after {
			out = append(out, item)
		}
	}
	return out
}

func attendanceSlotPaused(ev calendar.Event, pauses []observationPause) bool {
	for _, pause := range pauses {
		if pause.Start < ev.End.UnixMilli() && (pause.End == 0 || pause.End > ev.Start.UnixMilli()) {
			return true
		}
	}
	return false
}

func attendanceSlotGap(ev calendar.Event, gaps []mobileBriefAttendanceGap) bool {
	for _, gap := range gaps {
		if gap.Start < ev.End.UnixMilli() && gap.End > ev.Start.UnixMilli() {
			return true
		}
	}
	return false
}

func attendanceSlotAmbiguous(ev calendar.Event, events []calendar.Event) bool {
	for _, other := range events {
		if other.ID == ev.ID || other.AllDay || !other.Busy() || !other.IsMeetingLike() {
			continue
		}
		if other.Start.Before(ev.End) && other.End.After(ev.Start) && (other.ConferenceHost == "" || strings.EqualFold(other.ConferenceHost, ev.ConferenceHost)) {
			return true
		}
	}
	return false
}

// A strict matching call must be seen repeatedly for two minutes. Negative
// attendance requires captured input across at least 80% of the slot, with no
// gap over two minutes and no call, device or absence ambiguity. This only
// supports "not seen on this computer", never absence from a meeting elsewhere.
func classifyMobileBriefAttendance(ev calendar.Event, activity []mobileBriefAttendanceActivity) (string, string, []string) {
	ordered := append([]mobileBriefAttendanceActivity(nil), activity...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Event.Timestamp < ordered[j].Event.Timestamp })
	var active, calls []mobileBriefAttendanceActivity
	locked, asleep, idle := false, false, false
	mic, camera, uncertain := false, false, false
	micKnown, cameraKnown := false, false
	for _, item := range ordered {
		evt := item.Event
		if evt.Timestamp >= ev.End.UnixMilli() {
			break
		}
		inside := evt.Timestamp >= ev.Start.UnixMilli()
		if attendanceEventWithheld(evt) {
			if inside {
				return "unknown", "evidence_unavailable", []string{}
			}
			continue
		}
		// A transition back to active/off inside the slot does not erase the
		// preceding absence or device use that overlapped its beginning.
		if evt.Timestamp > ev.Start.UnixMilli() {
			if locked || asleep {
				return "unknown", "computer_away", []string{}
			}
			if mic || camera || idle || !micKnown || !cameraKnown {
				uncertain = true
			}
		}
		switch evt.Kind {
		case activitymon.EventSessionState:
			switch evt.StateName {
			case "locked":
				locked = true
			case "unlocked":
				locked = false
			case "sleep":
				asleep = true
			case "wake":
				asleep = false
			case "idle":
				idle = true
			case "active":
				idle = false
			}
			if inside && (locked || asleep) {
				return "unknown", "computer_away", []string{}
			}
		case activitymon.EventDeviceInUse:
			switch evt.Device {
			case "microphone":
				mic = evt.InUse
				micKnown = true
			case "camera":
				camera = evt.InUse
				cameraKnown = true
			}
		}
		if !inside {
			continue
		}
		if locked || asleep {
			return "unknown", "computer_away", []string{}
		}
		if mic || camera || idle {
			uncertain = true
		}
		call := meetingdetect.IsMeetingEvent(evt.AppBundleID, evt.WindowTitle)
		if evt.Kind == activitymon.EventSpeech && evt.SpeechInCall && !evt.MeetingCandidate && meetingdetect.IsTranscriptionSupportedMeetingName(evt.AppName) {
			call = meetingdetect.Result{IsMeeting: true, MeetingName: evt.AppName}
		}
		if call.IsMeeting {
			uncertain = true
			if strings.EqualFold(call.MeetingName, ev.ConferenceHost) {
				calls = append(calls, item)
			}
			continue
		}
		if evt.Kind == activitymon.EventSpeech && (evt.SpeechInCall || evt.MeetingCandidate || (evt.SpeechSource != "" && evt.SpeechDictationApp == "")) {
			uncertain = true
		}
		switch evt.Kind {
		case activitymon.EventAppSwitch, activitymon.EventFocus, activitymon.EventClick, activitymon.EventKeyboard:
			if evt.AppBundleID != "" || evt.AppName != "" {
				active = append(active, item)
			}
		}
	}
	if attendanceSustainedCall(calls) {
		return "attended", "matching_call_activity", attendanceSourceSessions(calls)
	}
	if uncertain || !micKnown || !cameraKnown || !attendanceContinuousInput(ev, active) {
		return "unknown", "insufficient_observation", []string{}
	}
	return "skipped", "active_elsewhere_on_computer", attendanceSourceSessions(active)
}

func attendanceSustainedCall(calls []mobileBriefAttendanceActivity) bool {
	if len(calls) < 2 {
		return false
	}
	start, previous := calls[0].Event.Timestamp, calls[0].Event.Timestamp
	for _, call := range calls[1:] {
		at := call.Event.Timestamp
		if at-previous > (5 * time.Minute).Milliseconds() {
			start = at
		}
		if at-start >= (2 * time.Minute).Milliseconds() {
			return true
		}
		previous = at
	}
	return false
}

func attendanceContinuousInput(ev calendar.Event, active []mobileBriefAttendanceActivity) bool {
	if len(active) < 3 {
		return false
	}
	gap := mobileBriefAttendanceMaxGap.Milliseconds()
	first, last := active[0].Event.Timestamp, active[len(active)-1].Event.Timestamp
	if first-ev.Start.UnixMilli() > gap || ev.End.UnixMilli()-last > gap {
		return false
	}
	if float64(last-first) < float64(ev.End.Sub(ev.Start).Milliseconds())*0.8 {
		return false
	}
	for i := 1; i < len(active); i++ {
		if active[i].Event.Timestamp-active[i-1].Event.Timestamp > gap {
			return false
		}
	}
	return true
}

func attendanceSourceSessions(activity []mobileBriefAttendanceActivity) []string {
	ids := make([]string, 0, len(activity))
	for _, item := range activity {
		if item.SessionID != "" {
			ids = append(ids, item.SessionID)
		}
	}
	return uniqueSortedStrings(ids)
}

func buildMobileBriefAttendance(builder *mobileBriefBuilder, observations []mobileBriefAttendanceObservation) []MobileBriefAttendance {
	result := []MobileBriefAttendance{}
	for _, observation := range observations {
		if len(result) >= mobileBriefAttendanceLimit {
			break
		}
		switch observation.State {
		case "attended", "skipped", "unknown":
		default:
			continue
		}
		candidates := prepareMobileBriefCandidates([]mobileBriefCandidate{{SourceKind: mobileBriefAttendanceSource,
			SourceID: observation.EventID, Title: mobileBriefWithoutClockTimes(observation.Title), At: observation.ObservedAt}}, mobileBriefNewestFirst)
		if len(candidates) == 0 {
			continue
		}
		ref, ok := builder.refFor(candidates[0])
		if !ok {
			builder.truncated = append(builder.truncated, "meetingAttendance")
			break
		}
		result = append(result, MobileBriefAttendance{EventID: mobileBriefText(candidates[0].SourceID, mobileBriefSourceIDLimit), Title: candidates[0].Title,
			State: observation.State, Reason: observation.Reason, Scope: "computer", Refs: []string{ref},
			sourceSessionIDs: observation.SourceSessionIDs})
	}
	return result
}

// mobileBriefMeetingName names a meeting the way a person would, with the day
// strip's title cleanup (handoff 2026-10-26 §9.1): "1:1 with Priya" for a 1:1
// or a meeting with one other person, otherwise the title without the
// user's own name, the company name, how often it repeats and "Meeting".
// The raw title stays when the cleanup leaves nothing (today-review v1.1.5 #6).
func mobileBriefMeetingName(ev calendar.Event) string {
	short, _, _ := todayEventNames(ev)
	if short == "" {
		return ev.Title
	}
	title := strings.ToLower(ev.Title)
	others := 0
	for _, attendee := range ev.Attendees {
		if !attendee.IsMe {
			others++
		}
	}
	if strings.Contains(title, "1:1") || strings.Contains(title, "1/1") || (todayEventKind(ev) != "personal" && others == 1) {
		return "1:1 with " + short
	}
	return short
}
