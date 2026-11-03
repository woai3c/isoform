//go:build bindings || !((darwin && (amd64 || arm64) && !ios) || (windows && (amd64 || 386)))

package main

import "errors"

// newSherpaTTS is a stub on platforms where sherpa-onnx isn't shipped. The
// cross-platform App methods reference speechSynthesizer (see tts_runtime.go),
// so this keeps the package buildable everywhere.
func newSherpaTTS(_ ttsPaths) (speechSynthesizer, error) {
	return nil, errors.New("sherpa-onnx TTS is not available on this platform")
}

func newKokoroTTS(_ kokoroPaths) (speechSynthesizer, error) {
	return nil, errors.New("sherpa-onnx TTS is not available on this platform")
}
