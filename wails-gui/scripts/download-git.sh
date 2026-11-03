#!/usr/bin/env bash
set -euo pipefail

# Download a portable git distribution for the current (or cross-compiled) platform.
#
# - Windows: MinGit from git-for-windows (official portable build, ~45 MB)
# - macOS/Linux: copies the system git and its minimal dependencies
#
# Usage:
#   wails-gui/scripts/download-git.sh

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WAILS_DIR="$(dirname "$SCRIPT_DIR")"
GIT_BUNDLE="${WAILS_DIR}/git-bundle"

MINGIT_VERSION="2.47.1"
MINGIT_RELEASE="2.47.1.windows.1"

# ── Platform detection ──────────────────────────────────────────────

detect_os() {
    local os
    os="$(uname -s | tr '[:upper:]' '[:lower:]')"
    case "$os" in
        darwin)                       echo "darwin" ;;
        linux)                        echo "linux" ;;
        mingw*|msys*|cygwin*|windows*) echo "windows" ;;
        *)
            echo "Unsupported OS: $os" >&2
            exit 1
            ;;
    esac
}

detect_arch() {
    local arch
    arch="$(uname -m)"
    case "$arch" in
        arm64|aarch64) echo "arm64" ;;
        x86_64|amd64)  echo "amd64" ;;
        *)
            echo "Unsupported architecture: $arch" >&2
            exit 1
            ;;
    esac
}

TARGET_OS="${GOOS:-$(detect_os)}"
TARGET_ARCH="${GOARCH:-$(detect_arch)}"
HOST_OS="$(detect_os)"
HOST_ARCH="$(detect_arch)"

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

write_bundle_metadata() {
    printf '%s/%s\n' "$TARGET_OS" "$TARGET_ARCH" > "${GIT_BUNDLE}/.target-platform"
}

# ── Windows: download MinGit ────────────────────────────────────────

download_mingit() {
    local url arch_suffix
    case "$TARGET_ARCH" in
        amd64) arch_suffix="64-bit" ;;
        arm64) arch_suffix="arm64" ;;
        *)
            echo "[download-git] Unsupported Windows arch: $TARGET_ARCH" >&2
            exit 1
            ;;
    esac

    url="https://github.com/git-for-windows/git/releases/download/v${MINGIT_RELEASE}/MinGit-${MINGIT_VERSION}-${arch_suffix}.zip"
    echo "[download-git] Downloading MinGit for Windows ${arch_suffix}..."
    echo "[download-git] URL: ${url}"

    curl -fSL --retry 5 --retry-all-errors -o "${TMP_DIR}/mingit.zip" "$url"

    echo "[download-git] Extracting MinGit..."
    rm -rf "$GIT_BUNDLE"
    mkdir -p "$GIT_BUNDLE"
    unzip -q -o "${TMP_DIR}/mingit.zip" -d "$GIT_BUNDLE"

    # MinGit extracts with cmd/git.exe at the top level.
    # Create a bin/ symlink so our resolution logic finds it consistently.
    if [ ! -d "${GIT_BUNDLE}/bin" ]; then
        mkdir -p "${GIT_BUNDLE}/bin"
    fi
    # MinGit puts git.exe in cmd/ — copy to bin/ for uniform access.
    if [ -f "${GIT_BUNDLE}/cmd/git.exe" ] && [ ! -f "${GIT_BUNDLE}/bin/git.exe" ]; then
        cp "${GIT_BUNDLE}/cmd/git.exe" "${GIT_BUNDLE}/bin/git.exe"
    fi

    write_bundle_metadata
    echo "[download-git] MinGit installed to ${GIT_BUNDLE}"
}

# ── macOS/Linux: bundle system git ──────────────────────────────────

bundle_system_git() {
    local git_path
    git_path="$(command -v git 2>/dev/null || true)"

    if [ -z "$git_path" ]; then
        echo "[download-git] Warning: git not found on system, skipping git bundle" >&2
        echo "[download-git] Install git (e.g., xcode-select --install on macOS) and re-run" >&2
        exit 0
    fi

    echo "[download-git] Bundling system git from ${git_path}..."

    rm -rf "$GIT_BUNDLE"
    mkdir -p "${GIT_BUNDLE}/bin"

    # Copy the git binary itself.  Use cp -f so a second copy (below)
    # can overwrite even if the first destination inherited restricted
    # permissions from a SIP-protected or read-only source binary.
    cp -f "$git_path" "${GIT_BUNDLE}/bin/git"
    chmod +x "${GIT_BUNDLE}/bin/git"

    # On macOS, git is often a shim that delegates to the Xcode toolchain.
    # Resolve the real path and copy the actual binary.
    local real_git
    if [[ "$(uname -s)" == "Darwin" ]]; then
        real_git="$(xcrun -f git 2>/dev/null || echo "$git_path")"
    else
        real_git="$(readlink -f "$git_path" 2>/dev/null || echo "$git_path")"
    fi
    if [ "$real_git" != "$git_path" ]; then
        rm -f "${GIT_BUNDLE}/bin/git"
        cp -f "$real_git" "${GIT_BUNDLE}/bin/git"
        chmod +x "${GIT_BUNDLE}/bin/git"
    fi

    # Try to also bundle git-core helpers (needed for clone, fetch, etc.)
    # Use -L to dereference symlinks — many git-core entries are relative
    # symlinks to ../../bin/git which become dangling after the copy.
    local git_exec_dir
    git_exec_dir="$(git --exec-path 2>/dev/null || true)"
    if [ -n "$git_exec_dir" ] && [ -d "$git_exec_dir" ]; then
        echo "[download-git] Bundling git core from ${git_exec_dir}..."
        mkdir -p "${GIT_BUNDLE}/libexec/git-core"
        cp -Rf "${git_exec_dir}/"* "${GIT_BUNDLE}/libexec/git-core/" 2>/dev/null || echo "[download-git] Warning: some git-core files could not be copied from ${git_exec_dir}"
        # Remove any dangling symlinks that survived the copy
        find "${GIT_BUNDLE}/libexec/git-core" -type l ! -exec test -e {} \; -delete 2>/dev/null || true

        local git_prefix git_templates_dir
        git_prefix="$(cd "${git_exec_dir}/../.." && pwd)"
        git_templates_dir="${git_prefix}/share/git-core/templates"
        if [ -d "$git_templates_dir" ]; then
            echo "[download-git] Bundling git templates from ${git_templates_dir}..."
            mkdir -p "${GIT_BUNDLE}/share/git-core"
            cp -RP "$git_templates_dir" "${GIT_BUNDLE}/share/git-core/"
        fi
    fi

    echo "[download-git] System git bundled to ${GIT_BUNDLE}"
    "${GIT_BUNDLE}/bin/git" --version 2>/dev/null || echo "[download-git] (git --version check skipped)"
    write_bundle_metadata
}

require_host_platform_match() {
    if [ "$TARGET_OS" != "$HOST_OS" ] || [ "$TARGET_ARCH" != "$HOST_ARCH" ]; then
        echo "[download-git] Warning: cannot bundle system git for ${TARGET_OS}/${TARGET_ARCH} from ${HOST_OS}/${HOST_ARCH}, skipping"
        echo "[download-git] The app will use the system git on the target machine"
        exit 0
    fi
}

# ── Main ────────────────────────────────────────────────────────────

case "$TARGET_OS" in
    windows)
        download_mingit
        ;;
    darwin|linux)
        require_host_platform_match
        bundle_system_git
        ;;
    *)
        echo "[download-git] Unsupported OS: $TARGET_OS" >&2
        exit 1
        ;;
esac

# ── Cache (skip in CI) ──────────────────────────────────────────────
GIT_CACHE_DIR="${HOME}/.cache/yansu/git-bundle-output"
if [ -z "${CI:-}" ]; then
    echo "[download-git] Caching bundle to ${GIT_CACHE_DIR}..."
    rm -rf "$GIT_CACHE_DIR"
    # Preserve git-core symlinks; macOS cp -r can expand them into full files.
    cp -RP "$GIT_BUNDLE" "$GIT_CACHE_DIR"
fi
