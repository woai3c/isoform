#!/bin/bash
set -euo pipefail

# Wails post-build hook (wails.json, postBuildHooks "darwin/*"): re-sign the
# development bundle with the "Isoform Dev" identity after every rebuild.
#
# `wails dev` compiles a fresh binary on each Go change and ad-hoc signs it,
# which keys the bundle's designated requirement to that binary's cdhash and
# invalidates the macOS privacy grants of the previous build. Signing the
# bundle with the development identity instead keeps the requirement, and so
# the grants, the same across rebuilds (see dev-signing-identity.sh). Wails
# runs the hook after its own ad-hoc signing, so this signature is the one
# that counts.
#
# The hook signs only when start-wails-gui.sh exported YANSU_DEV_SIGNING=1
# (the identity exists) AND the bundle's CFBundleIdentifier is
# com.wails.Isoform (Info.dev.plist); either check alone would let a leaked
# variable re-sign a Yansu build from build-wails.sh, which runs the same
# hooks and clears the variables first. An Isoform bundle that is NOT signed
# here (raw `wails dev`, no identity, codesign failure) keeps its ad-hoc
# signature, so the hook drops the privacy-grants record
# (reset-dev-permissions.sh --forget) and the next identity-signed build
# clears once. YANSU_KEEP_PERMISSIONS=1 (start-wails-gui.sh
# --keep-permissions) leaves the privacy grants alone. Wails runs the hook
# with build/bin as the working directory and the compiled executable as $1.
#
# Wails shows a hook's stdout only at -v 2 and its stderr only when the hook
# fails, and this hook never fails the build, so everything it says also goes
# to ~/.isoform-agent/dev-signing/sign-dev-build.log and to the terminal
# (/dev/tty) when there is one.

BIN="${1:-}"
LOG_PREFIX="[sign-dev-build]"
LOG_FILE="${HOME}/.isoform-agent/dev-signing/sign-dev-build.log"

log() {
    local line="${LOG_PREFIX} $*"
    echo "$line"
    if [ -d "$(dirname "$LOG_FILE")" ]; then
        printf '%s %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "$line" >> "$LOG_FILE" 2>/dev/null || true
    fi
    # Without a controlling terminal the redirection itself fails, so the
    # whole group is silenced.
    { printf '%s\n' "$line" > /dev/tty; } 2>/dev/null || true
}

if [ "$(uname -s)" != "Darwin" ]; then
    exit 0
fi
if [ -z "$BIN" ] || [ ! -f "$BIN" ]; then
    exit 0
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WAILS_DIR="$(dirname "$SCRIPT_DIR")"
ENTITLEMENTS="${WAILS_DIR}/build/darwin/entitlements.plist"

# build-wails.sh signs its own bundles after embedding the runtime bundles
# (release and development builds are both com.wails.Isoform at hook time),
# so the hook only handles `wails dev` and leaves the signing record alone.
if [ "${YANSU_BUILD_WAILS:-}" = "1" ]; then
    exit 0
fi

# The executable sits in <bundle>.app/Contents/MacOS/; only the Isoform dev
# bundle (Info.dev.plist) is handled.
BUNDLE="$(cd "$(dirname "$BIN")/../.." 2>/dev/null && pwd || true)"
case "$BUNDLE" in
    *.app) ;;
    *) exit 0 ;;
esac
BUNDLE_ID="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' "${BUNDLE}/Contents/Info.plist" 2>/dev/null || true)"
if [ "$BUNDLE_ID" != "com.wails.Isoform" ]; then
    if [ "${YANSU_DEV_SIGNING:-}" = "1" ]; then
        log "${BUNDLE##*/} is '${BUNDLE_ID:-unidentified}', not com.wails.Isoform; leaving its signature alone"
    fi
    exit 0
fi

# The Keychain access list can trust only the CLI sealed into this app, never
# a working-directory or PATH discovery. Sign nested code before the bundle.
embed_dev_cli() {
    local source="${WAILS_DIR}/yansu-cli-bundle/bin/yansu"
    local target="${BUNDLE}/Contents/Resources/yansu-cli-bundle/bin/yansu"
    if [ ! -f "$source" ] || [ ! -x "$source" ] || [ -L "$source" ]; then
        log "no regular bundled CLI to embed; desktop API access remains app-only"
        return 0
    fi
    mkdir -p "$(dirname "$target")" || return 1
    cp -f "$source" "$target" || return 1
    codesign --force --timestamp=none --options runtime "$@" "$target"
}

# From here on the bundle is the Isoform dev build. Whenever it is left with
# its ad-hoc signature, whatever gets granted to it is keyed to that build's
# cdhash, so the record of which signature the grants were made for is
# dropped: the next identity-signed build then clears them once instead of
# trusting a stale record.
leave_ad_hoc() {
    # Keep the CLI inside the sealed bundle even without a development
    # identity. Its ad-hoc identity changes on rebuild, just like the app's.
    if embed_dev_cli --sign -; then
        local args=(--force --timestamp=none --sign -)
        if [ -f "$ENTITLEMENTS" ]; then
            args+=(--entitlements "$ENTITLEMENTS")
        fi
        if ! codesign "${args[@]}" "$BUNDLE"; then
            log "could not refresh the ad-hoc bundle seal; Keychain CLI access will fail closed"
        fi
    else
        log "could not embed the CLI; Keychain CLI access will fail closed"
    fi
    log "$1; this build keeps its ad-hoc signature and macOS will ask for its permissions again"
    bash "${SCRIPT_DIR}/reset-dev-permissions.sh" --forget >/dev/null 2>&1 || true
    exit 0
}

if [ "${YANSU_DEV_SIGNING:-}" != "1" ]; then
    # A raw `wails dev`, or start-wails-gui.sh without the identity.
    leave_ad_hoc "signing with the development identity was not requested (start-wails-gui.sh exports YANSU_DEV_SIGNING=1 when the identity exists)"
fi

if ! identity="$(bash "${SCRIPT_DIR}/dev-signing-identity.sh" find 2>/dev/null)"; then
    reason="$(bash "${SCRIPT_DIR}/dev-signing-identity.sh" find 2>&1 >/dev/null || true)"
    leave_ad_hoc "no usable development identity${reason:+ (}${reason}${reason:+)}"
fi
IDENTITY="$(printf '%s\n' "$identity" | sed -n 1p)"
KEYCHAIN="$(printf '%s\n' "$identity" | sed -n 2p)"

if ! cli_output="$(embed_dev_cli --keychain "$KEYCHAIN" --sign "$IDENTITY" 2>&1)"; then
    leave_ad_hoc "could not sign the bundled CLI: ${cli_output}"
fi

sign_args=(--force --timestamp=none --keychain "$KEYCHAIN" --sign "$IDENTITY")
if [ -f "$ENTITLEMENTS" ]; then
    sign_args+=(--entitlements "$ENTITLEMENTS")
fi

if ! codesign_output="$(codesign "${sign_args[@]}" "$BUNDLE" 2>&1)"; then
    leave_ad_hoc "codesign failed: ${codesign_output}"
fi
log "signed ${BUNDLE##*/} with the development identity ${IDENTITY}"

# The grants are keyed to the designated requirement, which is now the same
# for every rebuild; they are cleared only when it differs from the one the
# grants were last made for (the first identity-signed build after ad-hoc
# builds, or a new certificate).
if [ "${YANSU_KEEP_PERMISSIONS:-}" = "1" ]; then
    log "privacy grants left in place (--keep-permissions)"
    exit 0
fi
if reset_output="$(bash "${SCRIPT_DIR}/reset-dev-permissions.sh" --if-signature-changed com.wails.Isoform "$BUNDLE" 2>&1)"; then
    printf '%s\n' "$reset_output" | while IFS= read -r line; do log "  $line"; done
else
    printf '%s\n' "$reset_output" | while IFS= read -r line; do log "  $line"; done
    log "could not check or clear the previous build's privacy grants; remove Isoform from Screen & System Audio Recording and Accessibility in System Settings and add it again"
fi
exit 0
