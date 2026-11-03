package pii

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestPIIModelFileUsesFP32Export(t *testing.T) {
	if got := piiModelFile(); got != "model.onnx" {
		t.Fatalf("piiModelFile() = %q, want model.onnx", got)
	}
}

func TestStaleModelFilesRemovesUnsupportedExports(t *testing.T) {
	got := staleModelFiles()
	want := []string{"model_fp16.onnx", "model_quint8.onnx"}
	if len(got) != len(want) {
		t.Fatalf("staleModelFiles() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("staleModelFiles() = %v, want %v", got, want)
		}
	}
}

func TestPIIAssetURLsPreferR2Mirror(t *testing.T) {
	got := piiAssetURLs("onnx/model.onnx")
	want := []string{
		"https://model-assets.yansu.app/gliner-pii/base-v1.0/onnx/model.onnx",
		"https://huggingface.co/knowledgator/gliner-pii-base-v1.0/resolve/main/onnx/model.onnx",
	}
	if len(got) != len(want) {
		t.Fatalf("piiAssetURLs() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("piiAssetURLs() = %v, want %v", got, want)
		}
	}
}

func TestURLsForPIIAssetUsesEnvOverride(t *testing.T) {
	t.Setenv(envPIIModelURL, "https://example.invalid/model.onnx")

	got, override := urlsForPIIAsset(envPIIModelURL, piiAssetURLs("onnx/model.onnx"))
	if !override {
		t.Fatal("urlsForPIIAsset override = false, want true")
	}
	if len(got) != 1 || got[0] != "https://example.invalid/model.onnx" {
		t.Fatalf("urlsForPIIAsset() = %v, want env override only", got)
	}
	if sha256OrEmpty(sha256PIIModel, override) != "" {
		t.Fatal("sha256OrEmpty() should skip verification for env overrides")
	}
}

func TestVerifyExistingFileKeepsMatchingAsset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "model.onnx")
	content := []byte("known-good")
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	want := fmt.Sprintf("%x", sha256.Sum256(content))
	if err := verifyExistingFile(path, "test model", want); err != nil {
		t.Fatalf("verifyExistingFile: %v", err)
	}
	if !fileExists(path) {
		t.Fatal("verifyExistingFile removed a matching asset")
	}
}

func TestVerifyExistingFileRemovesMismatchedAsset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "model.onnx")
	if err := os.WriteFile(path, []byte("corrupt"), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	goodHash := fmt.Sprintf("%x", sha256.Sum256([]byte("known-good")))
	if err := verifyExistingFile(path, "test model", goodHash); err != nil {
		t.Fatalf("verifyExistingFile: %v", err)
	}
	if fileExists(path) {
		t.Fatal("verifyExistingFile kept a mismatched asset")
	}
}

func TestDetectorInitializesWithRealModel(t *testing.T) {
	if os.Getenv("YANSU_PII_INTEGRATION") != "1" {
		t.Skip("set YANSU_PII_INTEGRATION=1 to download/load the real GLiNER model")
	}

	configDir := os.Getenv("YANSU_CONFIG_DIR")
	if configDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Fatalf("UserHomeDir: %v", err)
		}
		configDir = filepath.Join(home, ".yansu-agent")
	}

	det, err := NewDetector(configDir, nil)
	if err != nil {
		t.Fatalf("NewDetector: %v", err)
	}
	defer det.Destroy()

	if !det.IsReady() {
		t.Fatal("detector is not ready after initialization")
	}
}
