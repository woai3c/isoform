//go:build !cgo

package onnxdl

import "fmt"

// InitializeRuntime reports that local ONNX inference is unavailable without
// cgo. Packaged Yansu builds enable cgo for ONNX-backed features.
func InitializeRuntime(runtimeLib string) error {
	return fmt.Errorf("onnx runtime requires cgo")
}
