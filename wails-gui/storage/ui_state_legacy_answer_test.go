package storage

import "testing"

func TestUniqueLegacyMainAnswerLeavesAmbiguousAnswersAlone(t *testing.T) {
	s, _ := proactiveItemsTestStore(t)
	if _, err := s.db.ExecContext(t.Context(), `UPDATE sessions SET kind='main' WHERE local_id='chat'`); err != nil {
		t.Fatal(err)
	}
	appendProactiveTestItem(t, s, "chat", StreamItemRow{ID: "week", Kind: "user", Content: "release isoform 1.0", CreatedAt: 2000})
	if id, err := s.UniqueLegacyMainAnswer(t.Context(), "release isoform 1.0", 1000, 9000); err != nil || id != "week" {
		t.Fatalf("unique answer = %q, %v", id, err)
	}

	appendProactiveTestItem(t, s, "chat", StreamItemRow{ID: "question", Kind: "assistant", Content: "What should ship first?", CreatedAt: 3000})
	appendProactiveTestItem(t, s, "chat", StreamItemRow{ID: "reply", Kind: "user", Content: "ship the beta", CreatedAt: 3100})
	if id, err := s.UniqueLegacyMainAnswer(t.Context(), "ship the beta", 1000, 9000); err != nil || id != "" {
		t.Fatalf("answer to a chat question associated: %q, %v", id, err)
	}

	appendProactiveTestItem(t, s, "chat", StreamItemRow{ID: "again", Kind: "user", Content: "release isoform 1.0", CreatedAt: 4000})
	if id, err := s.UniqueLegacyMainAnswer(t.Context(), "release isoform 1.0", 1000, 9000); err != nil || id != "" {
		t.Fatalf("two matching rows associated: %q, %v", id, err)
	}
}
