#!/bin/bash
# Read all parameters from temp files to avoid shell parsing issues
msg_file="$1"
agent_file="$2"
thinking_file="$3"
provider_file="$4"

# Validate that all required temp files exist
if [ ! -f "$msg_file" ] || [ ! -f "$agent_file" ] || [ ! -f "$thinking_file" ] || [ ! -f "$provider_file" ]; then
  echo "Error: Missing required parameter files" >&2
  exit 1
fi

	session_id="ctx-$$-$(date +%s)"
err_file=$(mktemp)
prompt_inline_limit={{PROMPT_INLINE_LIMIT}}
inline_message=1

prompt_bytes="$(wc -c < "$msg_file" 2>/dev/null || echo 0)"
if [ "$prompt_bytes" -ge "$prompt_inline_limit" ]; then
  inline_message=0
fi

# Execute openclaw with all parameters safely passed via file arguments
# For small prompts keep existing --message mode for compatibility.
# For large prompts, stream input from the temp file to avoid oversized argv values.
if [ "$inline_message" -eq 1 ]; then
  openclaw agent \
    --agent "$(cat "$agent_file")" \
    --session-id "$session_id" \
    --message "$(cat "$msg_file")" \
    --thinking "$(cat "$thinking_file")" \
    2>"$err_file"
else
  openclaw agent \
    --agent "$(cat "$agent_file")" \
    --session-id "$session_id" \
    --thinking "$(cat "$thinking_file")" \
    2>"$err_file" \
    < "$msg_file"
fi
status=$?

if [ $status -eq 0 ]; then
  rm -f "$err_file"
  exit 0
fi

# Check if fallback is needed based on error patterns
should_fallback=0
if grep -Fqi 'falling back to embedded' "$err_file"; then
  should_fallback=1
fi
if grep -Fqi 'message (--message)' "$err_file"; then
  should_fallback=1
fi
if grep -Fqi 'unsupported --message' "$err_file"; then
  should_fallback=1
fi
if grep -Fqi 'unknown flag' "$err_file" && grep -Fqi -- '--message' "$err_file"; then
  should_fallback=1
fi
if grep -Fqi 'unknown option' "$err_file" && grep -Fqi -- '--message' "$err_file"; then
  should_fallback=1
fi

if [ "$should_fallback" -eq 1 ]; then
  fallback_err=$(mktemp)

  # Try stdin fallback first
  openclaw agent \
    --agent "$(cat "$agent_file")" \
    --session-id "$session_id" \
    --thinking "$(cat "$thinking_file")" \
    2>"$fallback_err" \
    < "$msg_file"
  status=$?
  
  # If that fails, try with explicit --message flag again for small prompts.
  if [ "$status" -ne 0 ] && [ "$inline_message" -eq 1 ]; then
    openclaw agent \
      --agent "$(cat "$agent_file")" \
      --session-id "$session_id" \
      --message "$(cat "$msg_file")" \
      --thinking "$(cat "$thinking_file")" \
      2>"$fallback_err"
    status=$?
  fi
  
  if [ "$status" -ne 0 ]; then
    cat "$fallback_err" >&2
  fi
  rm -f "$err_file" "$fallback_err"
  exit $status
fi

cat "$err_file" >&2
rm -f "$err_file"
exit $status
