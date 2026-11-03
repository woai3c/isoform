//go:build cgo

package onnxdl

import (
	"fmt"
	"log"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

var runtimeInitMu sync.Mutex

// InitializeRuntime initializes the process-wide ONNX Runtime environment.
// yalue/onnxruntime_go keeps one global environment, so all callers must
// serialize SetSharedLibraryPath + InitializeEnvironment.
func InitializeRuntime(runtimeLib string) error {
	runtimeInitMu.Lock()
	defer runtimeInitMu.Unlock()

	if ort.IsInitialized() {
		return nil
	}

	ort.SetSharedLibraryPath(runtimeLib)
	if err := ort.InitializeEnvironment(); err != nil {
		return fmt.Errorf("init onnx runtime from %s: %w", runtimeLib, err)
	}

	log.Printf("[onnx] runtime initialized: version=%s path=%s", ort.GetVersion(), runtimeLib)
	return nil
}
