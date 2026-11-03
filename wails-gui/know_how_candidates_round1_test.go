package main

// Round 1 review guards for PR #219: the warm candidate cache must never
// outlive a privacy transition, a source/policy read failure must never be
// treated as evidence loss, and the confirmed trigger keeps its display
// redaction. These guards use the real transitions (DeleteSession,
// SetClosedEyesModeEnabled, AddSensitiveApp, AuthManager.Reset), never the
// test-only cache invalidation the pre-round-1 tests relied on.

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/pii"
	"wails-gui/internal/sop"
	"wails-gui/storage"
)

// newKnowHowRound1TestApp is newKnowHowCandidateTestApp with the store paths
// kept, so an outage fixture can close and reopen the real stores.
func newKnowHowRound1TestApp(t *testing.T) (app *App, store *ActivityStore, activityDir, uiDB string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	uiDB = filepath.Join(home, "yansu.db")
	ui, err := storage.New(uiDB)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ui.Close() })
	app = &App{store: ui, activeTags: map[string]bool{}, eventBroadcaster: NewEventBroadcaster()}
	activityDir = filepath.Join(home, "activity")
	activity, err := NewActivityStore(activityDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = activity.Close() })
	app.activity = &activityMonitorState{store: activity}
	candidates, err := sop.NewStore(filepath.Join(home, "know-how-candidates"))
	if err != nil {
		t.Fatal(err)
	}
	app.knowHowCandidates = candidates
	return app, activity, activityDir, uiDB
}

// knowHowRound1Proposal warms a three-session proposal and proves the second
// read is the warm cache (the same pointer, no recompute).
func knowHowRound1Proposal(t *testing.T, app *App, store *ActivityStore, idPrefix string) *KnowHowCandidate {
	t.Helper()
	day := time.Now().AddDate(0, 0, -4)
	for i := 0; i < 3; i++ {
		knowHowTestSession(t, store, fmt.Sprintf("%s%d", idPrefix, i), day.AddDate(0, 0, i))
	}
	cold, err := app.GetKnowHowCandidate()
	if err != nil || cold == nil {
		t.Fatalf("expected proposal: %+v %v", cold, err)
	}
	warm, err := app.GetKnowHowCandidate()
	if err != nil || warm != cold {
		t.Fatalf("second read was not the warm cache: %+v %v", warm, err)
	}
	return cold
}

func knowHowRound1Cached(t *testing.T, app *App) *KnowHowCandidate {
	t.Helper()
	app.knowHow.candMu.Lock()
	defer app.knowHow.candMu.Unlock()
	return app.knowHow.candCached
}

func TestKnowHowCandidateWarmReadPrivacyTransitions(t *testing.T) {
	t.Run("forget", func(t *testing.T) {
		app, store, _, _ := newKnowHowRound1TestApp(t)
		before := knowHowRound1Proposal(t, app, store, "warm-forget")
		// The real Forget transition, no test-only invalidation: the store
		// revision changes, the warm read re-runs the gates and withdraws.
		if err := store.DeleteSession("warm-forget0"); err != nil {
			t.Fatal(err)
		}
		after, err := app.GetKnowHowCandidate()
		if err != nil || after != nil {
			t.Fatalf("forgotten-evidence proposal served from the warm cache: %+v %v", after, err)
		}
		if _, err := app.knowHowCandidates.Get(before.ID); err == nil {
			t.Fatal("withdrawn draft stayed in the store")
		}
	})

	t.Run("closed-eyes-pause", func(t *testing.T) {
		app, store, _, _ := newKnowHowRound1TestApp(t)
		before := knowHowRound1Proposal(t, app, store, "warm-pause")
		// The real pause transition records the pause log and hides the
		// proposal without withdrawing the draft.
		if err := app.SetClosedEyesModeEnabled(true); err != nil {
			t.Fatal(err)
		}
		if pauses, err := app.observationPauses(); err != nil || len(pauses) != 1 || pauses[0].End != 0 {
			t.Fatalf("pause transition did not open the pause log: %+v %v", pauses, err)
		}
		after, err := app.GetKnowHowCandidate()
		if err != nil || after != nil {
			t.Fatalf("paused read served the warm cache: %+v %v", after, err)
		}
		if _, err := app.knowHowCandidates.Get(before.ID); err != nil {
			t.Fatalf("Closed Eyes withdrew the draft: %v", err)
		}
		// Resume: the cached nil recovers into the still-authorized proposal.
		if err := app.SetClosedEyesModeEnabled(false); err != nil {
			t.Fatal(err)
		}
		back, err := app.GetKnowHowCandidate()
		if err != nil || back == nil || back.ID != before.ID {
			t.Fatalf("proposal did not return after resume: %+v %v", back, err)
		}
	})

	t.Run("protect-app", func(t *testing.T) {
		app, store, _, _ := newKnowHowRound1TestApp(t)
		before := knowHowRound1Proposal(t, app, store, "warm-protect")
		previous := pii.GetUserSensitiveApps()
		defer pii.SetUserSensitiveApps(previous)
		// The real Privacy page control, not the bare withdrawal helper.
		if err := app.AddSensitiveApp("com.example.terminal", "Terminal"); err != nil {
			t.Fatal(err)
		}
		after, err := app.GetKnowHowCandidate()
		if err != nil || after != nil {
			t.Fatalf("protected-app proposal served from the warm cache: %+v %v", after, err)
		}
		if _, err := app.knowHowCandidates.Get(before.ID); err == nil {
			t.Fatal("protected draft stayed in the store")
		}
	})

	t.Run("sign-out", func(t *testing.T) {
		app, store, _, _ := newKnowHowRound1TestApp(t)
		before := knowHowRound1Proposal(t, app, store, "warm-signout")
		// Sign-out erases warm derived state through the auth reset hook;
		// the owner's draft on disk is untouched, so the next read re-runs the
		// gates and may propose it again.
		NewAuthManager(app).Reset()
		if cached := knowHowRound1Cached(t, app); cached != nil {
			t.Fatalf("sign-out kept the warm publication: %+v", cached)
		}
		back, err := app.GetKnowHowCandidate()
		if err != nil || back == nil || back.ID != before.ID || back == before {
			t.Fatalf("sign-out read was not a fresh authorized compute: %+v %v", back, err)
		}
	})

	t.Run("control", func(t *testing.T) {
		app, store, _, _ := newKnowHowRound1TestApp(t)
		before := knowHowRound1Proposal(t, app, store, "warm-control")
		again, err := app.GetKnowHowCandidate()
		if err != nil || again != before {
			t.Fatalf("unchanged warm read lost the proposal: %+v %v", again, err)
		}
	})
}

func TestKnowHowCandidateWarmReadWithholdsWhileSourceChangeInFlight(t *testing.T) {
	app, store, _, _ := newKnowHowRound1TestApp(t)
	before := knowHowRound1Proposal(t, app, store, "warm-inflight")
	// The store's own fencing: an odd privacy revision marks a change in
	// flight. A read then withholds instead of publishing across the change.
	store.readPrivacyRevision.Add(1)
	after, err := app.GetKnowHowCandidate()
	if err != nil || after != nil {
		t.Fatalf("read published across an in-flight source change: %+v %v", after, err)
	}
	if cached := knowHowRound1Cached(t, app); cached != before {
		t.Fatalf("withheld read republished the cache: %+v", cached)
	}
	// The change settles; nothing was actually deleted, so the proposal
	// recomputes and returns.
	store.readPrivacyRevision.Add(1)
	back, err := app.GetKnowHowCandidate()
	if err != nil || back == nil || back.ID != before.ID {
		t.Fatalf("proposal did not recover after the in-flight change: %+v %v", back, err)
	}
}

func TestKnowHowCandidatePauseWindowWithdrawsCold(t *testing.T) {
	app, store, _, _ := newKnowHowRound1TestApp(t)
	day := time.Now().AddDate(0, 0, -4)
	for i := 0; i < 3; i++ {
		knowHowTestSession(t, store, fmt.Sprintf("pause%d", i), day.AddDate(0, 0, i))
	}
	candidate, err := app.GetKnowHowCandidate()
	if err != nil || candidate == nil {
		t.Fatalf("expected proposal: %+v %v", candidate, err)
	}
	// A pause window recorded over a source session is durable evidence loss
	// for a stored derivative: the draft is withdrawn, not merely hidden.
	pauseStart := day.Add(10 * time.Minute)
	app.recordObservationPause(true, pauseStart)
	app.recordObservationPause(false, pauseStart.Add(30*time.Minute))
	after, err := app.GetKnowHowCandidate()
	if err != nil || after != nil {
		t.Fatalf("pause-covered proposal survived: %+v %v", after, err)
	}
	if _, err := app.knowHowCandidates.Get(candidate.ID); err == nil {
		t.Fatal("pause-covered draft stayed in the store")
	}
}

func TestKnowHowCandidateReadFailureKeepsEditedDraft(t *testing.T) {
	app, store, activityDir, _ := newKnowHowRound1TestApp(t)
	day := time.Now().AddDate(0, 0, -4)
	for i := 0; i < 3; i++ {
		knowHowTestSession(t, store, fmt.Sprintf("failure%d", i), day.AddDate(0, 0, i))
	}
	candidate, err := app.GetKnowHowCandidate()
	if err != nil || candidate == nil {
		t.Fatalf("setup: %v %v", candidate, err)
	}
	if _, err := app.SaveKnowHowCandidate(candidate.ID, "User revised how-to", "User trigger", []string{"User revised step"}); err != nil {
		t.Fatal(err)
	}
	store.Close()
	if _, readErr := app.GetKnowHowCandidate(); readErr == nil {
		t.Fatal("fixture must produce a source-store error")
	}
	// A confirm during the outage refuses with the error and keeps the draft.
	if _, confirmErr := app.ConfirmKnowHowCandidate(candidate.ID); confirmErr == nil {
		t.Fatal("confirm succeeded during a source outage")
	}
	entry, err := app.knowHowCandidates.Get(candidate.ID)
	if err != nil || entry.Title != "User revised how-to" || entry.Trigger != "User trigger" {
		t.Fatalf("retryable source-store error destroyed the edited draft: %+v %v", entry, err)
	}
	// The outage lifts: the same draft, edits intact, is proposed again.
	reopened, err := NewActivityStore(activityDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	app.activity.store = reopened
	back, err := app.GetKnowHowCandidate()
	if err != nil || back == nil || back.ID != candidate.ID {
		t.Fatalf("edited draft did not recover after the outage: %+v %v", back, err)
	}
	if back.Title != "User revised how-to" || back.Trigger != "User trigger" || len(back.Steps) != 1 || back.Steps[0] != "User revised step" {
		t.Fatalf("recovered proposal lost the user's edits: %+v", back)
	}
}

func TestKnowHowCandidatePolicyReadFailureKeepsDraft(t *testing.T) {
	app, store, _, uiDB := newKnowHowRound1TestApp(t)
	day := time.Now().AddDate(0, 0, -4)
	for i := 0; i < 3; i++ {
		knowHowTestSession(t, store, fmt.Sprintf("policy%d", i), day.AddDate(0, 0, i))
	}
	candidate, err := app.GetKnowHowCandidate()
	if err != nil || candidate == nil {
		t.Fatalf("setup: %v %v", candidate, err)
	}
	if _, err := app.SaveKnowHowCandidate(candidate.ID, "Edited before outage", "", []string{"Edited step"}); err != nil {
		t.Fatal(err)
	}
	// The pause-log/policy store is unavailable: withhold, never delete.
	if err := app.store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, readErr := app.GetKnowHowCandidate(); readErr == nil {
		t.Fatal("fixture must produce a policy-read error")
	}
	entry, err := app.knowHowCandidates.Get(candidate.ID)
	if err != nil || entry.Title != "Edited before outage" {
		t.Fatalf("policy-read error destroyed the draft: %+v %v", entry, err)
	}
	reopened, err := storage.New(uiDB)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	app.store = reopened
	back, err := app.GetKnowHowCandidate()
	if err != nil || back == nil || back.ID != candidate.ID || back.Title != "Edited before outage" {
		t.Fatalf("draft did not recover after the policy outage: %+v %v", back, err)
	}
}

func TestKnowHowCandidateTriggerStaysMaskedAfterConfirm(t *testing.T) {
	const account = "1234567890"
	for _, tc := range []struct {
		name      string
		trigger   string
		storedRaw string // the owner file keeps this exact text
		wantCard  string
	}{
		{
			name:      "raw account number",
			trigger:   "When you open Account number: 1234567890 again.",
			storedRaw: "When you open Account number: 1234567890 again.",
			wantCard:  "When you open Account number: [...] again.",
		}, {
			name:      "stored placeholder",
			trigger:   "When you open [REDACTED:account-number] again.",
			storedRaw: "When you open [REDACTED:account-number] again.",
			wantCard:  "When you open [...] again.",
		}, {
			name:      "edited multiline",
			trigger:   "When the release checklist opens.\nAccount number: 1234567890",
			storedRaw: "When the release checklist opens. Account number: 1234567890", // the bundle's one-line frontmatter rule
			wantCard:  "When the release checklist opens. Account number: [...]",
		}, {
			name:      "ordinary text",
			trigger:   "When the release checklist opens.",
			storedRaw: "When the release checklist opens.",
			wantCard:  "When the release checklist opens.",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, store, _, _ := newKnowHowRound1TestApp(t)
			day := time.Now().AddDate(0, 0, -3)
			for i := 0; i < 3; i++ {
				knowHowTestSession(t, store, fmt.Sprintf("trigger%d", i), day.AddDate(0, 0, i))
			}
			candidate, err := app.GetKnowHowCandidate()
			if err != nil || candidate == nil {
				t.Fatalf("setup: %v %v", candidate, err)
			}
			edited, err := app.SaveKnowHowCandidate(candidate.ID, "QA for Account number: 1234567890", tc.trigger, []string{"Open account 1234567890", "Reconcile"})
			if err != nil {
				t.Fatal(err)
			}
			// The draft preview boundary already masks every display field.
			if strings.Contains(edited.Trigger, account) || strings.Contains(edited.Title, account) || strings.Contains(edited.Steps[0], account) {
				t.Fatalf("draft view leaked raw text: %+v", edited)
			}
			result, err := app.ConfirmKnowHowCandidate(candidate.ID)
			if err != nil {
				t.Fatal(err)
			}
			// The owner's file keeps the raw words (direct inspection stays).
			entry, err := app.GetKnowledgeEntry(result.KnowledgeID)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(entry.Content, tc.storedRaw) {
				t.Fatalf("confirmed file lost the owner's raw trigger: want %q in %q", tc.storedRaw, entry.Content)
			}
			cards, err := app.ListKnowHow()
			if err != nil {
				t.Fatal(err)
			}
			var card *KnowHowCard
			for i := range cards {
				if cards[i].ID == result.KnowledgeID {
					card = &cards[i]
				}
			}
			if card == nil {
				t.Fatal("confirmed entry missing from the card grid")
			}
			if strings.Contains(card.Trigger, account) || strings.Contains(card.Title, account) {
				t.Fatalf("confirmed card leaked raw text: %+v", card)
			}
			if card.Trigger != tc.wantCard {
				t.Fatalf("card trigger = %q, want %q", card.Trigger, tc.wantCard)
			}
		})
	}
}

// TestKnowHowCardTriggerProjectionMasks is the pure projection guard from the
// round 1 desk evidence: the raw trigger reaches the card only through
// DisplayText.
func TestKnowHowCardTriggerProjectionMasks(t *testing.T) {
	const raw = "When you open Account number: 1234567890 again."
	if pii.DisplayText(raw) == raw {
		t.Fatal("fixture must be redacted")
	}
	entry := sop.Entry{ID: "knowhow-fixture", Title: "Account number: 1234567890", Trigger: raw, Body: knowHowCandidateBody([]string{"Open account"})}
	content := buildOKFConfirmedHowTo(entry.ID, entry, nil, time.Now())
	card := knowHowCard(parseKnowledgeFile(entry.ID+".md", content))
	if strings.Contains(card.Title, "1234567890") {
		t.Fatal("existing title redaction failed")
	}
	if strings.Contains(card.Trigger, "1234567890") {
		t.Fatalf("confirmed card trigger bypassed display redaction: %q", card.Trigger)
	}
}

func TestKnowHowCandidateWarmSourceOutageWithholdsDraft(t *testing.T) {
	app, store, _, _ := newKnowHowRound1TestApp(t)
	before := knowHowRound1Proposal(t, app, store, "warm-outage")
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if candidate, err := app.GetKnowHowCandidate(); err == nil || candidate != nil {
		t.Fatalf("warm read published during an outage: %+v %v", candidate, err)
	}
	if _, err := app.knowHowCandidates.Get(before.ID); err != nil {
		t.Fatalf("outage deleted the draft: %v", err)
	}
}

func TestKnowHowCandidateWarmReadUsesPersistedPausePolicy(t *testing.T) {
	app, store, _, _ := newKnowHowRound1TestApp(t)
	before := knowHowRound1Proposal(t, app, store, "persisted-pause")
	// A persisted policy change must invalidate a warm view without relying
	// on a second, in-memory counter being updated in the right order.
	raw := fmt.Sprintf(`[{"start":%d,"end":%d}]`, before.OccurredAtMs[0], before.OccurredAtMs[0]+int64(time.Hour/time.Millisecond))
	if err := app.store.SetUIState(context.Background(), observationPausesKey, raw); err != nil {
		t.Fatal(err)
	}
	if candidate, err := app.GetKnowHowCandidate(); err != nil || candidate != nil {
		t.Fatalf("warm read ignored the persisted pause: %+v %v", candidate, err)
	}
	if _, err := app.knowHowCandidates.Get(before.ID); err == nil {
		t.Fatal("pause-covered draft stayed in the store")
	}
}

func TestKnowHowCandidateConfirmKeepsAllReviewedSteps(t *testing.T) {
	app, store, _, _ := newKnowHowRound1TestApp(t)
	candidate := knowHowRound1Proposal(t, app, store, "nine-steps")
	steps := make([]string, 9)
	for i := range steps {
		steps[i] = fmt.Sprintf("Reviewed step %d", i+1)
	}
	if _, err := app.SaveKnowHowCandidate(candidate.ID, "Release QA", "Before a release", steps); err != nil {
		t.Fatal(err)
	}
	result, err := app.ConfirmKnowHowCandidate(candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	cards, err := app.ListKnowHow()
	if err != nil {
		t.Fatal(err)
	}
	for _, card := range cards {
		if card.ID != result.KnowledgeID {
			continue
		}
		if strings.Join(card.Points, "\n") != strings.Join(steps, "\n") {
			t.Fatalf("confirmed detail dropped reviewed steps: %v", card.Points)
		}
		return
	}
	t.Fatal("confirmed how-to missing from the grid")
}
