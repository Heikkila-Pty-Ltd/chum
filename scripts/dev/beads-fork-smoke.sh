#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT_DIR"

echo "==> Running beads fork scaffold tests"
echo "    Package: ./internal/beadsfork"
echo "    Contract tests: ${CHUM_BD_CONTRACT:-0} (set to 1 to enable real bd execution)"

go test ./internal/beadsfork -count=1 -v

echo "==> beads fork scaffold tests completed"
