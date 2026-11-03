package push

import (
	"strings"
	"testing"
)

const testNoticeID = "6f1c2e4a-7b3d-4c5e-9a1b-2d3e4f5a6b7c"

func testNotice() Notice {
	return Notice{ID: testNoticeID, Moment: "decision_due", CT: strings.Repeat("A", 40), Category: CategoryDecision, Level: LevelTimeSensitive}
}

func TestNoticePrepareChecksEveryHeaderField(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Notice)
		valid  bool
	}{
		{"defaults the collapse id to the notice id", func(n *Notice) {}, true},
		{"keeps a hub collapse id", func(n *Notice) { n.Collapse = "decision:req-1" }, true},
		{"a passive moment with the brief category", func(n *Notice) { n.Level, n.Category = LevelPassive, CategoryBrief }, true},
		{"notice id must be a UUID", func(n *Notice) { n.ID = "not-a-uuid" }, false},
		{"moment kind must be lowercase words", func(n *Notice) { n.Moment = "Decision Due" }, false},
		{"moment kind is bounded", func(n *Notice) { n.Moment = "a" + strings.Repeat("b", 40) }, false},
		{"sealed text must be base64url", func(n *Notice) { n.CT = strings.Repeat("A", 39) + "+" }, false},
		{"sealed text must not be empty", func(n *Notice) { n.CT = "" }, false},
		{"sealed text is bounded at 3500 characters", func(n *Notice) { n.CT = strings.Repeat("A", MaxNoticeCT+1) }, false},
		{"sealed text at the bound is accepted", func(n *Notice) { n.CT = strings.Repeat("A", MaxNoticeCT) }, true},
		{"category must be a known one", func(n *Notice) { n.Category = "YANSU_OTHER" }, false},
		{"level must be an APNs level", func(n *Notice) { n.Level = "critical" }, false},
		{"collapse id may not contain spaces", func(n *Notice) { n.Collapse = "decision req" }, false},
		{"collapse id is at most 64 bytes", func(n *Notice) { n.Collapse = strings.Repeat("c", 65) }, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			n := testNotice()
			tc.change(&n)
			prepared, err := n.Prepare()
			if tc.valid && err != nil {
				t.Fatalf("refused: %v", err)
			}
			if !tc.valid && err != ErrInvalidNotice {
				t.Fatalf("accepted an invalid notice: %+v", prepared)
			}
			if tc.valid && prepared.Collapse == "" {
				t.Fatal("no collapse id after Prepare")
			}
		})
	}
}

func TestNoticePrepareDefaultsCollapseToNoticeID(t *testing.T) {
	prepared, err := testNotice().Prepare()
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Collapse != testNoticeID {
		t.Fatalf("collapse id %q, want the notice id", prepared.Collapse)
	}
}
