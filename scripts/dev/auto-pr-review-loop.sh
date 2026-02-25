#!/usr/bin/env bash
set -euo pipefail

: "${PR_NUMBER:?PR_NUMBER is required}"
: "${PR_BASE_REF:?PR_BASE_REF is required}"
: "${PR_HEAD_REF:?PR_HEAD_REF is required}"

MAX_ITERATIONS="${MAX_ITERATIONS:-3}"
AUTO_MERGE="${AUTO_MERGE:-false}"
REVIEW_AGENT="${REVIEW_AGENT:-codex}"
FIX_AGENT="${FIX_AGENT:-claude}"
ALLOW_CLI_AUTH_ONLY="${ALLOW_CLI_AUTH_ONLY:-false}"

if ! command -v gh >/dev/null 2>&1; then
  echo "auto-pr-review: gh CLI not found in PATH." >&2
  exit 2
fi

run_agent() {
  local agent="$1"
  local prompt_file="$2"
  local role="${3:-general}"
  local codex_model="${CODEX_MODEL:-}"

  if [[ "$role" == "review" && -n "${CODEX_REVIEW_MODEL:-}" ]]; then
    codex_model="${CODEX_REVIEW_MODEL}"
  fi
  if [[ "$role" == "fix" && -n "${CODEX_FIX_MODEL:-}" ]]; then
    codex_model="${CODEX_FIX_MODEL}"
  fi

  case "$agent" in
    codex)
      if [[ -z "${OPENAI_API_KEY:-}" && "${ALLOW_CLI_AUTH_ONLY}" != "true" ]]; then
        echo "auto-pr-review: OPENAI_API_KEY is required when REVIEW_AGENT/FIX_AGENT uses codex." >&2
        exit 2
      fi
      if ! command -v codex >/dev/null 2>&1; then
        echo "auto-pr-review: codex CLI not found in PATH." >&2
        exit 2
      fi
      if [[ -n "$codex_model" ]]; then
        codex exec --full-auto -m "$codex_model" <"$prompt_file"
      else
        codex exec --full-auto <"$prompt_file"
      fi
      ;;
    claude)
      if [[ -z "${ANTHROPIC_API_KEY:-}" && "${ALLOW_CLI_AUTH_ONLY}" != "true" ]]; then
        echo "auto-pr-review: ANTHROPIC_API_KEY is required when REVIEW_AGENT/FIX_AGENT uses claude." >&2
        exit 2
      fi
      if ! command -v claude >/dev/null 2>&1; then
        echo "auto-pr-review: claude CLI not found in PATH." >&2
        exit 2
      fi
      claude --print --output-format json --dangerously-skip-permissions <"$prompt_file" |
        jq -r '.result // empty'
      ;;
    gemini)
      if [[ -z "${GEMINI_API_KEY:-}" && -z "${GOOGLE_API_KEY:-}" && "${ALLOW_CLI_AUTH_ONLY}" != "true" ]]; then
        echo "auto-pr-review: GEMINI_API_KEY/GOOGLE_API_KEY is required when REVIEW_AGENT/FIX_AGENT uses gemini." >&2
        exit 2
      fi
      if ! command -v gemini >/dev/null 2>&1; then
        echo "auto-pr-review: gemini CLI not found in PATH." >&2
        exit 2
      fi
      gemini -p "" --yolo <"$prompt_file"
      ;;
    *)
      echo "auto-pr-review: unsupported agent '$agent' (expected codex, claude, or gemini)." >&2
      exit 2
      ;;
  esac
}

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

git fetch --no-tags origin "$PR_BASE_REF" "$PR_HEAD_REF"

state_root="${RUNNER_TEMP:-/tmp}/codex-auto-pr/${PR_NUMBER}"
mkdir -p "$state_root"

approved="false"

for ((iter=1; iter<=MAX_ITERATIONS; iter++)); do
  review_prompt_file="${state_root}/review-prompt-${iter}.txt"
  diff_file="${state_root}/diff-${iter}.patch"
  review_raw_file="${state_root}/review-raw-${iter}.txt"
  review_comment_file="${state_root}/review-comment-${iter}.md"

  git diff --no-color "origin/${PR_BASE_REF}...HEAD" >"$diff_file"

  cat >"$review_prompt_file" <<EOF
You are reviewing a GitHub pull request in repository: ${GITHUB_REPOSITORY:-local/repo}
PR number: ${PR_NUMBER}
Base branch: ${PR_BASE_REF}
Head branch: ${PR_HEAD_REF}
Iteration: ${iter}/${MAX_ITERATIONS}

Task:
1) Review ONLY the supplied git diff.
2) Do not modify any files in this review step.
3) Respond in plain text using this exact header format, then details:
APPROVED=true|false
BLOCKERS=<integer>
SUMMARY=<single-line summary>
DETAILS:
<markdown review content>

Review criteria:
- correctness and regressions
- missing tests
- merge blockers only
- keep comments concrete and actionable

If there are no blockers, set APPROVED=true and BLOCKERS=0.

BEGIN DIFF
$(cat "$diff_file")
END DIFF
EOF

  run_agent "$REVIEW_AGENT" "$review_prompt_file" "review" >"$review_raw_file"

  APPROVED="$(grep -E '^APPROVED=' "$review_raw_file" | tail -n1 | cut -d= -f2- || true)"
  BLOCKERS="$(grep -E '^BLOCKERS=' "$review_raw_file" | tail -n1 | cut -d= -f2- || true)"
  SUMMARY="$(grep -E '^SUMMARY=' "$review_raw_file" | tail -n1 | cut -d= -f2- || true)"
  if [[ -z "$APPROVED" ]]; then APPROVED="false"; fi
  if [[ -z "$BLOCKERS" || ! "$BLOCKERS" =~ ^[0-9]+$ ]]; then BLOCKERS="1"; fi
  if [[ -z "$SUMMARY" ]]; then SUMMARY="No summary provided"; fi

  {
    echo "## Auto PR Review (Iteration ${iter}/${MAX_ITERATIONS})"
    echo
    echo "**Summary:** ${SUMMARY}"
    echo
    awk 'found{print} /^DETAILS:/{found=1}' "$review_raw_file" | sed '1d'
  } >"$review_comment_file"

  gh pr review "$PR_NUMBER" --comment --body-file "$review_comment_file"

  if [[ "$APPROVED" == "true" || "$BLOCKERS" == "0" ]]; then
    approved="true"
    break
  fi

  fix_prompt_file="${state_root}/fix-prompt-${iter}.txt"
  cat >"$fix_prompt_file" <<EOF
You are an implementation agent applying PR review feedback.
Repository: ${GITHUB_REPOSITORY:-local/repo}
PR number: ${PR_NUMBER}
Iteration: ${iter}/${MAX_ITERATIONS}

Read review findings below and fix merge blockers only.

BEGIN REVIEW
$(cat "$review_raw_file")
END REVIEW

Requirements:
1) Apply fixes for merge blockers only.
2) Keep changes minimal and safe.
3) Run validation commands:
   - go test ./...
4) If tests fail, fix them and rerun.

When done, stop. Do not write explanations to stdout.
EOF

  run_agent "$FIX_AGENT" "$fix_prompt_file" "fix"

  if ! git diff --quiet; then
    git config user.name "github-actions[bot]"
    git config user.email "41898282+github-actions[bot]@users.noreply.github.com"
    git add -A
    if ! git diff --cached --quiet; then
      git commit -m "chore: auto-fix PR review blockers (iteration ${iter})"
      git push origin "HEAD:${PR_HEAD_REF}"
    fi
  fi
done

if [[ "$approved" == "true" ]]; then
  if [[ "$AUTO_MERGE" == "true" ]]; then
    gh pr merge "$PR_NUMBER" --auto --squash || true
  fi
  exit 0
fi

final_comment_file="${state_root}/final-comment.md"
cat >"$final_comment_file" <<EOF
## Auto PR Review Loop Stopped

Reached max iterations (${MAX_ITERATIONS}) without approval.

Next step: manual review is required.
EOF

gh pr comment "$PR_NUMBER" --body-file "$final_comment_file"
exit 0
