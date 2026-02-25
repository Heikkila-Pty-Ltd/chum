#!/bin/bash
# Install git hooks for chum

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GIT_DIR="$(git rev-parse --git-dir)"

echo "Installing git hooks..."

cp "$SCRIPT_DIR/pre-commit" "$GIT_DIR/hooks/pre-commit"
chmod +x "$GIT_DIR/hooks/pre-commit"
cp "$SCRIPT_DIR/pre-push" "$GIT_DIR/hooks/pre-push"
chmod +x "$GIT_DIR/hooks/pre-push"

echo "✅ Pre-commit and pre-push hooks installed"
echo ""
echo "This hook prevents direct commits to master."
echo "Allowed branch patterns: feature/*, chore/*, fix/*, refactor/*."
echo "Approved hotfix override: CHUM_ALLOW_MASTER_HOTFIX=1 (approved production case only)."
echo "Pre-push enforces the same branch policy before CI."
echo "Optional Stop hook for agents: ./scripts/hooks/stop-checks.sh"
echo "Always work on feature branches!"
