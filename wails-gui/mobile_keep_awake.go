package main

import (
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// The computer holds a system idle-sleep assertion while the setting is on and
// at least one phone is paired, except when battery charge is too low or Low
// Power Mode is on. Power is watched through events, never polled. The state
// machine takes the power source and the assertion as interfaces so tests can
// drive them without touching IOKit.

const (
	mobileKeepAwakeFileName = "keep-awake.json"
	mobileKeepAwakeVersion  = 1
)

type mobileKeepAwakePowerState struct {
	onAC           bool
	batteryPercent float64
	hasBattery     bool
	lowPowerMode   bool
	lidClosed      bool
}

// mobileKeepAwakePower reads the power source and reports changes.
type mobileKeepAwakePower interface {
	// State reads the current power inputs; battery percentage is -1 when unavailable.
	State() mobileKeepAwakePowerState
	// Watch calls changed on each power-source or Low Power Mode change. It is called at most once.
	Watch(changed func()) error
}

// mobileKeepAwakeAssertion holds or releases the idle-sleep assertion.
type mobileKeepAwakeAssertion interface {
	Hold() error
	Release()
}

// newMobileKeepAwakeSystem builds the real power source and assertion. Tests
// replace it so no run loop starts and no assertion is taken during a test run.
var newMobileKeepAwakeSystem = func() (mobileKeepAwakePower, mobileKeepAwakeAssertion) {
	return newMobileKeepAwakeOSPower(), newMobileKeepAwakeOSAssertion()
}

// MobileKeepAwakeStatus is the shape of GET and POST /settings/keep-awake and of
// the computer's keep-awake binding (PROTOCOL.md section 12).
type MobileKeepAwakeStatus struct {
	On           bool `json:"on"`
	PairedPhones int  `json:"pairedPhones"`
	OnPower      bool `json:"onPower"`
	HasBattery   bool `json:"hasBattery"`
}

// mobileKeepAwakeFile is the on-disk shape of keep-awake.json. A missing "on"
// means the owner has not chosen yet, so the setting is on once a phone is paired.
type mobileKeepAwakeFile struct {
	Version int   `json:"version"`
	On      *bool `json:"on,omitempty"`
}

type mobileKeepAwake struct {
	mu             sync.Mutex
	path           string
	chosen         *bool
	phones         int
	onPower        bool
	batteryPercent float64
	hasBattery     bool
	lowBattery     bool
	batteryKnown   bool
	lowPowerMode   bool
	lidClosed      bool
	watchFailed    bool
	held           bool
	watching       bool
	stopped        bool
	power          mobileKeepAwakePower
	assertion      mobileKeepAwakeAssertion
}

// newMobileKeepAwake reads keep-awake.json from stateDir. A missing or unreadable
// file leaves the setting unchosen.
func newMobileKeepAwake(stateDir string, power mobileKeepAwakePower, assertion mobileKeepAwakeAssertion) *mobileKeepAwake {
	k := &mobileKeepAwake{power: power, assertion: assertion}
	if stateDir != "" {
		k.path = filepath.Join(stateDir, mobileKeepAwakeFileName)
	}
	if k.path == "" {
		return k
	}
	raw, err := os.ReadFile(k.path)
	if err != nil {
		return k
	}
	var file mobileKeepAwakeFile
	if err := json.Unmarshal(raw, &file); err != nil || file.Version != mobileKeepAwakeVersion {
		log.Printf("[mobile-keep-awake] settings file is not readable; using the default")
		return k
	}
	k.chosen = file.On
	return k
}

// on reports the setting. Before the owner chooses, it is on. The caller holds mu.
func (k *mobileKeepAwake) on() bool {
	return k.chosen == nil || *k.chosen
}

func (k *mobileKeepAwake) statusLocked() MobileKeepAwakeStatus {
	return MobileKeepAwakeStatus{
		On: k.on(), PairedPhones: k.phones, OnPower: k.onPower, HasBattery: k.hasBattery,
	}
}

// Status returns the setting, the paired-phone count and the power state.
func (k *mobileKeepAwake) Status() MobileKeepAwakeStatus {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.statusLocked()
}

// SetOn saves the owner's choice and applies it at once.
func (k *mobileKeepAwake) SetOn(on bool) (MobileKeepAwakeStatus, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if err := k.saveLocked(&on); err != nil {
		return k.statusLocked(), err
	}
	k.chosen = &on
	k.reconcileLocked()
	return k.statusLocked(), nil
}

// initPairedPhones sets the count at startup. Unlike SetPairedPhones it never
// changes the saved choice, so a restart keeps a setting the owner turned off.
func (k *mobileKeepAwake) initPairedPhones(count int) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.phones = count
	k.reconcileLocked()
}

// SetPairedPhones records how many phones are paired. The first phone turns the
// default back on. The last one removed turns the setting off, saved, so a phone
// paired again starts from the default.
func (k *mobileKeepAwake) SetPairedPhones(count int) {
	k.mu.Lock()
	defer k.mu.Unlock()
	previous := k.phones
	k.phones = count
	switch {
	case previous == 0 && count > 0 && k.chosen != nil:
		k.chosen = nil
		if err := k.saveLocked(nil); err != nil {
			log.Printf("[mobile-keep-awake] could not save the default for a new phone: %v", err)
		}
	case previous > 0 && count == 0:
		off := false
		k.chosen = &off
		if err := k.saveLocked(&off); err != nil {
			log.Printf("[mobile-keep-awake] could not save the setting after the last phone was removed: %v", err)
		}
	}
	k.reconcileLocked()
}

// observePower records a power-source or Low Power Mode change reported by the watcher.
func (k *mobileKeepAwake) observePower() {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.stopped {
		return
	}
	k.readPowerLocked()
	k.reconcileLocked()
}

// At exactly 20%, retain the previous low-battery state. A fresh reading at
// 20% is allowed; after falling below 20%, charging must exceed it to resume.
func (k *mobileKeepAwake) readPowerLocked() {
	state := k.power.State()
	k.onPower = state.onAC
	k.batteryPercent = state.batteryPercent
	k.hasBattery = state.hasBattery
	k.lowPowerMode = state.lowPowerMode
	k.lidClosed = state.lidClosed
	k.batteryKnown = state.batteryPercent >= 0 && state.batteryPercent <= 100
	if !k.batteryKnown || state.batteryPercent < 20 {
		k.lowBattery = true
	} else if state.batteryPercent > 20 {
		k.lowBattery = false
	}
}

// SleepReason reads the current inputs once without taking or releasing an assertion.
func (k *mobileKeepAwake) SleepReason() string {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.power != nil {
		k.readPowerLocked()
	}
	switch {
	case !k.onPower && k.batteryKnown && k.batteryPercent <= 20:
		return "battery_floor"
	case k.lowPowerMode:
		return "low_power"
	case k.lidClosed:
		return "lid_closed"
	case k.held:
		return "user_sleep"
	default:
		return "unknown"
	}
}

// Stop releases the assertion. Later changes are ignored.
func (k *mobileKeepAwake) Stop() {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.stopped = true
	k.releaseLocked()
}

// reconcileLocked holds the assertion when enabled with a paired phone, Low
// Power Mode off, and either on AC or above the low-battery cutoff. A paired
// phone starts the event watch even when the setting is off, so hardware status
// stays current. The caller holds mu.
func (k *mobileKeepAwake) reconcileLocked() {
	if k.stopped {
		return
	}
	if k.phones > 0 && !k.watching && k.power != nil {
		k.watching = true
		if err := k.power.Watch(k.observePower); err != nil {
			k.watchFailed = true
			log.Printf("[mobile-keep-awake] power events are unavailable: %v", err)
		}
		k.readPowerLocked()
	}
	want := k.phones > 0 && k.on() && !k.lowPowerMode
	if want && !k.watchFailed && (k.onPower || (k.batteryKnown && !k.lowBattery)) {
		k.holdLocked()
	} else {
		k.releaseLocked()
	}
}

func (k *mobileKeepAwake) holdLocked() {
	if k.held || k.assertion == nil {
		return
	}
	if err := k.assertion.Hold(); err != nil {
		log.Printf("[mobile-keep-awake] could not keep this computer awake: %v", err)
		return
	}
	k.held = true
}

func (k *mobileKeepAwake) releaseLocked() {
	if !k.held || k.assertion == nil {
		k.held = false
		return
	}
	k.assertion.Release()
	k.held = false
}

// saveLocked writes the choice. A nil value removes it, so the default applies.
// The caller holds mu.
func (k *mobileKeepAwake) saveLocked(on *bool) error {
	if k.path == "" {
		return errMobileBriefStateUnavailable
	}
	data, err := json.MarshalIndent(mobileKeepAwakeFile{Version: mobileKeepAwakeVersion, On: on}, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(k.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return writeMobileBriefFileAtomic(dir, filepath.Base(k.path), data)
}

// errMobileKeepAwakeUnavailable answers the computer's binding before the phone
// connection exists.
var errMobileKeepAwakeUnavailable = errors.New("keeping this computer awake needs a paired phone")

// syncKeepAwakePhones passes the paired-phone count to the keep-awake controller.
// Callers must not hold bridge.mu.
func (bridge *mobileBridge) syncKeepAwakePhones() {
	if bridge == nil || bridge.keepAwake == nil {
		return
	}
	bridge.mu.Lock()
	count := len(bridge.devices)
	bridge.mu.Unlock()
	bridge.keepAwake.SetPairedPhones(count)
}

// keepAwakeSetting reports the saved switch for the phone's snapshot and status.
func (bridge *mobileBridge) keepAwakeSetting() bool {
	if bridge == nil || bridge.keepAwake == nil {
		return false
	}
	return bridge.keepAwake.Status().On
}

// mobileInstantPushConfigured reports whether this computer's relay has a push gateway
// (APNs), so notices reach phones at once. The relay health endpoint does not
// report it, so the hub's own gateway configuration is the signal.
func mobileInstantPushConfigured() bool {
	gatewayURL, _, _, err := mobilePushGatewayConfig()
	return err == nil && gatewayURL != ""
}

// GetMobileKeepAwake reads the computer's "Keep this computer awake" switch.
func (a *App) GetMobileKeepAwake() (perfValue MobileKeepAwakeStatus, perfErr error) {
	defer logPageBinding("GetMobileKeepAwake", time.Now(), &perfValue)
	bridge := a.mobileBridgeForUse()
	if bridge == nil || bridge.keepAwake == nil {
		return MobileKeepAwakeStatus{On: true}, nil
	}
	return bridge.keepAwake.Status(), nil
}

// SetMobileKeepAwake saves the switch. Hidden until a phone is paired.
func (a *App) SetMobileKeepAwake(on bool) (MobileKeepAwakeStatus, error) {
	bridge := a.mobileBridgeForUse()
	if bridge == nil || bridge.keepAwake == nil {
		return MobileKeepAwakeStatus{On: true}, errMobileKeepAwakeUnavailable
	}
	status, err := bridge.keepAwake.SetOn(on)
	if err == nil {
		a.emitBrowserEvent("mobile:updated")
		bridge.noteTodayChanged()
	}
	return status, err
}
