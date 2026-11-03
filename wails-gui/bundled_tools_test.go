package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

func TestStartupToolInstallersNeverFetchOrInstallPackages(t *testing.T) {
	// An architectural guard complements the executed missing-bundle cases:
	// network clients, package managers, and deferred docs/update fetches must
	// not return to any startup installer, including platform-only branches.
	for _, name := range []string{"bun", "ffmpeg", "opencli", "git"} {
		file := "bundled_" + name + ".go"
		node, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range node.Imports {
			if strings.Contains(imp.Path.Value, "net/") {
				t.Errorf("%s imports network client %s", file, imp.Path.Value)
			}
		}
		ast.Inspect(node, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if id, ok := call.Fun.(*ast.Ident); ok {
				switch id.Name {
				case "downloadBun", "downloadFFmpeg", "downloadGit", "FetchAdapterDocs", "autoUpdateOpenCLI", "StartToolDownload":
					t.Errorf("%s invokes %s without a confirmed plan", file, id.Name)
				}
			}
			for _, arg := range call.Args {
				if s, ok := arg.(*ast.BasicLit); ok && (s.Value == `"install"` || s.Value == `"update"`) {
					t.Errorf("%s invokes a package manager: %s", file, s.Value)
				}
			}
			return true
		})
	}
	app, err := os.ReadFile("app.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(app), ".StartToolDownload(") {
		t.Fatal("startup invokes confirmation action")
	}
}

type startupToolTransport struct{ requests atomic.Int32 }

func (s *startupToolTransport) RoundTrip(*http.Request) (*http.Response, error) {
	s.requests.Add(1)
	return nil, fmt.Errorf("startup tool network request is forbidden")
}

func TestMissingToolBundlesStayNotReadyWithoutNetwork(t *testing.T) {
	transport := &startupToolTransport{}
	previous := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = previous })
	defer func() {
		if transport.requests.Load() != 0 {
			t.Errorf("startup made %d unconfirmed requests", transport.requests.Load())
		}
	}()

	t.Setenv("HOME", t.TempDir())
	t.Setenv(agentHomeEnv, "")
	t.Setenv("PATH", t.TempDir())
	t.Chdir(t.TempDir())
	for name, ensure := range map[string]func() (string, error){
		"bun": ensureBundledBun, "ffmpeg": ensureBundledFFmpeg,
		"opencli": ensureBundledOpenCLI, "git": ensureBundledGit,
	} {
		t.Run(name, func(t *testing.T) {
			path, err := ensure()
			if path != "" || err == nil || !strings.Contains(err.Error(), "not ready") {
				t.Fatalf("path=%s err=%v", path, err)
			}
		})
	}
}

func TestToolBundlesFoundInMacAppAndPortableLayouts(t *testing.T) {
	for _, tool := range []struct{ name, binary string }{
		{bunBundleDirName, filepath.Join("bin", bunExeName)},
		{ffmpegBundleDirName, filepath.Join("bin", ffmpegExeName)},
		{opencliBundleDirName, filepath.Join("bin", opencliExeName)},
		{gitBundleDirName, filepath.Join("bin", gitExeName)},
	} {
		t.Run(tool.name, func(t *testing.T) {
			root := t.TempDir()
			exe := filepath.Join(root, "Isoform.app", "Contents", "MacOS", "Yansu")
			want := filepath.Join(root, "Isoform.app", "Contents", "Resources", tool.name, tool.binary)
			candidates := toolBundleCandidates(exe, "", tool.name, tool.binary)
			if candidates[1] != want {
				t.Fatalf("bundle candidate=%s want=%s", candidates[1], want)
			}
			if err := os.MkdirAll(filepath.Dir(want), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(want, []byte("fixture"), 0o755); err != nil {
				t.Fatal(err)
			}
			if !isExecutableFile(candidates[1]) {
				t.Fatal("app bundle binary not found")
			}
			portable := toolBundleCandidates(filepath.Join(root, "Yansu"), "", tool.name, tool.binary)
			if portable[0] != filepath.Join(root, tool.name, tool.binary) {
				t.Fatalf("portable=%s", portable[0])
			}
		})
	}
}

func TestOpenCLIBundleCopiesPinnedPackageAndDocs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv(agentHomeEnv, "")
	t.Setenv("PATH", t.TempDir())
	wd := t.TempDir()
	t.Chdir(wd)
	root := filepath.Join(wd, opencliBundleDirName)
	for path, contents := range map[string]string{
		filepath.Join("bin", opencliExeName):          "#!/bin/sh\nexit 0\n",
		".version":                                    "1.8.8\n",
		filepath.Join("docs", "browser", "github.md"): "pinned documentation",
		filepath.Join("node_modules", "@jackwener", "opencli", "dist", "src", "main.js"): "pinned package",
	} {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(contents), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	data, err := flavorDataDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(data, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "bin", bunExeName), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	bin, err := ensureBundledOpenCLI()
	if err != nil || !isExecutableFile(bin) {
		t.Fatalf("bin=%s err=%v", bin, err)
	}
	if got := LoadAdapterDoc("github"); got != "pinned documentation" {
		t.Fatal(got)
	}
	if got := LoadAdapterDoc("../../github"); got != "" {
		t.Fatal("path traversal accepted")
	}
	// A new app bundle replaces both package and docs locally.
	if err := os.WriteFile(filepath.Join(root, ".version"), []byte("next-pin"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ensureBundledOpenCLI(); err != nil {
		t.Fatal(err)
	}
	version, _ := os.ReadFile(filepath.Join(data, opencliInstallDir, ".version"))
	if string(version) != "next-pin" {
		t.Fatalf("version=%s", version)
	}
}

func TestBuildEmbedsOpenCLIOnEveryPlatform(t *testing.T) {
	raw, err := os.ReadFile("scripts/build-wails.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"download-opencli.sh", `${RESOURCES_DIR}/opencli-bundle`, `${BIN_DIR}/opencli-bundle`, `GOOS="$EFFECTIVE_TARGET_OS" GOARCH="$EFFECTIVE_TARGET_ARCH"`} {
		if !strings.Contains(string(raw), required) {
			t.Errorf("build missing %s", required)
		}
	}
}

func TestBunAndFFmpegInstallOnlyLocalBundles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv(agentHomeEnv, "")
	t.Chdir(t.TempDir())
	for _, tool := range []struct {
		bundle, exe string
		ensure      func() (string, error)
	}{
		{bunBundleDirName, bunExeName, ensureBundledBun},
		{ffmpegBundleDirName, ffmpegExeName, ensureBundledFFmpeg},
	} {
		t.Run(tool.bundle, func(t *testing.T) {
			source := filepath.Join(tool.bundle, "bin", tool.exe)
			if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(source, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			installed, err := tool.ensure()
			if err != nil || !isExecutableFile(installed) {
				t.Fatalf("path=%s err=%v", installed, err)
			}
			if err := os.Remove(source); err != nil {
				t.Fatal(err)
			}
			if again, err := tool.ensure(); err != nil || again != installed {
				t.Fatalf("cached path=%s err=%v", again, err)
			}
		})
	}
}

func TestBunWrongArchitectureStaysNotReady(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv(agentHomeEnv, "")
	t.Chdir(t.TempDir())
	source := filepath.Join(bunBundleDirName, "bin", bunExeName)
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if path, err := ensureBundledBun(); path != "" || err == nil || !strings.Contains(err.Error(), "not ready") {
		t.Fatalf("path=%s err=%v", path, err)
	}
}

func TestOpenCLILegacyInstallDoesNotBypassPinnedLauncher(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(agentHomeEnv, "")
	pathDir := t.TempDir()
	t.Setenv("PATH", pathDir)
	if err := os.WriteFile(filepath.Join(pathDir, opencliExeName), []byte("user-managed CLI with background update checks"), 0o755); err != nil {
		t.Fatal(err)
	}
	root, err := flavorDataDir()
	if err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(root, opencliInstallDir, "node_modules", ".bin", opencliExeName)
	if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte("old mutable install"), 0o755); err != nil {
		t.Fatal(err)
	}
	if path := localOpenCLIPath(); path != "" {
		t.Fatalf("legacy unpinned install resolved: %s", path)
	}
}

func TestOpenCLIBuildPinsPackageAndSkipsLifecycleDownloads(t *testing.T) {
	script, err := os.ReadFile("scripts/download-opencli.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		`OPENCLI_VERSION="1.8.8"`,
		`OPENCLI_SHA256="68fb69d6dcfbcb1f76da88278b499abe322dcca712f3b79fe01729cb63ae6d7a"`,
		`OPENCLI_SOURCE_SHA256="3b6f18c01f305fdbc84dae0e8233525461fda900583755d0e3a3d4e9fd1fe897"`,
		`--omit=dev --ignore-scripts --no-audit --no-fund`,
		`verify_sha256 "$TMP_DIR/opencli.tgz" "$OPENCLI_SHA256"`,
		`verify_sha256 "$TMP_DIR/source.tar.gz" "$OPENCLI_SOURCE_SHA256"`,
		`cp -R "$TMP_DIR/source/docs/adapters" "$TMP_DIR/bundle/docs"`,
	} {
		if !strings.Contains(string(script), required) {
			t.Errorf("OpenCLI build missing %s", required)
		}
	}
	lock, err := os.ReadFile("scripts/opencli/package-lock.json")
	if err != nil {
		t.Fatal(err)
	}
	var data struct {
		Packages map[string]struct {
			Version   string
			Integrity string
			Resolved  string
		}
	}
	if err := json.Unmarshal(lock, &data); err != nil {
		t.Fatal(err)
	}
	for name, pkg := range data.Packages {
		if name == "" {
			continue
		}
		if pkg.Version == "" || pkg.Integrity == "" || !strings.HasPrefix(pkg.Resolved, "https://registry.npmjs.org/") {
			t.Errorf("unlocked dependency %s: %+v", name, pkg)
		}
	}
	if data.Packages["node_modules/@jackwener/opencli"].Version != "1.8.8" {
		t.Fatal("package pin differs from script")
	}
}
