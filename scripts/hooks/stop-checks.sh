#!/usr/bin/env bash
set -euo pipefail

trim() {
  local value="$1"
  value="${value#"${value%%[![:space:]]*}"}"
  value="${value%"${value##*[![:space:]]}"}"
  printf '%s' "$value"
}

if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  exit 0
fi

repo_root="$(git rev-parse --show-toplevel)"
git_dir="$(git rev-parse --git-dir)"
cd "$repo_root"

status_output="$(git status --porcelain=v1 --untracked-files=all)"
if [[ -z "$status_output" ]]; then
  exit 0
fi

freshness_sec="${CHUM_STOP_HOOK_FRESHNESS_SEC:-60}"
if ! [[ "$freshness_sec" =~ ^[0-9]+$ ]]; then
  echo "stop-hook: CHUM_STOP_HOOK_FRESHNESS_SEC must be an integer (got: $freshness_sec)" >&2
  exit 2
fi

state_file="${CHUM_STOP_HOOK_STATE_FILE:-$git_dir/chum-stop-hook.state}"

declare -a checks=()
if [[ -n "${CHUM_STOP_HOOK_CHECKS:-}" ]]; then
  while IFS= read -r raw_line; do
    line="$(trim "$raw_line")"
    if [[ -n "$line" ]]; then
      checks+=("$line")
    fi
  done <<<"${CHUM_STOP_HOOK_CHECKS}"
else
  checks+=("go build ./...")
  checks+=("go vet ./...")
  checks+=("go test ./...")
fi

if [[ ${#checks[@]} -eq 0 ]]; then
  echo "stop-hook: no checks configured (set CHUM_STOP_HOOK_CHECKS)." >&2
  exit 2
fi

checks_blob="$(printf '%s\n' "${checks[@]}")"
fingerprint_input="$(printf 'status:\n%s\nchecks:\n%s\n' "$status_output" "$checks_blob")"
fingerprint="$(printf '%s' "$fingerprint_input" | git hash-object --stdin)"

last_success_epoch=0
last_success_fingerprint=""

if [[ -f "$state_file" ]]; then
  while IFS='=' read -r key value; do
    case "$key" in
      last_success_epoch)
        if [[ "$value" =~ ^[0-9]+$ ]]; then
          last_success_epoch="$value"
        fi
        ;;
      last_success_fingerprint)
        last_success_fingerprint="$value"
        ;;
    esac
  done <"$state_file"
fi

now_epoch="$(date +%s)"
age=$((now_epoch - last_success_epoch))
if [[ "$last_success_fingerprint" == "$fingerprint" && "$age" -ge 0 && "$age" -le "$freshness_sec" ]]; then
  echo "stop-hook: checks are fresh (${age}s <= ${freshness_sec}s); skipping rerun."
  exit 0
fi

echo "stop-hook: dirty worktree detected; running ${#checks[@]} check(s)."

for check_cmd in "${checks[@]}"; do
  echo "stop-hook: > $check_cmd"
  if ! bash -lc "$check_cmd"; then
    echo "stop-hook: check failed: $check_cmd" >&2
    echo "stop-hook: resolve failures before finishing this turn." >&2
    exit 1
  fi
done

tmp_state_file="${state_file}.tmp.$$"
mkdir -p "$(dirname "$state_file")"
{
  printf 'last_success_epoch=%s\n' "$now_epoch"
  printf 'last_success_fingerprint=%s\n' "$fingerprint"
} >"$tmp_state_file"
mv "$tmp_state_file" "$state_file"

echo "stop-hook: checks passed and cache updated."
