//go:build !cgo

package main

import (
	"context"
	"fmt"
)

// newLocalONNXProvider returns an error when CGO is not available.
// The ONNX runtime requires CGO for its C bindings.
func newLocalONNXProvider(configDir string, onProgress downloadProgressFn) (*localONNXProviderStub, error) {
	return nil, fmt.Errorf("local ONNX provider requires CGO (not available in this build)")
}

// localONNXProviderStub is a placeholder type used when CGO is disabled.
type localONNXProviderStub struct{}

func (p *localONNXProviderStub) Provider() string    { return "local-onnx" }
func (p *localONNXProviderStub) Model() string       { return "" }
func (p *localONNXProviderStub) ProviderKey() string { return "" }
func (p *localONNXProviderStub) EmbedQuery(_ context.Context, _ string) ([]float64, error) {
	return nil, nil
}
func (p *localONNXProviderStub) EmbedBatch(_ context.Context, _ []string) ([][]float64, error) {
	return nil, nil
}
func (p *localONNXProviderStub) Destroy() {}
