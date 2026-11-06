package identity

import "testing"

// A name never carries a date or a time: a captured message timestamp is not a name (N50).
func TestPlausibleAutoContactNameRejectsMessageTimestamps(t *testing.T) {
	for _, bad := range []string{"v1t0 11/1/26, 6:43 PM", "Priya 2026-11-01", "Ana 9:05", "Ben 14.11.26"} {
		if PlausibleAutoContactName(bad) {
			t.Fatalf("%q was accepted as a name", bad)
		}
	}
	for _, good := range []string{"Priya Shah", "v1t0", "Maria Okafor", "Ana Lopez-Ruiz"} {
		if !PlausibleAutoContactName(good) {
			t.Fatalf("%q was rejected as a name", good)
		}
	}
}
