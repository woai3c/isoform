#!/usr/bin/env bash
# Build, sign, notarize and verify both architectures. Upload only with --publish.
# Stable builds embed the PostHog settings from posthog-env.sh's key file;
# --no-telemetry is the deliberate, recorded opt-out.
set -euo pipefail
root="$(cd "$(dirname "$0")/../.." && pwd)"
if [[ $# -lt 3 ]]; then echo 'Usage: ship.sh VERSION --channel stable|beta [--dry-run] [--publish] [--no-telemetry]' >&2; exit 2; fi
version="$1"; shift
channel="" publish=0 dry_run=0 telemetry=on
while [[ $# -gt 0 ]]; do
    case "$1" in
        --channel) [[ $# -ge 2 ]] || exit 2; channel="$2"; shift 2 ;;
        --publish) publish=1; shift ;;
        --dry-run) dry_run=1; shift ;;
        --no-telemetry) telemetry=off; shift ;;
        *) echo "Unknown argument: $1" >&2; exit 2 ;;
    esac
done
stable='(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)'
case "$channel" in
    stable) [[ "$version" =~ ^${stable}$ ]] || { echo 'Stable version must be X.Y.Z' >&2; exit 2; } ;;
    beta) [[ "$version" =~ ^${stable}-beta\.(0|[1-9][0-9]*)$ ]] || { echo 'Beta version must be X.Y.Z-beta.N' >&2; exit 2; } ;;
    *) echo 'Choose --channel stable or beta' >&2; exit 2 ;;
esac
if [[ $dry_run == 1 && $publish == 1 ]]; then echo '--dry-run and --publish cannot be combined' >&2; exit 2; fi
[[ $(uname -s) == Darwin ]] || { echo 'A macOS host is required' >&2; exit 1; }
[[ "$root" != "$HOME/Documents/"* ]] || { echo 'Build outside Documents' >&2; exit 1; }
# build-wails.sh sources wails-gui/.env.local over the environment, so a key
# there would silently replace the release project's.
if [[ -f "$root/wails-gui/.env.local" ]] && grep -qE '^[[:space:]]*(export[[:space:]]+)?(ISOFORM|YANSU)_DESKTOP_POSTHOG_' "$root/wails-gui/.env.local"; then
    echo 'Remove ISOFORM_DESKTOP_POSTHOG_* and YANSU_DESKTOP_POSTHOG_* from wails-gui/.env.local; release builds read them from the release key file' >&2
    exit 1
fi
# shellcheck source=posthog-env.sh
. "$root/scripts/release/posthog-env.sh"
if [[ $telemetry == off ]]; then
    unset ISOFORM_DESKTOP_POSTHOG_KEY ISOFORM_DESKTOP_POSTHOG_HOST YANSU_DESKTOP_POSTHOG_KEY YANSU_DESKTOP_POSTHOG_HOST
    echo 'Telemetry: off (--no-telemetry)'
elif [[ $channel == beta && ! -e "$release_posthog_env_file" ]]; then
    unset ISOFORM_DESKTOP_POSTHOG_KEY ISOFORM_DESKTOP_POSTHOG_HOST YANSU_DESKTOP_POSTHOG_KEY YANSU_DESKTOP_POSTHOG_HOST
    telemetry=off
    echo "Telemetry: off (beta without $release_posthog_env_file)" >&2
elif load_release_posthog_env; then
    echo "Telemetry: on (PostHog settings from $release_posthog_env_file)"
else
    echo "A stable release sends anonymous usage to PostHog. Save the ingest key and host in $release_posthog_env_file (mode 600), or pass --no-telemetry to build without telemetry on purpose. No release build was started." >&2
    exit 1
fi
export ISOFORM_EXPECT_TELEMETRY="$telemetry"
: "${APPLE_SIGNING_IDENTITY:?Set APPLE_SIGNING_IDENTITY to your Developer ID Application identity}"
: "${APPLE_TEAM_ID:?Set APPLE_TEAM_ID to the expected signing team}"
: "${APPLE_NOTARY_KEYCHAIN_PROFILE:=isoform-notary}"
export APPLE_SIGNING_IDENTITY APPLE_TEAM_ID APPLE_NOTARY_KEYCHAIN_PROFILE
# shellcheck source=notary-credentials.sh
. "$root/scripts/release/notary-credentials.sh"
if ! notary_credentials_available; then
    echo 'Notary credentials are unavailable; no release build was started.' >&2
    exit 1
fi
export APPLE_NOTARY_KEY_PATH APPLE_NOTARY_KEY_ID APPLE_NOTARY_ISSUER
out="${ISOFORM_RELEASE_OUTPUT_ROOT:-$HOME/isoform-codex/release-rehearsal/pipeline}/$version"
if [[ $publish == 1 && -d "$out" ]]; then
    # Recheck the exact reviewed artifacts. An incomplete rehearsal can never
    # reach publication, even if its output directory already exists.
    (cd "$out" && shasum -a 256 -c checksums.txt)
    built_telemetry="$(cat "$out/telemetry.txt" 2>/dev/null || echo off)"
    if [[ "$built_telemetry" != "$telemetry" ]]; then
        echo "The rehearsal in $out was built with telemetry $built_telemetry; this run expects $telemetry" >&2
        exit 1
    fi
    for label in arm64 x64; do
        log="$out/build-$label.log"
        [[ -f "$log" ]] || { echo "Missing build log: $log" >&2; exit 1; }
        app_id=$(sed -n 's/.*Isoform\.app: submitted, submission id \([0-9a-fA-F-]*\).*/\1/p' "$log" | tail -1)
        dmg_id=$(sed -n 's/.*DMG: submitted, submission id \([0-9a-fA-F-]*\).*/\1/p' "$log" | tail -1)
        [[ -n "$app_id" && -n "$dmg_id" ]] || { echo 'Missing notarization submission ID' >&2; exit 1; }
        verify_log=$(mktemp "$out/publish-verify-$label.XXXXXXXX.log")
        "$root/scripts/release/verify-artifact.sh" "$out/Isoform-$version-darwin-$label.dmg" "$out/Isoform-$version-darwin-$label.app.tar.gz" "$app_id" "$dmg_id" "$label" "$APPLE_NOTARY_KEYCHAIN_PROFILE" "$version" 2>&1 | tee "$verify_log"
    done
    "$root/scripts/release/publish-artifacts.sh" "$version" "$channel" "$out"
    exit 0
fi
git -C "$root" diff --quiet HEAD -- wails-gui/wails.json wails-gui/go.mod || { echo 'Use an isolated checkout with unmodified build configuration' >&2; exit 1; }
mkdir -p "$(dirname "$out")"
mkdir "$out" || { echo "Refusing to overwrite $out" >&2; exit 1; }
cp "$root/wails-gui/wails.json" "$out/original-wails.json"
cp "$root/wails-gui/go.mod" "$out/original-go.mod"
echo "$telemetry" > "$out/telemetry.txt"
restore_build_configuration() {
    cp "$out/original-wails.json" "$root/wails-gui/wails.json"
    cp "$out/original-go.mod" "$root/wails-gui/go.mod"
}
trap restore_build_configuration EXIT
cd "$root"
for spec in 'arm64 arm64' 'amd64 x64'; do
    read -r target label <<< "$spec"
    log="$out/build-$label.log"
    /usr/bin/time -p ./wails-gui/scripts/build-wails.sh --platform "darwin/$target" --version "$version" --dmg 2>&1 | tee "$log"
    dmg="$root/wails-gui/build/bin/Isoform-$version-darwin-$label.dmg"
    archive="$root/wails-gui/build/bin/Isoform-$version-darwin-$label.app.tar.gz"
    cp -p "$dmg" "$archive" "$out/"
    app_id=$(sed -n 's/.*Isoform\.app: submitted, submission id \([0-9a-fA-F-]*\).*/\1/p' "$log" | tail -1)
    dmg_id=$(sed -n 's/.*DMG: submitted, submission id \([0-9a-fA-F-]*\).*/\1/p' "$log" | tail -1)
    [[ -n "$app_id" && -n "$dmg_id" ]] || { echo 'Missing notarization submission ID' >&2; exit 1; }
    ./scripts/release/verify-artifact.sh "$out/$(basename "$dmg")" "$out/$(basename "$archive")" "$app_id" "$dmg_id" "$label" "$APPLE_NOTARY_KEYCHAIN_PROFILE" "$version" 2>&1 | tee "$out/verify-$label.log"
    restore_build_configuration
done
printf 'Isoform %s %s\n' "$version" "$channel" > "$out/notes.txt"
python_cmd=python3
if command -v python3.12 >/dev/null 2>&1; then python_cmd=python3.12; fi
"$python_cmd" .github/scripts/desktop_release.py manifest --version "$version" --repository yubozhao/isoform-releases --channel "$channel" --notes "$out/notes.txt" --artifacts "$out"
(cd "$out" && shasum -a 256 -c checksums.txt)
if [[ $publish == 1 ]]; then
    ./scripts/release/publish-artifacts.sh "$version" "$channel" "$out"
else
    echo "Local rehearsal only: $out"
fi
