package handoff

// CanTransition is the lifecycle contract for persisted one-time work. Repeated
// states permit metadata/activity updates; completed entries cannot be restarted,
// and an aborted entry restarts only through Start again (back to in_progress).
// in_progress can return to its prior waiting state only when startup rolls back
// before execution has begun (for example, if session creation fails).
func CanTransition(from, to Status) bool {
	if !validStatus(from) || !validStatus(to) {
		return false
	}
	if from == to {
		return true
	}
	switch from {
	case StatusActive:
		return to == StatusInProgress || to == StatusAborted || to == StatusStale
	case StatusInProgress:
		return to == StatusActive || to == StatusStale || to == StatusBlocked ||
			to == StatusNeedsReview || to == StatusCompleted || to == StatusAborted
	case StatusBlocked, StatusNeedsReview, StatusStale:
		return to == StatusInProgress || to == StatusAborted
	case StatusAborted:
		return to == StatusInProgress
	case StatusCompleted:
		return false
	}
	return false
}

func validStatus(status Status) bool {
	switch status {
	case StatusActive, StatusInProgress, StatusNeedsReview, StatusBlocked, StatusCompleted, StatusAborted, StatusStale:
		return true
	}
	return false
}
