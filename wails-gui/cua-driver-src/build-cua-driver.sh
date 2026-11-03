#!/usr/bin/env bash
# build-cua-driver.sh — compile vendored alma/computer-use Swift sources
# into a Yansu-branded CuaDriver.app.
#
# Two modes:
#   - default: build into ./build/CuaDriver.app under this script's dir,
#     for standalone smoke testing without touching the production bundle.
#   - --out <path>: build directly into the given .app path (used by
#     build-wails.sh to populate wails-gui/cua-driver-bundle/CuaDriver.app
#     at production-build time).
#
# Output binary is named `cua-driver` and the bundle id is
# `com.yansu.cuadriver`, matching the constants in
# wails-gui/bundled_cua_driver.go. Keeping these stable preserves macOS
# TCC grants across upgrades.

set -euo pipefail

if [[ "$(uname)" != "Darwin" ]]; then
    echo "build-cua-driver.sh: skipping (not macOS)" >&2
    exit 0
fi

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SRC_DIR="$SCRIPT_DIR/src"
PLIST_SRC="$SCRIPT_DIR/Info.plist"

APP_OUT=""
ARCH_FILTER=""
SKIP_CODESIGN=0

while [[ $# -gt 0 ]]; do
    case "$1" in
        --out)
            APP_OUT="$2"
            shift 2
            ;;
        --arch)
            ARCH_FILTER="$2"
            shift 2
            ;;
        --skip-codesign)
            SKIP_CODESIGN=1
            shift
            ;;
        -h|--help)
            sed -n '2,20p' "$0"
            exit 0
            ;;
        *)
            echo "build-cua-driver.sh: unknown argument: $1" >&2
            exit 2
            ;;
    esac
done

if [[ -z "$APP_OUT" ]]; then
    APP_OUT="$SCRIPT_DIR/build/CuaDriver.app"
fi

MACOS_DIR="$APP_OUT/Contents/MacOS"
PLIST_DST="$APP_OUT/Contents/Info.plist"
BIN_NAME="cua-driver"

rm -rf "$APP_OUT"
mkdir -p "$MACOS_DIR"
cp "$PLIST_SRC" "$PLIST_DST"

# Source list matches alma/computer-use/build.sh order. Keep it explicit
# so a stray .swift dropped into src/ doesn't get pulled in by accident.
SOURCES=(
    "$SRC_DIR/main.swift"
    "$SRC_DIR/Protocol.swift"
    "$SRC_DIR/FeatureFlags.swift"
    "$SRC_DIR/ElementStore.swift"
    "$SRC_DIR/AXBridge.swift"
    "$SRC_DIR/ChromiumAccessibilityActivation.swift"
    "$SRC_DIR/BackgroundActivation.swift"
    "$SRC_DIR/EventDispatch.swift"
    "$SRC_DIR/FocusStealth.swift"
    "$SRC_DIR/VictimObservation.swift"
    "$SRC_DIR/AppCommandRegistry.swift"
    "$SRC_DIR/ForbiddenTargets.swift"
    "$SRC_DIR/MenuShortcuts.swift"
    "$SRC_DIR/VisualIndex.swift"
    "$SRC_DIR/WindowList.swift"
    "$SRC_DIR/AppUsage.swift"
    "$SRC_DIR/Capture.swift"
    "$SRC_DIR/Overlay.swift"
    "$SRC_DIR/PIPWindow.swift"
    "$SRC_DIR/MCPServer.swift"
    "$SRC_DIR/Daemon.swift"
)

FRAMEWORKS=(
    -framework AppKit
    -framework ApplicationServices
    -framework AVFoundation
    -framework Carbon
    -framework CoreGraphics
    -framework CoreMedia
    -framework CoreServices
    -framework CoreVideo
    -framework ImageIO
    -framework Network
    -framework ScreenCaptureKit
    -framework Security
    -framework Vision
)

TARGET_OS="14.0"

build_arch() {
    local arch="$1"
    local out="$2"
    echo "[cua-driver-build] compiling $arch..."
    swiftc "${SOURCES[@]}" \
        "${FRAMEWORKS[@]}" \
        -target "${arch}-apple-macosx${TARGET_OS}" \
        -O -o "$out"
}

# Allow narrowing to host arch for fast iteration during development.
ARCHES=("arm64" "x86_64")
if [[ -n "$ARCH_FILTER" ]]; then
    ARCHES=("$ARCH_FILTER")
fi

ARCH_BINS=()
for arch in "${ARCHES[@]}"; do
    out="$MACOS_DIR/${BIN_NAME}-${arch}"
    build_arch "$arch" "$out"
    ARCH_BINS+=("$out")
done

FINAL_BIN="$MACOS_DIR/$BIN_NAME"
if [[ ${#ARCH_BINS[@]} -eq 1 ]]; then
    mv "${ARCH_BINS[0]}" "$FINAL_BIN"
else
    echo "[cua-driver-build] lipo into universal binary..."
    lipo -create "${ARCH_BINS[@]}" -output "$FINAL_BIN"
    rm "${ARCH_BINS[@]}"
fi
chmod +x "$FINAL_BIN"

# Ad-hoc sign by default so the binary runs in dev. Production builds
# pass --skip-codesign and let build-wails.sh sign with Yansu's Developer
# ID + entitlements (matching scripts/sign_cua_driver_nested logic).
if [[ "$SKIP_CODESIGN" -eq 0 ]]; then
    echo "[cua-driver-build] ad-hoc signing $APP_OUT..."
    codesign --force --sign - --identifier com.yansu.cuadriver "$APP_OUT"
fi

echo "[cua-driver-build] done: $APP_OUT"
lipo -info "$FINAL_BIN" 2>/dev/null || file "$FINAL_BIN"
