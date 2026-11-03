package notify

import (
	"testing"
	"time"
)

func TestNotifyWakesEveryWatcherOfKey(t *testing.T) {
	n := New()
	first, cancelFirst := n.Watch("device:a")
	second, cancelSecond := n.Watch("device:a")
	other, cancelOther := n.Watch("device:b")
	defer cancelOther()
	n.Notify("device:a")
	for _, ch := range []<-chan struct{}{first, second} {
		select {
		case <-ch:
		case <-time.After(time.Second):
			t.Fatal("watcher not woken")
		}
	}
	select {
	case <-other:
		t.Fatal("unrelated key woken")
	default:
	}
	cancelFirst()
	cancelSecond()
	n.Notify("device:absent")
}

func TestCancelledWatchDoesNotLeakState(t *testing.T) {
	n := New()
	ch, cancel := n.Watch("pairing:x")
	cancel()
	cancel()
	n.Notify("pairing:x")
	select {
	case <-ch:
	default:
		// A cancelled watch is not required to fire; it only must not panic.
	}
	if len(n.waiters) != 0 {
		t.Fatal("cancelled watch left state behind")
	}
}
