package main

import (
	"encoding/hex"
	"testing"
)

func TestKokoroArchiveSHA256Pinned(t *testing.T) {
	sum, err := hex.DecodeString(sha256KokoroEnArchive)
	if err != nil {
		t.Fatalf("decode Kokoro SHA-256: %v", err)
	}
	if len(sum) != 32 {
		t.Fatalf("Kokoro SHA-256 length = %d bytes, want 32", len(sum))
	}
}
