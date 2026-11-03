#!/usr/bin/env bash
# Parsing/storage fixtures only. Never launches an app; works on Linux and macOS.
set -euo pipefail
SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=scripts/qa/fresh-install.sh
source "$SCRIPT_DIR/fresh-install.sh"
command -v sqlite3 > /dev/null || { echo 'sqlite3 is required' >&2; exit 1; }
QA_TEST_ROOT=$(mktemp -d "${TMPDIR:-/tmp}/fresh-install-test.XXXXXX")
trap 'rm -rf "$QA_TEST_ROOT"' EXIT
passed=0
failed=0

check() {
    local label=$1
    shift
    if "$@"; then
        printf 'PASS %s\n' "$label"
        passed=$((passed + 1))
    else
        printf 'FAIL %s\n' "$label" >&2
        failed=$((failed + 1))
    fi
}
rejects() { if "$@"; then return 1; fi; }

mkdir -p "$QA_TEST_ROOT/user/.yansu-agent" "$QA_TEST_ROOT/user/.isoform-agent" \
    "$QA_TEST_ROOT/user/empty QA" "$QA_TEST_ROOT/evidence" "$QA_TEST_ROOT/reports"
user_home="$QA_TEST_ROOT/user"
data="$user_home/empty QA"
check 'accept explicit empty home with spaces' qa_validate_home "$data" "$user_home"
check 'refuse missing home' rejects qa_validate_home "$user_home/missing" "$user_home"
check 'refuse relative home' rejects qa_validate_home relative "$user_home"
check 'refuse live Yansu home even when empty' rejects qa_validate_home "$user_home/.yansu-agent" "$user_home"
check 'refuse live Isoform home even when empty' rejects qa_validate_home "$user_home/.isoform-agent" "$user_home"
mkdir "$user_home/.yansu-agent/nested"
check 'refuse live descendant via dot-dot' rejects qa_validate_home "$user_home/empty QA/../.yansu-agent/nested" "$user_home"
ln -s "$user_home/.yansu-agent" "$user_home/alias"
check 'refuse symlink alias' rejects qa_validate_home "$user_home/alias/nested" "$user_home"
mkdir "$QA_TEST_ROOT/outside"
rmdir "$user_home/.isoform-agent"
ln -s "$QA_TEST_ROOT/outside" "$user_home/.isoform-agent"
check 'refuse real target of symlinked live home' rejects qa_validate_home "$QA_TEST_ROOT/outside" "$user_home"
touch "$data/.hidden"
check 'hidden files make a home nonempty' rejects qa_validate_home "$data" "$user_home"
rm "$data/.hidden"

# Exercise the exact exec/environment boundary with a plain shell, never Wails.
# The probe also writes a temp file to model startup's os.TempDir() consumers.
for flavor in yansu isoform; do
    caller_tmp="$user_home/.$flavor-agent/unsafe-tmp"
    mkdir "$caller_tmp"
    app_tmp=$(mktemp -d "$QA_TEST_ROOT/evidence/app-tmp.XXXXXX")
    probe_output="$QA_TEST_ROOT/evidence/env-$flavor"
    # Expand the probe variables in the child, after qa_exec_app sets its env.
    # shellcheck disable=SC2016
    TMPDIR="$caller_tmp" YANSU_AGENT_HOME="$user_home/.$flavor-agent" YANSU_APP_FLAVOR=wrong \
        qa_exec_app "$data" "$flavor" "$app_tmp" /bin/sh -c '
            printf "%s\n%s\n%s\n%s\n" "$YANSU_AGENT_HOME" "$YANSU_APP_FLAVOR" "$TMPDIR" "$$"
            mktemp "$TMPDIR/probe.XXXXXX" > /dev/null
        ' > "$probe_output" &
    probe_pid=$!
    check "$flavor environment probe exits cleanly" wait "$probe_pid"
    check "$flavor overrides inherited data home" test "$(sed -n '1p' "$probe_output")" = "$data"
    check "$flavor overrides inherited flavor" test "$(sed -n '2p' "$probe_output")" = "$flavor"
    check "$flavor replaces unsafe TMPDIR" test "$(sed -n '3p' "$probe_output")" = "$app_tmp"
    check "$flavor exec preserves owned PID" test "$(sed -n '4p' "$probe_output")" = "$probe_pid"
    check "$flavor temp writes land in QA evidence" test -n "$(find "$app_tmp" -name 'probe.*' -print -quit)"
    check "$flavor inherited temp directory stays empty" test -z "$(find "$caller_tmp" -mindepth 1 -print -quit)"
done

check 'missing DB fails and is not created' rejects qa_schema_matches "$data/sessions.db" 61
check 'read-only check did not create DB' test ! -e "$data/sessions.db"
sqlite3 "$data/sessions.db" 'PRAGMA user_version=61; CREATE TABLE ui_state (key TEXT PRIMARY KEY, value TEXT NOT NULL);'
check 'expected schema from real source' test "$(qa_expected_schema "$SCRIPT_DIR/../../wails-gui/storage/storage.go")" -gt 0
check 'matching schema' qa_schema_matches "$data/sessions.db" 61
check 'older schema fails' rejects qa_schema_matches "$data/sessions.db" 62
check 'newer schema fails' rejects qa_schema_matches "$data/sessions.db" 60
check 'onboarding absent is fresh' qa_onboarding_fresh "$data/sessions.db"
sqlite3 "$data/sessions.db" "INSERT INTO ui_state VALUES ('yansu.onboarding.state', '{\"completed\":false}');"
check 'saved false onboarding is not absent' rejects qa_onboarding_fresh "$data/sessions.db"
sqlite3 "$data/sessions.db" 'DELETE FROM ui_state; DROP TABLE ui_state;'
check 'SQL errors fail freshness' rejects qa_onboarding_fresh "$data/sessions.db"
sqlite3 "$data/sessions.db" 'CREATE TABLE ui_state (key TEXT PRIMARY KEY, value TEXT NOT NULL);'
check 'missing tier uses on default' qa_telemetry_on "$data"
printf ' detailed\n' > "$data/telemetry_tier.txt"
check 'detailed telemetry on' qa_telemetry_on "$data"
for tier in off basic unknown '' 'de tailed'; do
    printf '%s\n' "$tier" > "$data/telemetry_tier.txt"
    check "unexpected tier '$tier' fails" rejects qa_telemetry_on "$data"
done

printf '2026/10/17 12:00:00 [agent-start] runner ready in 25ms\n' > "$data/yansu-gui.log.old"
printf '2026/10/17 12:00:00 [engagement] service started\n' > "$data/yansu-gui.log"
check 'partial boot is not ready' rejects qa_boot_ready "$data"
printf '2026/10/17 12:00:00 [spotlight] helper ready on 127.0.0.1:51234\n' >> "$data/yansu-gui.log"
check 'real boot markers incl. rotation are ready' qa_boot_ready "$data"
check 'clean logs pass panic scan' qa_no_panic "$data" "$QA_TEST_ROOT/evidence"
printf 'panic: startup failed\n' > "$data/yansu-gui.stderr.log"
check 'Go panic on captured stderr fails' rejects qa_no_panic "$data" "$QA_TEST_ROOT/evidence"
printf 'fatal error: concurrent map writes\n' > "$data/yansu-gui.stderr.log"
check 'Go fatal on stderr fails' rejects qa_no_panic "$data" "$QA_TEST_ROOT/evidence"
rm "$data/yansu-gui.stderr.log"
printf 'panic: before log init\n' > "$QA_TEST_ROOT/evidence/launch.log"
check 'early launch panic fails' rejects qa_no_panic "$data" "$QA_TEST_ROOT/evidence"
rm "$QA_TEST_ROOT/evidence/launch.log"

# Let plain shell children finish before calling qa_stop_app: no Apple events,
# osascript, desktop process, or signal to a running application is involved.
qa_stopped_child() {
    local i
    "$@" &
    QA_APP_PID=$!
    for ((i=0; i<100; i++)); do
        kill -0 "$QA_APP_PID" 2>/dev/null || break
        sleep 0.01
    done
    if kill -0 "$QA_APP_PID" 2>/dev/null; then
        kill -KILL "$QA_APP_PID" 2>/dev/null || true
        wait "$QA_APP_PID" 2>/dev/null || true
        QA_APP_PID=
        return 1
    fi
    qa_stop_app
}
QA_APP_HOME=$data
QA_EVIDENCE="$QA_TEST_ROOT/evidence"
check 'normal exit-0 Quit control passes' qa_stopped_child /bin/sh -c 'exit 0'
check 'owned child exit 42 fails Quit' rejects qa_stopped_child /bin/sh -c 'exit 42'
check 'actual wait status 42 retained in evidence' qa_log_has '^PID [0-9]+: exit status 42, script forced termination 0$' "$QA_EVIDENCE/quit.log"
check 'signal-terminated child fails Quit' rejects qa_stopped_child /bin/sh -c 'kill -TERM "$$"'
check 'script-forced termination fails even with status 0' rejects qa_shutdown_clean 1 0 "$data" "$QA_EVIDENCE"
cp "$data/yansu-gui.log" "$QA_TEST_ROOT/boot.log"
for watchdog_log in "$data/yansu-gui.log" "$data/yansu-gui.log.old.2" "$data/yansu-gui.stderr.log" "$QA_EVIDENCE/launch.log"; do
    printf '2026/10/17 12:00:05 [shutdown] watchdog: 5s elapsed, forcing exit\n' > "$watchdog_log"
    check "watchdog in ${watchdog_log##*/} fails Quit despite exit 0" rejects qa_stopped_child /bin/sh -c 'exit 0'
    if [[ "$watchdog_log" == "$data/yansu-gui.log" ]]; then
        cp "$QA_TEST_ROOT/boot.log" "$watchdog_log"
    else
        rm "$watchdog_log"
    fi
done
check 'normal Quit still passes after watchdog fixtures removed' qa_stopped_child /bin/sh -c 'exit 0'
check 'cleanup clears the owned PID' test -z "$QA_APP_PID"

printf '{"bundleInfo":{"CFBundleIdentifier":"com.wails.IsoformOther"}}\n' > "$QA_TEST_ROOT/reports/other.ips"
check 'unrelated bundle prefix ignored' qa_crashes_clear "$QA_TEST_ROOT/reports" com.wails.Isoform Isoform "$QA_TEST_ROOT/evidence"
printf '{"bundleInfo":{"CFBundleIdentifier":"com.wails.Isoform"}}\n' > "$QA_TEST_ROOT/reports/new.ips"
check 'modern ips matched by complete bundle id' rejects qa_crashes_clear "$QA_TEST_ROOT/reports" com.wails.Isoform Isoform "$QA_TEST_ROOT/evidence"
touch -t 202001010000 "$QA_TEST_ROOT/reports/new.ips"
check 'old crash excluded from ten-minute window' qa_crashes_clear "$QA_TEST_ROOT/reports" com.wails.Isoform Isoform "$QA_TEST_ROOT/evidence"
printf 'Identifier: com.wails.Isoform\n' > "$QA_TEST_ROOT/reports/legacy.crash"
check 'legacy crash identifier matched' rejects qa_crashes_clear "$QA_TEST_ROOT/reports" com.wails.Isoform Isoform "$QA_TEST_ROOT/evidence"
rm "$QA_TEST_ROOT/reports/legacy.crash"
touch "$QA_TEST_ROOT/reports/Isoform-2026-10-17.ips"
check 'truncated report matched by executable filename' rejects qa_crashes_clear "$QA_TEST_ROOT/reports" com.wails.Isoform Isoform "$QA_TEST_ROOT/evidence"
check 'no DiagnosticReports directory is fine' qa_crashes_clear "$QA_TEST_ROOT/missing-reports" com.wails.Isoform Isoform "$QA_TEST_ROOT/evidence"

QA_FAILURES=0
qa_row 'pass fixture' evidence true
qa_row 'fail fixture' evidence false
check 'table accumulates failure exit status' test "$QA_FAILURES" -eq 1
printf '\n%d passed, %d failed\n' "$passed" "$failed"
[[ "$failed" -eq 0 ]]
