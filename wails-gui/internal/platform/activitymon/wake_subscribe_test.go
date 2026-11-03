package activitymon

import "testing"

func TestWakeSubscribersDoNotConsumeSystemStateStream(t *testing.T) {
	first, stopFirst := SubscribeWake()
	second, stopSecond := SubscribeWake()
	defer stopSecond()
	pushSystemStateChange(SystemStateWake)
	for _, ch := range []<-chan struct{}{first, second} {
		select {
		case <-ch:
		default:
			t.Fatal("subscriber missed wake")
		}
	}
	stopFirst()
	pushSystemStateChange(SystemStateWake)
	select {
	case <-first:
		t.Fatal("unsubscribed channel received wake")
	default:
	}
	select {
	case <-second:
	default:
		t.Fatal("remaining subscriber missed wake")
	}
}
