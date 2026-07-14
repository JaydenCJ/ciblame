#!/usr/bin/env bash
# diff-gate.sh — use `ciblame diff --fail-over` as a local regression gate.
#
#   bash examples/diff-gate.sh <base-archive> <head-archive> [budget]
#
# Exits 0 when total job time grew by at most the budget (default 60s),
# 1 when the budget is breached — ready for a release checklist step.
set -euo pipefail

BASE="${1:?usage: diff-gate.sh <base-archive> <head-archive> [budget]}"
HEAD="${2:?usage: diff-gate.sh <base-archive> <head-archive> [budget]}"
BUDGET="${3:-60s}"

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# Prefer an installed ciblame; fall back to running from source.
if command -v ciblame >/dev/null 2>&1; then
  CIBLAME=(ciblame)
else
  CIBLAME=(go run "$ROOT/cmd/ciblame")
fi

echo "gate: job time may grow at most $BUDGET ($BASE → $HEAD)"
if "${CIBLAME[@]}" diff --fail-over "$BUDGET" "$BASE" "$HEAD"; then
  echo "gate: PASS"
else
  echo "gate: FAIL — inspect the top step above" >&2
  exit 1
fi
