package main

import (
	"context"
	"reflect"
	"testing"
	"time"
)

type activityEmbeddingTestProvider struct {
	batches [][]string
}

func (p *activityEmbeddingTestProvider) EmbedBatch(
	_ context.Context,
	texts []string,
) ([][]float64, error) {
	p.batches = append(p.batches, append([]string(nil), texts...))
	result := make([][]float64, len(texts))
	for index := range result {
		result[index] = []float64{float64(index) + 0.25, 0.5}
	}
	return result, nil
}

func (p *activityEmbeddingTestProvider) EmbedQuery(
	_ context.Context,
	_ string,
) ([]float64, error) {
	return []float64{0.25, 0.5}, nil
}

func (*activityEmbeddingTestProvider) Provider() string    { return "test" }
func (*activityEmbeddingTestProvider) Model() string       { return "test" }
func (*activityEmbeddingTestProvider) ProviderKey() string { return "test" }

func TestBackfillEmbeddingsReleasesReadRowsBeforeEmbedding(t *testing.T) {
	store := newTestStore(t)
	insertSession := func(id string, startAt int64, triageStatus string) {
		t.Helper()
		if _, err := store.db.Exec(`
			INSERT INTO activity_sessions (
				id, start_time, end_time, trigger_kind, app_names,
				event_count, segment_count, triage_status, created_at
			) VALUES (?, ?, ?, 'test', '[]', 1, 1, ?, ?)`,
			id,
			startAt,
			startAt+1,
			triageStatus,
			startAt,
		); err != nil {
			t.Fatal(err)
		}
	}
	insertFrame := func(sessionID, text string, embedding any) {
		t.Helper()
		if _, err := store.db.Exec(`
			INSERT INTO activity_ocr_frames (
				session_id, segment_id, frame_time, text, embedding
			) VALUES (?, 1, 1, ?, ?)`,
			sessionID,
			text,
			embedding,
		); err != nil {
			t.Fatal(err)
		}
	}

	insertSession("protected", 100, "protected")
	insertFrame("protected", "private text", nil)
	insertSession("empty", 150, "pending")
	insertFrame("empty", "", nil)
	insertSession("eligible", 200, "pending")
	insertFrame("eligible", "source-backed text", nil)
	insertSession("complete", 300, "pending")
	insertFrame("complete", "already embedded", `[0.1,0.2]`)

	ids, err := store.listEmbeddingBackfillSessionIDs(context.Background(), (&App{}).activityEmbeddingGate(store))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ids, []string{"eligible"}) {
		t.Fatalf("backfill candidates=%v, want only eligible", ids)
	}
	if stats := store.db.Stats(); stats.InUse != 0 {
		t.Fatalf("candidate query retained %d database connections", stats.InUse)
	}

	provider := &activityEmbeddingTestProvider{}
	store.SetEmbedder(provider)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	store.BackfillEmbeddings(ctx, (&App{}).activityEmbeddingGate(store))
	if err := ctx.Err(); err != nil {
		t.Fatalf("backfill did not release the single database connection: %v", err)
	}
	if !reflect.DeepEqual(provider.batches, [][]string{{"source-backed text"}}) {
		t.Fatalf("embedded batches=%v", provider.batches)
	}

	var eligibleEmbedding string
	if err := store.db.QueryRow(`
		SELECT COALESCE(embedding, '')
		FROM activity_ocr_frames
		WHERE session_id = 'eligible'`,
	).Scan(&eligibleEmbedding); err != nil {
		t.Fatal(err)
	}
	if eligibleEmbedding == "" {
		t.Fatal("eligible frame was not embedded")
	}
	for _, sessionID := range []string{"protected", "empty"} {
		var embedding string
		if err := store.db.QueryRow(`
			SELECT COALESCE(embedding, '')
			FROM activity_ocr_frames
			WHERE session_id = ?`,
			sessionID,
		).Scan(&embedding); err != nil {
			t.Fatal(err)
		}
		if embedding != "" {
			t.Fatalf("session %q unexpectedly embedded", sessionID)
		}
	}
}
