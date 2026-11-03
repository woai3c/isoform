package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"wails-gui/internal/pii"
)

func addSelfModelPrivacyEvaluation(t *testing.T, app *App, eventEvidence bool) string {
	t.Helper()
	store := app.activity.store
	const sourceID = "self-privacy-source"
	insertTestSession(t, store, sourceID, "done", 100_000)
	input := taskEpisodeFixture("self-privacy-episode", 110_000, 120_000, TaskEpisodeStatusActive)
	input.InitialEvidence[0].SourceID = sourceID
	input.FrozenContext.Manifest = []TaskContextManifestEntry{{
		Ref: "session:" + sourceID, SourceKind: "activity_session", SourceID: sourceID,
		Section: TaskContextSectionActivity, ObservedAt: 100_000,
		Included: true, EvidenceClass: TaskContextEvidenceObservedUser,
		PrivacyDecision: TaskContextPrivacyRedact,
	}}
	if eventEvidence {
		onset := TaskOnsetEvidence{Ref: "event:110000:keyboard:0", Kind: "keyboard",
			SourceKind: "activity_event", SourceID: "0", OccurredAt: 110_000,
			AppKey: "com.example.test", AppName: "TestApp", Summary: "Drafted a progress update."}
		payload, err := json.Marshal(onset)
		if err != nil {
			t.Fatal(err)
		}
		input.InitialEvidence[0].SourceKind = onset.SourceKind
		input.InitialEvidence[0].SourceID = onset.SourceID
		input.InitialEvidence[0].PayloadJSON = string(payload)
	}
	episode, _, err := store.CreateTaskEpisode(input)
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := store.CreateShadowTaskRunFromEpisode(episode, "test-model", 130_000)
	if err != nil {
		t.Fatal(err)
	}
	observation, _, err := store.AddShadowRunObservation(ShadowRunObservation{
		ShadowRunID: run.ID, SourceKind: "task_episode_evaluation", SourceID: "self-privacy-result",
		ObservedAction: "Drafted a progress update", ObservedResult: "Saved the update",
		ObservedAt: 140_000, CreatedAt: 140_001,
	})
	if err != nil {
		t.Fatal(err)
	}
	matched := ShadowEvaluationDimension{Assessment: "matched", Score: shadowScore(1), Confidence: 0.9}
	evaluation, _, err := store.AddShadowRunEvaluation(ShadowRunEvaluation{
		ShadowRunID: run.ID, ObservationID: observation.ID, IdempotencyKey: "self-privacy-evaluation",
		ComparisonVersion: shadowCorrelationPromptVersion, EvaluatorModel: "test-model",
		Action: matched, Outcome: matched, Timing: matched,
		Reasoning: ShadowEvaluationDimension{Assessment: "unknown", Confidence: 0.2},
		Safety:    ShadowEvaluationDimension{Assessment: "safe", Score: shadowScore(1), Confidence: 0.9},
		CreatedAt: 150_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	return evaluation.ID
}

func TestTypedSelfInjectionRechecksEvaluationSourcePrivacy(t *testing.T) {
	for _, fixture := range []struct {
		name          string
		eventEvidence bool
		retained      bool
		protect       func(*App)
	}{
		{name: "app sensitive after materialization", protect: func(*App) {
			pii.SetUserSensitiveApps(map[string]string{"com.example.test": "TestApp"})
		}},
		{name: "retained source", retained: true, protect: func(*App) {
			pii.SetUserSensitiveApps(map[string]string{"com.example.test": "TestApp"})
		}},
		{name: "retained onset event", eventEvidence: true, retained: true, protect: func(*App) {
			pii.SetUserSensitiveApps(map[string]string{"com.example.test": "TestApp"})
		}},
		{name: "observation paused", protect: func(app *App) {
			app.saveObservationPauses([]observationPause{{Start: 100_000, End: 160_000}})
		}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			app := newMemoryInjectionPrivacyFixture(t)
			evaluationID := addSelfModelPrivacyEvaluation(t, app, fixture.eventEvidence)
			const statement = "The user prefers concise progress updates from private-source-work."
			entry, _, err := app.activity.store.AddSelfModelEntry(SelfModelEntry{
				Kind: selfModelKindPreference, Scope: "progress updates", Statement: statement,
				Status: selfModelStatusActive, Confidence: 0.9,
				ReasoningStatus: "source_backed_shadow_task_evaluation",
				Evidence: []SelfModelEvidence{{SourceKind: "shadow_task_evaluation", SourceID: evaluationID,
					Confidence: 0.9, OccurredAt: 150_000}}, CreatedAt: 150_001,
			})
			if err != nil {
				t.Fatal(err)
			}
			if fixture.retained {
				if err := app.activity.store.deleteRetainedSession("self-privacy-source"); err != nil {
					t.Fatal(err)
				}
			}
			const prompt = "Help with this task."
			if got := app.injectMemoryContext(context.Background(), prompt, "progress updates"); !strings.Contains(got, statement) {
				t.Fatalf("readable Self evidence did not enter context: %q", got)
			}
			fixture.protect(app)
			if got := app.injectMemoryContext(context.Background(), prompt, "progress updates"); got != prompt {
				t.Fatalf("protected Self evidence entered model context: %q", got)
			}
			if raw, err := app.activity.store.GetSelfModelEntryByID(entry.ID); err != nil || raw.Statement != statement {
				t.Fatalf("privacy filtering rewrote immutable Self history: %+v %v", raw, err)
			}
		})
	}
}

func TestTypedSelfInjectionWithholdsUnresolvedEvaluationEvidence(t *testing.T) {
	app := newMemoryInjectionPrivacyFixture(t)
	_, _, err := app.activity.store.AddSelfModelEntry(SelfModelEntry{
		Kind: selfModelKindPreference, Scope: "progress updates", Statement: "Unresolved private-source-work",
		Status: selfModelStatusActive, Confidence: 0.9, ReasoningStatus: "source_backed_shadow_task_evaluation",
		Evidence: []SelfModelEvidence{{SourceKind: "shadow_task_evaluation", SourceID: "missing-evaluation", Confidence: 0.9}},
	})
	if err != nil {
		t.Fatal(err)
	}
	const prompt = "Help with this task."
	if got := app.injectMemoryContext(context.Background(), prompt, "progress updates"); got != prompt {
		t.Fatalf("unresolved Self evidence entered model context: %q", got)
	}
}
