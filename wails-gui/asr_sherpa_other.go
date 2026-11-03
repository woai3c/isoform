//go:build bindings || !((darwin && (amd64 || arm64) && !ios) || (windows && (amd64 || 386)))

package main

import (
	"errors"

	"wails-gui/internal/asr"
)

// sherpaPaths is unused on platforms where sherpa-onnx isn't shipped, but the
// type is referenced from cross-platform code (e.g. bundled_sherpa.go).
type sherpaPaths struct {
	root         string
	senseVoice   string
	tokens       string
	silero       string
	speakerEmbd  string
	segmentation string
}

func defaultSherpaPaths() sherpaPaths { return sherpaPaths{} }

func initSherpaTranscriber(_ string) (asr.Transcriber, error) {
	return nil, errors.New("sherpa-onnx is not available on this platform")
}

// afterCallTranscriber is the live engine where Qwen3-ASR is not shipped.
func (a *App) afterCallTranscriber() (asr.LongFormTranscriber, func()) {
	live, _ := a.speechTranscriber().(asr.LongFormTranscriber)
	return live, func() {}
}

// afterCallModelView: no after-call model where sherpa-onnx is not shipped.
func (a *App) afterCallModelView() AfterCallSpeechModelView {
	return AfterCallSpeechModelView{Enabled: a.afterCallSpeechModelEnabled(), Status: "absent"}
}

func (a *App) removeAfterCallModel() error { return nil }

// stopQwen3ASRDownload: nothing downloads where sherpa-onnx is not shipped.
func stopQwen3ASRDownload() {}
