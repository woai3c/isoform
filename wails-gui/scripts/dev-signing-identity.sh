#!/bin/bash
set -euo pipefail

# The "Isoform Dev" code-signing identity for Isoform development builds.
#
# Why: macOS keys an app's privacy grants (Screen Recording, Accessibility,
# Microphone, Contacts, Automation, ...) to the app's designated requirement.
# An ad-hoc signature (codesign --sign -) has the binary's own cdhash as that
# requirement, so every rebuild starts over: System Settings keeps showing
# the switches on while the new binary is denied. A build signed with a
# certificate gets the requirement
#   identifier "com.wails.Isoform" and certificate leaf = H"<sha1>"
# instead, which is the same for every build, so the grants survive rebuilds.
#
# What: a self-signed certificate and its private key in a keychain of their
# own, ~/.isoform-agent/dev-signing/identity.keychain-db, locked with a
# random password kept beside it (identity.keychain-password, mode 600). The
# keychain is not added to the search list and the certificate is not
# trusted: codesign does not need trust to sign, TCC compares the leaf hash,
# and Gatekeeper never assesses a locally built app. Keeping the key out of
# the login keychain is what makes signing unattended: the login keychain
# asks for the account password in a dialog before a tool may use a key,
# while this keychain's partition list already allows codesign.
#
# Usage:
#   wails-gui/scripts/dev-signing-identity.sh create   # one-time, idempotent; no prompts, no password
#   wails-gui/scripts/dev-signing-identity.sh find     # prints the SHA-1 and the keychain path; exit 1 when absent
#   wails-gui/scripts/dev-signing-identity.sh remove   # deletes the keychain and clears the Isoform grants; builds fall back to ad-hoc signing
#
# build-wails.sh --flavor isoform and start-wails-gui.sh run `find` and sign
# with the identity when it exists (start-wails-gui.sh through the wails.json
# post-build hook, scripts/sign-dev-build.sh). The privacy grants keyed to the
# identity are kept in step by reset-dev-permissions.sh --if-signature-changed.
#
# The key signs without a prompt, so any process running as the user can sign
# as com.wails.Isoform with it and inherit Isoform's grants; it is marked
# non-extractable and only codesign may use it, but run `remove` when not
# working on Isoform.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
IDENTITY_NAME="Isoform Dev"
DEV_SIGNING_DIR="${HOME}/.isoform-agent/dev-signing"
KEYCHAIN="${DEV_SIGNING_DIR}/identity.keychain-db"
PASSWORD_FILE="${DEV_SIGNING_DIR}/identity.keychain-password"
LOG_PREFIX="[dev-signing-identity]"

usage() {
    echo "usage: $0 create|find|remove" >&2
    exit 2
}

if [ "$(uname -s)" != "Darwin" ]; then
    echo "${LOG_PREFIX} only macOS signs code; nothing to do" >&2
    exit 1
fi

# identity_sha prints the SHA-1 of the identity in the keychain, or nothing.
# `security find-identity -v` would list only trusted identities, and this
# certificate is deliberately untrusted, so the unfiltered listing is parsed.
identity_sha() {
    security find-identity -p codesigning "$KEYCHAIN" 2>/dev/null \
        | awk -v name="\"${IDENTITY_NAME}\"" 'index($0, name) { print $2; exit }'
}

unlock_keychain() {
    local password
    password="$(cat "$PASSWORD_FILE")"
    security unlock-keychain -p "$password" "$KEYCHAIN"
}

# find_identity prints "<sha1>\n<keychain>\n" when the identity is usable.
find_identity() {
    local sha
    if [ ! -f "$KEYCHAIN" ] || [ ! -f "$PASSWORD_FILE" ]; then
        return 1
    fi
    if ! unlock_keychain 2>/dev/null; then
        echo "${LOG_PREFIX} ${KEYCHAIN} exists but its password file does not unlock it; run '$0 remove' and then '$0 create'" >&2
        return 1
    fi
    sha="$(identity_sha)"
    if [ -z "$sha" ]; then
        echo "${LOG_PREFIX} ${KEYCHAIN} holds no '${IDENTITY_NAME}' identity; run '$0 remove' and then '$0 create'" >&2
        return 1
    fi
    # codesign would still sign with an expired certificate, but macOS
    # rejects such a signature at launch.
    if ! security find-certificate -c "$IDENTITY_NAME" -p "$KEYCHAIN" 2>/dev/null \
        | /usr/bin/openssl x509 -noout -checkend 0 >/dev/null 2>&1; then
        echo "${LOG_PREFIX} the '${IDENTITY_NAME}' certificate in ${KEYCHAIN} has expired; run '$0 remove' and then '$0 create'" >&2
        return 1
    fi
    printf '%s\n%s\n' "$sha" "$KEYCHAIN"
}

print_summary() {
    local sha="$1"
    echo "${LOG_PREFIX} '${IDENTITY_NAME}' is ready: SHA-1 ${sha} in ${KEYCHAIN}"
    echo "${LOG_PREFIX} Isoform builds now get the designated requirement"
    echo "${LOG_PREFIX}   identifier \"com.wails.Isoform\" and certificate leaf = H\"$(printf '%s' "$sha" | tr '[:upper:]' '[:lower:]')\""
    echo "${LOG_PREFIX} so their macOS privacy grants survive rebuilds; the first such build clears the stale ad-hoc grants once"
    echo "${LOG_PREFIX} check a build with: codesign -d -r- wails-gui/build/bin/Isoform.app"
    echo "${LOG_PREFIX} remove it again with: $0 remove"
}

note_legacy_identity() {
    # An earlier version of this setup imported the identity into the login
    # keychain with user trust settings; it is unused now and only confuses.
    if security find-identity -p codesigning 2>/dev/null | grep -q "\"${IDENTITY_NAME}\""; then
        echo "${LOG_PREFIX} note: the login keychain also holds an '${IDENTITY_NAME}' identity from an earlier setup; it is not used."
        echo "${LOG_PREFIX}       Delete it in Keychain Access (login > My Certificates) if you like; macOS asks for your password for its trust setting."
    fi
}

cmd_create() {
    # tmp is not local: the EXIT trap that removes it runs after the function
    # has returned.
    local existing p12_password keychain_password
    if existing="$(find_identity 2>/dev/null | sed -n 1p)" && [ -n "$existing" ]; then
        echo "${LOG_PREFIX} '${IDENTITY_NAME}' already exists; nothing to do"
        print_summary "$existing"
        note_legacy_identity
        return 0
    fi
    if [ -f "$KEYCHAIN" ] || [ -f "$PASSWORD_FILE" ]; then
        echo "${LOG_PREFIX} ${DEV_SIGNING_DIR} holds a keychain or password file that cannot be used; run '$0 remove' first" >&2
        return 1
    fi

    mkdir -p "$DEV_SIGNING_DIR"
    chmod 700 "$DEV_SIGNING_DIR"
    tmp="$(mktemp -d "${TMPDIR:-/tmp}/isoform-dev-signing.XXXXXX")"
    chmod 700 "$tmp"
    trap 'rm -rf "$tmp"' EXIT

    echo "${LOG_PREFIX} creating a self-signed code-signing certificate '${IDENTITY_NAME}'"
    cat > "${tmp}/openssl.cnf" <<CNF
[req]
distinguished_name = dn
x509_extensions = ext
prompt = no
[dn]
CN = ${IDENTITY_NAME}
[ext]
basicConstraints = critical,CA:false
keyUsage = critical,digitalSignature
extendedKeyUsage = critical,codeSigning
subjectKeyIdentifier = hash
CNF
    # /usr/bin/openssl (LibreSSL) on purpose: its PKCS#12 output uses the
    # legacy ciphers `security import` understands on every supported macOS.
    /usr/bin/openssl req -x509 -newkey rsa:2048 -sha256 -days 3650 -nodes \
        -keyout "${tmp}/key.pem" -out "${tmp}/cert.pem" -config "${tmp}/openssl.cnf" >/dev/null 2>&1
    p12_password="$(/usr/bin/openssl rand -base64 24)"
    export P12_PASSWORD="$p12_password"
    /usr/bin/openssl pkcs12 -export -inkey "${tmp}/key.pem" -in "${tmp}/cert.pem" \
        -name "$IDENTITY_NAME" -out "${tmp}/identity.p12" -passout env:P12_PASSWORD

    echo "${LOG_PREFIX} creating ${KEYCHAIN}"
    keychain_password="$(/usr/bin/openssl rand -base64 24)"
    (umask 077 && printf '%s' "$keychain_password" > "$PASSWORD_FILE")
    security create-keychain -p "$keychain_password" "$KEYCHAIN"
    # No automatic lock (neither on sleep nor after a timeout): a long
    # `wails dev` session re-signs after every rebuild without a prompt.
    security set-keychain-settings "$KEYCHAIN"
    # -x marks the key non-extractable, so it cannot be exported again; -T
    # lets codesign, and only codesign, use it; the partition list below is
    # what stops macOS from confirming that in a dialog.
    security import "${tmp}/identity.p12" -k "$KEYCHAIN" -P "$p12_password" \
        -x -T /usr/bin/codesign >/dev/null
    unset P12_PASSWORD
    rm -f "${tmp}/key.pem" "${tmp}/identity.p12"
    security set-key-partition-list -S apple:,codesign: -s -k "$keychain_password" "$KEYCHAIN" >/dev/null

    existing="$(identity_sha)"
    if [ -z "$existing" ]; then
        echo "${LOG_PREFIX} the identity was imported but cannot be found in ${KEYCHAIN}; run '$0 remove' and try again" >&2
        return 1
    fi
    print_summary "$existing"
    note_legacy_identity
}

cmd_find() {
    find_identity
}

cmd_remove() {
    if [ ! -d "$DEV_SIGNING_DIR" ]; then
        echo "${LOG_PREFIX} no development signing identity at ${DEV_SIGNING_DIR}; nothing to remove"
        return 0
    fi
    if [ -f "$KEYCHAIN" ]; then
        security delete-keychain "$KEYCHAIN" 2>/dev/null || rm -f "$KEYCHAIN"
    fi
    # The directory also holds the privacy-grants record that
    # reset-dev-permissions.sh keeps; it goes with the identity.
    rm -rf "$DEV_SIGNING_DIR"
    echo "${LOG_PREFIX} removed '${IDENTITY_NAME}' (${DEV_SIGNING_DIR})"
    # Deleting the key does not revoke anything: the grants stay keyed to the
    # certificate, and a copy of the keychain and its password file taken
    # earlier would still sign as Isoform. Clearing the grants is what makes
    # the certificate worthless, so `remove` does it; it is the one call that
    # clears the real Isoform grants on the user's own say-so.
    echo "${LOG_PREFIX} clearing the Isoform privacy grants keyed to the removed certificate"
    if bash "${SCRIPT_DIR}/reset-dev-permissions.sh" com.wails.Isoform; then
        echo "${LOG_PREFIX} Isoform builds fall back to ad-hoc signing and ask for their permissions again"
    else
        echo "${LOG_PREFIX} could not clear the grants; remove Isoform from Screen & System Audio Recording, Accessibility and Microphone in System Settings yourself" >&2
        return 1
    fi
}

case "${1:-}" in
    create) cmd_create ;;
    find) cmd_find ;;
    remove) cmd_remove ;;
    *) usage ;;
esac
