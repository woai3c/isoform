#!/usr/bin/env bash
# Credential-free source checks only. This does not build, sign or launch Isoform.
set -uo pipefail
root="$(cd "$(dirname "$0")/../.." && pwd)"
tmp="$(mktemp -d "${TMPDIR:-/tmp}/isoform-rc.XXXXXXXX")" || exit 1
trap 'rm -rf -- "$tmp"' EXIT
mkdir -p "$tmp/home" "$tmp/tmp"
# Let pnpm find already-installed versioned executables without exposing the
# user's home or package-manager configuration to the isolated checks.
pnpm_data="${XDG_DATA_HOME:-}"
if [ -z "$pnpm_data" ]; then
    case "$(uname -s)" in
        Darwin) pnpm_data="$HOME/Library" ;;
        *) pnpm_data="$HOME/.local/share" ;;
    esac
fi
mkdir -p "$tmp/data/pnpm"
if [ -d "$pnpm_data/pnpm/.tools" ]; then
    ln -s "$pnpm_data/pnpm/.tools" "$tmp/data/pnpm/.tools"
fi
# Cache paths are resolved before HOME is isolated; no account configuration or
# credentials are passed to any check. Prerequisites must already be installed.
go_cache="$(go env GOCACHE)"
go_mod_cache="$(go env GOMODCACHE)"
clean_env=(env -i "PATH=$PATH" "HOME=$tmp/home" "XDG_DATA_HOME=$tmp/data" "TMPDIR=$tmp/tmp" "GOCACHE=$go_cache" "GOMODCACHE=$go_mod_cache" "LANG=C.UTF-8")
labels=()
results=()
run_check() {
    local label="$1" dir="$2"; shift 2
    local log="$tmp/check-${#labels[@]}.log" status
    printf '\nChecking %s...\n' "$label"
    if (cd "$root/$dir" && "${clean_env[@]}" "$@") >"$log" 2>&1; then
        status=PASS
    else
        status=FAIL
        cat "$log"
    fi
    labels+=("$label")
    results+=("$status")
}
# Go's test binary links the package under its import path, unlike the app.
go_test=(go test -tags rc -count=1 -ldflags '-X wails-gui.Version=1.0.0 -X wails-gui.buildFlavor=isoform -X wails-gui.buildChannel=release')
# The bindings generator compiles the bindings-only helper; it does not start
# the desktop app. An embed placeholder is needed on a clean checkout.
if [ ! -d "$root/wails-gui/frontend/dist" ]; then
    mkdir -p "$root/wails-gui/frontend/dist"
    printf 'Bindings generation placeholder\n' > "$root/wails-gui/frontend/dist/index.html"
fi
run_check 'Wails bindings generation' wails-gui go run github.com/wailsapp/wails/v2/cmd/wails@v2.12.0 generate module -nocolour
# Go embeds frontend/dist, so build it before compiling the Go package.
run_check 'Frontend build' wails-gui/frontend pnpm build
run_check 'Release identity, feed and updates' wails-gui "${go_test[@]}" -run '^(TestRCReleaseIdentity|TestRCNonReleaseNeverChecksForUpdates|TestRCBuildScriptStampsRelease|TestAppFlavorFromEnvironmentAndLdflag|TestCompiledIsoformRefusesInheritedPublishedHome|TestIsoformReleaseBuildFindsTheIsoformUpdate|TestIsoformDevelopmentBuildNeverChecksForUpdates)$' .
run_check 'Crash capture' wails-gui "${go_test[@]}" -run '^(TestCrashLogPersistsFatalDiagnostics|TestRCCrashCollectorWiredIntoStartup)$' .
run_check 'Crash evidence available after restart' wails-gui "${go_test[@]}" -run '^TestRCCrashEvidenceAvailableToSupportArchive$' .
run_check 'Setup permission sequence' wails-gui "${go_test[@]}" -run '^(TestAgentProbeFreshInstallUnknownRows|TestPermissionSequenceOrderDenialAndResume|TestMissingModelsNeverDownloadFromFeatureConsumers)$' .
run_check 'First brief scheduling' wails-gui "${go_test[@]}" -run '^TestMobileBriefLaunchBuildsTheMorningLateOnce$' .
run_check 'Privacy on startup and pause' wails-gui "${go_test[@]}" -run '^(TestLaunchPublishesObservationPolicyBeforeFirstScreen|TestObservationPauseStopsNativeAudioAndDiscardsPendingChunk|TestClipboardObserverFollowsSwitchAndPause)$' .
run_check 'Protected app defaults' wails-gui go test -count=1 -run '^(TestIsCoveredByDefaults|TestIsSensitiveAppName|TestFilterSensitiveAppNames)$' ./internal/pii
run_check 'Iroh pairing invitation' wails-gui "${go_test[@]}" -run '^(TestMobileRemoteAccessInvitationAndReset|TestMobileRemotePairingRequiresTicket|TestMobileProtocolVectors)$' .
run_check 'Signed push gateway protocol' relay go test -count=1 -run '^(TestPushRequiresSignedRequestAndValidGrant|TestSignedGatewayClientRoundTripsThroughHandler)$' ./internal/pushgateway
run_check 'Version 1.0.0 and build stamp' wails-gui "${go_test[@]}" -run '^TestRCVersion$' .
run_check 'Go vet' wails-gui go vet ./...
run_check 'Frontend tests' wails-gui/frontend pnpm test
printf '\nRC checklist\n'
failed=0
for i in "${!labels[@]}"; do
    printf '[%s] %s\n' "${results[$i]}" "${labels[$i]}"
    if [ "${results[$i]}" = FAIL ]; then failed=1; fi
done
printf '\nSource checks do not replace a clean-install, first-brief, 5G, signed-build or update smoke test.\n'
exit "$failed"
