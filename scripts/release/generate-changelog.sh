#!/usr/bin/env bash
set -euo pipefail

OUT_MD="CHANGELOG.md"
OUT_JSON="release/changelog.json"
FROM_REF=""
TO_REF="HEAD"
MAX_MORSELS=50

usage() {
  cat <<'USAGE'
Usage: scripts/generate-changelog.sh [--from REF] [--to REF] [--out-md FILE] [--out-json FILE]

Generates deterministic changelog artifacts (Markdown + JSON) from git history and closed morsels.
USAGE
}

die() {
  printf 'ERROR: %s\n' "$*" >&2
  exit 1
}

log() {
  printf '[generate-changelog] %s\n' "$*"
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "Missing required command: $1"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --from)
      FROM_REF="$2"
      shift 2
      ;;
    --to)
      TO_REF="$2"
      shift 2
      ;;
    --out-md)
      OUT_MD="$2"
      shift 2
      ;;
    --out-json)
      OUT_JSON="$2"
      shift 2
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
require_cmd jq

if [[ -z "$FROM_REF" ]]; then
  FROM_REF="$(git describe --tags --abbrev=0 2>/dev/null || true)"
fi

range=""
if [[ -n "$FROM_REF" ]]; then
  range="${FROM_REF}..${TO_REF}"
else
  range="$TO_REF"
fi

mkdir -p "$(dirname "$OUT_JSON")"

start_date=""
if [[ -n "$FROM_REF" ]]; then
  start_date="$(git show -s --format=%cI "$FROM_REF" 2>/dev/null || true)"
fi

tmp_commits="$(mktemp)"
git log --no-merges --pretty=format:'%H%x09%s' "$range" > "$tmp_commits"

features="$(mktemp)"
bugfixes="$(mktemp)"
breaking="$(mktemp)"
deprecations="$(mktemp)"

while IFS=$'\t' read -r sha subject; do
  [[ -n "$sha" ]] || continue
  lower="$(printf '%s' "$subject" | tr '[:upper:]' '[:lower:]')"

  # Filter internal/non-user facing commit types.
  if [[ "$lower" =~ ^(chore|ci|test|docs|style|refactor)(\(|:).*$ ]]; then
    continue
  fi

  entry="- ${subject} (${sha:0:8})"

  if [[ "$lower" =~ breaking|! ]]; then
    printf '%s\n' "$entry" >> "$breaking"
  elif [[ "$lower" =~ deprecat ]]; then
    printf '%s\n' "$entry" >> "$deprecations"
  elif [[ "$lower" =~ ^(fix|bug|hotfix)(\(|:).*$ ]]; then
    printf '%s\n' "$entry" >> "$bugfixes"
  elif [[ "$lower" =~ ^(feat|feature)(\(|:).*$ ]]; then
    printf '%s\n' "$entry" >> "$features"
  else
    # Default bucket for user-facing but uncategorized changes.
    printf '%s\n' "$entry" >> "$features"
  fi
done < "$tmp_commits"

tmp_morsels="$(mktemp)"
if [[ -n "$start_date" ]]; then
  jq -r --arg start "$start_date" --argjson max "$MAX_MORSELS" '
    select(.status=="closed" and (.closed_at // "") != "")
    | select(.closed_at >= $start)
    | [.closed_at, .id, .title]
    | @tsv
  ' .morsels/issues.jsonl | sort -r | head -n "$MAX_MORSELS" > "$tmp_morsels"
else
  jq -r --argjson max "$MAX_MORSELS" '
    select(.status=="closed" and (.closed_at // "") != "")
    | [.closed_at, .id, .title]
    | @tsv
  ' .morsels/issues.jsonl | sort -r | head -n "$MAX_MORSELS" > "$tmp_morsels"
fi

version="$(tr -d '[:space:]' < VERSION 2>/dev/null || echo "0.0.0")"

tmp_md="$(mktemp)"
{
  echo "# Changelog"
  echo
  echo "## v${version}"
  echo
  printf 'Range: `%s`\n' "$range"
  echo

  echo "### Features"
  if [[ -s "$features" ]]; then
    sort "$features"
  else
    echo "- None"
  fi
  echo

  echo "### Bug Fixes"
  if [[ -s "$bugfixes" ]]; then
    sort "$bugfixes"
  else
    echo "- None"
  fi
  echo

  echo "### Breaking Changes"
  if [[ -s "$breaking" ]]; then
    sort "$breaking"
  else
    echo "- None"
  fi
  echo

  echo "### Deprecations"
  if [[ -s "$deprecations" ]]; then
    sort "$deprecations"
  else
    echo "- None"
  fi
  echo

  echo "### Closed Morsels"
  if [[ -s "$tmp_morsels" ]]; then
    while IFS=$'\t' read -r closed_at morsel_id title; do
      echo "- ${morsel_id}: ${title} (${closed_at})"
    done < "$tmp_morsels"
  else
    echo "- None"
  fi
} > "$tmp_md"

mv "$tmp_md" "$OUT_MD"

tmp_json="$(mktemp)"
jq -n \
  --arg version "$version" \
  --arg range "$range" \
  --arg from_ref "$FROM_REF" \
  --arg to_ref "$TO_REF" \
  --argjson features "$(jq -Rsc 'split("\n") | map(select(length>0))' "$features")" \
  --argjson bugfixes "$(jq -Rsc 'split("\n") | map(select(length>0))' "$bugfixes")" \
  --argjson breaking "$(jq -Rsc 'split("\n") | map(select(length>0))' "$breaking")" \
  --argjson deprecations "$(jq -Rsc 'split("\n") | map(select(length>0))' "$deprecations")" \
  --argjson closed_morsels "$(awk -F'\t' 'NF==3 {printf("{\"closed_at\":\"%s\",\"id\":\"%s\",\"title\":\"%s\"}\n", $1,$2,$3)}' "$tmp_morsels" | jq -s '.')" \
  '{
    version: $version,
    range: $range,
    from_ref: (if $from_ref=="" then null else $from_ref end),
    to_ref: $to_ref,
    categories: {
      features: $features,
      bugfixes: $bugfixes,
      breaking_changes: $breaking,
      deprecations: $deprecations
    },
    closed_morsels: $closed_morsels
  }' > "$tmp_json"

mv "$tmp_json" "$OUT_JSON"

rm -f "$tmp_commits" "$features" "$bugfixes" "$breaking" "$deprecations" "$tmp_morsels"

log "wrote $OUT_MD"
log "wrote $OUT_JSON"
