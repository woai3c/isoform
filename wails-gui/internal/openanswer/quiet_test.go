package openanswer

import (
	"testing"
	"time"

	"wails-gui/internal/engagement"
)

func TestHoldForBrief(t *testing.T) {
	quiet := engagement.QuietWindow{Start: 21 * time.Hour, End: 9 * time.Hour}
	at := func(hour int) time.Time { return time.Date(2026, 10, 26, hour, 0, 0, 0, time.Local) }
	for _, tc := range []struct {
		name       string
		now, named time.Time
		want       bool
	}{
		{"outside quiet", at(12), time.Time{}, false},
		{"event at night", at(22), time.Time{}, true},
		{"named outside quiet", at(22), at(16), true},
		{"named inside quiet", at(22), at(22), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := HoldForBrief(tc.now, tc.named, quiet); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
