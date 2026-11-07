package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Tests never take a real IOKit assertion or start a power run loop.
func init() {
	newMobileKeepAwakeSystem = func() (mobileKeepAwakePower, mobileKeepAwakeAssertion) {
		return &fakeKeepAwakePower{}, &fakeKeepAwakeAssertion{}
	}
}

type fakeKeepAwakePower struct {
	mu           sync.Mutex
	onAC         bool
	hasBattery   bool
	lowPowerMode bool
	lidClosed    bool
	changed      func()
	watches      int
	watchErr     error
}

func (p *fakeKeepAwakePower) State() mobileKeepAwakePowerState {
	p.mu.Lock()
	defer p.mu.Unlock()
	return mobileKeepAwakePowerState{
		onAC: p.onAC, hasBattery: p.hasBattery, lowPowerMode: p.lowPowerMode, lidClosed: p.lidClosed,
	}
}

func (p *fakeKeepAwakePower) Watch(changed func()) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.watches++
	p.changed = changed
	return p.watchErr
}

// set changes the power source and delivers the event the way the run loop does.
func (p *fakeKeepAwakePower) set(onAC bool) {
	p.mu.Lock()
	p.onAC = onAC
	changed := p.changed
	p.mu.Unlock()
	if changed != nil {
		changed()
	}
}

func (p *fakeKeepAwakePower) setLowPowerMode(on bool) {
	p.mu.Lock()
	p.lowPowerMode = on
	changed := p.changed
	p.mu.Unlock()
	if changed != nil {
		changed()
	}
}

func (p *fakeKeepAwakePower) watchCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.watches
}

type fakeKeepAwakeAssertion struct {
	mu       sync.Mutex
	held     bool
	holds    int
	releases int
}

func (a *fakeKeepAwakeAssertion) Hold() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.held {
		a.held = true
		a.holds++
	}
	return nil
}

func (a *fakeKeepAwakeAssertion) Release() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.held {
		a.held = false
		a.releases++
	}
}

func (a *fakeKeepAwakeAssertion) isHeld() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.held
}

func newTestKeepAwake(t *testing.T, dir string, onAC bool) (*mobileKeepAwake, *fakeKeepAwakePower, *fakeKeepAwakeAssertion) {
	t.Helper()
	power := &fakeKeepAwakePower{onAC: onAC, hasBattery: true}
	assertion := &fakeKeepAwakeAssertion{}
	keepAwake := newMobileKeepAwake(dir, power, assertion)
	t.Cleanup(keepAwake.Stop)
	return keepAwake, power, assertion
}

// writeKeepAwakeFile saves a choice the way an earlier build did; on nil writes no "on" key.
func writeKeepAwakeFile(t *testing.T, dir string, on *bool) {
	t.Helper()
	raw := `{"version":1}`
	if on != nil {
		raw = fmt.Sprintf(`{"version":1,"on":%t}`, *on)
	}
	if err := os.WriteFile(filepath.Join(dir, mobileKeepAwakeFileName), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestKeepAwakeDefaultsOff(t *testing.T) {
	keepAwake, power, assertion := newTestKeepAwake(t, t.TempDir(), true)
	keepAwake.SetPairedPhones(1)
	if assertion.isHeld() {
		t.Fatal("a new phone on AC power must not hold the computer awake until the owner turns it on")
	}
	if status := keepAwake.Status(); status.On || status.PairedPhones != 1 {
		t.Fatalf("status = %+v, want off with one phone", status)
	}
	if power.watchCount() != 1 {
		t.Fatalf("a paired phone should start the power watch even while off, watched %d", power.watchCount())
	}
}

func TestKeepAwakeMissingChoiceFileIsOff(t *testing.T) {
	dir := t.TempDir()
	writeKeepAwakeFile(t, dir, nil)
	keepAwake, _, assertion := newTestKeepAwake(t, dir, true)
	keepAwake.initPairedPhones(1)
	if keepAwake.Status().On || assertion.isHeld() {
		t.Fatalf("a file with no on key must be off: status %+v, held %v", keepAwake.Status(), assertion.isHeld())
	}
}

func TestKeepAwakeHoldsOnlyWhilePluggedIn(t *testing.T) {
	keepAwake, power, assertion := newTestKeepAwake(t, t.TempDir(), true)
	steps := []struct {
		name    string
		action  func()
		held    bool
		onPower bool
	}{
		{"no phone, off", func() {}, false, false},
		{"pair while off on AC", func() { keepAwake.SetPairedPhones(1) }, false, true},
		{"owner turns on on AC", func() {
			if _, err := keepAwake.SetOn(true); err != nil {
				t.Fatal(err)
			}
		}, true, true},
		{"Low Power Mode on AC", func() { power.setLowPowerMode(true) }, false, true},
		{"Low Power Mode off on AC", func() { power.setLowPowerMode(false) }, true, true},
		{"unplug to battery", func() { power.set(false) }, false, false},
		{"plug back in", func() { power.set(true) }, true, true},
		{"unplug again", func() { power.set(false) }, false, false},
		{"owner off", func() {
			if _, err := keepAwake.SetOn(false); err != nil {
				t.Fatal(err)
			}
		}, false, false},
		{"owner on while on battery", func() {
			if _, err := keepAwake.SetOn(true); err != nil {
				t.Fatal(err)
			}
		}, false, false},
		{"plug in with setting on", func() { power.set(true) }, true, true},
		{"another phone paired", func() { keepAwake.SetPairedPhones(2) }, true, true},
		{"one phone remains", func() { keepAwake.SetPairedPhones(1) }, true, true},
		{"last phone unpaired", func() { keepAwake.SetPairedPhones(0) }, false, true},
		{"phone paired again stays off", func() { keepAwake.SetPairedPhones(1) }, false, true},
	}
	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			step.action()
			if got := assertion.isHeld(); got != step.held {
				t.Errorf("held = %v, want %v", got, step.held)
			}
			if got := keepAwake.Status().OnPower; got != step.onPower {
				t.Errorf("onPower = %v, want %v", got, step.onPower)
			}
		})
	}
	if got := power.watchCount(); got != 1 {
		t.Errorf("watch count = %d, want 1", got)
	}
}

func TestKeepAwakeBatteryNeverHolds(t *testing.T) {
	keepAwake, power, assertion := newTestKeepAwake(t, t.TempDir(), false)
	if _, err := keepAwake.SetOn(true); err != nil {
		t.Fatal(err)
	}
	keepAwake.SetPairedPhones(1)
	if assertion.isHeld() {
		t.Fatal("on battery with the setting on, the computer must not be held awake")
	}
	power.set(true)
	if !assertion.isHeld() {
		t.Fatal("plugged in, the setting on and a phone paired should hold")
	}
	power.set(false)
	if assertion.isHeld() {
		t.Fatal("unplugged, the assertion must be released")
	}
}

func TestKeepAwakeInitialPowerState(t *testing.T) {
	on := true
	tests := []struct {
		name         string
		onAC         bool
		lowPowerMode bool
		held         bool
	}{
		{"AC", true, false, true},
		{"AC with Low Power Mode", true, true, false},
		{"battery", false, false, false},
		{"battery with Low Power Mode", false, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeKeepAwakeFile(t, dir, &on)
			power := &fakeKeepAwakePower{onAC: tt.onAC, hasBattery: true, lowPowerMode: tt.lowPowerMode}
			assertion := &fakeKeepAwakeAssertion{}
			keepAwake := newMobileKeepAwake(dir, power, assertion)
			t.Cleanup(keepAwake.Stop)
			keepAwake.initPairedPhones(1)
			if got := assertion.isHeld(); got != tt.held {
				t.Errorf("held = %v, want %v", got, tt.held)
			}
		})
	}
}

func TestKeepAwakeExistingChoiceIsPreserved(t *testing.T) {
	for _, on := range []bool{false, true} {
		t.Run(fmt.Sprintf("on=%v", on), func(t *testing.T) {
			dir := t.TempDir()
			original := fmt.Sprintf(`{"version":1,"on":%t}`, on)
			if err := os.WriteFile(filepath.Join(dir, mobileKeepAwakeFileName), []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
			keepAwake, _, assertion := newTestKeepAwake(t, dir, true)
			keepAwake.initPairedPhones(1)
			if keepAwake.Status().On != on || assertion.isHeld() != on {
				t.Fatalf("choice or assertion changed: status %+v, held %v", keepAwake.Status(), assertion.isHeld())
			}
			raw, err := os.ReadFile(filepath.Join(dir, mobileKeepAwakeFileName))
			if err != nil || string(raw) != original {
				t.Fatalf("stored choice rewritten: %q, %v", raw, err)
			}
		})
	}
}

func TestKeepAwakeDoesNotHoldWithoutPowerEvents(t *testing.T) {
	power := &fakeKeepAwakePower{onAC: true, watchErr: errors.New("watch failed")}
	assertion := &fakeKeepAwakeAssertion{}
	keepAwake := newMobileKeepAwake(t.TempDir(), power, assertion)
	t.Cleanup(keepAwake.Stop)
	if _, err := keepAwake.SetOn(true); err != nil {
		t.Fatal(err)
	}
	keepAwake.SetPairedPhones(1)
	if assertion.isHeld() {
		t.Fatal("assertion held without power events")
	}
}

func TestKeepAwakeSettingOffReleasesAndOnTakesItBack(t *testing.T) {
	keepAwake, power, assertion := newTestKeepAwake(t, t.TempDir(), true)
	keepAwake.SetPairedPhones(1)
	if _, err := keepAwake.SetOn(true); err != nil || !assertion.isHeld() {
		t.Fatalf("switching on with a phone on AC: err %v, held %v", err, assertion.isHeld())
	}
	status, err := keepAwake.SetOn(false)
	if err != nil || status.On || assertion.isHeld() {
		t.Fatalf("switching off: status %+v, err %v, held %v", status, err, assertion.isHeld())
	}
	power.set(false)
	power.set(true)
	if assertion.isHeld() {
		t.Fatal("a setting that is off must not take the assertion on power changes")
	}
	if _, err := keepAwake.SetOn(true); err != nil || !assertion.isHeld() {
		t.Fatalf("switching on: err %v, held %v", err, assertion.isHeld())
	}
}

func TestKeepAwakeWithoutPhoneTakesNothing(t *testing.T) {
	keepAwake, power, assertion := newTestKeepAwake(t, t.TempDir(), true)
	if _, err := keepAwake.SetOn(true); err != nil {
		t.Fatal(err)
	}
	if assertion.isHeld() || power.watchCount() != 0 {
		t.Fatalf("no phone paired: held %v, watched %d", assertion.isHeld(), power.watchCount())
	}
	if status := keepAwake.Status(); !status.On || status.PairedPhones != 0 {
		t.Fatalf("status with no phone = %+v", status)
	}
}

func TestKeepAwakeLastPhoneUnpairedTurnsSettingOff(t *testing.T) {
	dir := t.TempDir()
	keepAwake, _, assertion := newTestKeepAwake(t, dir, true)
	if _, err := keepAwake.SetOn(true); err != nil {
		t.Fatal(err)
	}
	keepAwake.SetPairedPhones(1)
	keepAwake.SetPairedPhones(0)
	if assertion.isHeld() {
		t.Fatal("removing the last phone must release the assertion")
	}
	if status := keepAwake.Status(); status.On {
		t.Fatalf("setting after the last phone was removed = %+v, want off", status)
	}
	reloaded := newMobileKeepAwake(dir, &fakeKeepAwakePower{}, &fakeKeepAwakeAssertion{})
	if status := reloaded.Status(); status.On {
		t.Fatalf("reloaded setting = %+v, want off", status)
	}
}

func TestKeepAwakePairingAgainStaysOff(t *testing.T) {
	dir := t.TempDir()
	keepAwake, _, assertion := newTestKeepAwake(t, dir, true)
	if _, err := keepAwake.SetOn(true); err != nil {
		t.Fatal(err)
	}
	keepAwake.SetPairedPhones(1)
	keepAwake.SetPairedPhones(0)
	keepAwake.SetPairedPhones(1)
	if status := keepAwake.Status(); status.On {
		t.Fatalf("setting after pairing again = %+v, want off until the owner turns it on", status)
	}
	if assertion.isHeld() {
		t.Fatal("pairing again must not take the assertion without the owner turning it on")
	}
	reloaded := newMobileKeepAwake(dir, &fakeKeepAwakePower{}, &fakeKeepAwakeAssertion{})
	if status := reloaded.Status(); status.On {
		t.Fatalf("reloaded setting = %+v, want off", status)
	}
}

func TestKeepAwakeSettingPersistsAcrossReload(t *testing.T) {
	dir := t.TempDir()
	keepAwake, _, _ := newTestKeepAwake(t, dir, false)
	keepAwake.SetPairedPhones(1)
	if _, err := keepAwake.SetOn(false); err != nil {
		t.Fatal(err)
	}
	// A restart with a paired phone keeps the saved off; it is not a first pairing.
	reloaded := newMobileKeepAwake(dir, &fakeKeepAwakePower{}, &fakeKeepAwakeAssertion{})
	reloaded.initPairedPhones(1)
	if status := reloaded.Status(); status.On {
		t.Fatalf("reloaded setting = %+v, want the saved off", status)
	}
	raw, err := os.ReadFile(filepath.Join(dir, mobileKeepAwakeFileName))
	if err != nil || !strings.Contains(string(raw), `"on": false`) {
		t.Fatalf("keep-awake.json = %q, %v", raw, err)
	}
}

func TestKeepAwakeStopReleasesAndIgnoresLaterPowerEvents(t *testing.T) {
	keepAwake, power, assertion := newTestKeepAwake(t, t.TempDir(), true)
	if _, err := keepAwake.SetOn(true); err != nil {
		t.Fatal(err)
	}
	keepAwake.SetPairedPhones(1)
	if !assertion.isHeld() {
		t.Fatal("setup: on AC with a phone and the setting on should hold")
	}
	keepAwake.Stop()
	if assertion.isHeld() {
		t.Fatal("stopping (quit) must release the assertion")
	}
	power.set(true)
	if assertion.isHeld() {
		t.Fatal("a stopped controller must not take the assertion again")
	}
}

func TestKeepAwakeSleepReason(t *testing.T) {
	tests := []struct {
		name     string
		state    mobileKeepAwakePowerState
		held     bool
		expected string
	}{
		{"Low Power Mode on AC", mobileKeepAwakePowerState{onAC: true, lowPowerMode: true}, false, "low_power"},
		{"Low Power Mode takes priority over lid", mobileKeepAwakePowerState{lowPowerMode: true, lidClosed: true}, true, "low_power"},
		{"lid closed", mobileKeepAwakePowerState{onAC: true, lidClosed: true}, true, "lid_closed"},
		{"chosen sleep on AC", mobileKeepAwakePowerState{onAC: true}, true, "user_sleep"},
		{"sleep on battery is not Isoform's choice", mobileKeepAwakePowerState{hasBattery: true}, false, "unknown"},
		{"no assertion and no known cause", mobileKeepAwakePowerState{onAC: true}, false, "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keepAwake := &mobileKeepAwake{
				onPower: tt.state.onAC, hasBattery: tt.state.hasBattery,
				lowPowerMode: tt.state.lowPowerMode, lidClosed: tt.state.lidClosed, held: tt.held,
			}
			if got := keepAwake.SleepReason(); got != tt.expected {
				t.Fatalf("SleepReason() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestKeepAwakeStatusJSONShape(t *testing.T) {
	raw, err := json.Marshal(MobileKeepAwakeStatus{On: true, PairedPhones: 2, OnPower: true, HasBattery: true})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"on":true,"pairedPhones":2,"onPower":true,"hasBattery":true}` {
		t.Fatalf("status JSON = %s", raw)
	}
}

func TestKeepAwakeRoutesNeedPushScopeAndAnswerShape(t *testing.T) {
	keepAwake, _, _ := newTestKeepAwake(t, t.TempDir(), true)
	if _, err := keepAwake.SetOn(true); err != nil {
		t.Fatal(err)
	}
	keepAwake.SetPairedPhones(1)
	bridge := &mobileBridge{keepAwake: keepAwake}
	withPush := mobileDeviceRecord{MobileDeviceView: MobileDeviceView{Scopes: []string{mobileScopePush}}}
	withoutPush := mobileDeviceRecord{}

	get := httptest.NewRecorder()
	bridge.handleAuthorized(get, httptest.NewRequest(http.MethodGet, mobileAPIPrefix+"/settings/keep-awake", nil), withPush)
	if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), `"pairedPhones":1`) ||
		!strings.Contains(get.Body.String(), `"hasBattery":true`) {
		t.Fatalf("GET with push scope: %d %s", get.Code, get.Body.String())
	}

	denied := httptest.NewRecorder()
	bridge.handleAuthorized(denied, httptest.NewRequest(http.MethodGet, mobileAPIPrefix+"/settings/keep-awake", nil), withoutPush)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("GET without push scope: %d, want 403", denied.Code)
	}

	post := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, mobileAPIPrefix+"/settings/keep-awake", strings.NewReader(`{"on":false}`))
	request.Header.Set("Content-Type", "application/json")
	bridge.handleAuthorized(post, request, withPush)
	if post.Code != http.StatusOK || keepAwake.Status().On {
		t.Fatalf("POST off: %d %s", post.Code, post.Body.String())
	}

	missing := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, mobileAPIPrefix+"/settings/keep-awake", strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	bridge.handleAuthorized(missing, request, withPush)
	if missing.Code != http.StatusBadRequest {
		t.Fatalf("POST without on: %d, want 400", missing.Code)
	}
}

func TestStatusCarriesKeepAwakeAndInstantPush(t *testing.T) {
	keepAwake, _, _ := newTestKeepAwake(t, t.TempDir(), true)
	if _, err := keepAwake.SetOn(true); err != nil {
		t.Fatal(err)
	}
	keepAwake.SetPairedPhones(1)
	bridge := &mobileBridge{keepAwake: keepAwake}
	recorder := httptest.NewRecorder()
	bridge.serveStatus(recorder)
	var body struct {
		Data struct {
			KeepAwake     bool                  `json:"keepAwake"`
			InstantPush   bool                  `json:"instantPush"`
			BriefSchedule []mobileBriefSlotTime `json:"briefSchedule"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("status JSON: %v (%s)", err, recorder.Body.String())
	}
	if !body.Data.KeepAwake || body.Data.InstantPush || body.Data.BriefSchedule == nil {
		t.Fatalf("status = %+v, want keepAwake true, instantPush false, empty schedule", body.Data)
	}
}

func TestInstantPushFollowsPushGatewayConfig(t *testing.T) {
	oldURL, oldKey, oldEnv := DefaultMobilePushGatewayURL, DefaultMobilePushGatewayPublicKey, DefaultMobilePushGatewayEnvironment
	t.Cleanup(func() {
		DefaultMobilePushGatewayURL, DefaultMobilePushGatewayPublicKey, DefaultMobilePushGatewayEnvironment = oldURL, oldKey, oldEnv
	})
	DefaultMobilePushGatewayURL, DefaultMobilePushGatewayPublicKey, DefaultMobilePushGatewayEnvironment = "", "", ""
	if mobileInstantPushConfigured() {
		t.Fatal("no gateway configured, instantPush must be false")
	}
	DefaultMobilePushGatewayURL = "https://push.example.test"
	DefaultMobilePushGatewayPublicKey = base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	DefaultMobilePushGatewayEnvironment = "production"
	if !mobileInstantPushConfigured() {
		t.Fatal("a valid gateway configured, instantPush must be true")
	}
}

func TestMobileBriefSlotTimesListNextTwentyFourHours(t *testing.T) {
	pacific := time.FixedZone("PDT", -7*3600)
	now := time.Date(2026, 10, 27, 6, 0, 0, 0, pacific)
	schedule := MobileBriefSchedule{Enabled: true, Morning: "07:00", Evening: "18:00"}
	got := mobileBriefSlotTimes(now, pacific, schedule)
	want := []mobileBriefSlotTime{
		{Slot: "morning", At: "2026-10-27T07:00:00-07:00"},
		{Slot: "evening", At: "2026-10-27T18:00:00-07:00"},
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("slot times = %+v, want %+v", got, want)
	}
	schedule.Enabled = false
	if off := mobileBriefSlotTimes(now, pacific, schedule); len(off) != 0 {
		t.Fatalf("disabled schedule listed %+v", off)
	}
}

func TestMobileBriefScheduledBuildStartsTenMinutesEarly(t *testing.T) {
	settings := mobileBriefSettings{Enabled: true, Morning: "07:00", Evening: "18:00"}
	never := func(string) bool { return false }
	if slot, due, _ := mobileBriefPendingSlot(time.Date(2026, 10, 27, 6, 49, 0, 0, time.UTC), time.UTC, settings, never); due {
		t.Fatalf("06:49 is before the 06:50 build start, got slot %q due", slot)
	}
	slot, due, _ := mobileBriefPendingSlot(time.Date(2026, 10, 27, 6, 50, 0, 0, time.UTC), time.UTC, settings, never)
	if !due || slot != mobileBriefSlotMorning {
		t.Fatalf("06:50 should build the morning brief, got slot %q due %v", slot, due)
	}
}

func TestMobileBriefAdaptiveFixedSlotStartsTenMinutesEarly(t *testing.T) {
	settings := mobileBriefSettings{Enabled: true, Morning: "07:00", Evening: "18:00", Adaptive: true}
	day := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	// With no history there is no learned time, so the fixed slot is the prediction.
	for _, p := range mobileBriefPredictDay(day, time.UTC, settings, mobileBriefTimingInput{}) {
		if p.Slot == mobileBriefSlotMorning && p.Reason == "fixed_slot" && p.ScheduledAt.Format("15:04") != "06:50" {
			t.Fatalf("morning build starts %s, want 06:50", p.ScheduledAt.Format(time.RFC3339))
		}
	}
}
