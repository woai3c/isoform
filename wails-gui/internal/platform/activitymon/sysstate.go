package activitymon

import (
	"sync"
	"sync/atomic"
	"time"
)

// System state signals that need no permission: the screen locking and
// unlocking, the Mac sleeping and waking, and (polled) the power source and
// whether any app has the microphone or the camera open. The desktop app
// turns them into activity events, holds deliveries while the screen is
// locked, refreshes calendars on wake, and shapes the day for the brief.

// SystemStateKind names one change.
type SystemStateKind string

const (
	SystemStateLocked   SystemStateKind = "locked"
	SystemStateUnlocked SystemStateKind = "unlocked"
	SystemStateSleep    SystemStateKind = "sleep"
	SystemStateWake     SystemStateKind = "wake"
	SystemStatePowerOff SystemStateKind = "poweroff"
)

// SystemStateChange is one notification from the OS.
type SystemStateChange struct {
	Kind SystemStateKind
	At   time.Time
	// SleepAt pairs a native wake with its observed sleep, even when queued
	// notifications between them were dropped. It is zero without that evidence.
	SleepAt time.Time
}

// PowerState is a reading of the power source.
type PowerState struct {
	OnBattery bool
	// Percent is the battery level, or -1 when there is no battery.
	Percent int
	Known   bool
}

// systemStateChanges receives one entry per lock, unlock, sleep or wake. The
// native callback does a non-blocking send; a full buffer drops the incoming
// event. Screen state is polled, and the latest sleep/wake observation is kept
// separately so a lost wake cannot leave a consumer indefinitely asleep.
var systemStateChanges = make(chan SystemStateChange, 32)

var latestSystemSleepState atomic.Pointer[SystemStateChange]

var wakeSubscribers = struct {
	sync.Mutex
	entries map[chan struct{}]struct{}
}{entries: make(map[chan struct{}]struct{})}

var lifecycleSubscribers = struct {
	sync.Mutex
	entries map[chan SystemStateKind]struct{}
}{entries: make(map[chan SystemStateKind]struct{})}

// SubscribeLifecycle lets the phone connection observe sleep and shutdown
// without consuming the activity monitor's bounded state stream.
func SubscribeLifecycle() (<-chan SystemStateKind, func()) {
	ch := make(chan SystemStateKind, 4)
	lifecycleSubscribers.Lock()
	lifecycleSubscribers.entries[ch] = struct{}{}
	lifecycleSubscribers.Unlock()
	return ch, func() {
		lifecycleSubscribers.Lock()
		delete(lifecycleSubscribers.entries, ch)
		lifecycleSubscribers.Unlock()
	}
}

// SubscribeWake does not consume the activity monitor's state stream.
func SubscribeWake() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	wakeSubscribers.Lock()
	wakeSubscribers.entries[ch] = struct{}{}
	wakeSubscribers.Unlock()
	return ch, func() {
		wakeSubscribers.Lock()
		delete(wakeSubscribers.entries, ch)
		wakeSubscribers.Unlock()
	}
}

// LatestSystemSleepState returns the last sleep or wake observation independently
// of the bounded notification stream. Lock and unlock events do not replace it.
func LatestSystemSleepState() SystemStateChange {
	if change := latestSystemSleepState.Load(); change != nil {
		return *change
	}
	return SystemStateChange{}
}

// SystemStateChanges is the stream of lock, unlock, sleep and wake changes.
// Call StartSystemStateObservers once first.
func SystemStateChanges() <-chan SystemStateChange { return systemStateChanges }

func pushSystemStateChange(kind SystemStateKind) {
	change := SystemStateChange{Kind: kind, At: time.Now()}
	switch kind {
	case SystemStateSleep, SystemStatePowerOff:
		lifecycleSubscribers.Lock()
		for ch := range lifecycleSubscribers.entries {
			select {
			case ch <- kind:
			default:
			}
		}
		lifecycleSubscribers.Unlock()
		if kind == SystemStatePowerOff {
			return
		}
	}
	switch kind {
	case SystemStateSleep:
		latestSystemSleepState.Store(&change)
	case SystemStateWake:
		wakeSubscribers.Lock()
		for ch := range wakeSubscribers.entries {
			select {
			case ch <- struct{}{}:
			default:
			}
		}
		wakeSubscribers.Unlock()
		if previous := latestSystemSleepState.Load(); previous != nil {
			switch previous.Kind {
			case SystemStateSleep:
				change.SleepAt = previous.At
			case SystemStateWake:
				// Deliver wake hooks again, preserving the observed sleep interval.
				// Repeated wake notifications must not move its end forward.
				change = *previous
			}
		}
		latestSystemSleepState.Store(&change)
	}
	select {
	case systemStateChanges <- change:
	default:
	}
}
