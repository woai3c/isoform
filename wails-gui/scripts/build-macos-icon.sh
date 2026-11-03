#!/bin/bash
set -euo pipefail

# The PNG is the supplied 1024 render of Violet's app-icon-macos.svg.
# Both desktop build paths use this generator so the Retina slot stays 1024px.
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
WAILS_DIR="$(dirname "$SCRIPT_DIR")"
SOURCE="${WAILS_DIR}/assets/appicon.png"
OUTPUT="${WAILS_DIR}/build/darwin/iconfile.icns"
WORK_DIR="$(mktemp -d)"
trap 'rm -rf "$WORK_DIR"' EXIT
ICONSET="${WORK_DIR}/appicon.iconset"
mkdir -p "$ICONSET" "$(dirname "$OUTPUT")"

for size in 16 32 128 256 512; do
    for scale in 1 2; do
        pixels=$((size * scale))
        suffix=""
        if [ "$scale" -eq 2 ]; then
            suffix="@2x"
        fi
        sips -z "$pixels" "$pixels" "$SOURCE" --out "${ICONSET}/icon_${size}x${size}${suffix}.png" >/dev/null
    done
done
iconutil -c icns "$ICONSET" -o "$OUTPUT"
echo "[icon] Generated $OUTPUT"
