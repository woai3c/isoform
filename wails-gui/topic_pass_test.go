package main

import (
	"context"
	"testing"
)

// A pass shares one noticed lookup: a date is read once for every brief of the
// pass, and no lookup leaks into a later pass.
func TestNoticedPassReadsEachDateOnceAcrossBriefs(t *testing.T) {
	reads := 0
	lookup := &mobileBriefNoticedLookup{days: map[string]map[string]NoticedSuggestion{}, read: func(string) ([]NoticedSuggestion, error) {
		reads++
		return nil, nil
	}}
	ctx := context.WithValue(context.Background(), mobileBriefNoticedPassKey{}, lookup)
	for i := 0; i < 5; i++ {
		if got := mobileBriefNoticedFor(ctx, nil); got != lookup {
			t.Fatal("the pass lookup was not reused")
		}
		mobileBriefNoticedFor(ctx, nil).suggestion("2026-11-06/item-" + string(rune('a'+i)))
	}
	if reads != 1 {
		t.Fatalf("GetDayNoticed reads for one date in one pass = %d, want 1", reads)
	}
	if other := mobileBriefNoticedFor(context.Background(), nil); other == lookup {
		t.Fatal("a pass lookup leaked outside its pass")
	}
}
