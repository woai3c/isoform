#!/usr/bin/env bash
set -euo pipefail

# Download the pinned magpie CLI (https://usemagpie.ai) for macOS arm64 and
# amd64 into wails-gui/magpie-bundle/ as magpie-cli-darwin-<arch>. Yansu runs
# it as a local gateway for managed model accounts (see magpie_sidecar.go).
#
# Trust: the SHA-256 pins below are the trust anchor. Each asset is fetched
# from the pinned version's GitHub release and must match its pin. The release
# feed (usemagpie.ai/api/latest) is only a cross-check: while it still lists
# MAGPIE_VERSION, its hashes must equal the pins too. Feed moves to a newer
# release are reported, not followed. To adopt a new magpie release, review it,
# then bump the version and both hashes together.
#
# Usage:
#   wails-gui/scripts/download-magpie.sh

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WAILS_DIR="$(dirname "$SCRIPT_DIR")"
MAGPIE_BUNDLE="${WAILS_DIR}/magpie-bundle"
source "${SCRIPT_DIR}/bundle-architecture.sh"

MAGPIE_VERSION="0.1.1172"
MAGPIE_FEED_URL="https://usemagpie.ai/api/latest"
MAGPIE_RELEASE_URL="https://github.com/yetone/magpie-releases/releases/download/v${MAGPIE_VERSION}"
MAGPIE_SHA256_DARWIN_ARM64="0415e93223180a69c845e91ef9e10dbb37c413da8f8a311c09b2e91177232189"
MAGPIE_SHA256_DARWIN_AMD64="65931637cf4d007b662753dee3725eed2fb5058659b2deeae9c76f3437e5787b"

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

sha256_of() {
    if command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$1" | awk '{print $1}'
    else
        sha256sum "$1" | awk '{print $1}'
    fi
}

# feed_check <asset-name> <pinned-sha256> checks the release feed when it still
# lists MAGPIE_VERSION. A different feed version is reported and not an error.
feed_check() {
    local asset="$1" pinned="$2" feed_sha
    if [[ ! -s "${TMP_DIR}/latest.json" ]]; then
        return 0 # feed unreachable: the pinned hash has already been checked
    fi
    feed_sha="$(python3 -I -c '
import json, sys
feed_path, version, name = sys.argv[1:4]
with open(feed_path, encoding="utf-8") as fh:
    feed = json.load(fh)
if feed.get("version") != version:
    print("feed:" + str(feed.get("version")))
    sys.exit(0)
asset = (feed.get("assets") or {}).get(name)
if not asset:
    sys.exit("[download-magpie] Error: feed has no asset " + name)
print(asset["sha256"])
' "${TMP_DIR}/latest.json" "$MAGPIE_VERSION" "$asset")"
    case "$feed_sha" in
        feed:*)
            echo "[download-magpie] Note: the feed now lists magpie ${feed_sha#feed:}; keeping the pinned ${MAGPIE_VERSION}"
            ;;
        *)
            if [[ "$feed_sha" != "$pinned" ]]; then
                echo "[download-magpie] Error: feed sha256 for ${asset} (${feed_sha}) differs from the pin (${pinned})" >&2
                exit 1
            fi
            ;;
    esac
}

download_arch() {
    local arch="$1" pinned="$2"
    local asset="magpie-cli-darwin-${arch}"
    local actual dest

    dest="${MAGPIE_BUNDLE}/${asset}"
    if [[ -f "$dest" ]] && bundle_macho_matches "$dest" "$arch" && [[ "$(sha256_of "$dest")" == "$pinned" ]]; then
        echo "[download-magpie] Reusing verified ${asset}"
        return
    fi

    echo "[download-magpie] Downloading ${asset} (magpie ${MAGPIE_VERSION})..."
    curl -fSL --retry 5 --retry-all-errors -o "${TMP_DIR}/${asset}" "${MAGPIE_RELEASE_URL}/${asset}"

    actual="$(sha256_of "${TMP_DIR}/${asset}")"
    if [[ "$actual" != "$pinned" ]]; then
        echo "[download-magpie] Error: sha256 mismatch for ${asset}: got ${actual}, want ${pinned}" >&2
        exit 1
    fi
    bundle_macho_matches "${TMP_DIR}/${asset}" "$arch" || { echo "[download-magpie] Wrong architecture for ${asset}" >&2; exit 1; }
    feed_check "$asset" "$pinned"

    dest="${MAGPIE_BUNDLE}/${asset}"
    mkdir -p "$MAGPIE_BUNDLE"
    cp "${TMP_DIR}/${asset}" "$dest"
    chmod +x "$dest"
    echo "[download-magpie] Installed ${dest}"
}

echo "[download-magpie] Reading the release feed..."
curl -fSL --retry 5 --retry-all-errors -o "${TMP_DIR}/latest.json" "$MAGPIE_FEED_URL" || {
    echo "[download-magpie] Warning: release feed unreachable; the pinned hashes still apply" >&2
    : >"${TMP_DIR}/latest.json"
}

download_arch arm64 "$MAGPIE_SHA256_DARWIN_ARM64"
download_arch amd64 "$MAGPIE_SHA256_DARWIN_AMD64"

# Smoke-test the binary that runs on this machine.
case "$(uname -m)" in
    arm64|aarch64) host_arch="arm64" ;;
    x86_64|amd64)  host_arch="amd64" ;;
    *)             host_arch="" ;;
esac
if [[ -n "$host_arch" && "$(uname -s)" == "Darwin" && "${GOARCH:-$host_arch}" == "$host_arch" && "${GOOS:-darwin}" == darwin ]]; then
    "${MAGPIE_BUNDLE}/magpie-cli-darwin-${host_arch}" version
fi
