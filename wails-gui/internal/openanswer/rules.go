// Package openanswer contains the deterministic rules for answers that have
// not settled yet. It does not create promises or perform checks on its own.
package openanswer

import (
	"strings"
	"time"
)

const MaxOpen = 5

type Candidate struct {
	Question     string
	Topic        string
	Settles      string
	Source       string
	NotYet       bool
	WindowStart  time.Time
	WindowEnd    time.Time
	WatchedEvent string
}

type Eligibility string

const (
	Eligible      Eligibility = "eligible"
	Ineligible    Eligibility = "ineligible"
	OfferReminder Eligibility = "offer_reminder"
	AskAtLimit    Eligibility = "ask_at_limit"
)

// CheckEligibility requires a caller to establish that the answer is not yet
// final and that the named source and event can actually be checked. A model's
// assertions alone are not evidence of either condition.
func CheckEligibility(c Candidate, now time.Time, sourceCheckable, alreadyPromised bool, openCount int) Eligibility {
	if !sourceCheckable || alreadyPromised {
		return Ineligible
	}
	if result := CheckCandidate(c, now); result != Eligible {
		return result
	}
	if openCount >= MaxOpen {
		return AskAtLimit
	}
	return Eligible
}

// CheckCandidate rejects invalid model claims before retaining them. An
// Eligible result is only a shape and time check, not source evidence,
// privacy authorization, or a durable check.
func CheckCandidate(c Candidate, now time.Time) Eligibility {
	if !c.NotYet || strings.TrimSpace(c.Question) == "" || strings.TrimSpace(c.Topic) == "" || strings.TrimSpace(c.Source) == "" {
		return Ineligible
	}
	switch c.Settles {
	case "result", "reply", "status":
	default:
		return Ineligible
	}
	hasWindow := !c.WindowStart.IsZero() || !c.WindowEnd.IsZero()
	hasEvent := strings.TrimSpace(c.WatchedEvent) != ""
	if hasWindow == hasEvent {
		return Ineligible
	}
	if hasWindow {
		if c.WindowStart.IsZero() || c.WindowEnd.IsZero() || c.WindowEnd.Before(c.WindowStart) || c.WindowEnd.Before(now) {
			return Ineligible
		}
		if c.WindowEnd.After(now.Add(7 * 24 * time.Hour)) {
			return OfferReminder
		}
	}
	return Eligible
}

// Expiry gives a validated candidate its check horizon. The named window ends
// 24 hours before expiry; an event can be watched for at most seven days.
func Expiry(c Candidate, createdAt time.Time) time.Time {
	if !c.WindowEnd.IsZero() {
		return c.WindowEnd.Add(24 * time.Hour)
	}
	return createdAt.Add(7 * 24 * time.Hour)
}

// NextCheck is used only for a named window. A zero return means the record
// has expired or does not have a named time.
func NextCheck(windowStart, expiresAt, lastCheck, now time.Time) time.Time {
	if windowStart.IsZero() || expiresAt.IsZero() || !now.Before(expiresAt) {
		return time.Time{}
	}
	next := windowStart
	if !lastCheck.IsZero() {
		next = lastCheck.Add(10 * time.Minute)
	}
	if next.Before(windowStart) {
		next = windowStart
	}
	if next.Before(now) {
		next = now
	}
	if !next.Before(expiresAt) {
		return time.Time{}
	}
	return next
}

type State string

const (
	StateOpen      State = "open"
	StateSettled   State = "settled"
	StateSawIt     State = "saw_it"
	StateGaveUp    State = "gave_up"
	StateCancelled State = "cancelled"
)

type Record struct {
	State         State
	Posts         int
	WindowStart   time.Time
	ExpiresAt     time.Time
	Baseline      string
	InterimPosted bool
}

type Observation struct {
	Settled            bool
	SawSettledOnScreen bool
	NewValue           string
}

type Decision string

const (
	NoChange    Decision = "none"
	PostInterim Decision = "interim"
	PostSettled Decision = "settled"
	MarkSawIt   Decision = "saw_it"
	PostGaveUp  Decision = "gave_up"
)

// Decide returns a transition only; the caller must commit the transition and
// its post atomically, after rechecking privacy and quiet-hour delivery.
func Decide(r Record, o Observation, now time.Time) Decision {
	if r.State != StateOpen || r.Posts < 0 || r.Posts >= 2 {
		return NoChange
	}
	if o.Settled {
		if o.SawSettledOnScreen {
			return MarkSawIt
		}
		return PostSettled
	}
	if !r.ExpiresAt.IsZero() && !now.Before(r.ExpiresAt) {
		return PostGaveUp
	}
	if r.Posts == 0 && !r.WindowStart.IsZero() && !now.Before(r.WindowStart) && !r.InterimPosted && strings.TrimSpace(o.NewValue) != "" && strings.TrimSpace(o.NewValue) != strings.TrimSpace(r.Baseline) {
		return PostInterim
	}
	return NoChange
}
