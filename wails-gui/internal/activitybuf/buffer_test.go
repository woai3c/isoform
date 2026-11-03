package activitybuf

import (
	"sync"
	"testing"
	"time"

	"wails-gui/internal/platform/activitymon"
)

func makeEvent(kind activitymon.EventKind, tsMs int64) activitymon.ActivityEvent {
	return activitymon.ActivityEvent{
		Timestamp: tsMs,
		Kind:      kind,
		AppName:   "TestApp",
	}
}

func TestNewRingBuffer(t *testing.T) {
	b := NewRingBuffer(100, 10*time.Second)
	if b.Len() != 0 {
		t.Errorf("expected empty buffer, got %d", b.Len())
	}
	if b.capacity != 100 {
		t.Errorf("expected capacity 100, got %d", b.capacity)
	}
}

func TestNewRingBufferDefaults(t *testing.T) {
	b := NewRingBuffer(0, 0)
	if b.capacity != defaultCapacity {
		t.Errorf("expected default capacity %d, got %d", defaultCapacity, b.capacity)
	}
	if b.window != defaultWindow {
		t.Errorf("expected default window %v, got %v", defaultWindow, b.window)
	}
}

func TestPushAndLen(t *testing.T) {
	now := time.Now().UnixMilli()
	b := NewRingBuffer(10, 30*time.Second)

	for i := 0; i < 5; i++ {
		b.Push(makeEvent(activitymon.EventClick, now+int64(i*100)))
	}

	if b.Len() != 5 {
		t.Errorf("expected 5 events, got %d", b.Len())
	}
}

func TestCapacityEviction(t *testing.T) {
	now := time.Now().UnixMilli()
	b := NewRingBuffer(5, 1*time.Hour) // large window so only capacity matters

	for i := 0; i < 10; i++ {
		b.Push(makeEvent(activitymon.EventClick, now+int64(i*100)))
	}

	if b.Len() != 5 {
		t.Errorf("expected 5 events after overflow, got %d", b.Len())
	}

	snap := b.Snapshot()
	if len(snap) != 5 {
		t.Fatalf("expected 5 events in snapshot, got %d", len(snap))
	}

	// Should contain events 5-9 (the last 5)
	for i, evt := range snap {
		expected := now + int64((i+5)*100)
		if evt.Timestamp != expected {
			t.Errorf("event %d: expected timestamp %d, got %d", i, expected, evt.Timestamp)
		}
	}
}

func TestTimeWindowEviction(t *testing.T) {
	b := NewRingBuffer(1000, 1*time.Second) // 1 second window

	base := time.Now().UnixMilli()

	// Push old events
	b.Push(makeEvent(activitymon.EventClick, base-5000)) // 5s ago
	b.Push(makeEvent(activitymon.EventClick, base-3000)) // 3s ago
	b.Push(makeEvent(activitymon.EventClick, base-2000)) // 2s ago

	// Push a recent event — this triggers eviction of old ones
	b.Push(makeEvent(activitymon.EventKeyboard, base))

	// Only the recent event should survive the 1-second window
	if b.Len() != 1 {
		t.Errorf("expected 1 event after time eviction, got %d", b.Len())
	}

	snap := b.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("expected 1 event in snapshot, got %d", len(snap))
	}
	if snap[0].Kind != activitymon.EventKeyboard {
		t.Errorf("expected keyboard event, got %s", snap[0].Kind)
	}
}

func TestSnapshotOrder(t *testing.T) {
	now := time.Now().UnixMilli()
	b := NewRingBuffer(100, 1*time.Hour)

	for i := 0; i < 7; i++ {
		b.Push(makeEvent(activitymon.EventClick, now+int64(i*100)))
	}

	snap := b.Snapshot()
	for i := 1; i < len(snap); i++ {
		if snap[i].Timestamp <= snap[i-1].Timestamp {
			t.Errorf("events not in order at index %d: %d <= %d",
				i, snap[i].Timestamp, snap[i-1].Timestamp)
		}
	}
}

func TestSnapshotIsCopy(t *testing.T) {
	now := time.Now().UnixMilli()
	b := NewRingBuffer(10, 1*time.Hour)

	b.Push(makeEvent(activitymon.EventClick, now))
	snap1 := b.Snapshot()

	// Modify the snapshot
	snap1[0].AppName = "Modified"

	// Original should be unchanged
	snap2 := b.Snapshot()
	if snap2[0].AppName == "Modified" {
		t.Error("snapshot modification affected buffer")
	}
}

func TestClear(t *testing.T) {
	now := time.Now().UnixMilli()
	b := NewRingBuffer(10, 1*time.Hour)

	b.Push(makeEvent(activitymon.EventClick, now))
	b.Push(makeEvent(activitymon.EventClick, now+100))
	b.Clear()

	if b.Len() != 0 {
		t.Errorf("expected 0 after clear, got %d", b.Len())
	}

	snap := b.Snapshot()
	if snap != nil {
		t.Errorf("expected nil snapshot after clear, got %v", snap)
	}
}

func TestSnapshotEmpty(t *testing.T) {
	b := NewRingBuffer(10, 1*time.Hour)
	snap := b.Snapshot()
	if snap != nil {
		t.Errorf("expected nil snapshot for empty buffer, got %v", snap)
	}
}

func TestDrainReturnsEventsAndClears(t *testing.T) {
	now := time.Now().UnixMilli()
	b := NewRingBuffer(10, 1*time.Hour)
	for i := 0; i < 3; i++ {
		b.Push(makeEvent(activitymon.EventClick, now+int64(i*100)))
	}

	got := b.Drain()
	if len(got) != 3 {
		t.Fatalf("Drain returned %d events, want 3", len(got))
	}
	if b.Len() != 0 {
		t.Fatalf("buffer length after Drain = %d, want 0", b.Len())
	}
	// Re-Drain should return nothing.
	if again := b.Drain(); again != nil {
		t.Fatalf("second Drain returned %d events, want nil", len(again))
	}
}

func TestDrainIsAtomicUnderContention(t *testing.T) {
	// Two concurrent Drain callers must NOT both observe the same events —
	// that's the bug that previously allowed extractSession to persist the
	// same events under two different session IDs.
	now := time.Now().UnixMilli()
	b := NewRingBuffer(100, 1*time.Hour)
	for i := 0; i < 50; i++ {
		b.Push(makeEvent(activitymon.EventClick, now+int64(i*10)))
	}

	var wg sync.WaitGroup
	results := make([][]activitymon.ActivityEvent, 2)
	for i := 0; i < 2; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = b.Drain()
		}()
	}
	wg.Wait()

	total := len(results[0]) + len(results[1])
	if total != 50 {
		t.Fatalf("concurrent Drain returned %d events total (caller A=%d, B=%d), want 50 — events were duplicated or lost",
			total, len(results[0]), len(results[1]))
	}
	// One caller must have drained all 50; the other must have gotten nil.
	if len(results[0]) != 0 && len(results[1]) != 0 {
		t.Fatalf("both Drain callers got events (A=%d, B=%d) — buffer drain is not atomic",
			len(results[0]), len(results[1]))
	}
}

func TestWrapAround(t *testing.T) {
	now := time.Now().UnixMilli()
	b := NewRingBuffer(3, 1*time.Hour)

	// Fill buffer and wrap around
	for i := 0; i < 7; i++ {
		evt := makeEvent(activitymon.EventClick, now+int64(i*100))
		evt.AppName = "App" + string(rune('A'+i))
		b.Push(evt)
	}

	if b.Len() != 3 {
		t.Errorf("expected 3 events, got %d", b.Len())
	}

	snap := b.Snapshot()
	expectedNames := []string{"AppE", "AppF", "AppG"}
	for i, name := range expectedNames {
		if snap[i].AppName != name {
			t.Errorf("event %d: expected %s, got %s", i, name, snap[i].AppName)
		}
	}
}

func TestAmendLastKeyboard(t *testing.T) {
	now := time.Now().UnixMilli()
	b := NewRingBuffer(100, 10*time.Minute)

	kb := makeEvent(activitymon.EventKeyboard, now)
	kb.AppBundleID = "com.tencent.xinWeChat"
	kb.KeyChars = []string{"y", "e"}
	b.Push(kb)
	// Interleaved click and another app's keyboard burst must be skipped.
	click := makeEvent(activitymon.EventClick, now+100)
	click.AppBundleID = "com.tencent.xinWeChat"
	b.Push(click)
	other := makeEvent(activitymon.EventKeyboard, now+200)
	other.AppBundleID = "com.apple.Terminal"
	other.KeyChars = []string{"l", "s"}
	b.Push(other)

	if !b.AmendLastKeyboard("com.tencent.xinWeChat", func(evt *activitymon.ActivityEvent) {
		evt.TypedText = "也不输上去了"
	}) {
		t.Fatal("AmendLastKeyboard did not find the WeChat keyboard event")
	}
	events := b.Snapshot()
	if events[0].TypedText != "也不输上去了" {
		t.Errorf("TypedText not attached: %+v", events[0])
	}
	if events[2].TypedText != "" {
		t.Errorf("other app's keyboard event must stay untouched: %+v", events[2])
	}
	if b.AmendLastKeyboard("com.unknown.app", func(evt *activitymon.ActivityEvent) {}) {
		t.Error("AmendLastKeyboard must report false for unknown bundle")
	}
}

func TestPushMergePropagatesIMEComposing(t *testing.T) {
	now := time.Now().UnixMilli()
	b := NewRingBuffer(100, 10*time.Minute)

	first := makeEvent(activitymon.EventKeyboard, now)
	first.AppBundleID = "com.tencent.xinWeChat"
	first.KeyChars = []string{"y"}
	b.Push(first)

	second := makeEvent(activitymon.EventKeyboard, now+500)
	second.AppBundleID = "com.tencent.xinWeChat"
	second.KeyChars = []string{"e"}
	second.IMEComposing = true
	b.Push(second)

	events := b.Snapshot()
	if len(events) != 1 {
		t.Fatalf("expected merged burst, got %d events", len(events))
	}
	if !events[0].IMEComposing {
		t.Error("merged burst must carry IMEComposing from any member keystroke")
	}
}
