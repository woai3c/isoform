package main

import (
	"log"
	"os"
	"path/filepath"
)

// cleanupLegacyWhisperData removes the leftover whisper.cpp install directory
// (~/.yansu-agent/whisper) that pre-sherpa builds populated. It only runs when
// sherpa-onnx assets are confirmed in place, so a user who somehow has both
// installed never loses voice input mid-upgrade.
//
// Best-effort: any error is logged and swallowed. Re-running on a clean install
// (no whisper dir, sherpa already there) is a no-op.
func cleanupLegacyWhisperData() {
	dataRoot, err := flavorDataDir()
	if err != nil {
		return
	}
	sherpaPaths := defaultSherpaPaths()
	// Only delete legacy data if the new sherpa assets are actually present.
	// Otherwise we'd leave the user with no working voice input on first launch
	// when the sherpa downloader hasn't finished yet.
	if !fileExistsAndNonEmpty(sherpaPaths.senseVoice) || !fileExistsAndNonEmpty(sherpaPaths.silero) {
		return
	}

	whisperDir := filepath.Join(dataRoot, "whisper")
	st, err := os.Stat(whisperDir)
	if err != nil || !st.IsDir() {
		return
	}

	if err := os.RemoveAll(whisperDir); err != nil {
		log.Printf("[whisper-legacy] failed to remove %s: %v", whisperDir, err)
		return
	}
	log.Printf("[whisper-legacy] removed legacy whisper.cpp install at %s", whisperDir)
}
