package main

import (
	"path/filepath"
	"testing"

	"wails-gui/storage"
)

func newVoiceTestApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	store, err := storage.New(filepath.Join(dir, "voice-test.db"))
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return &App{store: store}
}

func TestApp_GetVoiceEnrollmentStatus_NoSamples(t *testing.T) {
	a := newVoiceTestApp(t)
	self, _ := a.EnsureSelfProfile("TestUser", nil)
	got, err := a.GetVoiceEnrollmentStatus(self.ID)
	if err != nil {
		t.Fatalf("GetVoiceEnrollmentStatus: %v", err)
	}
	if got.SampleCount != 0 {
		t.Errorf("SampleCount = %d, want 0", got.SampleCount)
	}
	if got.PrincipalID != self.ID {
		t.Errorf("PrincipalID = %q, want %q", got.PrincipalID, self.ID)
	}
}

func TestApp_GetVoiceEnrollmentStatus_WithSamples(t *testing.T) {
	a := newVoiceTestApp(t)
	self, _ := a.EnsureSelfProfile("TestUser", nil)
	for i := int64(1); i <= 3; i++ {
		if _, err := a.store.CreateVoiceEmbedding(bindingsCtx, storage.VoiceEmbeddingRow{
			PrincipalID: self.ID,
			Embedding:   []float32{float32(i), 0, 0},
			CapturedAt:  i * 1000,
		}); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
	got, _ := a.GetVoiceEnrollmentStatus(self.ID)
	if got.SampleCount != 3 {
		t.Errorf("SampleCount = %d, want 3", got.SampleCount)
	}
	if got.LastCapturedAt != 3000 {
		t.Errorf("LastCapturedAt = %d, want 3000", got.LastCapturedAt)
	}
}

func TestApp_ListVoiceSamples_Empty(t *testing.T) {
	a := newVoiceTestApp(t)
	self, _ := a.EnsureSelfProfile("TestUser", nil)
	got, err := a.ListVoiceSamples(self.ID)
	if err != nil {
		t.Fatalf("ListVoiceSamples: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}

func TestApp_ListVoiceSamples_NewestFirstAndOmitsBytes(t *testing.T) {
	a := newVoiceTestApp(t)
	self, _ := a.EnsureSelfProfile("TestUser", nil)
	for i := int64(1); i <= 3; i++ {
		_, _ = a.store.CreateVoiceEmbedding(bindingsCtx, storage.VoiceEmbeddingRow{
			PrincipalID:  self.ID,
			Embedding:    []float32{float32(i)},
			CapturedAt:   i * 1000,
			AudioSeconds: 5.0,
		})
	}
	got, _ := a.ListVoiceSamples(self.ID)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	if got[0].CapturedAt != 3000 || got[2].CapturedAt != 1000 {
		t.Errorf("order wrong: %+v", got)
	}
	// DTO must NOT expose embedding bytes — the UI shouldn't be sent
	// the raw vectors over the bridge. Verified by the type itself
	// (no Embedding field), but exercise an arbitrary metadata access
	// so the test references everything.
	for _, s := range got {
		if s.Dim != 1 || s.AudioSeconds != 5.0 {
			t.Errorf("metadata mismatch: %+v", s)
		}
	}
}

func TestApp_DeleteVoiceSample_RemovesAndRefreshesCache(t *testing.T) {
	a := newVoiceTestApp(t)
	self, _ := a.EnsureSelfProfile("TestUser", nil)
	saved, err := a.store.CreateVoiceEmbedding(bindingsCtx, storage.VoiceEmbeddingRow{
		PrincipalID: self.ID, Embedding: []float32{1, 2, 3},
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := a.DeleteVoiceSample(saved.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	rows, _ := a.ListVoiceSamples(self.ID)
	if len(rows) != 0 {
		t.Errorf("not deleted: %d rows remain", len(rows))
	}
	// Second delete should error.
	if err := a.DeleteVoiceSample(saved.ID); err == nil {
		t.Errorf("second delete should error")
	}
}

func TestApp_EnrollVoiceSample_RequiresStore(t *testing.T) {
	a := &App{}
	_, err := a.EnrollVoiceSample("ghost", 10)
	if err == nil {
		t.Errorf("expected error without store")
	}
}

func TestApp_EnrollVoiceSample_PrincipalMustExist(t *testing.T) {
	a := newVoiceTestApp(t)
	_, err := a.EnrollVoiceSample("ghost-id", 10)
	if err == nil {
		t.Errorf("expected error for unknown principal")
	}
}
