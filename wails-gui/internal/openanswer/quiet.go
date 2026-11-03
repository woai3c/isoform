package openanswer

import (
	"time"

	"wails-gui/internal/engagement"
)

// HoldForBrief reports whether a post must wait for a brief. The exception is
// a named time within quiet hours; merely reaching quiet hours after an
// outside-quiet named time does not make that exception apply.
func HoldForBrief(now, namedTime time.Time, quiet engagement.QuietWindow) bool {
	if !quiet.Contains(now) {
		return false
	}
	return namedTime.IsZero() || !quiet.Contains(namedTime)
}
