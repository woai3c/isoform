//go:build darwin

package ocr

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDarwinHelperDisabledSkipsProcess(t *testing.T) {
	t.Setenv(darwinHelperDisableEnv, "1")
	resp, err := runDarwinOCRHelper(helperRequest{Mode: "boxes", JPEGData: []byte("unused")})
	if resp != nil || err != nil {
		t.Fatalf("disabled helper should fall back: response=%v err=%v", resp, err)
	}
}

func TestDarwinHelperPathOverrideAndChildGuard(t *testing.T) {
	path := filepath.Join(t.TempDir(), "helper")
	if err := os.WriteFile(path, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv(darwinHelperPathEnv, path)
	t.Setenv(darwinHelperChildEnv, "")
	t.Setenv(darwinHelperDisableEnv, "")
	if got := darwinOCRHelperPath(); got != path {
		t.Fatalf("helper path = %q, want %q", got, path)
	}
	t.Setenv(darwinHelperChildEnv, "1")
	if got := darwinOCRHelperPath(); got != "" {
		t.Fatalf("helper child must not recursively spawn: %q", got)
	}
}

func TestDarwinHelperPrewarmSendsRecognitionImage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "helper")
	if err := os.WriteFile(path, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv(darwinHelperPathEnv, path)
	t.Setenv(darwinHelperChildEnv, "")
	t.Setenv(darwinHelperDisableEnv, "")
	previous := darwinHelper
	darwinHelper = testHelperProcess(t)
	t.Cleanup(func() { darwinHelper = previous })
	PrewarmHelper()
	if !darwinHelper.warm || darwinHelper.nextID != 1 || darwinHelper.requests != 0 || darwinHelper.droppedFrames != 0 {
		t.Fatalf("prewarm did not recognize a valid synthetic image: warm=%v requests=%d drops=%d", darwinHelper.warm, darwinHelper.requests, darwinHelper.droppedFrames)
	}
}
