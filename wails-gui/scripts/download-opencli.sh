#!/usr/bin/env bash
set -euo pipefail

# Build-time only. Ship OpenCLI with locked dependencies and adapter docs;
# no npm/Bun install or update is needed when Isoform runs.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WAILS_DIR="$(dirname "$SCRIPT_DIR")"
OPENCLI_BUNDLE="${WAILS_DIR}/opencli-bundle"
OPENCLI_VERSION="1.8.8"
OPENCLI_SHA256="68fb69d6dcfbcb1f76da88278b499abe322dcca712f3b79fe01729cb63ae6d7a"
OPENCLI_SOURCE_SHA256="3b6f18c01f305fdbc84dae0e8233525461fda900583755d0e3a3d4e9fd1fe897"
TARGET_OS="${GOOS:-$(go env GOOS)}"
TARGET_ARCH="${GOARCH:-$(go env GOARCH)}"
LAUNCHER="opencli"
if [[ "$TARGET_OS" == "windows" ]]; then LAUNCHER="opencli.exe"; fi
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

verify_sha256() {
    local actual
    if command -v shasum >/dev/null 2>&1; then
        actual="$(shasum -a 256 "$1" | awk '{print $1}')"
    else
        actual="$(sha256sum "$1" | awk '{print $1}')"
    fi
    if [[ "$actual" != "$2" ]]; then
        echo "[download-opencli] SHA-256 mismatch: $1" >&2
        exit 1
    fi
}

curl -fSL --retry 5 --retry-all-errors \
    "https://registry.npmjs.org/@jackwener/opencli/-/opencli-${OPENCLI_VERSION}.tgz" -o "$TMP_DIR/opencli.tgz"
verify_sha256 "$TMP_DIR/opencli.tgz" "$OPENCLI_SHA256"
curl -fSL --retry 5 --retry-all-errors \
    "https://codeload.github.com/jackwener/opencli/tar.gz/refs/tags/v${OPENCLI_VERSION}" -o "$TMP_DIR/source.tar.gz"
verify_sha256 "$TMP_DIR/source.tar.gz" "$OPENCLI_SOURCE_SHA256"

mkdir -p "$TMP_DIR/bundle/bin" "$TMP_DIR/source"
cp "$SCRIPT_DIR/opencli/package.json" "$SCRIPT_DIR/opencli/package-lock.json" "$TMP_DIR/bundle/"
# The lock pins the whole dependency graph with integrity hashes. Ignore all
# lifecycle scripts (upstream postinstall otherwise fetches live adapters).
npm ci --prefix "$TMP_DIR/bundle" --omit=dev --ignore-scripts --no-audit --no-fund
# Install the exact SHA-256-checked package, independent of npm's cache.
rm -rf "$TMP_DIR/bundle/node_modules/@jackwener/opencli"
mkdir -p "$TMP_DIR/bundle/node_modules/@jackwener/opencli"
tar -xzf "$TMP_DIR/opencli.tgz" --strip-components=1 -C "$TMP_DIR/bundle/node_modules/@jackwener/opencli"
tar -xzf "$TMP_DIR/source.tar.gz" --strip-components=1 -C "$TMP_DIR/source"
cp -R "$TMP_DIR/source/docs/adapters" "$TMP_DIR/bundle/docs"

(cd "$WAILS_DIR" && CGO_ENABLED=0 GOOS="$TARGET_OS" GOARCH="$TARGET_ARCH" \
    go build -trimpath -o "$TMP_DIR/bundle/bin/$LAUNCHER" ./cmd/opencli-launcher)
printf '%s\n' "$OPENCLI_VERSION" > "$TMP_DIR/bundle/.version"
printf '%s/%s\n' "$TARGET_OS" "$TARGET_ARCH" > "$TMP_DIR/bundle/.target-platform"
# Rebuild the launcher on every build so source changes are never hidden by a
# stale bundle. Publish only after every download and build succeeded.
rm -rf "$OPENCLI_BUNDLE"
mv "$TMP_DIR/bundle" "$OPENCLI_BUNDLE"
echo "[download-opencli] Staged OpenCLI ${OPENCLI_VERSION} for ${TARGET_OS}/${TARGET_ARCH}"
