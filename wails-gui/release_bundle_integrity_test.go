package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestReleaseBundleArchitectureRejectsWrongMachO(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Mach-O build verification requires macOS")
	}
	root := t.TempDir()
	source := filepath.Join(root, "main.c")
	if err := os.WriteFile(source, []byte("int main(void) { return 0; }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	arm, x64 := filepath.Join(root, "arm"), filepath.Join(root, "x64")
	for arch, output := range map[string]string{"arm64": arm, "x86_64": x64} {
		if out, err := exec.Command("clang", "-arch", arch, "-o", output, source).CombinedOutput(); err != nil {
			t.Fatalf("clang %s: %v: %s", arch, err, out)
		}
	}
	script := `source scripts/bundle-architecture.sh; bundle_macho_matches "$1" arm64 && ! bundle_macho_matches "$1" amd64 && bundle_macho_matches "$2" amd64 && ! bundle_macho_matches "$2" arm64`
	if out, err := exec.Command("bash", "-c", script, "test", arm, x64).CombinedOutput(); err != nil {
		t.Fatalf("architecture checks: %v: %s", err, out)
	}
}

func TestIntelONNXRejectsNewerRuntimeCache(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Mach-O build verification requires macOS")
	}
	root := t.TempDir()
	scripts := filepath.Join(root, "scripts")
	libDir := filepath.Join(root, "onnxruntime-bundle", "lib")
	fakeBin := filepath.Join(root, "bin")
	for _, dir := range []string{scripts, libDir, fakeBin} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"bundle-architecture.sh", "build-onnxruntime-macos-x64.sh"} {
		content, err := os.ReadFile(filepath.Join("scripts", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(scripts, name), content, 0755); err != nil {
			t.Fatal(err)
		}
	}
	// Build an Intel dylib with the expected export but a newer minimum OS.
	// This exercises the cache compatibility check without depending on a
	// separately downloaded sherpa-onnx module or its bundled runtime version.
	source := filepath.Join(root, "runtime.c")
	if err := os.WriteFile(source, []byte("void OrtGetApiBase(void) {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	library := filepath.Join(libDir, "libonnxruntime.1.24.4.dylib")
	if out, err := exec.Command("clang", "-arch", "x86_64", "-dynamiclib", "-mmacosx-version-min=15.0", "-o", library, source).CombinedOutput(); err != nil {
		t.Fatalf("build incompatible runtime: %v: %s", err, out)
	}
	data, err := os.ReadFile(library)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if err := os.WriteFile(filepath.Join(root, "onnxruntime-bundle", ".sha256"), []byte(fmt.Sprintf("%x\n", sum)), 0600); err != nil {
		t.Fatal(err)
	}
	fakeCurl := "#!/bin/bash\nwhile [ \"$#\" -gt 0 ]; do\n  if [ \"$1\" = -o ]; then printf corrupt > \"$2\"; exit 0; fi\n  shift\ndone\nexit 1\n"
	if err := os.WriteFile(filepath.Join(fakeBin, "curl"), []byte(fakeCurl), 0755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", filepath.Join(scripts, "build-onnxruntime-macos-x64.sh"))
	cmd.Env = []string{"PATH=" + fakeBin + ":" + os.Getenv("PATH"), "HOME=" + root}
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "Source SHA-256 mismatch") {
		t.Fatalf("expected incompatible cached runtime to be rejected: %v: %s", err, out)
	}
}

func TestReleaseDownloadsRejectCorruptArchives(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS bundle verification")
	}
	root := t.TempDir()
	scripts, fakeBin := filepath.Join(root, "scripts"), filepath.Join(root, "fake-bin")
	if err := os.MkdirAll(scripts, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(fakeBin, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"bundle-architecture.sh", "download-bun.sh", "download-ffmpeg.sh"} {
		data, err := os.ReadFile(filepath.Join("scripts", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(scripts, name), data, 0755); err != nil {
			t.Fatal(err)
		}
	}
	fakeCurl := `#!/bin/bash
while [ "$#" -gt 0 ]; do
    if [ "$1" = "-o" ]; then printf 'corrupt archive' > "$2"; exit 0; fi
    shift
done
exit 1
`
	if err := os.WriteFile(filepath.Join(fakeBin, "curl"), []byte(fakeCurl), 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"download-bun.sh", "download-ffmpeg.sh"} {
		t.Run(name, func(t *testing.T) {
			cmd := exec.Command("bash", filepath.Join(scripts, name), "darwin", "amd64")
			cmd.Env = []string{"PATH=" + fakeBin + ":" + os.Getenv("PATH"), "HOME=" + root, "CI=1", "GOOS=darwin", "GOARCH=amd64"}
			out, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(out), "SHA-256 mismatch") {
				t.Fatalf("expected checksum failure: %v: %s", err, out)
			}
		})
	}
}
