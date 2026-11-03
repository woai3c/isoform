#!/usr/bin/env bash
# Records the iPhone launch demo from the real app on one booted Simulator: the DEBUG-only launch-demo seed
# (ios/Yansu/LaunchDemoUITestSeed.swift) supplies the sample morning, LaunchDemoRecordingUITests taps through it at a
# person's pace, and `simctl io recordVideo` records the screen. The clip is cut to the take and written as 60 fps
# H.264 .mov at the device's native size, or a .png still of Today (the seeded app opened untouched).
#
# Usage: scripts/record-ios-launch-demo.sh <dark|light> <out.mov|out.png> [pace]
# Needs YANSU_DEMO_UDID, a booted Simulator (this script boots and shuts down nothing), Xcode and ffmpeg.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
APPEARANCE="${1:?dark or light}"
OUT="${2:?output .mov or .png}"
PACE="${3:-1}"
UDID="${YANSU_DEMO_UDID:?set YANSU_DEMO_UDID to a booted Simulator}"
BUILD_DIR="${YANSU_DEMO_BUILD_DIR:-$REPO_ROOT/ios/.build-demo}"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

xcrun simctl list devices booted | grep -q "$UDID" || { echo "Simulator $UDID is not booted." >&2; exit 1; }
xcrun simctl ui "$UDID" appearance "$APPEARANCE"
xcrun simctl status_bar "$UDID" override --time 9:41 --dataNetwork wifi --wifiMode active --wifiBars 3 \
    --cellularMode active --cellularBars 4 --batteryState discharging --batteryLevel 100

if [[ ! -d "$BUILD_DIR/Build/Products" || -n "${YANSU_DEMO_REBUILD:-}" ]]; then
    xcodebuild -quiet -project "$REPO_ROOT/ios/Yansu.xcodeproj" -scheme Yansu -configuration Debug \
        -destination "id=$UDID" -derivedDataPath "$BUILD_DIR" CODE_SIGNING_ALLOWED=YES CODE_SIGN_IDENTITY=- \
        build-for-testing
fi
if [[ "$OUT" == *.png ]]; then
    # The still: the seeded app opened on Today, untouched, at the device's native size.
    APP=$(find "$BUILD_DIR/Build/Products/Debug-iphonesimulator" -maxdepth 1 -name Yansu.app | head -1)
    xcrun simctl install "$UDID" "$APP"
    BUNDLE=$(/usr/libexec/PlistBuddy -c "Print CFBundleIdentifier" "$APP/Info.plist")
    SIMCTL_CHILD_YANSU_UITEST_SETTINGS_STATE=launch-demo SIMCTL_CHILD_YANSU_UITEST_TAB=feed SIMCTL_CHILD_YANSU_UITEST_TOUCHES=0 \
        xcrun simctl launch --terminate-running-process "$UDID" "$BUNDLE" >/dev/null
    sleep "${YANSU_DEMO_STILL_WAIT:-14}"
    xcrun simctl io "$UDID" screenshot --type=png "$OUT" >/dev/null 2>&1
    xcrun simctl terminate "$UDID" "$BUNDLE" || true
    echo "{\"out\":\"$OUT\"}"
    exit 0
fi
TEST="LaunchDemoRecordingUITests/testLaunchDemoTake"
START_MARK=DEMO_START

xcrun simctl io "$UDID" recordVideo --codec h264 --force "$WORK/raw.mov" 2>"$WORK/record.log" &
RECORDER=$!
for _ in $(seq 1 100); do grep -q "Recording started" "$WORK/record.log" 2>/dev/null && break; sleep 0.05; done
T0=$(python3 -c 'import time; print(time.time())')

TEST_RUNNER_YANSU_LAUNCH_DEMO=1 TEST_RUNNER_YANSU_LAUNCH_DEMO_PACE="$PACE" \
    xcodebuild -project "$REPO_ROOT/ios/Yansu.xcodeproj" -scheme Yansu -destination "id=$UDID" \
    -derivedDataPath "$BUILD_DIR" -only-testing:"YansuUITests/$TEST" test-without-building >"$WORK/test.log" 2>&1 || {
    kill -INT "$RECORDER"; wait "$RECORDER" || true
    cp "$WORK/raw.mov" "${OUT%.*}.failed.mov" 2>/dev/null || true
    grep -E "error:|XCTAssert" "$WORK/test.log" | head -20 >&2
    exit 1
}
kill -INT "$RECORDER"; wait "$RECORDER" || true

START=$(grep -o "$START_MARK [0-9.]*" "$WORK/test.log" | head -1 | cut -d' ' -f2)
END=$(grep -o "DEMO_END [0-9.]*" "$WORK/test.log" | head -1 | cut -d' ' -f2 || true)
FROM=$(python3 -c "print(max(0, $START - $T0))")
LENGTH=$(python3 -c "print($END - $START)")
ffmpeg -y -loglevel error -ss "$FROM" -i "$WORK/raw.mov" -t "$LENGTH" -vf "fps=60,format=yuv420p" \
    -c:v libx264 -preset slow -crf 12 -an "$WORK/take.mov"

# UI test queries leave dead time between beats. Every still stretch keeps a person's pause (HOLD seconds, the
# first and last a little longer) and loses the rest; nothing that moves is cut.
HOLD="${YANSU_DEMO_HOLD:-2.0}"
FREEZES=$(ffmpeg -hide_banner -i "$WORK/take.mov" -vf "scale=402:-1,freezedetect=n=-50dB:d=$HOLD" -map 0:v -f null - 2>&1 \
    | grep -o "freeze_\(start\|end\): [0-9.]*" | awk '{print $2}' | tr '\n' ' ')
SELECT=$(python3 - "$HOLD" "$LENGTH" $FREEZES <<'PY'
import sys
hold, length, marks = float(sys.argv[1]), float(sys.argv[2]), [float(v) for v in sys.argv[3:]]
if len(marks) % 2: marks.append(length)
cuts = []
for i in range(0, len(marks), 2):
    start, end = marks[i], marks[i + 1]
    keep = hold + 0.5 if start < 0.1 or end > length - 0.1 else hold
    if end - start > keep:
        cuts.append(f"between(t,{start + keep:.3f},{end:.3f})")
print("not(" + "+".join(cuts) + ")" if cuts else "1")
PY
)
ffmpeg -y -loglevel error -i "$WORK/take.mov" -vf "select='$SELECT',setpts=N/60/TB,fps=60,format=yuv420p" \
    -c:v libx264 -preset slow -crf 14 -tag:v avc1 -movflags +faststart -an "$OUT"
SECONDS_OUT=$(ffprobe -v error -show_entries format=duration -of csv=p=0 "$OUT")
echo "{\"out\":\"$OUT\",\"seconds\":$SECONDS_OUT,\"recorded\":$LENGTH}"
