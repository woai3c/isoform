package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSMSRepliesKeepTheNewestAndCascade(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	hubID := newHub(t, s)
	now := time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)
	for i := 0; i < KeptSMSReplies+5; i++ {
		reply := SMSReply{ID: uuid.NewString(), HubID: hubID, BodyCT: fmt.Sprintf("ct-%d", i), ReceivedAt: now.Add(time.Duration(i) * time.Second)}
		if err := s.AddSMSReply(ctx, reply, now.Add(24*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	replies, err := s.SMSRepliesOf(ctx, hubID, now, 100)
	if err != nil || len(replies) != KeptSMSReplies || replies[0].BodyCT != "ct-5" {
		t.Fatalf("kept %d replies, first %v, err %v", len(replies), replies[0].BodyCT, err)
	}
	if err = s.AddSMSReply(ctx, SMSReply{ID: uuid.NewString(), HubID: uuid.NewString(), BodyCT: "x", ReceivedAt: now}, now.Add(time.Hour)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("reply for an unknown hub: %v", err)
	}
	if _, err = s.db.ExecContext(ctx, "DELETE FROM hubs WHERE id=?", hubID); err != nil {
		t.Fatal(err)
	}
	if replies, _ = s.SMSRepliesOf(ctx, hubID, now, 100); len(replies) != 0 {
		t.Fatal("replies outlived their hub")
	}
}

func TestSMSSweep(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	hubID, pendingHub := newHub(t, s), newHub(t, s)
	now := time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)
	if err := s.AddSMSReply(ctx, SMSReply{ID: uuid.NewString(), HubID: hubID, BodyCT: "ct", ReceivedAt: now}, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordSMSInbound(ctx, "old", now); err != nil {
		t.Fatal(err)
	}
	if err := s.StartSMSVerification(ctx, SMSVerification{HubID: pendingHub, NumberHash: "number", NumberCT: "ct", CodeHash: "code", ExpiresAt: now.Add(10 * time.Minute)}, now); err != nil {
		t.Fatal(err)
	}
	if ok, _, err := s.TakeSMSLimit(ctx, "codes", 5, time.Hour, now); err != nil || !ok {
		t.Fatalf("limit %t %v", ok, err)
	}
	if _, err := s.db.ExecContext(ctx, "INSERT INTO sms_usage(number_hash,day_key,day_count,month_key,month_count) VALUES('a','2026-08-01',1,'2026-08',1),('b','2026-09-30',1,'2026-09',1)"); err != nil {
		t.Fatal(err)
	}
	// Two hours on: the reply expired, the limit window ended, August usage is old.
	swept, err := s.Sweep(ctx, now.Add(2*time.Hour))
	if err != nil || swept.SMS != 3 {
		t.Fatalf("first sweep %+v %v", swept, err)
	}
	if _, err = s.SMSVerificationOf(ctx, pendingHub); err != nil {
		t.Fatal("a verification was removed before its keep time")
	}
	// Past the inbound keep: the message id, the verification and September usage.
	swept, err = s.Sweep(ctx, now.Add(SMSInboundKeep+time.Hour))
	if err != nil || swept.SMS != 3 {
		t.Fatalf("second sweep %+v %v", swept, err)
	}
	if fresh, _ := s.RecordSMSInbound(ctx, "old", now); !fresh {
		t.Fatal("a swept message id is still remembered")
	}
	if _, err = s.SMSVerificationOf(ctx, pendingHub); !errors.Is(err, ErrNotFound) {
		t.Fatal("a stale verification survived")
	}
}

func TestSMSConfirmMovesTheNumber(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	first, second := newHub(t, s), newHub(t, s)
	now := time.Now()
	match := func(_, codeHash string) bool { return codeHash == "code" }
	for _, hubID := range []string{first, second} {
		if err := s.StartSMSVerification(ctx, SMSVerification{HubID: hubID, NumberHash: "n1", NumberCT: "ct-" + hubID, CodeHash: "code", ExpiresAt: now.Add(time.Minute)}, now); err != nil {
			t.Fatal(err)
		}
		sub, displaced, err := s.ConfirmSMS(ctx, hubID, match, 5, 10, now)
		if err != nil || sub.State != SMSActive || sub.NumberCT != "ct-"+hubID {
			t.Fatalf("confirm %+v %v", sub, err)
		}
		if hubID == second && strings.Join(displaced, ",") != first {
			t.Fatalf("displaced %v", displaced)
		}
	}
	if _, err := s.SMSSubscriptionOf(ctx, first); !errors.Is(err, ErrNotFound) {
		t.Fatal("two hubs hold one number")
	}
	if _, _, err := s.ConfirmSMS(ctx, second, match, 5, 10, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a used verification confirmed again: %v", err)
	}
}

func TestSMSOptOutRemovesCodesAndStopsSubscriptions(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	verified, pending := newHub(t, s), newHub(t, s)
	now := time.Now()
	start := func(hubID string) error {
		return s.StartSMSVerification(ctx, SMSVerification{HubID: hubID, NumberHash: "n1", NumberCT: "ct", CodeHash: "code", ExpiresAt: now.Add(time.Minute)}, now)
	}
	if err := start(verified); err != nil {
		t.Fatal(err)
	}
	match := func(_, codeHash string) bool { return codeHash == "code" }
	if _, _, err := s.ConfirmSMS(ctx, verified, match, 5, 10, now); err != nil {
		t.Fatal(err)
	}
	if err := start(pending); err != nil {
		t.Fatal(err)
	}
	change, err := s.SMSOptOut(ctx, "n1", now)
	if err != nil || !change.Changed || !change.Known || len(change.Hubs) != 2 {
		t.Fatalf("opt-out %+v %v", change, err)
	}
	if sub, _ := s.SMSSubscriptionOf(ctx, verified); sub.State != SMSStopped {
		t.Fatalf("verified state %q", sub.State)
	}
	if _, err = s.SMSVerificationOf(ctx, pending); !errors.Is(err, ErrNotFound) {
		t.Fatal("a code survived STOP")
	}
	if err = start(pending); !errors.Is(err, ErrOptedOut) {
		t.Fatalf("verification of an opted-out number: %v", err)
	}
	if change, _ = s.SMSOptOut(ctx, "n1", now); change.Changed || !change.Known || len(change.Hubs) != 0 {
		t.Fatalf("second STOP %+v", change)
	}
	change, err = s.SMSOptIn(ctx, "n1", now)
	if err != nil || !change.Changed || strings.Join(change.Hubs, ",") != verified {
		t.Fatalf("opt-in %+v %v", change, err)
	}
	if sub, _ := s.SMSSubscriptionOf(ctx, verified); sub.State != SMSActive {
		t.Fatalf("state after START %q", sub.State)
	}
	if change, _ = s.SMSOptOut(ctx, "stranger", now); !change.Changed || change.Known {
		t.Fatalf("STOP from a stranger %+v", change)
	}
}

func TestSMSLimitWindows(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)
	for i := 0; i < 2; i++ {
		if ok, _, err := s.TakeSMSLimit(ctx, "k", 2, time.Hour, now.Add(time.Duration(i)*time.Minute)); err != nil || !ok {
			t.Fatalf("take %d: %t %v", i, ok, err)
		}
	}
	ok, wait, err := s.TakeSMSLimit(ctx, "k", 2, time.Hour, now.Add(30*time.Minute))
	if err != nil || ok || wait != 30*time.Minute {
		t.Fatalf("over the limit: %t %s %v", ok, wait, err)
	}
	if ok, _, _ = s.TakeSMSLimit(ctx, "k", 2, time.Hour, now.Add(time.Hour)); !ok {
		t.Fatal("a new window was refused")
	}
}

func TestSweepRemovesExpiredEmailCodes(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)
	hubID := "5f0f5c6e-6a0e-4c58-8b3b-0f6d2a3c9e11"
	if err := st.AuthorizeHub(ctx, hubID, strings.Repeat("a", 64), now); err != nil {
		t.Fatal(err)
	}
	if err := st.StartEmailVerification(ctx, EmailVerification{HubID: hubID, AddressHash: "h", AddressCT: "c", CodeHash: "x", ExpiresAt: now.Add(10 * time.Minute)}, now); err != nil {
		t.Fatal(err)
	}
	if swept, err := st.Sweep(ctx, now.Add(time.Hour)); err != nil || swept.Email != 0 {
		t.Fatalf("a code expired an hour ago is kept a day: %+v %v", swept, err)
	}
	if swept, err := st.Sweep(ctx, now.Add(25*time.Hour)); err != nil || swept.Email != 1 {
		t.Fatalf("sweep after a day: %+v %v", swept, err)
	}
	if _, err := st.EmailVerificationOf(ctx, hubID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("verification after the sweep: %v", err)
	}
}
