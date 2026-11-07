package httpapi

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// A brief made ahead of its slot is held by the relay: the phone's latest read
// skips it until its time, and its push is due at that time.
func TestHeldBriefAppearsAtItsTimeAndPushesThen(t *testing.T) {
	r := newRelay(t, "development")
	hubID := uuid.NewString()
	hc := r.connectHub(hubID, randomToken(t))
	dev := r.pairDevice(hc, allScopes)
	r.expectStatus(r.putPush(dev, strings.Repeat("ab", 32), "development"), http.StatusOK, "push registration")

	dueAt := time.Now().Add(2 * time.Hour).UTC().Truncate(time.Second)
	hc.send(briefFrame(t, hubID, dev.id, 1, false))
	held := briefFrame(t, hubID, dev.id, 2, true)
	held["deliverAt"] = rfc3339(dueAt)
	hc.send(held)
	hc.barrier()

	latest := r.getWith("/v1/brief/latest", dev.bearer)
	r.expectStatus(latest, http.StatusOK, "latest brief before the held one is due")
	if version := rawField(t, latest.Data, "version"); string(version) != "1" {
		t.Fatalf("latest before the due time is version %s, want 1", version)
	}
	claim, err := r.store.ClaimPush(t.Context(), dueAt.Add(-time.Minute), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if claim != nil && claim.EventID == "2" {
		t.Fatal("held brief's push was due before its time")
	}

	r.advance(3 * time.Hour)
	latest = r.getWith("/v1/brief/latest", dev.bearer)
	r.expectStatus(latest, http.StatusOK, "latest brief after the due time")
	if version := rawField(t, latest.Data, "version"); string(version) != "2" {
		t.Fatalf("latest after the due time is version %s, want 2", version)
	}
}

// A brief due more than a day ahead is refused, so nothing is held indefinitely.
func TestBriefDueMoreThanADayAheadIsRefused(t *testing.T) {
	r := newRelay(t, "")
	hubID := uuid.NewString()
	hc := r.connectHub(hubID, randomToken(t))
	dev := r.pairDevice(hc, allScopes)
	far := briefFrame(t, hubID, dev.id, 3, false)
	far["deliverAt"] = rfc3339(time.Now().Add(48 * time.Hour))
	hc.send(far)
	hc.barrier()
	briefs, err := r.store.ListBriefs(t.Context(), dev.id, 7, time.Now().Add(72*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(briefs) != 0 {
		t.Fatalf("a brief due in 48 hours was stored: %d", len(briefs))
	}
}
