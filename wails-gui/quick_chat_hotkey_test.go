package main

import (
	"testing"
	"time"
)

func TestDoubleTapDetector_FiresOnSecondReleaseInsideWindow(t *testing.T) {
	d := newDoubleTapDetector(400 * time.Millisecond)
	t0 := time.Unix(0, 0)

	if d.observe(true, false, t0) {
		t.Fatal("first press must not trigger")
	}
	if d.observe(false, false, t0.Add(50*time.Millisecond)) {
		t.Fatal("first release must not trigger")
	}
	if d.observe(true, false, t0.Add(150*time.Millisecond)) {
		t.Fatal("second press must not trigger")
	}
	if !d.observe(false, false, t0.Add(200*time.Millisecond)) {
		t.Fatal("second release within window should trigger")
	}
}

func TestDoubleTapDetector_DoesNotFireWhenSecondReleaseIsLate(t *testing.T) {
	d := newDoubleTapDetector(400 * time.Millisecond)
	t0 := time.Unix(0, 0)

	d.observe(true, false, t0)
	d.observe(false, false, t0.Add(10*time.Millisecond))
	d.observe(true, false, t0.Add(700*time.Millisecond))
	if d.observe(false, false, t0.Add(750*time.Millisecond)) {
		t.Fatal("release outside window must not trigger")
	}
}

func TestDoubleTapDetector_BlockedCancelsInFlight(t *testing.T) {
	d := newDoubleTapDetector(400 * time.Millisecond)
	t0 := time.Unix(0, 0)

	d.observe(true, false, t0)
	if d.observe(true, true, t0.Add(20*time.Millisecond)) {
		t.Fatal("blocked event must not trigger")
	}
	if d.observe(false, false, t0.Add(50*time.Millisecond)) {
		t.Fatal("release after blocked-cancel must not trigger")
	}
}

func TestDoubleTapDetector_DuplicateDownIsIdempotent(t *testing.T) {
	d := newDoubleTapDetector(400 * time.Millisecond)
	t0 := time.Unix(0, 0)

	d.observe(true, false, t0)
	d.observe(true, false, t0.Add(10*time.Millisecond))
	if d.observe(false, false, t0.Add(20*time.Millisecond)) {
		t.Fatal("first release must not trigger after duplicate down")
	}
	d.observe(true, false, t0.Add(100*time.Millisecond))
	if !d.observe(false, false, t0.Add(150*time.Millisecond)) {
		t.Fatal("second release within window should trigger")
	}
}

func TestDoubleTapDetector_ResetClearsInFlight(t *testing.T) {
	d := newDoubleTapDetector(400 * time.Millisecond)
	t0 := time.Unix(0, 0)

	d.observe(true, false, t0)
	d.observe(false, false, t0.Add(10*time.Millisecond))
	d.reset()
	d.observe(true, false, t0.Add(20*time.Millisecond))
	if d.observe(false, false, t0.Add(30*time.Millisecond)) {
		t.Fatal("reset must clear the first tap so the next pair starts fresh")
	}
}
