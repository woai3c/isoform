package apnspush

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"isoform-relay/internal/push"
	"isoform-relay/internal/store"
	"isoform-relay/internal/wire"
)

// Worker tests use the real durable store and a synthetic sender. They do not
// assert any APNs integration or delivery.
type senderFunc func(context.Context, push.Claim) (push.Outcome, error)

func (f senderFunc) Send(ctx context.Context, claim push.Claim) (push.Outcome, error) {
	return f(ctx, claim)
}

func randomText(t *testing.T) string {
	t.Helper()
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

// pairDevice runs the real pairing store path and returns the approved device.
func pairDevice(t *testing.T, st *store.Store, hubID string) string {
	t.Helper()
	ctx := context.Background()
	now := time.Now()
	if err := st.AuthorizeHub(ctx, hubID, wire.Digest(randomText(t)), now); err != nil {
		t.Fatal(err)
	}
	pairingID, claimToken, deviceID := uuid.NewString(), randomText(t), uuid.NewString()
	if err := st.OpenPairing(ctx, hubID, pairingID, wire.Digest(claimToken), now.Add(10*time.Minute), now); err != nil {
		t.Fatal(err)
	}
	claim := store.Claim{Hub: hubID, TokenHash: wire.Digest(claimToken), DeviceID: deviceID, DevicePub: randomText(t), CT: randomText(t)}
	if _, err := st.ClaimPairing(ctx, pairingID, claim, now); err != nil {
		t.Fatal(err)
	}
	approval := store.Approval{PairingID: pairingID, DeviceID: deviceID, BearerHash: wire.Digest(randomText(t)), Scopes: []string{"brief:read", "push"}, ExpiresAt: now.Add(24 * time.Hour), CT: randomText(t)}
	if err := st.ApprovePairing(ctx, hubID, approval, now); err != nil {
		t.Fatal(err)
	}
	return deviceID
}

// seedBrief stores a notifying brief for the device through the real store.
func seedBrief(t *testing.T, st *store.Store, hubID, deviceID string, seq uint64) {
	t.Helper()
	raw := make([]byte, 48)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	env := wire.Envelope{V: 1, Hub: hubID, Dev: deviceID, Dir: "h2d", Seq: seq, Kind: "brief", CT: base64.RawURLEncoding.EncodeToString(raw)}
	body, err := env.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err = st.PutBrief(context.Background(), hubID, env, body, wire.FormatTime(now), true, now); err != nil {
		t.Fatal(err)
	}
}

func queuedStore(t *testing.T) (*store.Store, string, string) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "relay.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	hubID := uuid.NewString()
	deviceID := pairDevice(t, st, hubID)
	if _, err = st.RegisterPush(context.Background(), deviceID, strings.Repeat("aa", 32), "development", time.Now().Add(-5*time.Second)); err != nil {
		t.Fatal(err)
	}
	seedBrief(t, st, hubID, deviceID, 1)
	return st, hubID, deviceID
}

func TestWorkerRecordsAcceptanceWithoutSendingTwice(t *testing.T) {
	st, _, _ := queuedStore(t)
	calls := 0
	worker := Worker{Outbox: st, Sender: senderFunc(func(context.Context, push.Claim) (push.Outcome, error) {
		calls++
		return push.Accepted, nil
	})}
	if worked, err := worker.processOne(context.Background()); !worked || err != nil {
		t.Fatalf("dispatch failed: %v", err)
	}
	if worked, err := worker.processOne(context.Background()); worked || err != nil {
		t.Fatalf("repeated accepted push: %v", err)
	}
	if calls != 1 {
		t.Fatal("duplicate confirmed request")
	}
}

func TestPayloadCarriesOnlyRoutingFields(t *testing.T) {
	st, hubID, _ := queuedStore(t)
	var sent push.Claim
	worker := Worker{Outbox: st, Sender: senderFunc(func(_ context.Context, claim push.Claim) (push.Outcome, error) {
		sent = claim
		return push.Accepted, nil
	})}
	if _, err := worker.processOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sent.HubID != hubID || sent.EventID != "1" || sent.Environment != "development" || sent.Token != strings.Repeat("aa", 32) {
		t.Fatalf("claim does not carry the routing scope: %+v", sent)
	}
}

func TestRevocationWhileSendingFencesResponse(t *testing.T) {
	st, hubID, deviceID := queuedStore(t)
	var sent push.Claim
	worker := Worker{Outbox: st, Sender: senderFunc(func(ctx context.Context, claim push.Claim) (push.Outcome, error) {
		sent = claim
		if _, err := st.RevokeDevice(ctx, hubID, deviceID); err != nil {
			t.Fatal(err)
		}
		return push.Accepted, nil
	})}
	if _, err := worker.processOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := st.FinishPush(context.Background(), sent, push.Accepted, time.Now()); !errors.Is(err, push.ErrLeaseLost) {
		t.Fatalf("late acceptance escaped revocation fence: %v", err)
	}
	if next, err := st.ClaimPush(context.Background(), time.Now().Add(time.Hour), time.Minute); err != nil || next != nil {
		t.Fatal("revoked push retried")
	}
}

func TestProviderConfigurationStopsQueueUntilRestart(t *testing.T) {
	st, hubID, deviceID := queuedStore(t)
	seedBrief(t, st, hubID, deviceID, 2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	called := make(chan struct{}, 1)
	worker := Worker{Outbox: st, Sender: senderFunc(func(context.Context, push.Claim) (push.Outcome, error) {
		calls.Add(1)
		select {
		case called <- struct{}{}:
		default:
		}
		return push.Retry, &configurationError{}
	})}
	done := make(chan struct{})
	go func() {
		defer close(done)
		worker.Run(ctx)
	}()
	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("first dispatch did not run")
	}
	select {
	case <-called:
		t.Fatal("bad provider credentials drained another queued notification")
	case <-time.After(250 * time.Millisecond):
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("paused worker did not stop")
	}
	if calls.Load() != 1 {
		t.Fatal("configuration failure sent multiple requests")
	}
}

func TestProviderCooldownRetainsDelayedRetry(t *testing.T) {
	st, _, _ := queuedStore(t)
	var sent push.Claim
	worker := Worker{Outbox: st, Sender: senderFunc(func(_ context.Context, claim push.Claim) (push.Outcome, error) {
		sent = claim
		return push.Retry, &providerCooldownError{}
	})}
	_, err := worker.processOne(context.Background())
	var cooldown *providerCooldownError
	if !errors.As(err, &cooldown) {
		t.Fatal("provider-wide cooldown was lost")
	}
	if next, err := st.ClaimPush(context.Background(), time.Now().Add(time.Minute), time.Minute); err != nil || next != nil {
		t.Fatal("retry was not delayed")
	}
	next, err := st.ClaimPush(context.Background(), time.Now().Add(16*time.Minute), time.Minute)
	if err != nil || next == nil || next.NotificationID != sent.NotificationID || next.Attempts != 2 {
		t.Fatal("durable retry identity was lost")
	}
}

func TestTokenInvalidRemovesOnlyThatRegistration(t *testing.T) {
	st, hubID, deviceID := queuedStore(t)
	worker := Worker{Outbox: st, Sender: senderFunc(func(context.Context, push.Claim) (push.Outcome, error) {
		return push.TokenInvalid, errors.New("token rejected")
	})}
	if _, err := worker.processOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	seedBrief(t, st, hubID, deviceID, 2)
	if next, err := st.ClaimPush(context.Background(), time.Now(), time.Minute); err != nil || next != nil {
		t.Fatal("notifications still queued after the token was declared invalid")
	}
}
