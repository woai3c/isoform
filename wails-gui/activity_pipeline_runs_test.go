package main

import (
	"reflect"
	"testing"
	"time"
)

// TestSelectedFramesEncodeDecodeRoundtrip locks the JSON shape of the
// selected_frames column so a future schema/codec change cannot silently
// regress the Settings > Activity Pipeline frame list. encode/decode are
// best-effort by design (errors log + drop), so without this test a bad
// codec change would make the column quietly stop populating and no other
// signal would surface the regression.
func TestSelectedFramesEncodeDecodeRoundtrip(t *testing.T) {
	cases := []struct {
		name   string
		frames []SelectedFrame
	}{
		{
			name:   "nil round-trips to nil",
			frames: nil,
		},
		{
			name:   "empty slice round-trips to nil",
			frames: []SelectedFrame{},
		},
		{
			name: "parsed entry preserves snapshot id and label",
			frames: []SelectedFrame{
				{Kind: "parsed", SessionID: "sess-1", SnapshotID: 42, App: "Slack", Label: "#general"},
			},
		},
		{
			name: "ocr entry preserves frame time without snapshot id",
			frames: []SelectedFrame{
				{Kind: "ocr", SessionID: "sess-1", FrameTime: 12.5, Label: "Hello world"},
			},
		},
		{
			name: "mixed parsed + ocr preserves ordering and per-row fields",
			frames: []SelectedFrame{
				{Kind: "parsed", SessionID: "sess-1", SnapshotID: 7, App: "Notion", Label: "Doc title"},
				{Kind: "ocr", SessionID: "sess-1", SnapshotID: 9, FrameTime: 1.2, Label: "screen text"},
				{Kind: "ocr", SessionID: "sess-2", FrameTime: 3.0, Label: "另一帧的 OCR 文本"},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := decodeSelectedFrames(encodeSelectedFrames(tc.frames))
			// Normalize the "empty slice" case: encode returns "" for both
			// nil and []SelectedFrame{}, and decode returns nil from "" —
			// we explicitly assert nil so contract drift here is loud.
			if len(tc.frames) == 0 {
				if got != nil {
					t.Fatalf("expected nil for empty input, got %#v", got)
				}
				return
			}
			if !reflect.DeepEqual(got, tc.frames) {
				t.Fatalf("round-trip mismatch:\n  got:  %#v\n  want: %#v", got, tc.frames)
			}
		})
	}
}

// TestActivityPipelineRunSelectedFramesPersistence exercises the column end
// to end: SaveActivityPipelineRun → GetActivityPipelineRunByID → ListActivity
// PipelineRuns. Each layer reads selected_frames independently, so a missing
// column in any of the three projection lists would silently strip the data;
// this test fails loudly instead.
func TestActivityPipelineRunSelectedFramesPersistence(t *testing.T) {
	store := newTestStore(t)

	frames := []SelectedFrame{
		{Kind: "parsed", SessionID: "sess-rt", SnapshotID: 101, App: "Notion", Label: "Doc"},
		{Kind: "ocr", SessionID: "sess-rt", SnapshotID: 102, FrameTime: 4.5, Label: "Hello"},
	}
	// StartedAt must be recent — SaveActivityPipelineRun prunes rows older
	// than activityPipelineMaxAge (7 days) right after the insert.
	runID, err := store.SaveActivityPipelineRun(ActivityPipelineRunRow{
		Kind:           "group",
		StartedAt:      time.Now().UnixMilli(),
		GroupID:        "sess-rt",
		InitiativeID:   "initiative-rt",
		ShadowRunID:    "shadow-run-rt",
		SelectedFrames: frames,
	})
	if err != nil {
		t.Fatalf("SaveActivityPipelineRun: %v", err)
	}

	gotByID, err := store.GetActivityPipelineRunByID(runID)
	if err != nil {
		t.Fatalf("GetActivityPipelineRunByID: %v", err)
	}
	if !reflect.DeepEqual(gotByID.SelectedFrames, frames) {
		t.Errorf("GetActivityPipelineRunByID dropped frames:\n  got:  %#v\n  want: %#v", gotByID.SelectedFrames, frames)
	}
	if gotByID.InitiativeID != "initiative-rt" {
		t.Errorf("GetActivityPipelineRunByID initiative=%q", gotByID.InitiativeID)
	}
	if gotByID.ShadowRunID != "shadow-run-rt" {
		t.Errorf("GetActivityPipelineRunByID shadow run=%q", gotByID.ShadowRunID)
	}

	rows, err := store.ListActivityPipelineRuns(50)
	if err != nil {
		t.Fatalf("ListActivityPipelineRuns: %v", err)
	}
	var matched *ActivityPipelineRunRow
	for i := range rows {
		if rows[i].ID == runID {
			matched = &rows[i]
			break
		}
	}
	if matched == nil {
		t.Fatalf("ListActivityPipelineRuns: row %d not found", runID)
	}
	if !reflect.DeepEqual(matched.SelectedFrames, frames) {
		t.Errorf("ListActivityPipelineRuns dropped frames:\n  got:  %#v\n  want: %#v", matched.SelectedFrames, frames)
	}
	if matched.InitiativeID != "initiative-rt" {
		t.Errorf("ListActivityPipelineRuns initiative=%q", matched.InitiativeID)
	}
	if matched.ShadowRunID != "shadow-run-rt" {
		t.Errorf("ListActivityPipelineRuns shadow run=%q", matched.ShadowRunID)
	}
}
