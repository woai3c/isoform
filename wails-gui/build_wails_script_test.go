package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNotaryCredentialSelection(t *testing.T) {
	for _, script := range []struct {
		path   string
		source string
	}{
		{path: "scripts/build-wails.sh", source: `. "${WAILS_DIR}/../scripts/release/notary-credentials.sh"`},
		{path: "../scripts/release/ship.sh", source: `. "$root/scripts/release/notary-credentials.sh"`},
	} {
		content, err := os.ReadFile(script.path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(content), "\n"+script.source+"\n") {
			t.Errorf("%s does not source the notary credential helper", script.path)
		}
	}

	content, err := os.ReadFile("../scripts/release/notary-credentials.sh")
	if err != nil {
		t.Fatal(err)
	}
	var functions strings.Builder
	for _, name := range []string{"notary_credentials_available", "notarytool_with_credentials"} {
		start := strings.Index(string(content), name+"() {\n")
		if start < 0 {
			t.Fatalf("missing shell function %s", name)
		}
		body := string(content[start:])
		end := strings.Index(body, "\n}\n")
		if end < 0 {
			t.Fatalf("missing end of shell function %s", name)
		}
		functions.WriteString(body[:end+3])
		functions.WriteString("\n")
	}
	for _, tt := range []struct {
		name          string
		profileResult string
		keyResult     string
		keyExists     bool
		env           string
		want          string
	}{
		{name: "missing", profileResult: "1", want: "No working notary profile, API key file, or complete Apple ID credentials are available.\nmissing\n"},
		{name: "incomplete legacy", profileResult: "1", env: "APPLE_ID=user APPLE_TEAM_ID=team", want: "No working notary profile, API key file, or complete Apple ID credentials are available.\nmissing\n"},
		{name: "legacy", profileResult: "1", env: "APPLE_ID=user APPLE_TEAM_ID=team APPLE_APP_SPECIFIC_PASSWORD=secret", want: "notarytool\nsubmit\nartifact with spaces\n--apple-id\nuser\n--password\nsecret\n--team-id\nteam\n"},
		{name: "profile takes precedence", profileResult: "0", keyExists: true, env: "APPLE_NOTARY_KEYCHAIN_PROFILE=profile APPLE_ID=user APPLE_TEAM_ID=team APPLE_APP_SPECIFIC_PASSWORD=secret", want: "notarytool\nsubmit\nartifact with spaces\n--keychain-profile\nprofile\n"},
		{name: "API key takes precedence over Apple ID", profileResult: "1", keyResult: "0", keyExists: true, env: "APPLE_ID=user APPLE_TEAM_ID=team APPLE_APP_SPECIFIC_PASSWORD=secret", want: "notarytool\nsubmit\nartifact with spaces\n--key\n<key-path>\n--key-id\nkey-id\n--issuer\nissuer\n"},
		{name: "rejected API key does not fall back", profileResult: "1", keyResult: "1", keyExists: true, env: "APPLE_ID=user APPLE_TEAM_ID=team APPLE_APP_SPECIFIC_PASSWORD=secret", want: "Apple rejected or could not verify the notary API key; no notarization was started.\nmissing\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			keyPath := filepath.Join(root, "test-key.p8")
			const keyContents = "private key contents"
			if tt.keyExists {
				if err := os.WriteFile(keyPath, []byte(keyContents), 0600); err != nil {
					t.Fatal(err)
				}
			}
			script := `set -eu
xcrun() {
    if [ "$1" = notarytool ] && [ "$2" = history ]; then
        case " $* " in
            *" --keychain-profile "*) return "$PROFILE_RESULT" ;;
            *" --key "*) return "$KEY_RESULT" ;;
        esac
        return 0
    fi
    printf '%s\n' "$@"
}
` + functions.String() + "\nif notary_credentials_available; then notarytool_with_credentials submit 'artifact with spaces'; else echo missing; fi\n"
			cmd := exec.Command("/bin/bash", "-c", script)
			cmd.Env = []string{
				"PATH=/usr/bin:/bin", "HOME=" + root,
				"PROFILE_RESULT=" + tt.profileResult, "KEY_RESULT=" + tt.keyResult,
				"APPLE_NOTARY_KEY_PATH=" + keyPath, "APPLE_NOTARY_KEY_ID=key-id", "APPLE_NOTARY_ISSUER=issuer",
			}
			for _, entry := range strings.Fields(tt.env) {
				cmd.Env = append(cmd.Env, entry)
			}
			out, err := cmd.CombinedOutput()
			want := strings.ReplaceAll(tt.want, "<key-path>", keyPath)
			if err != nil || string(out) != want {
				t.Fatalf("output %q, error %v; want %q", out, err, want)
			}
			if strings.Contains(string(out), keyContents) {
				t.Fatal("helper printed API key contents")
			}
		})
	}
}

func TestBuildWailsCapturesDirtyBeforeVersionInjection(t *testing.T) {
	contentBytes, err := os.ReadFile("scripts/build-wails.sh")
	if err != nil {
		t.Fatalf("read build-wails.sh: %v", err)
	}
	content := string(contentBytes)

	dirtyProbe := strings.Index(content, "GIT_DIRTY_FLAG=false")
	if dirtyProbe < 0 {
		t.Fatalf("build-wails.sh does not assign GIT_DIRTY_FLAG=false")
	}

	versionInjection := strings.Index(content, "# ── Version injection")
	if versionInjection < 0 {
		t.Fatalf("build-wails.sh is missing version injection marker")
	}

	if dirtyProbe > versionInjection {
		t.Fatalf("GIT_DIRTY_FLAG is assigned after version injection; release builds with --version will report dirty")
	}
}

// TestBuildWailsBundlesSherpaLibsOnLinux guards the fix for the Arch Linux
// "libsherpa-onnx-c-api.so: cannot open shared object file" crash. The
// sherpa-onnx-go-linux cgo flags bake the build host's module-cache rpath into
// the binary, so Linux releases must copy the .so files next to the executable
// and rewrite RUNPATH to $ORIGIN, mirroring the macOS/Windows handling.
func TestBuildWailsBundlesSherpaLibsOnLinux(t *testing.T) {
	contentBytes, err := os.ReadFile("scripts/build-wails.sh")
	if err != nil {
		t.Fatalf("read build-wails.sh: %v", err)
	}
	content := string(contentBytes)

	if !strings.Contains(content, "bundle_sherpa_libs_linux()") {
		t.Fatalf("build-wails.sh is missing the bundle_sherpa_libs_linux function")
	}
	if !strings.Contains(content, "bundle_sherpa_libs_linux \"$BIN_DIR\"") {
		t.Fatalf("build-wails.sh never invokes bundle_sherpa_libs_linux for the Linux target")
	}
	if !strings.Contains(content, "libsherpa-onnx-c-api.so") {
		t.Fatalf("build-wails.sh does not reference libsherpa-onnx-c-api.so")
	}
	if !strings.Contains(content, "--set-rpath '$ORIGIN'") {
		t.Fatalf("build-wails.sh does not rewrite the Linux ELF RUNPATH to $ORIGIN")
	}
}

func TestBuildWailsSpotlightFailurePolicyAndDependencyOrder(t *testing.T) {
	content, err := os.ReadFile("scripts/build-wails.sh")
	if err != nil {
		t.Fatal(err)
	}
	// Execute only these functions with shell stand-ins for build tools. The
	// production script's downloads, app build, signing and launch never run.
	var functions strings.Builder
	for _, name := range []string{"spotlight_build_failed", "build_quick_chat_spotlight", "remove_bundled_quick_chat_spotlight", "install_quick_chat_spotlight"} {
		start := strings.Index(string(content), name+"() {\n")
		if start < 0 {
			t.Fatalf("missing shell function %s", name)
		}
		body := string(content[start:])
		end := strings.Index(body, "\n}\n")
		if end < 0 {
			t.Fatalf("missing end of shell function %s", name)
		}
		functions.WriteString(body[:end+3])
		functions.WriteString("\n")
	}
	for _, tt := range []struct {
		name       string
		os         string
		failAt     string
		allow      string
		skip       bool
		wantStages string
		wantError  string
	}{
		{name: "mac success", os: "darwin", wantStages: "main-install,spotlight-install,spotlight-build,go-build,complete"},
		{name: "linux success", os: "linux", wantStages: "main-install,wails-build,complete"},
		{name: "windows success", os: "windows", wantStages: "main-install,wails-build,complete"},
		{name: "main dependency failure", os: "darwin", failAt: "main-install", wantStages: "main-install", wantError: "main frontend dependency install failed"},
		{name: "frontend install failure", os: "darwin", failAt: "spotlight-install", wantStages: "main-install,spotlight-install", wantError: "spotlight frontend build failed"},
		{name: "frontend build failure", os: "darwin", failAt: "spotlight-build", wantStages: "main-install,spotlight-install,spotlight-build", wantError: "spotlight frontend build failed"},
		{name: "native helper failure", os: "darwin", failAt: "go-build", wantStages: "main-install,spotlight-install,spotlight-build,go-build", wantError: "spotlight Go helper build failed"},
		{name: "wails helper failure", os: "linux", failAt: "wails-build", wantStages: "main-install,wails-build", wantError: "spotlight Wails helper build failed"},
		{name: "source missing", os: "darwin", failAt: "source", wantError: "quick-chat-spotlight source directory missing"},
		{name: "pnpm missing", os: "darwin", failAt: "pnpm", wantError: "pnpm not found"},
		{name: "wails missing", os: "linux", failAt: "wails", wantStages: "main-install", wantError: "wails CLI not found"},
		{name: "zero does not allow failure", os: "darwin", failAt: "spotlight-build", allow: "0", wantStages: "main-install,spotlight-install,spotlight-build", wantError: "spotlight frontend build failed"},
		{name: "true does not allow failure", os: "darwin", failAt: "spotlight-build", allow: "true", wantStages: "main-install,spotlight-install,spotlight-build", wantError: "spotlight frontend build failed"},
		{name: "allow frontend failure without stale helper", os: "darwin", failAt: "spotlight-build", allow: "1", wantStages: "main-install,spotlight-install,spotlight-build,complete"},
		{name: "allow native failure without stale helper", os: "darwin", failAt: "go-build", allow: "1", wantStages: "main-install,spotlight-install,spotlight-build,go-build,complete"},
		{name: "allow wails failure without stale helper", os: "windows", failAt: "wails-build", allow: "1", wantStages: "main-install,wails-build,complete"},
		{name: "explicit skip", os: "darwin", skip: true, wantStages: "complete"},
		{name: "missing build output fails", os: "darwin", failAt: "output", wantStages: "main-install,spotlight-install,spotlight-build,go-build", wantError: "expected spotlight binary"},
		{name: "allow missing build output", os: "darwin", failAt: "output", allow: "1", wantStages: "main-install,spotlight-install,spotlight-build,go-build,complete"},
		{name: "output disappears before install", os: "darwin", failAt: "install-output", wantStages: "main-install,spotlight-install,spotlight-build,go-build", wantError: "expected spotlight binary"},
		{name: "allow missing install output removes mac bundle", os: "darwin", failAt: "install-output", allow: "1", wantStages: "main-install,spotlight-install,spotlight-build,go-build,complete"},
		{name: "allow missing install output removes windows helper", os: "windows", failAt: "install-output", allow: "1", wantStages: "main-install,wails-build,complete"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root, err := os.MkdirTemp("/tmp", "sp-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(root) })
			wailsDir := filepath.Join(root, "work tree")
			spotlightDir := filepath.Join(wailsDir, "quick-chat-spotlight")
			if err := os.MkdirAll(filepath.Join(wailsDir, "frontend"), 0755); err != nil {
				t.Fatal(err)
			}
			if tt.failAt != "source" {
				if err := os.MkdirAll(filepath.Join(spotlightDir, "frontend"), 0755); err != nil {
					t.Fatal(err)
				}
			}
			helperName := "quick-chat-spotlight"
			if tt.os == "windows" {
				helperName += ".exe"
			}
			source := filepath.Join(spotlightDir, "build", "bin", helperName)
			destination := filepath.Join(wailsDir, "build", "bin", helperName)
			appPath := filepath.Join(wailsDir, "build", "bin", "Isoform.app")
			if tt.os == "darwin" {
				source = filepath.Join(spotlightDir, "build", "bin", helperName+".app", "Contents", "MacOS", helperName)
				destination = filepath.Join(appPath, "Contents", "Resources", helperName+".app", "Contents", "MacOS", helperName)
			}
			if tt.failAt != "source" && tt.failAt != "output" {
				if err := os.MkdirAll(filepath.Dir(source), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(source, []byte("stale"), 0755); err != nil {
					t.Fatal(err)
				}
			}
			if tt.failAt == "install-output" && tt.os == "darwin" {
				legacySource := filepath.Join(spotlightDir, "build", "bin", helperName)
				if err := os.WriteFile(legacySource, []byte("stale legacy helper"), 0755); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.MkdirAll(filepath.Join(appPath, "Contents", "Resources"), 0755); err != nil {
				t.Fatal(err)
			}
			if tt.allow == "1" || tt.skip {
				if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(destination, []byte("stale bundled helper"), 0755); err != nil {
					t.Fatal(err)
				}
			}
			// PATH excludes developer toolchains, while each build command is a
			// shell function that only records its stage and writes a text fixture.
			script := `set -euo pipefail
record_stage() {
    printf '%s\n' "$1" >> "$STAGES"
    [ "$FAIL_AT" != "$1" ]
}
pnpm() {
    if [ "$PWD" = "$WAILS_DIR/frontend" ]; then
        record_stage main-install
    elif [ "$*" = "install" ]; then
        record_stage spotlight-install
    else
        record_stage spotlight-build
    fi
}
write_fixture() {
    if [ "$FAIL_AT" != "output" ]; then
        mkdir -p "$(dirname "$HELPER_SOURCE")"
        printf fresh > "$HELPER_SOURCE"
    fi
}
go() { record_stage go-build && write_fixture; }
wails() { record_stage wails-build && write_fixture; }
write_spotlight_info_plist() { :; }
mark_spotlight_helper_lsui_element() { :; }
ditto() { cp -R "$1" "$2"; }
if [ "$FAIL_AT" = pnpm ]; then unset -f pnpm; fi
if [ "$FAIL_AT" = wails ]; then unset -f wails; fi
` + functions.String() + `
build_quick_chat_spotlight
if [ "$FAIL_AT" = install-output ]; then rm -f "$HELPER_SOURCE"; fi
install_quick_chat_spotlight
printf 'spotlight-built=%s\n' "$SPOTLIGHT_BUILT"
record_stage complete
`
			skip := "false"
			if tt.skip {
				skip = "true"
			}
			stagesPath := filepath.Join(root, "stages")
			cmd := exec.Command("bash", "-c", script)
			cmd.Env = []string{
				"PATH=/usr/bin:/bin", "HOME=" + root, "WAILS_DIR=" + wailsDir,
				"SPOTLIGHT_DIR=" + spotlightDir, "APP_PATH=" + appPath,
				"EFFECTIVE_TARGET_OS=" + tt.os, "EFFECTIVE_TARGET_ARCH=arm64", "PLATFORM=",
				"SKIP_SPOTLIGHT=" + skip, "SPOTLIGHT_BUILT=false", "YANSU_ALLOW_NO_SPOTLIGHT=" + tt.allow,
				"FAIL_AT=" + tt.failAt, "STAGES=" + stagesPath, "HELPER_SOURCE=" + source,
			}
			output, runErr := cmd.CombinedOutput()
			if tt.wantError != "" {
				if runErr == nil || !strings.Contains(string(output), "ERROR: "+tt.wantError) || !strings.Contains(string(output), "YANSU_ALLOW_NO_SPOTLIGHT=1") {
					t.Fatalf("expected actionable failure %q, got %v: %s", tt.wantError, runErr, output)
				}
			} else if runErr != nil {
				t.Fatalf("unexpected build policy failure: %v\n%s", runErr, output)
			}
			if tt.allow == "1" && !strings.Contains(string(output), "WARNING:") {
				t.Fatalf("allowed helper failure must emit a warning: %s", output)
			}
			if (tt.allow == "1" || tt.skip) && !strings.Contains(string(output), "spotlight-built=false") {
				t.Fatalf("failed/skipped helper must clear build readiness: %s", output)
			}
			stages, err := os.ReadFile(stagesPath)
			if err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if got := strings.ReplaceAll(strings.TrimSpace(string(stages)), "\n", ","); got != tt.wantStages {
				t.Fatalf("build stages = %q, want %q\n%s", got, tt.wantStages, output)
			}
			installed, err := os.ReadFile(destination)
			if tt.failAt == "" && !tt.skip {
				if err != nil || string(installed) != "fresh" {
					t.Fatalf("fresh helper was not installed: %q, %v", installed, err)
				}
			} else if !os.IsNotExist(err) {
				t.Fatalf("failed/skipped build installed a helper: %q, %v", installed, err)
			}
		})
	}
}

// TestReleaseBuildIdentityAndChannel guards the Isoform release: both
// build-wails.sh builds are compiled as Isoform, only the release carries the
// release channel, and artifacts are named Isoform.
func TestReleaseBuildIdentityAndChannel(t *testing.T) {
	contentBytes, err := os.ReadFile("scripts/build-wails.sh")
	if err != nil {
		t.Fatal(err)
	}
	content := string(contentBytes)
	for _, want := range []string{
		`APP_NAME="Isoform.app"`,
		`LDFLAGS_PARTS="${LDFLAGS_PARTS:+$LDFLAGS_PARTS }-X main.buildFlavor=isoform"`,
		`LDFLAGS_PARTS="${LDFLAGS_PARTS:+$LDFLAGS_PARTS }-X main.buildChannel=release"`,
		`ARTIFACT_PREFIX="Isoform"`,
		`YANSU_BUILD_WAILS=1 wails build`,
		`<key>CFBundleName</key><string>Isoform Spotlight</string>`,
	} {
		if !strings.Contains(content, want) {
			t.Errorf("build-wails.sh is missing %q", want)
		}
	}
	isoformBranch := strings.Index(content, `echo "[build-wails] FLAVOR isoform: development build`)
	channel := strings.Index(content, `LDFLAGS_PARTS:+$LDFLAGS_PARTS }-X main.buildChannel=release`)
	if isoformBranch < 0 || channel < isoformBranch {
		t.Fatal("the release channel must be set only in the release branch, after the development branch")
	}
}

// TestDevSigningHookSkipsBuildWails: wails runs the wails.json post-build hook
// after `wails build` too, and the bundle is com.wails.Isoform by then. The
// hook must leave build-wails.sh bundles and the signing record alone.
func TestDevSigningHookSkipsBuildWails(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	bundle := filepath.Join(root, "Isoform.app")
	bin := filepath.Join(bundle, "Contents", "MacOS", "Yansu")
	if err := os.MkdirAll(filepath.Dir(bin), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".isoform-agent", "dev-signing"), 0755); err != nil {
		t.Fatal(err)
	}
	plist := `<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>com.wails.Isoform</string></dict></plist>`
	if err := os.WriteFile(filepath.Join(bundle, "Contents", "Info.plist"), []byte(plist), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("binary"), 0755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "scripts/sign-dev-build.sh", bin)
	cmd.Env = append(os.Environ(), "HOME="+home, "YANSU_BUILD_WAILS=1", "YANSU_DEV_SIGNING=")
	if output, err := cmd.CombinedOutput(); err != nil || len(output) != 0 {
		t.Fatalf("hook ran for a build-wails.sh bundle: %v %s", err, output)
	}
	if entries, _ := os.ReadDir(filepath.Join(home, ".isoform-agent", "dev-signing")); len(entries) != 0 {
		t.Fatalf("hook wrote signing state: %v", entries)
	}
	if entries, _ := os.ReadDir(filepath.Join(bundle, "Contents")); len(entries) != 2 {
		t.Fatalf("hook changed the bundle: %v", entries)
	}
}

func TestMacBundleVersionStampUsesNumericAppleKeys(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("PlistBuddy is available on macOS")
	}
	source, err := os.ReadFile("scripts/build-wails.sh")
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(source), "stamp_macos_bundle_versions() {\n")
	if start < 0 {
		t.Fatal("missing version stamp")
	}
	body := string(source[start:])
	end := strings.Index(body, "\n}\n")
	if end < 0 {
		t.Fatal("missing version stamp end")
	}
	plist := filepath.Join(t.TempDir(), "Info.plist")
	data := `<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>CFBundleShortVersionString</key><string>0.1.0</string><key>CFBundleVersion</key><string>1</string></dict></plist>`
	if err := os.WriteFile(plist, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	script := "set -eu\n" + body[:end+3] + "\nVERSION=1.0.0-beta.2 BUNDLE_BUILD_NUMBER=12345 stamp_macos_bundle_versions \"$1\"\n"
	cmd := exec.Command("/bin/bash", "-c", script, "stamp", plist)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("stamp: %s: %v", output, err)
	}
	for key, want := range map[string]string{"CFBundleShortVersionString": "1.0.0", "CFBundleVersion": "12345", "IsoformReleaseVersion": "1.0.0-beta.2"} {
		output, err := exec.Command("/usr/libexec/PlistBuddy", "-c", "Print :"+key, plist).Output()
		if err != nil || strings.TrimSpace(string(output)) != want {
			t.Errorf("%s = %q (%v), want %q", key, output, err, want)
		}
	}
}
