package pii

import (
	"fmt"
	"reflect"
	"testing"

	ort "github.com/yalue/onnxruntime_go"
)

type fakeGLiNERSessionOptions struct {
	calls []string
}

func (f *fakeGLiNERSessionOptions) SetIntraOpNumThreads(n int) error {
	f.calls = append(f.calls, "intra")
	if n != glinerONNXIntraOpThreads {
		return fmt.Errorf("intra-op thread count = %d, want %d", n, glinerONNXIntraOpThreads)
	}
	return nil
}

func (f *fakeGLiNERSessionOptions) SetInterOpNumThreads(n int) error {
	f.calls = append(f.calls, "inter")
	if n != glinerONNXInterOpThreads {
		return fmt.Errorf("inter-op thread count = %d, want %d", n, glinerONNXInterOpThreads)
	}
	return nil
}

func (f *fakeGLiNERSessionOptions) SetExecutionMode(mode ort.ExecutionMode) error {
	f.calls = append(f.calls, "mode")
	if mode != ort.ExecutionModeSequential {
		return fmt.Errorf("execution mode = %v, want %v", mode, ort.ExecutionModeSequential)
	}
	return nil
}

func (f *fakeGLiNERSessionOptions) SetCpuMemArena(enabled bool) error {
	f.calls = append(f.calls, "arena")
	if enabled != glinerONNXCPUMemArena {
		return fmt.Errorf("CPU memory arena = %t, want %t", enabled, glinerONNXCPUMemArena)
	}
	return nil
}

func (f *fakeGLiNERSessionOptions) SetMemPattern(enabled bool) error {
	f.calls = append(f.calls, "mem_pattern")
	if enabled != glinerONNXMemPattern {
		return fmt.Errorf("memory pattern = %t, want %t", enabled, glinerONNXMemPattern)
	}
	return nil
}

func TestConfigureGLiNERSessionOptionsBoundsCPUAndMemory(t *testing.T) {
	opts := &fakeGLiNERSessionOptions{}
	if err := configureGLiNERSessionOptions(opts); err != nil {
		t.Fatalf("configureGLiNERSessionOptions: %v", err)
	}

	wantCalls := []string{"intra", "inter", "mode", "arena", "mem_pattern"}
	if !reflect.DeepEqual(opts.calls, wantCalls) {
		t.Fatalf("calls = %v, want %v", opts.calls, wantCalls)
	}

	if glinerONNXCPUMemArena {
		t.Fatal("GLiNER CPU memory arena should stay disabled to avoid native RSS retention")
	}
	if glinerONNXMemPattern {
		t.Fatal("GLiNER memory pattern should stay disabled for dynamic OCR input sizes")
	}
}
