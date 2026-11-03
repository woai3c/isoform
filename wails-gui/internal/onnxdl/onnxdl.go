// Package onnxdl provides shared ONNX Runtime download and extraction helpers
// used by both the memory embedding system and the PII detection module.
package onnxdl

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	Version    = "1.24.4"
	VersionX86 = "1.23.0" // v1.24+ dropped macOS x86_64 builds
)

// RuntimePaths identifies the ONNX Runtime shared library to use.
type RuntimePaths struct {
	RuntimeDir string
	RuntimeLib string
	Bundled    bool
}

// LibName returns the platform-specific shared library filename.
func LibName() string {
	switch runtime.GOOS {
	case "darwin":
		return "libonnxruntime.dylib"
	case "windows":
		return "onnxruntime.dll"
	default:
		return "libonnxruntime.so"
	}
}

// ResolveRuntimePaths returns the ONNX Runtime library path for this process.
// Packaged desktop builds already ship the runtime for sherpa-onnx; reusing
// that copy avoids loading a second native ONNX Runtime into the same process.
func ResolveRuntimePaths(configDir string) (*RuntimePaths, error) {
	if bundledLib, ok := BundledRuntimeLib(); ok {
		return &RuntimePaths{
			RuntimeDir: filepath.Dir(bundledLib),
			RuntimeLib: bundledLib,
			Bundled:    true,
		}, nil
	}

	runtimeDir := filepath.Join(configDir, "onnxruntime")
	if err := os.MkdirAll(runtimeDir, 0755); err != nil {
		return nil, fmt.Errorf("create runtime dir: %w", err)
	}

	return &RuntimePaths{
		RuntimeDir: runtimeDir,
		RuntimeLib: filepath.Join(runtimeDir, LibName()),
	}, nil
}

// BundledRuntimeLib returns the ONNX Runtime library shipped with packaged
// Yansu builds, if one is available for the current executable.
func BundledRuntimeLib() (string, bool) {
	exe, err := os.Executable()
	if err != nil {
		return "", false
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return bundledRuntimeLibForExecutable(runtime.GOOS, exe)
}

func bundledRuntimeLibForExecutable(goos, executablePath string) (string, bool) {
	switch goos {
	case "darwin":
		return bundledDarwinRuntimeLibForExecutable(executablePath)
	case "windows":
		lib := filepath.Join(filepath.Dir(executablePath), "onnxruntime.dll")
		if FileExists(lib) {
			return lib, true
		}
	}
	return "", false
}

func bundledDarwinRuntimeLibForExecutable(executablePath string) (string, bool) {
	contentsDir := darwinAppContentsDirForExecutable(executablePath)
	if contentsDir == "" {
		return "", false
	}

	frameworksDir := filepath.Join(contentsDir, "Frameworks")
	candidates := []string{
		filepath.Join(frameworksDir, fmt.Sprintf("libonnxruntime.%s.dylib", Version)),
		filepath.Join(frameworksDir, "libonnxruntime.dylib"),
	}
	for _, candidate := range candidates {
		if FileExists(candidate) {
			return candidate, true
		}
	}
	return "", false
}

func darwinAppContentsDirForExecutable(executablePath string) string {
	dir := filepath.Dir(filepath.Clean(executablePath))
	for {
		if filepath.Base(dir) == "Contents" {
			bundleDir := filepath.Dir(dir)
			if strings.HasSuffix(filepath.Base(bundleDir), ".app") {
				return dir
			}
			return ""
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// ArchiveURL returns the download URL for the ONNX Runtime release archive.
func ArchiveURL() (string, error) {
	base := fmt.Sprintf("https://github.com/microsoft/onnxruntime/releases/download/v%s", Version)

	switch runtime.GOOS {
	case "darwin":
		switch runtime.GOARCH {
		case "arm64":
			return base + fmt.Sprintf("/onnxruntime-osx-arm64-%s.tgz", Version), nil
		case "amd64":
			v := VersionX86
			return fmt.Sprintf("https://github.com/microsoft/onnxruntime/releases/download/v%s/onnxruntime-osx-x86_64-%s.tgz", v, v), nil
		}
	case "linux":
		switch runtime.GOARCH {
		case "amd64":
			return base + fmt.Sprintf("/onnxruntime-linux-x64-%s.tgz", Version), nil
		case "arm64":
			return base + fmt.Sprintf("/onnxruntime-linux-aarch64-%s.tgz", Version), nil
		}
	case "windows":
		switch runtime.GOARCH {
		case "amd64":
			return base + fmt.Sprintf("/onnxruntime-win-x64-%s.zip", Version), nil
		case "arm64":
			return base + fmt.Sprintf("/onnxruntime-win-arm64-%s.zip", Version), nil
		}
	}

	return "", fmt.Errorf("unsupported platform: %s/%s", runtime.GOOS, runtime.GOARCH)
}

// ExtractLib extracts the ONNX Runtime shared library from an archive stream.
// Detects the format (zip vs tgz) based on the current platform.
func ExtractLib(r io.Reader, runtimeDir, destLib, logTag string) error {
	if runtime.GOOS == "windows" {
		return extractFromZip(r, runtimeDir, destLib, logTag)
	}
	return extractFromTgz(r, runtimeDir, destLib, logTag)
}

// FileExists checks if a file exists at the given path.
func FileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Size() > 0
}

func extractFromTgz(r io.Reader, runtimeDir, destLib, logTag string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer gz.Close()

	libName := LibName()
	tr := tar.NewReader(gz)

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar read: %w", err)
		}

		baseName := filepath.Base(hdr.Name)
		if baseName == libName || strings.HasPrefix(baseName, libName+".") {
			outPath := filepath.Join(runtimeDir, baseName)
			if FileExists(outPath) {
				continue
			}
			if err := atomicWriteFromReader(tr, outPath, runtimeDir, 0755); err != nil {
				return err
			}
			if baseName == libName {
				log.Printf("[%s] ONNX Runtime extracted: %s", logTag, outPath)
			}
		}
	}

	if !FileExists(destLib) {
		return fmt.Errorf("shared library not found in archive")
	}

	return nil
}

func extractFromZip(r io.Reader, runtimeDir, destLib, logTag string) error {
	tmp, err := os.CreateTemp(runtimeDir, "onnx-*.zip")
	if err != nil {
		return fmt.Errorf("create temp zip: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := io.Copy(tmp, r); err != nil {
		tmp.Close()
		return fmt.Errorf("download zip: %w", err)
	}
	tmp.Close()

	zr, err := zip.OpenReader(tmpPath)
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	defer zr.Close()

	libName := LibName()

	for _, f := range zr.File {
		baseName := filepath.Base(f.Name)
		if baseName != libName {
			continue
		}

		outPath := filepath.Join(runtimeDir, baseName)
		if FileExists(outPath) {
			log.Printf("[%s] ONNX Runtime already present: %s", logTag, outPath)
			break
		}

		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("open zip entry: %w", err)
		}
		if err := atomicWriteFromReader(rc, outPath, runtimeDir, 0); err != nil {
			rc.Close()
			return err
		}
		rc.Close()
		log.Printf("[%s] ONNX Runtime extracted: %s", logTag, outPath)
		break
	}

	if !FileExists(destLib) {
		return fmt.Errorf("shared library not found in archive")
	}

	return nil
}

// atomicWriteFromReader writes data from r to outPath using a unique temp file.
// perm is applied via Chmod if non-zero (used on Unix for .so/.dylib).
func atomicWriteFromReader(r io.Reader, outPath, tmpDir string, perm os.FileMode) error {
	baseName := filepath.Base(outPath)
	tmpF, err := os.CreateTemp(tmpDir, baseName+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmpF.Name()

	if _, err := io.Copy(tmpF, r); err != nil {
		tmpF.Close()
		os.Remove(tmpPath)
		return err
	}
	tmpF.Close()

	if perm != 0 {
		os.Chmod(tmpPath, perm)
	}

	if err := os.Rename(tmpPath, outPath); err != nil {
		os.Remove(tmpPath)
		if FileExists(outPath) {
			return nil // another process placed it
		}
		return err
	}
	return nil
}
