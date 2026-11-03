#!/bin/bash
set -euo pipefail

# Clear the macOS privacy grants (TCC) of a development build so the next run
# asks for them again.
#
# Why: macOS keys Screen Recording, Accessibility and the other privacy
# grants to an app's code-signing designated requirement. An ad-hoc
# signature's requirement is the binary's own cdhash, so every ad-hoc rebuild
# invalidates the previous build's grants: System Settings still shows the
# switches on, but the new binary is denied, the app's own prompt comes back,
# and refreshing the permission state in the app changes nothing. Clearing
# the entries makes the state honest again: the stale switches disappear and
# the app asks once. A build signed with the "Isoform Dev" identity
# (dev-signing-identity.sh) or a Developer ID has a certificate-based
# requirement that never changes, so its grants stay valid; such a build needs
# a clear only once, when the grants were made for a differently signed build.
#
# Usage:
#   wails-gui/scripts/reset-dev-permissions.sh                                   # com.wails.Isoform
#   wails-gui/scripts/reset-dev-permissions.sh com.wails.Isoform
#   wails-gui/scripts/reset-dev-permissions.sh com.wails.Isoform build/bin/Isoform.app
#   wails-gui/scripts/reset-dev-permissions.sh --if-signature-changed com.wails.Isoform build/bin/Isoform.app
#   wails-gui/scripts/reset-dev-permissions.sh --dry-run [...]                   # report only, change nothing
#   wails-gui/scripts/reset-dev-permissions.sh --forget                          # drop the record below, clear nothing
#
# Options may appear anywhere; at most two positional arguments are accepted.
#
# The record ~/.isoform-agent/dev-signing/privacy-grants-requirement holds the
# designated requirement of the identity-signed build whose grants were last
# cleared (and so, from then on, granted). It is per user, like the grants,
# not per checkout, so builds from several worktrees share it.
# --if-signature-changed, used by build-wails.sh --flavor isoform and the
# wails dev post-build hook (sign-dev-build.sh) after an identity-signed
# build, clears only when the build's requirement differs from the record
# and then updates it. A plain clear, used after ad-hoc builds, drops the
# record: whatever gets granted next is keyed to a signature this script
# cannot predict. --forget drops it without clearing; every path that leaves
# a dev bundle with a different signature behind (an ad-hoc build kept with
# --keep-permissions, a dev run without the identity, a hook that could not
# sign) uses it, so an identity build after such a bundle clears once again.
#
# tccutil resolves the bundle id through Launch Services, which only knows a
# bundle that has been opened or registered. Pass the freshly built .app as
# the second argument and it is registered first (lsregister), so a build
# that was never opened can still be cleared.
#
# Only the development identity is accepted (the bundle id must contain
# "Isoform") so a slip can never wipe an installed Yansu.app's grants. Set
# YANSU_PERMISSION_RESET_ANY=1 to clear another identifier on purpose.
# Pass --keep-permissions to build-wails.sh or start-wails-gui.sh to skip the
# clear after a build.

LOG_PREFIX="[reset-dev-permissions]"
MARKER="${HOME}/.isoform-agent/dev-signing/privacy-grants-requirement"
LSREGISTER=/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister

usage() {
    echo "usage: $0 [--if-signature-changed] [--dry-run] [--forget] [bundle-id] [app-path]" >&2
    exit 2
}

IF_CHANGED=false
DRY_RUN=false
FORGET=false
POSITIONAL=()
for arg in "$@"; do
    case "$arg" in
        --if-signature-changed) IF_CHANGED=true ;;
        --dry-run) DRY_RUN=true ;;
        --forget) FORGET=true ;;
        --*) usage ;;
        *) POSITIONAL+=("$arg") ;;
    esac
done
if [ "${#POSITIONAL[@]}" -gt 2 ]; then
    usage
fi
BUNDLE_ID="${POSITIONAL[0]:-com.wails.Isoform}"
APP_PATH="${POSITIONAL[1]:-}"

if [ "$(uname -s)" != "Darwin" ]; then
    exit 0
fi

forget_record() {
    if [ -f "$MARKER" ]; then
        if [ "$DRY_RUN" = true ]; then
            echo "${LOG_PREFIX} dry run: would drop the record of which signature the grants were made for (${MARKER})"
        else
            rm -f "$MARKER"
            echo "${LOG_PREFIX} dropped the record of which signature the grants were made for; the next identity-signed build clears them once"
        fi
    fi
}

if [ "$FORGET" = true ]; then
    forget_record
    exit 0
fi

if ! command -v tccutil >/dev/null 2>&1; then
    echo "${LOG_PREFIX} tccutil not found; nothing cleared" >&2
    exit 1
fi
case "$BUNDLE_ID" in
    *Isoform*) ;;
    *)
        if [ "${YANSU_PERMISSION_RESET_ANY:-}" != "1" ]; then
            echo "${LOG_PREFIX} refusing to clear grants for '${BUNDLE_ID}': only the Isoform development identity is cleared (set YANSU_PERMISSION_RESET_ANY=1 to override)" >&2
            exit 2
        fi
        ;;
esac

# designated_requirement <bundle> prints the bundle's designated requirement,
# the thing macOS keys the grants to, or nothing when it is unsigned. codesign
# prefixes an ad-hoc (implicit) requirement with "# ".
designated_requirement() {
    codesign -d -r- "$1" 2>&1 | sed -n 's/^\(# \)\{0,1\}designated => //p' || true
}

REQUIREMENT=""
if [ -n "$APP_PATH" ] && [ -d "$APP_PATH" ]; then
    REQUIREMENT="$(designated_requirement "$APP_PATH")"
    if [ -z "$REQUIREMENT" ]; then
        echo "${LOG_PREFIX} ${APP_PATH} is not signed, or codesign could not read it" >&2
    fi
fi

if [ "$IF_CHANGED" = true ]; then
    if [ -z "$REQUIREMENT" ]; then
        echo "${LOG_PREFIX} --if-signature-changed needs a signed .app as the second argument" >&2
        exit 2
    fi
    PREVIOUS="$(cat "$MARKER" 2>/dev/null || true)"
    if [ "$PREVIOUS" = "$REQUIREMENT" ]; then
        echo "${LOG_PREFIX} ${BUNDLE_ID}: designated requirement unchanged (${REQUIREMENT}); the grants still apply, nothing cleared"
        exit 0
    fi
    if [ -n "$PREVIOUS" ]; then
        echo "${LOG_PREFIX} ${BUNDLE_ID}: designated requirement changed from ${PREVIOUS} to ${REQUIREMENT}; the grants were made for the old one and no longer apply, so they are cleared"
    else
        echo "${LOG_PREFIX} ${BUNDLE_ID}: no record of which signature the grants were made for; clearing them once so the app asks again for ${REQUIREMENT}"
    fi
fi

if [ "$DRY_RUN" = false ] && [ -n "$APP_PATH" ] && [ -d "$APP_PATH" ] && [ -x "$LSREGISTER" ]; then
    if "$LSREGISTER" -f "$APP_PATH" >/dev/null 2>&1; then
        echo "${LOG_PREFIX} registered ${APP_PATH} with Launch Services"
    else
        echo "${LOG_PREFIX} could not register ${APP_PATH} with Launch Services; clearing by bundle id only" >&2
    fi
fi

# The services the desktop asks for (wails-gui/permissions_darwin.go and the
# QuickChat hotkey): screen capture, accessibility (the activity walker and
# event taps), microphone and system audio (background listening), Apple
# events (the Messages text backup), contacts, and input monitoring.
SERVICES=(ScreenCapture Accessibility Microphone AudioCapture AppleEvents AddressBook ListenEvent)

if [ "$DRY_RUN" = true ]; then
    echo "${LOG_PREFIX} dry run: would clear ${SERVICES[*]} for ${BUNDLE_ID}"
    if [ "$IF_CHANGED" = true ]; then
        echo "${LOG_PREFIX} dry run: would record ${REQUIREMENT} in ${MARKER}"
    else
        forget_record
    fi
    exit 0
fi

status=0
cleared=0
unregistered=0
for service in "${SERVICES[@]}"; do
    if output=$(tccutil reset "$service" "$BUNDLE_ID" 2>&1); then
        echo "${LOG_PREFIX} cleared ${service} for ${BUNDLE_ID}"
        cleared=$((cleared + 1))
        continue
    fi
    case "$output" in
        *"No such bundle identifier"*)
            # Launch Services does not know the identifier (the build was
            # never opened and no .app was given), so there is nothing to
            # clear for this service.
            unregistered=$((unregistered + 1))
            ;;
        *)
            echo "${LOG_PREFIX} ${service}: ${output}" >&2
            status=1
            ;;
    esac
done

if [ "$unregistered" -gt 0 ]; then
    echo "${LOG_PREFIX} ${BUNDLE_ID} is not registered with Launch Services for ${unregistered} of ${#SERVICES[@]} services; open the build once (or pass its .app path) and run this again"
fi
if [ "$status" -eq 0 ] && [ "$cleared" -gt 0 ]; then
    echo "${LOG_PREFIX} ${BUNDLE_ID} will ask for its permissions again on the next launch"
fi

# The record (see the header): kept only for a successful clear on behalf of
# an identity-signed Isoform build; dropped after any other clear.
case "$BUNDLE_ID" in
    *Isoform*)
        if [ "$IF_CHANGED" = true ]; then
            if [ "$status" -eq 0 ] && [ "$cleared" -gt 0 ]; then
                if [ ! -d "$(dirname "$MARKER")" ]; then
                    mkdir -p "$(dirname "$MARKER")"
                    chmod 700 "$(dirname "$MARKER")"
                fi
                printf '%s' "$REQUIREMENT" > "$MARKER"
            else
                forget_record
            fi
        else
            forget_record
        fi
        ;;
esac
exit "$status"
