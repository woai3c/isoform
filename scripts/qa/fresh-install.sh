#!/usr/bin/env bash
# macOS RC smoke check. Sourceable for parsing tests; sourcing never launches an app.
set -euo pipefail

qa_usage() {
    cat <<'EOF'
Usage: fresh-install.sh APP.app [EMPTY_DATA_HOME] [--timeout SECONDS] [--expected-schema N]

EMPTY_DATA_HOME must already exist and be empty. If omitted, YANSU_AGENT_HOME
is required (both flavors use that variable). No default/live home is used.
Example: mkdir "$HOME/.yansu-agent-rc"
         fresh-install.sh build/bin/Yansu.app "$HOME/.yansu-agent-rc"

Copies the signed bundle to ~/Applications/<flavor>-QA-<unique>.app, launches
its executable with the isolated home, checks boot/storage/freshness/crashes,
then quits that PID. Exit 0 = all PASS, 1 = failed checks, 2 = invalid setup.
The QA app, data, and printed evidence directory are retained for inspection.
Do not interact with onboarding during this smoke check.
EOF
}

qa_die() { printf 'fresh-install: %s\n' "$*" >&2; exit 2; }

# Resolve an existing directory physically, including symlinked parents and .. .
# Refuse live roots, their descendants and ancestors, even via a symlink alias.
qa_validate_home() {
    local candidate=$1 user_home=$2 root resolved protected
    [[ "$candidate" = /* && -d "$candidate" ]] || return 1
    candidate=$(cd "$candidate" && pwd -P) || return 1
    user_home=$(cd "$user_home" && pwd -P) || return 1
    for root in "$user_home/.yansu-agent" "$user_home/.isoform-agent"; do
        resolved=$root
        if [[ -d "$root" ]]; then resolved=$(cd "$root" && pwd -P) || return 1; fi
        for protected in "$root" "$resolved"; do
            case "$candidate/" in "$protected/"*) return 1 ;; esac
            case "$protected/" in "$candidate/"*) return 1 ;; esac
        done
    done
    # find includes hidden files; fail closed on unreadable directories.
    [[ -r "$candidate" && -w "$candidate" && -x "$candidate" ]] || return 1
    local entries
    entries=$(find "$candidate" -mindepth 1 -maxdepth 1 -print -quit) || return 1
    [[ -z "$entries" ]] || return 1
    printf '%s\n' "$candidate"
}

qa_expected_schema() {
    awk '$1 == "const" && $2 == "schemaVersion" && $3 == "=" {print $4}' "$1"
}

qa_sql() {
    # -readonly prevents a missing DB from being created by the check itself.
    [[ -f "$1" ]] || return 1
    sqlite3 -batch -noheader -readonly -cmd '.timeout 2000' "$1" "$2"
}

qa_schema_matches() {
    local actual
    actual=$(qa_sql "$1" 'PRAGMA user_version;') || return 1
    [[ "$actual" == "$2" ]]
}

qa_onboarding_fresh() {
    local count
    count=$(qa_sql "$1" "SELECT count(*) FROM ui_state WHERE key = 'yansu.onboarding.state';") || return 1
    # A saved false/empty value still counts as an existing key.
    [[ "$count" == 0 ]]
}

qa_telemetry_on() {
    local path="$1/telemetry_tier.txt" tier
    # telemetry_tier_pref.go: missing file means defaultTelemetryTier = detailed.
    [[ -e "$path" || -L "$path" ]] || return 0
    [[ -f "$path" && -r "$path" ]] || return 1
    tier=$(LC_ALL=C sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//' "$path") || return 1
    [[ "$tier" == detailed ]]
}

qa_log_has() {
    local pattern=$1 file
    shift
    for file in "$@"; do
        [[ -f "$file" ]] || continue
        if LC_ALL=C grep -E "$pattern" "$file" > /dev/null; then return 0; fi
    done
    return 1
}

qa_boot_ready() {
    local data=$1
    qa_log_has '\[agent-start\] runner ready in ' "$data"/yansu-gui.log* &&
        qa_log_has '\[engagement\] service started' "$data"/yansu-gui.log* &&
        qa_log_has '\[spotlight\] helper ready on 127\.0\.0\.1:' "$data"/yansu-gui.log* &&
        qa_sql "$data/sessions.db" 'SELECT count(*) FROM ui_state;' > /dev/null 2>&1
}

qa_report_matches() {
    local report=$1 bundle_id=$2 executable=$3
    case "${report##*/}" in
        "$executable"_*.crash|"$executable"-*.ips|"$executable"_*.ips) return 0 ;;
    esac
    # Modern .ips JSON and legacy .crash Identifier lines. Match a complete
    # identifier, not another bundle whose identifier merely shares a prefix.
    LC_ALL=C grep -F "\"$bundle_id\"" "$report" > /dev/null ||
        awk -v id="$bundle_id" '$1 == "Identifier:" && $2 == id {found=1} END {exit !found}' "$report"
}

qa_crashes_clear() {
    local reports=$1 bundle_id=$2 executable=$3 evidence=$4 report found=0
    [[ -e "$reports" ]] || return 0
    [[ -d "$reports" && -r "$reports" && -x "$reports" ]] || return 1
    find "$reports" -type f -mmin -10 -print0 > "$evidence/crash-candidates" || return 1
    while IFS= read -r -d '' report; do
        [[ -r "$report" ]] || return 1
        if qa_report_matches "$report" "$bundle_id" "$executable"; then
            printf 'Recent crash report: %s\n' "$report" >&2
            found=1
        fi
    done < "$evidence/crash-candidates"
    [[ "$found" == 0 ]]
}

qa_row() {
    local label=$1 detail=$2
    shift 2
    if "$@"; then
        printf '%-25s | PASS | %s\n' "$label" "$detail"
    else
        printf '%-25s | FAIL | %s\n' "$label" "$detail"
        QA_FAILURES=$((QA_FAILURES + 1))
    fi
}

qa_exec_app() {
    local data=$1 flavor=$2 app_tmp=$3
    shift 3
    export YANSU_AGENT_HOME="$data" YANSU_APP_FLAVOR="$flavor" TMPDIR="$app_tmp"
    # Replace the background shell so $! is still the app PID, not a wrapper.
    exec "$@"
}

qa_shutdown_clean() {
    local forced=$1 exit_status=$2 data=$3 evidence=$4
    [[ "$forced" == 0 && "$exit_status" == 0 ]] || return 1
    # App.shutdown's watchdog calls os.Exit(0), so status alone is insufficient.
    qa_logs_clear '\[shutdown\] watchdog: .*forcing exit' "$data" "$evidence"
}

qa_stop_app() {
    local i forced=0 exit_status=0
    [[ -n "${QA_APP_PID:-}" ]] || return 0
    if kill -0 "$QA_APP_PID" 2>/dev/null; then
        # NSRunningApplication sends a normal Quit to this exact PID without
        # resolving a bundle id (which could select the user's live app).
        /usr/bin/osascript -l JavaScript -e '
            ObjC.import("AppKit");
            function run(argv) {
                var app = $.NSRunningApplication.runningApplicationWithProcessIdentifier(Number(argv[0]));
                if (app) app.terminate;
            }' "$QA_APP_PID" >> "$QA_EVIDENCE/quit.log" 2>&1 || true
        for ((i=0; i<10; i++)); do
            kill -0 "$QA_APP_PID" 2>/dev/null || break
            sleep 1
        done
        if kill -0 "$QA_APP_PID" 2>/dev/null; then
            forced=1
            kill -TERM "$QA_APP_PID" 2>/dev/null || true
            sleep 1
            if kill -0 "$QA_APP_PID" 2>/dev/null; then kill -KILL "$QA_APP_PID" 2>/dev/null || true; fi
        fi
    fi
    if wait "$QA_APP_PID" 2>/dev/null; then
        exit_status=0
    else
        exit_status=$?
    fi
    printf 'PID %s: exit status %s, script forced termination %s\n' \
        "$QA_APP_PID" "$exit_status" "$forced" >> "$QA_EVIDENCE/quit.log"
    QA_APP_PID=
    qa_shutdown_clean "$forced" "$exit_status" "$QA_APP_HOME" "$QA_EVIDENCE"
}

qa_main() {
    local app=${1:-} data=${YANSU_AGENT_HOME:-} timeout=120 expected='' script_dir
    local plist product flavor bundle_id executable installed applications deadline root
    local alive=0 boot=0
    if [[ "$app" == --help || "$app" == -h ]]; then qa_usage; return 0; fi
    [[ -n "$app" ]] || { qa_usage >&2; return 2; }
    shift
    if [[ $# -gt 0 && "$1" != --* ]]; then data=$1; shift; fi
    while [[ $# -gt 0 ]]; do
        [[ $# -ge 2 ]] || qa_die "missing value for $1"
        case "$1" in
            --timeout) timeout=$2 ;;
            --expected-schema) expected=$2 ;;
            *) qa_die "unknown option: $1" ;;
        esac
        shift 2
    done
    [[ "$(uname -s)" == Darwin ]] || qa_die 'this smoke check requires macOS'
    [[ "$timeout" =~ ^[1-9][0-9]*$ ]] || qa_die 'timeout must be a positive integer'
    for executable in sqlite3 ditto osascript; do command -v "$executable" > /dev/null || qa_die "missing $executable"; done
    [[ -n "$data" ]] || qa_die 'provide an explicit empty QA home or YANSU_AGENT_HOME'
    data=$(qa_validate_home "$data" "$HOME") || qa_die 'QA home must be an existing empty directory outside both live data homes'
    app=$(cd "$app" && pwd -P) || qa_die 'app bundle not found'
    plist="$app/Contents/Info.plist"
    bundle_id=$(/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' "$plist") || qa_die 'bundle identifier missing'
    executable=$(/usr/libexec/PlistBuddy -c 'Print :CFBundleExecutable' "$plist") || qa_die 'bundle executable missing'
    product=$(/usr/libexec/PlistBuddy -c 'Print :CFBundleName' "$plist") || qa_die 'bundle name missing'
    case "$product" in
        Isoform) flavor=isoform ;;
        Yansu) flavor=yansu ;;
        *) qa_die 'expected an Isoform.app or Yansu.app build' ;;
    esac
    [[ "$bundle_id" =~ ^[A-Za-z0-9.-]+$ && "$executable" != */* && -n "$executable" && -x "$app/Contents/MacOS/$executable" ]] || qa_die 'invalid bundle metadata or executable'
    script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
    if [[ -z "$expected" ]]; then
        expected=$(qa_expected_schema "$script_dir/../../wails-gui/storage/storage.go") || qa_die 'pass --expected-schema when running outside the checkout'
    fi
    [[ "$expected" =~ ^[1-9][0-9]*$ ]] || qa_die 'expected schema must be a positive integer'
    # Check the existing destination before mkdir, including live homes that
    # are themselves symlinks. Do not copy anything under either data root.
    if [[ -d "$HOME/Applications" ]]; then
        applications=$(cd "$HOME/Applications" && pwd -P)
        for root in "$HOME/.yansu-agent" "$HOME/.isoform-agent"; do
            if [[ -d "$root" ]]; then root=$(cd "$root" && pwd -P); fi
            case "$applications/" in "$root/"*) qa_die 'Applications points into live data' ;; esac
        done
    fi
    applications=$(mkdir -p "$HOME/Applications" && cd "$HOME/Applications" && pwd -P)
    # Also reject a symlinked Applications folder pointing into a live data home.
    case "$applications/" in "$HOME/.yansu-agent/"*|"$HOME/.isoform-agent/"*) qa_die 'Applications points into live data' ;; esac
    installed="$applications/$product-QA-$(date +%Y%m%d-%H%M%S)-$$.app"
    [[ ! -e "$installed" && ! -L "$installed" ]] || qa_die 'QA app destination already exists'
    # Do not let an inherited TMPDIR route QA writes into a live data home.
    QA_EVIDENCE=$(mktemp -d /tmp/fresh-install.XXXXXX)
    QA_APP_TMPDIR=$(mktemp -d "$QA_EVIDENCE/app-tmp.XXXXXX")
    QA_APP_HOME=$data
    QA_APP_PID=
    QA_FAILURES=0
    trap 'qa_stop_app || true' EXIT
    trap 'exit 130' INT
    trap 'exit 143' TERM
    ditto "$app" "$installed"
    printf 'QA app: %s\nQA data: %s\nEvidence: %s\nQA temp: %s\nExpected sessions schema: %s\n' \
        "$installed" "$data" "$QA_EVIDENCE" "$QA_APP_TMPDIR" "$expected"
    # Direct execution preserves the per-process environment and gives us the
    # PID we own. LaunchServices may reuse another instance of the same bundle.
    qa_exec_app "$data" "$flavor" "$QA_APP_TMPDIR" "$installed/Contents/MacOS/$executable" > "$QA_EVIDENCE/launch.log" 2>&1 &
    QA_APP_PID=$!
    deadline=$((SECONDS + timeout))
    while (( SECONDS < deadline )); do
        kill -0 "$QA_APP_PID" 2>/dev/null || break
        if qa_boot_ready "$data"; then boot=1; break; fi
        sleep 1
    done
    # Catch immediate post-startup crashes as well as failures during startup.
    if [[ "$boot" == 1 ]]; then sleep 5; fi
    if kill -0 "$QA_APP_PID" 2>/dev/null; then alive=1; fi
    printf '\n%-25s | %-4s | %s\n' CHECK RESULT EVIDENCE
    qa_row 'App stayed running' 'owned PID through startup + 5s' test "$alive" = 1
    qa_row 'Startup ready' 'runner, engagement, Spotlight, readable storage' test "$boot" = 1
    qa_row 'Agent started' '[agent-start] runner ready in ...' qa_log_has '\[agent-start\] runner ready in ' "$data"/yansu-gui.log*
    qa_row 'Engagement started' '[engagement] service started' qa_log_has '\[engagement\] service started' "$data"/yansu-gui.log*
    qa_row 'Spotlight ready' '[spotlight] helper ready on 127.0.0.1:...' qa_log_has '\[spotlight\] helper ready on 127\.0\.0\.1:' "$data"/yansu-gui.log*
    qa_row 'Schema version' "sessions.db user_version = $expected" qa_schema_matches "$data/sessions.db" "$expected"
    qa_row 'Fresh onboarding' 'ui_state yansu.onboarding.state absent' qa_onboarding_fresh "$data/sessions.db"
    qa_row 'Telemetry default on' 'missing tier file (detailed default), or detailed' qa_telemetry_on "$data"
    qa_row 'Quit QA app' 'owned PID exited 0; no forced termination or shutdown watchdog' qa_stop_app
    # DiagnosticReports can arrive shortly after process exit.
    sleep 2
    qa_row 'No panic' 'app logs, rotated logs, stderr and launch output' qa_no_panic "$data" "$QA_EVIDENCE"
    qa_row 'No recent crash report' 'bundle/executable, last 10 minutes incl. shutdown' qa_crashes_clear "$HOME/Library/Logs/DiagnosticReports" "$bundle_id" "$executable" "$QA_EVIDENCE"
    printf '\nFailed checks: %d\n' "$QA_FAILURES"
    [[ "$QA_FAILURES" == 0 ]]
}

qa_logs_clear() {
    local pattern=$1 data=$2 evidence=$3 file
    [[ -s "$data/yansu-gui.log" ]] || return 1
    for file in "$data"/yansu-gui.log* "$data"/yansu-gui.stderr.log* "$evidence/launch.log"; do
        [[ ! -e "$file" || -r "$file" ]] || return 1
    done
    ! qa_log_has "$pattern" \
        "$data"/yansu-gui.log* "$data"/yansu-gui.stderr.log* "$evidence/launch.log"
}

qa_no_panic() {
    qa_logs_clear '(^|[[:space:]])(panic:|fatal error:|runtime: out of memory)' "$1" "$2"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then qa_main "$@"; fi
