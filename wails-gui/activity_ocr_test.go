package main

import (
	"fmt"
	"testing"

	"wails-gui/internal/platform/ocr"
)

type fakeBackgroundOCREngine struct {
	observations []ocr.TextObservation
	err          error
	usedText     bool
	usedBoxes    bool
	priority     ocr.Priority
}

func (e *fakeBackgroundOCREngine) RecognizeJPEG(_ []byte, _ []string, _ ocr.Priority) (*ocr.Result, error) {
	e.usedText = true
	return nil, fmt.Errorf("RecognizeJPEG should not be used for background OCR")
}

func (e *fakeBackgroundOCREngine) RecognizeJPEGWithBoxes(_ []byte, _ []string, prio ocr.Priority) ([]ocr.TextObservation, error) {
	e.usedBoxes = true
	e.priority = prio
	return e.observations, e.err
}

func (e *fakeBackgroundOCREngine) IsSupported() bool {
	return true
}

func (e *fakeBackgroundOCREngine) SupportedLanguages() []string {
	return nil
}

func TestRecognizeBackgroundOCRTextUsesBoxesPath(t *testing.T) {
	engine := &fakeBackgroundOCREngine{
		observations: []ocr.TextObservation{
			{Text: "hello", Confidence: 0.8},
			{Text: "", Confidence: 0.1},
			{Text: "world", Confidence: 1.0},
		},
	}

	result, observations, err := recognizeBackgroundOCRText(engine, []byte{1, 2, 3}, []string{"en-US"}, ocr.PriorityLow)
	if err != nil {
		t.Fatalf("recognizeBackgroundOCRText returned error: %v", err)
	}
	if len(observations) != len(engine.observations) {
		t.Fatalf("observations = %d, want the engine's %d passed through", len(observations), len(engine.observations))
	}
	if engine.usedText {
		t.Fatal("background OCR used text-only RecognizeJPEG path")
	}
	if !engine.usedBoxes {
		t.Fatal("background OCR did not use RecognizeJPEGWithBoxes path")
	}
	if engine.priority != ocr.PriorityLow {
		t.Fatalf("priority = %v, want %v", engine.priority, ocr.PriorityLow)
	}
	if result.Text != "hello\nworld" {
		t.Fatalf("text = %q, want %q", result.Text, "hello\nworld")
	}
	if result.Confidence < 0.899 || result.Confidence > 0.901 {
		t.Fatalf("confidence = %v, want 0.9", result.Confidence)
	}
}

func TestSelectSnapshotsForOCRSamplesHighChurn(t *testing.T) {
	snapshots := make([]ActivitySnapshotRow, 0, 20)
	for i := range 20 {
		snapshots = append(snapshots, ActivitySnapshotRow{
			ID:        int64(i + 1),
			Timestamp: int64(i) * 3_000,
			Trigger:   "app_switch",
		})
	}
	snapshots[len(snapshots)-1].Trigger = "session_end"

	selected := selectSnapshotsForOCR(snapshots)
	if len(selected) >= len(snapshots) {
		t.Fatalf("expected high-churn snapshots to be sampled, got %d/%d", len(selected), len(snapshots))
	}
	if selected[0].ID != snapshots[0].ID {
		t.Fatalf("first selected snapshot ID = %d, want %d", selected[0].ID, snapshots[0].ID)
	}
	if selected[len(selected)-1].ID != snapshots[len(snapshots)-1].ID {
		t.Fatalf("last selected snapshot ID = %d, want session end ID %d", selected[len(selected)-1].ID, snapshots[len(snapshots)-1].ID)
	}
}

func TestSelectSnapshotsForOCRKeepsSparseSnapshots(t *testing.T) {
	snapshots := []ActivitySnapshotRow{
		{ID: 1, Timestamp: 0, Trigger: "app_switch"},
		{ID: 2, Timestamp: 20_000, Trigger: "visual_change"},
		{ID: 3, Timestamp: 40_000, Trigger: "session_end"},
	}

	selected := selectSnapshotsForOCR(snapshots)
	if len(selected) != len(snapshots) {
		t.Fatalf("selected %d sparse snapshots, want %d", len(selected), len(snapshots))
	}
	for i := range snapshots {
		if selected[i].ID != snapshots[i].ID {
			t.Fatalf("selected[%d].ID = %d, want %d", i, selected[i].ID, snapshots[i].ID)
		}
	}
}

func TestSelectSnapshotsForOCRCapsAndKeepsSessionEnd(t *testing.T) {
	snapshots := make([]ActivitySnapshotRow, 0, 40)
	for i := range 40 {
		snapshots = append(snapshots, ActivitySnapshotRow{
			ID:        int64(i + 1),
			Timestamp: int64(i) * 20_000,
			Trigger:   "visual_change",
		})
	}
	snapshots[len(snapshots)-1].Trigger = "session_end"

	selected := selectSnapshotsForOCR(snapshots)
	if len(selected) != ocrMaxSnapshotFramesPerRun {
		t.Fatalf("selected %d snapshots, want cap %d", len(selected), ocrMaxSnapshotFramesPerRun)
	}
	if selected[len(selected)-1].ID != snapshots[len(snapshots)-1].ID {
		t.Fatalf("last selected snapshot ID = %d, want session end ID %d", selected[len(selected)-1].ID, snapshots[len(snapshots)-1].ID)
	}
}
