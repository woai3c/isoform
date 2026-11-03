//go:build rc

package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These checks describe the intended 1.0 release, even when the current
// checkout does not yet meet it. Run through scripts/release/rc-check.sh.
func TestRCReleaseIdentity(t *testing.T) {
	setReleaseTestBuild(t, appFlavorIsoform, buildChannelRelease)
	t.Setenv(agentHomeEnv, "")
	oldDir := configDirName
	t.Cleanup(func() { configDirName = oldDir })
	configDirName = configDirForFlavor(appFlavor())
	root, err := flavorDataDir()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(root) != ".isoform-agent" || !isReleaseChannel() {
		t.Fatalf("release identity: root=%q channel=%q", root, buildChannel)
	}
	if info := environmentInfoFromConfig(yansuConfig{}); !info.IsRelease || info.IsDev || info.IsStaging {
		t.Errorf("release channel environment classified as development: %+v", info)
	}
	oldRepo, oldFeed := desktopReleasesRepo, R2ManifestURL
	t.Cleanup(func() { desktopReleasesRepo, R2ManifestURL = oldRepo, oldFeed })
	desktopReleasesRepo, R2ManifestURL = defaultDesktopReleasesRepo, ""
	if got := updateFeedURL(); got != "https://github.com/yubozhao/isoform-releases/releases/latest/download/version.json" {
		t.Fatalf("release feed = %q", got)
	}
}

func TestRCVersion(t *testing.T) {
	data, err := os.ReadFile("wails.json")
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Info struct {
			ProductVersion string `json:"productVersion"`
		} `json:"info"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	if config.Info.ProductVersion != "1.0.0" || Version != config.Info.ProductVersion {
		t.Fatalf("1.0.0 release requires productVersion and stamped version to agree: productVersion=%q, stamp=%q", config.Info.ProductVersion, Version)
	}
}

// The support archive is the app's user-facing log reader after a restart.
// Do not require raw crash text: it may contain private information.
func TestRCCrashEvidenceAvailableToSupportArchive(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "logs"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "logs", "crash-20261026T000000.000000000Z-1.txt"), []byte("panic: fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := writeSupportArchive(&output, root, time.Now()); err != nil {
		t.Fatal(err)
	}
	archive, err := zip.NewReader(bytes.NewReader(output.Bytes()), int64(output.Len()))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range archive.File {
		if strings.Contains(strings.ToLower(entry.Name), "crash") {
			return
		}
	}
	t.Fatal("support archive does not read crash evidence from the crash collector's logs directory")
}

func TestRCNonReleaseNeverChecksForUpdates(t *testing.T) {
	for _, flavor := range []string{appFlavorIsoform, appFlavorYansu} {
		t.Run(flavor, func(t *testing.T) {
			setReleaseTestBuild(t, flavor, "")
			requests := 0
			R2ManifestURL = releaseFeed(t, &requests).URL
			app := &App{updater: &updater{}}
			app.fetchLatestRelease()
			if requests != 0 {
				t.Fatalf("non-release %s contacted update feed %d time(s)", flavor, requests)
			}
		})
	}
}

func TestRCBuildScriptStampsRelease(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("scripts", "build-wails.sh"))
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	for _, fragment := range []string{
		`VERSION=$(sed -n`,
		`-X main.Version=$VERSION`,
		`-X main.buildFlavor=isoform`,
		`if [ "$FLAVOR" = "isoform" ]; then`,
		`-X main.buildChannel=release`,
	} {
		if !strings.Contains(script, fragment) {
			t.Errorf("build script no longer contains release stamping step %q", fragment)
		}
	}
}

// The fatal-output subprocess test exercises collection; this checks that
// ordinary startup still reaches that collector in the application entrypoint.
func TestRCCrashCollectorWiredIntoStartup(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for caller, callee := range map[string]string{"main": "initStartupLogs", "initStartupLogs": "initCrashLog"} {
		found := false
		for _, declaration := range file.Decls {
			fn, ok := declaration.(*ast.FuncDecl)
			if !ok || fn.Name.Name != caller || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if ok {
					if name, ok := call.Fun.(*ast.Ident); ok && name.Name == callee {
						found = true
					}
				}
				return true
			})
		}
		if !found {
			t.Errorf("%s no longer calls %s", caller, callee)
		}
	}
}
