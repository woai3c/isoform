#!/usr/bin/env bash
set -euo pipefail

# Download a static ffmpeg binary for the current (or specified) platform.
#
# Usage:
#   wails-gui/scripts/download-ffmpeg.sh              # auto-detect platform
#   wails-gui/scripts/download-ffmpeg.sh darwin arm64  # explicit platform/arch
#
# Source: https://github.com/eugeneware/ffmpeg-static

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WAILS_DIR="$(dirname "$SCRIPT_DIR")"
FFMPEG_BUNDLE="${WAILS_DIR}/ffmpeg-bundle"
source "${SCRIPT_DIR}/bundle-architecture.sh"

FFMPEG_VERSION="${FFMPEG_VERSION:-b6.0}"

# On Windows (MINGW/MSYS/Cygwin), the binary is ffmpeg.exe.
case "$(uname -s | tr '[:upper:]' '[:lower:]')" in
    mingw*|msys*|cygwin*) FFMPEG_DEST="${FFMPEG_BUNDLE}/bin/ffmpeg.exe" ;;
    *)                    FFMPEG_DEST="${FFMPEG_BUNDLE}/bin/ffmpeg" ;;
esac

# ── Platform detection ──────────────────────────────────────────────

detect_platform() {
    local os arch
    os="$(uname -s | tr '[:upper:]' '[:lower:]')"
    arch="$(uname -m)"

    case "$os" in
        darwin)                        os="darwin" ;;
        linux)                         os="linux" ;;
        mingw*|msys*|cygwin*|windows*) os="win32" ;;
        *)
            echo "Unsupported OS: $os" >&2
            exit 1
            ;;
    esac

    case "$arch" in
        arm64|aarch64) arch="arm64" ;;
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

if [[ -n "${FFMPEG_TARGET:-}" ]]; then
    # FFMPEG_TARGET is set by build-wails.sh during cross-compilation.
    FFMPEG_PLATFORM="$FFMPEG_TARGET"
elif [[ -n "$PLATFORM" && -n "$ARCH" ]]; then
    # Normalize explicit arguments to ffmpeg-static release naming
    case "$PLATFORM" in
        windows) PLATFORM="win32" ;;
    esac
    case "$ARCH" in
        arm64|aarch64) ARCH="arm64" ;;
        x86_64|amd64|x64) ARCH="x64" ;;
    esac
    FFMPEG_PLATFORM="${PLATFORM}-${ARCH}"
else
    FFMPEG_PLATFORM="$(detect_platform)"
fi

archive_sha="" binary_sha=""
case "$FFMPEG_PLATFORM" in
    darwin-arm64) archive_sha=6be74d6f449889c2e87a75873894f8520cad56c08ac76f2a628d85b0519daaca; binary_sha=a90e3db6a3fd35f6074b013f948b1aa45b31c6375489d39e572bea3f18336584 ;;
    darwin-x64) archive_sha=a12354fce7eb62361473bbe10d53a1893695babd35869ec8e92e5dfea8d0440b; binary_sha=cfe20936c83ecf5d68e424b87e8cc45b24dd6be81787810123bb964a0df686f9 ;;
esac
if [[ -n "$archive_sha" && "$FFMPEG_VERSION" != b6.0 ]]; then
    echo "[download-ffmpeg] macOS requires pinned version b6.0" >&2
    exit 1
fi

# ── Skip if already present ─────────────────────────────────────────

if [[ -f "$FFMPEG_DEST" ]]; then
    if [[ -z "$archive_sha" ]] || { bundle_macho_matches "$FFMPEG_DEST" "${FFMPEG_PLATFORM#darwin-}" && [[ "$(bundle_sha256 "$FFMPEG_DEST")" == "$binary_sha" ]]; }; then
        echo "[download-ffmpeg] Reusing verified target bundle at ${FFMPEG_DEST}"
        exit 0
    fi
    echo "[download-ffmpeg] Cached FFmpeg differs from the target or pin; re-downloading"
fi

# ── Download ────────────────────────────────────────────────────────

# eugeneware/ffmpeg-static distributes gzipped single binaries.
# Archive names never include .exe; the destination path handles the extension.
BINARY_NAME="ffmpeg-${FFMPEG_PLATFORM}"

FFMPEG_RELEASE_URL="https://github.com/eugeneware/ffmpeg-static/releases/download/${FFMPEG_VERSION}/${BINARY_NAME}.gz"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

echo "[download-ffmpeg] Downloading ffmpeg for ${FFMPEG_PLATFORM}..."
echo "[download-ffmpeg] URL: ${FFMPEG_RELEASE_URL}"

curl -fSL --retry 5 --retry-all-errors -o "${TMP_DIR}/ffmpeg.gz" "$FFMPEG_RELEASE_URL"
if [[ -n "$archive_sha" && "$(bundle_sha256 "${TMP_DIR}/ffmpeg.gz")" != "$archive_sha" ]]; then
    echo "[download-ffmpeg] Archive SHA-256 mismatch for ${FFMPEG_PLATFORM}" >&2
    exit 1
fi

echo "[download-ffmpeg] Decompressing..."
gunzip -c "${TMP_DIR}/ffmpeg.gz" > "${TMP_DIR}/ffmpeg"
if [[ -n "$binary_sha" ]]; then
    bundle_macho_matches "${TMP_DIR}/ffmpeg" "${FFMPEG_PLATFORM#darwin-}" || { echo "[download-ffmpeg] Wrong Mach-O architecture" >&2; exit 1; }
    [[ "$(bundle_sha256 "${TMP_DIR}/ffmpeg")" == "$binary_sha" ]] || { echo "[download-ffmpeg] Binary SHA-256 mismatch" >&2; exit 1; }
fi

mkdir -p "$(dirname "$FFMPEG_DEST")"
cp "${TMP_DIR}/ffmpeg" "$FFMPEG_DEST"
chmod +x "$FFMPEG_DEST"

echo "[download-ffmpeg] Installed ffmpeg to ${FFMPEG_DEST}"
if bundle_host_matches "${FFMPEG_PLATFORM%%-*}" "${FFMPEG_PLATFORM#*-}"; then
    "$FFMPEG_DEST" -version 2>/dev/null | head -1 || true
else
    echo "[download-ffmpeg] Cross-target: verified archive and architecture without execution"
fi
