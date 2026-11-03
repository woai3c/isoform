# Sourced by ship.sh. Release builds read the PostHog ingest settings from one
# file outside every checkout, so each per-release worktree builds with the same
# project. The file is parsed, never executed, and no value is printed.
#
#   export ISOFORM_DESKTOP_POSTHOG_KEY=...
#   export ISOFORM_DESKTOP_POSTHOG_HOST=https://us.i.posthog.com
#
# YANSU_DESKTOP_POSTHOG_* lines are read as a deprecated fallback.
# Mode 600, owned by the person building. See docs/product-telemetry.md.
release_posthog_env_file="${ISOFORM_RELEASE_POSTHOG_ENV:-$HOME/.config/isoform-release/posthog.env}"

# load_release_posthog_env exports both values as ISOFORM_DESKTOP_POSTHOG_*.
# On failure it names only the file and the missing setting, and returns 1.
load_release_posthog_env() {
    # The parsed values pass through assignments; keep them out of xtrace.
    case $- in
        *x*)
            { set +x; } 2>/dev/null
            _load_release_posthog_env
            local status=$?
            set -x
            return $status
            ;;
    esac
    _load_release_posthog_env
}

_load_release_posthog_env() {
    local file="$release_posthog_env_file" line name value perm key="" host="" legacy_key="" legacy_host=""
    local assignment='^[[:space:]]*(export[[:space:]]+)?((ISOFORM|YANSU)_DESKTOP_POSTHOG_(KEY|HOST))=(.*)$'
    local double_quoted='^"(.*)"$' single_quoted="^'(.*)'$"
    if [[ ! -f "$file" ]]; then
        echo "PostHog release settings not found: $file" >&2
        return 1
    fi
    if [[ ! -O "$file" ]]; then
        echo "$file must be owned by $(id -un)" >&2
        return 1
    fi
    perm="$(stat -f '%Lp' "$file")"
    if (( 8#$perm & 8#077 )); then
        echo "$file must not be readable by others: chmod 600 '$file'" >&2
        return 1
    fi
    while IFS= read -r line || [[ -n "$line" ]]; do
        [[ "$line" =~ $assignment ]] || continue
        name="${BASH_REMATCH[2]}"
        value="${BASH_REMATCH[5]%$'\r'}"
        if [[ "$value" =~ $double_quoted || "$value" =~ $single_quoted ]]; then
            value="${BASH_REMATCH[1]}"
        fi
        case "$name" in
            ISOFORM_DESKTOP_POSTHOG_KEY) key="$value" ;;
            ISOFORM_DESKTOP_POSTHOG_HOST) host="$value" ;;
            YANSU_DESKTOP_POSTHOG_KEY) legacy_key="$value" ;;
            YANSU_DESKTOP_POSTHOG_HOST) legacy_host="$value" ;;
        esac
    done < "$file"
    if [[ -z "$key" && -n "$legacy_key" ]] || [[ -z "$host" && -n "$legacy_host" ]]; then
        echo "$file uses deprecated YANSU_DESKTOP_POSTHOG_*; rename to ISOFORM_DESKTOP_POSTHOG_*" >&2
    fi
    key="${key:-$legacy_key}"
    host="${host:-$legacy_host}"
    if [[ -z "$key" ]]; then
        echo "$file does not set ISOFORM_DESKTOP_POSTHOG_KEY" >&2
        return 1
    fi
    if [[ -z "$host" ]]; then
        echo "$file does not set ISOFORM_DESKTOP_POSTHOG_HOST" >&2
        return 1
    fi
    if [[ "$host" != https://* ]]; then
        echo "ISOFORM_DESKTOP_POSTHOG_HOST in $file must be an https:// origin" >&2
        return 1
    fi
    # Only the Isoform names reach build-wails.sh, so a stale shell value
    # under the old names cannot conflict with the file.
    unset YANSU_DESKTOP_POSTHOG_KEY YANSU_DESKTOP_POSTHOG_HOST
    export ISOFORM_DESKTOP_POSTHOG_KEY="$key" ISOFORM_DESKTOP_POSTHOG_HOST="$host"
}
