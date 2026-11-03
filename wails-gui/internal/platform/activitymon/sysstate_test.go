package activitymon

import "testing"

func TestSystemLifecycleSubscribersReceiveSleepAndPowerOff(t *testing.T) {
	previousChanges, previousState := systemStateChanges, latestSystemSleepState.Load()
	systemStateChanges = make(chan SystemStateChange, 4)
	t.Cleanup(func() { systemStateChanges = previousChanges; latestSystemSleepState.Store(previousState) })
	ch, unsubscribe := SubscribeLifecycle()
	defer unsubscribe()
	for _, want := range []SystemStateKind{SystemStateSleep, SystemStatePowerOff} {
		pushSystemStateChange(want)
		select {
		case got := <-ch:
			if got != want {
				t.Fatalf("got %q, want %q", got, want)
			}
		default:
			t.Fatalf("missing %q", want)
		}
	}
}

func TestSystemSleepStateSurvivesFullNotificationBuffer(t *testing.T) {
	previousChanges, previousState := systemStateChanges, latestSystemSleepState.Load()
	systemStateChanges = make(chan SystemStateChange, 1)
	latestSystemSleepState.Store(nil)
	t.Cleanup(func() {
		systemStateChanges = previousChanges
		latestSystemSleepState.Store(previousState)
	})
	pushSystemStateChange(SystemStateSleep)
	sleep := LatestSystemSleepState()
	pushSystemStateChange(SystemStateWake) // The stream is full; this notification is dropped.
	for range 40 {
		pushSystemStateChange(SystemStateLocked)
		pushSystemStateChange(SystemStateUnlocked)
	}
	if latest := LatestSystemSleepState(); latest.Kind != SystemStateWake || latest.At.IsZero() || latest.SleepAt != sleep.At {
		t.Fatalf("wake observation was lost behind a full stream: %+v", latest)
	}
	wake := LatestSystemSleepState()
	pushSystemStateChange(SystemStateWake)
	if latest := LatestSystemSleepState(); latest != wake {
		t.Fatalf("duplicate wake extended the sleep interval: %+v, want %+v", latest, wake)
	}
	pushSystemStateChange(SystemStateSleep)
	secondSleep := LatestSystemSleepState()
	pushSystemStateChange(SystemStateWake)
	if latest := LatestSystemSleepState(); latest.SleepAt != secondSleep.At {
		t.Fatalf("separate sleep intervals were merged: %+v", latest)
	}
}

func TestDuplicateWakeIsDeliveredWithoutExtendingSleep(t *testing.T) {
	previousChanges, previousState := systemStateChanges, latestSystemSleepState.Load()
	systemStateChanges = make(chan SystemStateChange, 3)
	latestSystemSleepState.Store(nil)
	t.Cleanup(func() {
		systemStateChanges = previousChanges
		latestSystemSleepState.Store(previousState)
	})
	pushSystemStateChange(SystemStateSleep)
	<-systemStateChanges
	pushSystemStateChange(SystemStateWake)
	wake := <-systemStateChanges
	pushSystemStateChange(SystemStateWake)
	select {
	case duplicate := <-systemStateChanges:
		if duplicate != wake {
			t.Fatalf("duplicate wake changed the observed sleep interval: %+v, want %+v", duplicate, wake)
		}
	default:
		t.Fatal("duplicate wake was dropped before consumers could run their wake hooks")
	}
	if latest := LatestSystemSleepState(); latest != wake {
		t.Fatalf("duplicate wake replaced the sleep bookkeeping: %+v, want %+v", latest, wake)
	}
}
