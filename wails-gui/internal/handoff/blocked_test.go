package handoff

import "testing"

func TestBlockedStatusKeepsItsReasonAndOldFilesReadEmpty(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Save(Entry{ID: "hf_blocked", Title: "Export", Status: StatusBlocked, BlockedReason: BlockedTimeout, Source: SourceTriage, Body: "Export it.\n"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get("hf_blocked")
	if err != nil || got.Status != StatusBlocked || got.BlockedReason != BlockedTimeout {
		t.Fatalf("round trip = %+v err=%v", got, err)
	}

	// A file written before the field existed has no blockedReason line.
	legacy := "---\nid: hf_old\ntitle: Old\nstatus: blocked\nsource: triage\n---\n\nbody\n"
	e := Parse("hf_old", legacy)
	if e.Status != StatusBlocked || e.BlockedReason != "" {
		t.Fatalf("legacy blocked entry = %+v", e)
	}
}
