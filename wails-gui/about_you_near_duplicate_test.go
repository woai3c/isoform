package main

import "testing"

func TestNearDuplicateBeliefs(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"Wants public repo commits to carry the real fix messages", "Wants public repo sync commits to carry the real fix messages", true},
		{"Wants public repo commits to carry the real fix messages", "You want public repo commits to carry the real fix messages", true},
		{"Prefers bun over npm", "Prefers bun over npm", true},
		{"Prefers bun over npm", "Prefers npm over yarn", false},
		{"Keeps the backup hub read-only", "Keeps calls short before 11 AM", false},
		{"Writes short replies", "Writes short release notes", false},
		{"", "Writes short replies", false},
	} {
		if got := nearDuplicateBeliefs(tc.a, tc.b); got != tc.want {
			t.Errorf("nearDuplicateBeliefs(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}
