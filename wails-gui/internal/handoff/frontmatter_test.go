package handoff

import "testing"

func TestAttemptStartedAtSurvivesLegacyParseAndJSONRoundTrip(t *testing.T) {
	const raw = `---
id: hf-attempt
status: in_progress
attemptId: attempt-1
attemptStartedAt: 1730037600000
lastActivityAt: 1730037660000
---
Do the work.
`
	legacy := Parse("hf-attempt", raw)
	if legacy.AttemptStartedAt != 1730037600000 {
		t.Fatalf("legacy attempt start = %d", legacy.AttemptStartedAt)
	}

	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(legacy); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get("hf-attempt")
	if err != nil {
		t.Fatal(err)
	}
	if got.AttemptStartedAt != legacy.AttemptStartedAt {
		t.Fatalf("stored attempt start = %d, want %d", got.AttemptStartedAt, legacy.AttemptStartedAt)
	}
}
