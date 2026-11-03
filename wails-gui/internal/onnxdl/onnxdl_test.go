package onnxdl

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBundledRuntimeLibForDarwinAppPrefersVersionedFramework(t *testing.T) {
	appDir := t.TempDir()
	exe := filepath.Join(appDir, "Yansu.app", "Contents", "MacOS", "Yansu")
	frameworksDir := filepath.Join(appDir, "Yansu.app", "Contents", "Frameworks")
	versioned := filepath.Join(frameworksDir, "libonnxruntime."+Version+".dylib")
	unversioned := filepath.Join(frameworksDir, "libonnxruntime.dylib")
	writeTestFile(t, exe)
	writeTestFile(t, versioned)
	writeTestFile(t, unversioned)

	got, ok := bundledRuntimeLibForExecutable("darwin", exe)
	if !ok {
		t.Fatal("bundledRuntimeLibForExecutable returned ok=false")
	}
	if got != versioned {
		t.Fatalf("runtime lib = %q, want %q", got, versioned)
	}
}

func TestBundledRuntimeLibForDarwinAppFallsBackToUnversionedFramework(t *testing.T) {
	appDir := t.TempDir()
	exe := filepath.Join(appDir, "Yansu.app", "Contents", "MacOS", "Yansu")
	unversioned := filepath.Join(appDir, "Yansu.app", "Contents", "Frameworks", "libonnxruntime.dylib")
	writeTestFile(t, exe)
	writeTestFile(t, unversioned)

	got, ok := bundledRuntimeLibForExecutable("darwin", exe)
	if !ok {
		t.Fatal("bundledRuntimeLibForExecutable returned ok=false")
	}
	if got != unversioned {
		t.Fatalf("runtime lib = %q, want %q", got, unversioned)
	}
}

func TestBundledRuntimeLibForDarwinResourceHelperUsesAppFramework(t *testing.T) {
	appDir := t.TempDir()
	helper := filepath.Join(appDir, "Yansu.app", "Contents", "Resources", "yansu-pii-helper")
	versioned := filepath.Join(appDir, "Yansu.app", "Contents", "Frameworks", "libonnxruntime."+Version+".dylib")
	writeTestFile(t, helper)
	writeTestFile(t, versioned)

	got, ok := bundledRuntimeLibForExecutable("darwin", helper)
	if !ok {
		t.Fatal("bundledRuntimeLibForExecutable returned ok=false")
	}
	if got != versioned {
		t.Fatalf("runtime lib = %q, want %q", got, versioned)
	}
}

func TestBundledRuntimeLibForDarwinRejectsNonAppExecutable(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "Yansu")
	writeTestFile(t, exe)

	if got, ok := bundledRuntimeLibForExecutable("darwin", exe); ok {
		t.Fatalf("runtime lib = %q, want no bundled runtime", got)
	}
}

func TestBundledRuntimeLibForWindowsUsesExecutableDirectory(t *testing.T) {
	binDir := t.TempDir()
	exe := filepath.Join(binDir, "Yansu.exe")
	runtimeLib := filepath.Join(binDir, "onnxruntime.dll")
	writeTestFile(t, exe)
	writeTestFile(t, runtimeLib)

	got, ok := bundledRuntimeLibForExecutable("windows", exe)
	if !ok {
		t.Fatal("bundledRuntimeLibForExecutable returned ok=false")
	}
	if got != runtimeLib {
		t.Fatalf("runtime lib = %q, want %q", got, runtimeLib)
	}
}

func writeTestFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}
