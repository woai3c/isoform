package storage

import (
	"testing"
)

func TestMobileBriefEngagementMomentsUseExactKeysAndCutoff(t *testing.T) {
	s := newTestStore(t)
	for _, row := range []EngagementMomentRow{
		{ID: "wanted", Kind: "commitment_slipping", DedupeKey: "owed", State: EngagementStateSent, CreatedAt: 10},
		{ID: "other-kind", Kind: "brief_ready", DedupeKey: "unrelated", CreatedAt: 1},
		{ID: "other-key", Kind: "commitment_slipping", DedupeKey: "other", CreatedAt: 2},
		{ID: "future", Kind: "commitment_slipping", DedupeKey: "future", CreatedAt: 30},
		{ID: "suppressed", Kind: "commitment_slipping", DedupeKey: "hidden", State: EngagementStateSuppressed, CreatedAt: 3},
	} {
		if _, err := s.CreateEngagementMoment(ctx, row); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.BriefEngagementMoments(ctx, "commitment_slipping", []string{"owed", "future", "hidden"}, 20)
	if err != nil || len(rows) != 1 || rows[0].ID != "wanted" {
		t.Fatalf("exact moment lookup = %+v %v", rows, err)
	}
	rows, err = s.BriefEngagementMoments(ctx, "commitment_slipping", nil, 20)
	if err != nil || len(rows) != 0 {
		t.Fatalf("empty lookup = %+v %v", rows, err)
	}
}
