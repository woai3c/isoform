#!/usr/bin/env bash
set -euo pipefail

# Download the pinned dumbpipe release for both supported macOS architectures.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WAILS_DIR="$(dirname "$SCRIPT_DIR")"
DUMBPIPE_BUNDLE="${WAILS_DIR}/dumbpipe-bundle"
source "${SCRIPT_DIR}/bundle-architecture.sh"

DUMBPIPE_VERSION="0.40.1"
DUMBPIPE_RELEASE_URL="https://github.com/n0-computer/dumbpipe/releases/download/v${DUMBPIPE_VERSION}"
DUMBPIPE_SHA256_DARWIN_ARM64="e6bd3c9a98ba22e8233ae1f8ba5e2dceb4cbf132dc4d4332589295b702902a27"
DUMBPIPE_SHA256_DARWIN_AMD64="eaa35e228528cbf5fc2a8a07646570f49af6ab1acd35c8f539f150a0cb5d4b0e"

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

sha256_of() {
    if command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$1" | awk '{print $1}'
    else
        sha256sum "$1" | awk '{print $1}'
    fi
}

download_arch() {
    local arch="$1" asset_arch="$2" pinned="$3" binary_sha="$4"
    local asset="dumbpipe-v${DUMBPIPE_VERSION}-darwin-${asset_arch}.tar.gz"
    local archive="${TMP_DIR}/${asset}" unpack="${TMP_DIR}/${arch}"
    local dest="${DUMBPIPE_BUNDLE}/dumbpipe-darwin-${arch}"
    if [[ -f "$dest" ]] && bundle_macho_matches "$dest" "$arch" && [[ "$(sha256_of "$dest")" == "$binary_sha" ]]; then
        echo "[download-dumbpipe] Reusing verified darwin-${arch}"
        return
    fi
    echo "[download-dumbpipe] Downloading ${asset}..."
    curl -fSL --retry 5 --retry-all-errors -o "$archive" "${DUMBPIPE_RELEASE_URL}/${asset}"
    local actual
    actual="$(sha256_of "$archive")"
    if [[ "$actual" != "$pinned" ]]; then
        echo "[download-dumbpipe] Error: sha256 mismatch for ${asset}: got ${actual}, want ${pinned}" >&2
        exit 1
    fi
    mkdir -p "$unpack" "$DUMBPIPE_BUNDLE"
    tar -xzf "$archive" -C "$unpack"
    bundle_macho_matches "${unpack}/dumbpipe" "$arch" || { echo "[download-dumbpipe] Wrong architecture for ${asset}" >&2; exit 1; }
    [[ "$(sha256_of "${unpack}/dumbpipe")" == "$binary_sha" ]] || { echo "[download-dumbpipe] Binary SHA-256 mismatch for ${asset}" >&2; exit 1; }
    cp "${unpack}/dumbpipe" "${DUMBPIPE_BUNDLE}/dumbpipe-darwin-${arch}"
    chmod +x "${DUMBPIPE_BUNDLE}/dumbpipe-darwin-${arch}"
    echo "[download-dumbpipe] Installed ${DUMBPIPE_BUNDLE}/dumbpipe-darwin-${arch}"
}

download_arch arm64 aarch64 "$DUMBPIPE_SHA256_DARWIN_ARM64" ef81d48f03cf828b9f98b085cd6dc46601594c40b1fc1cc5005b561f3926f5ac
download_arch amd64 x86_64 "$DUMBPIPE_SHA256_DARWIN_AMD64" 80ac998ee716ae4f1160f371d6110c8c449667f95820c9791f90ec2374166d4e

case "$(uname -m)" in
    arm64|aarch64) host_arch="arm64" ;;
    x86_64|amd64)  host_arch="amd64" ;;
    *)             host_arch="" ;;
esac
if [[ -n "$host_arch" && "$(uname -s)" == "Darwin" && "${GOARCH:-$host_arch}" == "$host_arch" && "${GOOS:-darwin}" == darwin ]]; then
    "${DUMBPIPE_BUNDLE}/dumbpipe-darwin-${host_arch}" --version
fi
