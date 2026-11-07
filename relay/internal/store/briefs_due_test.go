package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"isoform-relay/internal/wire"
)

func TestHeldBriefIsHiddenUntilDueAndItsPushWaits(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	hubID := newHub(t, s)
	dev := seedDevice(t, s, hubID, time.Now().Add(72*time.Hour))
	if _, err := s.RegisterPush(ctx, dev.ID, strings.Repeat("71", 32), "development", time.Now()); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	dueAt := now.Add(2 * time.Hour)
	put := func(seq uint64, notify bool, deliverAt time.Time) {
		t.Helper()
		env := wire.Envelope{V: 1, Hub: hubID, Dev: dev.ID, Dir: "h2d", Seq: seq, Kind: "brief", CT: ct(t, 300)}
		body, _ := env.Canonical()
		if err := s.PutBriefDueAt(ctx, hubID, env, body, wire.FormatTime(now), notify, now, deliverAt); err != nil {
			t.Fatal(err)
		}
	}
	put(1, false, time.Time{})
	put(2, true, dueAt)

	// The newer held brief does not hide the older brief that is due now.
	latest, err := s.LatestBrief(ctx, dev.ID, now)
	if err != nil || latest.Version != 1 {
		t.Fatalf("latest before the due time %+v %v, want version 1", latest, err)
	}
	if listed, err := s.ListBriefs(ctx, dev.ID, 7, now); err != nil || len(listed) != 1 {
		t.Fatalf("listed before the due time %d (%v), want only version 1", len(listed), err)
	}
	if claim, err := s.ClaimPush(ctx, now.Add(time.Minute), time.Minute); err != nil || claim != nil {
		t.Fatalf("held brief's push was claimed early: %+v %v", claim, err)
	}

	// At the due time the brief is visible and its push is claimable.
	latest, err = s.LatestBrief(ctx, dev.ID, dueAt.Add(-time.Second))
	if err != nil || latest.Version != 1 {
		t.Fatalf("one second before the due time %+v %v, want version 1", latest, err)
	}
	latest, err = s.LatestBrief(ctx, dev.ID, dueAt)
	if err != nil || latest.Version != 2 {
		t.Fatalf("at the due time %+v %v, want version 2", latest, err)
	}
	claim, err := s.ClaimPush(ctx, dueAt, time.Minute)
	if err != nil || claim == nil {
		t.Fatalf("no push at the due time: %v", err)
	}
	if claim.Kind != "brief" || claim.EventID != "2" {
		t.Fatalf("claim %+v, want the brief push for version 2", claim)
	}
}

func TestImmediateBriefIsVisibleWhateverClockTheReadUses(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	hubID := newHub(t, s)
	dev := seedDevice(t, s, hubID, time.Now().Add(24*time.Hour))
	stored := time.Now()
	env := wire.Envelope{V: 1, Hub: hubID, Dev: dev.ID, Dir: "h2d", Seq: 1, Kind: "brief", CT: ct(t, 300)}
	body, _ := env.Canonical()
	if err := s.PutBrief(ctx, hubID, env, body, wire.FormatTime(stored), false, stored); err != nil {
		t.Fatal(err)
	}
	// A reader whose clock is behind the writer still sees a brief that is not held.
	if _, err := s.LatestBrief(ctx, dev.ID, stored.Add(-time.Hour)); err != nil {
		t.Fatalf("immediate brief hidden from an earlier clock: %v", err)
	}
}
