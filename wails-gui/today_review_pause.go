package main

import (
	"context"
	"log"
	"strconv"
	"time"
)

const todayObservationPauseStartedKey = "today_observation_pause_started"

// recordTodayObservationPauseStart runs only for a real control transition,
// never when a pause is restored at startup. The privacy log has different
// coverage semantics and cannot prove the start of an older pause.
func (a *App) recordTodayObservationPauseStart(paused bool, at time.Time) {
	if a == nil || a.store == nil {
		return
	}
	value := ""
	if paused {
		value = strconv.FormatInt(at.UnixMilli(), 10)
	}
	if err := a.store.SetUIState(context.Background(), todayObservationPauseStartedKey, value); err != nil {
		log.Printf("[observation] save pause start: %v", err)
	}
}

// GetTodayObservationPauseSince returns zero when the actual start is unknown.
func (a *App) GetTodayObservationPauseSince() (int64, error) {
	if a == nil || a.store == nil || a.closedEyes.policyPending.Load() || !a.IsClosedEyesModeEnabled() {
		return 0, nil
	}
	value, err := a.store.GetUIState(context.Background(), todayObservationPauseStartedKey)
	if err != nil {
		return 0, err
	}
	start, err := strconv.ParseInt(value, 10, 64)
	if err != nil || start <= 0 || a.closedEyes.policyPending.Load() || !a.IsClosedEyesModeEnabled() {
		return 0, nil
	}
	return start, nil
}

// observationPauseCoverageStart is a privacy exclusion boundary, not a claim
// about when the user paused. Startup may add a boundary for a restored pause.
func (a *App) observationPauseCoverageStart() (int64, error) {
	if a == nil || a.closedEyes.policyPending.Load() || !a.IsClosedEyesModeEnabled() {
		return 0, nil
	}
	pauses, err := a.observationPauses()
	if err != nil {
		return 0, err
	}
	if a.closedEyes.policyPending.Load() || !a.IsClosedEyesModeEnabled() {
		return 0, nil
	}
	for i := len(pauses) - 1; i >= 0; i-- {
		if pauses[i].End == 0 && pauses[i].Start > 0 {
			return pauses[i].Start, nil
		}
	}
	return 0, nil
}
