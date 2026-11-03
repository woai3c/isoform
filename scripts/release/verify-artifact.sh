#!/usr/bin/env bash
# Verify a signed distribution without launching its app.
# ISOFORM_EXPECT_TELEMETRY=on requires the PostHog key and host in the binary
# (and, when ISOFORM_DESKTOP_POSTHOG_* are set, exactly those values); off, the
# default, requires neither. The key is never printed.
set -euo pipefail
if [[ $# != 7 ]]; then
    echo 'Usage: verify-artifact.sh DMG ARCHIVE APP_NOTARY_ID DMG_NOTARY_ID arm64|x64 PROFILE VERSION' >&2
    exit 2
fi
dmg="$1" archive="$2" app_id="$3" dmg_id="$4" arch="$5" profile="$6" version="$7"
APPLE_NOTARY_KEYCHAIN_PROFILE="$profile"
# shellcheck source=notary-credentials.sh
. "$(dirname "$0")/notary-credentials.sh"
notary_credentials_available
case "$arch" in arm64) expected=arm64 ;; x64) expected=x86_64 ;; *) exit 2 ;; esac
tmp="$(mktemp -d "${TMPDIR:-/tmp}/isoform-verify.XXXXXXXX")"
mounted=0
cleanup() {
    if [[ $mounted == 1 ]]; then hdiutil detach "$tmp/mount" >/dev/null || return 1; fi
    rm -rf -- "$tmp"
}
trap cleanup EXIT
mkdir -p "$tmp/mount" "$tmp/extract"
hdiutil attach -readonly -nobrowse -mountpoint "$tmp/mount" "$dmg"
mounted=1
app="$tmp/mount/Isoform.app"
test -d "$app"
plist="$app/Contents/Info.plist"
[[ "$(/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' "$plist")" == com.wails.Isoform ]]
[[ "$(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "$plist")" == "${version%%-*}" ]]
[[ "$(/usr/libexec/PlistBuddy -c 'Print :IsoformReleaseVersion' "$plist")" == "$version" ]]
[[ "$(/usr/libexec/PlistBuddy -c 'Print :CFBundleVersion' "$plist")" =~ ^[0-9]+$ ]]
for key in NSMicrophoneUsageDescription NSAudioCaptureUsageDescription NSAppleEventsUsageDescription NSContactsUsageDescription NSCalendarsFullAccessUsageDescription NSCalendarsUsageDescription NSRemindersFullAccessUsageDescription NSRemindersUsageDescription; do
    [[ -n "$(/usr/libexec/PlistBuddy -c "Print :$key" "$plist")" ]]
done
go version -m "$app/Contents/MacOS/Yansu" > "$tmp/build-info"
# The linker flags carry the PostHog ingest key; log them with it redacted.
sed -E 's/(main\.DefaultDesktopPostHogKey=)[^ "]*/\1[redacted]/g' "$tmp/build-info"
grep -qF -- '-X main.buildFlavor=isoform' "$tmp/build-info"
grep -qF -- '-X main.buildChannel=release' "$tmp/build-info"
grep -qF -- "-X main.Version=$version" "$tmp/build-info"
case "${ISOFORM_EXPECT_TELEMETRY:-off}" in
    on)
        grep -qE -- '-X main\.DefaultDesktopPostHogKey=[^ "]+' "$tmp/build-info" || { echo 'Telemetry: the binary has no PostHog key' >&2; exit 1; }
        grep -qE -- '-X main\.DefaultDesktopPostHogHost=https://[^ "]+' "$tmp/build-info" || { echo 'Telemetry: the binary has no PostHog host' >&2; exit 1; }
        # Builtin pattern matching keeps the key out of every process's arguments.
        build_info="$(< "$tmp/build-info")"
        if [[ -n "${ISOFORM_DESKTOP_POSTHOG_KEY:-}" && "$build_info" != *"-X main.DefaultDesktopPostHogKey=${ISOFORM_DESKTOP_POSTHOG_KEY}"[\ \"]* ]]; then
            echo 'Telemetry: the PostHog key in the binary differs from the release settings' >&2
            exit 1
        fi
        if [[ -n "${ISOFORM_DESKTOP_POSTHOG_HOST:-}" && "$build_info" != *"-X main.DefaultDesktopPostHogHost=${ISOFORM_DESKTOP_POSTHOG_HOST}"[\ \"]* ]]; then
            echo 'Telemetry: the PostHog host in the binary differs from the release settings' >&2
            exit 1
        fi
        unset build_info
        echo 'Telemetry: PostHog key and host present'
        ;;
    off)
        if grep -qF -- 'main.DefaultDesktopPostHog' "$tmp/build-info"; then
            echo 'Telemetry: expected a build without PostHog settings' >&2
            exit 1
        fi
        echo 'Telemetry: off'
        ;;
    *) echo 'ISOFORM_EXPECT_TELEMETRY must be on or off' >&2; exit 2 ;;
esac
codesign --verify --deep --strict --verbose=2 "$app"
codesign --verify --deep --strict --verbose=2 "$dmg"
codesign -d -r- "$app" > "$tmp/requirement" 2>&1
cat "$tmp/requirement"
grep -F 'certificate leaf[field.1.2.840.113635.100.6.1.13]' "$tmp/requirement"
: "${APPLE_TEAM_ID:?Set APPLE_TEAM_ID to the expected signing team}"
grep -F -- "$APPLE_TEAM_ID" "$tmp/requirement"
spctl -a -vvv -t exec "$app" 2> "$tmp/app-assessment"
cat "$tmp/app-assessment"
grep -F 'source=Notarized Developer ID' "$tmp/app-assessment"
spctl -a -vvv -t open --context context:primary-signature "$dmg" 2> "$tmp/dmg-assessment"
cat "$tmp/dmg-assessment"
grep -F 'source=Notarized Developer ID' "$tmp/dmg-assessment"
xcrun stapler validate "$app"
xcrun stapler validate "$dmg"
for id in "$app_id" "$dmg_id"; do
    notarytool_with_credentials log "$id" "$tmp/notary.json"
    cat "$tmp/notary.json"
    python3 - "$tmp/notary.json" <<'PY'
import json, sys
with open(sys.argv[1]) as f:
    result = json.load(f)
if result.get("status") != "Accepted" or result.get("issues"):
    raise SystemExit("Apple reported notary issues")
PY
done
count=0
while IFS= read -r -d '' item; do
    if file -b "$item" | grep -q 'Mach-O'; then
        actual="$(lipo -archs "$item")"
        echo "${item#"$app"/}: $actual"
        case " $actual " in *" $expected "*) ;; *) echo "Wrong architecture: $item" >&2; exit 1 ;; esac
        if [[ $arch == x64 && "$actual" != x86_64 ]]; then echo "Unexpected x64 slices: $item" >&2; exit 1; fi
        count=$((count+1))
    fi
done < <(find "$app/Contents" -type f -print0)
[[ $count -gt 0 ]]
for path in magpie-bundle/magpie-cli bun-bundle/bin/bun ffmpeg-bundle/bin/ffmpeg dumbpipe-bundle/dumbpipe yansu-cli-bundle/bin/yansu; do
    test -x "$app/Contents/Resources/$path"
    codesign --verify --strict --verbose=2 "$app/Contents/Resources/$path"
done
tar -xzf "$archive" -C "$tmp/extract"
codesign --verify --deep --strict --verbose=2 "$tmp/extract/Isoform.app"
xcrun stapler validate "$tmp/extract/Isoform.app"
spctl -a -vvv -t exec "$tmp/extract/Isoform.app" 2> "$tmp/archive-assessment"
cat "$tmp/archive-assessment"
grep -F 'source=Notarized Developer ID' "$tmp/archive-assessment"
shasum -a 256 "$dmg" "$archive"
