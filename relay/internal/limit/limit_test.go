package limit

import (
	"testing"
	"time"
)

func TestFixedWindowLimitsAndResets(t *testing.T) {
	l := New()
	start := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	for i := range 3 {
		if ok, _ := l.Allow("claim:x", 3, 10*time.Minute, start.Add(time.Duration(i)*time.Second)); !ok {
			t.Fatalf("event %d refused inside the limit", i)
		}
	}
	ok, retry := l.Allow("claim:x", 3, 10*time.Minute, start.Add(5*time.Minute))
	if ok || retry != 5*time.Minute {
		t.Fatalf("limit not enforced: ok=%t retry=%s", ok, retry)
	}
	if ok, _ = l.Allow("claim:y", 3, 10*time.Minute, start); !ok {
		t.Fatal("keys share a window")
	}
	if ok, _ = l.Allow("claim:x", 3, 10*time.Minute, start.Add(10*time.Minute)); !ok {
		t.Fatal("window did not reset")
	}
}

func TestExpiredWindowsAreSwept(t *testing.T) {
	l := New()
	start := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	for i := range 100 {
		l.Allow("device:"+string(rune('a'+i%26))+string(rune('a'+i/26)), 1, time.Minute, start)
	}
	l.Allow("device:late", 1, time.Minute, start.Add(3*time.Minute))
	if len(l.windows) != 1 {
		t.Fatalf("%d windows survived the sweep", len(l.windows))
	}
}

func TestExceededReadsWithoutCounting(t *testing.T) {
	l := New()
	start := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	if over, _ := l.Exceeded("auth:x", 2, start); over {
		t.Fatal("empty key reported exceeded")
	}
	l.Allow("auth:x", 2, time.Minute, start)
	if over, _ := l.Exceeded("auth:x", 2, start); over {
		t.Fatal("one short of the limit reported exceeded")
	}
	l.Allow("auth:x", 2, time.Minute, start)
	over, retry := l.Exceeded("auth:x", 2, start.Add(10*time.Second))
	if !over || retry != 50*time.Second {
		t.Fatalf("at the limit: over=%t retry=%s", over, retry)
	}
	if over, _ = l.Exceeded("auth:x", 2, start.Add(time.Minute)); over {
		t.Fatal("expired window still exceeded")
	}
}
