package main

import "testing"

// A memory title keeps the words it was saved under. The noun-phrase rule
// ("Pricing call" -> "You care about Pricing call") is for beliefs only; the
// correction gate and the Learned list compare the saved title.
func TestMemoryTitleInSecondPersonKeepsNounPhrases(t *testing.T) {
	for _, tc := range []struct {
		name, input, want string
	}{
		{"call title keeps its words", "Pricing call", "Pricing call"},
		{"lowercase title keeps its words", "private corrected amber workspace", "private corrected amber workspace"},
		{"first person still reads You", "I prefer clear explanations", "You prefer clear explanations"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := memoryTitleInSecondPerson(tc.input); got != tc.want {
				t.Fatalf("memoryTitleInSecondPerson(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
	if got := beliefInSecondPerson("Pricing call"); got != "You care about Pricing call" {
		t.Fatalf("beliefInSecondPerson must still read a belief noun phrase: %q", got)
	}
}
