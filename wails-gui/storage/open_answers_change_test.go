package storage

import (
	"database/sql"
	"errors"
	"testing"
)

func TestRetimeOpenAnswerResetsCheckBaseline(t *testing.T) {
	s, _ := proactiveItemsTestStore(t)
	answer := openAnswerFixture(1)
	answer.LastValue = "old result"
	answer.LastCheckAt = 1500
	answer.HeldTransition = "settled"
	answer.HeldText = "An answer is ready."
	answer.HeldAt = 1750
	if err := s.CreateOpenAnswer(t.Context(), answer); err != nil {
		t.Fatal(err)
	}
	if err := s.RetimeOpenAnswer(t.Context(), answer.ID, 6000, 7000); err != nil {
		t.Fatal(err)
	}
	updated, err := s.GetOpenAnswer(t.Context(), answer.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.WindowStart != 6000 || updated.WindowEnd != 6000 || updated.ExpiresAt != 7000 ||
		updated.LastValue != "" || updated.LastCheckAt != 0 ||
		updated.HeldTransition != "" || updated.HeldText != "" || updated.HeldAt != 0 {
		t.Fatalf("retimed row = %+v", updated)
	}
	if err := s.CancelOpenAnswer(t.Context(), answer.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RetimeOpenAnswer(t.Context(), answer.ID, 8000, 9000); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("retime after cancellation error = %v", err)
	}
}

func TestOpenAnswerChangeTurnMustBeLatestMainUser(t *testing.T) {
	s, _ := proactiveItemsTestStore(t)
	if _, err := s.db.ExecContext(t.Context(), `UPDATE sessions SET kind='main' WHERE local_id='chat'`); err != nil {
		t.Fatal(err)
	}
	appendProactiveTestItem(t, s, "chat", StreamItemRow{ID: "user", Kind: "user", Content: "Change it"})
	if err := s.BindAnswerChipUserRun(t.Context(), "chat", "user", "run"); err != nil {
		t.Fatal(err)
	}
	current, err := s.IsCurrentOpenAnswerChangeTurn(t.Context(), "chat", "user", "run")
	if err != nil || !current {
		t.Fatalf("current turn = %v, error = %v", current, err)
	}
	if current, err := s.IsCurrentOpenAnswerChangeTurn(t.Context(), "chat", "user", "other"); err != nil || current {
		t.Fatalf("wrong run current = %v, error = %v", current, err)
	}
	if current, err := s.IsCurrentOpenAnswerChangeTurn(t.Context(), "other", "user", "run"); err != nil || current {
		t.Fatalf("wrong session current = %v, error = %v", current, err)
	}
	appendProactiveTestItem(t, s, "chat", StreamItemRow{ID: "later-user", Kind: "user", Content: "Later", RunID: "later-run"})
	if current, err := s.IsCurrentOpenAnswerChangeTurn(t.Context(), "chat", "user", "run"); err != nil || current {
		t.Fatalf("superseded turn current = %v, error = %v", current, err)
	}
}
