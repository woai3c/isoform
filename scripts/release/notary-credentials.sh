#!/usr/bin/env bash
# Source this file to select and use notarization credentials.
unset NOTARY_CREDENTIAL_MODE
notary_credentials_available() {
    [ -n "${NOTARY_CREDENTIAL_MODE:-}" ] && return 0
    : "${APPLE_NOTARY_KEYCHAIN_PROFILE:=isoform-notary}"
    : "${APPLE_NOTARY_KEY_ID:=${ISOFORM_ASC_KEY_ID:-}}"
    : "${APPLE_NOTARY_ISSUER:=${ISOFORM_ASC_ISSUER_ID:-}}"
    : "${APPLE_NOTARY_KEY_PATH:=$HOME/.appstoreconnect/private_keys/AuthKey_${APPLE_NOTARY_KEY_ID}.p8}"

    if xcrun notarytool history --keychain-profile "$APPLE_NOTARY_KEYCHAIN_PROFILE" --output-format json > /dev/null 2>&1; then
        NOTARY_CREDENTIAL_MODE=profile
    elif [ -n "$APPLE_NOTARY_KEY_ID" ] && [ -n "$APPLE_NOTARY_ISSUER" ] && [ -f "$APPLE_NOTARY_KEY_PATH" ]; then
        if ! xcrun notarytool history --key "$APPLE_NOTARY_KEY_PATH" --key-id "$APPLE_NOTARY_KEY_ID" --issuer "$APPLE_NOTARY_ISSUER" --output-format json > /dev/null; then
            echo 'Apple rejected or could not verify the notary API key; no notarization was started.' >&2
            return 1
        fi
        NOTARY_CREDENTIAL_MODE=key
    elif [ -n "${APPLE_ID:-}" ] && [ -n "${APPLE_APP_SPECIFIC_PASSWORD:-}" ] && [ -n "${APPLE_TEAM_ID:-}" ]; then
        if ! xcrun notarytool history --apple-id "$APPLE_ID" --password "$APPLE_APP_SPECIFIC_PASSWORD" --team-id "$APPLE_TEAM_ID" --output-format json > /dev/null; then
            echo 'Apple ID notarization credentials could not be verified.' >&2
            return 1
        fi
        NOTARY_CREDENTIAL_MODE=apple-id
    else
        echo 'No working notary profile, API key file, or complete Apple ID credentials are available.' >&2
        return 1
    fi
}

notarytool_with_credentials() {
    local command="$1"
    shift
    notary_credentials_available || return 1
    case "$NOTARY_CREDENTIAL_MODE" in
        profile) xcrun notarytool "$command" "$@" --keychain-profile "$APPLE_NOTARY_KEYCHAIN_PROFILE" ;;
        key) xcrun notarytool "$command" "$@" --key "$APPLE_NOTARY_KEY_PATH" --key-id "$APPLE_NOTARY_KEY_ID" --issuer "$APPLE_NOTARY_ISSUER" ;;
        apple-id) xcrun notarytool "$command" "$@" --apple-id "$APPLE_ID" --password "$APPLE_APP_SPECIFIC_PASSWORD" --team-id "$APPLE_TEAM_ID" ;;
    esac
}
