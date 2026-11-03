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
	percent      float64
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
		onAC: p.onAC, batteryPercent: p.percent, hasBattery: p.hasBattery,
		lowPowerMode: p.lowPowerMode, lidClosed: p.lidClosed,
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
func (p *fakeKeepAwakePower) set(onAC bool, percent float64) {
	p.mu.Lock()
	p.onAC = onAC
	p.percent = percent
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
	power := &fakeKeepAwakePower{onAC: onAC, percent: 80, hasBattery: true}
	assertion := &fakeKeepAwakeAssertion{}
	keepAwake := newMobileKeepAwake(dir, power, assertion)
	t.Cleanup(keepAwake.Stop)
	return keepAwake, power, assertion
}

func TestKeepAwakePowerAndPairingTransitions(t *testing.T) {
	keepAwake, power, assertion := newTestKeepAwake(t, t.TempDir(), true)
	steps := []struct {
		name    string
		action  func()
		held    bool
		onPower bool
	}{
		{"no phone", func() {}, false, false},
		{"pair on AC", func() { keepAwake.SetPairedPhones(1) }, true, true},
		{"Low Power Mode on AC", func() { power.setLowPowerMode(true) }, false, true},
		{"Low Power Mode off on AC", func() { power.setLowPowerMode(false) }, true, true},
		{"battery above cutoff", func() { power.set(false, 50) }, true, false},
		{"Low Power Mode on battery", func() { power.setLowPowerMode(true) }, false, false},
		{"Low Power Mode off on battery", func() { power.setLowPowerMode(false) }, true, false},
		{"battery exactly 20 while discharging", func() { power.set(false, 20) }, true, false},
		{"battery below cutoff", func() { power.set(false, 19) }, false, false},
		{"battery exactly 20 while charging", func() { power.set(false, 20) }, false, false},
		{"charged above cutoff", func() { power.set(false, 21) }, true, false},
		{"unknown battery", func() { power.set(false, -1) }, false, false},
		{"AC with low battery", func() { power.set(true, 10) }, true, true},
		{"unplug at low battery", func() { power.set(false, 10) }, false, false},
		{"back to AC", func() { power.set(true, 10) }, true, true},
		{"owner off", func() {
			if _, err := keepAwake.SetOn(false); err != nil {
				t.Fatal(err)
			}
		}, false, true},
		{"Low Power Mode on while owner off", func() { power.setLowPowerMode(true) }, false, true},
		{"Low Power Mode off while owner off", func() { power.setLowPowerMode(false) }, false, true},
		{"battery change while off", func() { power.set(false, 80) }, false, false},
		{"owner on", func() {
			if _, err := keepAwake.SetOn(true); err != nil {
				t.Fatal(err)
			}
		}, true, false},
		{"another phone paired", func() { keepAwake.SetPairedPhones(2) }, true, false},
		{"one phone remains", func() { keepAwake.SetPairedPhones(1) }, true, false},
		{"last phone unpaired", func() { keepAwake.SetPairedPhones(0) }, false, false},
		{"phone paired again", func() { keepAwake.SetPairedPhones(1) }, true, false},
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

func TestKeepAwakeInitialPowerState(t *testing.T) {
	tests := []struct {
		name         string
		onAC         bool
		percent      float64
		lowPowerMode bool
		held         bool
	}{
		{"AC with low battery", true, 5, false, true},
		{"AC with Low Power Mode", true, 80, true, false},
		{"battery above 20", false, 21, false, true},
		{"battery above 20 with Low Power Mode", false, 80, true, false},
		{"battery at 20", false, 20, false, true},
		{"battery below 20", false, 19, false, false},
		{"battery unavailable", false, -1, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			power := &fakeKeepAwakePower{onAC: tt.onAC, percent: tt.percent, hasBattery: true, lowPowerMode: tt.lowPowerMode}
			assertion := &fakeKeepAwakeAssertion{}
			keepAwake := newMobileKeepAwake(t.TempDir(), power, assertion)
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
			keepAwake, _, assertion := newTestKeepAwake(t, dir, false)
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
	power := &fakeKeepAwakePower{onAC: true, percent: 80, watchErr: errors.New("watch failed")}
	assertion := &fakeKeepAwakeAssertion{}
	keepAwake := newMobileKeepAwake(t.TempDir(), power, assertion)
	t.Cleanup(keepAwake.Stop)
	keepAwake.SetPairedPhones(1)
	if assertion.isHeld() {
		t.Fatal("assertion held without power events")
	}
}

func TestKeepAwakeSettingOffReleasesAndOnTakesItBack(t *testing.T) {
	keepAwake, power, assertion := newTestKeepAwake(t, t.TempDir(), true)
	keepAwake.SetPairedPhones(1)
	if !assertion.isHeld() {
		t.Fatal("on AC power with a phone paired, the assertion should be held")
	}
	status, err := keepAwake.SetOn(false)
	if err != nil || status.On || assertion.isHeld() {
		t.Fatalf("switching off: status %+v, err %v, held %v", status, err, assertion.isHeld())
	}
	power.set(false, 80)
	power.set(true, 80)
	if assertion.isHeld() {
		t.Fatal("a setting that is off must not take the assertion on power changes")
	}
	if _, err := keepAwake.SetOn(true); err != nil || !assertion.isHeld() {
		t.Fatalf("switching on: err %v, held %v", err, assertion.isHeld())
	}
}

func TestKeepAwakeWithoutPhoneTakesNothing(t *testing.T) {
	keepAwake, power, assertion := newTestKeepAwake(t, t.TempDir(), true)
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

func TestKeepAwakeFirstPhoneAgainTurnsDefaultOn(t *testing.T) {
	dir := t.TempDir()
	keepAwake, _, assertion := newTestKeepAwake(t, dir, true)
	keepAwake.SetPairedPhones(1)
	keepAwake.SetPairedPhones(0)
	keepAwake.SetPairedPhones(1)
	if status := keepAwake.Status(); !status.On {
		t.Fatalf("setting after pairing again = %+v, want on by default", status)
	}
	if !assertion.isHeld() {
		t.Fatal("pairing again on AC power should take the assertion")
	}
	reloaded := newMobileKeepAwake(dir, &fakeKeepAwakePower{}, &fakeKeepAwakeAssertion{})
	if status := reloaded.Status(); !status.On {
		t.Fatalf("reloaded default = %+v, want on", status)
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
	keepAwake.SetPairedPhones(1)
	keepAwake.Stop()
	if assertion.isHeld() {
		t.Fatal("stopping must release the assertion")
	}
	power.set(true, 80)
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
		{"battery at 20 takes priority", mobileKeepAwakePowerState{batteryPercent: 20, lowPowerMode: true, lidClosed: true}, true, "battery_floor"},
		{"battery below 20 without an assertion", mobileKeepAwakePowerState{batteryPercent: 19}, false, "battery_floor"},
		{"Low Power Mode on AC", mobileKeepAwakePowerState{onAC: true, batteryPercent: 80, lowPowerMode: true}, false, "low_power"},
		{"Low Power Mode takes priority over lid", mobileKeepAwakePowerState{batteryPercent: 80, lowPowerMode: true, lidClosed: true}, true, "low_power"},
		{"lid closed", mobileKeepAwakePowerState{onAC: true, batteryPercent: 80, lidClosed: true}, true, "lid_closed"},
		{"chosen sleep on AC", mobileKeepAwakePowerState{onAC: true, batteryPercent: 80}, true, "user_sleep"},
		{"chosen sleep above the battery floor", mobileKeepAwakePowerState{batteryPercent: 80}, true, "user_sleep"},
		{"no assertion and no known cause", mobileKeepAwakePowerState{onAC: true, batteryPercent: 80}, false, "unknown"},
		{"unknown battery without an assertion", mobileKeepAwakePowerState{batteryPercent: -1}, false, "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keepAwake := &mobileKeepAwake{
				onPower: tt.state.onAC, batteryPercent: tt.state.batteryPercent,
				batteryKnown: tt.state.batteryPercent >= 0 && tt.state.batteryPercent <= 100,
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
