#!/usr/bin/env bash
# End-to-end smoke test for ciblame: builds the binary, fabricates the
# deterministic demo archives, and asserts on real CLI output for every
# subcommand and both archive shapes (zip and extracted directory).
# No network, idempotent, finishes in seconds.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT

fail() {
  echo "SMOKE FAIL: $*" >&2
  exit 1
}

BIN="$WORKDIR/ciblame"

echo "1. build"
(cd "$ROOT" && go build -o "$BIN" ./cmd/ciblame) || fail "go build failed"

echo "2. version matches manifest"
"$BIN" --version | grep -qx "ciblame 0.1.0" || fail "--version mismatch"

echo "3. fabricate the demo archives (base + head, 4 minutes apart)"
(cd "$ROOT" && go run ./examples/make-demo-archive "$WORKDIR/demo" >/dev/null) \
  || fail "make-demo-archive failed"
[ -f "$WORKDIR/demo/base.zip" ] || fail "base.zip missing"
[ -f "$WORKDIR/demo/head.zip" ] || fail "head.zip missing"

echo "4. report renders a waterfall with gauges and overhead"
OUT="$("$BIN" report "$WORKDIR/demo/base.zip")"
echo "$OUT" | grep -q "ciblame report — base.zip" || fail "report header missing"
echo "$OUT" | grep -q "job build" || fail "build job missing"
echo "$OUT" | grep -q "job lint (ubuntu-latest)" || fail "matrix job missing"
echo "$OUT" | grep -q "Run unit tests" || fail "step row missing"
echo "$OUT" | grep -q "█" || fail "waterfall bar missing"
echo "$OUT" | grep -q "between-step overhead" || fail "overhead line missing"
echo "$OUT" | grep -q "ubuntu-24.04" || fail "runner image missing"

echo "5. JSON report is machine-readable and versioned"
JSON="$("$BIN" report --format json "$WORKDIR/demo/base.zip")"
echo "$JSON" | grep -q '"tool": "ciblame"' || fail "json envelope missing"
echo "$JSON" | grep -q '"schema_version": 1' || fail "schema version missing"
echo "$JSON" | grep -q '"name": "Run unit tests"' || fail "json step missing"

echo "6. slow ranks the test step first"
"$BIN" slow --top 3 "$WORKDIR/demo/base.zip" | sed -n 4p \
  | grep -q "Run unit tests" || fail "slow ranking wrong"

echo "7. diff names the culprit step first"
DIFF="$("$BIN" diff "$WORKDIR/demo/base.zip" "$WORKDIR/demo/head.zip")"
echo "$DIFF" | grep -q "+4m02s" || fail "total regression missing"
FIRST_STEP="$(echo "$DIFF" | grep -E '^  [~+-] ' | head -1)"
echo "$FIRST_STEP" | grep -q "Run unit tests" || fail "culprit not ranked first"
echo "$DIFF" | grep -q "+ Generate coverage report" || fail "added step missing"

echo "8. --fail-over gates with exit code 1"
"$BIN" diff --fail-over 10m "$WORKDIR/demo/base.zip" "$WORKDIR/demo/head.zip" >/dev/null \
  || fail "diff within budget should exit 0"
if "$BIN" diff --fail-over 60s "$WORKDIR/demo/base.zip" "$WORKDIR/demo/head.zip" >/dev/null 2>&1; then
  fail "diff over budget should exit 1"
fi

echo "9. --job filters and extracted directories load"
"$BIN" report --job lint "$WORKDIR/demo/base.zip" | grep -q "job lint" \
  || fail "--job filter broken"
EXTRACTED="$WORKDIR/extracted"
mkdir -p "$EXTRACTED/build"
printf '2026-07-01T10:00:00.0000000Z start\n2026-07-01T10:00:09.0000000Z end\n' \
  > "$EXTRACTED/build/1_Set up job.txt"
"$BIN" report "$EXTRACTED" | grep -q "9.0s" || fail "directory archive broken"

echo "10. usage errors exit 2"
set +e
"$BIN" report --format yaml "$WORKDIR/demo/base.zip" >/dev/null 2>&1
[ $? -eq 2 ] || fail "bad --format should exit 2"
set -e

echo "SMOKE OK"
