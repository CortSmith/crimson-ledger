#!/usr/bin/env bash
# T20 PoC 4: every test ID (Xnnn) cited in the threat model must exist as a test name or catalogue row.
set -euo pipefail
cd "$(dirname "$0")/.."
bad=0
for id in $(grep -oE '\bX[0-9]{3}\b' docs/security/threat-model.md | sort -u); do
  if ! grep -rqE "func Test${id}|\"${id}\"|${id}" tests docs/tasks/T18-integration-suite.md docs/tasks/T12-ui-shell-preflight.md docs/tasks/T14-viewer-crimson-ledger.md 2>/dev/null; then
    echo "dangling: $id"; bad=1
  fi
  if ! grep -rqE "func Test${id}" tests 2>/dev/null; then echo "note: $id has no Go test (external task or open)"; fi
done
exit $bad
