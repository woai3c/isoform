#!/usr/bin/env bash
set -euo pipefail

# Download PaddleOCR-json for the Windows OCR backend.
#
# Usage:
#   wails-gui/scripts/download-paddleocr.sh                # auto-detect platform
#   wails-gui/scripts/download-paddleocr.sh windows amd64  # explicit
#
# PaddleOCR-json is only published for windows/amd64; the script no-ops on
# every other target so cross-builds for macOS / Linux still pass.
#
# Source: https://github.com/hiroi-sora/PaddleOCR-json/releases

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WAILS_DIR="$(dirname "$SCRIPT_DIR")"
PADDLEOCR_BUNDLE="${WAILS_DIR}/paddleocr-bundle"

PADDLEOCR_VERSION="${PADDLEOCR_VERSION:-v1.4.1}"
PADDLEOCR_URL="https://github.com/hiroi-sora/PaddleOCR-json/releases/download/${PADDLEOCR_VERSION}/PaddleOCR-json_${PADDLEOCR_VERSION}_windows_x64.7z"
# SHA-256 of the upstream release archive. Recomputed when bumping
# PADDLEOCR_VERSION; failure to match aborts the install so a tampered or
# truncated download can never reach the user's machine. This is the real
# integrity guarantee — TLS only protects in-flight bytes.
PADDLEOCR_SHA256="${PADDLEOCR_SHA256:-c0912a70acb1f8f18fafe1f438a2935292a6ec7e2859156fa48a33e91358d71d}"
SEVENZR_URL="https://www.7-zip.org/a/7zr.exe"
# SHA-256 of the 7zr.exe used to unpack the PaddleOCR archive. Pinned so a
# compromised 7-zip.org distribution can't smuggle in a malicious extractor
# that would ignore our verified archive and write attacker-controlled bytes
# to disk under the legitimate filenames. Update alongside SEVENZR_URL if
# upstream rotates the binary.
SEVENZR_SHA256="${SEVENZR_SHA256:-abcf64ae1cbafddb5395e4cdd3bdc7e3e0561d54a0c6380e3dd43bdbffe519a2}"

# ── Platform detection ──────────────────────────────────────────────

detect_target() {
    local os arch
    os="$(uname -s | tr '[:upper:]' '[:lower:]')"
    arch="$(uname -m)"

    case "$os" in
        darwin)                        os="darwin" ;;
        linux)                         os="linux" ;;
        mingw*|msys*|cygwin*|windows*) os="windows" ;;
    esac

    case "$arch" in
        arm64|aarch64)    arch="arm64" ;;
        x86_64|amd64|x64) arch="amd64" ;;
    esac

    echo "${os}/${arch}"
}

PLATFORM_OS="${1:-}"
PLATFORM_ARCH="${2:-}"

if [[ -n "${PADDLEOCR_TARGET:-}" ]]; then
    TARGET="$PADDLEOCR_TARGET"
elif [[ -n "$PLATFORM_OS" && -n "$PLATFORM_ARCH" ]]; then
    TARGET="${PLATFORM_OS}/${PLATFORM_ARCH}"
else
    TARGET="$(detect_target)"
fi

if [[ "$TARGET" != "windows/amd64" ]]; then
    echo "[download-paddleocr] PaddleOCR-json is windows/amd64 only (target=${TARGET}); skipping."
    exit 0
fi

# ── Skip if already extracted ───────────────────────────────────────

if [[ -x "${PADDLEOCR_BUNDLE}/PaddleOCR-json.exe" ]]; then
    echo "[download-paddleocr] Bundle already present at ${PADDLEOCR_BUNDLE}, skipping"
    exit 0
fi

mkdir -p "$PADDLEOCR_BUNDLE"
ARCHIVE="${PADDLEOCR_BUNDLE}/paddleocr.7z"

# ── Download archive ────────────────────────────────────────────────

echo "[download-paddleocr] Downloading PaddleOCR-json ${PADDLEOCR_VERSION} (~89 MB)..."
echo "[download-paddleocr]   ${PADDLEOCR_URL}"
# --ssl-no-revoke disables Schannel's CRL/OCSP revocation lookups on Windows,
# which routinely fail with CRYPT_E_REVOCATION_OFFLINE on networks that block
# the upstream CA endpoints (most corporate / VPN setups). The certificate
# chain, expiry, and hostname are still validated normally — only the
# revocation lookup is bypassed. We do NOT rely on TLS for content
# integrity: the SHA-256 check below is the authoritative guarantee.
curl -L --ssl-no-revoke --progress-bar -o "$ARCHIVE" "$PADDLEOCR_URL"

# ── SHA-256 helpers ─────────────────────────────────────────────────

# Compute SHA-256 of a file using whichever hasher is available.
# Echoes lowercase 64-char hex on success, returns non-zero with no output
# on failure (caller must check $? and the captured stdout).
compute_sha256() {
    local file="$1"
    if command -v sha256sum &>/dev/null; then
        sha256sum "$file" | awk '{print $1}'
    elif command -v shasum &>/dev/null; then
        shasum -a 256 "$file" | awk '{print $1}'
    elif command -v certutil &>/dev/null; then
        # Windows fallback: certutil -hashfile prints "SHA256 hash of ...:"
        # then the hash on the next line. Strip whitespace, take 64 hex chars.
        certutil -hashfile "$file" SHA256 | tr -d '\r\n ' | grep -oE '[a-fA-F0-9]{64}' | head -1 | tr 'A-F' 'a-f'
    else
        return 1
    fi
}

# Verify a file's SHA-256 against an expected hex string. Exits with a clear
# diagnostic and removes the file on mismatch.
verify_sha256() {
    local file="$1" expected="$2" label="$3"
    local actual
    actual="$(compute_sha256 "$file")" || {
        echo "[download-paddleocr] ERROR: no sha256sum/shasum/certutil available; cannot verify ${label}" >&2
        rm -f "$file"
        exit 1
    }
    if [[ "$actual" != "$expected" ]]; then
        echo "[download-paddleocr] ERROR: SHA-256 mismatch on ${label} — refusing to install" >&2
        echo "  expected: ${expected}" >&2
        echo "  actual:   ${actual}" >&2
        rm -f "$file"
        exit 1
    fi
    echo "[download-paddleocr]   ${label} sha256=${actual} (verified)"
}

echo "[download-paddleocr] Verifying SHA-256..."
verify_sha256 "$ARCHIVE" "$PADDLEOCR_SHA256" "PaddleOCR archive"

# ── Locate or fetch a 7z extractor ──────────────────────────────────

EXTRACTOR=""
for cand in 7z 7zr 7za; do
    if command -v "$cand" &>/dev/null; then
        EXTRACTOR="$(command -v "$cand")"
        break
    fi
done

CLEANUP_EXTRACTOR=""
if [[ -z "$EXTRACTOR" ]]; then
    echo "[download-paddleocr] No 7z extractor in PATH; downloading standalone 7zr.exe (~600 KB)..."
    EXTRACTOR="${PADDLEOCR_BUNDLE}/7zr.exe"
    curl -L --ssl-no-revoke --silent --output "$EXTRACTOR" "$SEVENZR_URL"
    # Pin 7zr.exe too — a compromised extractor could ignore the verified
    # archive and write arbitrary content under expected filenames.
    verify_sha256 "$EXTRACTOR" "$SEVENZR_SHA256" "7zr.exe"
    CLEANUP_EXTRACTOR="$EXTRACTOR"
fi

# ── Extract ─────────────────────────────────────────────────────────

echo "[download-paddleocr] Extracting with ${EXTRACTOR}..."
# 7zr's `-o` flag must NOT have a space before the path (per its CLI syntax).
"$EXTRACTOR" x -y -o"$PADDLEOCR_BUNDLE" "$ARCHIVE" >/dev/null

rm -f "$ARCHIVE"
[[ -n "$CLEANUP_EXTRACTOR" ]] && rm -f "$CLEANUP_EXTRACTOR"

# Archive extracts into PADDLEOCR_BUNDLE/PaddleOCR-json_v1.4.1/. Flatten so
# the caller (build-wails.sh) can copy paddleocr-bundle directly next to
# Yansu.exe and the .exe sits at <install>/paddleocr/PaddleOCR-json.exe.
INNER_DIR="${PADDLEOCR_BUNDLE}/PaddleOCR-json_${PADDLEOCR_VERSION}"
if [[ -d "$INNER_DIR" ]]; then
    shopt -s dotglob nullglob
    mv -- "$INNER_DIR"/* "$PADDLEOCR_BUNDLE/"
    rmdir "$INNER_DIR"
    shopt -u dotglob nullglob
fi

if [[ ! -f "${PADDLEOCR_BUNDLE}/PaddleOCR-json.exe" ]]; then
    echo "[download-paddleocr] ERROR: PaddleOCR-json.exe missing after extract" >&2
    exit 1
fi

# ── Strip non-Chinese language models to shrink the bundle ─────────
#
# The product is Chinese + English (English glyphs are covered by the
# Chinese rec dictionary). Removing other languages saves ~50 MB on disk;
# the user can always re-run this script if they decide to expand language
# support later.
echo "[download-paddleocr] Stripping non-Chinese language assets..."
MODELS_DIR="${PADDLEOCR_BUNDLE}/models"
if [[ -d "$MODELS_DIR" ]]; then
    rm -rf \
        "${MODELS_DIR}/chinese_cht_mobile_v2.0_rec_infer" \
        "${MODELS_DIR}/cyrillic_PP-OCRv3_rec_infer" \
        "${MODELS_DIR}/en_PP-OCRv3_rec_infer" \
        "${MODELS_DIR}/japan_PP-OCRv3_rec_infer" \
        "${MODELS_DIR}/korean_PP-OCRv3_rec_infer" \
        2>/dev/null || true
    rm -f \
        "${MODELS_DIR}/config_chinese_cht.txt" \
        "${MODELS_DIR}/config_cyrillic.txt" \
        "${MODELS_DIR}/config_en.txt" \
        "${MODELS_DIR}/config_japan.txt" \
        "${MODELS_DIR}/config_korean.txt" \
        "${MODELS_DIR}/dict_chinese_cht.txt" \
        "${MODELS_DIR}/dict_cyrillic.txt" \
        "${MODELS_DIR}/dict_en.txt" \
        "${MODELS_DIR}/dict_japan.txt" \
        "${MODELS_DIR}/dict_korean.txt" \
        2>/dev/null || true
fi

echo "[download-paddleocr] Bundle ready at ${PADDLEOCR_BUNDLE}"
if command -v du &>/dev/null; then
    du -sh "$PADDLEOCR_BUNDLE" 2>/dev/null | sed 's/^/[download-paddleocr] Total size: /'
fi
