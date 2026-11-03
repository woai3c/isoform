#!/bin/bash
set -euo pipefail

# Build yansu-agent CLI for bundling into the Wails app resources.
# Output goes to wails-gui/yansu-cli-bundle/bin/.
#
# Usage:
#   wails-gui/scripts/build-yansu-cli.sh

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
WAILS_DIR="$(dirname "$SCRIPT_DIR")"
REPO_ROOT="$(dirname "$WAILS_DIR")"
OUTPUT_DIR="${WAILS_DIR}/yansu-cli-bundle/bin"

GOOS_VALUE="${GOOS:-$(go env GOOS)}"
# Native Keychain access keeps the credential bound to this executable's
# identity. A CGO-free macOS CLI cannot use the desktop API credential.
if [ "$GOOS_VALUE" = "darwin" ]; then
    # The trusted CLI must query the kernel sandbox before dispatching commands.
    # Go otherwise disables cgo automatically when cross-building CPU arches.
    export CGO_ENABLED=1
fi
BIN_NAME="yansu"
if [ "$GOOS_VALUE" = "windows" ]; then
    BIN_NAME="yansu.exe"
fi
OUTPUT_BIN="${OUTPUT_DIR}/${BIN_NAME}"

GOARCH_VALUE="${GOARCH:-$(go env GOARCH)}"
echo "[build-yansu-cli] Building yansu CLI (${GOOS_VALUE}/${GOARCH_VALUE})..."
mkdir -p "$OUTPUT_DIR"

cd "$REPO_ROOT/yansu-agent"
go build -o "$OUTPUT_BIN" ./cmd/yansu

if [ "$GOOS_VALUE" != "windows" ]; then
    chmod +x "$OUTPUT_BIN"
fi
if [ "$GOOS_VALUE" = "darwin" ]; then
    # The sandbox trusts this binary for local API credentials. Hardened
    # runtime prevents agent-controlled DYLD injection into that exception.
    /usr/bin/codesign --force --options runtime --sign - "$OUTPUT_BIN"
    /usr/bin/codesign --verify --strict "$OUTPUT_BIN"
fi

BUNDLE_SIZE=$(du -sh "${WAILS_DIR}/yansu-cli-bundle" | cut -f1)
echo "[build-yansu-cli] Done. Bundle size: ${BUNDLE_SIZE}"
