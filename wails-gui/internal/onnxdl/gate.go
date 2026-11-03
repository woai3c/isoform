package onnxdl

import "sync"

// inferenceGate serializes heavy local ONNX inference across every model in the
// process. Each model already caps itself to a couple of intra-op threads, but
// two models running concurrently (the PII GLiNER detector and the
// activity/memory embedding model use independent semaphores) doubles the CPU
// pressure. Combined with Apple Vision OCR — which saturates a core — that was
// enough to starve a foreground app's UI thread and make typing sluggish. This
// gate keeps peak CPU to one model at a time.
//
// Only the session.Run call is wrapped, so hold time stays short; tokenization
// and pooling run outside the gate. There is no lock-ordering cycle: callers
// take their own per-model semaphore first, then this gate, never the reverse.
var inferenceGate sync.Mutex

// AcquireInference blocks until the shared inference gate is free, then returns
// a release func. Wrap a single session.Run:
//
//	release := onnxdl.AcquireInference()
//	err := session.Run(...)
//	release()
func AcquireInference() (release func()) {
	inferenceGate.Lock()
	return inferenceGate.Unlock
}
