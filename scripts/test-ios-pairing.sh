#!/usr/bin/env bash
# Placeholder for a live pairing test. This release has no such test, so this
# script tests nothing. It explains what a future test needs and exits with 2.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
README_PATH="$(cd "$SCRIPT_DIR/.." && pwd)/ios/README.md"

cat >&2 <<'MESSAGE'
No live pairing test exists in this release, so nothing was tested.

A future relay-backed pairing test needs all of the following:
  1. A running relay that the test can reach over https, or over loopback http
     such as http://127.0.0.1:<port>.
  2. A Mac hub that has generated an invitation in Isoform desktop, under
     Settings > Phone > Pair an iPhone > Show pairing code.
     The invitation is an isoform://pair#... link (legacy yansu://pair# is accepted).
  3. That invitation exported as YANSU_PAIRING_INVITATION for the test run.
     It expires after 10 minutes. Treat it as a secret.
  4. A run that does not depend on notifications or APNs, so no APNs key or
     Apple push configuration is needed.

MESSAGE
echo "See $README_PATH for the pairing steps and current test coverage." >&2
exit 2
