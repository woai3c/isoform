package openanswer

import (
	"testing"
	"time"
)

func TestCheckEligibility(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	base := Candidate{Question: "When will we know the result?", Topic: "Election", Settles: "result", Source: "Results", NotYet: true, WindowStart: now.Add(time.Hour), WindowEnd: now.Add(2 * time.Hour)}
	tests := []struct {
		name                string
		change              func(*Candidate)
		checkable, promised bool
		open                int
		want                Eligibility
	}{
		{"result with time", func(*Candidate) {}, true, false, 0, Eligible},
		{"complete answer", func(c *Candidate) { c.Question = "What is a runoff?"; c.NotYet = false }, true, false, 0, Ineligible},
		{"eight days", func(c *Candidate) { c.WindowStart = now.Add(8 * 24 * time.Hour); c.WindowEnd = c.WindowStart }, true, false, 0, OfferReminder},
		{"seven days", func(c *Candidate) { c.WindowStart = now.Add(7 * 24 * time.Hour); c.WindowEnd = c.WindowStart }, true, false, 0, Eligible},
		{"uncheckable source", func(*Candidate) {}, false, false, 0, Ineligible},
		{"already promised", func(*Candidate) {}, true, true, 0, Ineligible},
		{"five open", func(*Candidate) {}, true, false, 5, AskAtLimit},
		{"event", func(c *Candidate) {
			c.WindowStart = time.Time{}
			c.WindowEnd = time.Time{}
			c.WatchedEvent = "mail:reply"
		}, true, false, 0, Eligible},
		{"event and window", func(c *Candidate) { c.WatchedEvent = "mail:reply" }, true, false, 0, Ineligible},
		{"no event or time", func(c *Candidate) { c.WindowStart = time.Time{}; c.WindowEnd = time.Time{} }, true, false, 0, Ineligible},
		{"invalid settle kind", func(c *Candidate) { c.Settles = "forecast" }, true, false, 0, Ineligible},
		{"backwards window", func(c *Candidate) { c.WindowEnd = now }, true, false, 0, Ineligible},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := base
			tt.change(&candidate)
			if got := CheckEligibility(candidate, now, tt.checkable, tt.promised, tt.open); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDecide(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	base := Record{State: StateOpen, WindowStart: now, ExpiresAt: now.Add(time.Hour), Baseline: "40%"}
	tests := []struct {
		name        string
		record      Record
		observation Observation
		at          time.Time
		want        Decision
	}{
		{"settled", base, Observation{Settled: true}, now, PostSettled},
		{"saw it", base, Observation{Settled: true, SawSettledOnScreen: true}, now, MarkSawIt},
		{"no news", base, Observation{NewValue: "40%"}, now, NoChange},
		{"new value with empty baseline", Record{State: StateOpen, WindowStart: now}, Observation{NewValue: "41%"}, now, PostInterim},
		{"news at window", base, Observation{NewValue: "41%"}, now, PostInterim},
		{"news before window", base, Observation{NewValue: "41%"}, now.Add(-time.Second), NoChange},
		{"one interim", Record{State: StateOpen, WindowStart: now, Posts: 1, InterimPosted: true, Baseline: "40%"}, Observation{NewValue: "42%"}, now, NoChange},
		{"no second interim if receipt missing", Record{State: StateOpen, WindowStart: now, Posts: 1, Baseline: "40%"}, Observation{NewValue: "42%"}, now, NoChange},
		{"settle after interim", Record{State: StateOpen, Posts: 1, InterimPosted: true}, Observation{Settled: true}, now, PostSettled},
		{"give up", base, Observation{}, now.Add(time.Hour), PostGaveUp},
		{"two post cap", Record{State: StateOpen, Posts: 2}, Observation{Settled: true}, now, NoChange},
		{"cancelled", Record{State: StateCancelled}, Observation{Settled: true}, now, NoChange},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Decide(tt.record, tt.observation, tt.at); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCheckHorizon(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	start, end := now.Add(time.Hour), now.Add(2*time.Hour)
	if got := Expiry(Candidate{WindowEnd: end}, now); !got.Equal(end.Add(24 * time.Hour)) {
		t.Fatalf("window expiry %s", got)
	}
	if got := Expiry(Candidate{WatchedEvent: "mail:reply"}, now); !got.Equal(now.Add(7 * 24 * time.Hour)) {
		t.Fatalf("event expiry %s", got)
	}
	expiry := end.Add(24 * time.Hour)
	for _, tt := range []struct {
		name           string
		last, at, want time.Time
	}{
		{"first", time.Time{}, now, start},
		{"never before window", now, now, start},
		{"subsequent", start, start, start.Add(10 * time.Minute)},
		{"late restart", start, start.Add(time.Hour), start.Add(time.Hour)},
		{"expired", start, expiry, time.Time{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := NextCheck(start, expiry, tt.last, tt.at); !got.Equal(tt.want) {
				t.Fatalf("got %s, want %s", got, tt.want)
			}
		})
	}
}
