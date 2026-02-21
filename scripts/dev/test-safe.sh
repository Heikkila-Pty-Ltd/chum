#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

LOCK_FILE="${TEST_SAFE_LOCK_FILE:-$ROOT/.tmp/go-test.lock}"
LOCK_WAIT_SEC="${TEST_SAFE_LOCK_WAIT_SEC:-120}"
GO_TEST_TIMEOUT="${TEST_SAFE_GO_TEST_TIMEOUT:-10m}"
JSON_OUT="${TEST_SAFE_JSON_OUT:-}"
BD_LOCK_CLEANUP_MINUTES="${TEST_SAFE_BD_LOCK_CLEANUP_MINUTES:-5}"
BD_LOCK_CLEANUP_DISABLED="${TEST_SAFE_BD_LOCK_CLEANUP_DISABLED:-0}"
BD_LOCK_CLEANUP_REQUIRE_FORCE="${TEST_SAFE_BD_LOCK_CLEANUP_REQUIRE_FORCE:-0}"

mkdir -p "$(dirname "$LOCK_FILE")"

args=("$@")
if [[ ${#args[@]} -eq 0 ]]; then
  args=("./...")
fi

exec 9>"$LOCK_FILE"
if ! flock -w "$LOCK_WAIT_SEC" 9; then
  echo "test-safe: failed to acquire lock within ${LOCK_WAIT_SEC}s: $LOCK_FILE" >&2
  echo "test-safe: if this is stale, check for lingering runners with:" >&2
  echo "  pgrep -af 'test-safe.sh|go test -json|go test ./internal'" >&2
  echo "  or stale lock file holders before retrying" >&2
  exit 73
fi

cmd=(go test -json -timeout "$GO_TEST_TIMEOUT" "${args[@]}")
echo "test-safe: running: ${cmd[*]}" >&2

if [[ -n "$JSON_OUT" ]]; then
  mkdir -p "$(dirname "$JSON_OUT")"
  "${cmd[@]}" | tee "$JSON_OUT"
else
  "${cmd[@]}"
fi
