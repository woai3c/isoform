#!/bin/bash
set -euo pipefail

# Build the bundled CuaDriver.app from vendored Swift sources in
# wails-gui/cua-driver-src/. This replaces the older download-cua-driver.sh
# pipeline that pulled prebuilt tarballs from trycua/cua. The vendored
# sources come from yetone/alma's computer-use helper, which adds:
#   - VisualIndex.swift: Vision-framework OCR with zh-Hans recognition,
#     producing v* refs for clicking on text inside AX-empty apps
#     (CEF/Electron, Chinese desktop apps, custom-drawn UIs).
#   - EventDispatch.swift: bgclick-rev CGEvent synthesis that targets a
#     specific CGWindowID without focus steal — works on Electron too.
#
# Stable bundle identity is preserved so macOS TCC grants for
# com.yansu.cuadriver carry over from the previous build:
#   - CFBundleIdentifier: com.yansu.cuadriver
#   - CFBundleExecutable / binary name: cua-driver
#   - On-disk path: wails-gui/cua-driver-bundle/CuaDriver.app/...

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
WAILS_DIR="$(dirname "$SCRIPT_DIR")"
SRC_DIR="${WAILS_DIR}/cua-driver-src"
SRC_BUILD="${SRC_DIR}/build-cua-driver.sh"
ENT_SRC="${SRC_DIR}/cua-driver.entitlements"
SKILLS_SRC="${SRC_DIR}/Skills"
OUTPUT_DIR="${WAILS_DIR}/cua-driver-bundle"
APP_NAME="CuaDriver.app"
APP_OUT="${OUTPUT_DIR}/${APP_NAME}"
NEW_BUNDLE_ID="com.yansu.cuadriver"

# Version is hardcoded here (rather than read from a tag) since the source
# is vendored — bump it manually whenever cua-driver-src/ is re-synced
# from alma and you want a fresh .version marker.
VERSION="0.2.0"

_OS="${GOOS:-$(uname -s | tr '[:upper:]' '[:lower:]')}"
_ARCH="${GOARCH:-$(uname -m)}"

# Architecture-policy: production cua-driver-bundle/ has always shipped
# darwin-arm64 only. The Go-side LocateBundledCuaDriver also gates on
# arm64. Keep that contract here so cross-compiling for other targets
# silently skips this step instead of producing an unusable bundle.
case "${_OS}-${_ARCH}" in
    darwin-arm64|darwin-aarch64) ARCH_TAG="arm64" ;;
    darwin-amd64|darwin-x86_64)
        echo "[build-cua-driver-bundle] cua-driver bundle is currently darwin-arm64 only; skipping." >&2
        exit 0
        ;;
    *)
        echo "[build-cua-driver-bundle] cua-driver bundle is macOS-only; skipping on ${_OS}-${_ARCH}." >&2
        exit 0
        ;;
esac

if [[ "$(uname -s)" != "Darwin" ]]; then
    echo "[build-cua-driver-bundle] need macOS host (swiftc + codesign); skipping." >&2
    exit 0
fi

for cmd in swiftc codesign ditto /usr/libexec/PlistBuddy; do
    if ! command -v "$cmd" >/dev/null 2>&1 && [[ ! -x "$cmd" ]]; then
        echo "[build-cua-driver-bundle] missing required command: $cmd" >&2
        exit 1
    fi
done

if [[ ! -x "$SRC_BUILD" ]]; then
    echo "[build-cua-driver-bundle] vendored builder missing: $SRC_BUILD" >&2
    exit 1
fi
if [[ ! -f "$ENT_SRC" ]]; then
    echo "[build-cua-driver-bundle] entitlements missing: $ENT_SRC" >&2
    exit 1
fi

echo "[build-cua-driver-bundle] Version: ${VERSION}, Target: darwin-${ARCH_TAG}"

# Wipe and rebuild — same idempotent semantics as the old download script
# so build-wails.sh's "always re-stage" loop keeps working.
rm -rf "$OUTPUT_DIR"
mkdir -p "$OUTPUT_DIR"

# Hand off the actual swiftc invocation to cua-driver-src/build-cua-driver.sh;
# it knows about lipo, target OS, framework list. The builder creates
# $APP_OUT itself (rm -rf'ing it first), so we must NOT pre-populate any
# Resources/ subfolder — anything written before this call would be wiped.
# --skip-codesign because we add entitlements + ad-hoc sign below
# (build-wails.sh's sign_cua_driver_nested replaces this signature with a
# Developer ID one at production-build time).
"$SRC_BUILD" --out "$APP_OUT" --skip-codesign

# Drop the entitlements file inside the bundle so sign_cua_driver_nested
# can find it via the historic path. mkdir -p in case the builder didn't
# create Resources/ (it doesn't by default — only Contents/MacOS/).
mkdir -p "${APP_OUT}/Contents/Resources"
cp "$ENT_SRC" "${APP_OUT}/Contents/Resources/cua-driver.entitlements"

# Skills/ holds the LLM-facing SKILL.md hierarchy that bundled_cua_skill.go
# points at. Source-of-truth lives in cua-driver-src/Skills/; copy it into
# the bundle on every build so edits flow through.
if [[ -d "$SKILLS_SRC" ]]; then
    rm -rf "${APP_OUT}/Contents/Resources/Skills"
    ditto "$SKILLS_SRC" "${APP_OUT}/Contents/Resources/Skills"
fi

# Ad-hoc sign with hardened runtime + entitlements so dev runs through
# start-wails-gui.sh can launch the binary. Production replaces this in
# sign_cua_driver_nested.
echo "[build-cua-driver-bundle] Ad-hoc signing for dev runs..."
codesign --force --sign - --options runtime \
    --entitlements "${APP_OUT}/Contents/Resources/cua-driver.entitlements" \
    --timestamp=none \
    "${APP_OUT}/Contents/MacOS/cua-driver"
codesign --force --sign - --options runtime \
    --entitlements "${APP_OUT}/Contents/Resources/cua-driver.entitlements" \
    --timestamp=none \
    "$APP_OUT"

# Verify the bundle ID stayed where the Go locator expects it.
current_id=$(/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' "${APP_OUT}/Contents/Info.plist")
if [[ "$current_id" != "$NEW_BUNDLE_ID" ]]; then
    echo "[build-cua-driver-bundle] Error: CFBundleIdentifier mismatch: expected ${NEW_BUNDLE_ID}, got ${current_id}" >&2
    exit 1
fi

# Marker files — same convention as the old download script so any tooling
# that grep'd .version / .target / .bundle-id keeps working.
echo "$VERSION" > "${OUTPUT_DIR}/.version"
echo "darwin-${ARCH_TAG}" > "${OUTPUT_DIR}/.target"
echo "$NEW_BUNDLE_ID" > "${OUTPUT_DIR}/.bundle-id"

BIN_SIZE=$(du -sh "${APP_OUT}/Contents/MacOS/cua-driver" | cut -f1)
echo "[build-cua-driver-bundle] Done. CuaDriver.app built from source (binary: ${BIN_SIZE})."
