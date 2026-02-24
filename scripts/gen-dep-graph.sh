#!/bin/bash
# gen-dep-graph.sh — Generate internal package dependency graph in DOT format
# Usage: bash scripts/gen-dep-graph.sh | dot -Tsvg -o docs/deps/package-deps.svg
#        bash scripts/gen-dep-graph.sh | dot -Tpng -o docs/deps/package-deps.png
set -euo pipefail

MODULE="github.com/antigravity-dev/chum"

echo 'digraph deps {'
echo '  rankdir=LR;'
echo '  node [shape=box, style="rounded,filled", fillcolor="#e8f4fd", fontname="Inter"];'
echo '  edge [color="#666666", arrowsize=0.7];'

go list -json ./... 2>/dev/null | python3 -c "
import json, sys

data = sys.stdin.read()
objects = []
depth = 0; start = 0
for i, c in enumerate(data):
    if c == '{': depth += 1
    elif c == '}':
        depth -= 1
        if depth == 0:
            objects.append(json.loads(data[start:i+1]))
            start = i+1

module = '$MODULE'
for obj in objects:
    pkg = obj.get('ImportPath', '')
    if not pkg.startswith(module): continue
    short = pkg.replace(module + '/', '')
    imports = [i.replace(module + '/', '') for i in obj.get('Imports', []) if i.startswith(module)]

    # Color-code by layer
    color = '#e8f4fd'
    if 'cmd/' in short: color = '#fce4ec'
    elif 'temporal' in short: color = '#f3e5f5'
    elif 'store' in short or 'graph' in short: color = '#e8f5e9'
    elif 'api' in short: color = '#fff3e0'
    elif 'config' in short: color = '#fffde7'
    elif 'dispatch' in short: color = '#e0f2f1'
    elif 'git' in short: color = '#fce4ec'

    print(f'  \"{short}\" [label=\"{short}\", fillcolor=\"{color}\"];')
    for dep in imports:
        print(f'  \"{short}\" -> \"{dep}\";')
"

echo '}'
