package storage

import (
	"context"
	"testing"
)

func TestPostUIStateAnswerRollsBackWithoutMainChat(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.PostUIStateAnswer(ctx, "answer", "", "first", "missing", "message"); err == nil {
		t.Fatal("missing main chat accepted")
	}
	if got, err := s.GetUIState(ctx, "answer"); err != nil || got != "" {
		t.Fatalf("partial answer persisted: %q %v", got, err)
	}
}
