#!/bin/bash
set -euo pipefail

# Keep one registered copy of a development build in Launch Services: the one
# just built.
#
# Why: every Isoform.app that was ever opened (each worktree's build/bin, a
# scratch copy in a temp folder) stays registered under the same bundle id,
# com.wails.Isoform, even after it is deleted. macOS opens an app by bundle id
# through Launch Services: Screen Recording's "Quit & Reopen" quits the app
# and asks for com.wails.Isoform again, and Launch Services can pick an old
# copy, or one that no longer exists, so the app the user was setting up
# never comes back.
#
# Usage:
#   wails-gui/scripts/unregister-stale-dev-builds.sh com.wails.Isoform build/bin/Isoform.app
#   wails-gui/scripts/unregister-stale-dev-builds.sh --dry-run com.wails.Isoform build/bin/Isoform.app
#
# The new build is registered, then every other registered copy of the bundle
# id is unregistered when it is a build output (.../build/bin/*.app) or sits
# in a temporary folder (/tmp, /private/tmp, /var/folders). Any other
# copy is left registered and named in the log; copies under /Applications or
# ~/Applications are never touched. Only bundle ids containing "Isoform" are
# accepted, so an installed Yansu.app is never affected.

LOG_PREFIX="[unregister-stale-dev-builds]"
LSREGISTER=/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister

usage() {
    echo "usage: $0 [--dry-run] <bundle-id> <app-path>" >&2
    exit 2
}

DRY_RUN=false
POSITIONAL=()
for arg in "$@"; do
    case "$arg" in
        --dry-run) DRY_RUN=true ;;
        --*) usage ;;
        *) POSITIONAL+=("$arg") ;;
    esac
done
if [ "${#POSITIONAL[@]}" -ne 2 ]; then
    usage
fi
BUNDLE_ID="${POSITIONAL[0]}"
APP_PATH="${POSITIONAL[1]}"

if [ "$(uname -s)" != "Darwin" ]; then
    exit 0
fi
case "$BUNDLE_ID" in
    *Isoform*) ;;
    *)
        echo "${LOG_PREFIX} refusing '${BUNDLE_ID}': only the Isoform development identity is handled" >&2
        exit 2
        ;;
esac
if [ ! -x "$LSREGISTER" ]; then
    echo "${LOG_PREFIX} lsregister not found; nothing changed" >&2
    exit 1
fi
if [ ! -d "$APP_PATH" ]; then
    echo "${LOG_PREFIX} ${APP_PATH} does not exist" >&2
    exit 1
fi
# Launch Services records resolved paths (/tmp is /private/tmp).
APP_PATH="$(cd "$APP_PATH" && pwd -P)"

if [ "$DRY_RUN" = true ]; then
    echo "${LOG_PREFIX} dry run: would register ${APP_PATH}"
elif "$LSREGISTER" -f "$APP_PATH" >/dev/null 2>&1; then
    echo "${LOG_PREFIX} registered ${APP_PATH}"
else
    echo "${LOG_PREFIX} could not register ${APP_PATH}" >&2
fi

# registered_copies prints the path of every registered bundle whose
# identifier is $BUNDLE_ID, one per line. A dump record has a "path:" line
# (with a trailing " (0x...)" id) and an "identifier:" line; records are
# separated by lines of dashes.
registered_copies() {
    "$LSREGISTER" -dump 2>/dev/null | awk -v id="$BUNDLE_ID" '
        /^-+$/ { path = "" }
        /^path:/ {
            sub(/^path:[ \t]+/, "")
            sub(/ \(0x[0-9a-fA-F]+\)$/, "")
            path = $0
        }
        /^identifier:/ { if ($2 == id && path != "") print path }
    ' | sort -u
}

unregistered=0
kept=0
while IFS= read -r copy; do
    [ -n "$copy" ] || continue
    [ "$copy" = "$APP_PATH" ] && continue
    case "$copy" in
        /Applications/*|"$HOME"/Applications/*)
            echo "${LOG_PREFIX} left ${copy} registered: installed apps are never touched"
            kept=$((kept + 1))
            continue
            ;;
        # Fixed temporary roots only ($TMPDIR lives under /var/folders): a
        # stray TMPDIR such as / must never widen what is unregistered.
        */build/bin/*.app|/tmp/*|/private/tmp/*|/var/folders/*|/private/var/folders/*) ;;
        *)
            echo "${LOG_PREFIX} left ${copy} registered: not a build output or a temporary copy"
            kept=$((kept + 1))
            continue
            ;;
    esac
    state="still on disk"
    [ -d "$copy" ] || state="no longer on disk"
    if [ "$DRY_RUN" = true ]; then
        echo "${LOG_PREFIX} dry run: would unregister ${copy} (${state})"
        unregistered=$((unregistered + 1))
    elif "$LSREGISTER" -u "$copy" >/dev/null 2>&1; then
        echo "${LOG_PREFIX} unregistered ${copy} (${state})"
        unregistered=$((unregistered + 1))
    else
        echo "${LOG_PREFIX} could not unregister ${copy}" >&2
    fi
done < <(registered_copies)

echo "${LOG_PREFIX} ${BUNDLE_ID}: ${unregistered} other copies unregistered, ${kept} left registered"
