#!/usr/bin/env bash
# Runs the YansuTests unit tests from the YansuCompatibility scheme on two iPhone
# simulators: iPhone 15 and iPhone SE (3rd generation). The UI smoke test is not
# part of this check. The script fails if either run fails.
#
# Environment (optional):
#   YANSU_COMPATIBILITY_BUILD_DIR  output directory (default: ios/.build-compatibility)
#   YANSU_IOS_IPHONE_15_ID         iPhone 15 simulator UDID
#   YANSU_IOS_IPHONE_SE_ID         iPhone SE (3rd generation) simulator UDID
#   YANSU_IOS_MANAGE_SIMULATORS=1  require no booted devices before each run
#   ISOFORM_ALLOW_PARALLEL_SIMULATORS=1  allow another booted simulator
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
BUILD_ROOT="${YANSU_COMPATIBILITY_BUILD_DIR:-$REPO_ROOT/ios/.build-compatibility}"
RESULT_ROOT="$BUILD_ROOT/results"
SIMULATORS=("iPhone 15" "iPhone SE (3rd generation)")
# These test devices use different runtimes. A UDID selects the installed device
# directly, without assuming both models exist on the same iOS version.
SIMULATOR_IDS=(
    "${YANSU_IOS_IPHONE_15_ID:-EF8428A8-56F2-4A83-8434-FC5051039CAF}"
    "${YANSU_IOS_IPHONE_SE_ID:-20F6D41D-37B4-4D28-83EB-BBF1E556A58A}"
)

# Exercise the actual command path without building or touching a simulator.
run_self_tests() (
    scratch="$(mktemp -d)"
    trap 'rm -rf "$scratch"' EXIT
    export YANSU_COMPATIBILITY_BUILD_DIR="$scratch/build"
    unset YANSU_IOS_IPHONE_15_ID YANSU_IOS_IPHONE_SE_ID ISOFORM_ALLOW_PARALLEL_SIMULATORS YANSU_IOS_MANAGE_SIMULATORS
    expected_iphone_15=EF8428A8-56F2-4A83-8434-FC5051039CAF
    expected_iphone_se=20F6D41D-37B4-4D28-83EB-BBF1E556A58A
    export COMPATIBILITY_TEST_INVENTORY="$scratch/devices"
    export COMPATIBILITY_TEST_CALLS="$scratch/calls"
    export COMPATIBILITY_TEST_SHUTDOWNS="$scratch/shutdowns"
    export COMPATIBILITY_TEST_FAILURE=""
    xcrun() {
        if [[ "${1:-}" == simctl && "${2:-}" == shutdown ]]; then
            printf '%s\n' "$3" >>"$COMPATIBILITY_TEST_SHUTDOWNS"
            return 0
        fi
        cat "$COMPATIBILITY_TEST_INVENTORY"
    }
    xcodebuild() {
        local destination=""
        while [[ $# -gt 0 ]]; do
            if [[ "$1" == -destination ]]; then
                destination="$2"
                break
            fi
            shift
        done
        printf '%s\n' "$destination" >>"$COMPATIBILITY_TEST_CALLS"
        [[ "$destination" != "$COMPATIBILITY_TEST_FAILURE" ]]
    }
    export -f xcrun xcodebuild
    cat >"$COMPATIBILITY_TEST_INVENTORY" <<'DEVICES'
== Devices ==
-- iOS 17.2 --
    iPhone 15 (345C2AB6-0000-0000-0000-000000000000) (Shutdown)
    iPhone SE (3rd generation) (20F6D41D-37B4-4D28-83EB-BBF1E556A58A) (Shutdown)
-- iOS 17.4 --
    iPhone 15 (EF8428A8-56F2-4A83-8434-FC5051039CAF) (Shutdown)
DEVICES
    printf 'platform=iOS Simulator,id=%s\n' "$expected_iphone_15" "$expected_iphone_se" >"$scratch/expected"
    printf '%s\n' "$expected_iphone_15" "$expected_iphone_se" >"$scratch/expected-shutdowns"
    bash "$SCRIPT_DIR/test-ios-compatibility.sh" >"$scratch/output" 2>&1
    diff -u "$scratch/expected" "$COMPATIBILITY_TEST_CALLS"
    diff -u "$scratch/expected-shutdowns" "$COMPATIBILITY_TEST_SHUTDOWNS"

    # A matching name on another runtime is not the requested device.
    : >"$COMPATIBILITY_TEST_CALLS"
    : >"$COMPATIBILITY_TEST_SHUTDOWNS"
    if YANSU_IOS_IPHONE_15_ID=00000000-0000-0000-0000-000000000000 \
        bash "$SCRIPT_DIR/test-ios-compatibility.sh" >"$scratch/output" 2>&1; then
        echo "Self-test failed: missing UDID was accepted." >&2
        exit 1
    fi
    [[ ! -s "$COMPATIBILITY_TEST_CALLS" ]]
    grep -Fq "not installed or not available" "$scratch/output"

    # Other machines can explicitly select their own installed test devices.
    YANSU_IOS_IPHONE_15_ID=345C2AB6-0000-0000-0000-000000000000 \
        bash "$SCRIPT_DIR/test-ios-compatibility.sh" >"$scratch/output" 2>&1
    printf 'platform=iOS Simulator,id=%s\n' 345C2AB6-0000-0000-0000-000000000000 "$expected_iphone_se" >"$scratch/override-expected"
    printf '%s\n' 345C2AB6-0000-0000-0000-000000000000 "$expected_iphone_se" >"$scratch/override-expected-shutdowns"
    diff -u "$scratch/override-expected" "$COMPATIBILITY_TEST_CALLS"
    diff -u "$scratch/override-expected-shutdowns" "$COMPATIBILITY_TEST_SHUTDOWNS"

    # Failure on one device must not skip the other or report overall success.
    : >"$COMPATIBILITY_TEST_CALLS"
    : >"$COMPATIBILITY_TEST_SHUTDOWNS"
    export COMPATIBILITY_TEST_FAILURE="platform=iOS Simulator,id=$expected_iphone_15"
    if bash "$SCRIPT_DIR/test-ios-compatibility.sh" >"$scratch/output" 2>&1; then
        echo "Self-test failed: a failed test run was accepted." >&2
        exit 1
    fi
    diff -u "$scratch/expected" "$COMPATIBILITY_TEST_CALLS"
    diff -u "$scratch/expected-shutdowns" "$COMPATIBILITY_TEST_SHUTDOWNS"
    grep -Fq "1 of 2 compatibility runs failed" "$scratch/output"

    # Another booted device blocks the run unless explicitly allowed.
    export COMPATIBILITY_TEST_FAILURE=""
    other_simulator_id=AAAAAAAA-0000-0000-0000-000000000000
    cat >>"$COMPATIBILITY_TEST_INVENTORY" <<DEVICES
    iPhone 14 Pro ($other_simulator_id) (Booted)
DEVICES
    : >"$COMPATIBILITY_TEST_CALLS"
    : >"$COMPATIBILITY_TEST_SHUTDOWNS"
    if bash "$SCRIPT_DIR/test-ios-compatibility.sh" >"$scratch/output" 2>&1; then
        echo "Self-test failed: another booted simulator was accepted." >&2
        exit 1
    fi
    [[ ! -s "$COMPATIBILITY_TEST_CALLS" ]]
    [[ ! -s "$COMPATIBILITY_TEST_SHUTDOWNS" ]]
    grep -Fq "Another simulator is running: iPhone 14 Pro ($other_simulator_id). Bo's rule is one at a time; shut it down or wait." "$scratch/output"
    ISOFORM_ALLOW_PARALLEL_SIMULATORS=1 bash "$SCRIPT_DIR/test-ios-compatibility.sh" >"$scratch/output" 2>&1
    diff -u "$scratch/expected" "$COMPATIBILITY_TEST_CALLS"
    diff -u "$scratch/expected-shutdowns" "$COMPATIBILITY_TEST_SHUTDOWNS"

    # A simulator that was already booted is left running.
    cat >"$COMPATIBILITY_TEST_INVENTORY" <<'DEVICES'
== Devices ==
-- iOS 17.2 --
    iPhone SE (3rd generation) (20F6D41D-37B4-4D28-83EB-BBF1E556A58A) (Shutdown)
-- iOS 17.4 --
    iPhone 15 (EF8428A8-56F2-4A83-8434-FC5051039CAF) (Booted)
DEVICES
    : >"$COMPATIBILITY_TEST_CALLS"
    : >"$COMPATIBILITY_TEST_SHUTDOWNS"
    ISOFORM_ALLOW_PARALLEL_SIMULATORS=1 bash "$SCRIPT_DIR/test-ios-compatibility.sh" >"$scratch/output" 2>&1
    diff -u "$scratch/expected" "$COMPATIBILITY_TEST_CALLS"
    printf '%s\n' "$expected_iphone_se" >"$scratch/prebooted-expected-shutdowns"
    diff -u "$scratch/prebooted-expected-shutdowns" "$COMPATIBILITY_TEST_SHUTDOWNS"

    # Strict managed mode must not take ownership of an already booted device.
    : >"$COMPATIBILITY_TEST_CALLS"
    : >"$COMPATIBILITY_TEST_SHUTDOWNS"
    if YANSU_IOS_MANAGE_SIMULATORS=1 bash "$SCRIPT_DIR/test-ios-compatibility.sh" --iphone-15 >"$scratch/output" 2>&1; then
        echo "Self-test failed: strict mode accepted a booted simulator." >&2
        exit 1
    fi
    [[ ! -s "$COMPATIBILITY_TEST_CALLS" && ! -s "$COMPATIBILITY_TEST_SHUTDOWNS" ]]
    grep -Fq "A simulator is already booted" "$scratch/output"
    # A single-device run selects only the requested destination and shuts it down.
    cat >"$COMPATIBILITY_TEST_INVENTORY" <<DEVICES
    iPhone 15 ($expected_iphone_15) (Shutdown)
DEVICES
    : >"$COMPATIBILITY_TEST_CALLS"
    : >"$COMPATIBILITY_TEST_SHUTDOWNS"
    YANSU_IOS_MANAGE_SIMULATORS=1 bash "$SCRIPT_DIR/test-ios-compatibility.sh" --iphone-15 >"$scratch/output" 2>&1
    printf 'platform=iOS Simulator,id=%s\n' "$expected_iphone_15" >"$scratch/single-expected"
    printf '%s\n' "$expected_iphone_15" >"$scratch/single-shutdown"
    diff -u "$scratch/single-expected" "$COMPATIBILITY_TEST_CALLS"
    diff -u "$scratch/single-shutdown" "$COMPATIBILITY_TEST_SHUTDOWNS"
    echo "Compatibility script self-tests: 8 passed. No builds or simulators were run."
)

if [[ $# -eq 1 && "$1" == --self-test ]]; then
    run_self_tests
    exit 0
fi

if [[ $# -eq 1 && "$1" == --iphone-15 ]]; then
    SIMULATORS=("${SIMULATORS[0]}")
    SIMULATOR_IDS=("${SIMULATOR_IDS[0]}")
elif [[ $# -ne 0 ]]; then
    echo "Usage: $0 [--self-test|--iphone-15]" >&2
    echo "Runs YansuTests on ${SIMULATORS[0]} and on ${SIMULATORS[1]}." >&2
    exit 2
fi
if ! command -v xcodebuild >/dev/null 2>&1; then
    echo "Install Xcode and select its developer directory before running the compatibility tests." >&2
    exit 1
fi

# Check both simulators before building anything.
AVAILABLE_SIMULATORS="$(xcrun simctl list devices available)"
for index in "${!SIMULATORS[@]}"; do
    simulator="${SIMULATORS[$index]}"
    simulator_id="${SIMULATOR_IDS[$index]}"
    if ! grep -Fq "($simulator_id)" <<<"$AVAILABLE_SIMULATORS"; then
        echo "The simulator '$simulator' ($simulator_id) is not installed or not available." >&2
        echo "Install an iOS simulator runtime in Xcode, then list the installed devices with: xcrun simctl list devices available" >&2
        exit 1
    fi
done

BOOTED_SIMULATORS="$(xcrun simctl list devices booted)"
SIMULATOR_WAS_BOOTED=()
for index in "${!SIMULATORS[@]}"; do
    SIMULATOR_WAS_BOOTED[$index]=0
    if grep -Fq "(${SIMULATOR_IDS[$index]}) (Booted)" <<<"$BOOTED_SIMULATORS"; then
        SIMULATOR_WAS_BOOTED[$index]=1
    fi
done

active_simulator_id=""
active_simulator_name=""
active_simulator_was_booted=1
shutdown_active_simulator() {
    local simulator_id="$active_simulator_id"
    local simulator="$active_simulator_name"
    local was_booted="$active_simulator_was_booted"
    active_simulator_id=""
    active_simulator_name=""
    active_simulator_was_booted=1
    if [[ -n "$simulator_id" && "$was_booted" -eq 0 ]]; then
        if ! xcrun simctl shutdown "$simulator_id"; then
            local booted
            booted="$(xcrun simctl list devices booted)" || return 1
            if grep -Fq "($simulator_id) (Booted)" <<<"$booted"; then
                echo "Could not shut down $simulator ($simulator_id)." >&2
                return 1
            fi
        fi
        echo "Shut down $simulator."
    fi
}
trap shutdown_active_simulator EXIT

RUN_ID="$(date -u +%Y%m%dT%H%M%SZ)"
mkdir -p "$RESULT_ROOT"
failed_runs=0
for index in "${!SIMULATORS[@]}"; do
    simulator="${SIMULATORS[$index]}"
    simulator_id="${SIMULATOR_IDS[$index]}"
    if [[ "${ISOFORM_ALLOW_PARALLEL_SIMULATORS:-0}" != 1 ]]; then
        while IFS= read -r device; do
            if [[ "$device" =~ ^[[:space:]]*(.*)[[:space:]]+\(([0-9A-Fa-f-]+)\)[[:space:]]+\(Booted\)[[:space:]]*$ ]]; then
                other_simulator="${BASH_REMATCH[1]}"
                other_simulator_id="${BASH_REMATCH[2]}"
                if [[ "$other_simulator_id" != "$simulator_id" ]]; then
                    echo "Another simulator is running: $other_simulator ($other_simulator_id). Bo's rule is one at a time; shut it down or wait." >&2
                    exit 1
                fi
            fi
        done <<<"$(xcrun simctl list devices booted)"
    fi
    # File-name slug, for example iPhone-SE-3rd-generation.
    slug="$(printf '%s' "$simulator" | sed -e 's/[^A-Za-z0-9][^A-Za-z0-9]*/-/g' -e 's/-$//')"
    result_bundle="$RESULT_ROOT/$RUN_ID-$slug.xcresult"
    if [[ "${YANSU_IOS_MANAGE_SIMULATORS:-0}" == 1 ]]; then
        if xcrun simctl list devices booted | grep -Fq '(Booted)'; then
            echo "A simulator is already booted; refusing to start another." >&2
            exit 1
        fi
    fi
    active_simulator_id="$simulator_id"
    active_simulator_name="$simulator"
    active_simulator_was_booted="${SIMULATOR_WAS_BOOTED[$index]}"
    echo "Running YansuTests on $simulator ($simulator_id). Result bundle: $result_bundle"
    if xcodebuild \
        -project "$REPO_ROOT/ios/Yansu.xcodeproj" \
        -scheme YansuCompatibility \
        -configuration Debug \
        -destination "platform=iOS Simulator,id=$simulator_id" \
        -derivedDataPath "$BUILD_ROOT/derived-data/$slug" \
        -resultBundlePath "$result_bundle" \
        -parallel-testing-enabled NO \
        -collect-test-diagnostics never \
        -only-testing:YansuTests \
        CODE_SIGNING_ALLOWED=YES \
        CODE_SIGN_IDENTITY=- \
        test; then
        echo "YansuTests passed on $simulator."
    else
        echo "YansuTests failed on $simulator. Result bundle: $result_bundle" >&2
        failed_runs=$((failed_runs + 1))
    fi
    shutdown_active_simulator
done

if [[ "$failed_runs" -ne 0 ]]; then
    echo "$failed_runs of ${#SIMULATORS[@]} compatibility runs failed. Results: $RESULT_ROOT" >&2
    exit 1
fi
echo "YansuTests passed on all ${#SIMULATORS[@]} simulators. Results: $RESULT_ROOT"
