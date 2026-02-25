#!/usr/bin/env bash
set -euo pipefail

POLL_INTERVAL_SEC="${POLL_INTERVAL_SEC:-60}"
STATE_FILE="${STATE_FILE:-.git/pr-review-watcher.state}"
MAX_ITERATIONS="${MAX_ITERATIONS:-3}"
AUTO_MERGE="${AUTO_MERGE:-false}"
REVIEW_AGENT="${REVIEW_AGENT:-codex}"
FIX_AGENT="${FIX_AGENT:-claude}"
ALLOW_CLI_AUTH_ONLY="${ALLOW_CLI_AUTH_ONLY:-true}"
PR_LABEL_FILTER="${PR_LABEL_FILTER:-}"
REPO="${REPO:-}"

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

if ! command -v gh >/dev/null 2>&1; then
  echo "pr-review-watcher: gh CLI not found in PATH." >&2
  exit 2
fi
if ! gh auth status >/dev/null 2>&1; then
  echo "pr-review-watcher: gh is not authenticated. Run 'gh auth login' first." >&2
  exit 2
fi

if [[ -z "$REPO" ]]; then
  REPO="$(gh repo view --json nameWithOwner --jq .nameWithOwner)"
fi

mkdir -p "$(dirname "$STATE_FILE")"
touch "$STATE_FILE"

state_get_sha() {
  local pr="$1"
  awk -F '\t' -v p="$pr" '$1 == p {print $2}' "$STATE_FILE" | tail -n1
}

state_upsert_sha() {
  local pr="$1"
  local sha="$2"
  local tmp="${STATE_FILE}.tmp.$$"
  awk -F '\t' -v p="$pr" '$1 != p' "$STATE_FILE" >"$tmp" || true
  printf '%s\t%s\n' "$pr" "$sha" >>"$tmp"
  mv "$tmp" "$STATE_FILE"
}

matches_label_filter() {
  local labels_csv="$1"
  if [[ -z "$PR_LABEL_FILTER" ]]; then
    return 0
  fi
  IFS=',' read -r -a required <<<"$PR_LABEL_FILTER"
  for label in "${required[@]}"; do
    local trimmed
    trimmed="$(echo "$label" | xargs)"
    if [[ -z "$trimmed" ]]; then
      continue
    fi
    if [[ ",$labels_csv," != *",$trimmed,"* ]]; then
      return 1
    fi
  done
  return 0
}

echo "pr-review-watcher: watching $REPO every ${POLL_INTERVAL_SEC}s"
echo "pr-review-watcher: agents review=${REVIEW_AGENT} fix=${FIX_AGENT}, auto_merge=${AUTO_MERGE}"

while true; do
  mapfile -t prs < <(
    gh pr list \
      --repo "$REPO" \
      --state open \
      --limit 100 \
      --json number,headRefOid,headRefName,baseRefName,isDraft,labels \
      --jq '.[] | select(.isDraft|not) |
      "\(.number)\t\(.headRefOid)\t\(.baseRefName)\t\(.headRefName)\t\([.labels[].name] | join(","))"'
  )

  for row in "${prs[@]}"; do
    IFS=$'\t' read -r pr sha base_ref head_ref labels_csv <<<"$row"
    if ! matches_label_filter "$labels_csv"; then
      continue
    fi

    last_sha="$(state_get_sha "$pr")"
    if [[ "$sha" == "$last_sha" ]]; then
      continue
    fi

    echo "pr-review-watcher: triggering PR #$pr ($head_ref @ $sha)"
    if PR_NUMBER="$pr" \
      PR_BASE_REF="$base_ref" \
      PR_HEAD_REF="$head_ref" \
      MAX_ITERATIONS="$MAX_ITERATIONS" \
      AUTO_MERGE="$AUTO_MERGE" \
      REVIEW_AGENT="$REVIEW_AGENT" \
      FIX_AGENT="$FIX_AGENT" \
      ALLOW_CLI_AUTH_ONLY="$ALLOW_CLI_AUTH_ONLY" \
      GITHUB_REPOSITORY="$REPO" \
      ./scripts/dev/auto-pr-review-loop.sh; then
      state_upsert_sha "$pr" "$sha"
      echo "pr-review-watcher: completed PR #$pr"
    else
      echo "pr-review-watcher: loop failed for PR #$pr; will retry on next poll." >&2
    fi
  done

  sleep "$POLL_INTERVAL_SEC"
done
