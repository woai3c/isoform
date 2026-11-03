package main

import (
	"testing"
	"time"

	"wails-gui/internal/engagement"
)

func briefTimingFixtureTime(t *testing.T, loc *time.Location, date, clock string) time.Time {
	t.Helper()
	if clock == "" {
		return time.Time{}
	}
	at, err := time.ParseInLocation("2006-01-02 15:04", date+" "+clock, loc)
	if err != nil {
		t.Fatal(err)
	}
	return at
}

func briefTimingFixtureDays(t *testing.T, day time.Time, opens, phones, starts, ends []string) []mobileBriefTimingDay {
	t.Helper()
	n := max(len(opens), len(phones), len(starts), len(ends))
	out := make([]mobileBriefTimingDay, 0, n)
	for i := 0; i < n; i++ {
		date := day.AddDate(0, 0, -i-1).Format("2006-01-02")
		at := func(clocks []string) time.Time {
			if i >= len(clocks) {
				return time.Time{}
			}
			return briefTimingFixtureTime(t, day.Location(), date, clocks[i])
		}
		out = append(out, mobileBriefTimingDay{
			Date: date, FirstOpen: at(opens), FirstPhone: at(phones),
			FirstActivity: at(starts), LastActivity: at(ends),
		})
	}
	return out
}

func briefTimingPredictionForSlot(t *testing.T, predictions []mobileBriefTimingPrediction, slot string) mobileBriefTimingPrediction {
	t.Helper()
	var got mobileBriefTimingPrediction
	count := 0
	for _, prediction := range predictions {
		if prediction.Slot == slot {
			got = prediction
			count++
		}
	}
	if count != 1 {
		t.Fatalf("predictions contain %d %s slots, want exactly one: %+v", count, slot, predictions)
	}
	return got
}

func assertBriefTimingPrediction(t *testing.T, got mobileBriefTimingPrediction, want time.Time, reason string) {
	t.Helper()
	// A fixed slot is predicted to start mobileBriefBuildLead before its time.
	if reason == "fixed_slot" {
		want = want.Add(-mobileBriefBuildLead)
	}
	if !got.ScheduledAt.Equal(want) || got.Reason != reason {
		t.Fatalf("prediction = %s (%s), want %s (%s)", got.ScheduledAt, got.Reason, want, reason)
	}
	if !got.ScheduledAt.IsZero() && got.ScheduledAt.After(got.Deadline) {
		t.Fatalf("prediction %s exceeds deadline %s", got.ScheduledAt, got.Deadline)
	}
}

func TestMobileBriefPredictMorningFixtureDays(t *testing.T) {
	day := briefTimingFixtureTime(t, time.UTC, "2026-10-17", "12:00")
	settings := mobileBriefSettings{Enabled: true, Morning: "09:00", Evening: "20:00"}
	cases := []struct {
		name                  string
		opens, phones, starts []string
		ends                  []string
		want, reason          string
	}{
		{name: "no history", want: "09:00", reason: "fixed_slot"},
		{name: "two days are insufficient", opens: []string{"07:00", "07:10"}, want: "09:00", reason: "fixed_slot"},
		{name: "median rejects a late outlier", opens: []string{"07:10", "11:30", "07:20"}, want: "07:10", reason: "learned_opens"},
		{name: "even median averages central observations", opens: []string{"11:00", "08:00", "07:00", "08:20"}, want: "08:00", reason: "learned_opens"},
		{name: "fixed time is a ceiling", opens: []string{"10:00", "10:30", "11:00"}, want: "09:00", reason: "fixed_slot"},
		{name: "opens precede phone presence and activity", opens: []string{"08:00", "08:00", "08:00"}, phones: []string{"07:00", "07:00", "07:00"}, starts: []string{"06:00", "06:00", "06:00"}, ends: []string{"17:00", "17:00", "17:00"}, want: "07:50", reason: "learned_opens"},
		{name: "sparse opens fall back to phone", opens: []string{"08:00", "08:00"}, phones: []string{"07:00", "07:00", "07:00"}, starts: []string{"06:00", "06:00", "06:00"}, ends: []string{"17:00", "17:00", "17:00"}, want: "06:50", reason: "learned_presence"},
		{name: "sparse phone falls back to sustained activity", phones: []string{"07:00", "07:00"}, starts: []string{"06:00", "06:00", "06:00"}, ends: []string{"17:00", "17:00", "17:00"}, want: "05:50", reason: "learned_activity"},
		{name: "brief wake is not sustained work", starts: []string{"06:00", "06:00", "06:00"}, ends: []string{"06:04", "06:04", "06:04"}, want: "09:00", reason: "fixed_slot"},
		{name: "five minutes qualifies as activity", starts: []string{"06:00", "06:00", "06:00"}, ends: []string{"06:05", "06:05", "06:05"}, want: "05:50", reason: "learned_activity"},
		{name: "afternoon opens do not teach a morning", opens: []string{"12:00", "13:00", "14:00"}, want: "09:00", reason: "fixed_slot"},
		{name: "lead never crosses midnight", opens: []string{"00:05", "00:05", "00:05"}, want: "00:00", reason: "learned_opens"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := mobileBriefTimingInput{Days: briefTimingFixtureDays(t, day, tc.opens, tc.phones, tc.starts, tc.ends)}
			got := briefTimingPredictionForSlot(t, mobileBriefPredictDay(day, time.UTC, settings, input), mobileBriefSlotMorning)
			assertBriefTimingPrediction(t, got, briefTimingFixtureTime(t, time.UTC, "2026-10-17", tc.want), tc.reason)
		})
	}
}

func TestMobileBriefPredictHistoryWindowAndDistinctDays(t *testing.T) {
	day := briefTimingFixtureTime(t, time.UTC, "2026-10-17", "12:00")
	settings := mobileBriefSettings{Enabled: true, Morning: "09:00", Evening: "20:00"}
	sample := func(date, clock string) mobileBriefTimingDay {
		return mobileBriefTimingDay{Date: date, FirstOpen: briefTimingFixtureTime(t, time.UTC, date, clock)}
	}
	cases := []struct {
		name         string
		days         []mobileBriefTimingDay
		want, reason string
	}{
		{name: "fourteenth day included", days: []mobileBriefTimingDay{sample("2026-10-03", "07:00"), sample("2026-10-04", "07:10"), sample("2026-10-16", "07:20")}, want: "07:00", reason: "learned_opens"},
		{name: "older today and future excluded", days: []mobileBriefTimingDay{sample("2026-10-02", "06:00"), sample("2026-10-15", "07:00"), sample("2026-10-16", "07:20"), sample("2026-10-17", "06:00"), sample("2026-10-18", "06:00")}, want: "09:00", reason: "fixed_slot"},
		{name: "duplicate days do not establish confidence", days: []mobileBriefTimingDay{sample("2026-10-15", "07:00"), sample("2026-10-16", "07:20"), sample("2026-10-16", "07:20")}, want: "09:00", reason: "fixed_slot"},
		{name: "timestamp must belong to fixture date", days: []mobileBriefTimingDay{sample("2026-10-15", "07:00"), sample("2026-10-16", "07:20"), {Date: "2026-10-14", FirstOpen: briefTimingFixtureTime(t, time.UTC, "2026-10-13", "06:00")}}, want: "09:00", reason: "fixed_slot"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := briefTimingPredictionForSlot(t, mobileBriefPredictDay(day, time.UTC, settings, mobileBriefTimingInput{Days: tc.days}), mobileBriefSlotMorning)
			assertBriefTimingPrediction(t, got, briefTimingFixtureTime(t, time.UTC, "2026-10-17", tc.want), tc.reason)
		})
	}
}

func TestMobileBriefPredictCurrentMeeting(t *testing.T) {
	day := briefTimingFixtureTime(t, time.UTC, "2026-10-17", "12:00")
	settings := mobileBriefSettings{Enabled: true, Morning: "09:00", Evening: "20:00"}
	cases := []struct {
		name, date, meetingDate, meeting, want, reason string
	}{
		{"before first meeting", "2026-10-17", "2026-10-17", "07:30", "07:20", "before_meeting"},
		{"later meeting leaves learned target", "2026-10-17", "2026-10-17", "10:30", "07:50", "learned_opens"},
		{"another day projection ignored", "2026-10-18", "2026-10-18", "07:30", "07:50", "learned_opens"},
		{"stale timestamp ignored", "2026-10-17", "2026-10-16", "07:30", "07:50", "learned_opens"},
		{"midnight meeting lead stays today", "2026-10-17", "2026-10-17", "00:05", "00:00", "before_meeting"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := mobileBriefTimingInput{
				Days:  briefTimingFixtureDays(t, day, []string{"08:00", "08:00", "08:00"}, nil, nil, nil),
				Today: mobileBriefTimingDay{Date: tc.date, FirstMeeting: briefTimingFixtureTime(t, time.UTC, tc.meetingDate, tc.meeting)},
			}
			got := briefTimingPredictionForSlot(t, mobileBriefPredictDay(day, time.UTC, settings, input), mobileBriefSlotMorning)
			assertBriefTimingPrediction(t, got, briefTimingFixtureTime(t, time.UTC, "2026-10-17", tc.want), tc.reason)
		})
	}
}

func TestMobileBriefPredictEveningFixtureDays(t *testing.T) {
	day := briefTimingFixtureTime(t, time.UTC, "2026-10-17", "12:00")
	settings := mobileBriefSettings{Enabled: true, Morning: "09:00", Evening: "18:00"}
	cases := []struct {
		name                      string
		starts, ends, meetingEnds []string
		lastActivity, lastMeeting string
		todayDate                 string
		want, reason              string
	}{
		{name: "median plus settling", ends: []string{"16:00", "16:30", "23:00"}, want: "17:00", reason: "work_settled"},
		{name: "calendar end extends historical work", ends: []string{"15:00", "15:00", "15:00"}, meetingEnds: []string{"16:30", "16:30", "16:30"}, want: "17:00", reason: "work_settled"},
		{name: "two workdays are insufficient", ends: []string{"16:00", "16:00"}, want: "18:00", reason: "fixed_slot"},
		{name: "learned evening is capped at fixed slot", ends: []string{"19:00", "19:00", "19:00"}, want: "18:00", reason: "fixed_slot"},
		{name: "morning activity cannot teach an evening", ends: []string{"11:00", "11:00", "11:00"}, want: "18:00", reason: "fixed_slot"},
		{name: "brief late wakes do not teach an evening", starts: []string{"16:00", "16:00", "16:00"}, ends: []string{"16:04", "16:04", "16:04"}, want: "18:00", reason: "fixed_slot"},
		{name: "missing activity start cannot teach an evening", starts: []string{"", "", ""}, ends: []string{"16:00", "16:00", "16:00"}, want: "18:00", reason: "fixed_slot"},
		{name: "current work cannot delay the configured target", lastActivity: "17:45", want: "18:00", reason: "fixed_slot"},
		{name: "current meeting cannot delay the configured target", lastActivity: "17:00", lastMeeting: "18:00", want: "18:00", reason: "fixed_slot"},
		{name: "activity at the configured time does not move the target", lastActivity: "19:00", want: "18:00", reason: "fixed_slot"},
		{name: "work after the configured time does not move the target", lastActivity: "19:01", want: "18:00", reason: "fixed_slot"},
		{name: "future work timestamp ignored", lastActivity: "18:30", todayDate: "2026-10-18", want: "18:00", reason: "fixed_slot"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			starts := tc.starts
			if starts == nil {
				starts = make([]string, len(tc.ends))
				for i := range starts {
					starts[i] = "09:00"
				}
			}
			days := briefTimingFixtureDays(t, day, nil, nil, starts, tc.ends)
			for i, clock := range tc.meetingEnds {
				days[i].LastMeeting = briefTimingFixtureTime(t, time.UTC, days[i].Date, clock)
			}
			date := tc.todayDate
			if date == "" {
				date = "2026-10-17"
			}
			input := mobileBriefTimingInput{Days: days, Today: mobileBriefTimingDay{
				Date: "2026-10-17", LastActivity: briefTimingFixtureTime(t, time.UTC, date, tc.lastActivity),
				LastMeeting: briefTimingFixtureTime(t, time.UTC, date, tc.lastMeeting),
			}}
			got := briefTimingPredictionForSlot(t, mobileBriefPredictDay(day, time.UTC, settings, input), mobileBriefSlotEvening)
			assertBriefTimingPrediction(t, got, briefTimingFixtureTime(t, time.UTC, "2026-10-17", tc.want), tc.reason)
			if !got.Deadline.Equal(time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC)) {
				t.Fatalf("deadline = %s, want midnight: the evening is made until its day ends", got.Deadline)
			}
		})
	}
}

func TestMobileBriefTimingQuietClockBoundaries(t *testing.T) {
	cases := []struct {
		name        string
		start, end  time.Duration
		clock       string
		quiet       bool
		endDateTime string
	}{
		{"disabled", 7 * time.Hour, 7 * time.Hour, "07:00", false, "2026-10-17 07:00"},
		{"nonwrapping before start", 7 * time.Hour, 8 * time.Hour, "06:59", false, "2026-10-17 06:59"},
		{"nonwrapping start included", 7 * time.Hour, 8 * time.Hour, "07:00", true, "2026-10-17 08:00"},
		{"nonwrapping end excluded", 7 * time.Hour, 8 * time.Hour, "08:00", false, "2026-10-17 08:00"},
		{"wrapping evening", 22 * time.Hour, 7 * time.Hour, "23:00", true, "2026-10-18 07:00"},
		{"wrapping morning", 22 * time.Hour, 7 * time.Hour, "06:00", true, "2026-10-17 07:00"},
		{"wrapping daytime", 22 * time.Hour, 7 * time.Hour, "12:00", false, "2026-10-17 12:00"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			at := briefTimingFixtureTime(t, time.UTC, "2026-10-17", tc.clock)
			quiet := engagement.QuietWindow{Start: tc.start, End: tc.end}
			if got := mobileBriefTimingQuiet(at, quiet); got != tc.quiet {
				t.Fatalf("quiet = %v, want %v", got, tc.quiet)
			}
			if got := mobileBriefTimingQuietEnd(at, quiet).Format("2006-01-02 15:04"); got != tc.endDateTime {
				t.Fatalf("quiet end = %s, want %s", got, tc.endDateTime)
			}
		})
	}
}

func TestMobileBriefPredictDoesNotDelayConfiguredSlotsForQuietHours(t *testing.T) {
	day := briefTimingFixtureTime(t, time.UTC, "2026-10-17", "12:00")
	cases := []struct {
		name, morning, evening, slot string
		quiet                        engagement.QuietWindow
		want, reason                 string
	}{
		{"morning inside wrapping quiet hours", "07:00", "18:00", mobileBriefSlotMorning, engagement.QuietWindow{Start: 22 * time.Hour, End: 7*time.Hour + 30*time.Minute}, "07:00", "fixed_slot"},
		{"morning inside daytime quiet hours", "07:00", "18:00", mobileBriefSlotMorning, engagement.QuietWindow{Start: 6*time.Hour + 30*time.Minute, End: 7*time.Hour + 30*time.Minute}, "07:00", "fixed_slot"},
		{"evening inside quiet hours", "07:00", "18:00", mobileBriefSlotEvening, engagement.QuietWindow{Start: 17 * time.Hour, End: 18*time.Hour + 30*time.Minute}, "18:00", "fixed_slot"},
		{"late configured slot inside wrapping quiet hours", "07:00", "23:30", mobileBriefSlotEvening, engagement.QuietWindow{Start: 23 * time.Hour, End: time.Hour}, "23:30", "fixed_slot"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			settings := mobileBriefSettings{Enabled: true, Morning: tc.morning, Evening: tc.evening}
			got := briefTimingPredictionForSlot(t, mobileBriefPredictDay(day, time.UTC, settings, mobileBriefTimingInput{Quiet: tc.quiet}), tc.slot)
			assertBriefTimingPrediction(t, got, briefTimingFixtureTime(t, time.UTC, "2026-10-17", tc.want), tc.reason)
		})
	}
}

func TestMobileBriefPredictLocalClockAcrossDST(t *testing.T) {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	for _, date := range []string{"2026-03-08", "2026-11-01"} {
		t.Run(date, func(t *testing.T) {
			day := briefTimingFixtureTime(t, loc, date, "12:00")
			days := briefTimingFixtureDays(t, day, []string{"08:10", "08:10", "08:10"}, nil, nil, nil)
			// Sources may encode UTC; learning must use their local clock and day.
			for i := range days {
				days[i].FirstOpen = days[i].FirstOpen.UTC()
			}
			settings := mobileBriefSettings{Enabled: true, Morning: "09:00", Evening: "18:00"}
			got := briefTimingPredictionForSlot(t, mobileBriefPredictDay(day.UTC(), loc, settings, mobileBriefTimingInput{Days: days}), mobileBriefSlotMorning)
			assertBriefTimingPrediction(t, got, briefTimingFixtureTime(t, loc, date, "08:00"), "learned_opens")
			if got.BriefID != date+"-"+mobileBriefSlotMorning {
				t.Fatalf("brief ID = %s, want local-day ID", got.BriefID)
			}
			quiet := engagement.QuietWindow{Start: 7 * time.Hour, End: 8 * time.Hour}
			at := briefTimingFixtureTime(t, loc, date, "07:00")
			if !mobileBriefTimingQuiet(at, quiet) || !mobileBriefTimingQuietEnd(at, quiet).Equal(briefTimingFixtureTime(t, loc, date, "08:00")) {
				t.Fatalf("quiet hours must follow wall clock on DST day: %s", at)
			}
		})
	}
}

func TestMobileBriefTimingQuietEndAcrossDSTGapAndFold(t *testing.T) {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, at, want string
		quiet          engagement.QuietWindow
	}{
		{
			name: "missing quiet end advances through spring gap",
			at:   "2026-03-08T01:45:00-08:00", want: "2026-03-08T03:00:00-07:00",
			quiet: engagement.QuietWindow{Start: 22 * time.Hour, End: 2*time.Hour + 30*time.Minute},
		},
		{
			name: "repeated quiet end uses current fall occurrence",
			at:   "2026-11-01T01:15:00-08:00", want: "2026-11-01T01:30:00-08:00",
			quiet: engagement.QuietWindow{Start: time.Hour, End: time.Hour + 30*time.Minute},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			at, err := time.Parse(time.RFC3339, tc.at)
			if err != nil {
				t.Fatal(err)
			}
			want, err := time.Parse(time.RFC3339, tc.want)
			if err != nil {
				t.Fatal(err)
			}
			got := mobileBriefTimingQuietEnd(at.In(loc), tc.quiet)
			if !got.Equal(want) || mobileBriefTimingQuiet(got, tc.quiet) {
				t.Fatalf("quiet end = %s, want first permitted time %s", got, want)
			}
		})
	}
}

func TestMobileBriefPredictStableIdentityAfterScheduleChanges(t *testing.T) {
	loc := time.FixedZone("UTC+14", 14*60*60)
	day := briefTimingFixtureTime(t, loc, "2026-10-17", "00:30")
	input := mobileBriefTimingInput{Days: briefTimingFixtureDays(t, day, []string{"07:30", "07:30", "07:30"}, nil, nil, nil)}
	first := mobileBriefPredictDay(day.UTC(), loc, mobileBriefSettings{Enabled: true, Morning: "09:00", Evening: "18:00"}, input)
	second := mobileBriefPredictDay(day.UTC(), loc, mobileBriefSettings{Enabled: true, Morning: "06:00", Evening: "19:00"}, input)
	for _, slot := range []string{mobileBriefSlotMorning, mobileBriefSlotEvening} {
		a := briefTimingPredictionForSlot(t, first, slot)
		b := briefTimingPredictionForSlot(t, second, slot)
		if a.BriefID != "2026-10-17-"+slot || a.BriefID != b.BriefID {
			t.Fatalf("schedule change changed slot identity: %s -> %s", a.BriefID, b.BriefID)
		}
	}
}
