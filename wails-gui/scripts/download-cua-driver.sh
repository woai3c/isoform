#!/bin/bash
set -euo pipefail

# Deprecation shim: the cua-driver bundle is now built from vendored
# Swift sources in wails-gui/cua-driver-src/ (forked from
# yetone/alma's computer-use helper) rather than downloaded from
# trycua/cua's GitHub releases. The old prebuilt path didn't ship
# VisualIndex / bgclick-rev, so we couldn't drive AX-empty Chinese /
# Electron apps without focus steal.
#
# This shim exists so any external script (CI, READMEs, dev notes)
# that still calls download-cua-driver.sh stays working. It just
# delegates to build-cua-driver-bundle.sh.

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
echo "[download-cua-driver] forwarding to build-cua-driver-bundle.sh (cua-driver is now built from vendored source)" >&2
exec "${SCRIPT_DIR}/build-cua-driver-bundle.sh" "$@"
