package main

import "testing"

// noticed:updated is announced only for a real change of a day's set: the first
// sighting is a baseline, an unchanged read is silent, a changed one is heard.
func TestNoticedPublicationEmitsOnlyOnRealChange(t *testing.T) {
	var p noticedPublications
	if p.changed("2026-11-04", "a") {
		t.Fatal("the first sighting counted as a change")
	}
	if p.changed("2026-11-04", "a") {
		t.Fatal("an unchanged set counted as a change")
	}
	if !p.changed("2026-11-04", "b") {
		t.Fatal("a changed set was not announced")
	}
	if p.changed("2026-11-05", "a") {
		t.Fatal("another day's baseline counted as a change")
	}
}
