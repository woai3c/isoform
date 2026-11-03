package openanswer

import (
	"testing"
	"time"
)

func TestParseBlockAndEligibility(t *testing.T) {
	now := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	raw := `{"question":"When is the result?","topic":"elections","settles":"result","source":"results","notYet":true,"windowStart":"2026-10-26T16:30:00Z","windowEnd":"2026-10-26T17:00:00Z"}`
	c, err := ParseBlock(raw)
	if err != nil || CheckEligibility(c, now, true, false, 0) != Eligible {
		t.Fatalf("candidate=%+v err=%v", c, err)
	}
	if got := CheckEligibility(c, now, false, false, 0); got != Ineligible {
		t.Fatalf("unverified source = %s", got)
	}
	if got := CheckEligibility(c, now, true, false, 5); got != AskAtLimit {
		t.Fatalf("five open = %s", got)
	}
	c.WindowEnd = now.Add(8 * 24 * time.Hour)
	if got := CheckEligibility(c, now, true, false, 0); got != OfferReminder {
		t.Fatalf("eight days = %s", got)
	}
}

func TestParseBlockRejectsAmbiguousInput(t *testing.T) {
	for _, raw := range []string{
		`[]`, `{"question":"one","question":"two"}`, `{"unknown":true}`, `{"notYet":true} {}`,
		`{"question":"one","Question":"two"}`, `{"Question":"one","question":"two"}`,
		`{"question":"one","\u0071uestion":"two"}`, `{"NOTYET":true}`,
		`{"windowStart":"2026-10-26 16:30"}`, `{"watchedEvent":{"label":"reply"}}`,
	} {
		if _, err := ParseBlock(raw); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
}
