package main

import (
	"context"
	"errors"
	"log"
	"math"
	"strings"
)

const (
	shadowConfidenceAdjustmentEnabledKey = "shadow_confidence_adjustment_enabled"
	initiativeConfidencePolicyVersion    = "calibration-v1"
	initiativeConfidenceBlendWeight      = 0.35
	initiativeConfidenceMaxMovement      = 0.10
)

func (a *App) shadowConfidenceAdjustmentEnabled() bool {
	if a == nil || a.store == nil {
		return true
	}
	value, err := a.store.GetUIState(context.Background(), shadowConfidenceAdjustmentEnabledKey)
	if err != nil || strings.TrimSpace(value) == "" {
		return true
	}
	return value != "false"
}

func (a *App) applyInitiativeConfidenceAdjustment(item *Initiative) {
	if item == nil {
		return
	}
	rawConfidence := clampConfidence(item.Confidence)
	enabled := a.shadowConfidenceAdjustmentEnabled()
	var summary InitiativeCalibrationSummary
	var calibrationErr error
	if enabled {
		if a == nil || a.activity == nil || a.activity.store == nil {
			calibrationErr = errors.New("activity store unavailable")
		} else {
			summary, calibrationErr = a.activity.store.GetInitiativeCalibration(item.Kind, item.SourceKind)
		}
	}
	adjustedConfidence, audit := calculateInitiativeConfidenceAdjustment(
		rawConfidence,
		item.Kind,
		item.SourceKind,
		enabled,
		summary,
		calibrationErr,
	)
	item.Confidence = adjustedConfidence
	item.RawModelConfidence = confidencePointer(rawConfidence)
	item.AdjustedConfidence = confidencePointer(adjustedConfidence)
	item.ConfidenceAdjustment = &audit
	if calibrationErr != nil {
		log.Printf("[initiative] confidence calibration unavailable for %s/%s: %v",
			item.Kind, item.SourceKind, calibrationErr)
	}
}

func calculateInitiativeConfidenceAdjustment(
	rawConfidence float64,
	kind, sourceKind string,
	enabled bool,
	summary InitiativeCalibrationSummary,
	calibrationErr error,
) (float64, InitiativeConfidenceAdjustment) {
	rawConfidence = clampConfidence(rawConfidence)
	audit := InitiativeConfidenceAdjustment{
		PolicyVersion:         initiativeConfidencePolicyVersion,
		Reason:                "insufficient_samples",
		RawModelConfidence:    rawConfidence,
		AdjustedConfidence:    rawConfidence,
		CalibrationKind:       strings.TrimSpace(kind),
		CalibrationSourceKind: strings.TrimSpace(sourceKind),
		BlendWeight:           initiativeConfidenceBlendWeight,
		MaxMovement:           initiativeConfidenceMaxMovement,
	}
	if !enabled {
		audit.Reason = "feature_disabled"
		return rawConfidence, audit
	}
	if calibrationErr != nil {
		audit.Reason = "calibration_unavailable"
		return rawConfidence, audit
	}
	band := initiativeCalibrationBandForConfidence(summary.Bands, rawConfidence)
	if band == nil {
		audit.Reason = "calibration_unavailable"
		return rawConfidence, audit
	}
	audit.CalibrationBand = band.Key
	audit.EvaluatedSamples = band.EvaluatedSamples
	if !band.Ready {
		return rawConfidence, audit
	}

	reliability := clampConfidence(band.Reliability)
	audit.CalibrationReliability = confidencePointer(reliability)
	delta := initiativeConfidenceBlendWeight * (reliability - rawConfidence)
	delta = math.Max(-initiativeConfidenceMaxMovement, math.Min(initiativeConfidenceMaxMovement, delta))
	adjustedConfidence := clampConfidence(rawConfidence + delta)
	audit.AdjustedConfidence = adjustedConfidence
	audit.Delta = adjustedConfidence - rawConfidence
	if math.Abs(audit.Delta) < 1e-9 {
		audit.Reason = "no_change"
		return rawConfidence, audit
	}
	audit.Applied = true
	audit.Reason = "ready_band"
	return adjustedConfidence, audit
}

func initiativeCalibrationBandForConfidence(
	bands []InitiativeCalibrationBand,
	confidence float64,
) *InitiativeCalibrationBand {
	confidence = clampConfidence(confidence)
	for i := range bands {
		band := &bands[i]
		if confidence >= band.MinConfidence &&
			(band.Key == "very_high" && confidence <= band.MaxConfidence ||
				band.Key != "very_high" && confidence < band.MaxConfidence) {
			return band
		}
	}
	return nil
}

func confidencePointer(value float64) *float64 {
	value = clampConfidence(value)
	return &value
}
