package handoff

// Board groups a task can sit in on Tasks, Today and the phone.
const (
	BoardNone     = ""
	BoardNeedsYou = "needs_you"
	BoardRunning  = "running"
	BoardDone     = "done"
)

// Placement is where an entry belongs on the board and, for a run that stopped
// without finishing, why. Every surface reads it, so a task the person started
// never leaves the board without a line.
func Placement(e Entry) (group, cause string) {
	switch e.Status {
	case StatusActive, StatusNeedsReview:
		return BoardNeedsYou, ""
	case StatusInProgress:
		return BoardRunning, ""
	case StatusCompleted:
		return BoardDone, ""
	case StatusBlocked:
		switch e.BlockedReason {
		case BlockedInterrupted:
			return BoardNeedsYou, CauseRestarted
		case BlockedTimeout:
			return BoardNeedsYou, CauseTimeout
		}
		return BoardNeedsYou, stopCauseOrError(e.StopCause)
	case StatusAborted:
		switch e.StoppedBy {
		case StoppedByPerson:
			return BoardDone, ""
		case StoppedBySystem:
			return BoardNeedsYou, stopCauseOrError(e.StopCause)
		}
		// Aborted before stops were recorded: who stopped it is unknown, so it
		// is done and never claims the person stopped it.
		return BoardDone, ""
	case StatusStale:
		return BoardNone, ""
	}
	return BoardNone, ""
}

// StoppedUnrecorded reports an aborted row from before stops were recorded.
func StoppedUnrecorded(e Entry) bool {
	return e.Status == StatusAborted && e.StoppedBy == ""
}

// StoppedByYou reports whether the person stopped the task themselves.
func StoppedByYou(e Entry) bool {
	return e.Status == StatusAborted && e.StoppedBy == StoppedByPerson
}

func stopCauseOrError(cause string) string {
	switch cause {
	case CauseNoAssistant, CauseSignedOut, CauseRestarted, CauseTimeout, CauseAssistantError, CauseLoopGuard:
		return cause
	}
	return CauseAssistantError
}

// ClearStop forgets how the last attempt stopped, for Start again.
func (e *Entry) ClearStop() {
	e.StoppedBy, e.StopCause, e.StopAssistant, e.StoppedAt = "", "", "", 0
	if e.Thread != nil && e.Thread.Outcome != nil && e.Thread.Outcome.Kind == OutcomeStopped {
		e.Thread.Outcome = nil
	}
}
