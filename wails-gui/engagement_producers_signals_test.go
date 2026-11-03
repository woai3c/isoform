package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/engagement"
	"wails-gui/internal/identity"
	"wails-gui/storage"
)

const engagementSignalsDay = 24 * time.Hour

// engagementSignalsApp wires an App to the engagement rig's store and service.
func engagementSignalsApp(t *testing.T) (*App, *engagementTestRig) {
	t.Helper()
	rig := newEngagementTestRig(t)
	return &App{store: rig.store, engagement: rig.svc}, rig
}

// engagementPendingOfKind lists the pending moments of one kind.
func engagementPendingOfKind(t *testing.T, rig *engagementTestRig, kind engagement.Kind) []storage.EngagementMomentRow {
	t.Helper()
	rows, err := rig.store.ListEngagementMoments(context.Background(), storage.EngagementStatePending, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []storage.EngagementMomentRow
	for _, row := range rows {
		if row.Kind == string(kind) {
			out = append(out, row)
		}
	}
	return out
}

func TestEngagementCaptureSnapshotOffNeedsAllThreeToHold(t *testing.T) {
	cases := []struct {
		name string
		snap captureSnapshot
		want bool
	}{
		{"healthy", captureSnapshot{Granted: true, Enabled: true, Running: true}, false},
		{"permission missing", captureSnapshot{Granted: false, Enabled: true, Running: true}, true},
		{"user toggle off", captureSnapshot{Granted: true, Enabled: false, Running: true}, true},
		{"monitor not running", captureSnapshot{Granted: true, Enabled: true, Running: false}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.snap.off(); got != tc.want {
				t.Fatalf("off() = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestEngagementCaptureSnapshotsFollowThePlatform(t *testing.T) {
	enabled := func(string) bool { return true }
	stopped := func(string) bool { return false }
	kinds := func(snaps []captureSnapshot) []string {
		out := make([]string, 0, len(snaps))
		for _, snap := range snaps {
			out = append(out, snap.Kind)
		}
		return out
	}
	linux := []PermissionItem{{Key: "screen_capture", Granted: true}, {Key: "input_automation", Granted: true}}
	if got := engagementCaptureSnapshots("linux", linux, enabled, stopped); len(got) != 0 {
		t.Fatalf("linux must watch nothing, got %v", kinds(got))
	}
	darwin := []PermissionItem{{Key: "screen_recording", Granted: true}, {Key: "microphone", Granted: true}}
	if got := kinds(engagementCaptureSnapshots("darwin", darwin, enabled, stopped)); strings.Join(got, ",") != "screen,microphone" {
		t.Fatalf("darwin without system audio should watch screen and microphone, got %v", got)
	}
	windows := []PermissionItem{{Key: "screen_capture", Granted: true}, {Key: "microphone", Granted: true}}
	if got := kinds(engagementCaptureSnapshots("windows", windows, enabled, stopped)); strings.Join(got, ",") != "screen,microphone" {
		t.Fatalf("windows should map screen_capture to screen, got %v", got)
	}
	// Each source reads its own switch.
	toggles := map[string]bool{"screen_recording": true, "microphone": false}
	got := engagementCaptureSnapshots("darwin", darwin, func(key string) bool { return toggles[key] }, stopped)
	if len(got) != 2 || !got[0].Enabled || got[1].Enabled {
		t.Fatalf("per-source toggles not applied: %+v", got)
	}
}

func TestEngagementCaptureOutageFiresOnceAfterFifteenMinutes(t *testing.T) {
	ctx := context.Background()
	app, rig := engagementSignalsApp(t)
	tracker := newEngagementCaptureTracker()
	start := rig.clock
	sample := func(after time.Duration, running bool) {
		rig.clock = start.Add(after)
		app.observeCaptureHealth(ctx, tracker, rig.clock, func() []captureSnapshot {
			return []captureSnapshot{{Kind: "screen", Label: "Screen capture", Granted: true, Enabled: true, Running: running}}
		})
	}

	// The source was on once, so going off counts as an outage.
	sample(-5*time.Minute, true)
	for _, after := range []time.Duration{0, 5 * time.Minute, 10 * time.Minute, 15 * time.Minute} {
		sample(after, false)
	}
	if got := engagementPendingOfKind(t, rig, engagement.KindCapturePaused); len(got) != 0 {
		t.Fatalf("no moment may fire up to fifteen minutes, got %d", len(got))
	}

	sample(20*time.Minute, false)
	got := engagementPendingOfKind(t, rig, engagement.KindCapturePaused)
	if len(got) != 1 {
		t.Fatalf("expected one capture_paused moment at twenty minutes, got %d", len(got))
	}
	wantKey := "capture_paused:screen:" + start.Format(time.RFC3339)
	if got[0].DedupeKey != wantKey || got[0].SubjectID != "screen" || got[0].CreatedAt != start.UnixMilli() {
		t.Fatalf("moment = %+v, want key %q created at the outage start", got[0], wantKey)
	}
	if !strings.Contains(got[0].Params, `"what":"Screen capture"`) || !strings.Contains(got[0].Params, `"duration":"20 minutes"`) {
		t.Fatalf("params = %s", got[0].Params)
	}

	// Once the moment is sent, the same outage must not be announced again.
	if err := rig.store.SetEngagementMomentState(ctx, got[0].ID, storage.EngagementStateSent, "test",
		storage.EngagementMomentRow{SentAt: rig.clock.UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	sample(25*time.Minute, false)
	if n := len(engagementPendingOfKind(t, rig, engagement.KindCapturePaused)); n != 0 {
		t.Fatalf("a sent outage must not be announced again, got %d pending", n)
	}

	// Capture resumes, then goes off again: the new outage starts at resume-off.
	sample(30*time.Minute, true)
	sample(31*time.Minute, false)
	sample(46*time.Minute, false)
	if n := len(engagementPendingOfKind(t, rig, engagement.KindCapturePaused)); n != 0 {
		t.Fatalf("fifteen minutes into the new outage is not yet enough, got %d", n)
	}
	sample(47*time.Minute, false)
	got = engagementPendingOfKind(t, rig, engagement.KindCapturePaused)
	wantSecond := "capture_paused:screen:" + start.Add(31*time.Minute).Format(time.RFC3339)
	if len(got) != 1 || got[0].DedupeKey != wantSecond {
		t.Fatalf("second outage moment = %+v, want key %q", got, wantSecond)
	}
}

func TestEngagementCaptureClosedEyesNeverCounts(t *testing.T) {
	ctx := context.Background()
	app, rig := engagementSignalsApp(t)
	tracker := newEngagementCaptureTracker()
	start := rig.clock
	off := func() []captureSnapshot {
		return []captureSnapshot{{Kind: "screen", Label: "Screen capture", Granted: true, Enabled: true, Running: false}}
	}
	on := func() []captureSnapshot {
		return []captureSnapshot{{Kind: "screen", Label: "Screen capture", Granted: true, Enabled: true, Running: true}}
	}
	app.observeCaptureHealth(ctx, tracker, start.Add(-5*time.Minute), on)
	app.observeCaptureHealth(ctx, tracker, start, off)

	app.closedEyes.enabled.Store(true)
	paused := func() []captureSnapshot {
		t.Error("capture must not be sampled while closed-eyes mode is on")
		return nil
	}
	for after := 5 * time.Minute; after <= 40*time.Minute; after += 5 * time.Minute {
		app.observeCaptureHealth(ctx, tracker, start.Add(after), paused)
	}
	if n := len(engagementPendingOfKind(t, rig, engagement.KindCapturePaused)); n != 0 {
		t.Fatalf("closed-eyes time must not produce a moment, got %d", n)
	}

	app.closedEyes.enabled.Store(false)
	resumed := start.Add(41 * time.Minute)
	app.observeCaptureHealth(ctx, tracker, resumed, off)
	app.observeCaptureHealth(ctx, tracker, resumed.Add(15*time.Minute), off)
	if n := len(engagementPendingOfKind(t, rig, engagement.KindCapturePaused)); n != 0 {
		t.Fatalf("the clock restarts when closed-eyes ends, got %d at fifteen minutes", n)
	}
	app.observeCaptureHealth(ctx, tracker, resumed.Add(16*time.Minute), off)
	got := engagementPendingOfKind(t, rig, engagement.KindCapturePaused)
	wantKey := "capture_paused:screen:" + resumed.Format(time.RFC3339)
	if len(got) != 1 || got[0].DedupeKey != wantKey {
		t.Fatalf("moment after closed-eyes = %+v, want key %q", got, wantKey)
	}
}

func TestEngagementCaptureNeverOnIsOnboardingNotAnOutage(t *testing.T) {
	ctx := context.Background()
	app, rig := engagementSignalsApp(t)
	tracker := newEngagementCaptureTracker()
	start := rig.clock
	missing := func() []captureSnapshot {
		return []captureSnapshot{{Kind: "screen", Label: "Screen capture", Granted: false, Enabled: true, Running: false}}
	}
	for after := time.Duration(0); after <= 60*time.Minute; after += 5 * time.Minute {
		app.observeCaptureHealth(ctx, tracker, start.Add(after), missing)
	}
	if n := len(engagementPendingOfKind(t, rig, engagement.KindCapturePaused)); n != 0 {
		t.Fatalf("a permission that was never granted must not count as an outage, got %d", n)
	}
}

func TestEngagementDurationAndDueText(t *testing.T) {
	la, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	durations := []struct {
		d    time.Duration
		want string
	}{
		{20 * time.Minute, "20 minutes"},
		{60 * time.Minute, "1 hour"},
		{150 * time.Minute, "2 hours"},
	}
	for _, tc := range durations {
		if got := engagementDurationText(tc.d); got != tc.want {
			t.Fatalf("engagementDurationText(%s) = %q, want %q", tc.d, got, tc.want)
		}
	}
	now := time.Date(2026, 10, 12, 10, 0, 0, 0, la)
	dues := []struct {
		due  time.Time
		want string
	}{
		{time.Date(2026, 10, 12, 23, 59, 0, 0, la), "today"},
		{time.Date(2026, 10, 13, 23, 59, 0, 0, la), "tomorrow"},
		{time.Date(2026, 10, 15, 23, 59, 0, 0, la), "on Oct 15"},
		{time.Date(2026, 10, 11, 23, 59, 0, 0, la), "on Oct 11"},
	}
	for _, tc := range dues {
		if got := engagementDueText(tc.due, now); got != tc.want {
			t.Fatalf("engagementDueText(%s) = %q, want %q", tc.due, got, tc.want)
		}
	}
}

func TestEngagementParseDueAcceptsOnlyDatesOrRFC3339(t *testing.T) {
	la, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := parseEngagementDue("2026-10-14", la); !ok || !got.Equal(time.Date(2026, 10, 14, 23, 59, 59, 0, la)) {
		t.Fatalf("a bare day should mean the end of that local day, got %v %t", got, ok)
	}
	if got, ok := parseEngagementDue("2026-10-14T17:00:00-07:00", la); !ok || !got.Equal(time.Date(2026, 10, 14, 17, 0, 0, 0, la)) {
		t.Fatalf("RFC 3339 should be used as written, got %v %t", got, ok)
	}
	for _, value := range []string{"Oct 14", "this week", ""} {
		if _, ok := parseEngagementDue(value, la); ok {
			t.Fatalf("%q is not a due time the producer can rely on", value)
		}
	}
}

func TestEngagementOneLineKeepsTheFirstLineAndCapsLength(t *testing.T) {
	if got := engagementOneLine("  You reply to Sam.\nSecond line.  ", 200); got != "You reply to Sam." {
		t.Fatalf("first line = %q", got)
	}
	long := strings.Repeat("a", 250)
	got := engagementOneLine(long, 200)
	if len([]rune(got)) != 203 || !strings.HasSuffix(got, "...") {
		t.Fatalf("long text should be capped with an ASCII ellipsis, got %d runes", len([]rune(got)))
	}
}

// purposeSignalDetail builds a current Purpose statement for the selection tests.
func purposeSignalDetail(kind, dueKind, dueValue string, created time.Time, eligible bool, evidence ...PurposeEvidenceRecord) PurposeOpenLoopDetail {
	return PurposeOpenLoopDetail{
		OpenLoop: PurposeOpenLoopRecord{ID: "loop-1", Kind: kind, CreatedAt: created.UnixMilli()},
		Current: PurposeOpenLoopEventRecord{
			Title:          "Send the Q3 deck",
			DueKind:        dueKind,
			DueValue:       dueValue,
			LifecycleState: PurposeStateActive,
			SupportState:   PurposeSupportSupported,
		},
		Evidence: evidence,
		Eligible: eligible,
	}
}

func purposeSignalEvidence(stance string, observedAt time.Time) PurposeEvidenceRecord {
	return PurposeEvidenceRecord{Stance: stance, SourceObservedAt: observedAt.UnixMilli()}
}

func TestEngagementPurposeSignalsSelectCommitmentsAndGoals(t *testing.T) {
	la, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	at := func(month time.Month, day, hour, minute int) time.Time {
		return time.Date(2026, month, day, hour, minute, 0, 0, la)
	}
	created := at(10, 8, 9, 0)
	cases := []struct {
		name    string
		detail  PurposeOpenLoopDetail
		now     time.Time
		wantKey string
		wantDue string
	}{
		{
			name:   "due in three days waits",
			detail: purposeSignalDetail(PurposeKindCommitment, PurposeDueDate, "2026-10-14", created, true),
			now:    at(10, 11, 9, 0),
		},
		{
			name:    "due today fires",
			detail:  purposeSignalDetail(PurposeKindCommitment, PurposeDueDate, "2026-10-11", created, true),
			now:     at(10, 11, 9, 0),
			wantKey: "commitment_slipping:loop-1:2026-10-11",
			wantDue: "today",
		},
		{
			name:    "due tomorrow fires inside the day before",
			detail:  purposeSignalDetail(PurposeKindCommitment, PurposeDueDate, "2026-10-12T17:00:00-07:00", created, true),
			now:     at(10, 11, 18, 0),
			wantKey: "commitment_slipping:loop-1:2026-10-12",
			wantDue: "tomorrow",
		},
		{
			name:   "due tomorrow waits when more than a day out",
			detail: purposeSignalDetail(PurposeKindCommitment, PurposeDueDate, "2026-10-12T17:00:00-07:00", created, true),
			now:    at(10, 11, 9, 0),
		},
		{
			name:    "past due within a day still fires",
			detail:  purposeSignalDetail(PurposeKindCommitment, PurposeDueDate, "2026-10-09", created, true),
			now:     at(10, 10, 12, 0),
			wantKey: "commitment_slipping:loop-1:2026-10-09",
			wantDue: "on Oct 9",
		},
		{
			name:   "past due for more than a day expires",
			detail: purposeSignalDetail(PurposeKindCommitment, PurposeDueDate, "2026-10-09", created, true),
			now:    at(10, 11, 9, 0),
		},
		{
			name: "update evidence after creation counts as progress",
			detail: purposeSignalDetail(PurposeKindCommitment, PurposeDueDate, "2026-10-11", created, true,
				purposeSignalEvidence(PurposeStanceUpdate, at(10, 10, 15, 0))),
			now: at(10, 11, 9, 0),
		},
		{
			name: "support evidence is not progress",
			detail: purposeSignalDetail(PurposeKindCommitment, PurposeDueDate, "2026-10-11", created, true,
				purposeSignalEvidence(PurposeStanceSupport, at(10, 10, 15, 0))),
			now:     at(10, 11, 9, 0),
			wantKey: "commitment_slipping:loop-1:2026-10-11",
			wantDue: "today",
		},
		{
			name:   "an unparseable due value waits",
			detail: purposeSignalDetail(PurposeKindCommitment, PurposeDueDate, "Oct 11", created, true),
			now:    at(10, 11, 9, 0),
		},
		{
			name:   "a window due never slips",
			detail: purposeSignalDetail(PurposeKindCommitment, PurposeDueWindow, "This week", created, true),
			now:    at(10, 11, 9, 0),
		},
		{
			name:   "responsibilities never slip",
			detail: purposeSignalDetail(PurposeKindResponsibility, PurposeDueDate, "2026-10-11", created, true),
			now:    at(10, 11, 9, 0),
		},
		{
			name:   "a statement that is not current is skipped",
			detail: purposeSignalDetail(PurposeKindCommitment, PurposeDueDate, "2026-10-11", created, false),
			now:    at(10, 11, 9, 0),
		},
		{
			name:    "a goal idle for a week gets a check-in",
			detail:  purposeSignalDetail(PurposeKindGoal, PurposeDueUnknown, "", created, true),
			now:     at(10, 15, 9, 0),
			wantKey: "goal_check_in:loop-1:" + engagement.WeekKey(at(10, 15, 9, 0)),
		},
		{
			name: "a goal touched this week waits",
			detail: purposeSignalDetail(PurposeKindGoal, PurposeDueUnknown, "", created, true,
				purposeSignalEvidence(PurposeStanceUpdate, at(10, 12, 9, 0))),
			now: at(10, 15, 9, 0),
		},
		{
			name: "a goal touched exactly a week ago gets a check-in",
			detail: purposeSignalDetail(PurposeKindGoal, PurposeDueUnknown, "", created, true,
				purposeSignalEvidence(PurposeStanceUpdate, at(10, 8, 9, 0))),
			now:     at(10, 15, 9, 0),
			wantKey: "goal_check_in:loop-1:" + engagement.WeekKey(at(10, 15, 9, 0)),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			moments := engagementPurposeSignals(tc.detail, tc.now)
			if tc.wantKey == "" {
				if len(moments) != 0 {
					t.Fatalf("expected no moment, got %+v", moments)
				}
				return
			}
			if len(moments) != 1 {
				t.Fatalf("expected one moment, got %+v", moments)
			}
			if moments[0].DedupeKey != tc.wantKey {
				t.Fatalf("dedupe key = %q, want %q", moments[0].DedupeKey, tc.wantKey)
			}
			if tc.wantDue != "" && moments[0].Params["due"] != tc.wantDue {
				t.Fatalf("due = %q, want %q", moments[0].Params["due"], tc.wantDue)
			}
			if tc.wantDue == "" && moments[0].Params["title"] != "Send the Q3 deck" {
				t.Fatalf("title = %q", moments[0].Params["title"])
			}
		})
	}
}

func TestEngagementPurposeCommitmentDueTodayIsSubmittedOnce(t *testing.T) {
	ctx := context.Background()
	app, rig := engagementSignalsApp(t)
	app.activity = &activityMonitorState{store: newTestStore(t)}
	now := time.Now()
	userPurposeForTest(
		t, NewPurposeMapStore(app.activity.store.db),
		"engagement-signals:commitment", PurposeKindCommitment,
		"Send the Q3 deck to Sam", "The Q3 deck is in Sam's inbox.",
		PurposeDueDate, now.Format("2006-01-02"), now.Add(-time.Hour).UnixMilli(),
	)

	app.checkPurposeEngagement(ctx, now)
	app.checkPurposeEngagement(ctx, now)
	got := engagementPendingOfKind(t, rig, engagement.KindCommitmentSlipping)
	if len(got) != 1 {
		t.Fatalf("expected one commitment_slipping moment across two passes, got %d", len(got))
	}
	if !strings.Contains(got[0].Params, `"due":"today"`) || !strings.Contains(got[0].Params, "Send the Q3 deck to Sam") {
		t.Fatalf("params = %s", got[0].Params)
	}
}

func TestEngagementPersonReappearsAfterThirtyDaysWithoutSightings(t *testing.T) {
	ctx := context.Background()
	app, rig := engagementSignalsApp(t)
	priya, err := rig.store.CreatePrincipal(ctx, storage.PrincipalRow{Kind: storage.PrincipalKindContact, DisplayName: "Priya"})
	if err != nil {
		t.Fatal(err)
	}
	stranger, err := rig.store.CreatePrincipal(ctx, storage.PrincipalRow{Kind: storage.PrincipalKindAnonymous, DisplayName: "Unknown sender"})
	if err != nil {
		t.Fatal(err)
	}
	// Detect against the prior last-seen time, then submit after persistence.
	sight := func(id string, at time.Time) {
		t.Helper()
		afterSighting := app.prepareEngagementPersonSighting(ctx, id, "wechat", at.UnixMilli())
		if err := rig.store.BumpPrincipalSighting(ctx, id, "wechat", "", at.UnixMilli(), ""); err != nil {
			t.Fatal(err)
		}
		if afterSighting != nil {
			afterSighting()
		}
	}
	t0 := rig.clock
	sight(priya.ID, t0) // the first sighting is not a reappearance
	sight(priya.ID, t0.Add(29*engagementSignalsDay))
	sight(priya.ID, t0.Add(60*engagementSignalsDay))           // 31 days after the last sighting
	sight(priya.ID, t0.Add(60*engagementSignalsDay+time.Hour)) // same week: no second moment

	got := engagementPendingOfKind(t, rig, engagement.KindPersonReappeared)
	if len(got) != 1 {
		t.Fatalf("expected one person_reappeared moment, got %d", len(got))
	}
	wantKey := "person_reappeared:" + priya.ID + ":" +
		engagement.WeekKey(time.UnixMilli(t0.Add(60*engagementSignalsDay).UnixMilli()))
	if got[0].DedupeKey != wantKey {
		t.Fatalf("dedupe key = %q, want %q", got[0].DedupeKey, wantKey)
	}
	if !strings.Contains(got[0].Params, `"name":"Priya"`) || !strings.Contains(got[0].Params, `"where":"WeChat"`) {
		t.Fatalf("params = %s", got[0].Params)
	}

	sight(stranger.ID, t0)
	sight(stranger.ID, t0.Add(60*engagementSignalsDay))
	if n := len(engagementPendingOfKind(t, rig, engagement.KindPersonReappeared)); n != 1 {
		t.Fatalf("anonymous senders never reappear, got %d moments", n)
	}
}

func TestEngagementSightingHookSubmitsAfterTheBump(t *testing.T) {
	ctx := context.Background()
	app, rig := engagementSignalsApp(t)
	priya, err := rig.store.CreatePrincipal(ctx, storage.PrincipalRow{Kind: storage.PrincipalKindContact, DisplayName: "Priya"})
	if err != nil {
		t.Fatal(err)
	}
	previous := engagementPersonSightingHook
	engagementPersonSightingHook = app.prepareEngagementPersonSighting
	t.Cleanup(func() { engagementPersonSightingHook = previous })

	labeler := newSenderLabeler(rig.store)
	entry := senderLabelCacheEntry{principalID: priya.ID, kind: identity.SubjectContact}
	labeler.recordSighting(ctx, entry, "wechat", "hello", "", rig.clock.UnixMilli())
	labeler.recordSighting(ctx, entry, "wechat", "hello", "", rig.clock.Add(60*engagementSignalsDay).UnixMilli())
	if n := len(engagementPendingOfKind(t, rig, engagement.KindPersonReappeared)); n != 1 {
		t.Fatalf("the sighting path should announce one reappearance, got %d", n)
	}
}

func TestEngagementSelfDreamUpdatesWaitingForReviewSubmitOnce(t *testing.T) {
	ctx := context.Background()
	app, rig := engagementSignalsApp(t)
	created := rig.clock.UnixMilli()
	ops := []SelfDreamOperationRecord{
		{ID: "op-supersede", Operation: SelfDreamOperationSupersede, TargetEntryIDs: []string{"self-1"}, Statement: "Plan the week on Monday.", ValidationStatus: SelfDreamValidationQualified, CreatedAt: created},
		{ID: "op-contradict", Operation: SelfDreamOperationContradict, TargetEntryIDs: []string{"self-2"}, Statement: "Skip Friday reviews.", ValidationStatus: SelfDreamValidationReviewRequired, CreatedAt: created},
		{ID: "op-no-change", Operation: SelfDreamOperationNoChange, ValidationStatus: SelfDreamValidationNoChange, CreatedAt: created},
		{ID: "op-rejected", Operation: SelfDreamOperationSupersede, ValidationStatus: SelfDreamValidationRejected, CreatedAt: created},
		{ID: "op-auto", Operation: SelfDreamOperationMerge, ValidationStatus: SelfDreamValidationQualified, AutoEligible: true, CreatedAt: created},
	}
	// A later run repeats the supersede proposal under a new operation id.
	repeat := ops[0]
	repeat.ID = "op-supersede-repeat"
	repeat.Rationale = "Worded differently by a later run."
	app.engagementSelfDreamOperations(ctx, ops)
	app.engagementSelfDreamOperations(ctx, append(ops, repeat))

	got := engagementPendingOfKind(t, rig, engagement.KindLearnedToConfirm)
	if len(got) != 2 {
		t.Fatalf("expected two review-worthy moments across two passes, got %d", len(got))
	}
	keys := map[string]bool{}
	for _, row := range got {
		keys[row.DedupeKey] = true
	}
	if !keys["learned_to_confirm:"+selfDreamProposalKey(ops[0])] ||
		!keys["learned_to_confirm:"+selfDreamProposalKey(ops[1])] {
		t.Fatalf("keys = %v", keys)
	}
	if !strings.Contains(got[0].Params, "statement") {
		t.Fatalf("params = %s", got[0].Params)
	}
	nilApp := (*App)(nil)
	nilApp.engagementSelfDreamOperations(ctx, ops) // must not panic
}

func TestEngagementSelfDreamFollowsSettingsReviews(t *testing.T) {
	ctx := context.Background()
	rig := newSelfDreamTestRig(t)
	eng := newEngagementTestRig(t)
	now := time.Now().Add(-time.Minute).UnixMilli()
	focus := addSelfDreamTestEntry(t, rig.store, "Prefer morning focus blocks for deep work.", now)
	notes := addSelfDreamTestEntry(t, rig.store, "Keep meeting notes short.", now+1)
	pulls := addSelfDreamTestEntry(t, rig.store, "Review pull requests after lunch.", now+2)
	supersede := func(target SelfModelEntry, statement string) SelfDreamOperationDraft {
		return SelfDreamOperationDraft{
			Operation:       SelfDreamOperationSupersede,
			TargetEntryIDs:  []string{target.ID},
			SelfKind:        selfModelKindPreference,
			Scope:           SelfDreamScope{Kind: "project", Key: "yansu"},
			Statement:       statement,
			ReasoningStatus: "unknown",
			Confidence:      0.6,
			EvidenceRefs:    []string{selfDreamManifestRef(t, rig, target.ID)},
		}
	}
	rig.runner.response = selfDreamModelResponse(t,
		supersede(focus, "Prefer morning focus blocks for deep work and planning."),
		supersede(notes, "Keep meeting notes short and shared."),
		supersede(pulls, "Review pull requests after lunch and before standup."),
	)
	detail, err := rig.app.runSelfDreamShadow(ctx, "scheduled")
	if err != nil {
		t.Fatal(err)
	}
	byTarget := map[string]SelfDreamOperationRecord{}
	for _, op := range detail.Operations {
		if op.ValidationStatus == SelfDreamValidationReviewRequired {
			byTarget[op.TargetEntryIDs[0]] = op
		}
	}
	if len(byTarget) != 3 {
		t.Fatalf("reviewable operations = %+v", detail.Operations)
	}

	// Before anything is announced, one proposal is answered in Settings and
	// another one's target gets a newer revision.
	if _, err := rig.app.ReviewSelfDreamOperation(ReviewSelfDreamOperationInput{
		OperationID:    byTarget[focus.ID].ID,
		IdempotencyKey: "settings-keep-focus",
		Decision:       SelfDreamDecisionKeepSeparate,
	}); err != nil {
		t.Fatal(err)
	}
	notes.Evidence = append(notes.Evidence, SelfModelEvidence{
		SourceKind: "explicit_user_statement",
		SourceID:   "newer-notes-source",
		Statement:  "Keep meeting notes short.",
		Confidence: 1,
		OccurredAt: time.Now().UnixMilli(),
	})
	notes.CreatedAt = time.Now().UnixMilli()
	if _, added, err := rig.store.AddSelfModelEntry(notes); err != nil || !added {
		t.Fatalf("append newer Self revision: added=%v err=%v", added, err)
	}

	rig.app.engagement = eng.svc
	rig.app.store = eng.store
	rig.app.engagementSelfDreamOperations(ctx, detail.Operations)
	pending := engagementPendingOfKind(t, eng, engagement.KindLearnedToConfirm)
	want := engagementSelfDreamDedupeKey(byTarget[pulls.ID])
	if len(pending) != 1 || pending[0].DedupeKey != want {
		t.Fatalf("pending = %+v, want only %s", pending, want)
	}

	// Answering the announced proposal in Settings withdraws its moment.
	if _, err := rig.app.ReviewSelfDreamOperation(ReviewSelfDreamOperationInput{
		OperationID:    byTarget[pulls.ID].ID,
		IdempotencyKey: "settings-more-evidence-pulls",
		Decision:       SelfDreamDecisionNotEnoughEvidence,
	}); err != nil {
		t.Fatal(err)
	}
	if n := len(engagementPendingOfKind(t, eng, engagement.KindLearnedToConfirm)); n != 0 {
		t.Fatalf("answered proposal is still waiting to be announced: %d", n)
	}
	suppressed, err := eng.store.ListEngagementMoments(ctx, storage.EngagementStateSuppressed, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(suppressed) != 1 || suppressed[0].DedupeKey != want {
		t.Fatalf("suppressed = %+v", suppressed)
	}
}

func TestEngagementMemoryDreamPreviewWithProposalsIsReviewable(t *testing.T) {
	ctx := context.Background()
	app, rig := engagementSignalsApp(t)
	preview := &DreamPreview{
		ArchiveProposed:   []DreamArchiveProposal{{ID: "m1"}, {ID: "m2"}},
		SynthesisProposed: []DreamSynthesisProposal{{Title: "merged"}},
	}
	app.engagementMemoryDreamPreview(ctx, false, &DreamRun{ID: "dream-committed", Status: "completed"}, preview)
	app.engagementMemoryDreamPreview(ctx, true, &DreamRun{ID: "dream-failed", Status: "failed"}, preview)
	app.engagementMemoryDreamPreview(ctx, true, &DreamRun{ID: "dream-empty", Status: "completed"}, &DreamPreview{})
	if n := len(engagementPendingOfKind(t, rig, engagement.KindLearnedToConfirm)); n != 0 {
		t.Fatalf("only completed dry runs with proposals count, got %d", n)
	}

	app.engagementMemoryDreamPreview(ctx, true, &DreamRun{ID: "dream-1", Status: "completed"}, preview)
	got := engagementPendingOfKind(t, rig, engagement.KindLearnedToConfirm)
	if len(got) != 1 || got[0].DedupeKey != "learned_to_confirm:dream-1" {
		t.Fatalf("moments = %+v", got)
	}
	if !strings.Contains(got[0].Params, "Isoform found 3 memory cleanups to review.") {
		t.Fatalf("params = %s", got[0].Params)
	}
	var nilApp *App
	nilApp.engagementMemoryDreamPreview(ctx, true, &DreamRun{ID: "dream-2", Status: "completed"}, preview) // must not panic
}
