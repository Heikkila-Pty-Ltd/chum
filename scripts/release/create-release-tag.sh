#!/usr/bin/env bash
set -euo pipefail

VERSION_FILE="VERSION"
annotate_message=""
dry_run=0
sign_tag=0
allow_dirty=0

usage() {
  cat <<'USAGE'
Usage:
  scripts/create-release-tag.sh [--message "..."] [--dry-run] [--sign] [--allow-dirty]

Creates annotated git tag from VERSION file in form vX.Y.Z.

Options:
  --message TEXT  Tag annotation message (default: "CHUM vX.Y.Z")
  --dry-run       Validate only; do not create tag
  --sign          Create signed tag (-s)
  --allow-dirty   Permit dirty working tree
  -h, --help      Show help
USAGE
}

log() {
  printf '[create-release-tag] %s\n' "$*"
}

die() {
  printf 'ERROR: %s\n' "$*" >&2
  exit 1
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "Missing required command: $1"
}

read_version() {
  [[ -f "$VERSION_FILE" ]] || die "VERSION file missing: $VERSION_FILE"
  local v
  v="$(tr -d '[:space:]' < "$VERSION_FILE")"
  [[ "$v" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || die "Invalid version in $VERSION_FILE: $v"
  printf '%s' "$v"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --message)
      [[ $# -ge 2 ]] || die "--message requires value"
      annotate_message="$2"
      shift 2
      ;;
    --dry-run)
      dry_run=1
      shift
      ;;
    --sign)
      sign_tag=1
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

require_cmd git

version="$(read_version)"
tag="v${version}"
[[ -n "$annotate_message" ]] || annotate_message="CHUM ${tag}"

if [[ "$allow_dirty" -ne 1 ]]; then
  git diff --quiet --ignore-submodules HEAD || die "Working tree is dirty. Commit/stash changes or pass --allow-dirty"
fi

git rev-parse --verify "$tag" >/dev/null 2>&1 && die "Tag already exists locally: $tag"
if git ls-remote --exit-code --tags origin "refs/tags/$tag" >/dev/null 2>&1; then
  die "Tag already exists on remote: $tag"
fi

log "tag=$tag message=$annotate_message sign=$sign_tag dry_run=$dry_run"
if [[ "$dry_run" -eq 1 ]]; then
  log "dry-run passed"
  exit 0
fi

if [[ "$sign_tag" -eq 1 ]]; then
  git tag -s "$tag" -m "$annotate_message"
else
  git tag -a "$tag" -m "$annotate_message"
fi

log "created tag $tag"
printf '%s\n' "$tag"
