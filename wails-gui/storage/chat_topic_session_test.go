package storage

import (
	"path/filepath"
	"testing"
)

func TestTopicSessionRotationPersistsAndIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.ReadRevision(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		original, err := s.ResolveTopicChatSession(t.Context(), "main", "original-session", nil)
		if err != nil || original != "original-session" {
			t.Fatalf("ordinary session: %q %v", original, err)
		}
	}
	after, err := s.ReadRevision(t.Context())
	if err != nil || before != after {
		t.Fatalf("ordinary turns mutated session state: %v", err)
	}
	first, err := s.ResolveTopicChatSession(t.Context(), "main", "original-session", []string{"run-a", "run-a", "run-b"})
	if err != nil || first == "" || first == "original-session" {
		t.Fatalf("rotation: %q %v", first, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, runs := range [][]string{nil, {"run-a"}, {"run-b", "run-a"}} {
		next, err := s.ResolveTopicChatSession(t.Context(), "main", "original-session", runs)
		if err != nil || next != first {
			t.Fatalf("restart or stale caller reset the session: %q %v", next, err)
		}
	}
	second, err := s.ResolveTopicChatSession(t.Context(), "main", first, []string{"run-c"})
	if err != nil || second == "" || second == first {
		t.Fatalf("new withdrawal did not rotate: %q %v", second, err)
	}
	other, err := s.ResolveTopicChatSession(t.Context(), "other", "other-session", []string{"run-a"})
	if err != nil || other == second || other == first {
		t.Fatalf("session epochs collided: %q %v", other, err)
	}
}
func TestTopicSessionRotationRollsBackReceiptOnFailure(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.db.Exec(`CREATE TRIGGER reject_topic_epoch BEFORE INSERT ON ui_state WHEN NEW.key='chat_topic_session:main'
 BEGIN SELECT RAISE(FAIL,'fixture rotation failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveTopicChatSession(t.Context(), "main", "original", []string{"run"}); err == nil {
		t.Fatal("failure accepted")
	}
	var receipts int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM ui_state WHERE key LIKE 'chat_topic_session:main%'`).Scan(&receipts); err != nil || receipts != 0 {
		t.Fatalf("partial withdrawal receipt: %d %v", receipts, err)
	}
	if _, err := s.db.Exec(`DROP TRIGGER reject_topic_epoch`); err != nil {
		t.Fatal(err)
	}
	current, err := s.ResolveTopicChatSession(t.Context(), "main", "original", []string{"run"})
	if err != nil || current == "" || current == "original" {
		t.Fatalf("retry lost rotation: %q %v", current, err)
	}
}
