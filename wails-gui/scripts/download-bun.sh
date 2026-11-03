#!/usr/bin/env bash
set -euo pipefail

# Download a bun binary for the current (or specified) platform.
#
# Usage:
#   wails-gui/scripts/download-bun.sh              # auto-detect platform
#   wails-gui/scripts/download-bun.sh darwin arm64  # explicit platform/arch

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WAILS_DIR="$(dirname "$SCRIPT_DIR")"
BUN_BUNDLE="${WAILS_DIR}/bun-bundle"
source "${SCRIPT_DIR}/bundle-architecture.sh"
# 1.4.3 matches the daily development build. Archive pins are from Bun's
# published SHASUMS256.txt; binary pins also validate an existing cache.
BUN_VERSION="1.4.3"

# On Windows (MINGW/MSYS/Cygwin), the binary is bun.exe.
case "$(uname -s | tr '[:upper:]' '[:lower:]')" in
    mingw*|msys*|cygwin*) BUN_DEST="${BUN_BUNDLE}/bin/bun.exe" ;;
    *)                    BUN_DEST="${BUN_BUNDLE}/bin/bun" ;;
esac

# ── Platform detection ──────────────────────────────────────────────

detect_platform() {
    local os arch
    os="$(uname -s | tr '[:upper:]' '[:lower:]')"
    arch="$(uname -m)"

    case "$os" in
        darwin)          os="darwin" ;;
        linux)           os="linux" ;;
        mingw*|msys*|cygwin*|windows*) os="windows" ;;
        *)
            echo "Unsupported OS: $os" >&2
            exit 1
            ;;
    esac

    case "$arch" in
        arm64|aarch64) arch="aarch64" ;;
        x86_64|amd64)  arch="x64" ;;
        *)
            echo "Unsupported architecture: $arch" >&2
            exit 1
            ;;
    esac

    echo "${os}-${arch}"
}

PLATFORM="${1:-}"
ARCH="${2:-}"

if [[ -n "${BUN_TARGET:-}" ]]; then
    # BUN_TARGET is set by build-wails.sh during cross-compilation
    # (e.g. "bun-darwin-aarch64"). Use it directly.
    BUN_PLATFORM="${BUN_TARGET#bun-}"
elif [[ -n "$PLATFORM" && -n "$ARCH" ]]; then
    # Normalize explicit arguments to bun release naming
    case "$ARCH" in
        arm64|aarch64) ARCH="aarch64" ;;
        x86_64|amd64|x64) ARCH="x64" ;;
    esac
    BUN_PLATFORM="${PLATFORM}-${ARCH}"
else
    BUN_PLATFORM="$(detect_platform)"
fi

case "$BUN_PLATFORM" in
    darwin-aarch64) archive_sha=80afd4c069b46a6fa8f3ec520bebc750a7843e4a8d5b9583d29ad436c00a3403; binary_sha=28c7dd0a06916aba284d570ef44a2d70e9c538eaf2ba58dfe0910525563b3f79 ;;
    darwin-x64) archive_sha=62660e6438eb872278877acca77e1152d5d031324df368d4a81b428211489d2d; binary_sha=c2a945a9d33f9088b32ac3550d1d1fd485a7fc10d71c66f91ef0d33b41cefa7c ;;
    linux-aarch64) archive_sha=efa9813da5ed72423bf847f916e8d2c47c0d776add972354026a75e10da9aa21; binary_sha= ;;
    linux-x64) archive_sha=3e730528d89775f03f87f05c06787c4df4099d0a37f3f252e7b829cb23816e18; binary_sha= ;;
    windows-x64) archive_sha=0bf84e808552beea67bf1202a287a35c6c825d837b287b3f8e9dd513dba35d3d; binary_sha= ;;
    *) echo "[download-bun] Unsupported pinned target: $BUN_PLATFORM" >&2; exit 1 ;;
esac
if [[ "$BUN_PLATFORM" == darwin-* && -f "$BUN_DEST" ]]; then
    if bundle_macho_matches "$BUN_DEST" "${BUN_PLATFORM#darwin-}" && [[ "$(bundle_sha256 "$BUN_DEST")" == "$binary_sha" ]]; then
        echo "[download-bun] Reusing verified ${BUN_PLATFORM} Bun ${BUN_VERSION}"
        exit 0
    fi
    echo "[download-bun] Cached Bun differs from the target or pin; re-downloading"
fi

# ── Download ────────────────────────────────────────────────────────

BUN_RELEASE_URL="https://github.com/oven-sh/bun/releases/download/bun-v${BUN_VERSION}/bun-${BUN_PLATFORM}.zip"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

echo "[download-bun] Downloading bun for ${BUN_PLATFORM}..."
echo "[download-bun] URL: ${BUN_RELEASE_URL}"

curl -fSL --retry 5 --retry-all-errors -o "${TMP_DIR}/bun.zip" "$BUN_RELEASE_URL"
actual="$(bundle_sha256 "${TMP_DIR}/bun.zip")"
if [[ "$actual" != "$archive_sha" ]]; then
    echo "[download-bun] SHA-256 mismatch: got $actual, expected $archive_sha" >&2
    exit 1
fi

echo "[download-bun] Extracting..."
unzip -q -o "${TMP_DIR}/bun.zip" -d "$TMP_DIR"

# The zip contains a directory like bun-darwin-aarch64/bun (or bun.exe on Windows).
BUN_EXTRACTED="$(find "$TMP_DIR" -name 'bun' -type f | head -1)"
if [[ -z "$BUN_EXTRACTED" ]]; then
    BUN_EXTRACTED="$(find "$TMP_DIR" -name 'bun.exe' -type f | head -1)"
fi
if [[ "$BUN_PLATFORM" == darwin-* ]]; then
    bundle_macho_matches "$BUN_EXTRACTED" "${BUN_PLATFORM#darwin-}" || { echo "[download-bun] Wrong Mach-O architecture" >&2; exit 1; }
    [[ "$(bundle_sha256 "$BUN_EXTRACTED")" == "$binary_sha" ]] || { echo "[download-bun] Extracted binary SHA-256 mismatch" >&2; exit 1; }
fi
if [[ -z "$BUN_EXTRACTED" ]]; then
    echo "[download-bun] Error: bun binary not found in archive" >&2
    exit 1
fi

mkdir -p "$(dirname "$BUN_DEST")"
cp "$BUN_EXTRACTED" "$BUN_DEST"
chmod +x "$BUN_DEST"

echo "[download-bun] Installed bun to ${BUN_DEST}"
if bundle_host_matches "${BUN_PLATFORM%%-*}" "${BUN_PLATFORM#*-}"; then
    "$BUN_DEST" --version
else
    echo "[download-bun] Cross-target: verified archive and architecture without execution"
fi

# ── Cache downloaded bundle globally (skip in CI) ─────────────────────
# Allows other worktrees to reuse the bun binary without re-downloading.
BUN_CACHE_DIR="${HOME}/.cache/yansu/bun-bundle-output"
if [ -z "${CI:-}" ]; then
    echo "[download-bun] Caching bundle to ${BUN_CACHE_DIR}..."
    mkdir -p "$(dirname "$BUN_CACHE_DIR")"
    rm -rf "$BUN_CACHE_DIR"
    cp -r "$BUN_BUNDLE" "$BUN_CACHE_DIR"
fi
