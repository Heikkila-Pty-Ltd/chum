#!/usr/bin/env bash
# Lint documentation for broken internal file references.
# Usage: scripts/dev/docs-lint.sh [docs_dir]
set -uo pipefail

ROOT="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
DOCS_DIR="${1:-$ROOT}"
EXIT_CODE=0
CHECKED=0
BROKEN=0

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
DIM='\033[0;90m'
NC='\033[0m'

echo "Scanning markdown files for broken internal references..."
echo ""

while IFS= read -r mdfile; do
    # Extract markdown links: [text](path) — skip URLs
    refs=$(grep -oP '\[.*?\]\(\K[^)]+' "$mdfile" 2>/dev/null || true)
    [ -z "$refs" ] && continue

    while IFS= read -r ref; do
        # Skip external URLs, anchors, and badge images
        case "$ref" in
            https://*|http://*|mailto:*|file://*) continue ;;
            \#*) continue ;;
        esac

        # Strip anchor fragments (path#section → path)
        target="${ref%%#*}"
        [ -z "$target" ] && continue

        CHECKED=$((CHECKED + 1))

        # Resolve relative to the markdown file's directory
        md_dir="$(dirname "$mdfile")"
        resolved="$md_dir/$target"

        if [ ! -e "$resolved" ] && [ ! -e "$ROOT/$target" ]; then
            echo -e "  ${RED}✗${NC} ${mdfile#$ROOT/}:  ${DIM}→${NC}  $ref"
            BROKEN=$((BROKEN + 1))
            EXIT_CODE=1
        fi
    done <<< "$refs"
done < <(find "$DOCS_DIR" -name '*.md' -not -path '*/.git/*' -not -path '*/_archived/*' -not -path '*/.beads/*' -not -path '*/.gemini/*' -not -path '*/.openclaw/*' -not -path '*/node_modules/*')

echo ""
echo -e "Checked ${GREEN}${CHECKED}${NC} references across markdown files."
if [ "$BROKEN" -gt 0 ]; then
    echo -e "${RED}Found ${BROKEN} broken reference(s).${NC}"
else
    echo -e "${GREEN}All references valid.${NC}"
fi

exit $EXIT_CODE
