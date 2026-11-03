#!/usr/bin/env bash
# Installs a signed Debug build of the Yansu iPhone app on a paired physical iPhone
# over the local network, using devicectl. It never uninstalls the app or resets its data.
#
# Prerequisites. This repository provisions none of them:
#   - The iPhone is paired and trusted in Xcode Device Hub, has Developer Mode on,
#     and is connected over the local network. Wired and unrecognized transports are rejected.
#   - An Apple Developer team is selected in Xcode for signing (YANSU_IOS_TEAM_ID).
#
# Usage:
#   install-ios-wireless.sh check <iPhone-UDID-or-CoreDevice-ID> [--personal-team]
#   install-ios-wireless.sh install <iPhone-UDID-or-CoreDevice-ID> [--personal-team]
#
# Environment:
#   YANSU_IOS_TEAM_ID       the selected 10-character Apple team identifier (required)
#   YANSU_IOS_BUILD_NUMBER  numeric build number (default: UTC timestamp)
#
# Build records are kept in ios/.build-wireless/<build number>/, which git ignores.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
OUTPUT_ROOT="$REPO_ROOT/ios/.build-wireless"
ACTION="${1:-}"
DEVICE_ID="${2:-}"
TEAM_ID="${YANSU_IOS_TEAM_ID:-}"
BUILD_NUMBER="${YANSU_IOS_BUILD_NUMBER:-}"
PERSONAL_TEAM=false
BUNDLE_ID=com.isoform.companion

fail() {
    echo "$*" >&2
    exit 2
}

usage() {
    echo "Usage: $0 check|install <paired-iPhone-UDID-or-CoreDevice-ID> [--personal-team]" >&2
    echo "Set YANSU_IOS_TEAM_ID to the selected Xcode signing team (your enrolled Apple Developer team)." >&2
    echo "The iPhone must be paired and trusted in Xcode Device Hub and connected over the local network." >&2
    exit 2
}

case "$ACTION" in
    check|install) ;;
    *) usage ;;
esac
[[ $# -eq 2 || $# -eq 3 ]] || usage
if [[ $# -eq 3 ]]; then
    [[ "$3" == --personal-team ]] || usage
    PERSONAL_TEAM=true
fi
[[ "$DEVICE_ID" =~ ^[A-Fa-f0-9]{8}-[A-Fa-f0-9]{16}$ ||
   "$DEVICE_ID" =~ ^[A-Fa-f0-9]{40}$ ||
   "$DEVICE_ID" =~ ^[A-Fa-f0-9]{8}-[A-Fa-f0-9]{4}-[A-Fa-f0-9]{4}-[A-Fa-f0-9]{4}-[A-Fa-f0-9]{12}$ ]] ||
    fail "Use the exact iPhone UDID or CoreDevice ID; device names and automatic selection are not supported."
[[ "$TEAM_ID" =~ ^[A-Z0-9]{10}$ ]] || fail "YANSU_IOS_TEAM_ID must be the selected 10-character Apple team identifier."
if [[ -n "$BUILD_NUMBER" && ! "$BUILD_NUMBER" =~ ^[1-9][0-9]{0,17}$ ]]; then
    fail "YANSU_IOS_BUILD_NUMBER must contain 1 through 18 digits, with no leading zero."
fi
for tool in xcodebuild xcrun python3; do
    command -v "$tool" >/dev/null 2>&1 || fail "Install Xcode and Python 3 before using this script."
done
xcrun --find devicectl >/dev/null 2>&1 || fail "Select an Xcode installation that includes devicectl."
[[ ! -L "$OUTPUT_ROOT" ]] || fail "The wireless build directory must not be a symbolic link."
if [[ -e "$OUTPUT_ROOT" ]]; then
    [[ -d "$OUTPUT_ROOT" && "$(stat -f '%u' "$OUTPUT_ROOT")" == "$(id -u)" ]] ||
        fail "The wireless build directory must be owned by the current user."
fi

inspect_device() {
    local inventory
    inventory="$(xcrun devicectl list devices --filter "identifier ==[c] '$DEVICE_ID' OR properties.hardware.udid ==[c] '$DEVICE_ID'" --quiet --timeout 20 --omit-deprecated-fields-in-json --json-output -)" ||
        fail "Could not read paired devices from Xcode."
    YANSU_DEVICE_INVENTORY="$inventory" python3 - "$DEVICE_ID" <<'PY_DEVICE'
import json, os, re, sys

def fail(message):
    sys.exit(message)

try:
    devices = json.loads(os.environ["YANSU_DEVICE_INVENTORY"])["result"]["devices"]
    selected = [device for device in devices if sys.argv[1].lower() in (
        str(device.get("identifier", "")).lower(),
        str(device.get("properties", {}).get("hardware", {}).get("udid", "")).lower())]
    if len(selected) != 1:
        fail("The exact iPhone is not available in Xcode. Complete pairing in Device Hub first.")
    properties = selected[0]["properties"]
    hardware, connection = properties["hardware"], properties["connection"]
    if (hardware.get("deviceType"), hardware.get("platform"), hardware.get("reality")) != ("iPhone", "iOS", "physical"):
        fail("Select a physical iPhone; simulators and other devices are not supported.")
    if connection.get("pairingState") != "paired" or connection.get("state") != "connected":
        fail("Unlock the paired iPhone and make it available in Device Hub before continuing.")
    # These are the current Apple CoreDevice transport values. Unknown values
    # fail closed, and wired/cloud/sameMachine cannot be treated as wireless.
    if connection.get("transportType") != "localNetwork":
        fail("The iPhone must be connected over the local network; wired or unrecognized transports are rejected.")
    udid = hardware["udid"]
    if not isinstance(udid, str) or not re.fullmatch(r"[A-Fa-f0-9]{8}-[A-Fa-f0-9]{16}|[A-Fa-f0-9]{40}", udid):
        fail("Xcode did not report a recognized physical iPhone UDID.")
    print(udid)
except (KeyError, TypeError, ValueError, AttributeError):
    fail("Xcode returned an unrecognized device schema; no build or installation was started.")
PY_DEVICE
}

DEVICE_UDID="$(inspect_device)"
if [[ "$ACTION" == check ]]; then
    echo "The selected physical iPhone is paired and connected over the local network. No signing or installation was started."
    exit 0
fi

BUILD_NUMBER="${BUILD_NUMBER:-$(date -u +%Y%m%d%H%M%S)}"
RUN_ROOT="$OUTPUT_ROOT/$BUILD_NUMBER"
[[ ! -e "$RUN_ROOT" && ! -L "$RUN_ROOT" ]] || fail "That build directory already exists. Choose a new YANSU_IOS_BUILD_NUMBER; previous outputs are never overwritten."
umask 077
mkdir -p "$OUTPUT_ROOT"
chmod 700 "$OUTPUT_ROOT"
mkdir "$RUN_ROOT"
BUILD_PROJECT="$REPO_ROOT/ios/Yansu.xcodeproj"
SIGNING_ARGS=(CODE_SIGNING_ALLOWED=YES CODE_SIGN_STYLE=Automatic CODE_SIGN_IDENTITY="Apple Development" "DEVELOPMENT_TEAM=$TEAM_ID")
if [[ "$PERSONAL_TEAM" == true ]]; then
    # Personal Teams cannot provision Push Notifications. Remove that capability
    # only from a disposable project; the checked-in Release target is unchanged.
    BUILD_PROJECT="$RUN_ROOT/Yansu.xcodeproj"
    mkdir -p "$BUILD_PROJECT/xcshareddata/xcschemes"
    plutil -convert xml1 -o "$BUILD_PROJECT/project.pbxproj" "$REPO_ROOT/ios/Yansu.xcodeproj/project.pbxproj"
    /usr/libexec/PlistBuddy -c 'Delete :objects:A80000000000000000000001:attributes:TargetAttributes:A70000000000000000000001:SystemCapabilities:com.apple.Push' "$BUILD_PROJECT/project.pbxproj"
    plutil -replace objects.A50000000000000000000002.path -string "$REPO_ROOT/ios/Yansu" "$BUILD_PROJECT/project.pbxproj"
    plutil -replace objects.A50000000000000000000002.sourceTree -string '<absolute>' "$BUILD_PROJECT/project.pbxproj"
    cp "$REPO_ROOT/ios/Yansu.xcodeproj/xcshareddata/xcschemes/Yansu.xcscheme" "$BUILD_PROJECT/xcshareddata/xcschemes/Yansu.xcscheme"
    cat > "$RUN_ROOT/PersonalTeam.entitlements" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict/></plist>
PLIST
    SIGNING_ARGS+=("CODE_SIGN_ENTITLEMENTS=$RUN_ROOT/PersonalTeam.entitlements" "INFOPLIST_FILE=$REPO_ROOT/ios/Yansu/Info.plist" YANSU_APNS_ENVIRONMENT=disabled)
fi

echo "Building a signed Debug iPhone app. Xcode may update provisioning and register only the selected destination for this team."
xcodebuild \
    -project "$BUILD_PROJECT" \
    -scheme Yansu \
    -configuration Debug \
    -sdk iphoneos \
    -destination "platform=iOS,id=$DEVICE_UDID" \
    -destination-timeout 20 \
    -derivedDataPath "$RUN_ROOT/DerivedData" \
    -allowProvisioningUpdates \
    -allowProvisioningDeviceRegistration \
    "CURRENT_PROJECT_VERSION=$BUILD_NUMBER" \
    "${SIGNING_ARGS[@]}" \
    build

APP_PATH="$RUN_ROOT/DerivedData/Build/Products/Debug-iphoneos/Yansu.app"
[[ -d "$APP_PATH" && -f "$APP_PATH/PrivacyInfo.xcprivacy" ]] || fail "The signed iPhone app or its privacy manifest is missing."
/usr/bin/codesign --verify --strict "$APP_PATH" >/dev/null 2>&1 || fail "The built app's signature is invalid; nothing was installed."
SIGNED_TEAM="$(/usr/bin/codesign -d --verbose=4 "$APP_PATH" 2>&1 | sed -n 's/^TeamIdentifier=//p')"
[[ "$SIGNED_TEAM" == "$TEAM_ID" ]] || fail "The built app was signed by a different team; nothing was installed."
/usr/bin/codesign -d --entitlements - "$APP_PATH" > "$RUN_ROOT/Signed.entitlements" 2> "$RUN_ROOT/codesign-entitlements.log"
/usr/bin/security cms -D -i "$APP_PATH/embedded.mobileprovision" > "$RUN_ROOT/Provisioning.plist" 2> "$RUN_ROOT/provisioning.log"
python3 - "$APP_PATH" "$RUN_ROOT" "$TEAM_ID" "$DEVICE_UDID" "$BUILD_NUMBER" "$PERSONAL_TEAM" "$BUNDLE_ID" <<'PY_APP'
import datetime, pathlib, plistlib, sys

app, run = map(pathlib.Path, sys.argv[1:3])
team, udid, build, personal, bundle = sys.argv[3:]
def load(path):
    with path.open("rb") as source:
        return plistlib.load(source)
def require(condition, message):
    if not condition:
        sys.exit(message + "; nothing was installed.")

info = load(app / "Info.plist")
entitlements = load(run / "Signed.entitlements")
profile = load(run / "Provisioning.plist")
require(info.get("CFBundleIdentifier") == bundle, "The built bundle identifier is incorrect")
require(info.get("CFBundleSupportedPlatforms") == ["iPhoneOS"], "The build is not a physical iPhone app")
require(info.get("CFBundleVersion") == build, "The build identity changed")
require(team in profile.get("TeamIdentifier", []), "The provisioning profile belongs to a different team")
require(udid.lower() in [value.lower() for value in profile.get("ProvisionedDevices", [])], "The profile does not include the exact selected iPhone")
require(profile.get("Entitlements", {}).get("get-task-allow") is True, "The profile is not for development")
require(entitlements.get("com.apple.developer.team-identifier") == team, "The signed entitlement team is incorrect")
require(entitlements.get("application-identifier", "").endswith("." + bundle), "The signed app identity is incorrect")
require(isinstance(profile.get("ExpirationDate"), datetime.datetime) and profile["ExpirationDate"] > datetime.datetime.now(datetime.timezone.utc).replace(tzinfo=None), "The provisioning profile is expired")
if personal == "true":
    require("aps-environment" not in entitlements and "aps-environment" not in profile.get("Entitlements", {}), "Personal Team builds must omit the push entitlement")
    require(info.get("YansuAPNSEnvironment") == "disabled", "Personal Team remote push must be disabled")
else:
    require(entitlements.get("aps-environment") == "development" and info.get("YansuAPNSEnvironment") == "development", "The developer push environment is incorrect")
print("Signed app, exact-device development profile, and push configuration verified.")
PY_APP

[[ "$(inspect_device)" == "$DEVICE_UDID" ]] || fail "The selected iPhone changed before installation."
xcrun devicectl device install app --device "$DEVICE_UDID" --quiet --timeout 60 --json-output "$RUN_ROOT/install.json" "$APP_PATH"
[[ "$(inspect_device)" == "$DEVICE_UDID" ]] || fail "The selected iPhone changed before launch. The app was installed but has not been launched."
xcrun devicectl device process launch --device "$DEVICE_UDID" --quiet --timeout 60 --json-output "$RUN_ROOT/launch.json" "$BUNDLE_ID"
echo "Yansu installation and launch commands succeeded. Build records: $RUN_ROOT"
