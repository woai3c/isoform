package main

import (
	"errors"
	"math"
	"path/filepath"
	"testing"

	"wails-gui/storage"
)

func TestCalculateInitiativeConfidenceAdjustmentRequiresReadyBand(t *testing.T) {
	summary := InitiativeCalibrationSummary{
		Bands: []InitiativeCalibrationBand{{
			Key:              "high",
			MinConfidence:    0.75,
			MaxConfidence:    0.90,
			EvaluatedSamples: 4,
			Reliability:      0.25,
		}},
	}
	adjusted, audit := calculateInitiativeConfidenceAdjustment(
		0.82, "prediction", "activity", true, summary, nil,
	)
	if adjusted != 0.82 || audit.Applied || audit.Reason != "insufficient_samples" {
		t.Fatalf("premature adjustment: adjusted=%.2f audit=%+v", adjusted, audit)
	}
	if audit.EvaluatedSamples != 4 || audit.CalibrationBand != "high" {
		t.Fatalf("missing early-band audit context: %+v", audit)
	}
}

func TestCalculateInitiativeConfidenceAdjustmentIsBoundedAndAuditable(t *testing.T) {
	summary := InitiativeCalibrationSummary{
		Bands: []InitiativeCalibrationBand{{
			Key:              "very_high",
			MinConfidence:    0.90,
			MaxConfidence:    1.00,
			EvaluatedSamples: 5,
			Reliability:      0.40,
			Ready:            true,
		}},
	}
	adjusted, audit := calculateInitiativeConfidenceAdjustment(
		0.94, "prediction", "activity", true, summary, nil,
	)
	if math.Abs(adjusted-0.84) > 1e-9 {
		t.Fatalf("adjusted=%.3f, want capped 0.84", adjusted)
	}
	if !audit.Applied || audit.Reason != "ready_band" ||
		math.Abs(audit.Delta+initiativeConfidenceMaxMovement) > 1e-9 {
		t.Fatalf("unexpected adjustment audit: %+v", audit)
	}
	if audit.CalibrationReliability == nil || *audit.CalibrationReliability != 0.40 ||
		audit.PolicyVersion != initiativeConfidencePolicyVersion {
		t.Fatalf("missing adjustment provenance: %+v", audit)
	}
}

func TestCalculateInitiativeConfidenceAdjustmentCanBeDisabledOrFailOpen(t *testing.T) {
	ready := InitiativeCalibrationSummary{
		Bands: []InitiativeCalibrationBand{{
			Key: "high", MinConfidence: 0.75, MaxConfidence: 0.90,
			EvaluatedSamples: 5, Reliability: 0.20, Ready: true,
		}},
	}
	for _, test := range []struct {
		name    string
		enabled bool
		err     error
		reason  string
	}{
		{name: "disabled", enabled: false, reason: "feature_disabled"},
		{name: "calibration unavailable", enabled: true, err: errors.New("db unavailable"), reason: "calibration_unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			adjusted, audit := calculateInitiativeConfidenceAdjustment(
				0.82, "prediction", "activity", test.enabled, ready, test.err,
			)
			if adjusted != 0.82 || audit.Applied || audit.Reason != test.reason {
				t.Fatalf("adjusted=%.2f audit=%+v", adjusted, audit)
			}
		})
	}
}

func TestShadowConfidenceAdjustmentFeatureFlagDefaultsOnAndCanDisable(t *testing.T) {
	stateStore, err := storage.New(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer stateStore.Close()
	app := &App{store: stateStore}
	if !app.shadowConfidenceAdjustmentEnabled() {
		t.Fatal("missing setting must default confidence adjustment on")
	}
	if err := app.SaveUIState(shadowConfidenceAdjustmentEnabledKey, "false"); err != nil {
		t.Fatal(err)
	}
	if app.shadowConfidenceAdjustmentEnabled() {
		t.Fatal("explicit false must disable confidence adjustment")
	}
}

func TestReadyCalibrationAdjustsShadowThresholdAndPreservesRawConfidence(t *testing.T) {
	store := newTestStore(t)
	for i := 0; i < initiativeCalibrationMinimumEvaluatedSamples; i++ {
		item := newInitiativeFixture(
			"confidence-sample-"+string(rune('a'+i)),
			"confidence-source-"+string(rune('a'+i)),
			InitiativeStatusShadowed,
		)
		item.Kind = "prediction"
		item.SourceKind = "activity"
		item.Confidence = 0.94
		if err := store.SaveInitiative(item); err != nil {
			t.Fatal(err)
		}
		if _, added, err := store.SaveInitiativeOutcome(InitiativeOutcome{
			InitiativeID:   item.ID,
			SourceKind:     "activity",
			SourceID:       item.SourceID + "-outcome",
			ObservedAction: "The user chose a different action.",
			Assessment:     "different",
			Confidence:     0.95,
		}); err != nil || !added {
			t.Fatalf("save calibration outcome: added=%v err=%v", added, err)
		}
	}

	app := &App{activity: &activityMonitorState{store: store}}
	created := app.createPredictionInitiativeFromTriage(&activityTriageResponse{
		WorthInitiative:      true,
		InitiativeTitle:      "Prepare the next report",
		InitiativeGoal:       "Finish the report",
		PredictedUserAction:  "Draft the report now.",
		InitiativeRationale:  "The report is open and due soon.",
		InitiativeConfidence: 0.94,
	}, "activity", "confidence-adjusted-source", "finish the report")
	if created.Status != "observed" {
		t.Fatalf("status=%q, want observed after confidence drops below immediate threshold", created.Status)
	}

	item, err := store.GetInitiative(created.InitiativeID)
	if err != nil {
		t.Fatal(err)
	}
	if item.RawModelConfidence == nil || *item.RawModelConfidence != 0.94 {
		t.Fatalf("raw model confidence was not preserved: %+v", item)
	}
	if item.AdjustedConfidence == nil || math.Abs(*item.AdjustedConfidence-0.84) > 1e-9 ||
		math.Abs(item.Confidence-0.84) > 1e-9 {
		t.Fatalf("adjusted confidence was not used as effective confidence: %+v", item)
	}
	if item.ConfidenceAdjustment == nil || !item.ConfidenceAdjustment.Applied {
		t.Fatalf("adjustment audit was not persisted: %+v", item.ConfidenceAdjustment)
	}
	observations, err := store.ListInitiativeObservations(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(observations) != 1 || observations[0].RawModelConfidence == nil ||
		*observations[0].RawModelConfidence != 0.94 ||
		observations[0].AdjustedConfidence == nil ||
		math.Abs(*observations[0].AdjustedConfidence-0.84) > 1e-9 {
		t.Fatalf("observation confidence audit did not round-trip: %+v", observations)
	}
	outcome, added, err := store.SaveInitiativeOutcome(InitiativeOutcome{
		InitiativeID:   item.ID,
		SourceKind:     "activity",
		SourceID:       "confidence-adjusted-outcome",
		ObservedAction: "The user drafted the report.",
		Assessment:     "matched",
		Confidence:     0.95,
	})
	if err != nil || !added {
		t.Fatalf("save adjusted prediction outcome: added=%v err=%v", added, err)
	}
	if outcome.PredictionConfidence == nil || *outcome.PredictionConfidence != 0.94 {
		t.Fatalf("outcome snapshot=%v, want raw confidence 0.94", outcome.PredictionConfidence)
	}
}
