package main

import (
	"testing"
	"time"
)

func TestMemoryTombstoneAbbreviationsDoNotSuppressUnrelatedFacts(t *testing.T) {
	for _, tc := range []struct {
		name, removed, unrelated string
	}{
		{"doctor", "Dr. Alice Smith is my oncologist.", "Dr. Bob Jones is my dentist."},
		{"mister", "Mr. Lee is my accountant.", "Mr. Omar is my architect."},
		{"saint", "St. Anne is my local clinic.", "St. George is my local library."},
		{"example", "e.g. Cycling is my weekend exercise.", "e.g. Painting is my evening hobby."},
		{"doctor within assertion", "Works with Dr. Chen.", "Works with Dr. Wang."},
		{"dotted acronym within assertion", "Lives in the U.S. now.", "Lives in the U.S. occasionally."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			now := time.Now()
			original := optionalMemoryObservation("Removed observation", tc.removed)
			if err := recordMemoryTombstone(dir, "removed", []string{original}, now); err != nil {
				t.Fatal(err)
			}
			unrelated := optionalMemoryObservation("Independent observation", tc.unrelated)
			if forgotten, err := isMemoryForgotten(dir, "independent", unrelated, now); err != nil || forgotten {
				t.Errorf("shared abbreviation suppressed a different fact: forgotten=%v err=%v", forgotten, err)
			}
			repeated := optionalMemoryObservation("Observed under another title", tc.removed)
			if forgotten, err := isMemoryForgotten(dir, "observed-again", repeated, now); err != nil || !forgotten {
				t.Errorf("complete forgotten assertion returned under a new ID/title: forgotten=%v err=%v", forgotten, err)
			}
		})
	}
}

func TestMemoryTombstoneMeaningfulShortFactsRemainSuppressed(t *testing.T) {
	for _, tc := range []struct{ name, fact string }{
		{"two words with three letters", "I go."},
		{"two word preference", "No ads."},
		{"short terminal word", "Uses Go."},
		{"two Korean syllables", "금연."},
		{"four letter assertion", "Deaf."},
		{"single word preference", "Vegan."},
		{"two Chinese characters", "素食。"},
		{"two Japanese characters", "禁煙。"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			now := time.Now()
			original := optionalMemoryObservation("Removed short fact", tc.fact)
			if err := recordMemoryTombstone(dir, "removed", []string{original}, now); err != nil {
				t.Fatal(err)
			}
			for _, candidate := range []string{
				optionalMemoryObservation("New larger observation", tc.fact, "Reads novels on weekends."),
				"# New plain observation\n\n- **Source**: activity\n\n" + tc.fact + " Reads novels on weekends.\n",
			} {
				if forgotten, err := isMemoryForgotten(dir, "new-observation", candidate, now); err != nil || !forgotten {
					t.Fatalf("meaningful short fact returned inside a larger record: forgotten=%v err=%v", forgotten, err)
				}
			}
		})
	}
}

func TestMemoryTombstoneTinyFragmentsDoNotSuppressOtherRecords(t *testing.T) {
	for _, fragment := range []string{"A.", "Dr.", "Mr.", "St.", "e.g.", "U.S."} {
		t.Run(fragment, func(t *testing.T) {
			dir := t.TempDir()
			now := time.Now()
			original := optionalMemoryObservation("Removed fragment", fragment)
			if err := recordMemoryTombstone(dir, "removed", []string{original}, now); err != nil {
				t.Fatal(err)
			}
			candidate := optionalMemoryObservation("Independent record", fragment, "Checks the calendar each morning.")
			if forgotten, err := isMemoryForgotten(dir, "independent", candidate, now); err != nil || forgotten {
				t.Errorf("tiny fragment became a reusable fact key: forgotten=%v err=%v", forgotten, err)
			}
			if forgotten, err := isMemoryForgotten(dir, "exact-copy", original, now); err != nil || !forgotten {
				t.Errorf("complete-record suppression was lost: forgotten=%v err=%v", forgotten, err)
			}
			if forgotten, err := isMemoryForgotten(dir, "removed", candidate, now); err != nil || !forgotten {
				t.Errorf("deleted-ID suppression was lost: forgotten=%v err=%v", forgotten, err)
			}
		})
	}
}

func TestMemoryTombstoneSingleLetterEndsAssertionAfterLowercaseWord(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	if err := recordMemoryTombstone(dir, "removed", []string{optionalMemoryObservation("Removed observation", "Takes vitamin D.")}, now); err != nil {
		t.Fatal(err)
	}
	// The forgotten sentence returns inside a longer line under a new ID.
	again := optionalMemoryObservation("Observed again", "Takes vitamin D. Reads novels on weekends.")
	if forgotten, err := isMemoryForgotten(dir, "again", again, now); err != nil || !forgotten {
		t.Fatalf("a forgotten sentence ending in a single letter came back: forgotten=%v err=%v", forgotten, err)
	}
	// A name with an initial stays one assertion and does not suppress its first name.
	if err := recordMemoryTombstone(dir, "removed-name", []string{optionalMemoryObservation("Removed observation", "John F. Kennedy is my hero.")}, now); err != nil {
		t.Fatal(err)
	}
	other := optionalMemoryObservation("Independent observation", "John Smith is my neighbour.")
	if forgotten, err := isMemoryForgotten(dir, "other", other, now); err != nil || forgotten {
		t.Fatalf("a shared first name suppressed a different fact: forgotten=%v err=%v", forgotten, err)
	}
}
