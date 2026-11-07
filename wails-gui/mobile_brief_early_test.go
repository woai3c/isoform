package main

import (
	"testing"
	"time"
)

// earlyTestZone is Pacific standard time, the zone the early-brief window is read in.
var earlyTestZone = time.FixedZone("PST", -8*60*60)

func setEarlyClock(t *testing.T, at time.Time) {
	t.Helper()
	previous := mobileBriefClock
	mobileBriefClock = func() time.Time { return at }
	t.Cleanup(func() { mobileBriefClock = previous })
}

func earlyTestSettings() mobileBriefSettings {
	settings := defaultMobileBriefSettings()
	settings.Enabled = true
	return settings
}

// slotFor returns the occurrence of a slot on the day of the given time.
func slotFor(t *testing.T, day time.Time, slot string, settings mobileBriefSettings) mobileBriefSlotOccurrence {
	t.Helper()
	for _, occurrence := range mobileBriefTodaySlots(day, earlyTestZone, settings) {
		if occurrence.Slot == slot {
			return occurrence
		}
	}
	t.Fatalf("no %s slot on %s", slot, day.Format("2006-01-02"))
	return mobileBriefSlotOccurrence{}
}

func aheadBriefFor(t *testing.T, occurrence mobileBriefSlotOccurrence, asOf time.Time) MobileBriefV1 {
	t.Helper()
	return MobileBriefV1{
		Version:         1,
		BriefID:         occurrence.BriefID,
		Slot:            occurrence.Slot,
		LocalDate:       occurrence.BriefID[:10],
		AsOf:            formatMobileBriefTime(asOf),
		NarrativeStatus: "ok",
		Generation: &MobileBriefGeneration{
			Reason:      mobileBriefGenerationMadeAhead,
			ScheduledAt: formatMobileBriefTime(occurrence.ScheduledAt),
			GeneratedAt: formatMobileBriefTime(asOf),
		},
	}
}

// noHeld is a store with no brief at all.
func noHeld(string) (MobileBriefV1, bool) { return MobileBriefV1{}, false }

// The 11:20 PM case (v1.0.1): the evening brief made at its slot does not block the
// early brief for the next morning.
func TestAheadTargetMakesAnEarlyBriefAt1120PMAfterTheEveningBriefWasMadeAtItsSlot(t *testing.T) {
	settings := earlyTestSettings()
	evening := slotFor(t, time.Date(2026, 11, 7, 12, 0, 0, 0, earlyTestZone), mobileBriefSlotEvening, settings)
	eveningBrief := MobileBriefV1{BriefID: evening.BriefID, Slot: evening.Slot, LocalDate: "2026-11-07",
		AsOf: formatMobileBriefTime(evening.ScheduledAt.Add(time.Minute)), NarrativeStatus: "ok"}
	held := func(id string) (MobileBriefV1, bool) {
		if id == evening.BriefID {
			return eveningBrief, true
		}
		return MobileBriefV1{}, false
	}
	now := time.Date(2026, 11, 7, 23, 20, 0, 0, earlyTestZone)
	got, ok := mobileBriefAheadTarget(now, earlyTestZone, settings, held)
	if !ok || got.BriefID[:10] != "2026-11-08" || got.Slot != mobileBriefSlotMorning {
		t.Fatalf("target %+v (%t), want the next morning's slot", got, ok)
	}
}

// The 7 AM case (v1.0.1): with no evening brief, nothing is made at 7 AM for the
// evening slot, which is more than an hour away.
func TestAheadTargetMakesNothingAt7AMForTheEveningSlot(t *testing.T) {
	settings := earlyTestSettings()
	now := time.Date(2026, 11, 7, 7, 0, 0, 0, earlyTestZone)
	if _, ok := mobileBriefAheadTarget(now, earlyTestZone, settings, noHeld); ok {
		t.Fatal("made an early brief at 7 AM for the evening slot")
	}
}

func TestAheadTargetOtherSlotsAreMadeOnlyAnHourAhead(t *testing.T) {
	settings := earlyTestSettings()
	evening := slotFor(t, time.Date(2026, 11, 7, 12, 0, 0, 0, earlyTestZone), mobileBriefSlotEvening, settings)
	if _, ok := mobileBriefAheadTarget(evening.ScheduledAt.Add(-90*time.Minute), earlyTestZone, settings, noHeld); ok {
		t.Fatal("made an early evening brief more than an hour ahead")
	}
	got, ok := mobileBriefAheadTarget(evening.ScheduledAt.Add(-30*time.Minute), earlyTestZone, settings, noHeld)
	if !ok || got.BriefID != evening.BriefID {
		t.Fatalf("target %+v (%t), want the evening slot within the hour", got, ok)
	}
}

// A held early brief is made again under the same ID once it is over an hour old.
func TestAheadTargetRemakesAHeldBriefOnlyWhenItIsOverAnHour(t *testing.T) {
	settings := earlyTestSettings()
	morning := slotFor(t, time.Date(2026, 11, 8, 12, 0, 0, 0, earlyTestZone), mobileBriefSlotMorning, settings)
	now := time.Date(2026, 11, 7, 23, 0, 0, 0, earlyTestZone)
	heldAt := func(made time.Time) func(string) (MobileBriefV1, bool) {
		brief := aheadBriefFor(t, morning, made)
		return func(id string) (MobileBriefV1, bool) {
			if id == morning.BriefID {
				return brief, true
			}
			return MobileBriefV1{}, false
		}
	}
	if _, ok := mobileBriefAheadTarget(now, earlyTestZone, settings, heldAt(now.Add(-30*time.Minute))); ok {
		t.Fatal("remade a held early brief that is only half an hour old")
	}
	got, ok := mobileBriefAheadTarget(now, earlyTestZone, settings, heldAt(now.Add(-2*time.Hour)))
	if !ok || got.BriefID != morning.BriefID {
		t.Fatalf("target %+v (%t), want the held brief remade", got, ok)
	}
	// A final brief in the slot is never remade.
	final := aheadBriefFor(t, morning, now.Add(-2*time.Hour))
	final.Generation = nil
	finalHeld := func(id string) (MobileBriefV1, bool) {
		if id == morning.BriefID {
			return final, true
		}
		return MobileBriefV1{}, false
	}
	if _, ok := mobileBriefAheadTarget(now, earlyTestZone, settings, finalHeld); ok {
		t.Fatal("remade a final brief")
	}
}

// The scheduled moment is 9 PM, or the quiet-hours start when earlier; never before
// the day's last slot.
func TestAheadMomentIsNineOrQuietStartNeverBeforeTheLastSlot(t *testing.T) {
	settings := earlyTestSettings()
	now := time.Date(2026, 11, 7, 9, 0, 0, 0, earlyTestZone)
	if got := mobileBriefAheadMoment(now, earlyTestZone, settings, 0, false); got.Hour() != 21 || got.Minute() != 0 {
		t.Fatalf("moment without quiet hours is %s, want 9 PM", got)
	}
	if got := mobileBriefAheadMoment(now, earlyTestZone, settings, 20*time.Hour, true); got.Hour() != 20 {
		t.Fatalf("moment with quiet hours from 8 PM is %s", got)
	}
	evening := slotFor(t, now, mobileBriefSlotEvening, settings)
	if got := mobileBriefAheadMoment(now, earlyTestZone, settings, 14*time.Hour, true); got.Before(evening.ScheduledAt) {
		t.Fatalf("moment %s is before the day's last slot %s", got, evening.ScheduledAt)
	}
}

func TestEarlyBriefHeldUntilItsSlot(t *testing.T) {
	settings := earlyTestSettings()
	store := newMobileBriefStore(t.TempDir())
	evening := slotFor(t, time.Date(2026, 11, 7, 12, 0, 0, 0, earlyTestZone), mobileBriefSlotEvening, settings)
	eveningBrief := MobileBriefV1{
		Version: 1, BriefID: evening.BriefID, Slot: evening.Slot, LocalDate: "2026-11-07",
		AsOf: formatMobileBriefTime(evening.ScheduledAt.Add(10 * time.Minute)), NarrativeStatus: "ok",
	}
	nextMorning := slotFor(t, time.Date(2026, 11, 8, 12, 0, 0, 0, earlyTestZone), mobileBriefSlotMorning, settings)
	early := aheadBriefFor(t, nextMorning, time.Date(2026, 11, 7, 23, 20, 0, 0, earlyTestZone))
	if err := store.saveDocument(eveningBrief, mobileBriefAsOfTime(eveningBrief)); err != nil {
		t.Fatal(err)
	}
	if err := store.saveDocument(early, mobileBriefAsOfTime(early)); err != nil {
		t.Fatal(err)
	}

	// At 11:30 PM every reader sees the evening brief, never the early one.
	setEarlyClock(t, time.Date(2026, 11, 7, 23, 30, 0, 0, earlyTestZone))
	if latest, ok := store.latest(); !ok || latest.BriefID != evening.BriefID {
		t.Fatalf("latest at 11:30 PM is %q, want the evening brief %q", latest.BriefID, evening.BriefID)
	}
	for _, listed := range store.briefs() {
		if listed.BriefID == early.BriefID {
			t.Fatal("the early brief is listed before its slot")
		}
	}

	// At its slot it becomes the latest.
	setEarlyClock(t, nextMorning.ScheduledAt.Add(time.Minute))
	if latest, ok := store.latest(); !ok || latest.BriefID != early.BriefID {
		t.Fatalf("latest after its slot is %q, want %q", latest.BriefID, early.BriefID)
	}
}

func TestEarlyBriefNeverBecomesTheLatestRecordBeforeItsSlot(t *testing.T) {
	settings := earlyTestSettings()
	store := newMobileBriefStore(t.TempDir())
	service := &mobileBriefService{store: store}
	nextMorning := slotFor(t, time.Date(2026, 11, 8, 12, 0, 0, 0, earlyTestZone), mobileBriefSlotMorning, settings)
	early := aheadBriefFor(t, nextMorning, time.Date(2026, 11, 7, 23, 20, 0, 0, earlyTestZone))
	setEarlyClock(t, time.Date(2026, 11, 7, 23, 30, 0, 0, earlyTestZone))
	service.noteLatestShown(early)
	if record, ok := store.readLatestShown(); ok {
		t.Fatalf("the latest record names %q before the early brief's slot", record.BriefID)
	}
}

func TestSlotIsBlockedByAnEarlyBriefOnlyWhileTheComputerSleptThroughIt(t *testing.T) {
	settings := earlyTestSettings()
	store := newMobileBriefStore(t.TempDir())
	service := &mobileBriefService{store: store}
	nextMorning := slotFor(t, time.Date(2026, 11, 8, 12, 0, 0, 0, earlyTestZone), mobileBriefSlotMorning, settings)
	early := aheadBriefFor(t, nextMorning, time.Date(2026, 11, 7, 23, 20, 0, 0, earlyTestZone))
	if err := store.saveDocument(early, mobileBriefAsOfTime(early)); err != nil {
		t.Fatal(err)
	}
	slot := nextMorning.ScheduledAt

	// Awake at the slot: the slot's own build replaces the early brief.
	if service.slotBlocked(early.BriefID, slot) {
		t.Fatal("an awake computer's slot build was blocked by the early brief")
	}
	// Asleep through the slot: the early brief stands.
	service.wakeWindows = []mobileBriefWakeWindow{{SleepAt: slot.Add(-time.Hour), WakeAt: slot.Add(3 * time.Hour)}}
	if !service.slotBlocked(early.BriefID, slot) {
		t.Fatal("a slept-through slot was rebuilt over its early brief")
	}
	// A final brief always blocks its slot.
	final := early
	final.Generation = nil
	if err := store.saveDocument(final, mobileBriefAsOfTime(final)); err != nil {
		t.Fatal(err)
	}
	service.wakeWindows = nil
	if !service.slotBlocked(final.BriefID, slot) {
		t.Fatal("a final brief did not block its slot")
	}
}

func TestPhoneNoticeRulesForEarlyBriefs(t *testing.T) {
	cases := []struct {
		name                    string
		ahead, replaced, notify bool
		want                    bool
	}{
		{"an early brief notifies the phone at its slot", true, false, false, true},
		{"a replacement sends no second notice", false, true, true, false},
		{"an ordinary brief keeps its notice", false, false, true, true},
		{"an ordinary brief without notice stays quiet", false, false, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mobileBriefPhoneNotify(tc.ahead, tc.replaced, tc.notify); got != tc.want {
				t.Fatalf("notify %t, want %t", got, tc.want)
			}
		})
	}
}

func TestEarlyBriefFrameCarriesItsSlotTime(t *testing.T) {
	settings := earlyTestSettings()
	nextMorning := slotFor(t, time.Date(2026, 11, 8, 12, 0, 0, 0, earlyTestZone), mobileBriefSlotMorning, settings)
	early := aheadBriefFor(t, nextMorning, time.Date(2026, 11, 7, 23, 20, 0, 0, earlyTestZone))
	want := nextMorning.ScheduledAt.UTC().Format(time.RFC3339)
	if got := mobileBriefDeliverAtText(early); got != want {
		t.Fatalf("deliverAt %q, want the slot time %q", got, want)
	}
	final := early
	final.Generation = nil
	if got := mobileBriefDeliverAtText(final); got != "" {
		t.Fatalf("a final brief carries deliverAt %q", got)
	}
}

func TestEarlyRunRecordKeepsItsReplacedFlag(t *testing.T) {
	store := newMobileBriefRunStore(t.TempDir())
	made := time.Date(2026, 11, 7, 23, 20, 0, 0, earlyTestZone)
	if _, err := store.record(mobileBriefRun{
		BriefID: "2026-11-08-morning", Slot: "morning", Status: mobileBriefRunOK,
		At: formatMobileBriefTime(made), Early: &mobileBriefEarlyRun{Trigger: "lock", MadeAt: formatMobileBriefTime(made)},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.markEarlyReplaced("2026-11-08-morning"); err != nil {
		t.Fatal(err)
	}
	// The slot's own run is recorded without the early record; it must keep it.
	stored, err := store.record(mobileBriefRun{BriefID: "2026-11-08-morning", Slot: "morning", Status: mobileBriefRunOK, At: formatMobileBriefTime(made)})
	if err != nil {
		t.Fatal(err)
	}
	if stored.Early == nil || stored.Early.Trigger != "lock" || !stored.Early.Replaced {
		t.Fatalf("early record %+v, want trigger lock and replaced", stored.Early)
	}
}
