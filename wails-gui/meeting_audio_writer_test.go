package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMeetingAudioWriterRemoveFinalizedRemovesOnlyOwnedWAV(t *testing.T) {
	audioDir := t.TempDir()
	writer, err := newMeetingAudioWriter(audioDir)
	if err != nil {
		t.Fatalf("newMeetingAudioWriter: %v", err)
	}
	writer.WriteChunk(backgroundListeningSourceMicrophone, makeWAV([]int16{1000, -1000, 500, -500}))

	relPath, _, err := writer.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if relPath == "" {
		t.Fatal("Finalize returned no WAV path")
	}
	fullPath := filepath.Join(audioDir, relPath)
	if _, err := os.Stat(fullPath); err != nil {
		t.Fatalf("finalized WAV missing: %v", err)
	}

	writer.RemoveFinalized(filepath.Join("other", "recording.wav"))
	if _, err := os.Stat(fullPath); err != nil {
		t.Fatalf("unowned path removed finalized WAV: %v", err)
	}

	writer.RemoveFinalized(relPath)
	if _, err := os.Stat(fullPath); !os.IsNotExist(err) {
		t.Fatalf("owned finalized WAV still exists: err=%v", err)
	}
}
