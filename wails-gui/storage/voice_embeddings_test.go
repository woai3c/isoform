package storage

import (
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

func mkSelf(t *testing.T, s *Store) PrincipalRow {
	t.Helper()
	row, err := s.EnsureSelf(ctx, "TestUser", nil)
	if err != nil {
		t.Fatalf("EnsureSelf: %v", err)
	}
	return row
}

func TestCreateVoiceEmbedding_RoundTrip(t *testing.T) {
	s := newTestStore(t)
	self := mkSelf(t, s)

	emb := []float32{0.1, -0.2, 0.3, 0.4}
	saved, err := s.CreateVoiceEmbedding(ctx, VoiceEmbeddingRow{
		PrincipalID:  self.ID,
		Embedding:    emb,
		SampleRate:   16000,
		AudioSeconds: 5.5,
		Label:        "first",
	})
	if err != nil {
		t.Fatalf("CreateVoiceEmbedding: %v", err)
	}
	if saved.ID == 0 {
		t.Errorf("ID not assigned")
	}
	if saved.Dim != 4 {
		t.Errorf("Dim = %d, want 4", saved.Dim)
	}

	rows, err := s.ListVoiceEmbeddingsByPrincipal(ctx, self.ID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("len = %d, want 1", len(rows))
	}
	got := rows[0]
	if got.Dim != 4 || len(got.Embedding) != 4 {
		t.Fatalf("dim/embedding mismatch: %+v", got)
	}
	for i := range emb {
		if math.Abs(float64(got.Embedding[i]-emb[i])) > 1e-7 {
			t.Errorf("embedding[%d] = %v, want %v", i, got.Embedding[i], emb[i])
		}
	}
	if got.Label != "first" || got.AudioSeconds != 5.5 {
		t.Errorf("metadata wrong: %+v", got)
	}
}

func TestCreateVoiceEmbedding_DefaultsAndValidation(t *testing.T) {
	s := newTestStore(t)
	self := mkSelf(t, s)

	// Empty embedding rejected.
	if _, err := s.CreateVoiceEmbedding(ctx, VoiceEmbeddingRow{
		PrincipalID: self.ID,
		Embedding:   nil,
	}); err == nil {
		t.Errorf("expected error on empty embedding")
	}

	// Empty principal rejected.
	if _, err := s.CreateVoiceEmbedding(ctx, VoiceEmbeddingRow{
		Embedding: []float32{0.1, 0.2},
	}); err == nil {
		t.Errorf("expected error on empty principal")
	}

	// Dim mismatch rejected.
	if _, err := s.CreateVoiceEmbedding(ctx, VoiceEmbeddingRow{
		PrincipalID: self.ID,
		Embedding:   []float32{0.1, 0.2},
		Dim:         99,
	}); err == nil {
		t.Errorf("expected error on dim/len mismatch")
	}

	// Defaults applied.
	saved, err := s.CreateVoiceEmbedding(ctx, VoiceEmbeddingRow{
		PrincipalID: self.ID,
		Embedding:   []float32{0.1, 0.2},
	})
	if err != nil {
		t.Fatalf("CreateVoiceEmbedding: %v", err)
	}
	if saved.SampleRate != 16000 {
		t.Errorf("SampleRate default = %d, want 16000", saved.SampleRate)
	}
	if saved.CapturedAt == 0 {
		t.Errorf("CapturedAt default not applied")
	}
}

func TestListVoiceEmbeddingsByPrincipal_OrderingNewestFirst(t *testing.T) {
	s := newTestStore(t)
	self := mkSelf(t, s)

	for i := int64(1); i <= 3; i++ {
		if _, err := s.CreateVoiceEmbedding(ctx, VoiceEmbeddingRow{
			PrincipalID: self.ID,
			Embedding:   []float32{float32(i)},
			CapturedAt:  i * 1000,
		}); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}
	rows, _ := s.ListVoiceEmbeddingsByPrincipal(ctx, self.ID)
	if len(rows) != 3 {
		t.Fatalf("len = %d, want 3", len(rows))
	}
	// captured_at DESC → 3000, 2000, 1000
	wantTs := []int64{3000, 2000, 1000}
	for i, ts := range wantTs {
		if rows[i].CapturedAt != ts {
			t.Errorf("rows[%d].CapturedAt = %d, want %d", i, rows[i].CapturedAt, ts)
		}
	}
}

func TestListVoiceEmbeddingsForKind_FiltersByKind(t *testing.T) {
	s := newTestStore(t)
	self := mkSelf(t, s)
	contact, err := s.CreatePrincipal(ctx, PrincipalRow{
		Kind: PrincipalKindContact, DisplayName: "Friend",
	})
	if err != nil {
		t.Fatalf("CreatePrincipal: %v", err)
	}

	_, _ = s.CreateVoiceEmbedding(ctx, VoiceEmbeddingRow{
		PrincipalID: self.ID, Embedding: []float32{1}, Label: "self-1",
	})
	_, _ = s.CreateVoiceEmbedding(ctx, VoiceEmbeddingRow{
		PrincipalID: contact.ID, Embedding: []float32{2}, Label: "contact-1",
	})

	selfRows, _ := s.ListVoiceEmbeddingsForKind(ctx, PrincipalKindSelf)
	if len(selfRows) != 1 || selfRows[0].Label != "self-1" {
		t.Errorf("Self filter wrong: %+v", selfRows)
	}
	contactRows, _ := s.ListVoiceEmbeddingsForKind(ctx, PrincipalKindContact)
	if len(contactRows) != 1 || contactRows[0].Label != "contact-1" {
		t.Errorf("Contact filter wrong: %+v", contactRows)
	}
}

func TestDeleteVoiceEmbedding(t *testing.T) {
	s := newTestStore(t)
	self := mkSelf(t, s)

	saved, _ := s.CreateVoiceEmbedding(ctx, VoiceEmbeddingRow{
		PrincipalID: self.ID, Embedding: []float32{1, 2},
	})
	if err := s.DeleteVoiceEmbedding(ctx, saved.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	rows, _ := s.ListVoiceEmbeddingsByPrincipal(ctx, self.ID)
	if len(rows) != 0 {
		t.Errorf("not deleted: %d remain", len(rows))
	}
	// Second delete of same id should error.
	if err := s.DeleteVoiceEmbedding(ctx, saved.ID); err == nil {
		t.Errorf("second delete should error")
	}
}

func TestVoiceEmbedding_CascadeOnPrincipalDelete(t *testing.T) {
	s := newTestStore(t)
	contact, _ := s.CreatePrincipal(ctx, PrincipalRow{
		Kind: PrincipalKindContact, DisplayName: "ToDelete",
	})
	_, _ = s.CreateVoiceEmbedding(ctx, VoiceEmbeddingRow{
		PrincipalID: contact.ID, Embedding: []float32{1, 2},
	})
	if err := s.DeletePrincipal(ctx, contact.ID); err != nil {
		t.Fatalf("DeletePrincipal: %v", err)
	}
	rows, _ := s.ListVoiceEmbeddingsByPrincipal(ctx, contact.ID)
	if len(rows) != 0 {
		t.Errorf("embeddings not cascaded: %d remain", len(rows))
	}
}

func TestCountVoiceEmbeddingsByPrincipal(t *testing.T) {
	s := newTestStore(t)
	self := mkSelf(t, s)
	n, _ := s.CountVoiceEmbeddingsByPrincipal(ctx, self.ID)
	if n != 0 {
		t.Errorf("initial count = %d, want 0", n)
	}
	for i := 0; i < 3; i++ {
		_, _ = s.CreateVoiceEmbedding(ctx, VoiceEmbeddingRow{
			PrincipalID: self.ID, Embedding: []float32{float32(i)},
			CapturedAt: time.Now().UnixMilli() + int64(i),
		})
	}
	n, _ = s.CountVoiceEmbeddingsByPrincipal(ctx, self.ID)
	if n != 3 {
		t.Errorf("count = %d, want 3", n)
	}
}

func TestEncodeDecodeEmbedding_Symmetric(t *testing.T) {
	cases := [][]float32{
		{0.1, -0.2, 0.3},
		{1.0},
		{-1.0, 0.0, 1.0, 0.5, -0.5},
	}
	for _, in := range cases {
		buf := encodeEmbedding(in)
		if len(buf) != 4*len(in) {
			t.Fatalf("encoded size mismatch: %d vs %d", len(buf), 4*len(in))
		}
		out := decodeEmbedding(buf, len(in))
		if len(out) != len(in) {
			t.Fatalf("decoded len mismatch: %d vs %d", len(out), len(in))
		}
		for i := range in {
			if in[i] != out[i] {
				t.Errorf("round-trip lost data at %d: in=%v out=%v", i, in[i], out[i])
			}
		}
	}
}

// errors.Is check used by other test files; importing for compile.
var _ = errors.Is
var _ = strings.HasPrefix
