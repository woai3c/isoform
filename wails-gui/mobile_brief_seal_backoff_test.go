package main

import (
	"bytes"
	"log"
	"os"
	"strings"
	"testing"
	"time"
)

// A brief whose seal keeps failing is retried a bounded number of times, then
// left alone for a backoff: reads during the backoff do not gate it again, and
// its failure is logged once, not once per read. While it is withheld, no text
// of the brief is shown.
func TestSealRetryBacksOffWhenTheBriefCannotBeSealed(t *testing.T) {
	_, svc, brief := cardRig(t)
	svc.todayRefreshWG.Wait()
	svc.wg.Wait()
	// Drop the copy the save sealed, so the brief has no sealed copy to show.
	if path, ok := svc.store.finishedReadPath(brief.BriefID); ok {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	sealRetryPaused.Delete(brief.BriefID)
	defer sealRetryPaused.Delete(brief.BriefID)

	savedDelays := sealRetryDelays
	sealRetryDelays = []time.Duration{time.Millisecond, time.Millisecond}
	defer func() { sealRetryDelays = savedDelays }()
	// While the policy is pending, every seal is refused as not ready, which is
	// the same transient refusal a gate that cannot decide gives.
	briefPolicyPending.Store(true)
	defer briefPolicyPending.Store(false)

	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previous)

	attemptsBefore := mobileBriefSealRetryAttempts.Load()
	svc.scheduleSealRetry(brief)
	svc.wg.Wait()
	if got := mobileBriefSealRetryAttempts.Load() - attemptsBefore; got != 2 {
		t.Fatalf("the first failing schedule made %d attempts, want 2 (one per retry delay)", got)
	}

	// Reads keep asking for a seal. None of them starts another round of attempts.
	for i := 0; i < 5; i++ {
		read, ok, err := svc.finishedLatest()
		if err != nil {
			t.Fatalf("read %d failed: %v", i, err)
		}
		if ok && (len(read.Cards) != 0 || read.Narrative != nil) {
			t.Fatalf("read %d showed text of a brief that could not be sealed", i)
		}
		svc.scheduleSealRetry(brief)
		svc.wg.Wait()
	}
	if got := mobileBriefSealRetryAttempts.Load() - attemptsBefore; got != 2 {
		t.Fatalf("a backed-off brief was gated again: %d attempts in total, want 2", got)
	}
	if n := strings.Count(logs.String(), "could not be sealed yet"); n != 1 {
		t.Fatalf("the failure was logged %d times, want once", n)
	}
	if !strings.Contains(logs.String(), "reason=store_unavailable") {
		t.Fatalf("the logged failure does not name its reason: %q", logs.String())
	}

	// A change to the brief's stored content starts a new attempt at once.
	changed := brief
	changed.Slot = brief.Slot + "-changed"
	svc.scheduleSealRetry(changed)
	svc.wg.Wait()
	if got := mobileBriefSealRetryAttempts.Load() - attemptsBefore; got != 4 {
		t.Fatalf("a changed brief was not tried again: %d attempts in total, want 4", got)
	}

	// Once the backoff has passed, the same brief is tried again.
	sealRetryPaused.Store(brief.BriefID, sealRetryPause{
		digest: mobileBriefSourceDigest(brief),
		until:  time.Now().Add(-time.Second),
	})
	svc.scheduleSealRetry(brief)
	svc.wg.Wait()
	if got := mobileBriefSealRetryAttempts.Load() - attemptsBefore; got != 6 {
		t.Fatalf("an expired backoff did not retry the brief: %d attempts in total, want 6", got)
	}
}

// A brief that seals once its gates can decide leaves no backoff behind, so a
// later failure starts its own retries.
func TestSealRetryClearsBackoffOnceTheBriefSeals(t *testing.T) {
	_, svc, brief := cardRig(t)
	svc.todayRefreshWG.Wait()
	svc.wg.Wait()
	sealRetryPaused.Delete(brief.BriefID)
	defer sealRetryPaused.Delete(brief.BriefID)

	savedDelays := sealRetryDelays
	sealRetryDelays = []time.Duration{time.Millisecond}
	defer func() { sealRetryDelays = savedDelays }()

	briefPolicyPending.Store(true)
	svc.scheduleSealRetry(brief)
	svc.wg.Wait()
	if _, paused := sealRetryPaused.Load(brief.BriefID); !paused {
		t.Fatal("a brief that could not seal has no backoff")
	}

	// The gates can decide again: the retry after the backoff seals the brief,
	// and the backoff is gone.
	briefPolicyPending.Store(false)
	sealRetryPaused.Store(brief.BriefID, sealRetryPause{
		digest: mobileBriefSourceDigest(brief),
		until:  time.Now().Add(-time.Second),
	})
	svc.scheduleSealRetry(brief)
	svc.wg.Wait()
	if _, paused := sealRetryPaused.Load(brief.BriefID); paused {
		t.Fatal("a brief that sealed still has a backoff")
	}
}
