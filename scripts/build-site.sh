#!/usr/bin/env bash
# Build the static website that GitHub Pages serves.
#
# The published site is the real Vue workspace compiled for a static host:
# the marketing page lives at /#/home, the docs at /#/docs, and the workspace
# runs in its explicitly labeled in-memory demo mode because there is no API.
#
# Usage: bash scripts/build-site.sh [base-path]
#   base-path defaults to $VITE_BASE, then to "/".
#   A project site uses "/<repository>/", e.g. "/My_DATABASE159/".
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

base="${1:-${VITE_BASE:-/}}"
[[ "$base" == /* ]] || base="/$base"
[[ "$base" == */ ]] || base="$base/"

npm run build -w @kestrel/client
VITE_BASE="$base" VITE_STATIC_DEMO=true npm run build -w @kestrel/dashboard
touch apps/dashboard/dist/.nojekyll

echo "Static site built with base ${base} -> apps/dashboard/dist"
