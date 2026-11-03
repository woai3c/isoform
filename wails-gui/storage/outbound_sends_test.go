package storage

import (
	"context"
	"errors"
	"testing"
)

func outboundSendFixture() OutboundSendRow {
	return OutboundSendRow{
		MomentID: "moment-1", Revision: "revision-1", Channel: "mail", PrincipalID: "person-1",
		Recipient: "Dana", DraftTo: "dana@example.test", Subject: "Re: Plan", Text: "Sounds good.",
		SourceKey: "waiting_on_reply:person-1:thread-1",
	}
}

func TestOutboundSendLifecycleAndSnapshotDedupe(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	fixture := outboundSendFixture()
	claimed, ok, err := s.ClaimOutboundSend(ctx, fixture, 100)
	if err != nil || !ok || claimed.State != OutboundSendSending || claimed.CreatedAt != 100 {
		t.Fatalf("first claim = %+v, %v, %v", claimed, ok, err)
	}
	again, ok, err := s.ClaimOutboundSend(ctx, fixture, 200)
	if err != nil || ok || again.State != OutboundSendSending {
		t.Fatalf("sending claim = %+v, %v, %v", again, ok, err)
	}
	changed := fixture
	changed.Text = "Changed"
	if _, _, err := s.ClaimOutboundSend(ctx, changed, 200); !errors.Is(err, ErrOutboundSendSnapshotChanged) {
		t.Fatalf("snapshot conflict = %v", err)
	}
	if err := s.MarkOutboundSendAccepted(ctx, fixture.MomentID, fixture.Revision, 300, 400); err != nil {
		t.Fatal(err)
	}
	accepted, ok, err := s.ClaimOutboundSend(ctx, fixture, 500)
	if err != nil || ok || accepted.State != OutboundSendAccepted || accepted.SentAt != 300 || accepted.FollowupAt != 400 {
		t.Fatalf("accepted claim = %+v, %v, %v", accepted, ok, err)
	}
	if err := s.MarkOutboundSendFinalized(ctx, fixture.MomentID, fixture.Revision); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkOutboundSendFinalized(ctx, fixture.MomentID, fixture.Revision); err != nil {
		t.Fatal(err)
	}
	waiting, err := s.ListOutboundSendsWaiting(ctx)
	if err != nil || len(waiting) != 1 || waiting[0].State != OutboundSendFinalized {
		t.Fatalf("waiting = %+v, %v", waiting, err)
	}
	if err := s.RetireOutboundSendFollowup(ctx, fixture.MomentID, fixture.Revision); err != nil {
		t.Fatal(err)
	}
	waiting, err = s.ListOutboundSendsWaiting(ctx)
	if err != nil || len(waiting) != 0 {
		t.Fatalf("retired waiting = %+v, %v", waiting, err)
	}
}

func TestOutboundSendOnlyDefinitiveFailureCanRetry(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for _, unknown := range []bool{false, true} {
		fixture := outboundSendFixture()
		fixture.Revision = "failed"
		if unknown {
			fixture.Revision = "unknown"
		}
		if _, ok, err := s.ClaimOutboundSend(ctx, fixture, 100); err != nil || !ok {
			t.Fatalf("claim %s: %v %v", fixture.Revision, ok, err)
		}
		if err := s.MarkOutboundSendFailed(ctx, fixture.MomentID, fixture.Revision, unknown); err != nil {
			t.Fatal(err)
		}
		stored, ok, err := s.ClaimOutboundSend(ctx, fixture, 200)
		if err != nil {
			t.Fatal(err)
		}
		if unknown && (ok || stored.State != OutboundSendUnknown) {
			t.Fatalf("unknown retried: %+v %v", stored, ok)
		}
		if !unknown && (!ok || stored.State != OutboundSendSending) {
			t.Fatalf("definitive failure not retryable: %+v %v", stored, ok)
		}
	}
}

func TestSingleOutboundSendRejectsEditedReplay(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	first := outboundSendFixture()
	second := first
	second.Revision, second.Text = "revision-2", "Changed text"
	if _, ok, err := s.ClaimSingleOutboundSend(ctx, first, 100); err != nil || !ok {
		t.Fatalf("first claim: %v %v", ok, err)
	}
	if _, ok, err := s.ClaimSingleOutboundSend(ctx, second, 101); err != nil || ok {
		t.Fatalf("a send in progress allowed an edited send: %v %v", ok, err)
	}
	if err := s.MarkOutboundSendFailed(ctx, first.MomentID, first.Revision, false); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.ClaimSingleOutboundSend(ctx, second, 102); err != nil || !ok {
		t.Fatalf("definitive failure prevented a corrected retry: %v %v", ok, err)
	}
	if err := s.MarkOutboundSendFailed(ctx, second.MomentID, second.Revision, true); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.ClaimSingleOutboundSend(ctx, first, 103); err != nil || ok {
		t.Fatalf("an unknown outcome allowed another send: %v %v", ok, err)
	}
}

func TestSingleOutboundSendConcurrentEditsReserveOnlyOne(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	first := outboundSendFixture()
	second := first
	second.Revision, second.Text = "revision-2", "A different edit"
	start := make(chan struct{})
	results := make(chan bool, 2)
	for _, row := range []OutboundSendRow{first, second} {
		go func(row OutboundSendRow) {
			<-start
			_, claimed, _ := s.ClaimSingleOutboundSend(ctx, row, 100)
			results <- claimed
		}(row)
	}
	close(start)
	claims := 0
	for range 2 {
		if <-results {
			claims++
		}
	}
	if claims != 1 {
		t.Fatalf("concurrent edits acquired %d send reservations", claims)
	}
}
