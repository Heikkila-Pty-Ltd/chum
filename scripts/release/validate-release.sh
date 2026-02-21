#!/usr/bin/env bash
set -euo pipefail

VERSION_FILE="VERSION"
skip_build=0
skip_tests=0
allow_dirty=0

usage() {
  cat <<'USAGE'
Usage:
  scripts/validate-release.sh [--skip-build] [--skip-tests] [--allow-dirty]

Runs pre-release quality gates before tag creation.

Options:
  --skip-build   Skip `make build`
  --skip-tests   Skip test suite
  --allow-dirty  Permit dirty working tree
  -h, --help     Show help
USAGE
}

log() {
  printf '[validate-release] %s\n' "$*"
}

die() {
  printf 'ERROR: %s\n' "$*" >&2
  exit 1
}

run_gate() {
  local name="$1"
  shift
  log "gate:start $name"
  if "$@"; then
    log "gate:pass $name"
  else
    die "gate:fail $name"
  fi
}

check_version() {
  [[ -f "$VERSION_FILE" ]] || die "VERSION file missing: $VERSION_FILE"
  local v
  v="$(tr -d '[:space:]' < "$VERSION_FILE")"
  [[ "$v" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || die "Invalid version in VERSION file: $v"
  printf '%s' "$v"
}

check_tag_absent() {
  local version="$1"
  local tag="v${version}"
  git rev-parse --verify "$tag" >/dev/null 2>&1 && die "Tag already exists locally: $tag"
  if git ls-remote --exit-code --tags origin "refs/tags/$tag" >/dev/null 2>&1; then
    die "Tag already exists on remote: $tag"
  fi
}

check_api_security_gate() {
  # Accept either explicit runtime config or implementation/docs evidence.
  if rg -n "\\[api\\.security\\]" chum.toml >/dev/null 2>&1; then
    return 0
  fi
  rg -n "AllowedTokens|RequireLocalOnly|api-security" internal/config/config.go docs/api-security.md >/dev/null 2>&1
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --skip-build)
      skip_build=1
      shift
      ;;
    --skip-tests)
      skip_tests=1
      shift
      ;;
    --allow-dirty)
      allow_dirty=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      die "Unknown argument: $1"
      ;;
  esac
done

command -v git >/dev/null 2>&1 || die "Missing required command: git"

if [[ "$allow_dirty" -ne 1 ]]; then
  run_gate "clean_worktree" git diff --quiet --ignore-submodules HEAD
fi

version="$(check_version)"
run_gate "tag_not_exists" check_tag_absent "$version"

if [[ "$skip_build" -ne 1 ]]; then
  run_gate "build" make build
fi

if [[ "$skip_tests" -ne 1 ]]; then
  run_gate "tests" env GOCACHE=/tmp/go-build go test ./internal/graph ./internal/health ./internal/api
fi

run_gate "api_security_controls_present" check_api_security_gate

log "release validation complete for v${version}"
