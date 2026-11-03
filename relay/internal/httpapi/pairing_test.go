package httpapi

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"isoform-relay/internal/wire"
)

func TestPairingApproveLifecycle(t *testing.T) {
	r := newRelay(t, "")
	hc := r.connectHub(uuid.NewString(), randomToken(t))
	dev := r.pairDevice(hc, []string{"brief:read", "presence:read"})
	// The approval is served once: a second poll reports approved, without it.
	if again := r.pollPairing(invitation{pairingID: dev.pairingID, claimToken: dev.claimToken}); again.State != "approved" || again.ApprovalCT != "" {
		t.Fatalf("approval served twice: %+v", again)
	}
	r.expectStatus(r.getWith("/v1/presence", dev.bearer), http.StatusOK, "approved device presence")
	r.expectStatus(r.getWith("/v1/presence", randomToken(t)), http.StatusUnauthorized, "unknown bearer")
}

func TestPairingDeclineAndSupersede(t *testing.T) {
	r := newRelay(t, "")
	hubID := uuid.NewString()
	hc := r.connectHub(hubID, randomToken(t))
	first := r.openPairing(hc)
	second := r.openPairing(hc)
	deviceID, devicePub := uuid.NewString(), randomToken(t)
	r.expectStatus(r.claimFrom(first, hubID, deviceID, first.claimToken, devicePub, randomCT(t, 200)), http.StatusNotFound, "claim on superseded invitation")
	r.expectStatus(r.claimFrom(second, hubID, deviceID, second.claimToken, devicePub, randomCT(t, 200)), http.StatusAccepted, "claim on current invitation")
	hc.recvType("pairing_claim")
	hc.send(map[string]any{"t": "pairing_decline", "pairing": second.pairingID})
	if poll := r.pollPairing(second); poll.State != "declined" || poll.ApprovalCT != "" {
		t.Fatalf("declined pairing poll %+v", poll)
	}
	r.expectStatus(r.claimFrom(second, hubID, uuid.NewString(), second.claimToken, devicePub, randomCT(t, 200)), http.StatusConflict, "claim after decline")
}

func TestPairingExpiryUsesRelayClock(t *testing.T) {
	r := newRelay(t, "")
	hubID := uuid.NewString()
	hc := r.connectHub(hubID, randomToken(t))
	stale := r.openPairing(hc)
	claimed := r.openPairing(hc)
	deviceID := uuid.NewString()
	r.expectStatus(r.claimFrom(claimed, hubID, deviceID, claimed.claimToken, randomToken(t), randomCT(t, 200)), http.StatusAccepted, "claim before expiry")
	hc.recvType("pairing_claim")
	// The first invitation was superseded, so only the claimed one can expire.
	r.advance(11 * time.Minute)
	if poll := r.pollPairing(claimed); poll.State != "expired" {
		t.Fatalf("claimed pairing after expiry: %+v", poll)
	}
	r.expectStatus(r.claimFrom(stale, hubID, uuid.NewString(), stale.claimToken, randomToken(t), randomCT(t, 200)), http.StatusNotFound, "claim on superseded invitation")
	fresh := r.openPairing(hc)
	r.advance(11 * time.Minute)
	r.expectStatus(r.claimFrom(fresh, hubID, uuid.NewString(), fresh.claimToken, randomToken(t), randomCT(t, 200)), http.StatusGone, "claim after expiry")
}

func TestClaimTokenRejectedAndRateLimited(t *testing.T) {
	r := newRelay(t, "")
	hubID := uuid.NewString()
	hc := r.connectHub(hubID, randomToken(t))
	inv := r.openPairing(hc)
	wrong := randomToken(t)
	for range 10 {
		r.expectStatus(r.claimFrom(inv, hubID, uuid.NewString(), wrong, randomToken(t), randomCT(t, 200)), http.StatusForbidden, "wrong claim token")
	}
	limited := r.claimFrom(inv, hubID, uuid.NewString(), inv.claimToken, randomToken(t), randomCT(t, 200))
	r.expectStatus(limited, http.StatusTooManyRequests, "eleventh claim attempt")
	if limited.Header.Get("Retry-After") == "" {
		t.Fatal("rate limit omitted Retry-After")
	}
}

func TestClaimIsIdempotentAndSingleUse(t *testing.T) {
	r := newRelay(t, "")
	hubID := uuid.NewString()
	hc := r.connectHub(hubID, randomToken(t))
	inv := r.openPairing(hc)
	deviceID, devicePub, ct := uuid.NewString(), randomToken(t), randomCT(t, 200)
	r.expectStatus(r.claimFrom(inv, hubID, deviceID, inv.claimToken, devicePub, ct), http.StatusAccepted, "first claim")
	r.expectStatus(r.claimFrom(inv, hubID, deviceID, inv.claimToken, devicePub, ct), http.StatusAccepted, "identical retry")
	r.expectStatus(r.claimFrom(inv, hubID, uuid.NewString(), inv.claimToken, randomToken(t), randomCT(t, 200)), http.StatusConflict, "different device")
	hc.recvType("pairing_claim")
	other := r.openPairing(hc)
	r.expectStatus(r.claimFrom(other, uuid.NewString(), uuid.NewString(), other.claimToken, randomToken(t), randomCT(t, 200)), http.StatusNotFound, "claim naming another hub")
	r.expectStatus(r.claimFrom(other, hubID, uuid.NewString(), other.claimToken, randomToken(t), randomCT(t, 200)), http.StatusAccepted, "claim after a mismatched attempt")
}

func TestPollRequiresClaimToken(t *testing.T) {
	r := newRelay(t, "")
	hc := r.connectHub(uuid.NewString(), randomToken(t))
	inv := r.openPairing(hc)
	path := "/v1/pairings/" + inv.pairingID
	r.expectStatus(r.call(http.MethodGet, path, nil, nil), http.StatusUnauthorized, "missing claim header")
	r.expectStatus(r.call(http.MethodGet, path, http.Header{"X-Yansu-Claim": {"short"}}, nil), http.StatusUnauthorized, "malformed claim header")
	r.expectStatus(r.call(http.MethodGet, path, http.Header{"X-Yansu-Claim": {randomToken(t)}}, nil), http.StatusForbidden, "wrong claim header")
	r.expectStatus(r.call(http.MethodGet, path, http.Header{"X-Yansu-Claim": {inv.claimToken}}, nil), http.StatusConflict, "poll before any claim")
}

func TestPollWakesOnApproval(t *testing.T) {
	r := newRelay(t, "")
	hubID := uuid.NewString()
	hc := r.connectHub(hubID, randomToken(t))
	inv := r.openPairing(hc)
	deviceID, bearerToken := uuid.NewString(), randomToken(t)
	r.expectStatus(r.claimFrom(inv, hubID, deviceID, inv.claimToken, randomToken(t), randomCT(t, 200)), http.StatusAccepted, "claim")
	hc.recvType("pairing_claim")
	type outcome struct {
		status int
		body   []byte
		err    error
	}
	done := make(chan outcome, 1)
	started := time.Now()
	go func() {
		status, body, err := rawGet(r.srv.URL+"/v1/pairings/"+inv.pairingID, "", http.Header{"X-Yansu-Claim": {inv.claimToken}})
		done <- outcome{status, body, err}
	}()
	time.Sleep(300 * time.Millisecond)
	approvalCT := randomCT(t, 200)
	hc.send(map[string]any{"t": "pairing_approve", "pairing": inv.pairingID, "deviceId": deviceID, "bearerHash": hexDigest(bearerToken), "scopes": []string{"brief:read"}, "expiresAt": rfc3339(time.Now().Add(time.Hour * 24 * 90)), "ct": approvalCT})
	select {
	case got := <-done:
		if got.err != nil || got.status != http.StatusOK || time.Since(started) > 10*time.Second {
			t.Fatalf("long poll: %d %v after %s", got.status, got.err, time.Since(started))
		}
		if !containsText(got.body, approvalCT) {
			t.Fatalf("approval missing from woken poll: %s", got.body)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("long poll did not wake on approval")
	}
}

func TestPairingCloseCancelsOnlyAnOpenInvitation(t *testing.T) {
	r := newRelay(t, "")
	hubID := uuid.NewString()
	hc := r.connectHub(hubID, randomToken(t))
	open := r.openPairing(hc)
	hc.send(map[string]any{"t": "pairing_close", "pairing": open.pairingID})
	hc.barrier()
	r.expectStatus(r.claimFrom(open, hubID, uuid.NewString(), open.claimToken, randomToken(t), randomCT(t, 200)), http.StatusNotFound, "claim after close")

	claimed := r.openPairing(hc)
	r.expectStatus(r.claimFrom(claimed, hubID, uuid.NewString(), claimed.claimToken, randomToken(t), randomCT(t, 200)), http.StatusAccepted, "claim")
	hc.recvType("pairing_claim")
	hc.send(map[string]any{"t": "pairing_close", "pairing": claimed.pairingID})
	hc.barrier()
	// An HTTP poll would long-poll while the state stays claimed, so read the
	// stored state directly.
	status, err := r.store.PollPairing(t.Context(), claimed.pairingID, wire.Digest(claimed.claimToken), r.now())
	if err != nil || status.State != "claimed" {
		t.Fatalf("closing a claimed invitation changed it: %+v %v", status, err)
	}
}
