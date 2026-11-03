#!/usr/bin/env bash
# Builds the Yansu iPhone app for the iOS Simulator and prints the path of Yansu.app.
#
# Environment (all optional):
#   YANSU_IOS_CONFIGURATION  Debug (default) or Release
#   YANSU_IOS_DESTINATION    xcodebuild destination (default: generic iOS Simulator)
#   YANSU_IOS_BUILD_DIR      derived data directory (default: ios/.build)
#
# This script only builds. To run tests, use scripts/test-ios-compatibility.sh.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
CONFIGURATION="${YANSU_IOS_CONFIGURATION:-Debug}"
DESTINATION="${YANSU_IOS_DESTINATION:-generic/platform=iOS Simulator}"
BUILD_DIR="${YANSU_IOS_BUILD_DIR:-$REPO_ROOT/ios/.build}"

if [[ $# -ne 0 ]]; then
    echo "Usage: $0" >&2
    echo "Set YANSU_IOS_DESTINATION, for example 'platform=iOS Simulator,name=iPhone 15,OS=17.4', to choose a simulator." >&2
    echo "This script only builds. To run tests, use scripts/test-ios-compatibility.sh." >&2
    exit 2
fi
case "$CONFIGURATION" in
    Debug | Release) ;;
    *)
        echo "YANSU_IOS_CONFIGURATION must be Debug or Release." >&2
        exit 2
        ;;
esac
if ! command -v xcodebuild >/dev/null 2>&1; then
    echo "Install Xcode and select its developer directory before building the iPhone app." >&2
    exit 1
fi

# Simulator Keychain access needs Xcode's generated application identity.
# Ad-hoc signing supplies it without an Apple team or provisioning profile.
# -quiet keeps the log short. Errors still print, and xcodebuild exits non-zero.
xcodebuild \
    -quiet \
    -project "$REPO_ROOT/ios/Yansu.xcodeproj" \
    -scheme Yansu \
    -configuration "$CONFIGURATION" \
    -destination "$DESTINATION" \
    -derivedDataPath "$BUILD_DIR" \
    CODE_SIGNING_ALLOWED=YES \
    CODE_SIGN_IDENTITY=- \
    build

PRODUCTS_DIR="$BUILD_DIR/Build/Products/$CONFIGURATION-iphonesimulator"
if [[ ! -d "$PRODUCTS_DIR/Yansu.app" ]]; then
    echo "The build finished, but Yansu.app was not found in $PRODUCTS_DIR." >&2
    echo "This script builds for the iOS Simulator; check YANSU_IOS_DESTINATION." >&2
    exit 1
fi
echo "Built: $(cd "$PRODUCTS_DIR" && pwd)/Yansu.app"
