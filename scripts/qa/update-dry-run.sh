#!/usr/bin/env bash
# Run only through the user's app-runner loop on a Mac. This wrapper is safe
# to syntax-check or invoke with --help on a cloud host.
set -euo pipefail
exec python3 "$(cd "$(dirname "$0")" && pwd)/update_dry_run.py" "$@"
